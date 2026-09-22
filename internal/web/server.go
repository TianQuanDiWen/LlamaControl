package web

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"llama-control/internal/fsutil"
	"llama-control/internal/platform"
	"llama-control/internal/updater"
)

// Config 存储 Web 服务的运行参数
type Config struct {
	ServiceName string
	SwapPort    int
	ConfigFile  string
	LogDir      string
	Addr        string
}

// Server 封装 Web 服务器和路由
type Server struct {
	cfg         Config
	mux         *http.ServeMux
	proxy       *httputil.ReverseProxy
	directProxy *httputil.ReverseProxy
	httpServer  *http.Server
}

// ResolveWebAddr 校验并解析 Web 控制台的监听地址，确保不与 llama-swap 端口发生冲突。
// - 若用户显式指定 (-web-addr) 且与 swapPort 冲突，返回明确错误。
// - 若为默认地址冲突，自动将端口向上顺延避让 (例如 swapPort + 1)。
func ResolveWebAddr(rawAddr string, swapPort int, explicit bool) (string, error) {
	if rawAddr == "" {
		rawAddr = "127.0.0.1:11452"
	}

	host, portStr, err := net.SplitHostPort(rawAddr)
	if err != nil {
		if p, convErr := strconv.Atoi(strings.TrimSpace(rawAddr)); convErr == nil && p > 0 && p <= 65535 {
			host = "127.0.0.1"
			portStr = strconv.Itoa(p)
		} else {
			return "", fmt.Errorf("无效的监听地址格式 %q: %w", rawAddr, err)
		}
	}

	port, err := strconv.Atoi(portStr)
	if err != nil || port <= 0 || port > 65535 {
		return "", fmt.Errorf("无效的端口号 %q", portStr)
	}

	if port == swapPort {
		if explicit {
			return "", fmt.Errorf("Web 监听端口 (%d) 不能与 llama-swap 服务端口 (%d) 相同，请修改 -web-addr 或配置文件", port, swapPort)
		}
		port = swapPort + 1
		if port > 65535 {
			port = swapPort - 1
		}
		resolved := net.JoinHostPort(host, strconv.Itoa(port))
		fmt.Printf("[Web] 检测到默认 Web 端口与 Swap 服务端口 (%d) 冲突，已自动避让至: %s\n", swapPort, resolved)
		return resolved, nil
	}

	return net.JoinHostPort(host, strconv.Itoa(port)), nil
}

// NewServer 创建并初始化 Web 服务实例
func NewServer(cfg Config) *Server {
	if cfg.SwapPort <= 0 {
		cfg.SwapPort = 8080
	}
	if cfg.Addr == "" {
		cfg.Addr = "127.0.0.1:11452"
	}

	targetURL, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", cfg.SwapPort))
	swapProxy := httputil.NewSingleHostReverseProxy(targetURL)
	originalDirector := swapProxy.Director
	swapProxy.Director = func(req *http.Request) {
		originalDirector(req)
		// 剥离 /swap 前缀转发给本地 swap 服务
		relPath := strings.TrimPrefix(req.URL.Path, "/swap")
		if relPath == "" {
			relPath = "/"
		}
		req.URL.Path = relPath
		req.Host = targetURL.Host
	}
	swapProxy.ModifyResponse = func(resp *http.Response) error {
		// 如果返回了重定向路径，重写至 /swap/ 下保持单入口统一
		if loc := resp.Header.Get("Location"); loc != "" {
			if strings.HasPrefix(loc, "/") && !strings.HasPrefix(loc, "/swap/") {
				resp.Header.Set("Location", "/swap"+loc)
			}
		}
		return nil
	}

	directProxy := httputil.NewSingleHostReverseProxy(targetURL)
	directDirector := directProxy.Director
	directProxy.Director = func(req *http.Request) {
		directDirector(req)
		req.Host = targetURL.Host
	}

	s := &Server{
		cfg:         cfg,
		mux:         http.NewServeMux(),
		proxy:       swapProxy,
		directProxy: directProxy,
	}
	s.registerRoutes()
	return s
}

func (s *Server) registerRoutes() {
	// 1. 静态资源（根页面）
	fsHandler := AssetsHandler()
	s.mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" || r.URL.Path == "/index.html" {
			fsHandler.ServeHTTP(w, r)
			return
		}
		// 如果未命中其他路由且以 /swap 开头，进入反向代理
		if strings.HasPrefix(r.URL.Path, "/swap") {
			s.proxy.ServeHTTP(w, r)
			return
		}
		fsHandler.ServeHTTP(w, r)
	})

	// 2. 反向代理路由：将 /swap/* 剥离前缀转发至本地 swap 服务
	s.mux.HandleFunc("/swap", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/swap/ui", http.StatusFound)
	})
	s.mux.HandleFunc("/swap/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/swap/" {
			http.Redirect(w, r, "/swap/ui", http.StatusFound)
			return
		}
		s.proxy.ServeHTTP(w, r)
	})

	// 3. 直通透传路由：将 /v1/、/ui/ 及 swap 专有端点透明反代至本地 llama-swap 服务
	s.mux.HandleFunc("/v1/", func(w http.ResponseWriter, r *http.Request) {
		s.directProxy.ServeHTTP(w, r)
	})
	s.mux.HandleFunc("/ui/", func(w http.ResponseWriter, r *http.Request) {
		s.directProxy.ServeHTTP(w, r)
	})
	s.mux.HandleFunc("/ui", func(w http.ResponseWriter, r *http.Request) {
		s.directProxy.ServeHTTP(w, r)
	})
	s.mux.HandleFunc("/running", func(w http.ResponseWriter, r *http.Request) {
		s.directProxy.ServeHTTP(w, r)
	})
	s.mux.HandleFunc("/upstream/", func(w http.ResponseWriter, r *http.Request) {
		s.directProxy.ServeHTTP(w, r)
	})

	// 4. API 路由：LlamaControl 专属运维管理 API
	s.mux.HandleFunc("/api/status", s.handleStatus)
	s.mux.HandleFunc("/api/service/", s.handleServiceAction)
	s.mux.HandleFunc("/api/logs", s.handleLogs)
	s.mux.HandleFunc("/api/logs/clean", s.handleCleanLogs)
	s.mux.HandleFunc("/api/apps/check", s.handleCheckApps)
	s.mux.HandleFunc("/api/apps/update", s.handleUpdateApp)

	// 5. 其余未命中的 /api/ 请求（如 llama-swap 原生 /api/models、/api/profiles 等）无缝转发给 swap
	s.mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		s.directProxy.ServeHTTP(w, r)
	})
}

type AppInfo struct {
	Name          string `json:"name"`
	LocalVersion  string `json:"local_version"`
	RemoteVersion string `json:"remote_version"`
	Variant       string `json:"variant"`
	NeedsUpdate   bool   `json:"needs_update"`
}

type StatusResponse struct {
	Platform      string    `json:"platform"`
	Arch          string    `json:"arch"`
	ServiceName   string    `json:"service_name"`
	ServiceStatus string    `json:"service_status"`
	Running       bool      `json:"running"`
	Port          int       `json:"port"`
	ConfigFile    string    `json:"config_file"`
	Listening     bool      `json:"listening"`
	LogDir        string    `json:"log_dir"`
	Apps          []AppInfo `json:"apps"`
}

var (
	appCacheMu   sync.RWMutex
	appCacheList []AppInfo
	appCacheTime time.Time
	updateLock   sync.Mutex
)

// getCurrentAppInfos 仅从本地二进制探测与只读内存缓存中组装版本数据，绝不发起任何网络 I/O，毫秒级即时返回
func getCurrentAppInfos() []AppInfo {
	appCacheMu.RLock()
	cached := appCacheList
	appCacheMu.RUnlock()

	cacheMap := make(map[string]string, len(cached))
	for _, item := range cached {
		cacheMap[item.Name] = item.RemoteVersion
	}

	var result []AppInfo
	for _, app := range updater.ManagedApps {
		_, localVer, variant := updater.InspectLocalBinary(app)
		remoteVer := cacheMap[app.Name]
		needsUpdate := localVer != "" && localVer != "unknown" && remoteVer != "" && updater.CompareVersions(localVer, remoteVer) < 0
		result = append(result, AppInfo{
			Name:          app.Name,
			LocalVersion:  localVer,
			RemoteVersion: remoteVer,
			Variant:       variant,
			NeedsUpdate:   needsUpdate,
		})
	}
	return result
}

// getOrFetchAppVersions 按需读取或刷新云端版本信息：
// - 若 !force 且 15 分钟内已有缓存，直接复用当前缓存；
// - 若已超时或 force，向 GitHub 获取最新 Release 并刷新缓存。
func getOrFetchAppVersions(force bool) []AppInfo {
	appCacheMu.RLock()
	hasCache := len(appCacheList) > 0
	isFresh := time.Since(appCacheTime) < 15*time.Minute
	appCacheMu.RUnlock()

	if !force && hasCache && isFresh {
		return getCurrentAppInfos()
	}

	var fresh []AppInfo
	for _, app := range updater.ManagedApps {
		status := updater.InspectApp(app)
		remoteVer := status.Release.TagName
		fresh = append(fresh, AppInfo{
			Name:          app.Name,
			LocalVersion:  status.LocalVersion,
			RemoteVersion: remoteVer,
			Variant:       status.Variant,
			NeedsUpdate:   status.NeedsUpdate(),
		})
	}

	appCacheMu.Lock()
	appCacheList = fresh
	appCacheTime = time.Now()
	appCacheMu.Unlock()

	return fresh
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	status, _ := platform.ServiceStatus(s.cfg.ServiceName)
	running, _ := platform.ServiceRunning(s.cfg.ServiceName)
	listening, _ := platform.PortListening(s.cfg.SwapPort)

	// 本地状态查询与网络 I/O 彻底解耦：从本地探测与内存缓存中毫秒级返回
	apps := getCurrentAppInfos()

	resp := StatusResponse{
		Platform:      platform.PlatformName(),
		Arch:          runtime.GOARCH,
		ServiceName:   s.cfg.ServiceName,
		ServiceStatus: status,
		Running:       running,
		Port:          s.cfg.SwapPort,
		ConfigFile:    s.cfg.ConfigFile,
		Listening:     listening,
		LogDir:        s.cfg.LogDir,
		Apps:          apps,
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(resp)
}

func (s *Server) handleCheckApps(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	force := r.URL.Query().Get("force") == "true"
	apps := getOrFetchAppVersions(force)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok":   true,
		"apps": apps,
		"msg":  "已完成云端最新版本检查",
	})
}

type UpdateAppRequest struct {
	App   string `json:"app"`
	Force bool   `json:"force"`
}

func (s *Server) handleUpdateApp(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	if !updateLock.TryLock() {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":  false,
			"msg": "当前已有正在进行的升级任务，请稍候再试",
		})
		return
	}
	defer updateLock.Unlock()

	var req UpdateAppRequest
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}
	if req.App == "" {
		req.App = r.URL.Query().Get("app")
	}
	if r.URL.Query().Get("force") == "true" {
		req.Force = true
	}
	if req.App == "" {
		http.Error(w, "Missing app parameter", http.StatusBadRequest)
		return
	}

	result, err := updater.UpdateAppByName(req.App, s.cfg.ServiceName, req.Force)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err != nil {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":  false,
			"msg": err.Error(),
		})
		return
	}

	// 升级成功后，强制刷新版本缓存
	getOrFetchAppVersions(true)

	// 若二进制升级成功但服务拉起失败，如实返回 warning 状态
	if result != nil && result.ServiceError != "" {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"ok":              true,
			"warning":         true,
			"service_running": false,
			"result":          result,
			"msg":             fmt.Sprintf("%s 升级成功至 %s，但服务启动失败: %s。请在控制面板手动尝试启动服务。", result.AppName, result.NewVersion, result.ServiceError),
		})
		return
	}

	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok":              true,
		"warning":         false,
		"service_running": result != nil && result.ServiceRunning,
		"result":          result,
		"msg":             fmt.Sprintf("%s 升级成功并已就绪！(%s -> %s)", result.AppName, result.OldVersion, result.NewVersion),
	})
}

func (s *Server) handleServiceAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	action := strings.TrimPrefix(r.URL.Path, "/api/service/")
	var err error
	var msg string

	switch action {
	case "start":
		err = platform.StartService(s.cfg.ServiceName)
		msg = "服务启动指令已发送"
	case "stop":
		err = platform.StopService(s.cfg.ServiceName)
		msg = "服务停止指令已发送"
	case "restart":
		err = platform.RestartService(s.cfg.ServiceName)
		msg = "服务重启指令已发送"
	default:
		http.Error(w, "Unknown Action", http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err != nil {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "msg": err.Error()})
	} else {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "msg": msg})
	}
}

func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	logContent := s.readLatestLogs(150)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte(logContent))
}

func (s *Server) handleCleanLogs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	err := fsutil.CleanLogs(s.cfg.LogDir)
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if err != nil {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "msg": err.Error()})
	} else {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "msg": "日志清理已完成"})
	}
}

// readTailLines 从文件末尾倒序分块读取最多 maxLines 行，避免大日志文件导致内存溢出
func readTailLines(filePath string, maxLines int) (string, error) {
	if maxLines <= 0 {
		maxLines = 100
	}
	file, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer file.Close()

	stat, err := file.Stat()
	if err != nil {
		return "", err
	}
	fileSize := stat.Size()
	if fileSize == 0 {
		return "", nil
	}

	const chunkSize = int64(8192)
	var (
		offset    = fileSize
		buf       = make([]byte, chunkSize)
		collected []byte
	)

	for offset > 0 {
		readSize := chunkSize
		if offset < chunkSize {
			readSize = offset
		}
		offset -= readSize

		_, err := file.Seek(offset, io.SeekStart)
		if err != nil {
			return "", err
		}

		n, err := io.ReadFull(file, buf[:readSize])
		if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
			return "", err
		}

		// 安全合并 chunk 到头部，避免切片底层数组别名污染
		newCollected := make([]byte, n+len(collected))
		copy(newCollected, buf[:n])
		copy(newCollected[n:], collected)
		collected = newCollected

		// 统计当前所含行数
		trimmed := collected
		if len(trimmed) > 0 && trimmed[len(trimmed)-1] == '\n' {
			trimmed = trimmed[:len(trimmed)-1]
		}
		newlines := 0
		for i := 0; i < len(trimmed); i++ {
			if trimmed[i] == '\n' {
				newlines++
			}
		}
		lineCount := 0
		if len(trimmed) > 0 {
			lineCount = newlines + 1
		}
		if lineCount >= maxLines {
			break
		}
	}

	content := string(collected)
	lines := strings.Split(content, "\n")
	hasTrailingNewline := false
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
		hasTrailingNewline = true
	}

	if len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
	}
	res := strings.Join(lines, "\n")
	if hasTrailingNewline {
		res += "\n"
	}
	return res, nil
}

// readLatestLogs 读取 logDir 下最新日志文件的最后 N 行
func (s *Server) readLatestLogs(maxLines int) string {
	if s.cfg.LogDir == "" {
		return "未配置日志目录"
	}
	entries, err := os.ReadDir(s.cfg.LogDir)
	if err != nil {
		return fmt.Sprintf("无法读取日志目录: %v", err)
	}

	var logFiles []os.FileInfo
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".log") {
			continue
		}
		if info, err := entry.Info(); err == nil {
			logFiles = append(logFiles, info)
		}
	}
	if len(logFiles) == 0 {
		return "暂无日志文件"
	}

	// 按修改时间降序，取最新的一个日志
	sort.Slice(logFiles, func(i, j int) bool {
		return logFiles[i].ModTime().After(logFiles[j].ModTime())
	})

	latestPath := filepath.Join(s.cfg.LogDir, logFiles[0].Name())
	logs, err := readTailLines(latestPath, maxLines)
	if err != nil {
		return fmt.Sprintf("读取日志文件失败: %v", err)
	}
	if logs == "" {
		return "暂无日志内容"
	}
	return logs
}

// Start 启动 HTTP 监听
func (s *Server) Start() error {
	s.httpServer = &http.Server{
		Addr:         s.cfg.Addr,
		Handler:      s.mux,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 30 * time.Second,
	}
	fmt.Printf("[Web] 控制面板正在监听: http://%s\n", s.cfg.Addr)
	fmt.Printf("[Web] Swap 代理路由已就绪: http://%s/swap/ -> 127.0.0.1:%d\n", s.cfg.Addr, s.cfg.SwapPort)
	return s.httpServer.ListenAndServe()
}

// Stop 优雅关闭 HTTP 服务
func (s *Server) Stop(ctx context.Context) error {
	if s.httpServer != nil {
		return s.httpServer.Shutdown(ctx)
	}
	return nil
}


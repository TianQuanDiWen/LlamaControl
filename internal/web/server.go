package web

import (
	"context"
	"encoding/json"
	"fmt"
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

// getOrFetchAppVersions 按需读取版本信息：
// - 本地版本：每次实时本地快速探测；
// - 云端版本：若在 15 分钟内且 !force，使用缓存；若已超时或 force，向 GitHub 获取最新 Release 并刷新缓存。
func getOrFetchAppVersions(force bool) []AppInfo {
	appCacheMu.RLock()
	cached := appCacheList
	updatedAt := appCacheTime
	appCacheMu.RUnlock()

	now := time.Now()
	useCache := !force && len(cached) > 0 && now.Sub(updatedAt) < 15*time.Minute

	if useCache {
		var result []AppInfo
		for _, item := range cached {
			for _, app := range updater.ManagedApps {
				if app.Name == item.Name {
					_, localVer, variant := updater.InspectLocalBinary(app)
					needsUpdate := localVer != "" && localVer != "unknown" && item.RemoteVersion != "" && updater.CompareVersions(localVer, item.RemoteVersion) < 0
					result = append(result, AppInfo{
						Name:          app.Name,
						LocalVersion:  localVer,
						RemoteVersion: item.RemoteVersion,
						Variant:       variant,
						NeedsUpdate:   needsUpdate,
					})
					break
				}
			}
		}
		return result
	}

	// 缓存过期或显式强制刷新，向 GitHub 请求最新版本
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
	appCacheTime = now
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

	// 获取受管应用状态 (按需懒加载，15分钟缓存避免GitHub限流)
	apps := getOrFetchAppVersions(false)

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
	apps := getOrFetchAppVersions(true)
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

	err := updater.UpdateAppByName(req.App, s.cfg.ServiceName, req.Force)
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
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok":  true,
		"msg": fmt.Sprintf("%s 升级成功并已就绪！", req.App),
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
	data, err := os.ReadFile(latestPath)
	if err != nil {
		return fmt.Sprintf("读取日志文件失败: %v", err)
	}

	lines := strings.Split(string(data), "\n")
	if len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
	}
	return strings.Join(lines, "\n")
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


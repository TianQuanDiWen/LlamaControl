//go:build windows

package platform

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"

	"llama-control/internal/fsutil"
)

// IsSupported 判断当前平台是否完全支持服务及管理操作
func IsSupported() bool         { return true }

// PlatformName 返回当前系统的展示名称
func PlatformName() string      { return "Windows" }

// RequiresElevation 判断在当前平台执行服务管理是否通常需要请求提权（管理员权限）
func RequiresElevation() bool   { return true }

// ConfigureConsole 配置 Windows 终端以支持 UTF-8 (65001) 编码，防止中文乱码
func ConfigureConsole()         { _ = windows.SetConsoleOutputCP(65001); _ = windows.SetConsoleCP(65001) }

// IsElevated 检测当前进程是否已经拥有管理员权限
func IsElevated() bool          { return exec.Command("cmd", "/C", "net session >nul 2>&1").Run() == nil }

// RelaunchElevated 请求 UAC 提权并以管理员身份重新运行当前可执行文件
func RelaunchElevated() error {
	exePath, err := os.Executable()
	if err != nil {
		return err
	}
	quoted := strings.ReplaceAll(exePath, "'", "''")
	return exec.Command("powershell", "-NoProfile", "-Command", "Start-Process '"+quoted+"' -Verb RunAs").Run()
}

// StartService 启动指定的 Windows 系统服务
func StartService(name string) error { return StreamCommand("net", "start", name) }

// StopService 停止指定的 Windows 系统服务
func StopService(name string) error  { return StreamCommand("net", "stop", name) }

// RestartService 重启指定的 Windows 系统服务
func RestartService(name string) error {
	_ = StopService(name)
	time.Sleep(2 * time.Second)
	return StartService(name)
}

// ServiceStatus 查询 Windows 系统服务的当前运行状态 (如 Running/Stopped 等)
func ServiceStatus(name string) (string, error) {
	script := "$svc=Get-Service -Name '" + strings.ReplaceAll(name, "'", "''") + "' -ErrorAction SilentlyContinue; if($svc){$svc.Status}else{'未找到'}"
	return CommandOutput("powershell", "-NoProfile", "-Command", script)
}

// ServiceRunning 快速判定系统服务当前是否正处于 "Running" 状态
func ServiceRunning(name string) (bool, error) {
	status, err := ServiceStatus(name)
	return strings.EqualFold(strings.TrimSpace(status), "Running"), err
}

// PortListening 检查指定的 TCP 本地端口是否正处于 Listen 状态
func PortListening(port int) (bool, error) {
	script := "$c=Get-NetTCPConnection -LocalPort " + strconv.Itoa(port) + " -State Listen -ErrorAction SilentlyContinue; if($c){'true'}else{'false'}"
	out, err := CommandOutput("powershell", "-NoProfile", "-Command", script)
	return strings.EqualFold(out, "true"), err
}



// SearchPath 在系统 PATH 环境变量中搜索给定的可执行文件名称，返回所有匹配项的绝对路径
func SearchPath(name string) ([]string, error) {
	out, err := CommandOutput("where", name)
	if err != nil {
		return nil, err
	}
	return strings.Fields(out), nil
}

// DefaultDirs 提供本平台下，搜索受管应用程序及其配置文件的常用候选目录
func DefaultDirs() []string {
	var dirs []string
	for _, env := range []string{"ProgramFiles", "ProgramFiles(x86)", "LOCALAPPDATA"} {
		if path := os.Getenv(env); path != "" {
			dirs = append(dirs, path)
		}
	}
	return dirs
}

// InstalledCUDAVariant 探测本机安装的 CUDA 版本 (如 cuda13, cuda12)，供推断匹配变体使用
func InstalledCUDAVariant() string {
	if path := os.Getenv("CUDA_PATH"); path != "" {
		lower := strings.ToLower(path)
		if strings.Contains(lower, "v13") {
			return "cuda13"
		}
		if strings.Contains(lower, "v12") {
			return "cuda12"
		}
	}
	for _, env := range os.Environ() {
		parts := strings.SplitN(env, "=", 2)
		key := strings.ToUpper(parts[0])
		if len(parts) > 1 && parts[1] != "" && strings.HasPrefix(key, "CUDA_PATH_V13") {
			return "cuda13"
		}
	}
	for _, env := range os.Environ() {
		parts := strings.SplitN(env, "=", 2)
		key := strings.ToUpper(parts[0])
		if len(parts) > 1 && parts[1] != "" && strings.HasPrefix(key, "CUDA_PATH_V12") {
			return "cuda12"
		}
	}

	programFiles := os.Getenv("ProgramFiles")
	if programFiles == "" {
		programFiles = `C:\Program Files`
	}
	entries, _ := os.ReadDir(filepath.Join(programFiles, "NVIDIA GPU Computing Toolkit", "CUDA"))
	best := ""
	for _, entry := range entries {
		name := strings.ToLower(entry.Name())
		if entry.IsDir() && strings.HasPrefix(name, "v13") {
			return "cuda13"
		}
		if entry.IsDir() && strings.HasPrefix(name, "v12") {
			best = "cuda12"
		}
	}
	return best
}

// ExecutableNames 根据基础程序名称展开出附带特定扩展名的实际可执行文件全名
func ExecutableNames(base string) []string { return []string{base + ".exe", base} }

// ---- Go 原生 Windows 服务守护与管理 ----

// RegisterService 使用 Windows 原生 Win32 API 直接向服务管理器创建服务，避免任何命令行转义问题
func RegisterService(name, displayName, exePath string, args ...string) error {
	m, err := mgr.Connect()
	if err != nil {
		return fmt.Errorf("连接系统服务控制管理器失败: %w", err)
	}
	defer m.Disconnect()

	s, err := m.OpenService(name)
	if err == nil {
		s.Close()
		return fmt.Errorf("服务 %s 已经存在", name)
	}

	s, err = m.CreateService(name, exePath, mgr.Config{
		DisplayName: displayName,
		StartType:   mgr.StartAutomatic,
		Description: "由 llama-control 自动注册的原生系统服务",
	}, args...)
	if err != nil {
		return fmt.Errorf("创建服务失败: %w", err)
	}
	defer s.Close()
	return nil
}

// UninstallService 通用注销系统服务（停止服务并从 Windows SCM 中彻底删除，对所有注册方式均有效）
func UninstallService(name string) error {
	_ = StopService(name)
	return StreamCommand("sc", "delete", name)
}

// RunAsService 将当前进程作为 Windows Service 运行，并托管运行 runFn 业务守护循环
func RunAsService(serviceName string, runFn func(ctx context.Context) error) error {
	isService, err := svc.IsWindowsService()
	if err != nil {
		return err
	}
	if !isService {
		return fmt.Errorf("当前不是在 Windows 服务环境下运行，请通过服务管理器启动")
	}
	return svc.Run(serviceName, &windowsServiceHandler{runFn: runFn})
}

type windowsServiceHandler struct {
	runFn func(ctx context.Context) error
}

func (h *windowsServiceHandler) Execute(args []string, r <-chan svc.ChangeRequest, changes chan<- svc.Status) (bool, uint32) {
	const cmdsAccepted = svc.AcceptStop | svc.AcceptShutdown
	changes <- svc.Status{State: svc.StartPending}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errChan := make(chan error, 1)
	go func() {
		errChan <- h.runFn(ctx)
	}()

	changes <- svc.Status{State: svc.Running, Accepts: cmdsAccepted}

	for {
		select {
		case err := <-errChan:
			if err != nil {
				changes <- svc.Status{State: svc.Stopped}
				return false, 1
			}
			changes <- svc.Status{State: svc.Stopped}
			return false, 0
		case req := <-r:
			switch req.Cmd {
			case svc.Interrogate:
				changes <- req.CurrentStatus
			case svc.Stop, svc.Shutdown:
				changes <- svc.Status{State: svc.StopPending}
				cancel()
				// 等待业务进程优雅退出（最多等 5 秒）
				select {
				case <-errChan:
				case <-time.After(5 * time.Second):
				}
				changes <- svc.Status{State: svc.Stopped}
				return false, 0
			}
		}
	}
}

// HandleServiceWorker 检查当前进程是否由 Windows 服务管理器唤醒或带有 --service-worker 标记
func HandleServiceWorker(serviceName string) bool {
	if isService, err := svc.IsWindowsService(); err == nil && isService {
		runServiceWorker(serviceName)
		return true
	}
	for _, arg := range os.Args[1:] {
		if arg == "--service-worker" || arg == "-service-worker" {
			runServiceWorker(serviceName)
			return true
		}
	}
	return false
}

// ManageService 封装 Windows 平台专属的服务查询、注销与原生 Win32 注册交互向导
func ManageService(reader *bufio.Reader, serviceName string, getAppInfo func() (swapPath, appDir string, port int)) {
	// 1. 优先通过系统底层查询服务是否已经存在
	status, err := ServiceStatus(serviceName)
	installed := err == nil && status != "" && status != "未找到"

	if installed {
		fmt.Printf("检测到系统服务 %s 已经存在（当前运行状态: %s）。\n", serviceName, status)
		fmt.Print("是否注销/卸载该服务? (y/n): ")
		choice, _ := reader.ReadString('\n')
		if strings.EqualFold(strings.TrimSpace(choice), "y") {
			if err := UninstallService(serviceName); err != nil {
				fmt.Println("卸载服务失败:", err)
			} else {
				fmt.Println("服务已成功注销并从系统服务列表中移除。")
			}
		} else {
			fmt.Println("已取消操作。")
		}
		return
	}

	// 2. 服务尚未注册，获取业务应用路径与端口
	swapPath, appDir, port := getAppInfo()
	if swapPath == "" {
		fmt.Println("未找到 llama-swap 可执行文件，请确保 llama-swap.exe 位于当前目录、bin/ 目录或系统 PATH 中。")
		return
	}

	fmt.Println("即将把 llama-swap 注册为 Windows 原生系统服务（零外部依赖，由本程序自守护）。")
	fmt.Printf("  服务名称: %s\n", serviceName)
	fmt.Printf("  程序路径: %s\n", swapPath)
	fmt.Printf("  监听端口: %d\n", port)
	fmt.Printf("  工作目录: %s\n", appDir)
	fmt.Print("是否继续? (y/n): ")
	choice, _ := reader.ReadString('\n')
	if !strings.EqualFold(strings.TrimSpace(choice), "y") {
		fmt.Println("已取消。")
		return
	}

	selfExe, err := os.Executable()
	if err != nil {
		fmt.Println("获取当前程序路径失败:", err)
		return
	}
	selfExe, _ = filepath.Abs(selfExe)

	if err := RegisterService(serviceName, "Llama Swap API", selfExe, "--service-worker"); err != nil {
		fmt.Println("注册服务失败:", err)
		return
	}
	fmt.Println("服务注册成功！正在启动...")
	if err := StartService(serviceName); err != nil {
		fmt.Println("启动服务失败:", err)
	} else {
		fmt.Println("服务已启动并设置为开机自动运行。")
	}
}

// runServiceWorker 在后台作为 Windows 服务守护进程运行，管理并自愈 llama-swap 子进程
func runServiceWorker(serviceName string) {
	err := RunAsService(serviceName, func(ctx context.Context) error {
		exeDir := ExecutableDir()
		candidates := []string{
			filepath.Join(exeDir, "llama-swap.exe"),
			filepath.Join(filepath.Dir(exeDir), "llama-swap.exe"),
		}
		for _, dir := range DefaultDirs() {
			candidates = append(candidates, filepath.Join(dir, "llama-swap.exe"))
		}
		if paths, err := SearchPath("llama-swap.exe"); err == nil {
			candidates = append(candidates, paths...)
		}

		var swapPath string
		for _, c := range candidates {
			if info, err := os.Stat(c); err == nil && !info.IsDir() {
				swapPath = c
				break
			}
		}
		if swapPath == "" {
			return fmt.Errorf("守护进程未找到 llama-swap.exe")
		}
		appDir := filepath.Dir(swapPath)

		logDir := filepath.Join(appDir, "logs")
		_ = os.MkdirAll(logDir, 0755)
		dailyLogger := &fsutil.DailyLogWriter{Dir: logDir, Prefix: "swap"}
		defer dailyLogger.Close()

		port := 8080
		for _, name := range []string{"config.yaml", "config.yml"} {
			if content, err := os.ReadFile(filepath.Join(appDir, name)); err == nil {
				for _, line := range strings.Split(string(content), "\n") {
					line = strings.TrimSpace(line)
					if strings.HasPrefix(strings.ToLower(line), "port:") {
						parts := strings.SplitN(line, ":", 2)
						if len(parts) > 1 {
							if p, err := strconv.Atoi(strings.Trim(strings.TrimSpace(parts[1]), `"'`)); err == nil && p > 0 {
								port = p
								break
							}
						}
					}
				}
			}
		}

		// 守护监控与自动重启循环
		for {
			select {
			case <-ctx.Done():
				return nil
			default:
			}

			cmd := exec.CommandContext(ctx, swapPath, "-config", "config.yaml", "-listen", fmt.Sprintf(":%d", port))
			cmd.Dir = appDir
			cmd.Stdout = dailyLogger
			cmd.Stderr = dailyLogger

			_ = cmd.Run()

			if ctx.Err() != nil {
				return nil // 收到服务停止信号，正常退出
			}

			// 进程异常闪退，等待 3 秒后自动自愈拉起
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(3 * time.Second):
			}
		}
	})
	if err != nil {
		fmt.Println("服务运行异常:", err)
	}
}



package platform

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"llama-control/internal/config"
	"llama-control/internal/fsutil"
)

// ExecutableDir 返回当前可执行文件所在的绝对目录路径
func ExecutableDir() string {
	path, err := os.Executable()
	if err != nil {
		return "."
	}
	dir, err := filepath.Abs(filepath.Dir(path))
	if err != nil {
		return filepath.Dir(path)
	}
	return dir
}

// StreamCommand 执行命令并将标准输出/标准错误重定向到当前终端
func StreamCommand(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd.Run()
}

// CommandOutput 执行命令并带 5 秒超时获取合并输出字符串
func CommandOutput(name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// ContainsAny 判断字符串是否包含任一 token（跨平台共用字符串工具）
func ContainsAny(value string, tokens ...string) bool {
	for _, token := range tokens {
		if token != "" && strings.Contains(value, token) {
			return true
		}
	}
	return false
}

// DetectLlamaCppVariant 根据文件路径、命令输出及平台环境探测 GPU / 硬件加速分支
func DetectLlamaCppVariant(path, output string) string {
	text := strings.ToLower(path + "\n" + output)
	switch {
	case ContainsAny(text, "cuda 13", "cuda13", "cuda-13"):
		return "cuda13"
	case ContainsAny(text, "cuda 12", "cuda12", "cuda-12", "cuda"):
		return "cuda12" // fallback to cuda12 if only "cuda" is found
	case strings.Contains(text, "metal"):
		return "metal"
	case strings.Contains(text, "vulkan"):
		return "vulkan"
	default:
		return ""
	}
}

// FormatVariant 友好格式化变体名称
func FormatVariant(variant string) string {
	switch strings.ToLower(variant) {
	case "cuda13":
		return "CUDA 13"
	case "cuda12":
		return "CUDA 12"
	case "metal":
		return "Metal"
	case "vulkan":
		return "Vulkan"
	default:
		return variant
	}
}

// HandleServiceWorker 检查当前进程是否由原生系统服务管理器唤醒或带有 --service-worker 标记
func HandleServiceWorker(serviceName string, hasWorkerFlag bool) bool {
	if isNativeService() || hasWorkerFlag {
		runServiceWorker(serviceName)
		return true
	}
	return false
}

// runServiceWorker 在后台作为守护进程运行，管理并自愈 llama-swap 子进程
func runServiceWorker(serviceName string) {
	err := RunAsService(serviceName, func(ctx context.Context) error {
		exeDir := ExecutableDir()
		var candidates []string
		for _, name := range ExecutableNames("llama-swap") {
			candidates = append(candidates, filepath.Join(exeDir, name))
			candidates = append(candidates, filepath.Join(filepath.Dir(exeDir), name))
			for _, dir := range DefaultDirs() {
				candidates = append(candidates, filepath.Join(dir, name))
			}
			if paths, err := SearchPath(name); err == nil {
				candidates = append(candidates, paths...)
			}
		}

		var swapPath string
		for _, c := range candidates {
			if info, err := os.Stat(c); err == nil && !info.IsDir() {
				swapPath = c
				break
			}
		}
		if swapPath == "" {
			return fmt.Errorf("守护进程未找到 llama-swap 执行文件")
		}
		appDir := filepath.Dir(swapPath)

		var configFile string
		var port int
		
		// 动态检测端口和配置文件
		if p, cfgName := config.DetectSwapPort(appDir); p > 0 {
			port = p
			configFile = cfgName
		} else {
			port = 8080
			configFile = "config.yaml"
		}

		// 动态检测日志目录，保持守护进程与CLI一致，且强制基准化为绝对路径
		logDir := filepath.Join(appDir, "logs")
		if dir, ok := config.DetectSwapLogDir(appDir, exeDir); ok && dir != "" {
			if filepath.IsAbs(dir) {
				logDir = dir
			} else {
				logDir = filepath.Join(appDir, dir)
			}
		}
		_ = os.MkdirAll(logDir, 0755)
		dailyLogger := &fsutil.DailyLogWriter{Dir: logDir, Prefix: "swap"}
		defer dailyLogger.Close()

		// 守护监控与自动重启循环
		for {
			select {
			case <-ctx.Done():
				return nil
			default:
			}

			cmd := exec.CommandContext(ctx, swapPath, "-config", configFile, "-listen", fmt.Sprintf(":%d", port))
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

// ManageService 封装全平台共用的服务查询、注销与原生服务注册交互向导
func ManageService(reader *bufio.Reader, serviceName string, getAppInfo func() (swapPath, appDir string, port int, configFile string)) {
	if !IsSupported() {
		fmt.Printf("%s 平台暂不支持原生服务管理。\n", PlatformName())
		return
	}

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
	swapPath, appDir, port, configFile := getAppInfo()
	if swapPath == "" {
		fmt.Println("未找到 llama-swap 可执行文件，请确保执行文件位于当前目录、bin/ 目录或系统 PATH 中。")
		return
	}

	fmt.Printf("即将把 llama-swap 注册为 %s 原生系统服务（零外部依赖，由本程序自守护）。\n", PlatformName())
	fmt.Printf("  服务名称: %s\n", serviceName)
	fmt.Printf("  程序路径: %s\n", swapPath)
	fmt.Printf("  配置读取: %s\n", configFile)
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

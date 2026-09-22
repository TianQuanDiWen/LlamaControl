package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"llama-control/internal/config"
	"llama-control/internal/fsutil"
	"llama-control/internal/platform"
	"llama-control/internal/updater"
	"llama-control/internal/web"
)

var (
	serviceName     = "llama-swap"
	isWorker        bool
	runWeb          bool
	webAddr         string
	webServerActive bool
	activeWebAddr   string
)

func init() {
	if envName := os.Getenv("LLAMA_SERVICE_NAME"); envName != "" {
		serviceName = envName
	}
	flag.StringVar(&serviceName, "service", serviceName, "Service name")
	flag.BoolVar(&isWorker, "service-worker", false, "以守护进程 Worker 模式运行")
	flag.BoolVar(&runWeb, "web", false, "以 Web 控制面板模式运行")
	flag.StringVar(&webAddr, "web-addr", "127.0.0.1:11452", "Web 控制面板监听地址")
}

func isWebAddrExplicit() bool {
	explicit := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "web-addr" {
			explicit = true
		}
	})
	return explicit
}

func main() {
	flag.Parse()

	webHook := func(ctx context.Context, swapPort int, configFile, logDir string) {
		resolvedAddr, err := web.ResolveWebAddr(webAddr, swapPort, isWebAddrExplicit())
		if err != nil {
			fmt.Println("[Web] 无法启动伴随 Web 服务:", err)
			return
		}
		server := web.NewServer(web.Config{
			ServiceName: serviceName,
			SwapPort:    swapPort,
			ConfigFile:  configFile,
			LogDir:      logDir,
			Addr:        resolvedAddr,
		})
		go func() {
			<-ctx.Done()
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			_ = server.Stop(shutdownCtx)
		}()
		if err := server.Start(); err != nil && err != http.ErrServerClosed {
			fmt.Println("[Web] 伴随 Web 控制面板退出:", err)
		}
	}

	if platform.HandleServiceWorker(serviceName, isWorker, webHook) {
		return
	}

	if runWeb {
		port, configFile := detectSwapPort()
		logDir := getLogDir(platform.ExecutableDir())
		resolvedAddr, err := web.ResolveWebAddr(webAddr, port, isWebAddrExplicit())
		if err != nil {
			fmt.Println("Web 地址配置错误:", err)
			return
		}
		server := web.NewServer(web.Config{
			ServiceName: serviceName,
			SwapPort:    port,
			ConfigFile:  configFile,
			LogDir:      logDir,
			Addr:        resolvedAddr,
		})
		if err := server.Start(); err != nil && err != http.ErrServerClosed {
			fmt.Println("启动 Web 服务失败:", err)
		}
		return
	}

	platform.ConfigureConsole()
	if !platform.IsSupported() {
		fmt.Printf("当前系统平台 (%s) 暂未完全支持服务管理；更新核心已支持扩展该平台。\n", platform.PlatformName())
		return
	}
	if platform.RequiresElevation() && !platform.IsElevated() {
		if err := platform.RelaunchElevated(); err != nil {
			fmt.Println("请求管理员权限失败:", err)
		}
		return
	}
	showMenu()
}

// showMenu 打印交互式控制台主菜单并处理用户输入
func showMenu() {
	reader := bufio.NewReader(os.Stdin)
	for {
		fmt.Println("========================================")
		fmt.Printf("   llama-swap 服务器管理工具 (%s)\n", platform.PlatformName())
		fmt.Println("========================================")
		fmt.Println("  [1] 启动服务")
		fmt.Println("  [2] 停止服务")
		fmt.Println("  [3] 重启服务")
		fmt.Println("  [4] 查看状态")
		fmt.Println("  [5] 清理日志")
		fmt.Println("  [6] 检查并更新 llama.cpp / llama-swap")
		fmt.Println("  [7] 注册/卸载系统服务")
		if webServerActive {
			fmt.Printf("  [8] 查看 Web 监控面板 (http://%s) [运行中]\n", activeWebAddr)
		} else {
			fmt.Printf("  [8] 启动 Web 监控面板 (http://%s)\n", webAddr)
		}
		fmt.Println("  [0] 退出")
		fmt.Print("\n请选择操作 (0-8): ")
		choice, err := reader.ReadString('\n')
		if err != nil {
			fmt.Println("读取输入失败:", err)
			return
		}
		switch strings.TrimSpace(choice) {
		case "1":
			runServiceAction("启动", platform.StartService)
		case "2":
			runServiceAction("停止", platform.StopService)
		case "3":
			runServiceAction("重启", platform.RestartService)
		case "4":
			showStatus()
		case "5":
			cleanLogs()
		case "6":
			updater.UpdateManagedApps(reader, serviceName)
		case "7":
			manageService(reader)
		case "8":
			startWebDashboard(reader)
		case "0":
			return
		default:
			fmt.Println("无效输入，请重新选择。")
			time.Sleep(time.Second)
		}
	}
}

// startWebDashboard 在后台协程启动 Web 控制面板与反代，并在终端给出提示
func startWebDashboard(reader *bufio.Reader) {
	if webServerActive {
		fmt.Println("\n========================================")
		fmt.Printf("  Web 控制面板已在后台运行中！\n")
		fmt.Printf("  监控管理主页: http://%s\n", activeWebAddr)
		fmt.Printf("  Swap 代理入口: http://%s/swap/ui\n", activeWebAddr)
		fmt.Println("========================================")
		waitForEnter()
		return
	}

	port, configFile := detectSwapPort()
	logDir := getLogDir(platform.ExecutableDir())
	resolvedAddr, err := web.ResolveWebAddr(webAddr, port, isWebAddrExplicit())
	if err != nil {
		fmt.Println("Web 地址配置错误:", err)
		waitForEnter()
		return
	}
	server := web.NewServer(web.Config{
		ServiceName: serviceName,
		SwapPort:    port,
		ConfigFile:  configFile,
		LogDir:      logDir,
		Addr:        resolvedAddr,
	})
	webServerActive = true
	activeWebAddr = resolvedAddr

	go func() {
		if err := server.Start(); err != nil && err != http.ErrServerClosed {
			webServerActive = false
			fmt.Println("Web 监控面板运行异常:", err)
		}
	}()

	time.Sleep(100 * time.Millisecond)
	fmt.Println("\n========================================")
	fmt.Printf("  Web 控制面板已启动！\n")
	fmt.Printf("  监控管理主页: http://%s\n", resolvedAddr)
	fmt.Printf("  Swap 代理入口: http://%s/swap/ui\n", resolvedAddr)
	fmt.Println("========================================")
	waitForEnter()
}

// runServiceAction 包装服务相关的操作，统一步骤提示和错误处理
func runServiceAction(action string, fn func(string) error) {
	fmt.Printf("正在%s %s...\n", action, serviceName)
	if err := fn(serviceName); err != nil {
		fmt.Printf("%s失败: %v\n", action, err)
	} else {
		fmt.Printf("%s完成。\n", action)
	}
	waitForEnter()
}

// showStatus 获取并显示系统服务的当前运行状态以及端口的监听情况
func showStatus() {
	status, err := platform.ServiceStatus(serviceName)
	if err != nil {
		fmt.Println("无法查询服务状态:", err)
	} else {
		fmt.Printf("服务 %s: %s\n", serviceName, status)
	}

	port, _ := detectSwapPort()
	listening, err := platform.PortListening(port)
	if err != nil {
		fmt.Println("无法查询端口状态:", err)
	} else if listening {
		fmt.Printf("端口 %d 已监听 [正常]\n", port)
	} else {
		fmt.Printf("端口 %d 未监听 [异常]\n", port)
	}
	waitForEnter()
}

// detectSwapPort 根据可执行文件所在路径自动探测配置文件的监听端口
func detectSwapPort() (int, string) {
	dirs := []string{platform.ExecutableDir()}
	for _, app := range updater.ManagedApps {
		if app.Name == "llama-swap" {
			if path, _, _ := updater.InspectLocalBinary(app); path != "" {
				dirs = append(dirs, filepath.Dir(path))
			}
			break
		}
	}
	return config.DetectSwapPort(dirs...)
}

func getLogDir(exeDir string) string {
	dirs := append([]string{filepath.Dir(exeDir)}, platform.DefaultDirs()...)
	if dir, ok := config.DetectSwapLogDir(dirs...); ok && dir != "" {
		if filepath.IsAbs(dir) {
			return dir
		}
		return filepath.Join(exeDir, dir)
	}
	return filepath.Join(exeDir, "logs")
}

// cleanLogs 调用平台相关实现去清理服务日志文件
func cleanLogs() {
	exeDir := platform.ExecutableDir()
	logDir := getLogDir(exeDir)
	fmt.Printf("准备清理日志目录: %s\n", logDir)
	if err := fsutil.CleanLogs(logDir); err != nil {
		fmt.Println("清理日志失败:", err)
	}
	waitForEnter()
}

// manageService 委派底层平台抽象层处理系统服务的查询、注销与注册流程
func manageService(reader *bufio.Reader) {
	platform.ManageService(reader, serviceName, func() (string, string, int, string) {
		var swapPath string
		for _, app := range updater.ManagedApps {
			if app.Name == "llama-swap" {
				swapPath, _, _ = updater.InspectLocalBinary(app)
				break
			}
		}
		if swapPath != "" {
			appDir := filepath.Dir(swapPath)
			port, configFile := config.DetectSwapPort(appDir)
			return swapPath, appDir, port, configFile
		}
		return "", "", 0, "config.yaml"
	})
	waitForEnter()
}

// waitForEnter 暂停程序执行直到用户按下回车键，防止控制台窗口闪退
func waitForEnter() {
	fmt.Print("按 Enter 键继续...")
	_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
}

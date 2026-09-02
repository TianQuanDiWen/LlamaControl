package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"llama-control/internal/config"
	"llama-control/internal/platform"
	"llama-control/internal/updater"
)

var (
	serviceName = "llama-swap"
)

func init() {
	if envName := os.Getenv("LLAMA_SERVICE_NAME"); envName != "" {
		serviceName = envName
	}
	flag.StringVar(&serviceName, "service", serviceName, "Service name")
}

func main() {
	flag.Parse()
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
		fmt.Println("  [0] 退出")
		fmt.Print("\n请选择操作 (0-6): ")
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
		case "0":
			return
		default:
			fmt.Println("无效输入，请重新选择。")
			time.Sleep(time.Second)
		}
	}
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

	port := detectSwapPort()
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
func detectSwapPort() int {
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

// cleanLogs 调用平台相关实现去清理服务日志文件
func cleanLogs() {
	var logDir string
	exeDir := platform.ExecutableDir()
	dirs := append([]string{filepath.Dir(exeDir)}, platform.DefaultDirs()...)
	if dir, ok := config.DetectSwapLogDir(dirs...); ok && dir != "" {
		logDir = dir
		fmt.Printf("从配置文件中读取到日志路径: %s\n", logDir)
	} else {
		// 回退到程序所在目录的 logs 文件夹（绿色便携模式）
		logDir = filepath.Join(exeDir, "logs")
		fmt.Printf("未配置日志路径，使用默认本地路径: %s\n", logDir)
	}
	if err := platform.CleanLogs(logDir); err != nil {
		fmt.Println("清理日志失败:", err)
	}
	waitForEnter()
}

// waitForEnter 暂停程序执行直到用户按下回车键，防止控制台窗口闪退
func waitForEnter() {
	fmt.Print("按 Enter 键继续...")
	_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
}

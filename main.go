package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"time"
)

const (
	serviceName = "llama-swap"
	port        = 11451
)

func main() {
	configureConsole()
	if !platformSupported() {
		fmt.Printf("当前版本暂不支持 %s；更新核心已支持扩展该平台。\n", platformName())
		return
	}
	if requiresElevation() && !isElevated() {
		if err := relaunchElevated(); err != nil {
			fmt.Println("请求管理员权限失败:", err)
		}
		return
	}
	showMenu()
}

func showMenu() {
	reader := bufio.NewReader(os.Stdin)
	for {
		fmt.Println("========================================")
		fmt.Println("   llama-swap 服务器管理工具")
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
			runServiceAction("启动", startPlatformService)
		case "2":
			runServiceAction("停止", stopPlatformService)
		case "3":
			runServiceAction("重启", restartPlatformService)
		case "4":
			showStatus()
		case "5":
			cleanLogs()
		case "6":
			updateManagedApps(reader)
		case "0":
			return
		default:
			fmt.Println("无效输入，请重新选择。")
			time.Sleep(time.Second)
		}
	}
}

func runServiceAction(action string, fn func(string) error) {
	fmt.Printf("正在%s %s...\n", action, serviceName)
	if err := fn(serviceName); err != nil {
		fmt.Printf("%s失败: %v\n", action, err)
	} else {
		fmt.Printf("%s完成。\n", action)
	}
	waitForEnter()
}

func showStatus() {
	status, err := platformServiceStatus(serviceName)
	if err != nil {
		fmt.Println("无法查询服务状态:", err)
	} else {
		fmt.Printf("服务 %s: %s\n", serviceName, status)
	}
	listening, err := platformPortListening(port)
	if err != nil {
		fmt.Println("无法查询端口状态:", err)
	} else if listening {
		fmt.Printf("端口 %d 已监听 [正常]\n", port)
	} else {
		fmt.Printf("端口 %d 未监听 [异常]\n", port)
	}
	waitForEnter()
}

func cleanLogs() {
	if err := runLogCleanup(); err != nil {
		fmt.Println("清理日志失败:", err)
	} else {
		fmt.Println("日志清理完成。")
	}
	waitForEnter()
}

func waitForEnter() {
	fmt.Print("按 Enter 键继续...")
	_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
}

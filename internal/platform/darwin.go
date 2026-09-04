//go:build darwin

package platform

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// IsSupported 判断当前平台是否完全支持服务及管理操作
func IsSupported() bool         { return true }

// PlatformName 返回当前系统的展示名称
func PlatformName() string      { return "macOS" }

// RequiresElevation 判断在当前平台执行服务管理是否通常需要请求提权（管理员权限）
func RequiresElevation() bool   { return false }

// ConfigureConsole 配置 macOS 终端环境
func ConfigureConsole()         {}

// IsElevated 检测当前进程是否已经拥有 root 权限
func IsElevated() bool          { return os.Geteuid() == 0 }

// RelaunchElevated 请求 UAC 提权并以管理员身份重新运行当前可执行文件 (macOS 实现暂留)
func RelaunchElevated() error   { return nil }

// StartService 启动指定的 macOS 守护服务
func StartService(name string) error {
	plistPath := getPlistPath(name)
	if _, err := os.Stat(plistPath); err != nil {
		return fmt.Errorf("服务配置文件不存在: %v", err)
	}

	status, err := ServiceStatus(name)
	if err != nil {
		return fmt.Errorf("查询服务状态失败: %v", err)
	}
	if status == "未找到" {
		if out, err := CommandOutput("launchctl", "load", "-w", plistPath); err != nil {
			return fmt.Errorf("加载服务 (load) 失败: %v (输出: %s)", err, out)
		}
	}
	return StreamCommand("launchctl", "start", "com."+name)
}

// StopService 停止指定的 macOS 守护服务
func StopService(name string) error {
	return StreamCommand("launchctl", "stop", "com."+name)
}

// RestartService 重启指定的 macOS 守护服务
func RestartService(name string) error {
	running, err := ServiceRunning(name)
	if err != nil {
		return fmt.Errorf("查询服务运行状态失败: %v", err)
	}
	if running {
		if err := StopService(name); err != nil {
			return fmt.Errorf("重启时停止服务失败: %v", err)
		}
		time.Sleep(time.Second)
	}
	if err := StartService(name); err != nil {
		return fmt.Errorf("重启启动失败: %v", err)
	}
	return nil
}

// ServiceStatus 查询 macOS 守护服务的当前运行状态
func ServiceStatus(name string) (string, error) {
	out, err := CommandOutput("launchctl", "list", "com."+name)
	return parseLaunchctlStatus(out, err)
}

func parseLaunchctlStatus(out string, cmdErr error) (string, error) {
	if cmdErr != nil {
		if strings.Contains(cmdErr.Error(), "113") || strings.Contains(out, "Could not find service") {
			return "未找到", nil
		}
		return "", fmt.Errorf("查询失败: %v", cmdErr)
	}
	if strings.Contains(out, `"PID" =`) {
		return "Running", nil
	}
	return "Stopped", nil
}

// ServiceRunning 快速判定守护服务当前是否正处于运行状态
func ServiceRunning(name string) (bool, error) {
	status, err := ServiceStatus(name)
	if err != nil {
		return false, err
	}
	return status == "Running", nil
}

// PortListening 检查指定的 TCP 本地端口是否正处于 Listen 状态
func PortListening(port int) (bool, error) {
	out, err := CommandOutput("lsof", "-iTCP:"+strconv.Itoa(port), "-sTCP:LISTEN", "-t")
	if err != nil {
		return false, nil
	}
	return strings.TrimSpace(out) != "", nil
}

// SearchPath 在系统 PATH 环境变量中搜索给定的可执行文件名称，返回绝对路径
func SearchPath(name string) ([]string, error) {
	path, err := exec.LookPath(name)
	if err != nil {
		return nil, err
	}
	return []string{path}, nil
}

// DefaultDirs 提供本平台下，搜索受管应用程序及其配置文件的常用候选目录
func DefaultDirs() []string {
	home, _ := os.UserHomeDir()
	dirs := []string{"/usr/local/bin", "/opt/homebrew/bin", "/usr/bin"}
	if home != "" {
		dirs = append(dirs, filepath.Join(home, ".local", "bin"), filepath.Join(home, "bin"))
	}
	return dirs
}

// InstalledCUDAVariant macOS 默认无 CUDA 环境支持，返回空
func InstalledCUDAVariant() string         { return "" }

// ExecutableNames 根据基础程序名称展开出附带特定扩展名的实际可执行文件全名
func ExecutableNames(base string) []string { return []string{base} }

// ---- 原生服务管理 (Launchd) ----

func isNativeService() bool {
	// macOS 下不需要特殊进程间通信(如 Windows SVC)即可在 launchd 环境运行
	return false
}

func getPlistPath(name string) string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "LaunchAgents", fmt.Sprintf("com.%s.plist", name))
}

func xmlEscape(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, "\"", "&quot;")
	s = strings.ReplaceAll(s, "'", "&apos;")
	return s
}

func RegisterService(name, displayName, exePath string, args ...string) error {
	plistPath := getPlistPath(name)
	workDir := filepath.Dir(exePath)
	logDir := filepath.Join(workDir, "logs")

	if err := os.MkdirAll(logDir, 0755); err != nil {
		return fmt.Errorf("创建日志目录失败: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(plistPath), 0755); err != nil {
		return fmt.Errorf("创建 Plist 目录失败: %v", err)
	}

	var argsXML strings.Builder
	argsXML.WriteString(fmt.Sprintf("\t\t<string>%s</string>\n", xmlEscape(exePath)))
	for _, arg := range args {
		argsXML.WriteString(fmt.Sprintf("\t\t<string>%s</string>\n", xmlEscape(arg)))
	}

	plistContent := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>com.%s</string>
	<key>ProgramArguments</key>
	<array>
%s	</array>
	<key>WorkingDirectory</key>
	<string>%s</string>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<true/>
	<key>StandardOutPath</key>
	<string>%s/service_out.log</string>
	<key>StandardErrorPath</key>
	<string>%s/service_err.log</string>
	<key>EnvironmentVariables</key>
	<dict>
		<key>PATH</key>
		<string>/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin</string>
	</dict>
</dict>
</plist>
`, xmlEscape(name), argsXML.String(), xmlEscape(workDir), xmlEscape(logDir), xmlEscape(logDir))

	if err := os.WriteFile(plistPath, []byte(plistContent), 0644); err != nil {
		return err
	}
	// 注册完成后主动 load，并检查错误
	if _, err := CommandOutput("launchctl", "load", "-w", plistPath); err != nil {
		return fmt.Errorf("加载服务失败: %v", err)
	}
	return nil
}

func UninstallService(name string) error {
	plistPath := getPlistPath(name)

	// 1. 如果服务正在运行，先停止
	running, err := ServiceRunning(name)
	if err == nil && running {
		if err := StopService(name); err != nil {
			return fmt.Errorf("停止服务失败: %v", err)
		}
	}

	// 2. 从 launchd 中卸载配置 (unload)
	if _, err := os.Stat(plistPath); err == nil {
		if out, err := CommandOutput("launchctl", "unload", "-w", plistPath); err != nil {
			return fmt.Errorf("注销服务 (unload) 失败: %v (输出: %s)", err, out)
		}
	}

	// 3. 删除 plist 文件
	if err := os.Remove(plistPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("删除 plist 文件失败: %v", err)
	}
	return nil
}

func RunAsService(serviceName string, runFn func(ctx context.Context) error) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// macOS 下接收停止信号 (SIGTERM/SIGINT) 以实现优雅退出
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-c
		cancel()
	}()

	return runFn(ctx)
}

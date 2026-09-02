//go:build darwin

package platform

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// IsSupported 判断当前平台是否完全支持服务及管理操作
func IsSupported() bool         { return true }

// PlatformName 返回当前系统的展示名称
func PlatformName() string      { return "macOS" }

// RequiresElevation 判断在当前平台执行服务管理是否通常需要请求提权（管理员权限）
func RequiresElevation() bool   { return false }

// ConfigureConsole 配置 macOS 终端环境 (macOS 默认支持 UTF-8，无需额外配置)
func ConfigureConsole()         {}

// IsElevated 检测当前进程是否已经拥有 root 权限
func IsElevated() bool          { return os.Geteuid() == 0 }

// RelaunchElevated 请求 UAC 提权并以管理员身份重新运行当前可执行文件 (macOS 实现暂留)
func RelaunchElevated() error   { return nil }

// StartService 使用 brew 或 launchctl 启动指定的 macOS 守护服务
func StartService(name string) error {
	if _, err := exec.LookPath("brew"); err == nil {
		return StreamCommand("brew", "services", "start", name)
	}
	return StreamCommand("launchctl", "start", name)
}

// StopService 使用 brew 或 launchctl 停止指定的 macOS 守护服务
func StopService(name string) error {
	if _, err := exec.LookPath("brew"); err == nil {
		return StreamCommand("brew", "services", "stop", name)
	}
	return StreamCommand("launchctl", "stop", name)
}

// RestartService 重启指定的 macOS 守护服务
func RestartService(name string) error {
	if _, err := exec.LookPath("brew"); err == nil {
		return StreamCommand("brew", "services", "restart", name)
	}
	_ = StopService(name)
	time.Sleep(time.Second)
	return StartService(name)
}

// ServiceStatus 查询 macOS 守护服务的当前运行状态
func ServiceStatus(name string) (string, error) {
	if _, err := exec.LookPath("brew"); err == nil {
		out, err := CommandOutput("brew", "services", "info", name, "--json")
		if err == nil {
			return "Managed by brew", nil
		}
	}
	out, err := CommandOutput("launchctl", "list", name)
	if err != nil {
		return "未找到", nil
	}
	return strings.TrimSpace(out), nil
}

// ServiceRunning 快速判定守护服务当前是否正处于运行状态
func ServiceRunning(name string) (bool, error) {
	status, err := ServiceStatus(name)
	if err != nil || status == "未找到" {
		return false, err
	}
	return true, nil
}

// PortListening 检查指定的 TCP 本地端口是否正处于 Listen 状态
func PortListening(port int) (bool, error) {
	out, err := CommandOutput("lsof", "-iTCP:"+strconv.Itoa(port), "-sTCP:LISTEN", "-t")
	if err != nil {
		return false, nil
	}
	return strings.TrimSpace(out) != "", nil
}

// CleanLogs 删除指定目录下 7 天前的过期日志文件
func CleanLogs(logDir string) error {
	info, err := os.Stat(logDir)
	if err != nil || !info.IsDir() {
		fmt.Printf("[INFO] 日志目录不存在: %s\n", logDir)
		return nil
	}
	cutoff := time.Now().AddDate(0, 0, -7)
	entries, err := os.ReadDir(logDir)
	if err != nil {
		return err
	}
	deletedCount := 0
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			if err := os.Remove(filepath.Join(logDir, entry.Name())); err == nil {
				deletedCount++
			}
		}
	}
	fmt.Printf("[OK] 清理完成。已删除 %d 个日志文件。\n", deletedCount)
	return nil
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

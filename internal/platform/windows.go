//go:build windows

package platform

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/windows"
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

// CleanLogs 删除指定目录下 7 天前的过期日志文件
func CleanLogs(logDir string) error {
	info, err := os.Stat(logDir)
	if err != nil || !info.IsDir() {
		fmt.Printf("[WARN] 日志目录不存在: %s\n", logDir)
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

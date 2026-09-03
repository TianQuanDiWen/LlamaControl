//go:build !windows && !darwin

package platform

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// IsSupported 判断当前平台是否完全支持服务及管理操作
func IsSupported() bool                    { return false }
// PlatformName 返回当前系统的展示名称
func PlatformName() string                 { return runtime.GOOS }
// RequiresElevation 判断在当前平台执行服务管理是否通常需要请求提权（管理员权限）
func RequiresElevation() bool              { return false }
// ConfigureConsole 平台相关：配置终端支持
func ConfigureConsole()                    {}
// IsElevated 检测当前进程是否已经拥有 root 权限
func IsElevated() bool                     { return os.Geteuid() == 0 }
// RelaunchElevated 请求 UAC 提权并以管理员身份重新运行当前可执行文件 (占位)
func RelaunchElevated() error              { return nil }

func unsupported() error                   { return fmt.Errorf("尚未实现 %s 平台适配", runtime.GOOS) }

// StartService 启动系统守护服务 (占位)
func StartService(string) error            { return unsupported() }
// StopService 停止系统守护服务 (占位)
func StopService(string) error             { return unsupported() }
// RestartService 重启系统守护服务 (占位)
func RestartService(string) error          { return unsupported() }
// ServiceStatus 获取系统守护服务状态 (占位)
func ServiceStatus(string) (string, error) { return "", unsupported() }
// ServiceRunning 判断系统守护服务是否正运行 (占位)
func ServiceRunning(string) (bool, error)  { return false, unsupported() }
// PortListening 检查指定的 TCP 本地端口是否监听 (占位)
func PortListening(int) (bool, error)      { return false, unsupported() }

// SearchPath 在系统 PATH 环境变量中搜索给定的可执行文件名称
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
	dirs := []string{"/usr/local/bin", "/usr/bin"}
	if home != "" {
		dirs = append(dirs, filepath.Join(home, ".local", "bin"))
	}
	return dirs
}

// InstalledCUDAVariant 探测本机安装的 CUDA 版本
func InstalledCUDAVariant() string         { return "" }

// ExecutableNames 根据基础程序名称展开出附带特定扩展名的实际可执行文件全名
func ExecutableNames(base string) []string { return []string{base} }

// ---- 原生服务管理 (当前平台未实现) ----

func RegisterService(name, displayName, exePath string, args ...string) error { return unsupported() }
func UninstallService(string) error                                               { return unsupported() }
func RunAsService(serviceName string, runFn func(ctx context.Context) error) error { return unsupported() }
func HandleServiceWorker(serviceName string) bool                                 { return false }
func ManageService(reader *bufio.Reader, serviceName string, getAppInfo func() (swapPath, appDir string, port int)) {
	fmt.Println("当前平台暂未实现系统服务管理功能。")
}



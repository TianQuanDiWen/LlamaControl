package platform

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
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
	case ContainsAny(text, "cuda 12", "cuda12", "cuda-12"):
		return "cuda12"
	case InstalledCUDAVariant() != "":
		return InstalledCUDAVariant()
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


package config

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
)

var (
	portPattern   = regexp.MustCompile(`(?im)^\s*port\s*:\s*"?(\d+)"?`)
	listenPattern = regexp.MustCompile(`(?im)^\s*listen\s*:\s*"?.*?:(\d+)"?`)
	
	// 匹配 YAML 中常见的日志路径配置，如 log_dir, log-dir, log_file, log
	logDirPattern  = regexp.MustCompile(`(?im)^\s*log(?:_|-)?dir\s*:\s*"?([^"\r\n]+)"?`)
	logFilePattern = regexp.MustCompile(`(?im)^\s*log(?:(?:_|-)?(?:file|path))?\s*:\s*"?([^"\r\n]+)"?`)
)

// ParseSwapPort 从 YAML 文本字节流中提取 port 或 listen 端口
func ParseSwapPort(content []byte) (int, bool) {
	if match := portPattern.FindSubmatch(content); len(match) > 1 {
		if p, err := strconv.Atoi(string(match[1])); err == nil && p > 0 && p <= 65535 {
			return p, true
		}
	}
	if match := listenPattern.FindSubmatch(content); len(match) > 1 {
		if p, err := strconv.Atoi(string(match[1])); err == nil && p > 0 && p <= 65535 {
			return p, true
		}
	}
	return 0, false
}

// ParseSwapLogDir 从 YAML 文本字节流中提取日志目录路径
func ParseSwapLogDir(content []byte) (string, bool) {
	if match := logDirPattern.FindSubmatch(content); len(match) > 1 {
		return string(match[1]), true
	}
	if match := logFilePattern.FindSubmatch(content); len(match) > 1 {
		// 如果配置的是 log_file 具体文件路径，则取其所在目录
		return filepath.Dir(string(match[1])), true
	}
	return "", false
}

// DetectSwapPort 在给定的候选目录中寻找 config.yaml / config.yml，若找不到则返回官方默认端口 8080
func DetectSwapPort(dirs ...string) int {
	for _, dir := range dirs {
		if dir == "" {
			continue
		}
		for _, name := range []string{"config.yaml", "config.yml"} {
			if content, err := os.ReadFile(filepath.Join(dir, name)); err == nil {
				if port, ok := ParseSwapPort(content); ok {
					return port
				}
			}
		}
	}
	return 8080
}

// DetectSwapLogDir 尝试从配置文件中读取日志目录配置
func DetectSwapLogDir(dirs ...string) (string, bool) {
	for _, dir := range dirs {
		if dir == "" {
			continue
		}
		for _, name := range []string{"config.yaml", "config.yml"} {
			if content, err := os.ReadFile(filepath.Join(dir, name)); err == nil {
				if logDir, ok := ParseSwapLogDir(content); ok {
					return logDir, true
				}
			}
		}
	}
	return "", false
}

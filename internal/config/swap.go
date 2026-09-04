package config

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

var (
	portPattern   = regexp.MustCompile(`(?im)^\s*port\s*:\s*"?(\d+)"?\s*(?:#.*)?$`)
	listenPattern = regexp.MustCompile(`(?im)^\s*listen\s*:\s*"?.*?:(\d+)"?\s*(?:#.*)?$`)
	
	// 匹配 YAML 中常见的日志路径配置，如 log_dir, log-dir, log_file, log-file, log_path
	logDirPattern  = regexp.MustCompile(`(?im)^\s*log(?:_|-)?dir\s*:\s*"?([^"\r\n#]+?)"?\s*(?:#.*)?$`)
	logFilePattern = regexp.MustCompile(`(?im)^\s*log(?:_|-)?(?:file|path)\s*:\s*"?([^"\r\n#]+?)"?\s*(?:#.*)?$`)
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
		dir := filepath.Clean(strings.TrimSpace(string(match[1])))
		if dir != "" && dir != "." {
			return dir, true
		}
	}
	if match := logFilePattern.FindSubmatch(content); len(match) > 1 {
		dir := filepath.Dir(strings.TrimSpace(string(match[1])))
		if dir != "" && dir != "." {
			return dir, true
		}
	}
	return "", false
}

// DetectSwapPort 在给定的候选目录中寻找 config.yaml / config.yml，若找不到则返回官方默认端口 8080 和 "config.yaml"
func DetectSwapPort(dirs ...string) (int, string) {
	for _, dir := range dirs {
		if dir == "" {
			continue
		}
		for _, name := range []string{"config.yaml", "config.yml"} {
			if content, err := os.ReadFile(filepath.Join(dir, name)); err == nil {
				if port, ok := ParseSwapPort(content); ok {
					return port, name
				}
			}
		}
	}
	return 8080, "config.yaml"
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

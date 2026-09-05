package config

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// SwapConfig 映射 llama-swap 配置文件的核心字段
type SwapConfig struct {
	PortRaw     any    `yaml:"port"`
	Listen      string `yaml:"listen"`
	LogDir      string `yaml:"log_dir"`
	LogDirDash  string `yaml:"log-dir"`
	LogFile     string `yaml:"log_file"`
	LogFileDash string `yaml:"log-file"`
	LogPath     string `yaml:"log_path"`
}

// ParseSwapConfig 解析 YAML 字节流为结构化配置对象
func ParseSwapConfig(content []byte) (SwapConfig, error) {
	var cfg SwapConfig
	err := yaml.Unmarshal(content, &cfg)
	return cfg, err
}

// EffectivePort 提取有效端口（优先读取 port，支持数值与字符串格式，备选解析 listen）
func (c *SwapConfig) EffectivePort() (int, bool) {
	if c.PortRaw != nil {
		switch v := c.PortRaw.(type) {
		case int:
			if v > 0 && v <= 65535 {
				return v, true
			}
		case int64:
			if v > 0 && v <= 65535 {
				return int(v), true
			}
		case string:
			if p, err := strconv.Atoi(strings.Trim(strings.TrimSpace(v), `"'`)); err == nil && p > 0 && p <= 65535 {
				return p, true
			}
		}
	}
	if c.Listen != "" {
		s := strings.Trim(strings.TrimSpace(c.Listen), `"'`)
		// 支持无冒号纯数字格式，如 "9090"
		if p, err := strconv.Atoi(s); err == nil && p > 0 && p <= 65535 {
			return p, true
		}
		// 支持 ":8080", "0.0.0.0:8080", "127.0.0.1:8080" 等格式
		parts := strings.Split(s, ":")
		if len(parts) > 1 {
			if p, err := strconv.Atoi(parts[len(parts)-1]); err == nil && p > 0 && p <= 65535 {
				return p, true
			}
		}
	}
	return 0, false
}

// EffectiveLogDir 提取有效日志路径（支持目录直接定义，或从具体日志文件名中推导）
func (c *SwapConfig) EffectiveLogDir() (string, bool) {
	// 1. 显式目录配置 (log_dir / log-dir)
	for _, raw := range []string{c.LogDir, c.LogDirDash} {
		clean := filepath.Clean(strings.TrimSpace(raw))
		if clean != "" && clean != "." {
			return clean, true
		}
	}

	// 2. 显式日志文件配置 (log_file / log-file)
	for _, raw := range []string{c.LogFile, c.LogFileDash} {
		if raw = strings.TrimSpace(raw); raw != "" {
			cleanDir := filepath.Clean(filepath.Dir(raw))
			if cleanDir != "" && cleanDir != "." {
				return cleanDir, true
			}
		}
	}

	// 3. log_path (兼容器目录或具体文件)
	if raw := strings.TrimSpace(c.LogPath); raw != "" {
		clean := filepath.Clean(raw)
		if strings.HasSuffix(strings.ToLower(clean), ".log") || strings.HasSuffix(strings.ToLower(clean), ".txt") {
			cleanDir := filepath.Clean(filepath.Dir(clean))
			if cleanDir != "" && cleanDir != "." {
				return cleanDir, true
			}
			return "", false // 类似 "swap.log" 其父目录为 "."，必须彻底拦截，杜绝误判为当前根目录
		}
		if clean != "" && clean != "." {
			return clean, true
		}
	}
	return "", false
}

// ParseSwapPort 从 YAML 文本字节流中提取 port 或 listen 端口
func ParseSwapPort(content []byte) (int, bool) {
	if cfg, err := ParseSwapConfig(content); err == nil {
		return cfg.EffectivePort()
	}
	return 0, false
}

// ParseSwapLogDir 从 YAML 文本字节流中提取日志目录路径
func ParseSwapLogDir(content []byte) (string, bool) {
	if cfg, err := ParseSwapConfig(content); err == nil {
		return cfg.EffectiveLogDir()
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

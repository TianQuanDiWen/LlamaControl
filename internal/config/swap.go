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

// SwapConfigLocation 记录探测到的 llama-swap 配置文件路径及环境元信息
type SwapConfigLocation struct {
	ConfigFile     string // 配置文件的绝对路径
	ConfigFileName string // 配置文件基准名称 (config.yaml 或 config.yml)
	WorkDir        string // 配置文件所在的目录绝对路径（以此作为工作目录运行）
	Port           int    // 监听端口（默认 8080）
	Found          bool   // 是否在候选目录中真实找到了配置文件
}

// ResolveSwapConfig 在候选目录中按顺序检索 config.yaml 或 config.yml，统一返回绝对路径及对应工作目录。
// 若所有目录均未找到，以首个有效候选目录（或当前目录）作为工作目录，返回默认的 8080 端口与 config.yaml 默认路径。
func ResolveSwapConfig(dirs ...string) SwapConfigLocation {
	var fallbackDir string
	for _, dir := range dirs {
		if dir == "" {
			continue
		}
		if fallbackDir == "" {
			fallbackDir = dir
		}
		for _, name := range []string{"config.yaml", "config.yml"} {
			cfgPath := filepath.Join(dir, name)
			if content, err := os.ReadFile(cfgPath); err == nil {
				port := 8080
				if p, ok := ParseSwapPort(content); ok {
					port = p
				}
				absCfg, _ := filepath.Abs(cfgPath)
				absDir, _ := filepath.Abs(dir)
				return SwapConfigLocation{
					ConfigFile:     absCfg,
					ConfigFileName: name,
					WorkDir:        absDir,
					Port:           port,
					Found:          true,
				}
			}
		}
	}

	if fallbackDir == "" {
		fallbackDir = "."
	}
	absWorkDir, _ := filepath.Abs(fallbackDir)
	return SwapConfigLocation{
		ConfigFile:     filepath.Join(absWorkDir, "config.yaml"),
		ConfigFileName: "config.yaml",
		WorkDir:        absWorkDir,
		Port:           8080,
		Found:          false,
	}
}

// DetectSwapPort 在给定的候选目录中寻找 config.yaml / config.yml，若找不到则返回官方默认端口 8080 和 "config.yaml"
func DetectSwapPort(dirs ...string) (int, string) {
	loc := ResolveSwapConfig(dirs...)
	return loc.Port, loc.ConfigFileName
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

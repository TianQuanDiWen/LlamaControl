package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseSwapPort(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    int
		ok      bool
	}{
		{"standard port", "port: 11451", 11451, true},
		{"string port", "port: \"8080\"", 8080, true},
		{"listen address", "listen: 0.0.0.0:9090", 9090, true},
		{"listen string", "listen: \":8080\"", 8080, true},
		{"listen pure port string", "listen: \"9090\"", 9090, true},
		{"listen pure port unquoted", "listen: 9090", 9090, true},
		{"invalid port format", "port: abc", 0, false},
		{"comment out port", "server: true\n# port: 8080", 0, false},
		{"trailing comment", "port: 1234 # comment here", 1234, true},
		{"nested indent", "  port: 5678", 5678, true},
		{"illegal high port", "port: 99999", 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ParseSwapPort([]byte(tt.content))
			if got != tt.want || ok != tt.ok {
				t.Fatalf("ParseSwapPort() = %d, %v; want %d, %v", got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestDetectSwapPortFallback(t *testing.T) {
	gotPort, gotFile := DetectSwapPort("non_existent_dir_12345")
	if gotPort != 8080 || gotFile != "config.yaml" {
		t.Fatalf("DetectSwapPort() = %d, %s; want 8080, config.yaml", gotPort, gotFile)
	}
}

func TestParseSwapLogDir(t *testing.T) {
	tests := []struct {
		name    string
		content string
		wantDir string
		ok      bool
	}{
		{"valid log_dir", "log_dir: /var/log/llama", filepath.FromSlash("/var/log/llama"), true},
		{"valid log-dir with quotes", "log-dir: \"/var/log/llama\"", filepath.FromSlash("/var/log/llama"), true},
		{"valid log_file", "log_file: /var/log/llama/swap.log", filepath.FromSlash("/var/log/llama"), true},
		{"valid log-file", "log-file: /opt/swap/logs/run.log", filepath.FromSlash("/opt/swap/logs"), true},
		{"valid log_path", "log_path: /var/logs/llama/app.log", filepath.FromSlash("/var/logs/llama"), true},
		// 负向拦截测试：避免普通的 log: info / stdout 等配置被误判为路径
		{"negative log info", "log: info", "", false},
		{"negative log stdout", "log: stdout", "", false},
		{"negative log debug", "log: debug # level", "", false},
		{"dot dir ignored", "log_dir: .", "", false},
		{"filename only log_path ignored (prevent .)", "log_path: swap.log", "", false},
		{"filename only log_file ignored (prevent .)", "log_file: run.log", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ParseSwapLogDir([]byte(tt.content))
			if got != tt.wantDir || ok != tt.ok {
				t.Fatalf("ParseSwapLogDir() = %q, %v; want %q, %v", got, ok, tt.wantDir, tt.ok)
			}
		})
	}
}

func TestParseSwapConfig_AdvancedYAML(t *testing.T) {
	yamlContent := `
# 测试复杂的锚点引用与行尾注释
defaults: &default_settings
  port: 9000 # 默认端口
  log_dir: /opt/shared/logs # 默认共享日志

port: 9000
log_dir: /opt/shared/logs
`
	cfg, err := ParseSwapConfig([]byte(yamlContent))
	if err != nil {
		t.Fatalf("ParseSwapConfig() failed: %v", err)
	}
	port, ok := cfg.EffectivePort()
	if !ok || port != 9000 {
		t.Errorf("EffectivePort() = %v, %v; want 9000, true", port, ok)
	}
	dir, ok := cfg.EffectiveLogDir()
	if !ok || dir != filepath.FromSlash("/opt/shared/logs") {
		t.Errorf("EffectiveLogDir() = %v, %v; want %v, true", dir, ok, filepath.FromSlash("/opt/shared/logs"))
	}
}

func TestSwapConfig_PortTypes(t *testing.T) {
	cfgInt64 := SwapConfig{PortRaw: int64(8888)}
	if p, ok := cfgInt64.EffectivePort(); !ok || p != 8888 {
		t.Fatalf("int64 port failed: %d, %v", p, ok)
	}

	cfgString := SwapConfig{PortRaw: "7777"}
	if p, ok := cfgString.EffectivePort(); !ok || p != 7777 {
		t.Fatalf("string port failed: %d, %v", p, ok)
	}
}

func TestResolveSwapConfig(t *testing.T) {
	tmpRoot := t.TempDir()
	exeDir := filepath.Join(tmpRoot, "control")
	appDir := filepath.Join(tmpRoot, "control", "bin")
	_ = os.MkdirAll(exeDir, 0755)
	_ = os.MkdirAll(appDir, 0755)

	// 1. 当任何目录都没有配置时，回退到首个候选目录，Found 为 false
	locFallback := ResolveSwapConfig(exeDir, appDir)
	if locFallback.Found {
		t.Errorf("expected Found == false, got true")
	}
	if locFallback.Port != 8080 || locFallback.ConfigFileName != "config.yaml" {
		t.Errorf("expected fallback 8080/config.yaml, got %d/%s", locFallback.Port, locFallback.ConfigFileName)
	}
	expectedExeDir, _ := filepath.Abs(exeDir)
	if locFallback.WorkDir != expectedExeDir {
		t.Errorf("expected WorkDir %s, got %s", expectedExeDir, locFallback.WorkDir)
	}

	// 2. 模拟旧安装：根目录无配置，仅在 appDir 下存在 config.yml
	ymlPath := filepath.Join(appDir, "config.yml")
	_ = os.WriteFile(ymlPath, []byte("port: 9090\n"), 0644)

	locYml := ResolveSwapConfig(exeDir, appDir)
	if !locYml.Found {
		t.Errorf("expected Found == true")
	}
	if locYml.Port != 9090 || locYml.ConfigFileName != "config.yml" {
		t.Errorf("expected 9090/config.yml, got %d/%s", locYml.Port, locYml.ConfigFileName)
	}
	expectedAppDir, _ := filepath.Abs(appDir)
	if locYml.WorkDir != expectedAppDir {
		t.Errorf("expected WorkDir to resolve to appDir %s, got %s", expectedAppDir, locYml.WorkDir)
	}
	expectedYmlPath, _ := filepath.Abs(ymlPath)
	if locYml.ConfigFile != expectedYmlPath {
		t.Errorf("expected ConfigFile %s, got %s", expectedYmlPath, locYml.ConfigFile)
	}

	// 3. 根目录存在 config.yaml 时，优先使用根目录工作目录及配置文件
	yamlPath := filepath.Join(exeDir, "config.yaml")
	_ = os.WriteFile(yamlPath, []byte("port: 11451\n"), 0644)

	locYaml := ResolveSwapConfig(exeDir, appDir)
	if !locYaml.Found || locYaml.Port != 11451 || locYaml.ConfigFileName != "config.yaml" {
		t.Errorf("expected 11451/config.yaml, got %d/%s", locYaml.Port, locYaml.ConfigFileName)
	}
	if locYaml.WorkDir != expectedExeDir {
		t.Errorf("expected WorkDir to resolve to exeDir %s, got %s", expectedExeDir, locYaml.WorkDir)
	}
}

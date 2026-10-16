package platform

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateServiceName(t *testing.T) {
	validNames := []string{
		"llama-swap",
		"llama_swap",
		"service123",
		"My-Service_01",
		"a",
	}
	for _, name := range validNames {
		if err := ValidateServiceName(name); err != nil {
			t.Errorf("expected valid for %q, got error: %v", name, err)
		}
	}

	invalidNames := []string{
		"",
		"   ",
		"service name",
		"service;calc.exe",
		"svc&echo 1",
		"svc|netstat",
		"svc\"--flag",
		"svc'--flag",
		"service$name",
		strings.Repeat("a", 65),
	}
	for _, name := range invalidNames {
		if err := ValidateServiceName(name); err == nil {
			t.Errorf("expected invalid for %q, got nil error", name)
		}
	}
}

func TestPauseAndResumeSwapProcess(t *testing.T) {
	// 验证在没有活跃进程时 Pause 和 Resume 能安全幂等执行
	if err := PauseSwapProcess(); err != nil {
		t.Fatalf("unexpected error pausing swap: %v", err)
	}

	workerStateMu.Lock()
	paused := swapPaused
	workerStateMu.Unlock()
	if !paused {
		t.Errorf("expected swapPaused to be true")
	}

	if err := ResumeSwapProcess(); err != nil {
		t.Fatalf("unexpected error resuming swap: %v", err)
	}

	workerStateMu.Lock()
	paused = swapPaused
	workerStateMu.Unlock()
	if paused {
		t.Errorf("expected swapPaused to be false after resume")
	}
}

func TestRestartSwapProcess(t *testing.T) {
	if err := RestartSwapProcess(); err != nil {
		t.Fatalf("unexpected error restarting swap: %v", err)
	}

	workerStateMu.Lock()
	restarting := swapRestarting
	workerStateMu.Unlock()
	if !restarting {
		t.Errorf("expected swapRestarting to be true after restart requested")
	}

	// 清理测试标记
	workerStateMu.Lock()
	swapRestarting = false
	workerStateMu.Unlock()
}

func TestConfigCandidateDirs(t *testing.T) {
	exeDir := filepath.Clean("/test/root")
	appDir := filepath.Clean("/test/root/bin")

	dirs := ConfigCandidateDirs(exeDir, appDir)
	if len(dirs) < 2 {
		t.Fatalf("expected at least 2 dirs, got %d", len(dirs))
	}
	if dirs[0] != exeDir {
		t.Errorf("expected first dir to be exeDir %q, got %q", exeDir, dirs[0])
	}
	if dirs[1] != filepath.Join(exeDir, "bin") {
		t.Errorf("expected second dir to be bin dir %q, got %q", filepath.Join(exeDir, "bin"), dirs[1])
	}

	// 验证去重：appDir 就是 exeDir/bin 时不应出现连续重复
	count := 0
	for _, d := range dirs {
		if strings.EqualFold(d, filepath.Join(exeDir, "bin")) {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected exactly 1 instance of bin dir due to deduplication, got %d", count)
	}
}

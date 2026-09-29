package platform

import (
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

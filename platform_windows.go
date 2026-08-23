//go:build windows

package main

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

func platformSupported() bool { return true }
func platformName() string    { return "Windows" }
func requiresElevation() bool { return true }
func configureConsole()       { _ = windows.SetConsoleOutputCP(65001); _ = windows.SetConsoleCP(65001) }
func isElevated() bool        { return exec.Command("cmd", "/C", "net session >nul 2>&1").Run() == nil }

func relaunchElevated() error {
	exePath, err := os.Executable()
	if err != nil {
		return err
	}
	quoted := strings.ReplaceAll(exePath, "'", "''")
	return exec.Command("powershell", "-NoProfile", "-Command", "Start-Process '"+quoted+"' -Verb RunAs").Run()
}

func startPlatformService(name string) error { return streamCommand("net", "start", name) }
func stopPlatformService(name string) error  { return streamCommand("net", "stop", name) }
func restartPlatformService(name string) error {
	_ = stopPlatformService(name)
	time.Sleep(2 * time.Second)
	return startPlatformService(name)
}

func platformServiceStatus(name string) (string, error) {
	script := "$svc=Get-Service -Name '" + strings.ReplaceAll(name, "'", "''") + "' -ErrorAction SilentlyContinue; if($svc){$svc.Status}else{'未找到'}"
	return commandOutput("powershell", "-NoProfile", "-Command", script)
}

func platformServiceRunning(name string) (bool, error) {
	status, err := platformServiceStatus(name)
	return strings.EqualFold(strings.TrimSpace(status), "Running"), err
}

func platformPortListening(port int) (bool, error) {
	script := "$c=Get-NetTCPConnection -LocalPort " + strconv.Itoa(port) + " -State Listen -ErrorAction SilentlyContinue; if($c){'true'}else{'false'}"
	out, err := commandOutput("powershell", "-NoProfile", "-Command", script)
	return strings.EqualFold(out, "true"), err
}

func runLogCleanup() error {
	script := filepath.Join(executableDir(), "clean_logs.ps1")
	if _, err := os.Stat(script); err != nil {
		return fmt.Errorf("未找到 %s", script)
	}
	return streamCommand("powershell", "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", script)
}

func platformSearchPath(name string) ([]string, error) {
	out, err := commandOutput("where", name)
	if err != nil {
		return nil, err
	}
	return strings.Fields(out), nil
}
func platformDefaultDirs() []string {
	return []string{`C:\Data\Llama`, `C:\Data\Llama\bin`, `C:\Program Files`, `C:\Program Files (x86)`}
}

func installedCUDAVariant() string {
	entries, _ := os.ReadDir(`C:\Program Files\NVIDIA GPU Computing Toolkit\CUDA`)
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
func executableNames(base string) []string { return []string{base + ".exe", base} }

func streamCommand(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd.Run()
}
func commandOutput(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

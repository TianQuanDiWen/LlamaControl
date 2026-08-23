//go:build !windows

package main

import (
	"fmt"
	"os/exec"
	"runtime"
	"strings"
)

func platformSupported() bool                      { return false }
func platformName() string                         { return runtime.GOOS }
func requiresElevation() bool                      { return false }
func configureConsole()                            {}
func isElevated() bool                             { return true }
func relaunchElevated() error                      { return nil }
func unsupported() error                           { return fmt.Errorf("尚未实现 %s 平台适配", runtime.GOOS) }
func startPlatformService(string) error            { return unsupported() }
func stopPlatformService(string) error             { return unsupported() }
func restartPlatformService(string) error          { return unsupported() }
func platformServiceStatus(string) (string, error) { return "", unsupported() }
func platformServiceRunning(string) (bool, error)  { return false, unsupported() }
func platformPortListening(int) (bool, error)      { return false, unsupported() }
func runLogCleanup() error                         { return unsupported() }
func platformSearchPath(name string) ([]string, error) {
	path, err := exec.LookPath(name)
	if err != nil {
		return nil, err
	}
	return []string{path}, nil
}
func platformDefaultDirs() []string        { return []string{"/usr/local/bin", "/opt/homebrew/bin"} }
func installedCUDAVariant() string         { return "" }
func executableNames(base string) []string { return []string{base} }
func commandOutput(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

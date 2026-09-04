//go:build windows

package platform

import (
	"os"
	"strings"
	"testing"
)

func TestDefaultDirs(t *testing.T) {
	os.Setenv("ProgramFiles", "C:\\Test\\PF")
	os.Setenv("ProgramFiles(x86)", "C:\\Test\\PFx86")
	os.Setenv("LOCALAPPDATA", "C:\\Test\\LocalAppData")

	dirs := DefaultDirs()
	if len(dirs) != 3 {
		t.Fatalf("Expected 3 directories, got %d", len(dirs))
	}
	if dirs[0] != "C:\\Test\\PF" {
		t.Errorf("Expected first dir to be C:\\Test\\PF, got %s", dirs[0])
	}
	if dirs[2] != "C:\\Test\\LocalAppData" {
		t.Errorf("Expected third dir to be C:\\Test\\LocalAppData, got %s", dirs[2])
	}

	os.Setenv("ProgramFiles(x86)", "")
	dirs = DefaultDirs()
	if len(dirs) != 2 {
		t.Fatalf("Expected 2 directories, got %d", len(dirs))
	}
}

func TestInstalledCUDAVariant(t *testing.T) {
	os.Unsetenv("CUDA_PATH")
	for _, env := range os.Environ() {
		key := strings.SplitN(env, "=", 2)[0]
		if strings.HasPrefix(strings.ToUpper(key), "CUDA_PATH_V") {
			os.Unsetenv(key)
		}
	}

	os.Setenv("CUDA_PATH", "C:\\Program Files\\NVIDIA GPU Computing Toolkit\\CUDA\\v13.0")
	if variant := InstalledCUDAVariant(); variant != "cuda13" {
		t.Errorf("Expected cuda13 from CUDA_PATH, got %s", variant)
	}

	os.Unsetenv("CUDA_PATH")
	os.Setenv("CUDA_PATH_V12_1", "C:\\Some\\Path")
	if variant := InstalledCUDAVariant(); variant != "cuda12" {
		t.Errorf("Expected cuda12 from CUDA_PATH_V12_1, got %s", variant)
	}
	os.Unsetenv("CUDA_PATH_V12_1")

	os.Setenv("ProgramFiles", "C:\\InvalidProgramFilesPathXYZ")
	if variant := InstalledCUDAVariant(); variant != "" {
		t.Errorf("Expected empty string for invalid path, got %s", variant)
	}
}

func TestSearchPathParsing(t *testing.T) {
	// 模拟 where 命令输出的字符串，包括带空格的路径和多余的空行
	mockOut := "C:\\Program Files\\llama\\llama-server.exe\nC:\\local tools\\llama-server.exe\n\n"
	
	var paths []string
	for _, line := range strings.Split(mockOut, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			paths = append(paths, line)
		}
	}

	if len(paths) != 2 {
		t.Fatalf("expected 2 paths, got %d", len(paths))
	}
	if paths[0] != "C:\\Program Files\\llama\\llama-server.exe" {
		t.Errorf("unexpected path 0: %s", paths[0])
	}
	if paths[1] != "C:\\local tools\\llama-server.exe" {
		t.Errorf("unexpected path 1: %s", paths[1])
	}
}

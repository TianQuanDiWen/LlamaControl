package updater

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestDownloadTimeoutExceedsAPITimeout(t *testing.T) {
	if DownloadClient.Timeout <= GithubClient.Timeout {
		t.Fatalf("download timeout %s must exceed API timeout %s", DownloadClient.Timeout, GithubClient.Timeout)
	}
}

func TestSelectReleaseAssetWindows(t *testing.T) {
	assets := []GithubAsset{
		{Name: "llama-b10488-bin-win-arm64.zip"},
		{Name: "cudart-llama-bin-win-cuda-13.0-x64.zip"},
		{Name: "llama-b10488-bin-win-cuda-13.0-x64.zip"},
	}
	app := ManagedApp{Name: "llama.cpp", BinaryBase: "llama-server"}
	got, ok := SelectReleaseAsset(assets, app, "cuda13", "windows", "amd64")
	if !ok || got.Name != assets[2].Name {
		t.Fatalf("SelectReleaseAsset() = %q, %v", got.Name, ok)
	}
}

func TestSelectReleaseAssetMacOS(t *testing.T) {
	assets := []GithubAsset{
		{Name: "llama-b10488-bin-win-arm64.zip"},
		{Name: "llama-b10488-bin-macos-arm64.tar.gz"},
		{Name: "llama-b10488-bin-macos-x64.tar.gz"},
	}
	app := ManagedApp{Name: "llama.cpp", BinaryBase: "llama-server"}
	got, ok := SelectReleaseAsset(assets, app, "metal", "darwin", "arm64")
	if !ok || got.Name != assets[1].Name {
		t.Fatalf("SelectReleaseAsset() macOS = %q, %v", got.Name, ok)
	}
}

func TestCUDARTAssetIsNotAnApplicationUpdate(t *testing.T) {
	app := ManagedApp{Name: "llama.cpp", BinaryBase: "llama-server"}
	if score := assetScore("cudart-llama-bin-win-cuda-13.3-x64.zip", app, "cuda13", "windows", "amd64"); score >= 0 {
		t.Fatalf("cudart asset received score %d", score)
	}
}

func TestUnknownVersionDoesNotUpdate(t *testing.T) {
	status := AppStatus{Path: "llama-server.exe", LocalVersion: "unknown", Release: GithubRelease{TagName: "b10488"}}
	if status.NeedsUpdate() {
		t.Fatal("unknown local version must not trigger an automatic update")
	}
}

func TestFormatBytes(t *testing.T) {
	if got := FormatBytes(1024); got != "1.0 KB" {
		t.Fatalf("FormatBytes(1024) = %q", got)
	}
}

func TestAssetScore_ArchMatching(t *testing.T) {
	app := ManagedApp{
		Name:       "llama.cpp",
		BinaryBase: "llama-server",
	}

	tests := []struct {
		name      string
		assetName string
		goos      string
		goarch    string
		variant   string
		want      int // -1 for reject, or > 0 for accept
	}{
		{"amd64 rejects arm64", "llama-b3000-bin-macos-arm64.zip", "darwin", "amd64", "", -1},
		{"amd64 accepts x64", "llama-b3000-bin-windows-x64.zip", "windows", "amd64", "", 12},
		{"arm64 rejects amd64", "llama-b3000-bin-linux-amd64.tar.gz", "linux", "arm64", "", -1},
		{"arm64 accepts arm64", "llama-b3000-bin-macos-arm64.zip", "darwin", "arm64", "metal", 20},
		{"arm64 accepts tgz", "llama-b3000-bin-macos-arm64.tgz", "darwin", "arm64", "metal", 20},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := assetScore(tt.assetName, app, tt.variant, tt.goos, tt.goarch)
			if tt.want == -1 && got != -1 {
				t.Errorf("assetScore() = %v, want -1", got)
			}
			if tt.want > 0 && got <= 0 {
				t.Errorf("assetScore() = %v, want > 0", got)
			}
		})
	}
}

func TestAssetScore_VariantMatching(t *testing.T) {
	app := ManagedApp{
		Name:       "llama.cpp",
		BinaryBase: "llama-server",
	}
	tests := []struct {
		name      string
		assetName string
		goos      string
		goarch    string
		variant   string
		want      int
	}{
		{"cuda12 accepts cuda12", "llama-b3000-bin-windows-cuda12-x64.zip", "windows", "amd64", "cuda12", 20},
		{"cuda12 rejects cpu", "llama-b3000-bin-windows-x64.zip", "windows", "amd64", "cuda12", -1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := assetScore(tt.assetName, app, tt.variant, tt.goos, tt.goarch)
			if tt.want == -1 && got != -1 {
				t.Errorf("assetScore() = %v, want -1", got)
			}
			if tt.want > 0 && got <= 0 {
				t.Errorf("assetScore() = %v, want > 0", got)
			}
		})
	}
}

func TestDownloadWithProgressCleansUpOnFailure(t *testing.T) {
	// 1. 模拟一个非完整传输的异常服务端（ContentLength 100 字节，实际只发 10 字节）
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "100")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("1234567890"))
	}))
	defer server.Close()

	tmpDir := t.TempDir()
	targetPath := filepath.Join(tmpDir, "broken.zip")

	err := DownloadWithProgress(server.URL, targetPath)
	if err == nil {
		t.Fatal("expected DownloadWithProgress to return error on truncated content")
	}

	// 验证残缺文件已被自动清理删除
	if _, statErr := os.Stat(targetPath); !os.IsNotExist(statErr) {
		t.Fatalf("expected broken file to be removed, but it still exists: %v", statErr)
	}

	// 2. 模拟正常服务端，验证正常下载成功保留
	goodServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data := []byte("complete package content")
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(data)
	}))
	defer goodServer.Close()

	goodPath := filepath.Join(tmpDir, "good.zip")
	goodErr := DownloadWithProgress(goodServer.URL, goodPath)
	if goodErr != nil {
		t.Fatalf("expected download to succeed, got: %v", goodErr)
	}
	if _, statErr := os.Stat(goodPath); statErr != nil {
		t.Fatalf("expected good file to exist, got: %v", statErr)
	}
}

func TestUpdateAppByName_NotFound(t *testing.T) {
	_, err := UpdateAppByName("non-existent-app-12345", "test-svc", false)
	if err == nil || !strings.Contains(err.Error(), "未找到受管应用") {
		t.Errorf("expected error for nonexistent app, got: %v", err)
	}
}

func TestDefaultInstallDir(t *testing.T) {
	dir := DefaultInstallDir()
	if filepath.Base(dir) != "bin" {
		t.Errorf("expected DefaultInstallDir to end in 'bin', got: %s", dir)
	}
}

func TestEnsureBootstrapConfig(t *testing.T) {
	tmpDir := t.TempDir()

	if err := EnsureBootstrapConfig(tmpDir); err != nil {
		t.Fatalf("unexpected error creating bootstrap config: %v", err)
	}

	cfgPath := filepath.Join(tmpDir, "config.yaml")
	content, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("expected config.yaml to exist: %v", err)
	}

	if !strings.Contains(string(content), "port: 8080") {
		t.Errorf("expected default port 8080 in config.yaml")
	}
	if !strings.Contains(string(content), "latest-llama") {
		t.Errorf("expected latest-llama macro in config.yaml")
	}

	modelsDir := filepath.Join(tmpDir, "models")
	if info, err := os.Stat(modelsDir); err != nil || !info.IsDir() {
		t.Errorf("expected models directory to be created")
	}

	// 验证候选目录存在配置时，绝不在 targetDir 生成新的配置进行抢占
	targetDir := filepath.Join(tmpDir, "new_target")
	candidateDir := filepath.Join(tmpDir, "existing_candidate")
	_ = os.MkdirAll(targetDir, 0755)
	_ = os.MkdirAll(candidateDir, 0755)
	_ = os.WriteFile(filepath.Join(candidateDir, "config.yml"), []byte("port: 7070\n"), 0644)

	if err := EnsureBootstrapConfig(targetDir, candidateDir); err != nil {
		t.Fatalf("EnsureBootstrapConfig returned error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(targetDir, "config.yaml")); err == nil {
		t.Errorf("EnsureBootstrapConfig created config.yaml in targetDir despite existing config in candidateDir")
	}
}

func TestHasUpdatesOrInstalls(t *testing.T) {
	uninstalled := []AppStatus{
		{Path: ""},
	}
	if !hasUpdatesOrInstalls(uninstalled) {
		t.Errorf("expected uninstalled app to require update or install")
	}

	upToDate := []AppStatus{
		{Path: "bin/llama-swap.exe", LocalVersion: "v1.0.0", Release: GithubRelease{TagName: "v1.0.0"}},
	}
	if hasUpdatesOrInstalls(upToDate) {
		t.Errorf("expected up to date app not to require update or install")
	}
}

func TestProgressState(t *testing.T) {
	app := "test-progress-app"
	defer ClearProgress(app)

	// 初始状态应为 idle
	initP := GetProgress(app)
	if initP.Stage != "idle" {
		t.Errorf("expected stage 'idle', got %q", initP.Stage)
	}

	// 更新进度
	SetProgress(app, "downloading", 250, 1000, nil)
	p := GetProgress(app)
	if p.Stage != "downloading" || p.Percent != 25 || p.ReadBytes != 250 || p.TotalBytes != 1000 {
		t.Errorf("unexpected progress: %+v", p)
	}

	// 清理
	ClearProgress(app)
	pCleared := GetProgress(app)
	if pCleared.Stage != "idle" {
		t.Errorf("expected cleared stage 'idle', got %q", pCleared.Stage)
	}
}

func TestUpdateAppByName_InvalidAppDoesNotPolluteProgress(t *testing.T) {
	invalidApp := "invalid-app-xyz-not-exist"
	_, err := UpdateAppByName(invalidApp, "mock-service", false)
	if err == nil {
		t.Fatalf("expected error for invalid app name")
	}

	p := GetProgress(invalidApp)
	if p.Stage != "idle" {
		t.Errorf("expected stage 'idle' for invalid app, got %q", p.Stage)
	}
}

func TestProgressState_Warning(t *testing.T) {
	app := "test-warning-app"
	defer ClearProgress(app)

	SetProgress(app, "warning", 100, 100, errors.New("service failed to restart"))
	p := GetProgress(app)
	if p.Stage != "warning" {
		t.Errorf("expected stage 'warning', got %q", p.Stage)
	}
	if p.Percent != 100 {
		t.Errorf("expected percent 100, got %d", p.Percent)
	}
	if p.ErrorMsg != "service failed to restart" {
		t.Errorf("expected error msg 'service failed to restart', got %q", p.ErrorMsg)
	}
}

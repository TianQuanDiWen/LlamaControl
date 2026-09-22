package updater

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
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


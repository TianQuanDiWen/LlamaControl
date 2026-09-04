package updater

import (
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

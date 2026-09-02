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

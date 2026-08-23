package main

import "testing"

func TestDownloadTimeoutExceedsAPITimeout(t *testing.T) {
	if downloadClient.Timeout <= githubClient.Timeout {
		t.Fatalf("download timeout %s must exceed API timeout %s", downloadClient.Timeout, githubClient.Timeout)
	}
}

func TestExtractLlamaCppBuildVersion(t *testing.T) {
	tests := []struct {
		name string
		text string
		want string
	}{
		{"new output", "version: 0.1.2-dev\nbuild: 10488 (abcdef0)", "b10488"},
		{"current output", "version: 0.1.2-dev (build 10483, commit 27e345b57)\nbuilt with Clang 20.1.8 for Windows x86_64", "b10483"},
		{"version suffix", "version: 0.1.2-dev (b10488)\nbuilt with MSVC", "b10488"},
		{"old output", "version: 10488 (abcdef0)\nbuilt with MSVC", "b10488"},
		{"standalone tag", "llama.cpp b10488", "b10488"},
		{"no build", "version: 0.1.2-dev", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := extractLlamaCppBuildVersion(tt.text); got != tt.want {
				t.Fatalf("extractLlamaCppBuildVersion() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCompareLlamaCppBuildTags(t *testing.T) {
	if got := compareVersionStrings("b10488", "b10488"); got != 0 {
		t.Fatalf("equal builds compared as %d", got)
	}
	if got := compareVersionStrings("b10487", "b10488"); got >= 0 {
		t.Fatalf("older local build compared as %d", got)
	}
}

func TestSelectReleaseAsset(t *testing.T) {
	assets := []githubAsset{
		{Name: "llama-b10488-bin-win-arm64.zip"},
		{Name: "cudart-llama-bin-win-cuda-13.0-x64.zip"},
		{Name: "llama-b10488-bin-win-cuda-13.0-x64.zip"},
	}
	app := managedApp{Name: "llama.cpp", BinaryBase: "llama-server"}
	got, ok := selectReleaseAsset(assets, app, "cuda13", "windows", "amd64")
	if !ok || got.Name != assets[2].Name {
		t.Fatalf("selectReleaseAsset() = %q, %v", got.Name, ok)
	}
}

func TestCUDARTAssetIsNotAnApplicationUpdate(t *testing.T) {
	app := managedApp{Name: "llama.cpp", BinaryBase: "llama-server"}
	if score := assetScore("cudart-llama-bin-win-cuda-13.3-x64.zip", app, "cuda13", "windows", "amd64"); score >= 0 {
		t.Fatalf("cudart asset received score %d", score)
	}
}

func TestUnknownVersionDoesNotUpdate(t *testing.T) {
	status := appStatus{Path: "llama-server.exe", LocalVersion: "unknown", Release: githubRelease{TagName: "b10488"}}
	if status.needsUpdate() {
		t.Fatal("unknown local version must not trigger an automatic update")
	}
}

func TestFormatBytes(t *testing.T) {
	if got := formatBytes(1024); got != "1.0 KB" {
		t.Fatalf("formatBytes(1024) = %q", got)
	}
}

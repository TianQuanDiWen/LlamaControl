package updater

import "testing"

func TestExtractLlamaCppVersion(t *testing.T) {
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
			if got := ExtractLlamaCppVersion(tt.text); got != tt.want {
				t.Fatalf("ExtractLlamaCppVersion() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCompareVersions(t *testing.T) {
	if got := CompareVersions("b10488", "b10488"); got != 0 {
		t.Fatalf("equal builds compared as %d", got)
	}
	if got := CompareVersions("b10487", "b10488"); got >= 0 {
		t.Fatalf("older local build compared as %d", got)
	}
	if got := CompareVersions("1.2.0", "1.10.0"); got >= 0 {
		t.Fatalf("1.2.0 should be less than 1.10.0, got %d", got)
	}
}

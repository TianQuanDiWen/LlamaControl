package config

import "testing"

func TestParseSwapPort(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    int
		ok      bool
	}{
		{"standard port", "port: 11451", 11451, true},
		{"string port", "port: \"8080\"", 8080, true},
		{"listen address", "listen: 0.0.0.0:9090", 9090, true},
		{"listen string", "listen: \":8080\"", 8080, true},
		{"invalid port format", "port: abc", 0, false},
		{"no port", "server: true\n# port: 8080", 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ParseSwapPort([]byte(tt.content))
			if got != tt.want || ok != tt.ok {
				t.Fatalf("ParseSwapPort() = %d, %v; want %d, %v", got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestDetectSwapPortFallback(t *testing.T) {
	if got := DetectSwapPort("non_existent_dir_12345"); got != 8080 {
		t.Fatalf("DetectSwapPort() = %d; want 8080", got)
	}
}

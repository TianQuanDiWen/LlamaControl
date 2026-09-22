package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestWebRootHandler(t *testing.T) {
	server := NewServer(Config{
		ServiceName: "test-service",
		SwapPort:    8080,
		Addr:        "127.0.0.1:0",
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	server.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "LlamaControl") {
		t.Errorf("expected body to contain LlamaControl, got %s", body)
	}
}

func TestWebStatusAPI(t *testing.T) {
	server := NewServer(Config{
		ServiceName: "test-service",
		SwapPort:    9999,
		ConfigFile:  "custom.yaml",
		Addr:        "127.0.0.1:0",
	})

	req := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	w := httptest.NewRecorder()
	server.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", w.Code)
	}

	var resp StatusResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode status response: %v", err)
	}

	if resp.Port != 9999 {
		t.Errorf("expected port 9999, got %d", resp.Port)
	}
	if resp.ConfigFile != "custom.yaml" {
		t.Errorf("expected configFile custom.yaml, got %s", resp.ConfigFile)
	}
	if resp.ServiceName != "test-service" {
		t.Errorf("expected serviceName test-service, got %s", resp.ServiceName)
	}
}

func TestSwapReverseProxyForwarding(t *testing.T) {
	// 1. 模拟本地的 llama-swap 实例
	var receivedPath string
	mockSwap := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"models": ["qwen-7b"]}`))
	}))
	defer mockSwap.Close()

	u, _ := url.Parse(mockSwap.URL)
	port, _ := strconv.Atoi(u.Port())

	// 2. 初始化反向代理服务
	server := NewServer(Config{
		ServiceName: "test-service",
		SwapPort:    port,
		Addr:        "127.0.0.1:0",
	})

	// 3. 模拟浏览器请求 /swap/v1/models
	req := httptest.NewRequest(http.MethodGet, "/swap/v1/models", nil)
	w := httptest.NewRecorder()
	server.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected proxy status 200, got %d", w.Code)
	}

	// 4. 验证 /swap 前缀已被透明剥离，上游实际收到的是 /v1/models
	if receivedPath != "/v1/models" {
		t.Errorf("expected mockSwap to receive /v1/models, got %s", receivedPath)
	}
	if !strings.Contains(w.Body.String(), "qwen-7b") {
		t.Errorf("unexpected body from proxy: %s", w.Body.String())
	}
}

func TestResolveWebAddr(t *testing.T) {
	// 1. 无冲突正常解析
	addr, err := ResolveWebAddr("127.0.0.1:11452", 8080, false)
	if err != nil || addr != "127.0.0.1:11452" {
		t.Fatalf("expected 127.0.0.1:11452, got %s (err: %v)", addr, err)
	}

	// 2. 默认端口与 swapPort 冲突，自动顺延避让
	addr, err = ResolveWebAddr("127.0.0.1:11451", 11451, false)
	if err != nil || addr != "127.0.0.1:11452" {
		t.Fatalf("expected 127.0.0.1:11452, got %s (err: %v)", addr, err)
	}

	// 3. 用户显式指定端口与 swapPort 冲突，必须报错拦截
	_, err = ResolveWebAddr("127.0.0.1:11451", 11451, true)
	if err == nil {
		t.Fatalf("expected error for explicit port conflict, got nil")
	}

	// 4. 纯端口数字支持
	addr, err = ResolveWebAddr("9999", 8080, false)
	if err != nil || addr != "127.0.0.1:9999" {
		t.Fatalf("expected 127.0.0.1:9999, got %s (err: %v)", addr, err)
	}
}

func TestDirectV1ProxyForwarding(t *testing.T) {
	var receivedPath string
	mockSwap := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices": [{"text": "hello"}]}`))
	}))
	defer mockSwap.Close()

	u, _ := url.Parse(mockSwap.URL)
	port, _ := strconv.Atoi(u.Port())

	server := NewServer(Config{
		ServiceName: "test-service",
		SwapPort:    port,
		Addr:        "127.0.0.1:0",
	})

	// 直连 /v1/chat/completions
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	w := httptest.NewRecorder()
	server.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected direct proxy status 200, got %d", w.Code)
	}
	if receivedPath != "/v1/chat/completions" {
		t.Errorf("expected mockSwap to receive /v1/chat/completions, got %s", receivedPath)
	}
	if !strings.Contains(w.Body.String(), "hello") {
		t.Errorf("unexpected body from direct proxy: %s", w.Body.String())
	}
}

func TestReadTailLines(t *testing.T) {
	tmpDir := t.TempDir()

	// 1. 测试空文件
	emptyPath := filepath.Join(tmpDir, "empty.log")
	if err := os.WriteFile(emptyPath, []byte(""), 0644); err != nil {
		t.Fatalf("failed to create empty file: %v", err)
	}
	emptyRes, err := readTailLines(emptyPath, 10)
	if err != nil {
		t.Fatalf("unexpected error on empty file: %v", err)
	}
	if emptyRes != "" {
		t.Fatalf("expected empty string, got %q", emptyRes)
	}

	// 2. 测试 200 行的文件截取最后 10 行
	multiLinePath := filepath.Join(tmpDir, "multiline.log")
	var sb strings.Builder
	for i := 0; i < 200; i++ {
		sb.WriteString(fmt.Sprintf("log line %03d\n", i))
	}
	if err := os.WriteFile(multiLinePath, []byte(sb.String()), 0644); err != nil {
		t.Fatalf("failed to write multiline file: %v", err)
	}

	tail10, err := readTailLines(multiLinePath, 10)
	if err != nil {
		t.Fatalf("unexpected error reading tail: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(tail10), "\n")
	if len(lines) != 10 {
		t.Fatalf("expected 10 lines, got %d: %v", len(lines), lines)
	}
	if lines[0] != "log line 190" {
		t.Errorf("expected first line to be 'log line 190', got %q", lines[0])
	}
	if lines[9] != "log line 199" {
		t.Errorf("expected last line to be 'log line 199', got %q", lines[9])
	}

	// 3. 测试请求行数超过文件总行数
	tailAll, err := readTailLines(multiLinePath, 500)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	allLines := strings.Split(strings.TrimSpace(tailAll), "\n")
	if len(allLines) != 200 {
		t.Fatalf("expected 200 lines, got %d", len(allLines))
	}
	if allLines[0] != "log line 000" {
		t.Errorf("expected first line to be 'log line 000', got %q", allLines[0])
	}
}

func TestStaticCssAndHtmlOffline(t *testing.T) {
	server := NewServer(Config{
		ServiceName: "test-service",
		SwapPort:    8080,
		Addr:        "127.0.0.1:0",
	})

	// 1. 验证 embed 本地 style.css 正常被提供
	reqCSS := httptest.NewRequest(http.MethodGet, "/style.css", nil)
	wCSS := httptest.NewRecorder()
	server.mux.ServeHTTP(wCSS, reqCSS)

	if wCSS.Code != http.StatusOK {
		t.Fatalf("expected /style.css status 200, got %d", wCSS.Code)
	}
	if !strings.Contains(wCSS.Body.String(), "LlamaControl Standalone Dark Theme") {
		t.Errorf("expected style.css content, got: %s", wCSS.Body.String())
	}

	// 2. 验证 index.html 完全移除了 Tailwind CDN，改用 /style.css
	reqHTML := httptest.NewRequest(http.MethodGet, "/", nil)
	wHTML := httptest.NewRecorder()
	server.mux.ServeHTTP(wHTML, reqHTML)

	if wHTML.Code != http.StatusOK {
		t.Fatalf("expected / status 200, got %d", wHTML.Code)
	}
	htmlBody := wHTML.Body.String()
	if strings.Contains(htmlBody, "cdn.tailwindcss.com") {
		t.Errorf("index.html should NOT contain cdn.tailwindcss.com")
	}
	if !strings.Contains(htmlBody, `href="/style.css"`) {
		t.Errorf("index.html must reference /style.css")
	}
}


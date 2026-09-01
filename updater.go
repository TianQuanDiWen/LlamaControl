package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

type githubRelease struct {
	TagName string        `json:"tag_name"`
	Assets  []githubAsset `json:"assets"`
}

type githubAsset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
}

type managedApp struct {
	Name          string
	Repo          string
	BinaryBase    string
	Parse         func(string) string
	Variant       func(string, string) string
	NewestRelease bool
}

type appStatus struct {
	App          managedApp
	Path         string
	LocalVersion string
	Variant      string
	Release      githubRelease
	CheckErr     error
}

var managedApps = []managedApp{
	{Name: "llama.cpp", Repo: "ggml-org/llama.cpp", BinaryBase: "llama-server", Parse: extractLlamaCppVersion, Variant: detectLlamaCppVariant, NewestRelease: true},
	{Name: "llama-swap", Repo: "mostlygeek/llama-swap", BinaryBase: "llama-swap", Parse: extractLlamaSwapVersion},
}

var (
	githubClient   = &http.Client{Timeout: 30 * time.Second}
	downloadClient = &http.Client{Timeout: 30 * time.Minute}
)

func updateManagedApps(reader *bufio.Reader) {
	statuses := make([]appStatus, 0, len(managedApps))
	for _, app := range managedApps {
		status := inspectApp(app)
		statuses = append(statuses, status)
		printAppStatus(status)
	}

	if !hasUpdates(statuses) {
		fmt.Println("没有可用更新。")
		waitForEnter()
		return
	}
	fmt.Print("是否继续更新? (y/n): ")
	choice, err := reader.ReadString('\n')
	if err != nil || !strings.EqualFold(strings.TrimSpace(choice), "y") {
		fmt.Println("已取消更新。")
		waitForEnter()
		return
	}

	serviceWasRunning, statusErr := platformServiceRunning(serviceName)
	if statusErr != nil {
		fmt.Println("无法确认服务状态，已取消更新:", statusErr)
		waitForEnter()
		return
	}
	if serviceWasRunning {
		fmt.Printf("正在停止 %s 服务以安全更新...\n", serviceName)
		if err := stopPlatformService(serviceName); err != nil {
			fmt.Println("停止服务失败，已取消更新:", err)
			waitForEnter()
			return
		}
	}

	for _, status := range statuses {
		if !status.needsUpdate() {
			continue
		}
		fmt.Printf("[更新] %s %s -> %s\n", status.App.Name, status.LocalVersion, status.Release.TagName)
		if err := installRelease(status); err != nil {
			fmt.Println("  失败:", err)
		} else {
			fmt.Println("  完成")
		}
	}
	if serviceWasRunning {
		fmt.Printf("正在恢复 %s 服务...\n", serviceName)
		if err := startPlatformService(serviceName); err != nil {
			fmt.Println("服务恢复失败，请手动启动:", err)
		} else {
			fmt.Println("服务已重新启动。")
		}
	}
	waitForEnter()
}

func inspectApp(app managedApp) appStatus {
	status := appStatus{App: app}
	status.Path, status.LocalVersion, status.Variant = inspectLocalBinary(app)
	if status.Path == "" {
		return status
	}
	status.Release, status.CheckErr = fetchLatestRelease(app)
	return status
}

func (s appStatus) needsUpdate() bool {
	return s.Path != "" && s.CheckErr == nil && s.LocalVersion != "" && s.LocalVersion != "unknown" && compareVersions(s.LocalVersion, s.Release.TagName) < 0
}

func printAppStatus(status appStatus) {
	fmt.Printf("\n[%s]\n", status.App.Name)
	if status.Path == "" {
		fmt.Println("  本地未找到可执行文件")
		return
	}
	fmt.Println("  本地版本:", status.LocalVersion)
	if status.Variant != "" {
		fmt.Println("  构建分支:", formatVariant(status.Variant))
	}
	if status.CheckErr != nil {
		fmt.Println("  最新版本: 获取失败:", status.CheckErr)
		return
	}
	fmt.Println("  最新版本:", status.Release.TagName)
	fmt.Println("  需要更新:", status.needsUpdate())
}

func hasUpdates(statuses []appStatus) bool {
	for _, status := range statuses {
		if status.needsUpdate() {
			return true
		}
	}
	return false
}

func inspectLocalBinary(app managedApp) (path, version, variant string) {
	for _, candidate := range binaryCandidates(app) {
		info, err := os.Stat(candidate)
		if err != nil || info.IsDir() {
			continue
		}
		output := binaryVersionOutput(candidate)
		version = app.Parse(output)
		if version == "" {
			version = versionFromFilename(candidate)
		}
		if version == "" {
			version = "unknown"
		}
		if app.Variant != nil {
			variant = app.Variant(candidate, output)
		}
		return candidate, version, variant
	}
	return "", "", ""
}

func binaryCandidates(app managedApp) []string {
	dirs := append([]string{executableDir(), filepath.Join(executableDir(), "bin")}, platformDefaultDirs()...)
	seen := make(map[string]bool)
	var candidates []string
	for _, dir := range dirs {
		for _, name := range executableNames(app.BinaryBase) {
			path := filepath.Clean(filepath.Join(dir, name))
			key := strings.ToLower(path)
			if dir != "" && !seen[key] {
				seen[key] = true
				candidates = append(candidates, path)
			}
		}
	}
	for _, name := range executableNames(app.BinaryBase) {
		paths, _ := platformSearchPath(name)
		for _, path := range paths {
			key := strings.ToLower(filepath.Clean(path))
			if !seen[key] {
				seen[key] = true
				candidates = append(candidates, path)
			}
		}
	}
	return candidates
}

func binaryVersionOutput(path string) string {
	for _, args := range [][]string{{"--version"}, {"-version"}, {"-V"}} {
		if out, err := commandOutput(path, args...); err == nil && out != "" {
			return out
		}
	}
	return ""
}

func executableDir() string {
	path, err := os.Executable()
	if err != nil {
		return "."
	}
	dir, err := filepath.Abs(filepath.Dir(path))
	if err != nil {
		return filepath.Dir(path)
	}
	return dir
}

func fetchLatestRelease(app managedApp) (githubRelease, error) {
	endpoint := "/releases/latest"
	if app.NewestRelease {
		// GitHub 的 /releases/latest 端点会刻意排除预发布版本 (pre-releases)。
		// 这里改为获取最新发布的 llama.cpp 版本，无论其标签是稳定版、
		// 构建号，还是未来的其他标签格式。
		endpoint = "/releases?per_page=1"
	}
	url := "https://api.github.com/repos/" + app.Repo + endpoint
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return githubRelease{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "llama-control")
	resp, err := githubClient.Do(req)
	if err != nil {
		return githubRelease{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return githubRelease{}, fmt.Errorf("GitHub API 返回 %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var release githubRelease
	if app.NewestRelease {
		var releases []githubRelease
		if err := json.NewDecoder(resp.Body).Decode(&releases); err != nil {
			return githubRelease{}, err
		}
		if len(releases) > 0 {
			release = releases[0]
		}
	} else if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return githubRelease{}, err
	}
	if strings.TrimSpace(release.TagName) == "" {
		return githubRelease{}, fmt.Errorf("Release 缺少版本标签")
	}
	return release, nil
}

func installRelease(status appStatus) error {
	asset, ok := selectReleaseAsset(status.Release.Assets, status.App, status.Variant, runtime.GOOS, runtime.GOARCH)
	if !ok {
		return fmt.Errorf("未找到适合 %s/%s 的发布资源", runtime.GOOS, runtime.GOARCH)
	}
	dir := filepath.Dir(status.Path)
	downloadPath := filepath.Join(dir, "_update_"+filepath.Base(asset.Name))
	defer os.Remove(downloadPath)
	if err := downloadWithProgress(asset.URL, downloadPath); err != nil {
		return err
	}
	unpackDir, err := os.MkdirTemp(dir, "_update_unpack_")
	if err != nil {
		return err
	}
	defer os.RemoveAll(unpackDir)
	updated, err := extractPackage(downloadPath, unpackDir, executableNames(status.App.BinaryBase))
	if err != nil {
		return err
	}
	fmt.Println("  资源:", asset.Name)
	return replacePackageSafely(filepath.Dir(updated), filepath.Dir(status.Path), filepath.Base(updated), func(path string) error {
		version := status.App.Parse(binaryVersionOutput(path))
		if version == "" {
			return fmt.Errorf("更新后无法读取版本")
		}
		if compareVersions(version, status.Release.TagName) != 0 {
			return fmt.Errorf("更新后版本为 %s，预期 %s", version, status.Release.TagName)
		}
		return nil
	})
}

func selectReleaseAsset(assets []githubAsset, app managedApp, variant, goos, goarch string) (githubAsset, bool) {
	bestScore, best := -1, githubAsset{}
	for _, asset := range assets {
		name := strings.ToLower(asset.Name)
		score := assetScore(name, app, variant, goos, goarch)
		if score > bestScore {
			bestScore, best = score, asset
		}
	}
	return best, bestScore >= 0
}

func assetScore(name string, app managedApp, variant, goos, goarch string) int {
	if !strings.Contains(name, ".zip") && !strings.Contains(name, ".tar.gz") && !strings.HasSuffix(name, ".exe") {
		return -1
	}
	score := 0
	if strings.Contains(name, strings.ToLower(app.BinaryBase)) || strings.Contains(name, strings.ToLower(app.Name)) {
		score += 4
	}
	osTokens := map[string][]string{"windows": {"windows", "win"}, "darwin": {"macos", "darwin", "osx"}, "linux": {"linux", "ubuntu"}}
	if !containsAny(name, osTokens[goos]...) {
		return -1
	}
	if goos == "windows" && strings.Contains(name, "darwin") {
		return -1
	}
	score += 5
	archTokens := map[string][]string{"amd64": {"x64", "amd64", "x86_64"}, "arm64": {"arm64", "aarch64"}}
	if containsAny(name, archTokens[goarch]...) {
		score += 3
	}
	if goarch == "amd64" && containsAny(name, "arm64", "aarch64") {
		return -1
	}
	if variant != "" {
		if app.Name == "llama.cpp" && strings.HasPrefix(name, "cudart-") {
			return -1
		}
		if app.Name == "llama.cpp" && strings.HasPrefix(variant, "cuda") && !strings.Contains(name, "cuda") {
			return -1
		}
		if strings.Contains(compactToken(name), compactToken(variant)) {
			score += 8
		} else if strings.Contains(name, "cuda") {
			score -= 4
		}
	}
	if strings.Contains(name, "cudart") || strings.Contains(name, "dependency") {
		score -= 6
	}
	return score
}

func compactToken(value string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			return r
		}
		if r >= 'A' && r <= 'Z' {
			return r + ('a' - 'A')
		}
		return -1
	}, value)
}

func containsAny(value string, tokens ...string) bool {
	for _, token := range tokens {
		if token != "" && strings.Contains(value, token) {
			return true
		}
	}
	return false
}

func downloadWithProgress(url, path string) error {
	resp, err := downloadClient.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("下载失败，HTTP %d", resp.StatusCode)
	}
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()
	written, err := io.Copy(file, &progressReader{reader: resp.Body, total: resp.ContentLength})
	if err == nil {
		fmt.Printf("\r已下载 %s                         \n", formatBytes(written))
	}
	return err
}

type progressReader struct {
	reader      io.Reader
	total, read int64
}

func (p *progressReader) Read(buf []byte) (int, error) {
	n, err := p.reader.Read(buf)
	p.read += int64(n)
	if p.total > 0 {
		fmt.Printf("\r下载中... %3d%% (%s/%s)", p.read*100/p.total, formatBytes(p.read), formatBytes(p.total))
	} else {
		fmt.Printf("\r下载中... %s", formatBytes(p.read))
	}
	return n, err
}

func formatBytes(n int64) string {
	if n < 1024 {
		return fmt.Sprintf("%d B", n)
	}
	units := []string{"KB", "MB", "GB", "TB"}
	value := float64(n) / 1024
	unit := 0
	for value >= 1024 && unit < len(units)-1 {
		value /= 1024
		unit++
	}
	return fmt.Sprintf("%.1f %s", value, units[unit])
}

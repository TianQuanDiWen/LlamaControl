package updater

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"llama-control/internal/fsutil"
	"llama-control/internal/platform"
)

// ---- 数据模型 ----

// GithubRelease 映射 GitHub API 返回的 Release 结构
type GithubRelease struct {
	TagName string        `json:"tag_name"`
	Assets  []GithubAsset `json:"assets"`
}

// GithubAsset 映射 GitHub API 返回的资产文件信息
type GithubAsset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
}

// ManagedApp 定义了一个受管应用程序的特征与行为
type ManagedApp struct {
	Name          string                      // 应用的显示名称，如 "llama.cpp"
	Repo          string                      // GitHub 仓库路径，如 "ggml-org/llama.cpp"
	BinaryBase    string                      // 可执行文件的基础名称，如 "llama-server"
	Parse         func(string) string         // 用于从终端输出文本中解析版本号的回调函数
	Variant       func(string, string) string // 用于探测当前环境/文件变体的回调函数 (可选)
	NewestRelease bool                        // 是否始终拉取最新的 release（包括 pre-release）
}

// AppStatus 承载了受管应用程序在本地的安装及更新状态
type AppStatus struct {
	App          ManagedApp    // 绑定的受管应用
	Path         string        // 本地可执行文件的实际路径
	LocalVersion string        // 探测到的本地版本号
	Variant      string        // 探测到的构建分支/变体类型 (如 CUDA 13)
	Release      GithubRelease // 从云端拉取的最新 Release 信息
	CheckErr     error         // 检查更新时可能发生的错误
}

var ManagedApps = []ManagedApp{
	{
		Name:          "llama.cpp",
		Repo:          "ggml-org/llama.cpp",
		BinaryBase:    "llama-server",
		Parse:         ExtractLlamaCppVersion,
		Variant:       platform.DetectLlamaCppVariant,
		NewestRelease: true,
	},
	{
		Name:       "llama-swap",
		Repo:       "mostlygeek/llama-swap",
		BinaryBase: "llama-swap",
		Parse:      ExtractLlamaSwapVersion,
	},
}

// ---- 资产评分 ----

// SelectReleaseAsset 在一组资产文件中，挑选出与当前平台环境及变体要求最匹配的一项
func SelectReleaseAsset(assets []GithubAsset, app ManagedApp, variant, goos, goarch string) (GithubAsset, bool) {
	bestScore, best := -1, GithubAsset{}
	for _, asset := range assets {
		name := strings.ToLower(asset.Name)
		score := assetScore(name, app, variant, goos, goarch)
		if score > bestScore {
			bestScore, best = score, asset
		}
	}
	return best, bestScore >= 0
}

func assetScore(name string, app ManagedApp, variant, goos, goarch string) int {
	if !strings.Contains(name, ".zip") && !strings.Contains(name, ".tar.gz") && !strings.HasSuffix(name, ".exe") {
		return -1
	}
	score := 0
	if strings.Contains(name, strings.ToLower(app.BinaryBase)) || strings.Contains(name, strings.ToLower(app.Name)) {
		score += 4
	}
	osTokens := map[string][]string{
		"windows": {"windows", "win"},
		"darwin":  {"macos", "darwin", "osx"},
		"linux":   {"linux", "ubuntu"},
	}
	if !platform.ContainsAny(name, osTokens[goos]...) {
		return -1
	}
	if goos == "windows" && strings.Contains(name, "darwin") {
		return -1
	}
	score += 5
	archTokens := map[string][]string{
		"amd64": {"x64", "amd64", "x86_64"},
		"arm64": {"arm64", "aarch64"},
	}
	if platform.ContainsAny(name, archTokens[goarch]...) {
		score += 3
	}
	if goarch == "amd64" && platform.ContainsAny(name, "arm64", "aarch64") {
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

// compactToken 去除字符串中所有的非字母数字字符，统一转小写，用于进行模糊匹配
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

// ---- 更新逻辑 ----

// NeedsUpdate 评估该应用状态是否说明当前具备可用且未安装的更新
func (s AppStatus) NeedsUpdate() bool {
	return s.Path != "" && s.CheckErr == nil && s.LocalVersion != "" && s.LocalVersion != "unknown" && CompareVersions(s.LocalVersion, s.Release.TagName) < 0
}

// UpdateManagedApps 执行全局应用更新流程：探测、提示用户、停止服务、下载替换，并恢复服务
func UpdateManagedApps(reader *bufio.Reader, serviceName string) {
	statuses := make([]AppStatus, 0, len(ManagedApps))
	for _, app := range ManagedApps {
		status := InspectApp(app)
		statuses = append(statuses, status)
		PrintAppStatus(status)
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

	serviceWasRunning, statusErr := platform.ServiceRunning(serviceName)
	if statusErr != nil {
		fmt.Println("无法确认服务状态，已取消更新:", statusErr)
		waitForEnter()
		return
	}
	if serviceWasRunning {
		fmt.Printf("正在停止 %s 服务以安全更新...\n", serviceName)
		if err := platform.StopService(serviceName); err != nil {
			fmt.Println("停止服务失败，已取消更新:", err)
			waitForEnter()
			return
		}
	}

	for _, status := range statuses {
		if !status.NeedsUpdate() {
			continue
		}
		fmt.Printf("[更新] %s %s -> %s\n", status.App.Name, status.LocalVersion, status.Release.TagName)
		if err := InstallRelease(status); err != nil {
			fmt.Println("  失败:", err)
		} else {
			fmt.Println("  完成")
		}
	}
	if serviceWasRunning {
		fmt.Printf("正在恢复 %s 服务...\n", serviceName)
		if err := platform.StartService(serviceName); err != nil {
			fmt.Println("服务恢复失败，请手动启动:", err)
		} else {
			fmt.Println("服务已重新启动。")
		}
	}
	waitForEnter()
}

// InspectApp 检查指定受管应用程序的本地状态和云端最新版本
func InspectApp(app ManagedApp) AppStatus {
	status := AppStatus{App: app}
	status.Path, status.LocalVersion, status.Variant = InspectLocalBinary(app)
	if status.Path == "" {
		return status
	}
	status.Release, status.CheckErr = FetchLatestRelease(app)
	return status
}

// PrintAppStatus 在控制台友善地打印应用探测与版本比对结果
func PrintAppStatus(status AppStatus) {
	fmt.Printf("\n[%s]\n", status.App.Name)
	if status.Path == "" {
		fmt.Println("  本地未找到可执行文件")
		return
	}
	fmt.Println("  本地版本:", status.LocalVersion)
	if status.Variant != "" {
		fmt.Println("  构建分支:", platform.FormatVariant(status.Variant))
	}
	if status.CheckErr != nil {
		fmt.Println("  最新版本: 获取失败:", status.CheckErr)
		return
	}
	fmt.Println("  最新版本:", status.Release.TagName)
	fmt.Println("  需要更新:", status.NeedsUpdate())
}

// hasUpdates 遍历状态数组判定是否至少存在一个应用需要更新
func hasUpdates(statuses []AppStatus) bool {
	for _, status := range statuses {
		if status.NeedsUpdate() {
			return true
		}
	}
	return false
}

// InspectLocalBinary 找出应用最优的本地路径并解析其版本和分支变体
func InspectLocalBinary(app ManagedApp) (path, ver, variant string) {
	for _, candidate := range BinaryCandidates(app) {
		info, err := os.Stat(candidate)
		if err != nil || info.IsDir() {
			continue
		}
		output := BinaryVersionOutput(candidate)
		ver = app.Parse(output)
		if ver == "" {
			ver = VersionFromFilename(candidate)
		}
		if ver == "" {
			ver = "unknown"
		}
		if app.Variant != nil {
			variant = app.Variant(candidate, output)
		}
		return candidate, ver, variant
	}
	return "", "", ""
}

// BinaryCandidates 利用环境、默认位置及 PATH 枚举程序的可执行文件候选路径
func BinaryCandidates(app ManagedApp) []string {
	exeDir := platform.ExecutableDir()
	dirs := append([]string{exeDir, filepath.Join(exeDir, "bin")}, platform.DefaultDirs()...)
	seen := make(map[string]bool)
	var candidates []string
	for _, dir := range dirs {
		for _, name := range platform.ExecutableNames(app.BinaryBase) {
			path := filepath.Clean(filepath.Join(dir, name))
			key := strings.ToLower(path)
			if dir != "" && !seen[key] {
				seen[key] = true
				candidates = append(candidates, path)
			}
		}
	}
	for _, name := range platform.ExecutableNames(app.BinaryBase) {
		paths, _ := platform.SearchPath(name)
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

// BinaryVersionOutput 执行程序并携带 version 相关的标识，试图截取返回的文本内容
func BinaryVersionOutput(path string) string {
	for _, args := range [][]string{{"--version"}, {"-version"}, {"-V"}} {
		if out, err := platform.CommandOutput(path, args...); err == nil && out != "" {
			return out
		}
	}
	return ""
}

// InstallRelease 处理特定应用的下载、解包以及原目录原子替换验证
func InstallRelease(status AppStatus) error {
	asset, ok := SelectReleaseAsset(status.Release.Assets, status.App, status.Variant, runtime.GOOS, runtime.GOARCH)
	if !ok {
		return fmt.Errorf("未找到适合 %s/%s 的发布资源", runtime.GOOS, runtime.GOARCH)
	}
	workDir, err := os.MkdirTemp("", "llama-control-update-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(workDir)
	downloadPath := filepath.Join(workDir, filepath.Base(asset.Name))
	if err := DownloadWithProgress(asset.URL, downloadPath); err != nil {
		return err
	}
	unpackDir := filepath.Join(workDir, "unpack")
	if err := os.Mkdir(unpackDir, 0755); err != nil {
		return err
	}
	updated, err := fsutil.ExtractPackage(downloadPath, unpackDir, platform.ExecutableNames(status.App.BinaryBase))
	if err != nil {
		return err
	}
	fmt.Println("  资源:", asset.Name)
	return fsutil.ReplacePackageSafely(filepath.Dir(updated), filepath.Dir(status.Path), filepath.Base(updated), func(path string) error {
		v := status.App.Parse(BinaryVersionOutput(path))
		if v == "" {
			return fmt.Errorf("更新后无法读取版本")
		}
		if CompareVersions(v, status.Release.TagName) != 0 {
			return fmt.Errorf("更新后版本为 %s，预期 %s", v, status.Release.TagName)
		}
		return nil
	})
}

// waitForEnter 暂停执行以便用户可以观察终端信息
func waitForEnter() {
	fmt.Print("按 Enter 键继续...")
	_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
}

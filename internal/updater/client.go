package updater

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

var (
	GithubClient   = &http.Client{Timeout: 30 * time.Second}
	DownloadClient = &http.Client{Timeout: 30 * time.Minute}
)

// FetchLatestRelease 调用 GitHub API 获取指定仓库的最新发布版本
func FetchLatestRelease(app ManagedApp) (GithubRelease, error) {
	endpoint := "/releases/latest"
	if app.NewestRelease {
		endpoint = "/releases?per_page=1"
	}
	url := "https://api.github.com/repos/" + app.Repo + endpoint
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return GithubRelease{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "llama-control")
	resp, err := GithubClient.Do(req)
	if err != nil {
		return GithubRelease{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return GithubRelease{}, fmt.Errorf("GitHub API 返回 %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var release GithubRelease
	if app.NewestRelease {
		var releases []GithubRelease
		if err := json.NewDecoder(resp.Body).Decode(&releases); err != nil {
			return GithubRelease{}, err
		}
		if len(releases) > 0 {
			release = releases[0]
		}
	} else if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return GithubRelease{}, err
	}
	if strings.TrimSpace(release.TagName) == "" {
		return GithubRelease{}, fmt.Errorf("Release 缺少版本标签")
	}
	return release, nil
}

// DownloadWithProgress 带控制台进度条的下载器，将 URL 下载到指定本地路径（支持镜像加速 fallback，失败时自动清理残缺文件）
func DownloadWithProgress(url, path string) (downloadErr error) {
	resp, err := DownloadClient.Get(url)
	if (err != nil || resp.StatusCode < 200 || resp.StatusCode >= 300) && !strings.HasPrefix(url, "https://ghproxy.net/") {
		if resp != nil {
			_ = resp.Body.Close()
		}
		mirrorURL := "https://ghproxy.net/" + url
		fmt.Printf("\n直连下载异常，正在尝试加速镜像: %s\n", mirrorURL)
		resp, err = DownloadClient.Get(mirrorURL)
	}
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
	defer func() {
		_ = file.Close()
		if downloadErr != nil {
			_ = os.Remove(path)
		}
	}()

	written, err := io.Copy(file, &progressReader{reader: resp.Body, total: resp.ContentLength})
	if err != nil {
		downloadErr = err
		return err
	}
	if resp.ContentLength > 0 && written < resp.ContentLength {
		downloadErr = fmt.Errorf("下载未完成: 预期 %d 字节，实际仅获取 %d 字节", resp.ContentLength, written)
		return downloadErr
	}

	fmt.Printf("\r已下载 %s                         \n", FormatBytes(written))
	return nil
}

type progressReader struct {
	reader      io.Reader
	total, read int64
}

// Read 实现 io.Reader 并在读取时向控制台打印下载进度
func (p *progressReader) Read(buf []byte) (int, error) {
	n, err := p.reader.Read(buf)
	p.read += int64(n)
	if p.total > 0 {
		fmt.Printf("\r下载中... %3d%% (%s/%s)", p.read*100/p.total, FormatBytes(p.read), FormatBytes(p.total))
	} else {
		fmt.Printf("\r下载中... %s", FormatBytes(p.read))
	}
	return n, err
}

// FormatBytes 将字节数转换为带单位的友好字符串显示 (KB/MB/GB/TB)
func FormatBytes(n int64) string {
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

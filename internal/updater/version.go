package updater

import (
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

var (
	llamaBuildPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)\bbuild(?:\s+(?:number|version))?\s*[:=]?\s*b?(\d{4,})\b`),
		regexp.MustCompile(`(?im)^\s*version\s*:\s*b?(\d{4,})\b`),
		regexp.MustCompile(`(?i)\(\s*b?(\d{4,})\s*(?:[,)]|$)`),
		regexp.MustCompile(`(?i)\bb(\d{4,})\b`),
	}
	llamaSwapPattern       = regexp.MustCompile(`(?i)version\s*:\s*v?(\d+)`)
	filenameVersionPattern = regexp.MustCompile(`(?i)(?:b|build|#)(\d{4,})|\b(\d+\.\d+(?:\.\d+)?)\b`)
	numberPattern          = regexp.MustCompile(`\d+`)
)

// ExtractLlamaCppVersion 从输出文本中提取 llama.cpp 的 bXXXX 构建版本号
func ExtractLlamaCppVersion(text string) string {
	for _, pattern := range llamaBuildPatterns {
		if match := pattern.FindStringSubmatch(text); len(match) > 1 {
			return "b" + match[1]
		}
	}
	return ""
}

// ExtractLlamaSwapVersion 从输出文本中提取 llama-swap 的版本号
func ExtractLlamaSwapVersion(text string) string {
	if match := llamaSwapPattern.FindStringSubmatch(text); len(match) > 1 {
		return match[1]
	}
	return ""
}

// VersionFromFilename 从文件名中尝试提取版本号
func VersionFromFilename(path string) string {
	match := filenameVersionPattern.FindStringSubmatch(strings.ToLower(filepath.Base(path)))
	if len(match) == 0 {
		return ""
	}
	for _, value := range match[1:] {
		if value != "" {
			return value
		}
	}
	return ""
}

// CompareVersions 比较两个多段数字或构建版本号
// 返回 -1 (local < remote), 0 (local == remote), 1 (local > remote)
func CompareVersions(local, remote string) int {
	a, b := VersionNumbers(local), VersionNumbers(remote)
	for i := 0; i < max(len(a), len(b)); i++ {
		av, bv := 0, 0
		if i < len(a) {
			av = a[i]
		}
		if i < len(b) {
			bv = b[i]
		}
		if av < bv {
			return -1
		}
		if av > bv {
			return 1
		}
	}
	return 0
}

// VersionNumbers 提取字符串中的所有连续整型数字段
func VersionNumbers(value string) []int {
	matches := numberPattern.FindAllString(value, -1)
	numbers := make([]int, 0, len(matches))
	for _, match := range matches {
		if number, err := strconv.Atoi(match); err == nil {
			numbers = append(numbers, number)
		}
	}
	return numbers
}

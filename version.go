package main

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

func extractLlamaCppVersion(text string) string {
	for _, pattern := range llamaBuildPatterns {
		if match := pattern.FindStringSubmatch(text); len(match) > 1 {
			return "b" + match[1]
		}
	}
	return ""
}

func extractLlamaCppBuildVersion(text string) string { return extractLlamaCppVersion(text) }

func extractLlamaSwapVersion(text string) string {
	if match := llamaSwapPattern.FindStringSubmatch(text); len(match) > 1 {
		return match[1]
	}
	return ""
}

func versionFromFilename(path string) string {
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

func compareVersions(local, remote string) int {
	a, b := versionNumbers(local), versionNumbers(remote)
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

func compareVersionStrings(local, remote string) int { return compareVersions(local, remote) }

func versionNumbers(value string) []int {
	matches := numberPattern.FindAllString(value, -1)
	numbers := make([]int, 0, len(matches))
	for _, match := range matches {
		if number, err := strconv.Atoi(match); err == nil {
			numbers = append(numbers, number)
		}
	}
	return numbers
}

func detectLlamaCppVariant(path, output string) string {
	text := strings.ToLower(path + "\n" + output)
	switch {
	case containsAny(text, "cuda 13", "cuda13", "cuda-13"):
		return "cuda13"
	case containsAny(text, "cuda 12", "cuda12", "cuda-12"):
		return "cuda12"
	case installedCUDAVariant() != "":
		return installedCUDAVariant()
	case strings.Contains(text, "vulkan"):
		return "vulkan"
	case strings.Contains(text, "metal"):
		return "metal"
	default:
		return ""
	}
}

func formatVariant(variant string) string {
	switch strings.ToLower(variant) {
	case "cuda13":
		return "CUDA 13"
	case "cuda12":
		return "CUDA 12"
	case "vulkan":
		return "Vulkan"
	case "metal":
		return "Metal"
	default:
		return variant
	}
}

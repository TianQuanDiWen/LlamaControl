package fsutil

import (
	"archive/tar"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func createTestTarGz(t *testing.T, archivePath string, entries []tarEntry) {
	f, err := os.Create(archivePath)
	if err != nil {
		t.Fatalf("failed to create test archive: %v", err)
	}
	defer f.Close()

	gz := gzip.NewWriter(f)
	defer gz.Close()

	tw := tar.NewWriter(gz)
	defer tw.Close()

	for _, entry := range entries {
		hdr := &tar.Header{
			Name:     entry.name,
			Mode:     entry.mode,
			Typeflag: entry.typeflag,
			Size:     int64(len(entry.body)),
			Linkname: entry.linkname,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("failed to write tar header: %v", err)
		}
		if len(entry.body) > 0 {
			if _, err := tw.Write([]byte(entry.body)); err != nil {
				t.Fatalf("failed to write tar body: %v", err)
			}
		}
	}
}

type tarEntry struct {
	name     string
	mode     int64
	typeflag byte
	body     string
	linkname string
}

func TestExtractTarPackage_SymlinkSecurity(t *testing.T) {
	tmpDir := t.TempDir()
	archivePath := filepath.Join(tmpDir, "test.tar.gz")

	// 1. 尝试包含逃逸出解压目录的恶意软链接 (../outside)
	createTestTarGz(t, archivePath, []tarEntry{
		{
			name:     "pkg/llama-server",
			mode:     0755,
			typeflag: tar.TypeReg,
			body:     "binary-content",
		},
		{
			name:     "pkg/bad-symlink",
			mode:     0755,
			typeflag: tar.TypeSymlink,
			linkname: "../../../outside",
		},
	})

	destDir := filepath.Join(tmpDir, "dest")
	_ = os.MkdirAll(destDir, 0755)

	_, err := ExtractTarPackage(archivePath, destDir, []string{"llama-server"})
	if err == nil {
		t.Fatalf("expected error when tar contains escaping symlink, got nil")
	}

	// 2. 尝试绝对路径软链接 (/etc/passwd)
	createTestTarGz(t, archivePath, []tarEntry{
		{
			name:     "pkg/llama-server",
			mode:     0755,
			typeflag: tar.TypeReg,
			body:     "binary-content",
		},
		{
			name:     "pkg/abs-symlink",
			mode:     0755,
			typeflag: tar.TypeSymlink,
			linkname: "/etc/passwd",
		},
	})

	destDir2 := filepath.Join(tmpDir, "dest2")
	_ = os.MkdirAll(destDir2, 0755)

	_, err = ExtractTarPackage(archivePath, destDir2, []string{"llama-server"})
	if err == nil {
		t.Fatalf("expected error when tar contains absolute symlink, got nil")
	}
}

func TestWriteReader_DoesNotFollowSymlink(t *testing.T) {
	tmpDir := t.TempDir()
	targetFile := filepath.Join(tmpDir, "secret.txt")
	_ = os.WriteFile(targetFile, []byte("original-secret"), 0644)

	symlinkPath := filepath.Join(tmpDir, "link.txt")
	if err := os.Symlink(targetFile, symlinkPath); err != nil {
		t.Skipf("skipping symlink test on current environment: %v", err)
	}

	// 尝试向已存在的 symlink 写入
	data := "malicious-overwrite"
	err := WriteReader(symlinkPath, strings.NewReader(data), 0644)
	if err != nil {
		t.Fatalf("WriteReader failed: %v", err)
	}

	// 验证 targetFile 未被篡改
	content, _ := os.ReadFile(targetFile)
	if string(content) != "original-secret" {
		t.Fatalf("WriteReader followed symlink and modified target file! Got: %s", string(content))
	}

	// 验证 symlinkPath 变为了常规普通文件
	symContent, _ := os.ReadFile(symlinkPath)
	if string(symContent) != data {
		t.Fatalf("expected symlinkPath to be overwritten with new data, got: %s", string(symContent))
	}
}

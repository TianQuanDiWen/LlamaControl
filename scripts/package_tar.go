package main

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// package_tar.go: 跨平台打包 .tar.gz 并显式为可执行文件设置 0755 权限
func main() {
	if len(os.Args) < 4 {
		fmt.Fprintf(os.Stderr, "Usage: %s <output.tar.gz> <source-dir> <binary-name>\n", os.Args[0])
		os.Exit(1)
	}

	outPath := os.Args[1]
	srcDir := os.Args[2]
	binName := os.Args[3]

	outFile, err := os.Create(outPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create output file: %v\n", err)
		os.Exit(1)
	}
	defer outFile.Close()

	gw := gzip.NewWriter(outFile)
	defer gw.Close()

	tw := tar.NewWriter(gw)
	defer tw.Close()

	srcFile := filepath.Join(srcDir, binName)
	info, err := os.Stat(srcFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to stat source file: %v\n", err)
		os.Exit(1)
	}

	hdr := &tar.Header{
		Name:     binName,
		Mode:     0755, // 显式确保 Unix / macOS / Linux 平台执行权限位生效
		Size:     info.Size(),
		ModTime:  info.ModTime(),
		Typeflag: tar.TypeReg,
	}

	if err := tw.WriteHeader(hdr); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to write tar header: %v\n", err)
		os.Exit(1)
	}

	f, err := os.Open(srcFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to open source file: %v\n", err)
		os.Exit(1)
	}
	defer f.Close()

	if _, err := io.Copy(tw, f); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to copy file content into tar: %v\n", err)
		os.Exit(1)
	}
}

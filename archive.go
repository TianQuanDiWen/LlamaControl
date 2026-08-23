package main

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// extractPackage extracts every file beside the requested executable. Modern
// llama.cpp executables are small launchers whose implementation and build
// metadata live in adjacent DLLs, so replacing only the exe is not sufficient.
func extractPackage(archivePath, destDir string, targetNames []string) (string, error) {
	lower := strings.ToLower(archivePath)
	switch {
	case strings.HasSuffix(lower, ".zip"):
		return extractZipPackage(archivePath, destDir, targetNames)
	case strings.HasSuffix(lower, ".tar.gz"), strings.HasSuffix(lower, ".tgz"):
		return extractTarPackage(archivePath, destDir, targetNames)
	default:
		target := filepath.Join(destDir, targetNames[0])
		if err := copyFile(archivePath, target); err != nil {
			return "", err
		}
		return target, nil
	}
}

func extractZipPackage(archivePath, destDir string, targetNames []string) (string, error) {
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		return "", err
	}
	defer reader.Close()
	targetDir, targetBase := "", ""
	for _, target := range targetNames {
		for _, file := range reader.File {
			if !file.FileInfo().IsDir() && strings.EqualFold(filepath.Base(filepath.FromSlash(file.Name)), target) {
				targetDir, targetBase = filepath.ToSlash(filepath.Dir(filepath.FromSlash(file.Name))), filepath.Base(file.Name)
				break
			}
		}
		if targetBase != "" {
			break
		}
	}
	if targetBase == "" {
		return "", fmt.Errorf("压缩包中未找到目标程序 %s", strings.Join(targetNames, "/"))
	}
	for _, file := range reader.File {
		name := filepath.FromSlash(file.Name)
		if file.FileInfo().IsDir() || filepath.ToSlash(filepath.Dir(name)) != targetDir {
			continue
		}
		in, err := file.Open()
		if err != nil {
			return "", err
		}
		err = writeReader(filepath.Join(destDir, filepath.Base(name)), in, file.Mode())
		in.Close()
		if err != nil {
			return "", err
		}
	}
	return filepath.Join(destDir, targetBase), nil
}

func extractTarPackage(archivePath, destDir string, targetNames []string) (string, error) {
	targetDir, targetBase, err := findTarTarget(archivePath, targetNames)
	if err != nil {
		return "", err
	}
	file, gz, tarReader, err := openTarGz(archivePath)
	if err != nil {
		return "", err
	}
	defer file.Close()
	defer gz.Close()
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
		name := filepath.FromSlash(header.Name)
		if header.Typeflag != tar.TypeReg || filepath.ToSlash(filepath.Dir(name)) != targetDir {
			continue
		}
		if err := writeReader(filepath.Join(destDir, filepath.Base(name)), tarReader, os.FileMode(header.Mode)); err != nil {
			return "", err
		}
	}
	return filepath.Join(destDir, targetBase), nil
}

func findTarTarget(path string, targetNames []string) (string, string, error) {
	file, gz, reader, err := openTarGz(path)
	if err != nil {
		return "", "", err
	}
	defer file.Close()
	defer gz.Close()
	targets := make(map[string]bool)
	for _, target := range targetNames {
		targets[strings.ToLower(target)] = true
	}
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", "", err
		}
		name := filepath.FromSlash(header.Name)
		if header.Typeflag == tar.TypeReg && targets[strings.ToLower(filepath.Base(name))] {
			return filepath.ToSlash(filepath.Dir(name)), filepath.Base(name), nil
		}
	}
	return "", "", fmt.Errorf("压缩包中未找到目标程序 %s", strings.Join(targetNames, "/"))
}

func openTarGz(path string) (*os.File, *gzip.Reader, *tar.Reader, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, nil, nil, err
	}
	gz, err := gzip.NewReader(file)
	if err != nil {
		file.Close()
		return nil, nil, nil, err
	}
	return file, gz, tar.NewReader(gz), nil
}

func replacePackageSafely(srcDir, dstDir, executable string, verify func(string) error) error {
	entries, err := os.ReadDir(srcDir)
	if err != nil {
		return err
	}
	backupDir, err := os.MkdirTemp(dstDir, ".llama-control-backup-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(backupDir)
	var installed []string
	rollback := func() {
		for i := len(installed) - 1; i >= 0; i-- {
			name := installed[i]
			_ = os.Remove(filepath.Join(dstDir, name))
			_ = os.Rename(filepath.Join(backupDir, name), filepath.Join(dstDir, name))
		}
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		src, dst := filepath.Join(srcDir, name), filepath.Join(dstDir, name)
		temp, err := os.CreateTemp(dstDir, ".llama-control-update-")
		if err != nil {
			rollback()
			return err
		}
		tempPath := temp.Name()
		temp.Close()
		if err := copyFile(src, tempPath); err != nil {
			os.Remove(tempPath)
			rollback()
			return err
		}
		hadBackup := false
		if _, err := os.Stat(dst); err == nil {
			if err := os.Rename(dst, filepath.Join(backupDir, name)); err != nil {
				os.Remove(tempPath)
				rollback()
				return err
			}
			hadBackup = true
		}
		if err := os.Rename(tempPath, dst); err != nil {
			if hadBackup {
				_ = os.Rename(filepath.Join(backupDir, name), dst)
			}
			rollback()
			return err
		}
		installed = append(installed, name)
	}
	if err := verify(filepath.Join(dstDir, executable)); err != nil {
		rollback()
		return err
	}
	return nil
}

func writeReader(path string, reader io.Reader, mode os.FileMode) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode.Perm())
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(file, reader)
	closeErr := file.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

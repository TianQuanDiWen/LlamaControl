package archive

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

// ExtractPackage 根据文件后缀名自动选择解压策略，将目标可执行文件及其同目录下的所有依赖文件解压到目标文件夹
func ExtractPackage(archivePath, destDir string, targetNames []string) (string, error) {
	lower := strings.ToLower(archivePath)
	switch {
	case strings.HasSuffix(lower, ".zip"):
		return ExtractZipPackage(archivePath, destDir, targetNames)
	case strings.HasSuffix(lower, ".tar.gz"), strings.HasSuffix(lower, ".tgz"):
		return ExtractTarPackage(archivePath, destDir, targetNames)
	default:
		target := filepath.Join(destDir, targetNames[0])
		if err := CopyFile(archivePath, target); err != nil {
			return "", err
		}
		return target, nil
	}
}

// ExtractZipPackage 处理 .zip 压缩包：在包中搜寻目标可执行文件所在的路径，并将该路径下的所有文件解压出来
func ExtractZipPackage(archivePath, destDir string, targetNames []string) (string, error) {
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
		err = WriteReader(filepath.Join(destDir, filepath.Base(name)), in, file.Mode())
		in.Close()
		if err != nil {
			return "", err
		}
	}
	return filepath.Join(destDir, targetBase), nil
}

// ExtractTarPackage 处理 .tar.gz 压缩包：在包中搜寻目标可执行文件所在的路径，并将该路径下的所有文件解压出来
func ExtractTarPackage(archivePath, destDir string, targetNames []string) (string, error) {
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
		if err := WriteReader(filepath.Join(destDir, filepath.Base(name)), tarReader, os.FileMode(header.Mode)); err != nil {
			return "", err
		}
	}
	return filepath.Join(destDir, targetBase), nil
}

// findTarTarget 辅助方法：快速扫描 tar 包找到包含目标二进制文件的内部文件夹路径
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

// openTarGz 辅助方法：开启文件并初始化 gzip 以及 tar 读取器
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

// WriteReader 从一个 io.Reader 流中将数据写入到指定的文件路径中
func WriteReader(path string, reader io.Reader, mode os.FileMode) error {
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

// CopyFile 将指定的源文件完整复制到目标位置
func CopyFile(src, dst string) error {
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

// ReplacePackageSafely 事务性原子替换目标目录下的程序及其依赖动态库，若验证失败自动回滚
func ReplacePackageSafely(srcDir, dstDir, executable string, verify func(string) error) error {
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
		if err := CopyFile(src, tempPath); err != nil {
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
			os.Remove(tempPath)
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

package fsutil

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
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
		_ = os.Chmod(target, 0755)
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
		mode := file.Mode()
		if isTarget(name, targetNames) {
			mode = mode | 0755
		}
		err = WriteReader(filepath.Join(destDir, filepath.Base(name)), in, mode)
		in.Close()
		if err != nil {
			return "", err
		}
	}
	return filepath.Join(destDir, targetBase), nil
}

// ExtractTarPackage 处理 .tar.gz 压缩包：在包中搜寻目标可执行文件所在的路径，并将该路径下的所有文件及软链接解压出来
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
		dir := filepath.Clean(filepath.ToSlash(filepath.Dir(name)))
		if dir != targetDir {
			continue
		}
		dstPath := filepath.Join(destDir, filepath.Base(name))

		switch header.Typeflag {
		case tar.TypeReg, tar.TypeRegA:
			mode := os.FileMode(header.Mode)
			if isTarget(name, targetNames) || strings.HasSuffix(name, ".dylib") || strings.HasSuffix(name, ".so") {
				mode = mode | 0755
			}
			// 必须清理可能存在的同名软链接，防止普通文件写入跟随已有链接导致越界覆写外部目标
			_ = os.Remove(dstPath)
			if err := WriteReader(dstPath, tarReader, mode); err != nil {
				return "", err
			}
		case tar.TypeSymlink:
			linkTarget := header.Linkname
			// 1. 禁止绝对路径软链接（如 /etc/passwd 或 C:\Windows）
			if filepath.IsAbs(linkTarget) || strings.HasPrefix(linkTarget, "/") || strings.HasPrefix(linkTarget, "\\") || (len(linkTarget) > 1 && linkTarget[1] == ':') {
				return "", fmt.Errorf("非法软链接目标 (禁止绝对路径): %s -> %s", header.Name, linkTarget)
			}
			// 2. 校验软链接目标不得逃逸出 destDir
			resolved := filepath.Clean(filepath.Join(destDir, linkTarget))
			cleanDest := filepath.Clean(destDir)
			rel, err := filepath.Rel(cleanDest, resolved)
			if err != nil || strings.HasPrefix(rel, "..") || rel == ".." {
				return "", fmt.Errorf("非法软链接目标 (越界): %s -> %s", header.Name, linkTarget)
			}
			// 3. 安全创建软链接，严禁忽略创建失败的错误
			_ = os.Remove(dstPath)
			if err := os.Symlink(linkTarget, dstPath); err != nil {
				return "", fmt.Errorf("创建软链接失败 (%s -> %s): %w", dstPath, linkTarget, err)
			}
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
		isReg := header.Typeflag == tar.TypeReg || header.Typeflag == tar.TypeRegA
		if isReg && targets[strings.ToLower(filepath.Base(name))] {
			return filepath.Clean(filepath.ToSlash(filepath.Dir(name))), filepath.Base(name), nil
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

// WriteReader 从一个 io.Reader 流中将数据写入到指定的文件路径中，并确保 POSIX 权限位生效
func WriteReader(path string, reader io.Reader, mode os.FileMode) error {
	perm := mode.Perm()
	if perm == 0 {
		perm = 0644
	}
	// 防御性保护：若目标路径为软链接，先将其移除，防止 OpenFile 跟随软链接截断或写入外部目标
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		_ = os.Remove(path)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(file, reader)
	closeErr := file.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	_ = os.Chmod(path, perm)
	return nil
}

// CopyFile 将指定的源文件完整复制到目标位置，并完整保留源文件的权限位
func CopyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	info, err := in.Stat()
	if err != nil {
		return err
	}

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	_ = os.Chmod(dst, info.Mode().Perm())
	return nil
}

// retryRename 在重命名遇到文件占用（如 Windows 下服务刚停止、内核句柄未完全关闭）时进行短暂重试
func retryRename(src, dst string, maxAttempts int, interval time.Duration) error {
	var err error
	for i := 0; i < maxAttempts; i++ {
		err = os.Rename(src, dst)
		if err == nil {
			return nil
		}
		time.Sleep(interval)
	}
	return err
}

// ReplacePackageSafely 事务性原子替换目标目录下的程序及其依赖动态库，若验证失败自动回滚
func ReplacePackageSafely(srcDir, dstDir, executable string, verify func(string) error) error {
	if err := os.MkdirAll(dstDir, 0755); err != nil {
		return err
	}
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
			_ = retryRename(filepath.Join(backupDir, name), filepath.Join(dstDir, name), 5, 200*time.Millisecond)
		}
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		src, dst := filepath.Join(srcDir, name), filepath.Join(dstDir, name)

		isSymlink := entry.Type()&os.ModeSymlink != 0
		var tempPath string
		if isSymlink {
			target, err := os.Readlink(src)
			if err != nil {
				rollback()
				return err
			}
			tempFile, err := os.CreateTemp(dstDir, ".llama-control-symlink-")
			if err != nil {
				rollback()
				return err
			}
			tempPath = tempFile.Name()
			tempFile.Close()
			_ = os.Remove(tempPath)
			if err := os.Symlink(target, tempPath); err != nil {
				rollback()
				return err
			}
		} else {
			temp, err := os.CreateTemp(dstDir, ".llama-control-update-")
			if err != nil {
				rollback()
				return err
			}
			tempPath = temp.Name()
			temp.Close()
			if err := CopyFile(src, tempPath); err != nil {
				os.Remove(tempPath)
				rollback()
				return err
			}
		}

		hadBackup := false
		if _, err := os.Lstat(dst); err == nil {
			if err := retryRename(dst, filepath.Join(backupDir, name), 5, 200*time.Millisecond); err != nil {
				os.Remove(tempPath)
				rollback()
				return fmt.Errorf("备份目标文件 %s 失败 (文件可能仍被进程占用): %w", name, err)
			}
			hadBackup = true
		}
		if err := retryRename(tempPath, dst, 5, 200*time.Millisecond); err != nil {
			if hadBackup {
				_ = retryRename(filepath.Join(backupDir, name), dst, 5, 200*time.Millisecond)
			}
			os.Remove(tempPath)
			rollback()
			return fmt.Errorf("替换目标文件 %s 失败: %w", name, err)
		}
		installed = append(installed, name)
	}

	// 确保目标可执行文件与动态库在 Unix/macOS 下拥有执行权限 (0755)
	for _, name := range installed {
		p := filepath.Join(dstDir, name)
		if info, err := os.Lstat(p); err == nil && info.Mode()&os.ModeSymlink == 0 {
			if strings.EqualFold(name, executable) || strings.HasSuffix(name, ".dylib") || strings.HasSuffix(name, ".so") {
				_ = os.Chmod(p, 0755)
			}
		}
	}

	targetExe := filepath.Join(dstDir, executable)
	if runtime.GOOS == "darwin" {
		_ = exec.Command("xattr", "-r", "-d", "com.apple.quarantine", dstDir).Run()
	}

	if err := verify(targetExe); err != nil {
		rollback()
		return err
	}
	return nil
}

// isTarget 辅助判断文件名是否命中预期的可执行文件候选列表
func isTarget(path string, targetNames []string) bool {
	base := strings.ToLower(filepath.Base(path))
	for _, target := range targetNames {
		if strings.EqualFold(base, target) {
			return true
		}
	}
	return false
}

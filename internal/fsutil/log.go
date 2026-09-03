package fsutil

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// DailyLogWriter 按照日期自动切片写入日志的简易写入器（实现 io.WriteCloser 接口）
type DailyLogWriter struct {
	Dir    string
	Prefix string
	mu     sync.Mutex
	day    string
	file   *os.File
}

// Write 写入日志数据，跨自然日时自动切换到新的日期文件 (如 swap-2006-01-02.log)
func (w *DailyLogWriter) Write(b []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	today := time.Now().Format("2006-01-02")
	if w.file == nil || w.day != today {
		if w.file != nil {
			_ = w.file.Close()
		}
		w.day = today
		filePath := filepath.Join(w.Dir, fmt.Sprintf("%s-%s.log", w.Prefix, today))
		f, err := os.OpenFile(filePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			return 0, err
		}
		w.file = f
	}
	return w.file.Write(b)
}

// Close 关闭当前持有的日志文件句柄
func (w *DailyLogWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file != nil {
		err := w.file.Close()
		w.file = nil
		return err
	}
	return nil
}

// CleanLogs 删除指定目录下 7 天前的过期历史日志文件
func CleanLogs(logDir string) error {
	info, err := os.Stat(logDir)
	if err != nil || !info.IsDir() {
		fmt.Printf("[WARN] 日志目录不存在: %s\n", logDir)
		return nil
	}
	cutoff := time.Now().AddDate(0, 0, -7) // 保留最近 7 天日志
	entries, err := os.ReadDir(logDir)
	if err != nil {
		return err
	}
	deletedCount := 0
	var freedBytes int64
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		path := filepath.Join(logDir, entry.Name())
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			size := info.Size()
			if err := os.Remove(path); err == nil {
				deletedCount++
				freedBytes += size
			}
		}
	}
	freedMB := float64(freedBytes) / (1024 * 1024)
	fmt.Printf("[OK] 清理完成。已删除 %d 个历史日志文件，共释放空间: %.2f MB。\n", deletedCount, freedMB)
	return nil
}

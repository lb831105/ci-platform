package pkg

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// EnsureDir 创建目录（含父级），已存在则忽略。
func EnsureDir(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}
	return nil
}

// Exists 判断路径是否存在。
func Exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// IsDir 判断路径是否为目录。
func IsDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// WriteFileAtomic 原子写入文件：先写同目录临时文件，fsync 后 rename。
// 避免并发更新时读到半截文件。
func WriteFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := EnsureDir(dir); err != nil {
		return err
	}

	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp file in %s: %w", dir, err)
	}
	tmpName := tmp.Name()
	// 配置/凭据文件只允许服务账号读取。
	if err = tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod temp file: %w", err)
	}
	// rename 成功后该路径已不存在，Remove 失败可忽略。
	defer func() { _ = os.Remove(tmpName) }()

	if _, err = tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temp file: %w", err)
	}
	if err = tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync temp file: %w", err)
	}
	if err = tmp.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}
	if err = os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("rename temp to %s: %w", path, err)
	}
	// 目录 fsync 让 rename 在崩溃后也尽量可见；Windows 不支持目录 fsync，忽略错误。
	if dirFile, openErr := os.Open(dir); openErr == nil {
		_ = dirFile.Sync()
		_ = dirFile.Close()
	}
	return nil
}

// WriteJSONAtomic 以缩进 JSON 原子写入。
func WriteJSONAtomic(path string, v interface{}) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal json for %s: %w", path, err)
	}
	return WriteFileAtomic(path, data)
}

// ReadJSON 读取并反序列化 JSON 文件。
func ReadJSON(path string, v interface{}) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	if err = json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("unmarshal %s: %w", path, err)
	}
	return nil
}

// WithinDir 判断 target 是否位于 base 目录内（含 base 本身）。
// 用于所有「用户可控相对路径 + 本地文件」场景的越界防护。
func WithinDir(base, target string) bool {
	rel, err := filepath.Rel(base, target)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// RemoveAll 递归删除路径。
func RemoveAll(path string) error {
	if err := os.RemoveAll(path); err != nil {
		return fmt.Errorf("remove all %s: %w", path, err)
	}
	return nil
}

// AppendLine 以追加方式写一行文本到文件，保证换行结尾。
func AppendLine(path string, line string) error {
	if err := EnsureDir(filepath.Dir(path)); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open append %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	if _, err = f.WriteString(line); err != nil {
		return fmt.Errorf("append %s: %w", path, err)
	}
	return nil
}

// ListDirs 返回目录下所有子目录名（不含文件）。
func ListDirs(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read dir %s: %w", dir, err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	return names, nil
}

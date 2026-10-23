package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Bootstrap 是引导配置，独立于数据目录保存（默认 ./ci-platform.json）。
// 它只记录“数据目录在哪里”，因为数据目录本身可能被用户改到任意位置。
type Bootstrap struct {
	DataHome string `json:"data_home"`
}

// LoadBootstrap 读取引导配置；文件不存在时返回 (nil, nil)。
func LoadBootstrap(path string) (*Bootstrap, error) {
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read bootstrap %s: %w", path, err)
	}
	var b Bootstrap
	if err = json.Unmarshal(data, &b); err != nil {
		return nil, fmt.Errorf("parse bootstrap %s: %w", path, err)
	}
	return &b, nil
}

// SaveBootstrap 原子写入引导配置。
func SaveBootstrap(path string, b Bootstrap) error {
	if path == "" {
		return errors.New("bootstrap path is empty")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir for bootstrap %s: %w", path, err)
	}
	data, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal bootstrap: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".bootstrap-*")
	if err != nil {
		return fmt.Errorf("create temp bootstrap: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if _, err = tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write bootstrap: %w", err)
	}
	if err = tmp.Close(); err != nil {
		return fmt.Errorf("close bootstrap: %w", err)
	}
	if err = os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("rename bootstrap to %s: %w", path, err)
	}
	return nil
}

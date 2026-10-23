package store

import (
	"os"
	"path/filepath"

	"ci-platform/internal/model"
	"ci-platform/internal/pkg"
)

// SettingsStore 读写 data/settings.json。
type SettingsStore struct {
	path string
}

// NewSettingsStore 创建系统配置存储，dataHome 为 data 目录。
func NewSettingsStore(dataHome string) *SettingsStore {
	return &SettingsStore{path: filepath.Join(dataHome, "settings.json")}
}

// Load 读取配置；文件不存在时返回 (nil, nil)，由 service 决定默认值。
func (s *SettingsStore) Load() (*model.Settings, error) {
	if !pkg.Exists(s.path) {
		return nil, nil
	}
	var out model.Settings
	if err := pkg.ReadJSON(s.path, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Save 原子写入配置。
func (s *SettingsStore) Save(in *model.Settings) error {
	return pkg.WriteJSONAtomic(s.path, in)
}

// Path 返回配置文件路径，供「系统信息」展示。
func (s *SettingsStore) Path() string {
	return s.path
}

// Remove 删除配置文件（用于测试/重置）。
func (s *SettingsStore) Remove() error {
	err := os.Remove(s.path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

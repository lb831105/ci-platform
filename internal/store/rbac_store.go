package store

import (
	"os"
	"path/filepath"

	"ci-platform/internal/model"
	"ci-platform/internal/pkg"
)

// RbacStore 读写 data/rbac.json：角色、授权与用户（密码为 bcrypt hash）。
type RbacStore struct {
	path string
}

// NewRbacStore 创建 RBAC 存储，dataHome 为 data 目录。
func NewRbacStore(dataHome string) *RbacStore {
	return &RbacStore{path: filepath.Join(dataHome, "rbac.json")}
}

// Load 读取 RBAC 数据；文件不存在时返回 (nil, nil)，由 service 决定默认值。
func (s *RbacStore) Load() (*model.RbacData, error) {
	if !pkg.Exists(s.path) {
		return nil, nil
	}
	var out model.RbacData
	if err := pkg.ReadJSON(s.path, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Save 原子写入 RBAC 数据（临时文件 0600，含用户密码 hash）。
func (s *RbacStore) Save(in *model.RbacData) error {
	return pkg.WriteJSONAtomic(s.path, in)
}

// Path 返回存储文件路径，供「系统信息」展示。
func (s *RbacStore) Path() string { return s.path }

// Remove 删除存储文件（用于测试/重置）。
func (s *RbacStore) Remove() error {
	err := os.Remove(s.path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

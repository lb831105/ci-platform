package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"ci-platform/internal/model"
	"ci-platform/internal/pkg"
)

// ViewStore 读写 data/views.json：全部任务视图（给任务归类）。
// 视图数量小、结构简单，用单文件全量读写即可，与 settings.json 保持一致。
type ViewStore struct {
	path string
}

// NewViewStore 构造视图存储，path 为 data home 下的 views.json。
func NewViewStore(dataHome string) *ViewStore {
	return &ViewStore{path: filepath.Join(dataHome, "views.json")}
}

// Load 读取全部视图；文件不存在或为空时返回 nil（首次启动）。
func (s *ViewStore) Load() ([]model.View, error) {
	b, err := os.ReadFile(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(b)) == 0 {
		return nil, nil
	}
	var out []model.View
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// Save 原子写回全部视图（避免断电/崩溃导致文件截断）。
func (s *ViewStore) Save(views []model.View) error {
	if views == nil {
		views = []model.View{}
	}
	b, err := json.MarshalIndent(views, "", "  ")
	if err != nil {
		return err
	}
	return pkg.WriteFileAtomic(s.path, b)
}

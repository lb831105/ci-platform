package store

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"ci-platform/internal/model"
	"ci-platform/internal/pkg"
)

// configFileName 是 pipeline 定义文件名。
const configFileName = "config.json"

// PipelineStore 负责 data/jobs/{id}/config.json 的读写，不做任何业务判断。
type PipelineStore struct {
	root string // DATA_HOME/jobs
}

// NewPipelineStore 创建 pipeline 文件存储，root 为 data/jobs 目录。
func NewPipelineStore(jobsDir string) *PipelineStore {
	return &PipelineStore{root: jobsDir}
}

// Root 返回存储根目录。
func (s *PipelineStore) Root() string { return s.root }

// JobDir 返回某个 pipeline 的目录 data/jobs/{id}。
func (s *PipelineStore) JobDir(id string) string {
	return filepath.Join(s.root, id)
}

// configPath 返回 config.json 路径。
func (s *PipelineStore) configPath(id string) string {
	return filepath.Join(s.JobDir(id), configFileName)
}

// WriteAuxFile 在任务目录下写辅助文件（如导入来源 jenkins-config.xml）。
// 文件名限定为单段安全名称，避免调用方把它变成路径穿越入口。
func (s *PipelineStore) WriteAuxFile(id, name string, data []byte) error {
	if id == "" || name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\`) {
		return fmt.Errorf("invalid aux file %q/%q", id, name)
	}
	dir := s.JobDir(id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, name), data, 0o600)
}

// Exists 判断 pipeline 目录是否存在。
func (s *PipelineStore) Exists(id string) bool {
	return pkg.Exists(s.configPath(id))
}

// Create 创建 pipeline 目录并写入 config.json。
func (s *PipelineStore) Create(ctx context.Context, p *model.Pipeline) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := sanitizeID(p.ID); err != nil {
		return err
	}
	if pkg.Exists(s.JobDir(p.ID)) {
		return pkg.ErrConflict("pipeline %s already exists", p.ID)
	}
	if err := pkg.EnsureDir(filepath.Join(s.JobDir(p.ID), "builds")); err != nil {
		return err
	}
	return pkg.WriteJSONAtomic(s.configPath(p.ID), p)
}

// Get 读取 pipeline 定义。
func (s *PipelineStore) Get(ctx context.Context, id string) (*model.Pipeline, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := sanitizeID(id); err != nil {
		return nil, err
	}
	path := s.configPath(id)
	if !pkg.Exists(path) {
		return nil, pkg.ErrNotFound("pipeline %s not found", id)
	}
	var p model.Pipeline
	if err := pkg.ReadJSON(path, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

// List 遍历 data/jobs 目录读取全部 pipeline，按创建时间倒序。
// 单个目录损坏时跳过，不影响整体列表。
func (s *PipelineStore) List(ctx context.Context) ([]*model.Pipeline, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !pkg.IsDir(s.root) {
		return []*model.Pipeline{}, nil
	}
	names, err := pkg.ListDirs(s.root)
	if err != nil {
		return nil, err
	}
	out := make([]*model.Pipeline, 0, len(names))
	for _, name := range names {
		var p model.Pipeline
		if err := pkg.ReadJSON(s.configPath(name), &p); err != nil {
			continue // 忽略损坏目录，保证列表可用
		}
		out = append(out, &p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

// Update 覆盖写入 pipeline 定义。
func (s *PipelineStore) Update(ctx context.Context, p *model.Pipeline) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := sanitizeID(p.ID); err != nil {
		return err
	}
	if !pkg.Exists(s.JobDir(p.ID)) {
		return pkg.ErrNotFound("pipeline %s not found", p.ID)
	}
	return pkg.WriteJSONAtomic(s.configPath(p.ID), p)
}

// Delete 递归删除整个 pipeline 目录（含全部构建记录）。
func (s *PipelineStore) Delete(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := sanitizeID(id); err != nil {
		return err
	}
	if !pkg.Exists(s.JobDir(id)) {
		return pkg.ErrNotFound("pipeline %s not found", id)
	}
	return pkg.RemoveAll(s.JobDir(id))
}

// sanitizeID 拒绝空值与路径穿越字符，防止 id 逃逸出 data/jobs 目录。
func sanitizeID(id string) error {
	// "." 会把 JobDir 解析为 jobs 根目录，DELETE 时可能删除全部任务；
	// 必须和 ".." 一样显式拒绝。
	if id == "" || id == "." || id == ".." ||
		strings.Contains(id, "..") ||
		strings.ContainsAny(id, `/\:*?"<>|`) {
		return pkg.ErrBadRequest("invalid id %q", id)
	}
	return nil
}

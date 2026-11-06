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
// Webhook 密钥、SSH 密码、群机器人 URL 等敏感字段使用 credentials.key 加密落盘。
type PipelineStore struct {
	root string // DATA_HOME/jobs
	box  *pkg.SecretBox
}

// NewPipelineStore 创建 pipeline 文件存储，root 为 data/jobs 目录。
func NewPipelineStore(jobsDir string) *PipelineStore {
	return &PipelineStore{
		root: jobsDir,
		box:  pkg.NewSecretBox(filepath.Join(filepath.Dir(jobsDir), "credentials.key")),
	}
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
	return s.writePipeline(p)
}

// Get 读取并解密 pipeline 定义。
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
	if err := s.decryptPipeline(&p); err != nil {
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
		if err := s.decryptPipeline(&p); err != nil {
			continue
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
	return s.writePipeline(p)
}

func (s *PipelineStore) writePipeline(p *model.Pipeline) error {
	// 在副本上加密，不能改调用方（BuildService 仍需要明文 WebhookSecret/密码）。
	out := *p
	if p.SshPublish != nil {
		cfg := *p.SshPublish
		out.SshPublish = &cfg
	}
	if len(p.SshPublishes) > 0 {
		out.SshPublishes = append([]model.SshPublishConfig(nil), p.SshPublishes...)
	}
	out.Notify = cloneNotify(p.Notify)
	if err := s.encryptPipeline(&out); err != nil {
		return err
	}
	return pkg.WriteJSONAtomic(s.configPath(p.ID), &out)
}

func (s *PipelineStore) encryptPipeline(p *model.Pipeline) error {
	seal := func(value string) (string, error) { return s.box.Seal(value) }
	var err error
	if p.WebhookSecret, err = seal(p.WebhookSecret); err != nil {
		return err
	}
	if p.SshPublish != nil {
		if p.SshPublish.Password, err = seal(p.SshPublish.Password); err != nil {
			return err
		}
	}
	for i := range p.SshPublishes {
		if p.SshPublishes[i].Password, err = seal(p.SshPublishes[i].Password); err != nil {
			return err
		}
	}
	if p.Notify != nil {
		if err = sealNotify(p.Notify, seal); err != nil {
			return err
		}
	}
	return nil
}

func (s *PipelineStore) decryptPipeline(p *model.Pipeline) error {
	open := func(value string) (string, error) { return s.box.Unseal(value) }
	var err error
	if p.WebhookSecret, err = open(p.WebhookSecret); err != nil {
		return err
	}
	if p.SshPublish != nil {
		if p.SshPublish.Password, err = open(p.SshPublish.Password); err != nil {
			return err
		}
	}
	for i := range p.SshPublishes {
		if p.SshPublishes[i].Password, err = open(p.SshPublishes[i].Password); err != nil {
			return err
		}
	}
	if p.Notify != nil {
		if err = openNotify(p.Notify, open); err != nil {
			return err
		}
	}
	return nil
}

func cloneNotify(in *model.JobNotify) *model.JobNotify {
	if in == nil {
		return nil
	}
	out := *in
	out.People = append([]string(nil), in.People...)
	out.Emails = append([]string(nil), in.Emails...)
	out.Targets = append([]model.NotifyTarget(nil), in.Targets...)
	if in.OnCall != nil {
		v := *in.OnCall
		v.Rotation = append([]string(nil), in.OnCall.Rotation...)
		out.OnCall = &v
	}
	if in.Escalation != nil {
		v := *in.Escalation
		v.People = append([]string(nil), in.Escalation.People...)
		v.Emails = append([]string(nil), in.Escalation.Emails...)
		v.Targets = append([]model.NotifyTarget(nil), in.Escalation.Targets...)
		out.Escalation = &v
	}
	if len(in.Routes) > 0 {
		routes := make([]model.NotifyRoute, len(in.Routes))
		copy(routes, in.Routes)
		for i := range routes {
			routes[i].People = append([]string(nil), in.Routes[i].People...)
			routes[i].Emails = append([]string(nil), in.Routes[i].Emails...)
			routes[i].Targets = append([]model.NotifyTarget(nil), in.Routes[i].Targets...)
		}
		out.Routes = routes
	}
	return &out
}

func sealNotify(n *model.JobNotify, seal func(string) (string, error)) error {
	for i := range n.Targets {
		value, err := seal(n.Targets[i].URL)
		if err != nil {
			return err
		}
		n.Targets[i].URL = value
	}
	if n.Escalation != nil {
		for i := range n.Escalation.Targets {
			value, err := seal(n.Escalation.Targets[i].URL)
			if err != nil {
				return err
			}
			n.Escalation.Targets[i].URL = value
		}
	}
	for i := range n.Routes {
		for j := range n.Routes[i].Targets {
			value, err := seal(n.Routes[i].Targets[j].URL)
			if err != nil {
				return err
			}
			n.Routes[i].Targets[j].URL = value
		}
	}
	return nil
}

func openNotify(n *model.JobNotify, open func(string) (string, error)) error {
	for i := range n.Targets {
		value, err := open(n.Targets[i].URL)
		if err != nil {
			return err
		}
		n.Targets[i].URL = value
	}
	if n.Escalation != nil {
		for i := range n.Escalation.Targets {
			value, err := open(n.Escalation.Targets[i].URL)
			if err != nil {
				return err
			}
			n.Escalation.Targets[i].URL = value
		}
	}
	for i := range n.Routes {
		for j := range n.Routes[i].Targets {
			value, err := open(n.Routes[i].Targets[j].URL)
			if err != nil {
				return err
			}
			n.Routes[i].Targets[j].URL = value
		}
	}
	return nil
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

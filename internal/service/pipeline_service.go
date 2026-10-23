package service

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"ci-platform/internal/executor"
	"ci-platform/internal/model"
	"ci-platform/internal/pkg"
)

// PipelineStore 是 service 依赖的 pipeline 存储抽象（由 store 包实现）。
type PipelineStore interface {
	Exists(id string) bool
	Create(ctx context.Context, p *model.Pipeline) error
	Get(ctx context.Context, id string) (*model.Pipeline, error)
	List(ctx context.Context) ([]*model.Pipeline, error)
	Update(ctx context.Context, p *model.Pipeline) error
	Delete(ctx context.Context, id string) error
}

// PipelineService 是 Pipeline 的唯一业务编排入口：负责校验、ID 分配与时间戳维护。
// 不直接读写文件（走 store），不拼接 shell/ssh 命令（走 executor）。
type PipelineService struct {
	mu        sync.Mutex // 串行化 ID 分配与工作区目录名冲突检查
	store     PipelineStore
	settings  *SettingsService
	workspace *WorkspaceManager
}

// NewPipelineService 构造 Pipeline 业务服务。
func NewPipelineService(store PipelineStore, settings *SettingsService, workspace *WorkspaceManager) *PipelineService {
	return &PipelineService{store: store, settings: settings, workspace: workspace}
}

// List 返回全部 pipeline。
func (s *PipelineService) List(ctx context.Context) ([]*model.Pipeline, error) {
	return s.store.List(ctx)
}

// Get 返回单个 pipeline。
func (s *PipelineService) Get(ctx context.Context, id string) (*model.Pipeline, error) {
	return s.store.Get(ctx, id)
}

// MigrateWorkspacePolicy 一次性把旧版本默认的“构建前清空工作区”改为复用。
// 只处理带 Git 仓库的任务，避免升级后仍然每次重新 clone 源代码。
func (s *PipelineService) MigrateWorkspacePolicy(ctx context.Context) (int, error) {
	pipelines, err := s.store.List(ctx)
	if err != nil {
		return 0, err
	}
	migrated := 0
	for _, p := range pipelines {
		if p.GitURL == "" {
			continue
		}
		if p.DeleteWorkspaceBeforeBuild != nil && !*p.DeleteWorkspaceBeforeBuild {
			continue
		}
		keep := false
		p.DeleteWorkspaceBeforeBuild = &keep
		p.UpdatedAt = time.Now().UTC()
		if err := s.store.Update(ctx, p); err != nil {
			return migrated, err
		}
		migrated++
	}
	return migrated, nil
}

// Create 校验并落盘新 pipeline，ID 使用时间戳字符串（可读、唯一）。
func (s *PipelineService) Create(ctx context.Context, in *model.Pipeline) (*model.Pipeline, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := normalizeAndValidate(in, s.nameRegex()); err != nil {
		return nil, err
	}
	if err := s.validateSshServer(in); err != nil {
		return nil, err
	}
	in.ID = s.allocateID()
	dir := workspaceDirName(in.Name, in.ID)
	if err := s.ensureWorkspaceDirFree(ctx, in.Name, dir, ""); err != nil {
		return nil, err
	}
	// 已删除任务可能留下同名孤儿工作区；直接复用会把旧源码/产物带进新任务。
	// 这里要求用户先清理，避免两个任务意外共享历史数据。
	if s.workspace != nil && s.workspace.Exists(dir) {
		return nil, pkg.ErrConflict("工作区目录 %q 已存在（可能是已删除任务的残留），请先在「管理旧数据」中清理", dir)
	}
	in.WorkspaceDir = dir
	now := time.Now().UTC()
	in.CreatedAt = now
	in.UpdatedAt = now
	if err := s.store.Create(ctx, in); err != nil {
		return nil, err
	}
	return in, nil
}

// Update 更新 pipeline，保留创建时间；SSH 敏感字段不回显时沿用旧值。
func (s *PipelineService) Update(ctx context.Context, id string, in *model.Pipeline) (*model.Pipeline, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	old, err := s.store.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	in.ID = id
	in.CreatedAt = old.CreatedAt
	in.UpdatedAt = time.Now().UTC()
	// 先合并敏感字段再校验：编辑时前端不回显密码，需用旧值补齐，
	// 否则 auth_type=password 的任务保存会被“密码必填”误拦截。
	mergeSensitive(old, in)
	if err = normalizeAndValidate(in, s.nameRegex()); err != nil {
		return nil, err
	}
	if err = s.validateSshServer(in); err != nil {
		return nil, err
	}
	oldDir, newDir, moved, err := s.applyWorkspace(ctx, old, in.Name)
	if err != nil {
		return nil, err
	}
	in.WorkspaceDir = newDir
	if err = s.store.Update(ctx, in); err != nil {
		if moved && s.workspace != nil {
			_ = s.workspace.Rename(newDir, oldDir)
		}
		return nil, err
	}
	return in, nil
}

// Delete 删除 pipeline 及其全部构建记录（递归删目录）。
func (s *PipelineService) Delete(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.store.Delete(ctx, id)
}

// Rename 只修改任务名称（对应 Jenkins 的「重命名」），并受工程命名限制约束。
func (s *PipelineService) Rename(ctx context.Context, id, name string) (*model.Pipeline, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, err := s.store.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, pkg.ErrBadRequest("name is required")
	}
	if regex := s.nameRegex(); regex != "" {
		re, compileErr := regexp.Compile(regex)
		if compileErr != nil {
			return nil, pkg.ErrBadRequest("工程命名限制正则非法: %v", compileErr)
		}
		if !re.MatchString(name) {
			return nil, pkg.ErrBadRequest("任务名称 %q 不满足命名限制：%s", name, regex)
		}
	}
	oldDir, newDir, moved, err := s.applyWorkspace(ctx, p, name)
	if err != nil {
		return nil, err
	}
	p.Name = name
	p.WorkspaceDir = newDir
	p.UpdatedAt = time.Now().UTC()
	if err = s.store.Update(ctx, p); err != nil {
		if moved && s.workspace != nil {
			_ = s.workspace.Rename(newDir, oldDir)
		}
		return nil, err
	}
	return p, nil
}

// ensureWorkspaceDirFree 检查目录名是否被其他任务占用（大小写不敏感，兼容 Windows）。
func (s *PipelineService) ensureWorkspaceDirFree(ctx context.Context, name, dir, selfID string) error {
	pipelines, err := s.store.List(ctx)
	if err != nil {
		return err
	}
	for _, p := range pipelines {
		if p.ID == selfID {
			continue
		}
		other := strings.TrimSpace(p.WorkspaceDir)
		if other == "" {
			other = workspaceDirName(p.Name, p.ID)
		}
		if strings.EqualFold(other, dir) {
			return pkg.ErrConflict("任务名称 %q 已被使用（工作区目录 %q）", name, dir)
		}
	}
	return nil
}

// applyWorkspace 在名称变化时选择新目录并同步移动旧工作区。
// 返回旧目录、新目录以及是否实际移动，供 store 更新失败时回滚。
func (s *PipelineService) applyWorkspace(ctx context.Context, p *model.Pipeline, name string) (oldDir, newDir string, moved bool, err error) {
	oldDir = strings.TrimSpace(p.WorkspaceDir)
	if oldDir == "" {
		oldDir = p.ID
	}
	if p.Name == name && strings.TrimSpace(p.WorkspaceDir) != "" {
		return oldDir, oldDir, false, nil
	}
	newDir = workspaceDirName(name, p.ID)
	if name != p.Name {
		if err = s.ensureWorkspaceDirFree(ctx, name, newDir, p.ID); err != nil {
			return "", "", false, err
		}
	}
	if newDir == oldDir {
		return oldDir, newDir, false, nil
	}
	// Windows 文件系统大小写不敏感，仅改大小写时保留原目录，避免误报“已存在”。
	if runtime.GOOS == "windows" && strings.EqualFold(oldDir, newDir) {
		return oldDir, newDir, false, nil
	}
	if s.workspace != nil {
		if err = s.workspace.Rename(oldDir, newDir); err != nil {
			return "", "", false, err
		}
		moved = true
	}
	return oldDir, newDir, moved, nil
}

// MigrateWorkspaceDirs 把旧任务的 data/workspace/{id} 迁移为任务名目录。
// 仅处理缺少 WorkspaceDir 的旧配置；重复名称会追加 -{id} 避免互相覆盖。
func (s *PipelineService) MigrateWorkspaceDirs(ctx context.Context) (int, error) {
	pipelines, err := s.store.List(ctx)
	if err != nil {
		return 0, err
	}
	sort.Slice(pipelines, func(i, j int) bool { return pipelines[i].ID < pipelines[j].ID })
	used := make(map[string]bool, len(pipelines))
	for _, p := range pipelines {
		if dir := strings.TrimSpace(p.WorkspaceDir); dir != "" {
			used[strings.ToLower(dir)] = true
		}
	}

	migrated := 0
	var firstErr error
	for _, p := range pipelines {
		if strings.TrimSpace(p.WorkspaceDir) != "" {
			continue
		}
		base := workspaceDirName(p.Name, p.ID)
		dir := base
		for i := 1; ; i++ {
			if i > 1 {
				dir = fmt.Sprintf("%s-%s-%d", base, p.ID, i)
			} else if i == 1 && used[strings.ToLower(dir)] {
				dir = base + "-" + p.ID
			}
			lower := strings.ToLower(dir)
			// 目录已存在且不是当前任务的旧 ID 目录，说明是孤儿目录或其他任务，继续加后缀。
			exists := s.workspace != nil && dir != p.ID && s.workspace.Exists(dir)
			if !used[lower] && !exists {
				break
			}
		}
		oldDir := p.ID
		if s.workspace != nil && oldDir != dir {
			if err = s.workspace.Rename(oldDir, dir); err != nil {
				// 单个工作区被占用（Windows 文件锁）不应阻断其他任务迁移。
				if firstErr == nil {
					firstErr = fmt.Errorf("rename workspace %s -> %s: %w", oldDir, dir, err)
				}
				continue
			}
		}
		p.WorkspaceDir = dir
		p.UpdatedAt = time.Now().UTC()
		if err = s.store.Update(ctx, p); err != nil {
			if s.workspace != nil && oldDir != dir {
				_ = s.workspace.Rename(dir, oldDir)
			}
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		used[strings.ToLower(dir)] = true
		migrated++
	}
	return migrated, firstErr
}

// validateSshServer 确保任务引用的 SSH 服务器在系统配置中存在。
func (s *PipelineService) validateSshServer(p *model.Pipeline) error {
	if !p.SSHEnabled() || p.SshPublish.ServerID == "" || s.settings == nil {
		return nil
	}
	for _, server := range s.settings.Current().SshServers {
		if server.ID == p.SshPublish.ServerID {
			return nil
		}
	}
	return pkg.ErrBadRequest("SSH 服务器 %q 不存在，请先在系统配置中添加", p.SshPublish.ServerID)
}

// allocateID 以秒级时间戳为基线分配目录名，冲突则递增，保证目录名唯一且可读。
func (s *PipelineService) allocateID() string {
	id := time.Now().Unix()
	for s.store.Exists(strconv.FormatInt(id, 10)) {
		id++
	}
	return strconv.FormatInt(id, 10)
}

// nameRegex 返回当前生效的工程命名限制，未配置时为空。
func (s *PipelineService) nameRegex() string {
	if s.settings == nil {
		return ""
	}
	return s.settings.Current().JobNameRegex
}

// normalizeAndValidate 做字段归一化与必填校验（业务规则只允许出现在 service）。
func normalizeAndValidate(p *model.Pipeline, nameRegex string) error {
	p.Name = strings.TrimSpace(p.Name)
	p.Description = strings.TrimSpace(p.Description)
	p.Image = strings.TrimSpace(p.Image)
	p.GitURL = strings.TrimSpace(p.GitURL)
	p.GitBranch = strings.TrimSpace(p.GitBranch)
	p.GitCredential = strings.TrimSpace(p.GitCredential)
	p.WebhookSecret = strings.TrimSpace(p.WebhookSecret)
	// 平台已改为纯本地构建；executor/image 字段保留仅为兼容旧数据，不再参与调度。
	p.Executor = model.ExecutorLocal

	if p.Name == "" {
		return pkg.ErrBadRequest("name is required")
	}
	if nameRegex != "" {
		re, err := regexp.Compile(nameRegex)
		if err != nil {
			return pkg.ErrBadRequest("工程命名限制正则非法: %v", err)
		}
		if !re.MatchString(p.Name) {
			return pkg.ErrBadRequest("任务名称 %q 不满足命名限制：%s", p.Name, nameRegex)
		}
	}
	if len(p.Steps) == 0 {
		return pkg.ErrBadRequest("at least one build step is required")
	}
	for i := range p.Steps {
		p.Steps[i].Run = strings.TrimSpace(p.Steps[i].Run)
		p.Steps[i].Name = strings.TrimSpace(p.Steps[i].Name)
		if p.Steps[i].Name == "" {
			p.Steps[i].Name = "step-" + strconv.Itoa(i+1)
		}
		if p.Steps[i].Run == "" {
			return pkg.ErrBadRequest("step %q: run is required", p.Steps[i].Name)
		}
	}
	if p.GitBranch == "" {
		p.GitBranch = "main"
	}
	if p.BuildRetentionMaxPerJob < 0 {
		p.BuildRetentionMaxPerJob = 0
	}
	if p.BuildRetentionMaxAgeDays < 0 {
		p.BuildRetentionMaxAgeDays = 0
	}
	if p.DeleteWorkspaceBeforeBuild == nil {
		keep := false
		p.DeleteWorkspaceBeforeBuild = &keep
	}
	if err := normalizeArtifacts(&p.Artifacts); err != nil {
		return err
	}
	return validateSshPublish(p.SshPublish)
}

// normalizeArtifacts 去重/归一化产物匹配模式，并在启用时提前做安全校验。
// 禁用时保留模式，方便用户下次重新启用而不用重填。
func normalizeArtifacts(a *model.ArtifactConfig) error {
	files := make([]string, 0, len(a.Files))
	seen := make(map[string]bool)
	for _, raw := range a.Files {
		f := strings.TrimSpace(filepath.ToSlash(raw))
		if f == "" || seen[f] {
			continue
		}
		seen[f] = true
		files = append(files, f)
	}
	a.Files = files
	if !a.Enabled {
		return nil
	}
	if len(files) == 0 {
		return pkg.ErrBadRequest("artifacts.files is required when artifacts.enabled=true")
	}
	if err := executor.ValidateWorkspacePatterns(files); err != nil {
		return pkg.ErrBadRequest("artifacts.files: %v", err)
	}
	return nil
}

// validateSshPublish 仅在启用时校验 SSH 配置，未启用则清空以免残留。
// 新版任务只保存 server_id + 发布参数；旧版任务继续允许内联主机/账号配置。
func validateSshPublish(c *model.SshPublishConfig) error {
	if c == nil || !c.Enabled {
		return nil
	}
	c.ServerID = strings.TrimSpace(c.ServerID)
	c.RemoteBaseDir = strings.TrimSpace(c.RemoteBaseDir)
	c.RemovePrefix = strings.TrimSpace(c.RemovePrefix)
	c.ExecCommand = strings.TrimSpace(c.ExecCommand)

	files := make([]string, 0, len(c.SourceFiles))
	for _, f := range c.SourceFiles {
		if f = strings.TrimSpace(f); f != "" {
			files = append(files, f)
		}
	}
	c.SourceFiles = files
	if len(c.SourceFiles) == 0 {
		return pkg.ErrBadRequest("ssh_publish.source_files is required")
	}

	// 引用系统配置中的 SSH 服务器：连接信息由服务器提供，任务不保存任何密钥。
	if c.ServerID != "" {
		c.Host = ""
		c.Port = 0
		c.Username = ""
		c.AuthType = ""
		c.PrivateKeyPath = ""
		c.Password = ""
		return nil
	}

	// 旧版任务内联配置（向后兼容）。
	c.Host = strings.TrimSpace(c.Host)
	c.Username = strings.TrimSpace(c.Username)
	if c.AuthType == "" {
		c.AuthType = "key"
	}
	if c.AuthType != "key" && c.AuthType != "password" {
		return pkg.ErrBadRequest("ssh_publish.auth_type must be key or password")
	}
	if c.Host == "" || c.Username == "" {
		return pkg.ErrBadRequest("ssh_publish.host and ssh_publish.username are required")
	}
	if c.RemoteBaseDir == "" {
		return pkg.ErrBadRequest("ssh_publish.remote_base_dir is required")
	}
	if c.AuthType == "key" && c.PrivateKeyPath == "" {
		return pkg.ErrBadRequest("ssh_publish.private_key_path is required when auth_type=key")
	}
	if c.AuthType == "password" && c.Password == "" {
		return pkg.ErrBadRequest("ssh_publish.password is required when auth_type=password")
	}
	if c.Port <= 0 {
		c.Port = 22
	}
	return nil
}

// mergeSensitive 编辑时前端不回显敏感字段，为空则沿用旧值。
func mergeSensitive(old, in *model.Pipeline) {
	if old.SshPublish == nil || in.SshPublish == nil {
		return
	}
	if in.SshPublish.PrivateKeyPath == "" {
		in.SshPublish.PrivateKeyPath = old.SshPublish.PrivateKeyPath
	}
	if in.SshPublish.Password == "" {
		in.SshPublish.Password = old.SshPublish.Password
	}
}

// 从显式构建命令推断产物位置，用于旧任务的一次性自动归档迁移。
// 只认「明确写出输出文件名」的命令，避免把整个工作区误当产物。
var (
	goBuildOutputPattern = regexp.MustCompile(`(?i)go[ \t]+build[^\n]*-o[ \t]+("[^"]+"|'[^']+'|[^ \t]+)`)
	jarOutputPattern     = regexp.MustCompile(`(?i)jar[ \t]+c[a-z]*[ \t]+("[^"]+"|'[^']+'|[^ \t]+)`)
)

// inferArtifactPatterns 从步骤中推断产物模式。
// 支持：go build -o <file>、jar cf/cfe <file>；其他命令保持不启用，交给用户配置。
func inferArtifactPatterns(steps []model.Step) []string {
	var out []string
	seen := make(map[string]bool)
	add := func(patterns []string) {
		for _, pattern := range patterns {
			if pattern == "" || seen[pattern] {
				continue
			}
			seen[pattern] = true
			out = append(out, pattern)
		}
	}
	for _, step := range steps {
		if match := goBuildOutputPattern.FindStringSubmatch(step.Run); len(match) > 1 {
			add(inferOutputPatterns(match[1], true))
		}
		if match := jarOutputPattern.FindStringSubmatch(step.Run); len(match) > 1 {
			add(inferOutputPatterns(match[1], false))
		}
	}
	return out
}

// inferOutputPatterns 校验并展开一个输出文件名。
// addExe 为 true 时同时匹配 Windows 下可能自动追加的 .exe。
func inferOutputPatterns(target string, addExe bool) []string {
	target = strings.Trim(strings.TrimSpace(target), `"'`)
	target = filepath.ToSlash(target)
	if target == "" || target == "." || target == ".." ||
		strings.HasPrefix(target, "/") || strings.HasPrefix(target, "../") ||
		filepath.IsAbs(filepath.FromSlash(target)) || strings.ContainsAny(target, "$%*?") {
		return nil
	}
	out := []string{target}
	if addExe && !strings.HasSuffix(strings.ToLower(target), ".exe") {
		out = append(out, target+".exe")
	}
	return out
}

// MigrateArtifactConfig 为旧任务补齐产物归档配置：仅当配置完全为空且能从步骤
// 明确推断输出文件时启用。只运行一次（由 main 的 marker 控制），之后用户的修改会被尊重。
func (s *PipelineService) MigrateArtifactConfig(ctx context.Context) (int, error) {
	pipelines, err := s.store.List(ctx)
	if err != nil {
		return 0, err
	}
	migrated := 0
	for _, p := range pipelines {
		if p.Artifacts.Enabled || len(p.Artifacts.Files) > 0 {
			continue
		}
		files := inferArtifactPatterns(p.Steps)
		if len(files) == 0 {
			continue
		}
		p.Artifacts = model.ArtifactConfig{Enabled: true, Files: files}
		p.UpdatedAt = time.Now().UTC()
		if err = s.store.Update(ctx, p); err != nil {
			return migrated, err
		}
		migrated++
	}
	return migrated, nil
}

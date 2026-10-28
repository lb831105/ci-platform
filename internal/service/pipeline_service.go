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
	scoper    JobScoper // 任务可见性来源（RBAC），未装配时不做过滤
}

// JobScoper 提供某用户的任务可见性判定（由 RBACService 实现）。
type JobScoper interface {
	JobScope(user string) *JobScope
}

// NewPipelineService 构造 Pipeline 业务服务。
func NewPipelineService(store PipelineStore, settings *SettingsService, workspace *WorkspaceManager) *PipelineService {
	return &PipelineService{store: store, settings: settings, workspace: workspace}
}

// SetJobScoper 装上任务级权限来源；装配顺序上 RBACService 晚于本服务创建。
func (s *PipelineService) SetJobScoper(scoper JobScoper) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.scoper = scoper
}

// List 返回全部 pipeline（内部使用：调度、系统统计）。
func (s *PipelineService) List(ctx context.Context) ([]*model.Pipeline, error) {
	return s.store.List(ctx)
}

// ListFor 返回指定用户可见的 pipeline。
//
// 列表/概览/全局构建历史走这里，任务角色 Pattern 之外的任务直接不出现：
// 否则用户能看到「隔壁生产任务」的名字，只是在点进去时收到 403。
func (s *PipelineService) ListFor(ctx context.Context, user string) ([]*model.Pipeline, error) {
	items, err := s.store.List(ctx)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	scoper := s.scoper
	s.mu.Unlock()
	if scoper == nil {
		return items, nil
	}
	scope := scoper.JobScope(user)
	if scope.All() {
		return items, nil
	}
	visible := make([]*model.Pipeline, 0, len(items))
	for _, p := range items {
		if scope.Allows(p.Name) {
			visible = append(visible, p)
		}
	}
	return visible, nil
}

// Get 返回单个 pipeline。
func (s *PipelineService) Get(ctx context.Context, id string) (*model.Pipeline, error) {
	return s.store.Get(ctx, id)
}

// JobName 返回任务名（供权限中间件做角色 Pattern 匹配）。
func (s *PipelineService) JobName(ctx context.Context, id string) (string, error) {
	p, err := s.store.Get(ctx, id)
	if err != nil {
		return "", err
	}
	return p.Name, nil
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
	if err := normalizeAndValidate(in, s.nameRegex(), s.defaultGitBranch()); err != nil {
		return nil, err
	}
	if err := s.validateSshServer(in); err != nil {
		return nil, err
	}
	if err := s.validateToolSelections(in); err != nil {
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

// Validate 只做归一化与校验，不落盘（供「导入预览 / dry-run」使用）。
// 与 Create 走同一条校验路径，避免预览通过、真正导入却失败。
func (s *PipelineService) Validate(_ context.Context, in *model.Pipeline) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := normalizeAndValidate(in, s.nameRegex(), s.defaultGitBranch()); err != nil {
		return err
	}
	if err := s.validateToolSelections(in); err != nil {
		return err
	}
	return s.validateSshServer(in)
}

// SaveJenkinsSource 把导入来源的 config.xml 存到任务目录，便于审计与「导入器改进后重导」。
// 存储层没实现辅助文件写入时静默跳过（测试用的内存 store 即如此）。
func (s *PipelineService) SaveJenkinsSource(_ context.Context, id string, data []byte) error {
	writer, ok := s.store.(interface {
		WriteAuxFile(id, name string, data []byte) error
	})
	if !ok {
		return nil
	}
	return writer.WriteAuxFile(id, "jenkins-config.xml", data)
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
	// Webhook 密钥从不回显：前端提交空值时沿用旧密钥，避免「打开配置直接保存」
	// 把外部 Webhook 弄失效。需要更换时直接填入新密钥即可。
	if in.ClearWebhookSecret {
		in.WebhookSecret = ""
	} else if strings.TrimSpace(in.WebhookSecret) == "" {
		in.WebhookSecret = old.WebhookSecret
	}
	// 先合并敏感字段再校验：编辑时前端不回显密码，需用旧值补齐，
	// 否则 auth_type=password 的任务保存会被“密码必填”误拦截。
	mergeSensitive(old, in)
	mergeNotifyURLs(old, in)
	if err = normalizeAndValidate(in, s.nameRegex(), s.defaultGitBranch()); err != nil {
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

// Delete 删除任务（连同其全部构建记录与工作区目录，递归删目录）。
// 工作区目录一并删除，避免同名任务重建时残留旧构建产物；
// 正在执行的构建会因工作区消失而失败（删除是罕见操作，可接受）。
func (s *PipelineService) Delete(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, err := s.store.Get(ctx, id)
	if err != nil {
		return err
	}
	// 先删工作区目录（尽力而为；失败不阻塞任务删除）。
	if s.workspace != nil {
		ws := s.workspace.Path(Job{PipelineID: id, WorkspaceDir: p.WorkspaceDir})
		if err := pkg.RemoveAll(ws); err != nil {
			return pkg.ErrInternal("删除工作区目录失败: %v", err)
		}
	}
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

// validateSshServer 确保每个启用的发布目标引用的 SSH 服务器都存在于系统配置中。
// 逐个目标校验：老实现只看单目标 p.SshPublish，多目标任务在归一化后该字段为 nil。
func (s *PipelineService) validateSshServer(p *model.Pipeline) error {
	if s.settings == nil {
		return nil
	}
	servers := s.settings.Current().SshServers
	for _, cfg := range p.EffectiveSshPublishes() {
		serverID := strings.TrimSpace(cfg.ServerID)
		if serverID == "" {
			continue
		}
		found := false
		for _, server := range servers {
			if server.ID == serverID {
				found = true
				break
			}
		}
		if !found {
			return pkg.ErrBadRequest("发布目标 %q 引用的 SSH 服务器 %q 不存在，请先在系统配置中添加", cfg.Name, serverID)
		}
	}
	return nil
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

// maxApprovalTimeoutMinutes 是发布审批等待上限（7 天）。
const maxApprovalTimeoutMinutes = 7 * 24 * 60

// defaultGitBranch 返回「系统配置 → 默认构建参数 → 默认分支」，未配置时回退 main。
// 任务留空分支时用它补齐：该配置项既然放在界面上，就必须真的影响新建任务。
func (s *PipelineService) defaultGitBranch() string {
	if s.settings == nil {
		return ""
	}
	return s.settings.Current().DefaultBranch
}

// normalizeAndValidate 做字段归一化与必填校验（业务规则只允许出现在 service）。
// defaultBranch 可选：任务未填分支时用它兜底（缺省 main）。
func normalizeAndValidate(p *model.Pipeline, nameRegex string, defaultBranch ...string) error {
	p.Name = strings.TrimSpace(p.Name)
	p.Description = strings.TrimSpace(p.Description)
	p.Environment = strings.TrimSpace(p.Environment)
	if len([]rune(p.Environment)) > 64 {
		return pkg.ErrBadRequest("环境名称最多 64 个字符")
	}
	p.Image = strings.TrimSpace(p.Image)
	p.GitURL = strings.TrimSpace(p.GitURL)
	p.GitBranch = strings.TrimSpace(p.GitBranch)
	p.GitCredential = strings.TrimSpace(p.GitCredential)
	p.WebhookSecret = strings.TrimSpace(p.WebhookSecret)
	p.HasWebhookSecret = false // 只读提示位，不进入持久化配置
	p.ClearWebhookSecret = false
	// 平台已改为纯本地构建；executor/image 字段保留仅为兼容旧数据，不再参与调度。
	p.Executor = model.ExecutorLocal
	// Webhook 触发器策略（事件 / 分支 / 回写），业务规则只在这里校验。
	if err := normalizeWebhookTrigger(p); err != nil {
		return err
	}
	// 构建工具版本：只允许选「全局工具配置」里真实存在的安装项（Jenkins 也是从列表里选）。
	if err := normalizeToolSelections(p); err != nil {
		return err
	}
	// 定时构建：保存前校验 cron 语法，避免任务保存成功但永不触发。
	p.CronSpec = strings.TrimSpace(p.CronSpec)
	if p.CronSpec != "" {
		if _, err := NextCron(p.CronSpec, p.Name, time.Now()); err != nil {
			return pkg.ErrBadRequest("定时构建规格非法: %v", err)
		}
	}
	// 通知配置：校验渠道、邮箱与数量上限（防误配置导致漏告警或刷屏）。
	if err := normalizeNotify(p.Notify); err != nil {
		return err
	}

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
		if err := normalizeStepControls(&p.Steps[i]); err != nil {
			return err
		}
	}
	if p.GitBranch == "" {
		p.GitBranch = "main"
		if len(defaultBranch) > 0 && strings.TrimSpace(defaultBranch[0]) != "" {
			p.GitBranch = strings.TrimSpace(defaultBranch[0])
		}
	}
	// 发布审批：超时上限 7 天，避免笔误把发布卡很久；不开审批时不保留超时值。
	if p.RequireApproval {
		if p.ApprovalTimeoutMinutes < 0 {
			return pkg.ErrBadRequest("审批超时时间不能为负数")
		}
		if p.ApprovalTimeoutMinutes > maxApprovalTimeoutMinutes {
			return pkg.ErrBadRequest("审批超时时间不能超过 %d 分钟", maxApprovalTimeoutMinutes)
		}
	} else {
		p.ApprovalTimeoutMinutes = 0
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
	if err := normalizeTestReport(&p.TestReport); err != nil {
		return err
	}
	if err := normalizeCache(&p.Cache); err != nil {
		return err
	}
	if err := normalizeParams(p); err != nil {
		return err
	}
	return normalizeSshPublishes(p)
}

// normalizeCache 校验显式缓存配置。缓存 key 允许 ${PARAM} 占位符，
// 保存阶段只校验字符集，真正展开在构建时按本次参数进行。
func normalizeCache(c **model.CacheConfig) error {
	if c == nil || *c == nil || !(*c).Enabled {
		*c = nil
		return nil
	}
	cfg := *c
	cfg.Key = strings.TrimSpace(cfg.Key)
	if cfg.Key == "" {
		cfg.Key = "default"
	}
	if len([]rune(cfg.Key)) > 128 {
		return pkg.ErrBadRequest("缓存 key 最多 128 个字符")
	}
	for _, r := range cfg.Key {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' ||
			r == '.' || r == '_' || r == '-' || r == '$' || r == '{' || r == '}' {
			continue
		}
		return pkg.ErrBadRequest("缓存 key 只能包含字母、数字、._- 与 ${参数} 占位符")
	}
	seen := make(map[string]bool, len(cfg.Paths))
	paths := make([]string, 0, len(cfg.Paths))
	for _, raw := range cfg.Paths {
		raw = strings.TrimSpace(raw)
		if raw == "" || seen[raw] {
			continue
		}
		seen[raw] = true
		paths = append(paths, raw)
	}
	if len(paths) == 0 {
		return pkg.ErrBadRequest("启用缓存后至少要配置一个缓存路径")
	}
	if len(paths) > 50 {
		return pkg.ErrBadRequest("缓存路径最多 50 个")
	}
	if err := executor.ValidateWorkspacePatterns(paths); err != nil {
		return pkg.ErrBadRequest("缓存路径非法: %v", err)
	}
	cfg.Paths = paths
	*c = cfg
	return nil
}

// normalizeSshPublishes 归一化并校验多目标 SSH 发布配置。
//
// 存储形态只有一种：ssh_publishes 列表。旧任务的 ssh_publish（单目标）在这里
// 迁移进列表并清空旧字段，避免「两个字段都可能生效」的歧义。
func normalizeSshPublishes(p *model.Pipeline) error {
	list := p.SshPublishes
	if len(list) == 0 && p.SshPublish != nil {
		legacy := *p.SshPublish
		if strings.TrimSpace(legacy.Name) == "" {
			legacy.Name = "默认发布"
		}
		list = []model.SshPublishConfig{legacy}
	}
	p.SshPublish = nil

	if len(list) > maxSshPublishes {
		return pkg.ErrBadRequest("SSH 发布目标最多 %d 个（当前 %d）", maxSshPublishes, len(list))
	}
	used := map[string]bool{}
	out := make([]model.SshPublishConfig, 0, len(list))
	for i := range list {
		item := list[i]
		name := strings.TrimSpace(item.Name)
		if name == "" {
			name = fmt.Sprintf("发布 %d", i+1)
		}
		// 重名会让日志与「重新发布」无法区分：自动加后缀而不是报错
		// （前端新增一行时名称通常还是空的）。
		unique := name
		for n := 2; used[strings.ToLower(unique)]; n++ {
			unique = fmt.Sprintf("%s (%d)", name, n)
		}
		used[strings.ToLower(unique)] = true
		item.Name = unique
		if err := validateSshPublish(&item); err != nil {
			return err
		}
		out = append(out, item)
	}
	p.SshPublishes = out
	return nil
}

// maxSshPublishes 限制单次构建的发布目标数量，避免误配导致构建卡在大量 SSH 连接上。
const maxSshPublishes = 10

// normalizeTestReport 去重/归一化 JUnit 报告匹配模式，并在启用时提前校验路径安全。
func normalizeTestReport(t *model.TestReportConfig) error {
	files := make([]string, 0, len(t.Files))
	seen := make(map[string]bool)
	for _, raw := range t.Files {
		f := strings.TrimSpace(filepath.ToSlash(raw))
		if f == "" || seen[f] {
			continue
		}
		seen[f] = true
		files = append(files, f)
	}
	t.Files = files
	if !t.Enabled {
		return nil
	}
	if len(files) == 0 {
		return pkg.ErrBadRequest("test_report.files is required when test_report.enabled=true")
	}
	if err := executor.ValidateWorkspacePatterns(files); err != nil {
		return pkg.ErrBadRequest("test_report.files: %v", err)
	}
	return nil
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
	if c.ExecTimeoutSec < 0 || c.ExecTimeoutSec > model.MaxStepTimeoutSec {
		return pkg.ErrBadRequest("ssh_publish.exec_timeout_sec 需在 0~%d 秒之间", model.MaxStepTimeoutSec)
	}
	if c.CleanRemote && (c.RemoteBaseDir == "" || c.RemoteBaseDir == "/") {
		// 清空远程目录是不可逆操作：没有明确目录时直接拒绝，避免误删根目录。
		return pkg.ErrBadRequest("发布目标 %q 勾选了「清空远程目录」，必须填写明确的远程目录", c.Name)
	}

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
// normalizeNotify 清洗并校验任务级通知配置：
// 去重人员/邮箱、校验渠道合法性与邮箱格式、限制数量（防刷屏）。
func normalizeNotify(n *model.JobNotify) error {
	if n == nil {
		return nil
	}
	people := dedupeNames(n.People)
	if len(people) > 50 {
		return pkg.ErrBadRequest("通知人员最多 50 人（当前 %d）", len(people))
	}
	n.People = people

	emails, err := normalizeEmailList(n.Emails, "通知邮箱")
	if err != nil {
		return err
	}
	n.Emails = emails

	targets, err := normalizeNotifyTargets(n.Targets, "群机器人")
	if err != nil {
		return err
	}
	n.Targets = targets

	// ---- 值班轮值 ----
	if n.OnCall != nil {
		if err = normalizeOnCall(n.OnCall); err != nil {
			return err
		}
	}

	// ---- 升级策略 ----
	if n.Escalation != nil {
		if err = normalizeEscalation(n.Escalation); err != nil {
			return err
		}
	}

	// ---- 抑制窗口 ----
	switch {
	case n.SuppressWindowMin < 0:
		return pkg.ErrBadRequest("抑制窗口不能为负数")
	case n.SuppressWindowMin > 1440:
		return pkg.ErrBadRequest("抑制窗口最多 1440 分钟（24 小时），当前 %d", n.SuppressWindowMin)
	case n.SuppressWindowMin == 1:
		// 1 分钟窗口几乎总在窗口内，等于永久静默；提示改用 0（关闭）或 >=2。
		return pkg.ErrBadRequest("抑制窗口最小 2 分钟（1 分钟会几乎永久静默），0 表示关闭")
	}

	// ---- 分支路由 ----
	routes := make([]model.NotifyRoute, 0, len(n.Routes))
	for _, r := range n.Routes {
		r.Match = strings.TrimSpace(r.Match)
		r.People = dedupeNames(r.People)
		emails, eerr := normalizeEmailList(r.Emails, "路由邮箱")
		if eerr != nil {
			return eerr
		}
		r.Emails = emails
		rt, terr := normalizeNotifyTargets(r.Targets, "路由群机器人")
		if terr != nil {
			return terr
		}
		r.Targets = rt
		if r.Match == "" && len(r.People) == 0 && len(r.Emails) == 0 && len(r.Targets) == 0 && !r.Committer && !r.OnCall {
			continue // 空路由：忽略而不是报错（前端新增行后未填）
		}
		routes = append(routes, r)
	}
	if len(routes) > 20 {
		return pkg.ErrBadRequest("分支路由最多 20 条（当前 %d）", len(routes))
	}
	n.Routes = routes

	// ---- 消息模板 ----
	n.Template = strings.TrimSpace(n.Template)
	if len(n.Template) > 4000 {
		return pkg.ErrBadRequest("消息模板最多 4000 字符（当前 %d）", len(n.Template))
	}
	return nil
}

// normalizeNotifyTargets 校验并去重群机器人：按「渠道 + 脱敏 URL」去重，
// 避免同一条告警重复推送；渠道统一为 dingtalk / wecom / slack。
func normalizeNotifyTargets(in []model.NotifyTarget, label string) ([]model.NotifyTarget, error) {
	seen := map[string]bool{}
	out := make([]model.NotifyTarget, 0, len(in))
	for _, t := range in {
		t.URL = strings.TrimSpace(t.URL)
		if t.URL == "" {
			continue
		}
		raw := t.Channel
		t.Channel = normalizeNotifyChannel(raw)
		switch t.Channel {
		case "slack", "wecom", "dingtalk":
		default:
			return nil, pkg.ErrBadRequest("不支持的通知渠道 %q（可选 dingtalk / wecom / slack）", raw)
		}
		if !strings.HasPrefix(t.URL, "http://") && !strings.HasPrefix(t.URL, "https://") {
			return nil, pkg.ErrBadRequest("%s URL 必须以 http:// 或 https:// 开头", label)
		}
		if err := ValidateNotifyTargetURL(t.URL); err != nil {
			return nil, notifyURLValidationError(label, t.URL, err)
		}
		key := t.Channel + "|" + pkg.MaskWebhookURL(t.Channel, t.URL)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, t)
	}
	if len(out) > 20 {
		return nil, pkg.ErrBadRequest("%s最多 20 个（当前 %d）", label, len(out))
	}
	return out, nil
}

// normalizeOnCall 校验值班轮值表：启用人必须非空，起始日期可解析，周期 1~90 天。
func normalizeOnCall(c *model.OnCallConfig) error {
	c.Rotation = dedupeNames(c.Rotation)
	if len(c.Rotation) > 50 {
		return pkg.ErrBadRequest("值班轮值最多 50 人（当前 %d）", len(c.Rotation))
	}
	if c.Enabled && len(c.Rotation) == 0 {
		return pkg.ErrBadRequest("启用值班轮值需要至少填写 1 个值班人")
	}
	c.StartDate = strings.TrimSpace(c.StartDate)
	if c.StartDate != "" {
		if _, err := time.ParseInLocation("2006-01-02", c.StartDate, time.Local); err != nil {
			return pkg.ErrBadRequest("值班起始日期格式应为 YYYY-MM-DD（收到 %q）", c.StartDate)
		}
	}
	switch {
	case c.PeriodDays == 0:
		c.PeriodDays = 7
	case c.PeriodDays < 1 || c.PeriodDays > 90:
		return pkg.ErrBadRequest("每人值班天数应在 1~90 之间（当前 %d）", c.PeriodDays)
	}
	return nil
}

// normalizeEscalation 校验升级策略：启用时阈值 2~20、且至少要有一个升级目标。
func normalizeEscalation(c *model.EscalationConfig) error {
	c.People = dedupeNames(c.People)
	emails, err := normalizeEmailList(c.Emails, "升级邮箱")
	if err != nil {
		return err
	}
	c.Emails = emails
	targets, err := normalizeNotifyTargets(c.Targets, "升级群机器人")
	if err != nil {
		return err
	}
	c.Targets = targets
	if !c.Enabled {
		return nil
	}
	if c.After == 0 {
		c.After = 3
	}
	if c.After < 2 || c.After > 20 {
		return pkg.ErrBadRequest("升级阈值应在 2~20 次连续失败之间（当前 %d）", c.After)
	}
	if len(c.People) == 0 && len(c.Emails) == 0 && len(c.Targets) == 0 {
		return pkg.ErrBadRequest("启用升级策略需要至少配置 1 个升级人员、邮箱或群机器人")
	}
	return nil
}

// dedupeNames 去重（忽略大小写）并裁剪空白，保持顺序。
func dedupeNames(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, name := range in {
		name = strings.TrimSpace(name)
		if name == "" || seen[strings.ToLower(name)] {
			continue
		}
		seen[strings.ToLower(name)] = true
		out = append(out, name)
	}
	return out
}

// normalizeEmailList 去重并校验邮箱格式（允许空列表）。
func normalizeEmailList(in []string, label string) ([]string, error) {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, e := range in {
		e = strings.TrimSpace(e)
		if e == "" || seen[strings.ToLower(e)] {
			continue
		}
		if !strings.Contains(e, "@") {
			return nil, pkg.ErrBadRequest("%s %q 格式不正确", label, e)
		}
		seen[strings.ToLower(e)] = true
		out = append(out, e)
	}
	if len(out) > 50 {
		return nil, pkg.ErrBadRequest("%s最多 50 个（当前 %d）", label, len(out))
	}
	return out, nil
}

// mergeNotifyURLs 用旧配置回填任务级通知群机器人的真实 URL：
// 接口返回时 URL 中的 access_token 等被脱敏为 ***，前端原样提交时
// 需要还原，否则保存会把真实密钥覆盖成 ***。
//
// 匹配键 = 规范化渠道 + 脱敏形态。先规范化渠道（大小写/别名）再建索引：
// 本函数在校验（normalizeNotify）之前执行，若用原始渠道字符串，
// "WeChat"/""/"dingtalk " 等写法会造成键不一致，导致真实密钥被 *** 覆盖。
func mergeNotifyURLs(old, in *model.Pipeline) {
	if old.Notify == nil || in.Notify == nil {
		return
	}
	// 按脱敏形态建立旧 URL 索引，避免同渠道多群时张冠李戴。
	index := map[string]string{}
	addIndex := func(targets []model.NotifyTarget) {
		for _, t := range targets {
			ch := normalizeNotifyChannel(t.Channel)
			index[ch+"|"+pkg.MaskWebhookURL(ch, t.URL)] = t.URL
		}
	}
	addIndex(old.Notify.Targets)
	// 升级目标与分支路由的群同样会被脱敏返回，一并回填，
	// 否则「打开配置直接保存」会把它们的 *** 存成真实地址。
	if old.Notify.Escalation != nil {
		addIndex(old.Notify.Escalation.Targets)
	}
	for _, r := range old.Notify.Routes {
		addIndex(r.Targets)
	}

	restore := func(targets []model.NotifyTarget) {
		for i := range targets {
			t := &targets[i]
			if !strings.Contains(t.URL, "***") {
				continue
			}
			ch := normalizeNotifyChannel(t.Channel)
			if real, ok := index[ch+"|"+t.URL]; ok {
				t.URL = real
			}
		}
	}
	restore(in.Notify.Targets)
	if in.Notify.Escalation != nil {
		restore(in.Notify.Escalation.Targets)
	}
	for i := range in.Notify.Routes {
		restore(in.Notify.Routes[i].Targets)
	}
}

// normalizeNotifyChannel 统一渠道名：小写、常见别名归一、空值视为 slack。
func normalizeNotifyChannel(channel string) string {
	ch := strings.ToLower(strings.TrimSpace(channel))
	switch ch {
	case "", "slack":
		return "slack"
	case "wechat", "weixin", "qyweixin", "wecom":
		return "wecom"
	default:
		return ch
	}
}

func mergeSensitive(old, in *model.Pipeline) {
	if old.SshPublish != nil && in.SshPublish != nil {
		if in.SshPublish.PrivateKeyPath == "" {
			in.SshPublish.PrivateKeyPath = old.SshPublish.PrivateKeyPath
		}
		if in.SshPublish.Password == "" {
			in.SshPublish.Password = old.SshPublish.Password
		}
	}
	// 多目标发布：接口返回时密码被清空，编辑保存时按 server_id 回填旧密码。
	// 同一个 server_id 可能被多个目标复用（不同远程目录），这里只按顺序匹配，
	// 密码相同不影响正确性；服务器级凭据优先，内联密码仅用于兼容旧任务。
	if len(in.SshPublishes) == 0 || len(old.SshPublishes) == 0 {
		return
	}
	oldByKey := make(map[string]model.SshPublishConfig, len(old.SshPublishes))
	for _, item := range old.SshPublishes {
		key := sshPublishMergeKey(item)
		if key != "" {
			oldByKey[key] = item
		}
	}
	for i := range in.SshPublishes {
		key := sshPublishMergeKey(in.SshPublishes[i])
		if key == "" {
			continue
		}
		prev, ok := oldByKey[key]
		if !ok {
			continue
		}
		if in.SshPublishes[i].PrivateKeyPath == "" {
			in.SshPublishes[i].PrivateKeyPath = prev.PrivateKeyPath
		}
		if in.SshPublishes[i].Password == "" {
			in.SshPublishes[i].Password = prev.Password
		}
	}
}

// sshPublishMergeKey 返回多目标敏感字段回填的匹配键。
// 优先用稳定的 server_id；旧版内联配置没有 server_id 时退回 host+username。
func sshPublishMergeKey(item model.SshPublishConfig) string {
	if id := strings.TrimSpace(item.ServerID); id != "" {
		return "server:" + strings.ToLower(id)
	}
	host := strings.TrimSpace(item.Host)
	user := strings.TrimSpace(item.Username)
	if host == "" || user == "" {
		return ""
	}
	return "inline:" + strings.ToLower(host) + "|" + user
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

const (
	maxBuildParams  = 30
	maxParamNameLen = 64
	maxParamValLen  = 4096
)

// paramNamePattern 限制参数名只能是合法的标识符：
// 参数会被写成环境变量（NAME 与 CI_PARAM_NAME），非法字符会让 shell 无法读取。
var paramNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// reservedEnvNames 是构建参数禁止覆盖的危险环境变量。
// 任务参数最终会注入步骤进程；允许覆盖 PATH/LD_PRELOAD/NODE_OPTIONS 等
// 等于绕过平台的工具链与进程隔离，可能加载任意动态库或执行非预期程序。
var reservedEnvNames = map[string]bool{
	"PATH": true, "HOME": true, "TMPDIR": true, "TEMP": true, "TMP": true,
	"LD_PRELOAD": true, "LD_LIBRARY_PATH": true, "DYLD_INSERT_LIBRARIES": true,
	"DYLD_LIBRARY_PATH": true, "BASH_ENV": true, "IFS": true,
	"CDPATH": true, "GLOBIGNORE": true, "SHELL": true, "COMSPEC": true, "PATHEXT": true,
	"NODE_OPTIONS": true, "PYTHONPATH": true, "PYTHONHOME": true,
	"GEM_HOME": true, "GEM_PATH": true, "JAVA_TOOL_OPTIONS": true, "_JAVA_OPTIONS": true,
	"MAVEN_OPTS": true, "GRADLE_OPTS": true, "GIT_SSH_COMMAND": true,
	"GIT_CONFIG_GLOBAL": true, "GIT_CONFIG_SYSTEM": true, "SSH_AUTH_SOCK": true,
	"HTTP_PROXY": true, "HTTPS_PROXY": true, "ALL_PROXY": true, "NO_PROXY": true,
}

// normalizeParams 归一化并校验参数定义（对齐 Jenkins Parameters 的常用能力）。
func normalizeParams(p *model.Pipeline) error {
	if len(p.Params) == 0 {
		p.Params = nil
		return nil
	}
	if len(p.Params) > maxBuildParams {
		return pkg.ErrBadRequest("最多支持 %d 个构建参数", maxBuildParams)
	}
	seen := make(map[string]bool, len(p.Params))
	out := make([]model.ParamDef, 0, len(p.Params))
	for _, def := range p.Params {
		def.Name = strings.TrimSpace(def.Name)
		if def.Name == "" {
			return pkg.ErrBadRequest("构建参数名不能为空")
		}
		if len(def.Name) > maxParamNameLen || !paramNamePattern.MatchString(def.Name) {
			return pkg.ErrBadRequest("参数名 %q 非法：只能用字母、数字、下划线，且不能以数字开头", def.Name)
		}
		if reservedEnvNames[strings.ToUpper(def.Name)] {
			return pkg.ErrBadRequest("参数名 %q 是平台保留的环境变量，不能覆盖", def.Name)
		}
		// CI_ 前缀是平台保留命名空间，避免任务覆盖 CI_BUILD_ID 等内置变量。
		if strings.HasPrefix(strings.ToUpper(def.Name), "CI_") {
			return pkg.ErrBadRequest("参数名 %q 非法：CI_ 前缀为平台保留", def.Name)
		}
		key := strings.ToUpper(def.Name)
		if seen[key] {
			// 环境变量在 Windows 上大小写不敏感，version 与 VERSION 会互相覆盖。
			return pkg.ErrBadRequest("参数名 %q 重复（环境变量大小写不敏感）", def.Name)
		}
		seen[key] = true

		def.Type = strings.ToLower(strings.TrimSpace(def.Type))
		if def.Type == "" {
			def.Type = model.ParamString
		}
		switch def.Type {
		case model.ParamString, model.ParamBool, model.ParamText, model.ParamChoice, model.ParamPassword:
		default:
			return pkg.ErrBadRequest("参数 %q 的类型 %q 不支持（string/bool/choice/text/password）", def.Name, def.Type)
		}
		if def.Type == model.ParamPassword && strings.TrimSpace(def.Default) != "" {
			// 密码参数的默认值会写进 pipeline 配置，等于把密钥长期明文保存在任务里。
			return pkg.ErrBadRequest("密码参数 %q 不能设置默认值，请在触发构建时填写", def.Name)
		}
		def.Description = strings.TrimSpace(def.Description)
		options := make([]string, 0, len(def.Options))
		for _, opt := range def.Options {
			opt = strings.TrimSpace(opt)
			if opt != "" {
				options = append(options, opt)
			}
		}
		def.Options = options
		if def.Type == model.ParamChoice {
			if len(options) == 0 {
				return pkg.ErrBadRequest("参数 %q 是 choice 类型，至少需要一个可选值", def.Name)
			}
		} else {
			def.Options = nil
		}

		def.Default = strings.TrimSpace(def.Default)
		switch def.Type {
		case model.ParamChoice:
			if def.Default == "" {
				def.Default = options[0]
			} else if !containsString(options, def.Default) {
				return pkg.ErrBadRequest("参数 %q 的默认值 %q 不在可选值中", def.Name, def.Default)
			}
		case model.ParamBool:
			if def.Default == "" {
				def.Default = "false"
			}
			if def.Default != "true" && def.Default != "false" {
				return pkg.ErrBadRequest("参数 %q 是 bool 类型，默认值只能是 true/false", def.Name)
			}
		}
		if len(def.Default) > maxParamValLen {
			return pkg.ErrBadRequest("参数 %q 的默认值过长", def.Name)
		}
		if def.Required && def.Default == "" && def.Type != model.ParamBool {
			// 必填但无默认值：触发时必须由用户填写，这本身是合法配置。
			def.Required = true
		}
		out = append(out, def)
	}
	p.Params = out
	return nil
}

// ResolveBuildParams 把用户传入的参数值按定义解析成最终值：
// 未传的参数取默认值，未知参数视为错误（多半是前端契约不一致或拼写错误）。
func ResolveBuildParams(defs []model.ParamDef, in map[string]string) (map[string]string, error) {
	if len(defs) == 0 {
		if len(in) > 0 {
			return nil, pkg.ErrBadRequest("该任务没有定义构建参数")
		}
		return nil, nil
	}
	known := make(map[string]bool, len(defs))
	resolved := make(map[string]string, len(defs))
	for _, def := range defs {
		known[def.Name] = true
		raw, ok := in[def.Name]
		if !ok {
			// 兼容大小写不一致（CI 命令行里 VERSION=1 与 version=1 应等价）。
			for k, v := range in {
				if strings.EqualFold(k, def.Name) {
					raw, ok = v, true
					break
				}
			}
		}
		value := def.Default
		if ok {
			if def.Type == model.ParamPassword {
				// 密码可能以空格开头/结尾，不能 TrimSpace，否则会出现“同一密码登录失败”。
				value = raw
			} else {
				value = strings.TrimSpace(raw)
			}
		}
		switch def.Type {
		case model.ParamBool:
			switch strings.ToLower(value) {
			case "", "false", "0", "no":
				value = "false"
			case "true", "1", "yes":
				value = "true"
			default:
				return nil, pkg.ErrBadRequest("参数 %q 只能是 true 或 false", def.Name)
			}
		case model.ParamChoice:
			if value == "" {
				value = def.Default
			}
			if !containsString(def.Options, value) {
				return nil, pkg.ErrBadRequest("参数 %q 的值 %q 不在可选值中", def.Name, value)
			}
		default:
			if value == "" && def.Required {
				return nil, pkg.ErrBadRequest("参数 %q 为必填项", def.Name)
			}
			// string 类型是单行值，换行会破坏 shell 变量；text 保留换行。
			if def.Type == model.ParamString {
				value = singleLine(value)
			}
			if len(value) > maxParamValLen {
				return nil, pkg.ErrBadRequest("参数 %q 的值过长（上限 %d 字节）", def.Name, maxParamValLen)
			}
		}
		resolved[def.Name] = value
	}
	for k := range in {
		if known[k] {
			continue
		}
		matched := false
		for name := range known {
			if strings.EqualFold(k, name) {
				matched = true
				break
			}
		}
		if !matched {
			return nil, pkg.ErrBadRequest("未知的构建参数 %q", k)
		}
	}
	return resolved, nil
}

// singleLine 把多行值压成单行：string 参数是单行值，换行会破坏 shell 变量赋值。
func singleLine(s string) string {
	const (
		lf = rune(10)
		cr = rune(13)
	)
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r == lf || r == cr {
			r = ' '
		}
		b.WriteRune(r)
	}
	return b.String()
}

func containsString(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}

// normalizeStepControls 归一化并校验步骤级控制项（超时/重试/失败处理/when）。
// 上限在这里硬性拦截：误填 retry=1000 会让构建卡几小时，且难以及时发现。
func normalizeStepControls(step *model.Step) error {
	if step.TimeoutSec < 0 || step.TimeoutSec > model.MaxStepTimeoutSec {
		return pkg.ErrBadRequest("step %q: timeout_sec 需在 0~%d 秒之间", step.Name, model.MaxStepTimeoutSec)
	}
	if step.Retry < 0 || step.Retry > model.MaxStepRetry {
		return pkg.ErrBadRequest("step %q: retry 需在 0~%d 次之间", step.Name, model.MaxStepRetry)
	}
	if step.RetryDelaySec < 0 || step.RetryDelaySec > model.MaxStepRetryDelay {
		return pkg.ErrBadRequest("step %q: retry_delay_sec 需在 0~%d 秒之间", step.Name, model.MaxStepRetryDelay)
	}
	when := strings.ToLower(strings.TrimSpace(step.When))
	switch when {
	case "", model.StepWhenSuccess:
		step.When = model.StepWhenSuccess
	case model.StepWhenFailure, model.StepWhenAlways:
		step.When = when
	default:
		return pkg.ErrBadRequest("step %q: when 只能是 success / failure / always", step.Name)
	}
	if step.AllowFailure && step.When == model.StepWhenFailure {
		// 「只在失败时执行」又「允许失败」是自相矛盾的配置，直接提示改配置。
		return pkg.ErrBadRequest("step %q: when=failure 的步骤不能同时设置 allow_failure", step.Name)
	}
	return nil
}

// normalizeToolSelections 归一化并校验任务级的工具版本选择。
//
// 只做格式与"是否存在"的校验：值必须能在对应类别的安装项里找到，
// 否则构建时会静默退回默认版本，用户以为选了 8 结果用 17 —— 这种坑必须保存时就报错。
func normalizeToolSelections(p *model.Pipeline) error {
	if len(p.ToolSelections) == 0 {
		p.ToolSelections = nil
		return nil
	}
	cleaned := make(map[string]string, len(p.ToolSelections))
	for rawCategory, rawName := range p.ToolSelections {
		categoryID := strings.ToLower(strings.TrimSpace(rawCategory))
		name := strings.TrimSpace(rawName)
		if categoryID == "" || name == "" {
			continue // 空选择等于"用默认"，不当作错误
		}
		cleaned[categoryID] = name
	}
	if len(cleaned) == 0 {
		p.ToolSelections = nil
		return nil
	}
	p.ToolSelections = cleaned
	return nil
}

// validateToolSelections 用当前全局工具配置校验选择（需要 settings，故单独一步）。
func (s *PipelineService) validateToolSelections(p *model.Pipeline) error {
	if len(p.ToolSelections) == 0 || s.settings == nil {
		return nil
	}
	settings := s.settings.Current()
	for categoryID, name := range p.ToolSelections {
		var category *model.ToolCategory
		for i := range settings.ToolCategories {
			if settings.ToolCategories[i].ID == categoryID {
				category = &settings.ToolCategories[i]
				break
			}
		}
		if category == nil {
			return pkg.ErrBadRequest("构建工具版本：平台没有「%s」这个工具类别", categoryID)
		}
		found := false
		for _, item := range category.Installations {
			if item.Name == name {
				found = true
				break
			}
		}
		if !found {
			return pkg.ErrBadRequest("构建工具版本：%s 下没有名为「%s」的安装项（请在「系统管理 → 全局工具配置」中添加）",
				category.Title, name)
		}
	}
	return nil
}

// maxWebhookBranches 是分支白名单上限：条目过多通常意味着该拆任务了。
const maxWebhookBranches = 30

// normalizeWebhookTrigger 归一化并校验 Webhook 触发器配置。
//
// 为什么要在这里硬校验：分支白名单写错（比如写成 "release-*" 却期望匹配 "release/1.0"）
// 的症状是"Webhook 返回成功但从不构建"，排查成本远高于保存时直接报错。
func normalizeWebhookTrigger(p *model.Pipeline) error {
	p.WebhookBranchMode = strings.ToLower(strings.TrimSpace(p.WebhookBranchMode))
	switch p.WebhookBranchMode {
	case "", "payload", "fixed":
	default:
		return pkg.ErrBadRequest("Webhook 分支策略只能是 payload 或 fixed")
	}
	// 事件白名单：去重、只允许 push / tag_push。
	events := make([]string, 0, len(p.WebhookEvents))
	for _, raw := range p.WebhookEvents {
		e := strings.ToLower(strings.TrimSpace(raw))
		if e == "" {
			continue
		}
		if e != "push" && e != "tag_push" {
			return pkg.ErrBadRequest("不支持的 Webhook 事件类型 %q（可选 push / tag_push）", raw)
		}
		if !containsFold(events, e) {
			events = append(events, e)
		}
	}
	p.WebhookEvents = events

	// 分支白名单：去空、去重、长度与合法性校验。
	branches := make([]string, 0, len(p.WebhookBranches))
	for _, raw := range p.WebhookBranches {
		b := strings.TrimSpace(raw)
		if b == "" {
			continue
		}
		if len(b) > maxBranchNameLen {
			return pkg.ErrBadRequest("分支匹配模式过长：%s", b)
		}
		if strings.ContainsAny(b, " 	") {
			return pkg.ErrBadRequest("分支匹配模式不能包含空白字符：%q", b)
		}
		if !containsFold(branches, b) {
			branches = append(branches, b)
		}
	}
	if len(branches) > maxWebhookBranches {
		return pkg.ErrBadRequest("Webhook 分支白名单最多 %d 条（当前 %d）", maxWebhookBranches, len(branches))
	}
	p.WebhookBranches = branches

	// 状态回写：启用时必须指定凭据（没有 token 无法写 GitLab）。
	p.CommitStatus.CredentialID = strings.TrimSpace(p.CommitStatus.CredentialID)
	p.CommitStatus.Project = strings.TrimSpace(p.CommitStatus.Project)
	if p.CommitStatus.Enabled && p.CommitStatus.CredentialID == "" {
		return pkg.ErrBadRequest("开启「回写构建状态」需要选择 GitLab 凭据（Personal Access Token）")
	}
	return nil
}

// maxBranchNameLen 是分支名的合理上限（git 允许 255，这里留余量）。
const maxBranchNameLen = 200

// containsFold 大小写不敏感地判断切片中是否已存在该值。
func containsFold(list []string, want string) bool {
	for _, item := range list {
		if strings.EqualFold(item, want) {
			return true
		}
	}
	return false
}

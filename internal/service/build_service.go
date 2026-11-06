package service

import (
	"ci-platform/internal/pkg/logger"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"ci-platform/internal/executor"
	"ci-platform/internal/model"
	"ci-platform/internal/pkg"
	"ci-platform/internal/tools"
)

// BuildStore 是 service 依赖的构建记录存储抽象（由 store 包实现）。
type BuildStore interface {
	NextBuildID(ctx context.Context, pipelineID string) (string, error)
	CreateNext(ctx context.Context, b *model.Build) error
	Get(ctx context.Context, pipelineID, buildID string) (*model.Build, error)
	Update(ctx context.Context, b *model.Build) error
	UpdateMetadata(ctx context.Context, pipelineID, buildID string, description *string, keepForever *bool) (*model.Build, error)
	ListByPipeline(ctx context.Context, pipelineID string) ([]*model.Build, error)
	AppendLog(ctx context.Context, pipelineID, buildID string, data []byte) error
	ReadLog(ctx context.Context, pipelineID, buildID string) (string, error)
	LogWriter(ctx context.Context, pipelineID, buildID string) (io.WriteCloser, error)
	DeleteBuild(ctx context.Context, pipelineID, buildID string) error
	BuildDir(pipelineID, buildID string) string
	// CacheDir 返回 task 级缓存目录（cacheKey 已由 service 做过文件名安全化）。
	CacheDir(pipelineID, cacheKey string) string
}

// StepRunner 由 LocalExecutor 实现（在服务器上直接执行步骤）。
type StepRunner interface {
	RunSteps(ctx context.Context, spec executor.RunSpec, out io.Writer) (int, error)
}

// StepResultRunner 是可选能力：LocalExecutor 实现它，测试/自定义 runner 可以只实现 RunSteps。
type StepResultRunner interface {
	RunStepsWithResults(ctx context.Context, spec executor.RunSpec, out io.Writer) ([]model.StepResult, int, error)
}

// GitRunner 由 GitClient 实现。
type GitRunner interface {
	Clone(ctx context.Context, url, branch, dest string, auth *executor.GitAuth, out io.Writer) error
}

// Publisher 由 SshPublisher 实现。
type Publisher interface {
	Publish(ctx context.Context, cfg *model.SshPublishConfig, workspace string, out io.Writer) error
	PublishArtifacts(ctx context.Context, cfg *model.SshPublishConfig, buildDir string, out io.Writer) error
}

// ArtifactReader 提供构建产物清单（由 ArtifactService 实现）。
type ArtifactReader interface {
	Manifest(ctx context.Context, pipelineID, buildID string) (*model.ArtifactManifest, error)
}

// ArtifactCollector 把构建产物归档到 build 目录（由 executor.ArtifactCollector 实现）。
type ArtifactCollector interface {
	// allowEmpty 表示"没匹配到文件"只告警不失败（见 model.ArtifactConfig.AllowEmpty）。
	Collect(ctx context.Context, workspace, buildDir string, patterns []string, limits executor.ArtifactLimits, allowEmpty bool, out io.Writer) ([]model.ArtifactFile, error)
}

// SettingsProvider 提供当前系统配置（由 SettingsService 实现）。
type SettingsProvider interface {
	Current() model.Settings
}

// CredentialResolver 由 CredentialService 实现，供构建阶段解析 Git 凭据。
type CredentialResolver interface {
	Resolve(ctx context.Context, id string) (*model.Credential, error)
}

// SecretCodec 加密构建密码参数（由 CredentialService 实现，与凭据共用密钥）。
type SecretCodec interface {
	SealSecret(plain string) (string, error)
	UnsealSecret(value string) (string, error)
}

// BuildService 编排一次构建的完整生命周期：状态流转 → 拉代码 → 本地执行 → SSH 发布。
// 只调用 store 与 executor 的抽象接口，自身不读写文件、不拼命令。
type BuildService struct {
	pipelines      PipelineStore
	builds         BuildStore
	git            GitRunner
	runner         StepRunner
	publisher      Publisher
	hub            *executor.Hub
	workspace      *WorkspaceManager
	settings       SettingsProvider
	credentials    CredentialResolver
	secrets        SecretCodec
	artifacts      ArtifactCollector
	artifactReader ArtifactReader
	tests          *executor.JUnitCollector
	enqueuer       Enqueuer
	// notifier 负责构建结果通知（项目 × 人员/群 × 事件）；为 nil 时跳过。
	notifier *NotifyService
	// events 负责全局出站事件订阅（build/deploy/approval）；为 nil 时跳过。
	events *EventService
	// queueStats 供首页仪表盘展示队列/worker 状态；为 nil 时相关指标为 0。
	queueStats QueueStats
	// toolInstaller 负责「自动安装」的工具（JDK/Maven/Node…）下载；为 nil 时只用已有主目录。
	toolInstaller *tools.Installer

	// 运行中构建的取消注册表（取消构建用）：key 为 LogKey(pipelineID, buildID)。
	// 构建开始时注册，结束（含取消/panic）时注销。
	cancelMu sync.Mutex
	cancels  map[string]*cancelEntry

	// Idempotency-Key 的短期内存储：只保存「键 → build ID」。
	// 不落盘是有意取舍：客户端重试通常发生在分钟级；进程重启后重新触发
	// 仍可能产生重复构建，但不会把过期键永久写进数据目录。
	idemMu      sync.Mutex
	idemLocks   map[string]*sync.Mutex
	idemEntries map[string]idemEntry
}

// SetToolInstaller 注入工具安装器（自动安装模式的下载/解压）。
func (s *BuildService) SetToolInstaller(installer *tools.Installer) {
	s.toolInstaller = installer
}

// SetSecretCodec 注入密码参数加密实现。未注入时，带密码参数的任务拒绝触发，
// 避免在无法安全落盘的情况下把明文写进 build.json。
func (s *BuildService) SetSecretCodec(codec SecretCodec) {
	s.secrets = codec
}

// cancelEntry 记录一次运行中构建的取消句柄；cancelled 标记用于区分
// “用户主动取消”与“关机/超时导致的取消”，前者落 aborted 状态。
type cancelEntry struct {
	cancel    context.CancelFunc
	cancelled atomic.Bool
}

// wasCancelled 并发安全地读取取消标记。
func (e *cancelEntry) wasCancelled() bool {
	if e == nil {
		return false
	}
	return e.cancelled.Load()
}

// NewBuildService 构造构建业务服务。
func NewBuildService(
	pipelines PipelineStore,
	builds BuildStore,
	git GitRunner,
	runner StepRunner,
	publisher Publisher,
	hub *executor.Hub,
	workspace *WorkspaceManager,
	settings SettingsProvider,
	artifacts ArtifactCollector,
	artifactReader ArtifactReader,
) *BuildService {
	return &BuildService{
		pipelines:      pipelines,
		builds:         builds,
		git:            git,
		runner:         runner,
		publisher:      publisher,
		hub:            hub,
		workspace:      workspace,
		settings:       settings,
		artifacts:      artifacts,
		artifactReader: artifactReader,
		tests:          executor.NewJUnitCollector(),
		cancels:        make(map[string]*cancelEntry),
	}
}

// SetEnqueuer 注入调度器（由 main 在装配阶段调用，打破构造循环）。
func (s *BuildService) SetEnqueuer(e Enqueuer) { s.enqueuer = e }

// SetCredentialResolver 注入凭据解析器（由 main 在装配阶段调用）。
func (s *BuildService) SetCredentialResolver(r CredentialResolver) { s.credentials = r }

// SetNotifier 注入构建结果通知服务（依赖 RBAC 人员联系方式）。
func (s *BuildService) SetNotifier(n *NotifyService) { s.notifier = n }

// SetEvents 注入全局出站事件服务。
func (s *BuildService) SetEvents(events *EventService) { s.events = events }

// Recover 在启动时把上次进程异常退出遗留的 pending/running 构建标记为失败，
// 避免任务永远显示「运行中」。必须在调度器启动前调用。
func (s *BuildService) Recover(ctx context.Context) {
	pipelines, err := s.pipelines.List(ctx)
	if err != nil {
		logger.Infof("recover builds: list pipelines: %v", err)
		return
	}
	recovered := 0
	for _, p := range pipelines {
		builds, listErr := s.builds.ListByPipeline(ctx, p.ID)
		if listErr != nil {
			continue
		}
		for _, b := range builds {
			// 只回收真正执行中的构建：pending_approval 是「等人审批」，重启后应当继续等待，
			// 否则一次重启就会把还没人审批的发布悄悄变成失败。
			if b.Status != model.BuildRunning && b.Status != model.BuildPending {
				continue
			}
			now := time.Now().UTC()
			b.Status = model.BuildFailed
			b.ExitCode = -1
			if b.StartedAt == nil {
				b.StartedAt = &now
			}
			b.FinishedAt = &now
			if updateErr := s.builds.Update(ctx, b); updateErr != nil {
				continue
			}
			_ = s.builds.AppendLog(ctx, p.ID, b.ID,
				[]byte("[ci] build interrupted: server restarted before completion\n"))
			recovered++
		}
	}
	if recovered > 0 {
		logger.Errorf("recovered %d interrupted build(s) -> failed", recovered)
	}
}

// Trigger 创建构建记录并入队，返回 pending 状态的构建。
// TriggerOptions 是触发构建时的可选覆盖项。
type TriggerOptions struct {
	// Params 是参数化构建的用户填值（未传的参数取默认值）。
	Params map[string]string
	// IdempotencyKey 让客户端重试同一个请求时复用第一次创建的构建。
	// 键按「任务 + 触发人」隔离，避免不同用户/任务互相踩键。
	IdempotencyKey string
	// Branch 覆盖任务配置的分支：Webhook 按推送事件的分支构建时使用，
	// 这是「一个任务服务所有分支」的关键（否则推 dev 却构建 main）。
	Branch string
	// CommitProject 是代码平台项目路径（Webhook payload 提供），用于状态回写。
	CommitProject string
}

func (s *BuildService) Trigger(ctx context.Context, pipelineID string, trigger model.TriggerType, triggeredBy string, opts TriggerOptions) (*model.Build, error) {
	// 先处理幂等键：重复请求应返回第一次创建的构建，即使任务随后被禁用，
	// 也不应该让客户端拿到 409 而误以为第一次触发失败。
	idemBuild, releaseIdem, err := s.beginIdempotency(ctx, pipelineID, triggeredBy, opts.IdempotencyKey)
	if err != nil {
		return nil, err
	}
	if idemBuild != nil {
		return idemBuild, nil
	}
	defer releaseIdem()

	p, err := s.pipelines.Get(ctx, pipelineID)
	if err != nil {
		return nil, err
	}
	if p.Disabled {
		return nil, pkg.ErrConflict("任务 %q 已禁用；请先在任务配置中重新启用", p.Name)
	}
	if s.enqueuer == nil {
		return nil, pkg.NewError(pkg.CodeInternal, "scheduler is not initialized")
	}
	// 参数在触发时就解析成最终值并快照进构建记录：
	// 后续有人改了参数默认值，历史构建仍能还原「当时用的什么」。
	resolved, err := ResolveBuildParams(p.Params, opts.Params)
	if err != nil {
		return nil, err
	}
	resolved, secretParams, err := s.protectPasswordParams(p.Params, resolved)
	if err != nil {
		return nil, err
	}
	if branch := strings.TrimSpace(opts.Branch); branch != "" {
		if err := validateTriggerBranch(branch); err != nil {
			return nil, err
		}
		// 只改本次构建使用的分支（p 是从磁盘新读出来的副本，不会写回配置）。
		p.GitBranch = branch
	}

	createdAt := time.Now().UTC()
	b := &model.Build{
		PipelineID:   pipelineID,
		Status:       model.BuildPending,
		TriggerType:  trigger,
		TriggeredBy:  strings.TrimSpace(triggeredBy),
		CreatedAt:    &createdAt,
		Params:       resolved,
		SecretParams: secretParams,
		// 触发时先记下分支：签出后会由 git 现场信息覆盖，但排队/待审批阶段
		// 也需要知道"这次是哪条分支"，否则列表里只看到任务配置的那条。
		Branch:        strings.TrimSpace(p.GitBranch),
		Environment:   strings.TrimSpace(p.Environment),
		CommitProject: strings.TrimSpace(opts.CommitProject),
	}
	// 发布审批：先落 pending_approval 再等放行，绝不能先跑起来再问——生产发布
	// 一旦执行就有副作用，审批必须发生在「执行之前」。
	if p.RequireApproval {
		now := time.Now().UTC()
		b.Status = model.BuildPendingApproval
		b.Approval = &model.BuildApproval{RequestedAt: now, RequestedBy: b.TriggeredBy}
		if err := s.builds.CreateNext(ctx, b); err != nil {
			return nil, err
		}
		_ = s.builds.AppendLog(ctx, pipelineID, b.ID, []byte(
			"[ci] 等待发布审批：本任务开启了发布审批，需要拥有 job.approve 权限的人放行后才开始执行\n"))
		logger.Infof("build %s/%s pending approval (requested by %s)", pipelineID, b.ID, b.TriggeredBy)
		s.dispatchBuildEvent(p, b, model.CIEventApprovalRequested, nil)
		s.rememberIdempotency(pipelineID, triggeredBy, opts.IdempotencyKey, b.ID)
		return b, nil
	}
	if err := s.builds.CreateNext(ctx, b); err != nil {
		return nil, err
	}
	dir := p.WorkspaceDir
	if dir == "" {
		// 迁移尚未完成时退回旧目录，避免与清理器的保护集合不一致。
		dir = p.ID
	}
	if err := s.enqueuer.Enqueue(Job{PipelineID: pipelineID, BuildID: b.ID, WorkspaceDir: dir}); err != nil {
		// 入队失败也要留下可读的终态，否则构建历史里只有一条没有 StartedAt/FinishedAt
		// 的 failed 记录，用户无法判断是队列满还是服务异常。
		now := time.Now().UTC()
		b.Status = model.BuildFailed
		b.ExitCode = -1
		b.FinishedAt = &now
		_ = s.builds.Update(ctx, b)
		_ = s.builds.AppendLog(ctx, pipelineID, b.ID, []byte("[ci] 入队失败: "+err.Error()+"\n"))
		return nil, err
	}
	s.rememberIdempotency(pipelineID, triggeredBy, opts.IdempotencyKey, b.ID)
	return b, nil
}

const (
	idemTTL       = 24 * time.Hour
	idemMaxKeys   = 10000
	maxIdemKeyLen = 128
)

// idemEntry 记录一次成功触发，用于重试时返回同一个构建。
type idemEntry struct {
	buildID string
	at      time.Time
}

// beginIdempotency 锁定给定键并返回已存在的构建（若有）。
// 返回的 release 必须在 Trigger 结束时调用，避免并发同键请求同时创建构建。
func (s *BuildService) beginIdempotency(ctx context.Context, pipelineID, triggeredBy, key string) (*model.Build, func(), error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return nil, func() {}, nil
	}
	if len([]rune(key)) > maxIdemKeyLen {
		return nil, func() {}, pkg.ErrBadRequest("Idempotency-Key 最多 %d 个字符", maxIdemKeyLen)
	}
	full := idemKey(pipelineID, triggeredBy, key)

	s.idemMu.Lock()
	if s.idemLocks == nil {
		s.idemLocks = make(map[string]*sync.Mutex)
	}
	lock := s.idemLocks[full]
	if lock == nil {
		lock = &sync.Mutex{}
		s.idemLocks[full] = lock
	}
	s.idemMu.Unlock()
	lock.Lock()

	s.idemMu.Lock()
	entry, ok := s.idemEntries[full]
	s.idemMu.Unlock()
	if ok && time.Since(entry.at) < idemTTL {
		b, err := s.builds.Get(ctx, pipelineID, entry.buildID)
		if err == nil {
			lock.Unlock()
			return b, func() {}, nil
		}
		// 构建已被清理：删掉旧映射，允许同一个键重新触发。
		s.idemMu.Lock()
		delete(s.idemEntries, full)
		s.idemMu.Unlock()
	}
	return nil, lock.Unlock, nil
}

func idemKey(pipelineID, triggeredBy, key string) string {
	return pipelineID + "\x00" + triggeredBy + "\x00" + strings.TrimSpace(key)
}

func (s *BuildService) rememberIdempotency(pipelineID, triggeredBy, key, buildID string) {
	key = strings.TrimSpace(key)
	if key == "" {
		return
	}
	full := idemKey(pipelineID, triggeredBy, key)
	now := time.Now()
	s.idemMu.Lock()
	defer s.idemMu.Unlock()
	if s.idemEntries == nil {
		s.idemEntries = make(map[string]idemEntry)
	}
	if len(s.idemEntries) >= idemMaxKeys {
		for k, v := range s.idemEntries {
			if now.Sub(v.at) >= idemTTL {
				delete(s.idemEntries, k)
			}
		}
		// 仍然过多时只清最旧的一批；不做 LRU 以避免额外复杂度。
		if len(s.idemEntries) >= idemMaxKeys {
			for k := range s.idemEntries {
				delete(s.idemEntries, k)
				if len(s.idemEntries) < idemMaxKeys/2 {
					break
				}
			}
		}
	}
	s.idemEntries[full] = idemEntry{buildID: buildID, at: now}
}

// protectPasswordParams 把密码参数移入加密字段，并在公开 Params 中打码。
// 返回的 map 是重新分配的新 map，不修改调用方传入的 resolved。
func (s *BuildService) protectPasswordParams(defs []model.ParamDef, values map[string]string) (map[string]string, map[string]string, error) {
	var secrets map[string]string
	for _, def := range defs {
		if def.Type != model.ParamPassword {
			continue
		}
		value := values[def.Name]
		if s.secrets == nil {
			return nil, nil, pkg.NewError(pkg.CodeInternal, "密码参数需要先配置服务器密钥（缺 credentials.key）")
		}
		sealed, err := s.secrets.SealSecret(value)
		if err != nil {
			return nil, nil, fmt.Errorf("加密密码参数 %s: %w", def.Name, err)
		}
		if secrets == nil {
			secrets = make(map[string]string)
		}
		secrets[def.Name] = sealed
	}
	if len(secrets) == 0 {
		return values, nil, nil
	}
	masked := make(map[string]string, len(values))
	for k, v := range values {
		masked[k] = v
	}
	for name := range secrets {
		masked[name] = "***"
	}
	return masked, secrets, nil
}

// PipelineName 返回任务名，供跨任务操作做任务级权限校验。
func (s *BuildService) PipelineName(ctx context.Context, pipelineID string) (string, error) {
	p, err := s.pipelines.Get(ctx, pipelineID)
	if err != nil {
		return "", err
	}
	return p.Name, nil
}

// Rerun 用历史构建的参数快照重新触发（对齐 Jenkins Re-run）。
// 密码参数不能在 API 响应里回传，因此重跑必须由服务端从源构建的加密字段还原。
func (s *BuildService) Rerun(ctx context.Context, pipelineID, buildID, triggeredBy string) (*model.Build, error) {
	src, err := s.builds.Get(ctx, pipelineID, buildID)
	if err != nil {
		return nil, err
	}
	params := make(map[string]string, len(src.Params)+len(src.SecretParams))
	for k, v := range src.Params {
		if _, isSecret := src.SecretParams[k]; !isSecret {
			params[k] = v
		}
	}
	for name, sealed := range src.SecretParams {
		if s.secrets == nil {
			return nil, pkg.NewError(pkg.CodeInternal, "密码参数无法还原：服务未配置密钥")
		}
		plain, err := s.secrets.UnsealSecret(sealed)
		if err != nil {
			return nil, fmt.Errorf("解密参数 %s: %w", name, err)
		}
		params[name] = plain
	}
	return s.Trigger(ctx, pipelineID, model.TriggerManual, triggeredBy, TriggerOptions{
		Params: params,
		Branch: src.Branch,
	})
}

// ListByPipeline 返回某个 pipeline 的构建历史。
func (s *BuildService) ListByPipeline(ctx context.Context, pipelineID string) ([]*model.Build, error) {
	if _, err := s.pipelines.Get(ctx, pipelineID); err != nil {
		return nil, err
	}
	return s.builds.ListByPipeline(ctx, pipelineID)
}

// Get 返回构建详情。
func (s *BuildService) Get(ctx context.Context, pipelineID, buildID string) (*model.Build, error) {
	return s.builds.Get(ctx, pipelineID, buildID)
}

// UpdateMetadata 更新构建页可编辑的元数据：备注与「永久保留」。
// 只改 build.json，不触碰日志与产物；保留策略会在下一轮清理时读取 KeepForever。
func (s *BuildService) UpdateMetadata(ctx context.Context, pipelineID, buildID string, description *string, keepForever *bool) (*model.Build, error) {
	if description != nil {
		trimmed := strings.TrimSpace(*description)
		if len([]rune(trimmed)) > 2000 {
			return nil, pkg.ErrBadRequest("构建描述最多 2000 个字符")
		}
	}
	return s.builds.UpdateMetadata(ctx, pipelineID, buildID, description, keepForever)
}

// Summaries 为任务列表计算聚合视图（上次成功 / 上次失败 / 上次持续时间）。
// 直接读构建存储，不重复校验 pipeline 存在性。
func (s *BuildService) Summaries(ctx context.Context, pipelines []*model.Pipeline) ([]model.PipelineSummary, error) {
	out := make([]model.PipelineSummary, 0, len(pipelines))
	for _, p := range pipelines {
		builds, err := s.builds.ListByPipeline(ctx, p.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, model.Summarize(*p, builds))
	}
	return out, nil
}

// AllBuilds 返回全部任务的构建记录（含任务名），按开始时间倒序。
func (s *BuildService) AllBuilds(ctx context.Context, pipelines []*model.Pipeline) ([]model.BuildListItem, error) {
	out := make([]model.BuildListItem, 0)
	for _, p := range pipelines {
		builds, err := s.builds.ListByPipeline(ctx, p.ID)
		if err != nil {
			return nil, err
		}
		// ListByPipeline 按编号倒序；这里从旧到新算失败 streak 和「上一次完成状态」，
		// 分页后前端仍能拿到每行需要的上下文，不必依赖同页的其他行。
		streak := 0
		var failingSince *time.Time
		var failingSinceBuild string
		var previousFinished model.BuildStatus
		annotated := make([]model.BuildListItem, len(builds))
		for i := len(builds) - 1; i >= 0; i-- {
			b := builds[i]
			item := model.BuildListItem{Build: *b, PipelineName: p.Name, PreviousStatus: previousFinished}
			switch b.Status {
			case model.BuildFailed:
				if streak == 0 {
					failingSince = b.StartedAt
					failingSinceBuild = b.ID
				}
				streak++
			case model.BuildSuccess, model.BuildAborted:
				streak = 0
				failingSince = nil
			}
			if b.Status == model.BuildFailed {
				item.FailureStreak = streak
				item.FailingSinceBuild = failingSinceBuild
				item.FailingSinceAt = failingSince
			}
			annotated[i] = item
			// pending/running 不算「上一次完成」，继续沿用更早的完成状态。
			if b.Status == model.BuildSuccess || b.Status == model.BuildFailed || b.Status == model.BuildAborted {
				previousFinished = b.Status
			}
		}
		out = append(out, annotated...)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return startTimeKey(out[i].StartedAt) > startTimeKey(out[j].StartedAt)
	})
	return out, nil
}

// startTimeKey 把可空时间转为可比较的整数，nil 视为最早。
func startTimeKey(t *time.Time) int64 {
	if t == nil {
		return 0
	}
	return t.UnixNano()
}

// ReadLog 返回完整控制台日志文本。
// 读取时再遮罩一次：旧版本构建的日志可能已经落盘了明文密钥，
// 只靠写入侧遮罩无法保护这些历史文件（界面/下载接口仍会返回原文）。
func (s *BuildService) ReadLog(ctx context.Context, pipelineID, buildID string) (string, error) {
	b, err := s.builds.Get(ctx, pipelineID, buildID)
	if err != nil {
		return "", err
	}
	text, err := s.builds.ReadLog(ctx, pipelineID, buildID)
	if err != nil {
		return "", err
	}
	if s.pipelines == nil || s.settings == nil {
		return text, nil
	}
	p, err := s.pipelines.Get(ctx, pipelineID)
	if err != nil {
		return text, nil
	}
	return pkg.MaskSecrets(text, s.collectMaskSecrets(ctx, p, b, s.currentSettings())), nil
}

// QueryLog 读取日志并按 tail / grep 过滤，供构建页「只看最近 N 行 / 搜索关键字」使用。
// 返回过滤后的文本、原始总行数、命中行数与是否因 tail 截断。
func (s *BuildService) QueryLog(ctx context.Context, pipelineID, buildID string, tail int, grep string) (string, int, int, bool, error) {
	text, err := s.ReadLog(ctx, pipelineID, buildID)
	if err != nil {
		return "", 0, 0, false, err
	}
	lines := strings.Split(text, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	total := len(lines)
	if grep != "" {
		needle := strings.ToLower(grep)
		matched := make([]string, 0, len(lines))
		for _, line := range lines {
			if strings.Contains(strings.ToLower(line), needle) {
				matched = append(matched, line)
			}
		}
		lines = matched
	}
	matchedCount := len(lines)
	truncated := false
	if tail > 0 && len(lines) > tail {
		lines = lines[len(lines)-tail:]
		truncated = true
	}
	return strings.Join(lines, "\n"), total, matchedCount, truncated, nil
}

// Run 由 Scheduler 的 worker 调用，执行一次完整构建。
func (s *BuildService) Run(ctx context.Context, job Job) {
	// 构建内部的意外 panic 不能让 worker 带着 busy=true 退出，否则该任务后续
	// 构建会永远卡在 waiting。这里兜底为失败并释放调度器状态。
	defer func() {
		if rec := recover(); rec != nil {
			logger.Errorf("build panic (pipeline=%s build=%s): %v", job.PipelineID, job.BuildID, rec)
			s.finishWithError(context.WithoutCancel(ctx), job, fmt.Errorf("build panic: %v", rec))
		}
	}()
	p, err := s.pipelines.Get(ctx, job.PipelineID)
	if err != nil {
		s.finishWithError(ctx, job, fmt.Errorf("load pipeline: %w", err))
		return
	}
	if p.WorkspaceDir != "" {
		job.WorkspaceDir = p.WorkspaceDir
	}
	b, err := s.builds.Get(ctx, job.PipelineID, job.BuildID)
	if err != nil {
		return
	}
	// Trigger 时把本次构建的分支快照写进 b.Branch（Webhook 可能是 push 的分支，
	// 而不是任务配置的固定分支）。执行阶段必须用快照覆盖配置，否则“按推送分支构建”
	// 只体现在构建记录里，实际仍会 clone 任务配置的分支。
	if branch := strings.TrimSpace(b.Branch); branch != "" {
		p.GitBranch = branch
	}

	settings := s.currentSettings()

	// pending 构建在排队/静默期被取消：直接跳过执行，保持 aborted 状态。
	if b.Status == model.BuildAborted {
		logger.Infof("build %s/%s skipped (cancelled while pending)", job.PipelineID, job.BuildID)
		return
	}

	// 提前注册取消句柄：静默等待阶段也允许用户取消（取消落 aborted 而非 failed）。
	cancelKey := model.LogKey(job.PipelineID, job.BuildID)
	entry := &cancelEntry{}
	cancelCtx, runCancel := context.WithCancel(ctx)
	entry.cancel = runCancel
	s.cancelMu.Lock()
	s.cancels[cancelKey] = entry
	s.cancelMu.Unlock()
	defer func() {
		runCancel()
		s.cancelMu.Lock()
		delete(s.cancels, cancelKey)
		s.cancelMu.Unlock()
	}()

	// 静默期：在真正开始前等待若干秒，期间保持 pending；用户取消 → aborted。
	if settings.QuietPeriodSec > 0 {
		select {
		case <-cancelCtx.Done():
			if entry.wasCancelled() {
				s.finishAborted(job, b, "cancelled during quiet period")
				return
			}
			// 关机/调度器取消：显式落失败，避免遗留 pending。
			s.finishWithError(ctx, job, fmt.Errorf("cancelled during quiet period: %w", ctx.Err()))
			return
		case <-time.After(time.Duration(settings.QuietPeriodSec) * time.Second):
		}
	}

	startedAt := time.Now().UTC()
	b.Status = model.BuildRunning
	b.StartedAt = &startedAt
	if err = s.builds.Update(ctx, b); err != nil {
		return
	}
	s.dispatchBuildEvent(p, b, model.CIEventBuildStarted, nil)

	// 日志同时写文件与广播到 WebSocket 订阅者；文件侧带大小上限（超限截断，
	// 不影响构建结果），WebSocket 侧不限制（在线查看实时输出）。
	logFile, err := s.builds.LogWriter(ctx, job.PipelineID, job.BuildID)
	if err != nil {
		s.finishWithError(ctx, job, fmt.Errorf("open log file: %w", err))
		return
	}
	defer func() { _ = logFile.Close() }()
	limited := pkg.NewLimitedWriter(logFile,
		int64(s.currentSettings().LogMaxMB)*1024*1024,
		[]byte(fmt.Sprintf("\n[ci] 日志超过 %d MB 上限，后续输出已截断（可在系统配置调整 log_max_mb）\n", s.currentSettings().LogMaxMB)))

	// 构建日志必须先过遮罩再写文件/广播：凭据、Token、密码一旦 echo 出来，
	// 落盘后再擦除已经来不及（历史日志/WebSocket 订阅者都拿到了）。
	masked := pkg.NewMaskingWriter(io.MultiWriter(limited, executor.NewHubWriter(s.hub, model.LogKey(job.PipelineID, job.BuildID))),
		s.collectMaskSecrets(ctx, p, b, s.currentSettings()))
	defer func() { _ = masked.Flush() }()
	out := io.Writer(masked)
	fmt.Fprintf(out, "[ci] build #%s of pipeline %q started (trigger=%s, executor=local)\n",
		job.BuildID, p.Name, b.TriggerType)

	// Global Build Time Out：仅约束执行阶段，状态落盘仍用原始 ctx；
	// 用户取消句柄叠加在最外层，超时与取消同时生效。
	execCtx := cancelCtx
	if settings.GlobalBuildTimeoutMinutes > 0 {
		var timeoutCancel context.CancelFunc
		execCtx, timeoutCancel = context.WithTimeout(cancelCtx, time.Duration(settings.GlobalBuildTimeoutMinutes)*time.Minute)
		defer timeoutCancel()
	}

	exitCode, buildErr := s.executeStages(execCtx, p, b, job, settings, out)

	finishedAt := time.Now().UTC()
	b.FinishedAt = &finishedAt
	b.ExitCode = exitCode
	if buildErr != nil {
		b.Status = model.BuildFailed
		fmt.Fprintf(out, "\n[ci] build #%s FAILED: %v\n", job.BuildID, buildErr)
	} else {
		b.Status = model.BuildSuccess
		fmt.Fprintf(out, "\n[ci] build #%s SUCCESS (exit=%d, duration=%s)\n", job.BuildID, exitCode, finishedAt.Sub(startedAt).Round(time.Millisecond))
	}
	// 用户主动取消：覆盖为 aborted 状态（对齐 Jenkins），与失败区分开。
	if entry.wasCancelled() {
		b.Status = model.BuildAborted
		b.ExitCode = -1
		fmt.Fprintf(out, "\n[ci] build #%s ABORTED (cancelled by user)\n", job.BuildID)
	}
	// 即使进程正在优雅退出（ctx 已取消），也要落盘最终状态，
	// 避免构建永远停留在 running。
	persistCtx := context.WithoutCancel(ctx)
	_ = s.builds.Update(persistCtx, b)

	s.dispatchBuildEvent(p, b, model.CIEventBuildFinished, nil)
	for i := range b.Deployments {
		s.dispatchBuildEvent(p, b, model.CIEventDeployFinished, &b.Deployments[i])
	}
	s.reportCommitStatus(p, b, settings, gitlabStateOf(b.Status), buildStatusSummary(b))
	s.pruneBuilds(persistCtx, p, settings)
	s.dispatchNotify(persistCtx, p, b)
}

// dispatchBuildEvent 构造并异步投递一个全局事件。
// 投递失败只写平台日志，不能影响构建状态或通知链路。
func (s *BuildService) dispatchBuildEvent(p *model.Pipeline, b *model.Build, eventType string, deployment *model.DeploymentRecord) {
	if s.events == nil || p == nil || b == nil {
		return
	}
	event := model.CIEvent{
		Type:       eventType,
		Time:       time.Now().UTC(),
		Pipeline:   p.ID,
		JobName:    p.Name,
		Build:      b.ID,
		Status:     string(b.Status),
		Trigger:    string(b.TriggerType),
		TriggerBy:  b.TriggeredBy,
		Branch:     b.Branch,
		Commit:     b.Commit,
		Env:        b.Environment,
		ExitCode:   b.ExitCode,
		Duration:   b.Duration().Milliseconds(),
		TestReport: b.TestReport,
	}
	switch eventType {
	case model.CIEventApprovalRequested:
		event.Approval = b.Approval
	case model.CIEventDeployFinished:
		event.Deployment = deployment
		if deployment != nil {
			event.Status = deployment.Status
			event.Env = deployment.Environment
		}
	}
	s.events.DispatchAsync(event)
}

// dispatchNotify 发送构建结果通知（项目 × 人员/群 × 事件）。
// 结果摘要写入构建日志，便于事后确认「通知了谁、是否成功」。
func (s *BuildService) dispatchNotify(ctx context.Context, p *model.Pipeline, b *model.Build) {
	if s.notifier == nil {
		return
	}
	prev := s.previousBuild(ctx, p.ID, b.ID)
	res := s.notifier.Notify(ctx, p, b, prev)
	if line := res.Line(); line != "" {
		_ = s.builds.AppendLog(ctx, p.ID, b.ID, []byte("[notify] "+line+"\n"))
	}
}

// previousBuild 返回同任务中紧邻本次的上一次构建（用于判定「修复」）。
func (s *BuildService) previousBuild(ctx context.Context, pipelineID, buildID string) *model.Build {
	list, err := s.builds.ListByPipeline(ctx, pipelineID)
	if err != nil {
		return nil
	}
	for i, item := range list {
		if item.ID == buildID {
			if i+1 < len(list) {
				return list[i+1]
			}
			return nil
		}
	}
	return nil
}

// Cancel 手动取消运行中/等待中的构建：取消后构建落 aborted 状态。
// 对齐 Jenkins“Stop Build”，需要 job.build 权限（由路由中间件校验）。
func (s *BuildService) Cancel(pipelineID, buildID string) error {
	b, err := s.builds.Get(context.Background(), pipelineID, buildID)
	if err != nil {
		return err
	}
	if b.Status != model.BuildRunning && b.Status != model.BuildPending && b.Status != model.BuildPendingApproval {
		return pkg.ErrConflict("build #%s is %s, only running/pending builds can be cancelled", buildID, b.Status)
	}
	cancelKey := model.LogKey(pipelineID, buildID)
	s.cancelMu.Lock()
	entry, ok := s.cancels[cancelKey]
	s.cancelMu.Unlock()
	if !ok {
		// pending / pending_approval 构建尚未开始执行（排队或等审批），没有句柄可取消；
		// 直接落 aborted，worker 执行前会重新检查状态。
		if b.Status == model.BuildPending || b.Status == model.BuildPendingApproval {
			if b.Status == model.BuildPendingApproval {
				// 撤回审批申请：记录为「被发起人取消」，避免事后看起来像是审批超时。
				now := time.Now().UTC()
				if b.Approval == nil {
					b.Approval = &model.BuildApproval{RequestedAt: now}
				}
				b.Approval.DecidedAt = &now
				b.Approval.Approved = false
				b.Approval.Comment = "发起人撤回审批申请"
			}
			b.Status = model.BuildAborted
			b.ExitCode = -1
			finishedAt := time.Now().UTC()
			b.FinishedAt = &finishedAt
			persistCtx := context.WithoutCancel(context.Background())
			if err := s.builds.Update(persistCtx, b); err != nil {
				return err
			}
			logger.Infof("build %s/%s cancelled while pending", pipelineID, buildID)
			return nil
		}
		return pkg.ErrConflict("build #%s is not running", buildID)
	}
	// 标记“用户主动取消”，再关闭 ctx 终止当前步骤。
	s.cancelMu.Lock()
	entry.cancelled.Store(true)
	s.cancelMu.Unlock()
	entry.cancel()
	logger.Infof("build %s/%s cancelled by user", pipelineID, buildID)
	return nil
}

// DeleteBuild 删除某次构建及其目录（build.json、log.txt、artifacts/ 一并移除）。
//
// 为什么不允许删运行中的构建：日志与产物仍在写入，删目录会让状态永远停在
// running，并留下半截日志。需要终止时先「停止构建」（aborted），再删除。
func (s *BuildService) DeleteBuild(ctx context.Context, pipelineID, buildID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := s.pipelines.Get(ctx, pipelineID); err != nil {
		return err
	}
	b, err := s.builds.Get(ctx, pipelineID, buildID)
	if err != nil {
		// 幂等：目标已经不存在（重复点击/并发删除）视为删除成功，
		// 否则前端重试会看到「构建不存在」这种无意义的报错。
		var appErr *pkg.AppError
		if errors.As(err, &appErr) && appErr.Code == pkg.CodeNotFound {
			return nil
		}
		return err
	}
	if b.Status == model.BuildRunning || b.Status == model.BuildPending {
		return pkg.ErrConflict("构建 #%s 正在运行/排队，请先停止再删除", buildID)
	}
	if err = s.builds.DeleteBuild(ctx, pipelineID, buildID); err != nil {
		return err
	}
	// 清理可能残留的取消句柄（正常结束时已删除，这里兜底防泄漏）。
	cancelKey := model.LogKey(pipelineID, buildID)
	s.cancelMu.Lock()
	delete(s.cancels, cancelKey)
	s.cancelMu.Unlock()
	logger.Infof("build %s/%s deleted by user (status=%s)", pipelineID, buildID, b.Status)
	return nil
}

// executeStages 串行执行：Git 拉取 → 本地构建 → SSH 发布。
func (s *BuildService) executeStages(ctx context.Context, p *model.Pipeline, b *model.Build, job Job, settings model.Settings, out io.Writer) (int, error) {
	workspace := s.workspace.Path(job)
	if p.ShouldCleanWorkspace() {
		if err := pkg.RemoveAll(workspace); err != nil {
			return -1, err
		}
	}
	if err := pkg.EnsureDir(workspace); err != nil {
		return -1, err
	}
	absWorkspace, err := filepath.Abs(workspace)
	if err != nil {
		return -1, fmt.Errorf("resolve workspace path: %w", err)
	}

	if p.GitURL != "" {
		auth, authErr := s.resolveGitAuth(ctx, p)
		if authErr != nil {
			return -1, authErr
		}
		if err = s.cloneWithRetry(ctx, p, absWorkspace, settings.SCMRetryCount, auth, out); err != nil {
			return -1, err
		}
		// 记录 SCM 现场（实际分支 + HEAD 提交人）：按分支路由、@ 值班人、
		// 「通知提交者」都依赖它；读取失败不阻塞构建（退化为无分支信息）。
		if info := executor.ReadCheckoutInfo(absWorkspace); info.Branch != "" || info.Commit != "" {
			b.Branch = info.Branch
			b.Commit = info.Commit
			b.CommitFull = info.CommitFull
			b.CommitMessage = info.Message
			b.ChangedFiles = info.Files
			b.CommitAuthor = info.Author
			b.CommitEmail = info.Email
			fmt.Fprintf(out, "[git] checkout %s\n", info.String())
		}
	}

	// 显式缓存：在步骤前恢复，在成功步骤后保存。缓存失败只告警，
	// 不影响构建本身（缓存是优化，不是正确性依赖）。
	if err = s.restoreCache(ctx, p, b, absWorkspace, out); err != nil {
		fmt.Fprintf(out, "[cache] restore skipped: %v\n", err)
	}

	env := map[string]string{
		"CI":               "true",
		"CI_BUILD_ID":      job.BuildID,
		"CI_BUILD_NUMBER":  job.BuildID,
		"CI_PIPELINE_NAME": p.Name,
	}
	// 构建参数 → 环境变量。同时提供原名与 CI_PARAM_<大写名>：
	// 原名对齐 Jenkins（$VERSION），带前缀的别名避免与全局属性重名时被覆盖。
	for name, value := range b.Params {
		env[name] = value
		env[model.ParamDef{Name: name}.EnvName()] = value
	}
	// 密码参数在 Params 里是 ***，真实值来自加密的 SecretParams。
	// 解密失败必须中止构建：继续执行只会拿到 "***" 或空密码，产生误导性的失败。
	for name, sealed := range b.SecretParams {
		if s.secrets == nil {
			return -1, fmt.Errorf("密码参数 %s 无法解密：服务未配置密钥", name)
		}
		plain, decryptErr := s.secrets.UnsealSecret(sealed)
		if decryptErr != nil {
			return -1, fmt.Errorf("密码参数 %s 解密失败: %w", name, decryptErr)
		}
		env[name] = plain
		env[model.ParamDef{Name: name}.EnvName()] = plain
	}
	// 全局属性 → 环境变量；Maven / Git 全局配置同样以环境变量注入。
	for _, kv := range settings.GlobalEnv {
		if kv.Key != "" {
			env[kv.Key] = kv.Value
		}
	}
	if settings.Maven.MavenOpts != "" {
		env["MAVEN_OPTS"] = settings.Maven.MavenOpts
	}
	// Local Maven Repository 需以 -Dmaven.repo.local 传给 Maven，
	// 单独设置 MAVEN_REPO_LOCAL 环境变量对 Maven 无效。
	if settings.Maven.LocalRepo != "" {
		opts := strings.TrimSpace(env["MAVEN_OPTS"] + " -Dmaven.repo.local=" + settings.Maven.LocalRepo)
		env["MAVEN_OPTS"] = opts
	}
	if settings.GitUserName != "" {
		env["GIT_AUTHOR_NAME"] = settings.GitUserName
		env["GIT_COMMITTER_NAME"] = settings.GitUserName
	}
	if settings.GitUserEmail != "" {
		env["GIT_AUTHOR_EMAIL"] = settings.GitUserEmail
		env["GIT_COMMITTER_EMAIL"] = settings.GitUserEmail
	}

	// 回写代码平台的 commit status（GitLab）：让 MR 里能看到"构建中"。
	// 异步执行且失败只写日志——状态回写失败绝不能影响构建本身。
	s.reportCommitStatus(p, b, settings, "running", "构建中")

	// 本地执行：把全局工具配置里填写的主目录注入 PATH/GOROOT/JAVA_HOME 等。
	// 自动安装的工具先确保已下载（首次会在构建日志里看到下载进度）。
	settings = s.materializeTools(ctx, p, settings, out)
	injectLocalToolHomes(settings, env)

	spec := executor.RunSpec{
		Steps:     p.Steps,
		Workspace: absWorkspace,
		Env:       env,
		Shell:     settings.Shell,
	}
	var code int
	if runner, ok := s.runner.(StepResultRunner); ok {
		b.StepResults, code, err = runner.RunStepsWithResults(ctx, spec, out)
	} else {
		code, err = s.runner.RunSteps(ctx, spec, out)
	}
	if err != nil {
		return code, err
	}
	// 测试报告在产物归档前解析：即使测试命令成功返回，JUnit 里的 failure/error
	// 也应把构建标记为失败（对齐 Jenkins Publish JUnit test result report）。
	if p.TestReport.Enabled {
		if s.tests == nil {
			return 1, fmt.Errorf("junit collector is not initialized")
		}
		report, testErr := s.tests.Collect(ctx, absWorkspace, s.builds.BuildDir(job.PipelineID, job.BuildID), p.TestReport, out)
		if testErr != nil {
			fmt.Fprintf(out, "[junit] FAILED: %v\n", testErr)
			if code == 0 {
				code = 1
			}
		} else {
			b.TestReport = report
			if report != nil && (report.Failed > 0 || report.Errors > 0) {
				fmt.Fprintf(out, "[junit] %d failed / %d error test(s)，构建标记为失败\n", report.Failed, report.Errors)
				if code == 0 {
					code = 1
				}
			}
		}
	}
	if code != 0 {
		return code, fmt.Errorf("build steps failed with exit code %d", code)
	}
	if err = s.saveCache(ctx, p, b, absWorkspace, out); err != nil {
		fmt.Fprintf(out, "[cache] save skipped: %v\n", err)
	}

	// 先归档本次编译产物，再发布。这样即使 SSH 发布失败，
	// 本次构建的产物也已保存在 jobs/{pid}/builds/{bid}/artifacts/ 下，
	// 可以随时回滚到该版本。
	if p.Artifacts.Enabled {
		if s.artifacts == nil {
			return 1, fmt.Errorf("artifact collector is not initialized")
		}
		limits := executor.ArtifactLimits{
			MaxFiles:      settings.ArtifactMaxFiles,
			MaxTotalBytes: int64(settings.ArtifactMaxTotalMB) * 1024 * 1024,
		}
		if _, err = s.artifacts.Collect(ctx, absWorkspace, s.builds.BuildDir(job.PipelineID, job.BuildID),
			p.Artifacts.Files, limits, p.Artifacts.AllowEmpty, out); err != nil {
			// 模式没匹配到是最常见的失败：把"当前模式 / 建议模式 / 找到的文件"
			// 记进构建记录，前端直接给「一键改成建议模式」。
			var noMatch *executor.NoMatchError
			if errors.As(err, &noMatch) {
				b.Advice = &model.BuildAdvice{
					Kind:      "artifact_pattern",
					Message:   noMatch.Error(),
					Patterns:  noMatch.Patterns,
					Suggested: noMatch.Suggested,
					Files:     noMatch.Files,
				}
			}
			return 1, fmt.Errorf("归档产物失败：%w", err)
		}
	}

	// SSH 发布：支持多个目标（不同服务器/不同目录）。
	// 单个目标失败不打断其余目标——一次构建把能发的都发出去，
	// 否则「修好第一个才发现第二个也坏」会白白多跑一轮构建。
	targets := p.EffectiveSshPublishes()
	if len(targets) > 0 {
		failures := make([]string, 0, len(targets))
		fmt.Fprintf(out, "[ssh] %d publish target(s) configured\n", len(targets))
		for i := range targets {
			target := targets[i]
			sshCfg, resolveErr := resolveSshConfig(&target, settings)
			if resolveErr != nil {
				failures = append(failures, fmt.Sprintf("%s: %v", target.Name, resolveErr))
				fmt.Fprintf(out, "[ssh] target %q resolve FAILED: %v\n", target.Name, resolveErr)
				continue
			}
			fmt.Fprintf(out, "[ssh] target %q -> server %q (remote=%s)\n",
				sshCfg.Name, sshCfg.ServerID, sshCfg.RemoteBaseDir)
			if err = s.publisher.Publish(ctx, sshCfg, absWorkspace, out); err != nil {
				failures = append(failures, fmt.Sprintf("%s: %v", sshCfg.Name, err))
				fmt.Fprintf(out, "[ssh] target %q FAILED: %v\n", sshCfg.Name, err)
				b.Deployments = append(b.Deployments, model.DeploymentRecord{
					Target: sshCfg.Name, ServerID: sshCfg.ServerID, Environment: b.Environment,
					Status: "failed", DeployedAt: time.Now().UTC(), DeployedBy: b.TriggeredBy, Message: err.Error(),
				})
				continue
			}
			b.Deployments = append(b.Deployments, model.DeploymentRecord{
				Target: sshCfg.Name, ServerID: sshCfg.ServerID, Environment: b.Environment,
				Status: "success", DeployedAt: time.Now().UTC(), DeployedBy: b.TriggeredBy,
			})
			fmt.Fprintf(out, "[ssh] target %q done\n", sshCfg.Name)
		}
		if len(failures) > 0 {
			return 1, fmt.Errorf("ssh publish failed (%d/%d targets): %s",
				len(failures), len(targets), strings.Join(failures, "; "))
		}
	}
	return 0, nil
}

// materializeTools 把「自动安装」的工具准备到本地，并检查手动填写的主目录是否存在。
//
// 这一步放在构建开始时（而不是保存配置时）：下载可能几十上百 MB，
// 不该让保存设置这种轻操作卡住；构建日志里能看到进度，失败也能定位到具体工具。
func (s *BuildService) materializeTools(ctx context.Context, p *model.Pipeline, settings model.Settings, out io.Writer) model.Settings {
	if len(settings.ToolCategories) == 0 {
		return settings
	}
	// 1) 先应用任务级的版本选择（把选中的安装项提到该类别第一位）
	effective := applyToolSelections(settings, p)

	// 2) 只准备「这个任务真的会用到」的工具：
	//    任务里显式选了的类别 + 步骤脚本里出现对应命令的类别。
	//    否则一个只跑 mvn 的任务会连带把 JDK/Node/Go/Gradle 全下载一遍（几百 MB）。
	needed := neededToolCategories(effective, p)

	updated := effective
	updated.ToolCategories = make([]model.ToolCategory, len(effective.ToolCategories))
	copy(updated.ToolCategories, effective.ToolCategories)

	for i := range updated.ToolCategories {
		category := &updated.ToolCategories[i]
		category.Installations = make([]model.ToolInstallation, len(effective.ToolCategories[i].Installations))
		copy(category.Installations, effective.ToolCategories[i].Installations)
		used := needed[category.ID]
		for j := range category.Installations {
			item := &category.Installations[j]
			if item.IsAutoInstall() && used {
				if s.toolInstaller == nil {
					fmt.Fprintf(out, "[tools] %s %s 配置为自动安装，但当前实例未启用安装器\n", category.Title, item.Version)
					continue
				}
				home, err := s.toolInstaller.Ensure(ctx, category.ID, item.Version)
				if err != nil {
					// 下载失败不阻断构建：工具本来就可能用不到，
					// 但要在日志里说清楚，避免后面 command not found 无从排查。
					fmt.Fprintf(out, "[tools] 自动安装 %s %s 失败：%v\n", category.Title, item.Version, err)
					continue
				}
				item.Home = home
				fmt.Fprintf(out, "[tools] %s %s → %s\n", category.Title, item.Version, home)
				continue
			}
			if item.Home != "" && used {
				if info, err := os.Stat(item.Home); err != nil || !info.IsDir() {
					fmt.Fprintf(out, "[tools] %s 的主目录不存在：%s（请检查「系统管理 → 全局工具配置」）\n", category.Title, item.Home)
				}
			}
		}
		// 没用到的类别不注入任何路径：避免"没用到也把 PATH 顶掉"。
		if !used {
			for j := range category.Installations {
				category.Installations[j].Home = ""
			}
		}
	}

	// 步骤里用到的命令 vs 没配置的工具：提前提示，别等 command not found。
	for _, hint := range missingToolHints(updated, p.Steps) {
		fmt.Fprintf(out, "[tools] %s\n", hint)
	}
	return updated
}

// applyToolSelections 把任务选中的安装项提到所属类别第一位。
// 后续注入逻辑统一"取每类第一个可用项"，因此调整顺序即可让选择生效，
// 不必在多处判断"这个类别有没有被任务指定"。
func applyToolSelections(settings model.Settings, p *model.Pipeline) model.Settings {
	if p == nil || len(p.ToolSelections) == 0 {
		return settings
	}
	out := settings
	out.ToolCategories = make([]model.ToolCategory, len(settings.ToolCategories))
	for i, category := range settings.ToolCategories {
		selected, ok := p.ToolSelections[category.ID]
		copied := model.ToolCategory{ID: category.ID, Title: category.Title, Hint: category.Hint}
		copied.Installations = make([]model.ToolInstallation, len(category.Installations))
		copy(copied.Installations, category.Installations)
		if ok && selected != "" {
			for j, item := range copied.Installations {
				if item.Name != selected {
					continue
				}
				picked := copied.Installations[j]
				copied.Installations = append([]model.ToolInstallation{picked}, append(copied.Installations[:j:j], copied.Installations[j+1:]...)...)
				break
			}
		}
		out.ToolCategories[i] = copied
	}
	return out
}

// neededToolCategories 返回本任务需要准备好的工具类别集合。
func neededToolCategories(settings model.Settings, p *model.Pipeline) map[string]bool {
	needed := map[string]bool{}
	if p != nil {
		for categoryID, name := range p.ToolSelections {
			if strings.TrimSpace(name) != "" {
				needed[categoryID] = true
			}
		}
	}
	if p != nil && len(p.Steps) > 0 {
		var script strings.Builder
		for _, step := range p.Steps {
			script.WriteString(step.Run)
			script.WriteString("\n")
		}
		body := script.String()
		for _, mapping := range toolCommandCategories {
			for _, cmd := range mapping.commands {
				if containsCommand(body, cmd) {
					needed[mapping.categoryID] = true
					break
				}
			}
		}
	}
	return needed
}

// toolCommandCategories 把常见命令映射到工具类别，用于「用了但没配」的提示。
var toolCommandCategories = []struct {
	categoryID string
	commands   []string
}{
	{"jdk", []string{"java", "javac", "keytool"}},
	{"maven", []string{"mvn"}},
	{"gradle", []string{"gradle"}},
	{"ant", []string{"ant"}},
	{"nodejs", []string{"node", "npm", "npx", "yarn", "pnpm"}},
	{"golang", []string{"go", "gofmt"}},
}

// missingToolHints 返回「步骤里用到某个命令，但对应工具没配置主目录」的提示列表。
func missingToolHints(settings model.Settings, steps []model.Step) []string {
	configured := map[string]bool{}
	for _, category := range settings.ToolCategories {
		for _, item := range category.Installations {
			if strings.TrimSpace(item.Home) != "" {
				configured[category.ID] = true
				break
			}
		}
	}
	// 把步骤脚本拼起来做关键字判断：比精确解析 shell 简单，且只用于提示。
	var script strings.Builder
	for _, step := range steps {
		script.WriteString(step.Run)
		script.WriteString("\n")
	}
	body := script.String()
	var hints []string
	for _, mapping := range toolCommandCategories {
		if configured[mapping.categoryID] {
			continue
		}
		for _, cmd := range mapping.commands {
			if containsCommand(body, cmd) {
				hints = append(hints, fmt.Sprintf(
					"步骤里用到 %q，但「全局工具配置 → %s」没有可用的主目录（可能会 command not found）；可在那里填写服务器路径或选择自动安装",
					cmd, toolCategoryTitle(settings, mapping.categoryID)))
				break
			}
		}
	}
	return hints
}

// containsCommand 判断脚本里是否**以命令的形式**调用了某个可执行文件。
//
// 只认"命令的第一个词"：前面要么是行首/分隔符（; && || | ( ），
// 要么是 sudo/env/nohup 这类包装命令。这样 `echo jar > app.jar` 这种
// 把命令名当参数的写法不会被误判成"用到了 jar 工具"——误判会让构建
// 白白去下载一个 190MB 的 JDK。
func containsCommand(script, cmd string) bool {
	idx := 0
	for {
		pos := strings.Index(script[idx:], cmd)
		if pos < 0 {
			return false
		}
		start := idx + pos
		end := start + len(cmd)
		idx = end
		if start > 0 && !isCommandBoundary(script[start-1]) {
			continue
		}
		if end < len(script) && !isCommandBoundary(script[end]) {
			continue
		}
		if isCommandStart(script, start) {
			return true
		}
	}
}

// commandWrappers 是可以出现在真正命令前面的包装命令。
var commandWrappers = map[string]bool{
	"sudo": true, "env": true, "nohup": true, "time": true,
	"exec": true, "command": true, "builtin": true, "xargs": true,
}

// isCommandStart 判断 cmd 在 start 位置是否是"命令的第一个词"。
// 做法：往前跳过空白，看上一个是行首/分隔符，还是包装命令。
func isCommandStart(script string, start int) bool {
	i := start - 1
	for i >= 0 {
		ch := script[i]
		if ch == ' ' || ch == '\t' {
			i--
			continue
		}
		if ch == '\n' || ch == '\r' || ch == ';' || ch == '&' || ch == '|' || ch == '(' || ch == '`' {
			return true
		}
		if ch == '=' {
			// FOO=npm 这种变量赋值不是执行
			return false
		}
		// 前面还有词：只有包装命令才算命令起点
		wordEnd := i + 1
		for i >= 0 && !isCommandBoundary(script[i]) {
			i--
		}
		word := script[i+1 : wordEnd]
		return commandWrappers[word]
	}
	return true
}

func isCommandBoundary(ch byte) bool {
	switch ch {
	case ' ', '\t', '\n', '\r', ';', '&', '|', '(', ')', '`', '$', '"', '\'', ':', '=', '/', '\\':
		return true
	}
	return false
}

// toolCategoryTitle 返回类别标题，未知类别回退 ID。
func toolCategoryTitle(settings model.Settings, categoryID string) string {
	for _, category := range settings.ToolCategories {
		if category.ID == categoryID {
			return category.Title
		}
	}
	return categoryID
}

// toolHomeEnvKey 返回工具类别对应的“主目录”环境变量名。
func toolHomeEnvKey(categoryID string) string {
	switch strings.ToLower(categoryID) {
	case "jdk":
		return "JAVA_HOME"
	case "maven":
		return "MAVEN_HOME"
	case "gradle":
		return "GRADLE_HOME"
	case "ant":
		return "ANT_HOME"
	case "golang":
		return "GOROOT"
	case "nodejs":
		return "NODE_HOME"
	}
	return ""
}

// injectLocalToolHomes 把全局工具配置中每个类别第一个填写了 home 的安装项
// 注入为环境变量（JAVA_HOME/GOROOT/...），并把其可执行目录前置到 PATH。
// 本地执行器据此使用服务器上指定路径的工具（例如指定 GOROOT 后直接用该 Go 编译）。
//
// Windows / Linux 差异集中在这里处理：
//   - PATH 变量名在 Windows 上通常是 Path，写死 "PATH" 会让某些子进程读不到；
//   - Windows 版 Git 的 git.exe 在 cmd/ 下（不是 bin/），Node 的 npm.cmd 在主目录本身；
//   - Windows 上执行 .cmd/.bat 需要额外把主目录放进 PATH。
func injectLocalToolHomes(settings model.Settings, env map[string]string) {
	var bins []string
	for _, category := range settings.ToolCategories {
		for _, item := range category.Installations {
			home := strings.TrimSpace(item.Home)
			if home == "" {
				continue
			}
			home = filepath.FromSlash(home)
			switch category.ID {
			case "git":
				env["GIT_EXECUTABLE"] = gitExecutable(home)
				bins = append(bins, toolBinDirs("git", home)...)
			default:
				if key := toolHomeEnvKey(category.ID); key != "" {
					env[key] = home
				}
				bins = append(bins, toolBinDirs(category.ID, home)...)
			}
			break // 每个类别取第一个配置了主目录的安装项
		}
	}
	if len(bins) == 0 {
		return
	}
	base := envPath(env)
	setEnvPath(env, strings.Join(bins, string(os.PathListSeparator))+string(os.PathListSeparator)+base)
}

// toolBinDirs 返回某个工具主目录下需要加入 PATH 的目录（按优先级）。
func toolBinDirs(categoryID, home string) []string {
	switch categoryID {
	case "git":
		// Git for Windows: git.exe 在 cmd/，bash 工具在 bin/；Linux 只有 bin/。
		if runtime.GOOS == "windows" {
			return []string{filepath.Join(home, "cmd"), filepath.Join(home, "bin"), home}
		}
		return []string{filepath.Join(home, "bin"), home}
	case "nodejs":
		// node/npm(.cmd) 直接位于 Node 主目录，部分发行版放在 bin/。
		return []string{home, filepath.Join(home, "bin")}
	default:
		// JDK / Maven / Gradle / Ant / Go：可执行文件都在 bin/ 下。
		return []string{filepath.Join(home, "bin"), home}
	}
}

// gitExecutable 找到 git 可执行文件的实际位置（Windows 在 cmd/，Linux 在 bin/）。
func gitExecutable(home string) string {
	candidates := []string{filepath.Join(home, "bin", "git"), filepath.Join(home, "cmd", "git"), filepath.Join(home, "git")}
	if runtime.GOOS == "windows" {
		for i := range candidates {
			candidates[i] += ".exe"
		}
	}
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
	}
	return candidates[0] // 都不存在时给出常规位置，让日志里的路径可读
}

// envPath 读取 PATH（Windows 上键名可能写作 Path）。
func envPath(env map[string]string) string {
	for key, value := range env {
		if strings.EqualFold(key, "PATH") && value != "" {
			return value
		}
	}
	return os.Getenv("PATH")
}

// setEnvPath 写入 PATH 并清理大小写重复的键
// （同时存在 PATH 与 Path 时，Windows 子进程取哪个是未定义的）。
func setEnvPath(env map[string]string, value string) {
	for key := range env {
		if strings.EqualFold(key, "PATH") {
			delete(env, key)
		}
	}
	if runtime.GOOS == "windows" {
		env["Path"] = value
		return
	}
	env["PATH"] = value
}

// resolveSshConfig 把任务引用的全局 SSH 服务器与任务级发布参数合并成完整配置。
// 旧版任务（ServerID 为空）继续使用任务内联的主机/账号配置，保证兼容。
func resolveSshConfig(spec *model.SshPublishConfig, settings model.Settings) (*model.SshPublishConfig, error) {
	if spec == nil {
		return nil, nil
	}
	if strings.TrimSpace(spec.ServerID) == "" {
		return spec, nil
	}
	for _, server := range settings.SshServers {
		if server.ID != spec.ServerID {
			continue
		}
		effective := *spec
		effective.Host = server.Host
		effective.Port = server.Port
		effective.Username = server.Username
		effective.KnownHostsPath = server.KnownHostsPath
		effective.AuthType = server.AuthType
		effective.PrivateKeyPath = server.PrivateKeyPath
		effective.Password = server.Password
		if strings.TrimSpace(effective.RemoteBaseDir) == "" {
			effective.RemoteBaseDir = server.RemoteBaseDir
		} else if strings.TrimSpace(server.RemoteBaseDir) != "" {
			// 对齐 Jenkins Publish Over SSH：任务级「远程基础目录」相对服务器的
			// 默认远程目录拼接（/data/projects + gomysqldump → /data/projects/gomysqldump），
			// 而不是整体替换。
			base := strings.TrimRight(strings.TrimSpace(server.RemoteBaseDir), "/")
			effective.RemoteBaseDir = base + "/" + strings.TrimLeft(strings.TrimSpace(effective.RemoteBaseDir), "/")
		}
		return &effective, nil
	}
	return nil, fmt.Errorf("SSH 服务器 %q 不存在（可能已在系统配置中删除）", spec.ServerID)
}

// currentSettings 返回当前系统配置，未注入时给出最小可用默认。
func (s *BuildService) currentSettings() model.Settings {
	if s.settings == nil {
		return model.Settings{Shell: model.DefaultShell()}
	}
	return s.settings.Current()
}

// cloneWithRetry 按 SCM 签出重试次数执行克隆。
func (s *BuildService) cloneWithRetry(ctx context.Context, p *model.Pipeline, dest string, retries int, auth *executor.GitAuth, out io.Writer) error {
	var err error
	for attempt := 0; attempt <= retries; attempt++ {
		if attempt > 0 {
			fmt.Fprintf(out, "[git] retry clone (%d/%d)...\n", attempt, retries)
		}
		err = s.git.Clone(ctx, p.GitURL, p.EffectiveBranch(), dest, auth, out)
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return err
		}
	}
	return err
}

// resolveGitAuth 根据任务引用的凭据解析 HTTPS 认证信息；失败或类型不支持时回退匿名。
func (s *BuildService) resolveGitAuth(ctx context.Context, p *model.Pipeline) (*executor.GitAuth, error) {
	if p.GitCredential == "" {
		return nil, nil
	}
	if s.credentials == nil {
		return nil, fmt.Errorf("git: 凭据服务未初始化，无法解析凭据 %s", p.GitCredential)
	}
	c, err := s.credentials.Resolve(ctx, p.GitCredential)
	if err != nil {
		return nil, fmt.Errorf("git: 凭据 %s 不存在或不可读: %w", p.GitCredential, err)
	}
	if err := ensureCredentialHost(c, p.GitURL); err != nil {
		return nil, err
	}
	switch c.Type {
	case model.CredentialUsernamePassword:
		return &executor.GitAuth{Username: c.Username, Password: c.Secret}, nil
	case model.CredentialSecretText:
		username := c.Username
		if username == "" {
			username = "oauth2"
		}
		return &executor.GitAuth{Username: username, Password: c.Secret}, nil
	default:
		return nil, fmt.Errorf("git: 凭据 %q 类型 %s 不支持 HTTPS 拉取", c.Name, c.Type)
	}
}

// ensureCredentialHost 校验凭据是否允许用于本次 Git URL。
// 旧凭据的 AllowedHosts 为空时保持兼容（只告警），新凭据建议在凭据管理里配置。
func ensureCredentialHost(c *model.Credential, rawURL string) error {
	if c == nil || len(c.AllowedHosts) == 0 {
		if c != nil {
			logger.Warnf("git credential %q has no allowed_hosts; consider scoping it to a host", c.Name)
		}
		return nil
	}
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || u.Hostname() == "" {
		return pkg.ErrBadRequest("Git URL 格式不正确，无法校验凭据主机")
	}
	host := strings.ToLower(u.Hostname())
	for _, allowed := range c.AllowedHosts {
		allowed = strings.ToLower(strings.TrimSpace(allowed))
		if allowed == host {
			return nil
		}
		if strings.HasPrefix(allowed, "*.") && strings.HasSuffix(host, strings.TrimPrefix(allowed, "*")) {
			return nil
		}
	}
	return pkg.ErrForbidden("凭据 %q 不允许用于 Git 主机 %q", c.Name, host)
}

// Republish 把某次成功构建已归档的产物直接重新发布到 SSH 服务器（不重新编译），
// 用于「发布失败」或「需要回滚到历史版本」的场景。日志写入该构建的 log.txt。
func (s *BuildService) Republish(ctx context.Context, pipelineID, buildID, serverID, publishName string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	p, err := s.pipelines.Get(ctx, pipelineID)
	if err != nil {
		return err
	}
	b, err := s.builds.Get(ctx, pipelineID, buildID)
	if err != nil {
		return err
	}
	if b.Status != model.BuildSuccess {
		return pkg.ErrBadRequest("只能重新发布构建成功的产物（当前状态 %s）", b.Status)
	}
	if s.artifactReader == nil {
		return fmt.Errorf("artifact service is not initialized")
	}
	manifest, err := s.artifactReader.Manifest(ctx, pipelineID, buildID)
	if err != nil {
		return err
	}
	if len(manifest.Files) == 0 {
		return pkg.ErrNotFound("该构建没有归档产物，无法重新发布")
	}
	targets := p.EffectiveSshPublishes()
	if len(targets) == 0 {
		return pkg.ErrBadRequest("任务未配置 SSH 发布（或所有目标都未启用）")
	}
	// 选择发布目标：按名称精确匹配（忽略大小写）；未指定时用第一个启用的目标。
	spec := targets[0]
	publishName = strings.TrimSpace(publishName)
	if publishName != "" {
		found := false
		for _, t := range targets {
			if strings.EqualFold(t.Name, publishName) {
				spec = t
				found = true
				break
			}
		}
		if !found {
			return pkg.ErrBadRequest("发布目标 %q 不存在或未启用", publishName)
		}
	}
	spec.Enabled = true
	if serverID = strings.TrimSpace(serverID); serverID != "" {
		// 只允许覆盖成任务已经配置过的服务器：job.replay 是较低权限，
		// 不能让它在任意全局 SSH 服务器上部署产物。
		allowed := false
		for _, t := range targets {
			if strings.EqualFold(strings.TrimSpace(t.ServerID), serverID) {
				allowed = true
				break
			}
		}
		if !allowed {
			return pkg.ErrForbidden("重新发布只能使用任务已配置的 SSH 服务器 %q", serverID)
		}
		spec.ServerID = serverID
	}
	if spec.ServerID == "" {
		return pkg.ErrBadRequest("未指定 SSH 服务器（任务或请求都需要提供 server_id）")
	}
	cfg, err := resolveSshConfig(&spec, s.currentSettings())
	if err != nil {
		return err
	}

	logFile, err := s.builds.LogWriter(ctx, pipelineID, buildID)
	if err != nil {
		return fmt.Errorf("open build log: %w", err)
	}
	defer func() { _ = logFile.Close() }()
	masked := pkg.NewMaskingWriter(io.MultiWriter(logFile, executor.NewHubWriter(s.hub, model.LogKey(pipelineID, buildID))),
		s.collectMaskSecrets(ctx, p, b, s.currentSettings()))
	defer func() { _ = masked.Flush() }()
	out := io.Writer(masked)

	fmt.Fprintf(out, "\n[republish] start: build #%s -> server %q\n", buildID, cfg.ServerID)
	envName := b.Environment
	if envName == "" {
		envName = p.Environment
	}
	if err = s.publisher.PublishArtifacts(ctx, cfg, s.builds.BuildDir(pipelineID, buildID), out); err != nil {
		fmt.Fprintf(out, "\n[republish] FAILED: %v\n", err)
		b.Deployments = append(b.Deployments, model.DeploymentRecord{
			Target: cfg.Name, ServerID: cfg.ServerID, Environment: envName,
			Status: "failed", DeployedAt: time.Now().UTC(), DeployedBy: pkg.UserFromContext(ctx), Message: err.Error(),
		})
		_ = s.builds.Update(context.WithoutCancel(ctx), b)
		s.dispatchBuildEvent(p, b, model.CIEventDeployFinished, &b.Deployments[len(b.Deployments)-1])
		return err
	}
	b.Deployments = append(b.Deployments, model.DeploymentRecord{
		Target: cfg.Name, ServerID: cfg.ServerID, Environment: envName,
		Status: "success", DeployedAt: time.Now().UTC(), DeployedBy: pkg.UserFromContext(ctx),
		ArtifactCount: len(manifest.Files), Message: "republish",
	})
	_ = s.builds.Update(context.WithoutCancel(ctx), b)
	fmt.Fprintf(out, "\n[republish] finished\n")
	return nil
}

// collectMaskSecrets 汇总本次构建可能被步骤 echo 出来的敏感值。
// 只遮「平台自己知道」的密钥；步骤里临时生成/外部注入的密钥无法识别。
func (s *BuildService) collectMaskSecrets(ctx context.Context, p *model.Pipeline, b *model.Build, settings model.Settings) []string {
	seen := make(map[string]struct{})
	add := func(value string) {
		value = strings.TrimSpace(value)
		if len(value) < 4 {
			return
		}
		seen[value] = struct{}{}
	}

	for _, item := range settings.GlobalEnv {
		// Secret 显式标记优先于名称启发式：用户可以把任意变量名标成密钥。
		if item.Secret || isSecretEnvName(item.Key) {
			add(item.Value)
		}
	}
	if b != nil {
		for name, value := range b.Params {
			if isSecretEnvName(name) {
				add(value)
			}
		}
		// 密码参数的真实值不在 Params 里，需要解密后再加入遮罩列表。
		// SecretParams 本身已经限定了密码参数，不能再按名称过滤：
		// 参数名叫 FOO 但类型是 password 时也必须遮罩。
		if s.secrets != nil {
			for _, sealed := range b.SecretParams {
				if plain, err := s.secrets.UnsealSecret(sealed); err == nil {
					add(plain)
				}
			}
		}
	}
	if p.GitCredential != "" && s.credentials != nil {
		if c, err := s.credentials.Resolve(ctx, p.GitCredential); err == nil {
			add(c.Secret)
		}
	}
	if p.CommitStatus.Enabled && p.CommitStatus.CredentialID != "" && s.credentials != nil {
		if c, err := s.credentials.Resolve(ctx, p.CommitStatus.CredentialID); err == nil {
			add(c.Secret)
		}
	}
	add(settings.Email.Password)
	add(settings.WebhookNotify.URL)
	add(p.WebhookSecret)
	if p.Notify != nil {
		for _, target := range p.Notify.Targets {
			add(target.URL)
		}
		if p.Notify.Escalation != nil {
			for _, target := range p.Notify.Escalation.Targets {
				add(target.URL)
			}
		}
		for _, route := range p.Notify.Routes {
			for _, target := range route.Targets {
				add(target.URL)
			}
		}
	}

	for _, publish := range p.EffectiveSshPublishes() {
		add(publish.Password)
		if publish.ServerID == "" {
			continue
		}
		for _, server := range settings.SshServers {
			if server.ID == publish.ServerID {
				add(server.Password)
			}
		}
	}

	out := make([]string, 0, len(seen))
	for value := range seen {
		out = append(out, value)
	}
	return out
}

// isSecretEnvName 保留旧调用名，规则统一由 model 提供。
func isSecretEnvName(name string) bool {
	return model.IsSecretEnvName(name)
}

// pruneBuilds 应用构建保留策略：任务级配置优先，否则回落到全局 Build Discarders。
func (s *BuildService) pruneBuilds(ctx context.Context, p *model.Pipeline, settings model.Settings) {
	maxPerJob := p.BuildRetentionMaxPerJob
	maxAgeDays := p.BuildRetentionMaxAgeDays
	if maxPerJob <= 0 {
		maxPerJob = settings.BuildRetentionMaxPerJob
	}
	if maxAgeDays <= 0 {
		maxAgeDays = settings.BuildRetentionMaxAgeDays
	}
	if maxPerJob <= 0 && maxAgeDays <= 0 {
		return
	}
	builds, err := s.builds.ListByPipeline(ctx, p.ID)
	if err != nil {
		return
	}
	now := time.Now()
	maxAge := time.Duration(maxAgeDays) * 24 * time.Hour
	for i, b := range builds {
		// 排队/运行/待审批的构建不能删：删目录会让状态永远停在活动状态，
		// 审批人随后点开也只会拿到 404。
		if b.Status == model.BuildRunning || b.Status == model.BuildPending || b.Status == model.BuildPendingApproval {
			continue
		}
		// Jenkins 的 Keep this build forever：用户显式保留的记录不参与保留策略。
		if b.KeepForever {
			continue
		}
		remove := false
		if maxPerJob > 0 && i >= maxPerJob {
			remove = true
		}
		if maxAgeDays > 0 && b.StartedAt != nil && now.Sub(*b.StartedAt) > maxAge {
			remove = true
		}
		if remove {
			_ = s.builds.DeleteBuild(ctx, p.ID, b.ID)
		}
	}
}

// PruneAll 对全部任务应用保留策略（定时扫描入口）：
// 构建后清理只在有新构建时触发，用户调低全局保留策略后，
// 不活跃的旧任务不会被立即清理；定期扫描补上这一缺口。
func (s *BuildService) PruneAll(ctx context.Context) {
	pipelines, err := s.pipelines.List(ctx)
	if err != nil {
		return
	}
	settings := s.currentSettings()
	// 即使全局策略关闭，也要扫描任务级策略：用户调低某个任务的保留数后，
	// 不活跃任务不会再有新构建触发清理，只能靠这里补扫。
	for _, p := range pipelines {
		s.pruneBuilds(ctx, p, settings)
	}
}

// parseRecipients 拆分默认收件人并补全邮件后缀。
func parseRecipients(raw, suffix string) []string {
	fields := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ';' || r == ' ' || r == '\n' || r == '\t'
	})
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		if suffix != "" && !strings.Contains(f, "@") {
			f += suffix
		}
		out = append(out, f)
	}
	return out
}

// renderSubject 渲染邮件标题模板。
func renderSubject(tpl string, p *model.Pipeline, b *model.Build) string {
	if strings.TrimSpace(tpl) == "" {
		tpl = "$PROJECT_NAME - Build #$BUILD_NUMBER - $BUILD_STATUS"
	}
	return strings.NewReplacer(
		"$PROJECT_NAME", p.Name,
		"$BUILD_NUMBER", b.ID,
		"$BUILD_STATUS", string(b.Status),
	).Replace(tpl)
}

// finishWithError 在拿到日志文件之前发生错误时，直接落一条失败记录。
func (s *BuildService) finishWithError(ctx context.Context, job Job, cause error) {
	b, err := s.builds.Get(ctx, job.PipelineID, job.BuildID)
	if err != nil {
		return
	}
	now := time.Now().UTC()
	b.Status = model.BuildFailed
	b.ExitCode = -1
	if b.StartedAt == nil {
		b.StartedAt = &now
	}
	b.FinishedAt = &now
	// 进程可能已进入优雅退出（ctx 被取消），用不带取消的 ctx 落盘，避免遗留 pending/running。
	persistCtx := context.WithoutCancel(ctx)
	_ = s.builds.Update(persistCtx, b)
	_ = s.builds.AppendLog(persistCtx, job.PipelineID, job.BuildID,
		[]byte(fmt.Sprintf("[ci] build aborted: %v\n", cause)))
}

// finishAborted 把静默期被用户取消的构建落为 aborted 状态（与失败区分）。
func (s *BuildService) finishAborted(job Job, b *model.Build, reason string) {
	now := time.Now().UTC()
	b.Status = model.BuildAborted
	b.ExitCode = -1
	if b.StartedAt == nil {
		b.StartedAt = &now
	}
	b.FinishedAt = &now
	persistCtx := context.WithoutCancel(context.Background())
	if err := s.builds.Update(persistCtx, b); err != nil {
		return
	}
	_ = s.builds.AppendLog(persistCtx, job.PipelineID, job.BuildID,
		[]byte(fmt.Sprintf("[ci] build #%s ABORTED (%s)\n", job.BuildID, reason)))
	logger.Infof("build %s/%s aborted: %s", job.PipelineID, job.BuildID, reason)
}

// DecideApproval 放行或拒绝一次「待审批」的构建。
//
// approve=true  → 状态回到 pending 并进入调度队列（此后与普通构建无异）；
// approve=false → 直接落 aborted（对齐 Jenkins：input 被拒绝即 ABORTED），
// 理由与审批人写进 build.json 与日志，供复盘「谁在什么时候拦下了这次发布」。
func (s *BuildService) DecideApproval(ctx context.Context, pipelineID, buildID string, approve bool, comment, actor string) (*model.Build, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	b, err := s.builds.Get(ctx, pipelineID, buildID)
	if err != nil {
		return nil, err
	}
	if b.Status != model.BuildPendingApproval {
		return nil, pkg.ErrConflict("build #%s is %s, only pending_approval builds can be approved", buildID, b.Status)
	}
	comment = strings.TrimSpace(comment)
	if !approve && comment == "" {
		// 拒绝必须给出理由：否则事后没人知道这次发布为什么被拦下。
		return nil, pkg.ErrBadRequest("拒绝发布必须填写理由")
	}
	if b.Approval == nil {
		b.Approval = &model.BuildApproval{RequestedAt: time.Now().UTC()}
	}
	now := time.Now().UTC()
	b.Approval.DecidedAt = &now
	b.Approval.DecidedBy = strings.TrimSpace(actor)
	b.Approval.Approved = approve
	b.Approval.Comment = comment

	if !approve {
		b.Status = model.BuildAborted
		b.ExitCode = -1
		b.FinishedAt = &now
		if err := s.builds.Update(ctx, b); err != nil {
			return nil, err
		}
		_ = s.builds.AppendLog(ctx, pipelineID, buildID, []byte(fmt.Sprintf(
			"[ci] 发布审批被拒绝（审批人 %s）：%s\n[ci] build #%s ABORTED\n", actorName(b.Approval.DecidedBy), comment, buildID)))
		logger.Infof("build %s/%s approval rejected by %s", pipelineID, buildID, actor)
		return b, nil
	}

	b.Status = model.BuildPending
	if err := s.builds.Update(ctx, b); err != nil {
		return nil, err
	}
	_ = s.builds.AppendLog(ctx, pipelineID, buildID, []byte(fmt.Sprintf(
		"[ci] 发布审批通过（审批人 %s）：%s\n", actorName(b.Approval.DecidedBy), orNone(comment))))
	p, err := s.pipelines.Get(ctx, pipelineID)
	if err != nil {
		return nil, err
	}
	dir := p.WorkspaceDir
	if dir == "" {
		dir = p.ID
	}
	if s.enqueuer == nil {
		return nil, pkg.NewError(pkg.CodeInternal, "scheduler is not initialized")
	}
	if err := s.enqueuer.Enqueue(Job{PipelineID: pipelineID, BuildID: buildID, WorkspaceDir: dir}); err != nil {
		// 入队失败必须让构建明确失败，否则会永远停在 pending。
		b.Status = model.BuildFailed
		b.ExitCode = -1
		b.FinishedAt = &now
		_ = s.builds.Update(ctx, b)
		return nil, err
	}
	logger.Infof("build %s/%s approval granted by %s", pipelineID, buildID, actor)
	return b, nil
}

// SweepApprovals 把超过审批等待上限的构建落为 aborted。
// 由后台定时任务调用：没人处理的发布不能一直挂在「待审批」，
// 否则历史列表里会出现一个永远不动的条目，也会让「该任务上次构建」失真。
func (s *BuildService) SweepApprovals(ctx context.Context) int {
	pipelines, err := s.pipelines.List(ctx)
	if err != nil {
		return 0
	}
	swept := 0
	for _, p := range pipelines {
		if p.ApprovalTimeoutMinutes <= 0 {
			continue
		}
		builds, listErr := s.builds.ListByPipeline(ctx, p.ID)
		if listErr != nil {
			continue
		}
		deadline := time.Duration(p.ApprovalTimeoutMinutes) * time.Minute
		for _, b := range builds {
			if b.Status != model.BuildPendingApproval || b.Approval == nil {
				continue
			}
			if time.Since(b.Approval.RequestedAt) < deadline {
				continue
			}
			now := time.Now().UTC()
			reason := fmt.Sprintf("超过 %d 分钟未审批，自动取消", p.ApprovalTimeoutMinutes)
			b.Status = model.BuildAborted
			b.ExitCode = -1
			b.FinishedAt = &now
			b.Approval.DecidedAt = &now
			b.Approval.Approved = false
			b.Approval.Comment = reason
			if updateErr := s.builds.Update(ctx, b); updateErr != nil {
				continue
			}
			_ = s.builds.AppendLog(ctx, p.ID, b.ID, []byte(fmt.Sprintf(
				"[ci] %s\n[ci] build #%s ABORTED\n", reason, b.ID)))
			logger.Infof("build %s/%s approval timed out after %d minutes", p.ID, b.ID, p.ApprovalTimeoutMinutes)
			swept++
		}
	}
	return swept
}

// actorName 在审批人为空（认证关闭时）给出可读标识。
func actorName(actor string) string {
	if strings.TrimSpace(actor) == "" {
		return "未登录用户"
	}
	return actor
}

// orNone 把空意见转成可读占位。
func orNone(comment string) string {
	if comment == "" {
		return "（未填写意见）"
	}
	return comment
}

// validateTriggerBranch 校验 Webhook 传入的分支名。
// 分支名会直接进 git 命令与工作区路径，必须拒绝空白、路径穿越与选项注入形态。
func validateTriggerBranch(branch string) error {
	if len(branch) > maxBranchNameLen {
		return pkg.ErrBadRequest("分支名过长（最多 %d 字符）", maxBranchNameLen)
	}
	if strings.ContainsAny(branch, " \t\r\n") {
		return pkg.ErrBadRequest("分支名不能包含空白字符")
	}
	if strings.HasPrefix(branch, "-") || strings.Contains(branch, "..") ||
		strings.HasPrefix(branch, "/") || strings.HasSuffix(branch, "/") {
		return pkg.ErrBadRequest("分支名非法：%s", branch)
	}
	return nil
}

// reportCommitStatus 把构建状态回写到代码平台（GitLab commit status）。
//
// 设计要点：
//   - 参数默认关闭（pipeline.commit_status.enabled），不配置就完全不发请求；
//   - 异步执行：一次 HTTP 往返不该拖慢构建，更不能因网络问题让构建失败；
//   - 失败只写平台日志，不写构建日志（避免噪音污染用户可见输出）。
func (s *BuildService) reportCommitStatus(p *model.Pipeline, b *model.Build, settings model.Settings, state, description string) {
	if !p.CommitStatus.Enabled || state == "" {
		return
	}
	if strings.TrimSpace(settings.GitlabBaseURL) == "" {
		logger.Infof("[commit-status] pipeline=%s 已开启状态回写，但未配置「系统配置 → GitLab 地址」", p.ID)
		return
	}
	if strings.TrimSpace(b.Commit) == "" {
		// 非 Git 任务或尚未签出成功：没有 SHA 就无可回写。
		return
	}
	if s.credentials == nil {
		return
	}
	cred, err := s.credentials.Resolve(context.Background(), p.CommitStatus.CredentialID)
	if err != nil {
		logger.Errorf("[commit-status] pipeline=%s resolve credential failed: %v", p.ID, err)
		return
	}
	project := strings.TrimSpace(p.CommitStatus.Project)
	if project == "" {
		project = strings.TrimSpace(b.CommitProject)
	}
	if project == "" {
		// 既没在任务里固定项目、Webhook 也没带 payload：无从回写。
		return
	}
	target := ""
	if base := strings.TrimRight(strings.TrimSpace(settings.JenkinsURL), "/"); base != "" {
		target = fmt.Sprintf("%s/pipelines/%s/builds/%s", base, p.ID, b.ID)
	}
	name := "ci-platform"
	in := executor.GitlabCommitStatus{
		BaseURL:     settings.GitlabBaseURL,
		Token:       cred.Secret,
		Project:     project,
		SHA:         b.Commit,
		State:       state,
		Name:        name,
		TargetURL:   target,
		Description: fmt.Sprintf("#%s %s (%s)", b.ID, description, b.Branch),
	}
	go func() {
		// 与请求生命周期解耦：构建结束时 ctx 可能已被取消。
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := executor.PostGitlabCommitStatus(ctx, in); err != nil {
			logger.Errorf("[commit-status] pipeline=%s build=%s state=%s failed: %v", p.ID, b.ID, state, err)
		}
	}()
}

// gitlabStateOf 把平台状态映射为 GitLab commit status 的 state。
func gitlabStateOf(status model.BuildStatus) string {
	switch status {
	case model.BuildRunning:
		return "running"
	case model.BuildSuccess:
		return "success"
	case model.BuildFailed:
		return "failed"
	case model.BuildAborted:
		return "canceled"
	}
	return ""
}

// buildStatusSummary 给 commit status 生成一句人类可读的说明。
func buildStatusSummary(b *model.Build) string {
	status := string(b.Status)
	if b.Duration() > 0 {
		return fmt.Sprintf("%s，耗时 %s", status, b.Duration().Round(time.Second))
	}
	return status
}

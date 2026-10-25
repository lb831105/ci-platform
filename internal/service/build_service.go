package service

import (
	"ci-platform/internal/pkg/logger"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"ci-platform/internal/executor"
	"ci-platform/internal/model"
	"ci-platform/internal/pkg"
)

// BuildStore 是 service 依赖的构建记录存储抽象（由 store 包实现）。
type BuildStore interface {
	NextBuildID(ctx context.Context, pipelineID string) (string, error)
	CreateNext(ctx context.Context, b *model.Build) error
	Get(ctx context.Context, pipelineID, buildID string) (*model.Build, error)
	Update(ctx context.Context, b *model.Build) error
	ListByPipeline(ctx context.Context, pipelineID string) ([]*model.Build, error)
	AppendLog(ctx context.Context, pipelineID, buildID string, data []byte) error
	ReadLog(ctx context.Context, pipelineID, buildID string) (string, error)
	LogWriter(ctx context.Context, pipelineID, buildID string) (io.WriteCloser, error)
	DeleteBuild(ctx context.Context, pipelineID, buildID string) error
	BuildDir(pipelineID, buildID string) string
}

// StepRunner 由 LocalExecutor 实现（在服务器上直接执行步骤）。
type StepRunner interface {
	RunSteps(ctx context.Context, spec executor.RunSpec, out io.Writer) (int, error)
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
	Collect(ctx context.Context, workspace, buildDir string, patterns []string, limits executor.ArtifactLimits, out io.Writer) ([]model.ArtifactFile, error)
}

// SettingsProvider 提供当前系统配置（由 SettingsService 实现）。
type SettingsProvider interface {
	Current() model.Settings
}

// CredentialResolver 由 CredentialService 实现，供构建阶段解析 Git 凭据。
type CredentialResolver interface {
	Resolve(ctx context.Context, id string) (*model.Credential, error)
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
	artifacts      ArtifactCollector
	artifactReader ArtifactReader
	enqueuer       Enqueuer
	// notifier 负责构建结果通知（项目 × 人员/群 × 事件）；为 nil 时跳过。
	notifier *NotifyService

	// 运行中构建的取消注册表（取消构建用）：key 为 LogKey(pipelineID, buildID)。
	// 构建开始时注册，结束（含取消/panic）时注销。
	cancelMu sync.Mutex
	cancels  map[string]*cancelEntry
}

// cancelEntry 记录一次运行中构建的取消句柄；cancelled 标记用于区分
// “用户主动取消”与“关机/超时导致的取消”，前者落 aborted 状态。
type cancelEntry struct {
	cancel    context.CancelFunc
	cancelled bool
}

// wasCancelled 并发安全地读取取消标记。
func (e *cancelEntry) wasCancelled() bool {
	if e == nil {
		return false
	}
	return e.cancelled
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
		cancels:        make(map[string]*cancelEntry),
	}
}

// SetEnqueuer 注入调度器（由 main 在装配阶段调用，打破构造循环）。
func (s *BuildService) SetEnqueuer(e Enqueuer) { s.enqueuer = e }

// SetCredentialResolver 注入凭据解析器（由 main 在装配阶段调用）。
func (s *BuildService) SetCredentialResolver(r CredentialResolver) { s.credentials = r }

// SetNotifier 注入构建结果通知服务（依赖 RBAC 人员联系方式）。
func (s *BuildService) SetNotifier(n *NotifyService) { s.notifier = n }

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
	// Branch 覆盖任务配置的分支：Webhook 按推送事件的分支构建时使用，
	// 这是「一个任务服务所有分支」的关键（否则推 dev 却构建 main）。
	Branch string
	// CommitProject 是代码平台项目路径（Webhook payload 提供），用于状态回写。
	CommitProject string
}

func (s *BuildService) Trigger(ctx context.Context, pipelineID string, trigger model.TriggerType, triggeredBy string, opts TriggerOptions) (*model.Build, error) {
	p, err := s.pipelines.Get(ctx, pipelineID)
	if err != nil {
		return nil, err
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
	if branch := strings.TrimSpace(opts.Branch); branch != "" {
		if err := validateTriggerBranch(branch); err != nil {
			return nil, err
		}
		// 只改本次构建使用的分支（p 是从磁盘新读出来的副本，不会写回配置）。
		p.GitBranch = branch
	}

	b := &model.Build{
		PipelineID:  pipelineID,
		Status:      model.BuildPending,
		TriggerType: trigger,
		TriggeredBy: strings.TrimSpace(triggeredBy),
		Params:      resolved,
		// 触发时先记下分支：签出后会由 git 现场信息覆盖，但排队/待审批阶段
		// 也需要知道"这次是哪条分支"，否则列表里只看到任务配置的那条。
		Branch:        strings.TrimSpace(p.GitBranch),
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
		b.Status = model.BuildFailed
		b.ExitCode = -1
		_ = s.builds.Update(ctx, b)
		return nil, err
	}
	return b, nil
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
		for _, b := range builds {
			out = append(out, model.BuildListItem{Build: *b, PipelineName: p.Name})
		}
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
func (s *BuildService) ReadLog(ctx context.Context, pipelineID, buildID string) (string, error) {
	if _, err := s.builds.Get(ctx, pipelineID, buildID); err != nil {
		return "", err
	}
	return s.builds.ReadLog(ctx, pipelineID, buildID)
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

	out := io.MultiWriter(limited, executor.NewHubWriter(s.hub, model.LogKey(job.PipelineID, job.BuildID)))
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

	s.reportCommitStatus(p, b, settings, gitlabStateOf(b.Status), buildStatusSummary(b))
	s.pruneBuilds(persistCtx, p, settings)
	s.dispatchNotify(persistCtx, p, b)
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
	entry.cancelled = true
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
			b.CommitAuthor = info.Author
			b.CommitEmail = info.Email
			fmt.Fprintf(out, "[git] checkout %s\n", info.String())
		}
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
	injectLocalToolHomes(settings, env)

	code, err := s.runner.RunSteps(ctx, executor.RunSpec{
		Steps:     p.Steps,
		Workspace: absWorkspace,
		Env:       env,
		Shell:     settings.Shell,
	}, out)
	if err != nil {
		return code, err
	}
	if code != 0 {
		return code, fmt.Errorf("build steps failed with exit code %d", code)
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
		if _, err = s.artifacts.Collect(ctx, absWorkspace, s.builds.BuildDir(job.PipelineID, job.BuildID), p.Artifacts.Files, limits, out); err != nil {
			return 1, fmt.Errorf("archive artifacts: %w", err)
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
				continue
			}
			fmt.Fprintf(out, "[ssh] target %q done\n", sshCfg.Name)
		}
		if len(failures) > 0 {
			return 1, fmt.Errorf("ssh publish failed (%d/%d targets): %s",
				len(failures), len(targets), strings.Join(failures, "; "))
		}
	}
	return 0, nil
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
// 注入为环境变量（JAVA_HOME/GOROOT/...），并把其 bin 目录前置到 PATH。
// 本地执行器据此使用服务器上指定路径的工具（例如指定 GOROOT 后直接用该 Go 编译）。
func injectLocalToolHomes(settings model.Settings, env map[string]string) {
	var bins []string
	for _, category := range settings.ToolCategories {
		for _, item := range category.Installations {
			home := strings.TrimSpace(item.Home)
			if home == "" {
				continue
			}
			home = filepath.FromSlash(home)
			if category.ID == "git" {
				gitExe := filepath.Join(home, "bin", "git")
				if runtime.GOOS == "windows" {
					gitExe += ".exe"
				}
				env["GIT_EXECUTABLE"] = gitExe
			} else if key := toolHomeEnvKey(category.ID); key != "" {
				env[key] = home
			}
			// Windows 版 Node 的可执行文件直接位于主目录（node.exe/npm.cmd），
			// 其他工具通常在 bin 下；两者都加入 PATH 可同时兼容。
			bins = append(bins, filepath.Join(home, "bin"), home)
			break // 每个类别取第一个配置了主目录的安装项
		}
	}
	if len(bins) == 0 {
		return
	}
	base := env["PATH"]
	if base == "" {
		base = os.Getenv("PATH")
	}
	env["PATH"] = strings.Join(bins, string(os.PathListSeparator)) + string(os.PathListSeparator) + base
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
	out := io.MultiWriter(logFile, executor.NewHubWriter(s.hub, model.LogKey(pipelineID, buildID)))

	fmt.Fprintf(out, "\n[republish] start: build #%s -> server %q\n", buildID, cfg.ServerID)
	if err = s.publisher.PublishArtifacts(ctx, cfg, s.builds.BuildDir(pipelineID, buildID), out); err != nil {
		fmt.Fprintf(out, "\n[republish] FAILED: %v\n", err)
		return err
	}
	fmt.Fprintf(out, "\n[republish] finished\n")
	return nil
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
	// 全局策略未启用时无需扫描（任务级配置在各自构建后已生效）。
	if settings.BuildRetentionMaxPerJob <= 0 && settings.BuildRetentionMaxAgeDays <= 0 {
		return
	}
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

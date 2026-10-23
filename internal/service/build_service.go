package service

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
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
	}
}

// SetEnqueuer 注入调度器（由 main 在装配阶段调用，打破构造循环）。
func (s *BuildService) SetEnqueuer(e Enqueuer) { s.enqueuer = e }

// SetCredentialResolver 注入凭据解析器（由 main 在装配阶段调用）。
func (s *BuildService) SetCredentialResolver(r CredentialResolver) { s.credentials = r }

// Recover 在启动时把上次进程异常退出遗留的 pending/running 构建标记为失败，
// 避免任务永远显示「运行中」。必须在调度器启动前调用。
func (s *BuildService) Recover(ctx context.Context) {
	pipelines, err := s.pipelines.List(ctx)
	if err != nil {
		log.Printf("recover builds: list pipelines: %v", err)
		return
	}
	recovered := 0
	for _, p := range pipelines {
		builds, listErr := s.builds.ListByPipeline(ctx, p.ID)
		if listErr != nil {
			continue
		}
		for _, b := range builds {
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
		log.Printf("recovered %d interrupted build(s) -> failed", recovered)
	}
}

// Trigger 创建构建记录并入队，返回 pending 状态的构建。
func (s *BuildService) Trigger(ctx context.Context, pipelineID string, trigger model.TriggerType, triggeredBy string) (*model.Build, error) {
	p, err := s.pipelines.Get(ctx, pipelineID)
	if err != nil {
		return nil, err
	}
	if s.enqueuer == nil {
		return nil, pkg.NewError(pkg.CodeInternal, "scheduler is not initialized")
	}

	b := &model.Build{
		PipelineID:  pipelineID,
		Status:      model.BuildPending,
		TriggerType: trigger,
		TriggeredBy: strings.TrimSpace(triggeredBy),
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
			log.Printf("build panic (pipeline=%s build=%s): %v", job.PipelineID, job.BuildID, rec)
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

	// 静默期：在真正开始前等待若干秒，期间保持 pending。
	if settings.QuietPeriodSec > 0 {
		select {
		case <-ctx.Done():
			// 关闭/取消发生在静默等待阶段：显式落失败，避免遗留 pending。
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

	// 日志同时写文件与广播到 WebSocket 订阅者。
	logFile, err := s.builds.LogWriter(ctx, job.PipelineID, job.BuildID)
	if err != nil {
		s.finishWithError(ctx, job, fmt.Errorf("open log file: %w", err))
		return
	}
	defer func() { _ = logFile.Close() }()

	out := io.MultiWriter(logFile, executor.NewHubWriter(s.hub, model.LogKey(job.PipelineID, job.BuildID)))
	fmt.Fprintf(out, "[ci] build #%s of pipeline %q started (trigger=%s, executor=local)\n",
		job.BuildID, p.Name, b.TriggerType)

	// Global Build Time Out：仅约束执行阶段，状态落盘仍用原始 ctx。
	runCtx := ctx
	if settings.GlobalBuildTimeoutMinutes > 0 {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(ctx, time.Duration(settings.GlobalBuildTimeoutMinutes)*time.Minute)
		defer cancel()
	}

	exitCode, buildErr := s.executeStages(runCtx, p, job, settings, out)

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
	// 即使进程正在优雅退出（ctx 已取消），也要落盘最终状态，
	// 避免构建永远停留在 running。
	persistCtx := context.WithoutCancel(ctx)
	_ = s.builds.Update(persistCtx, b)

	s.pruneBuilds(persistCtx, p, settings)
	s.notify(p, b, settings)
}

// executeStages 串行执行：Git 拉取 → 本地构建 → SSH 发布。
func (s *BuildService) executeStages(ctx context.Context, p *model.Pipeline, job Job, settings model.Settings, out io.Writer) (int, error) {
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
	}

	env := map[string]string{
		"CI":               "true",
		"CI_BUILD_ID":      job.BuildID,
		"CI_BUILD_NUMBER":  job.BuildID,
		"CI_PIPELINE_NAME": p.Name,
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

	// 仅当 ssh_publish.enabled=true 时才发起 SSH 连接。
	if p.SSHEnabled() {
		sshCfg, resolveErr := resolveSshConfig(p.SshPublish, settings)
		if resolveErr != nil {
			return 1, resolveErr
		}
		if err = s.publisher.Publish(ctx, sshCfg, absWorkspace, out); err != nil {
			return 1, fmt.Errorf("ssh publish failed: %w", err)
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
func (s *BuildService) Republish(ctx context.Context, pipelineID, buildID, serverID string) error {
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
	if p.SshPublish == nil {
		return pkg.ErrBadRequest("任务未配置 SSH 发布")
	}
	spec := *p.SshPublish
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

// notify 在构建结束后按配置发送邮件通知；失败只记日志，不影响构建结果。
func (s *BuildService) notify(p *model.Pipeline, b *model.Build, settings model.Settings) {
	cfg := settings.Email
	if !cfg.Enabled {
		return
	}
	if b.Status == model.BuildFailed && !cfg.NotifyOnFailure {
		return
	}
	if b.Status == model.BuildSuccess && !cfg.NotifyOnSuccess {
		return
	}
	recipients := parseRecipients(cfg.Recipients, cfg.DefaultSuffix)
	if len(recipients) == 0 {
		return
	}
	subject := renderSubject(cfg.SubjectTemplate, p, b)
	body := fmt.Sprintf("任务: %s\n构建: #%s\n状态: %s\n时间: %s\n", p.Name, b.ID, b.Status, b.FinishedAt)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := executor.SendMailContext(ctx, cfg, recipients, subject, body); err != nil {
		log.Printf("email notify failed (pipeline=%s build=%s): %v", p.ID, b.ID, err)
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

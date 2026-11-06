package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"ci-platform" // 根包 assets：内嵌 web/dist
	"ci-platform/internal/config"
	"ci-platform/internal/executor"
	"ci-platform/internal/handler"
	"ci-platform/internal/model"
	"ci-platform/internal/pkg"
	"ci-platform/internal/pkg/logger"
	"ci-platform/internal/router"
	"ci-platform/internal/service"
	"ci-platform/internal/store"
	"ci-platform/internal/tools"
)

// 以下变量由构建脚本通过 -ldflags "-X main.buildVersion=..." 注入，
// 未注入时保留默认值，保证 `go run` 也能正常启动。
var (
	buildVersion = "dev"
	buildCommit  = "none"
	buildDate    = "unknown"
)

// main 只做装配：构造 config / store / executor / service / handler，
// 启动调度器与 HTTP 服务，并处理优雅退出。业务逻辑不在此处出现。
func main() {
	// 三方库/未迁移的 stdlib log 输出仍然走 stderr，并带上时间戳。
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)

	// 系统日志缓冲：平台日志同时进内存环形缓冲，供「系统管理 → 系统日志」页面回看。
	logBuffer := pkg.NewLogBuffer(2000)

	// 0. 装配参数：环境变量打底，命令行覆盖（-port / -addr / -data-home ...）
	cfg := config.Load()
	if err := cfg.ParseArgs(os.Args[1:]); err != nil {
		if errors.Is(err, config.ErrHelp) {
			return // -h/--help 已打印帮助，正常退出
		}
		fmt.Fprintf(os.Stderr, "参数错误: %v\n\n", err)
		fmt.Fprintf(os.Stderr, "运行 `%s -h` 查看全部可用参数。\n", progName())
		os.Exit(2)
	}
	if cfg.ShowVersion {
		fmt.Printf("ci-platform version=%s commit=%s built=%s\n", buildVersion, buildCommit, buildDate)
		return
	}

	// 数据目录可由引导配置（默认 ci-platform.json）指定；
	// 若通过 -data-home / CI_DATA_HOME 显式指定，则以显式值为准。
	if !cfg.DataHomeSet {
		if b, bootErr := config.LoadBootstrap(cfg.ConfigFile); bootErr != nil {
			log.Printf("WARN: 读取引导配置 %s 失败: %v", cfg.ConfigFile, bootErr)
		} else if b != nil && b.DataHome != "" {
			cfg.DataHome = b.DataHome
			logger.Infof("data home from bootstrap %s: %s", cfg.ConfigFile, cfg.DataHome)
		}
	}

	if err := cfg.Validate(); err != nil {
		log.Fatalf("配置错误: %v", err)
	}

	// 1.0 日志系统：带级别、按月轮转到 data/logs/app-YYYY-MM.log。
	// 必须在其它日志之前初始化，否则启动阶段的消息会缺失或没有级别。
	level, levelErr := logger.ParseLevel(cfg.LogLevel)
	if levelErr != nil {
		logger.Warnf("%v，已回退为 info", levelErr)
	}
	if err := logger.Init(logger.Options{
		Dir:           filepath.Join(cfg.DataHome, "logs"),
		Level:         level,
		RetainDays:    cfg.LogRetainDays,
		RetainForever: cfg.LogRetainDays == 0,
		Stdout:        os.Stderr,
		Sink:          logBuffer,
	}); err != nil {
		// 日志落盘失败不阻断启动：继续用控制台输出即可，但要让运维看见。
		logger.Warnf("初始化文件日志失败（仅输出到控制台）: %v", err)
	}
	defer func() { _ = logger.Close() }()

	logger.Infof("ci-platform version=%s commit=%s built=%s level=%s log_file=%s retain_days=%d",
		buildVersion, buildCommit, buildDate, level, logger.CurrentFile(), cfg.LogRetainDays)

	// 1. 运行时目录（文件即数据库）
	for _, dir := range []string{cfg.JobsDir(), cfg.WorkspaceDir()} {
		if err := pkg.EnsureDir(dir); err != nil {
			log.Fatalf("init data dir: %v", err)
		}
	}

	// 2. 存储层
	pipelineStore := store.NewPipelineStore(cfg.JobsDir())
	buildStore := store.NewBuildStore(cfg.JobsDir())

	// 2.1 系统配置（data/settings.json）：启动参数仅提供默认值，此后以文件为准
	settingsSvc, err := service.NewSettingsService(store.NewSettingsStore(cfg.DataHome), defaultSettings(cfg))
	if err != nil {
		log.Fatalf("init settings: %v", err)
	}
	settings := settingsSvc.Current()

	// 3. 执行器层：平台已改为纯本地构建（直接在服务器上使用已安装的工具链）
	localExec := executor.NewLocalExecutor()
	gitClient := executor.NewGitClient()
	sshPublisher := executor.NewSshPublisher()
	artifactCollector := executor.NewArtifactCollector()
	hub := executor.NewHub()

	// 4. 业务层（先建 workspace 解析器，避免 service 之间构造循环）
	workspace := service.NewWorkspaceManager(cfg.WorkspaceDir())
	pipelineSvc := service.NewPipelineService(pipelineStore, settingsSvc, workspace)
	artifactSvc := service.NewArtifactService(buildStore)
	buildSvc := service.NewBuildService(pipelineStore, buildStore, gitClient, localExec, sshPublisher, hub, workspace, settingsSvc, artifactCollector, artifactSvc)
	scheduler := service.NewScheduler(buildSvc, cfg.QueueSize, workspace, cfg.WorkspaceTTL, cfg.CleanInterval)
	buildSvc.SetQueueStats(scheduler)
	scheduler.SetWorkspaceKeeper(func() (map[string]bool, error) {
		pipelines, err := pipelineStore.List(context.Background())
		if err != nil {
			return nil, err
		}
		keep := make(map[string]bool, len(pipelines))
		for _, p := range pipelines {
			dir := p.WorkspaceDir
			if dir == "" {
				dir = p.ID
			}
			keep[dir] = true
		}
		return keep, nil
	})
	buildSvc.SetEnqueuer(scheduler)
	scheduler.SetQuiet(settings.QuietMode)
	// 定时构建触发器随任务在 Start 前创建；扫描循环在 workerCtx 启动后开启。
	cronTrigger := service.NewCronTrigger(pipelineStore, buildSvc, 30*time.Second)

	// 4.1 认证服务（基础版单管理员账号，会话存内存；凭据以 settings.json 为准）
	authSvc := service.NewAuthService(settings.AuthEnabled, settings.AuthUser, settings.AuthPasswordHash, cfg.AuthTokenTTL)
	// RBAC：角色、授权与平台内用户（data/rbac.json），对齐 Jenkins Manage and Assign Roles。
	rbacSvc, rbacErr := service.NewRBACService(store.NewRbacStore(cfg.DataHome), authSvc)
	if rbacErr != nil {
		log.Fatalf("init rbac: %v", rbacErr)
	}
	// 任务可见性：列表/概览/全局构建历史按任务角色 Pattern 过滤，
	// 让「角色 ↔ 任务」真正挂钩（看不到的任务不出现，而不是点进去才 403）。
	if pipelineSvc != nil {
		pipelineSvc.SetJobScoper(rbacSvc)
	}
	// 多用户登录：管理员凭据不匹配时回退校验平台内用户。
	authSvc.SetUserVerifier(rbacSvc)
	// 平台内用户（rbac.json 里的账号）自助改密：内置管理员走系统配置，其余走 RBAC。
	authSvc.SetUserPasswordUpdater(rbacSvc)
	// API Token：独立持久化文件，只存哈希；校验时确认账号仍存在。
	apiTokenSvc := service.NewAPITokenService(store.NewTokenStore(cfg.DataHome))
	apiTokenSvc.SetUserChecker(func(name string) bool {
		return authSvc.IsAdmin(name) || rbacSvc.UserExists(name)
	})
	authSvc.SetAPITokenVerifier(apiTokenSvc)
	if authSvc.Enabled() {
		logger.Infof("auth enabled: user=%s (可在「系统管理 → 全局安全配置」中修改，-no-auth 关闭)", settings.AuthUser)
		if cfg.AuthPasswordGenerated && authSvc.UsesPassword(cfg.AuthPassword) {
			// 只在随机密码确实被采用（首次初始化）时输出一次；已有 settings.json
			// 的实例不会重复打印。请立即登录并修改。
			logger.Warnf("首次启动已生成管理员初始密码: %s（请立即登录并修改）", cfg.AuthPassword)
		}
		// 只提示"还没改过初始密码"，不写出具体口令：
		// 启动日志常被收集/转发，写出默认口令等于把凭据公开。
		if authSvc.UsesPassword("admin") {
			logger.Warnf("管理员密码仍为初始密码，请登录后在「个人资料 → 修改密码」中修改")
		}
	} else {
		logger.Warnf("登录校验已关闭（-no-auth / CI_AUTH_ENABLED=false），任何人都可直接访问")
	}

	// 4.2 凭据管理与系统管理服务
	credentialSvc := service.NewCredentialService(store.NewCredentialStore(cfg.DataHome))
	buildSvc.SetCredentialResolver(credentialSvc)
	// 密码参数与凭据共用 credentials.key 做 AES-GCM 加密，避免 build.json 出现明文。
	buildSvc.SetSecretCodec(credentialSvc)
	// 操作审计：JSONL 追加写 data/audit.log；启动时恢复最大 ID 保证单调。
	auditStore := store.NewAuditStore(cfg.DataHome)
	var auditSvc *service.AuditService
	if lastEntries, err := auditStore.Tail(1); err == nil {
		auditSvc = service.NewAuditService(auditStore, service.LastIDFromEntries(lastEntries))
	} else {
		auditSvc = service.NewAuditService(auditStore, 0)
	}
	systemSvc := service.NewSystemService(
		cfg, settingsSvc, pipelineStore, buildSvc, scheduler, logBuffer,
		pkg.BuildInfo{Version: buildVersion, Commit: buildCommit, Date: buildDate},
		auditSvc,
	)

	// 5. 前端资源：优先使用二进制内嵌的 web/dist，其次回退磁盘目录
	webAssets, source := assets.Resolve(cfg.WebDir)
	logger.Infof("web assets source: %s", source)
	if webAssets == nil {
		logger.Warnf("前端资源不可用（既未内嵌也未在 %s 找到 index.html），"+
			"Web 控制台将返回提示页。请执行 `cd web && npm install && npm run build` 后重新编译。", cfg.WebDir)
	}

	// 6. 接口层
	pipelineHandler := handler.NewPipelineHandler(pipelineSvc, buildSvc)
	buildHandler := handler.NewBuildHandler(buildSvc, artifactSvc)
	buildHandler.SetRBAC(rbacSvc)
	webhookHandler := handler.NewWebhookHandler(pipelineSvc, buildSvc, pipelineHandler, auditSvc)
	wsHandler := handler.NewWSHandler(hub, buildSvc, cfg.LogBufferSize, cfg.CORSOrigins)
	authHandler := handler.NewAuthHandler(authSvc, settingsSvc)
	authHandler.SetRBAC(rbacSvc)
	systemHandler := handler.NewSystemHandler(systemSvc, authSvc)
	// 全局出站事件订阅：URL/Secret 加密落盘，构建状态变化时异步推送。
	eventSvc := service.NewEventService(store.NewEventStore(cfg.DataHome))
	buildSvc.SetEvents(eventSvc)
	eventHandler := handler.NewEventHandler(eventSvc)
	credentialHandler := handler.NewCredentialHandler(credentialSvc)
	// 工具自动安装：下载到 data/tools，构建时按需调用（首次会下载）。
	toolInstaller := tools.NewInstaller(cfg.ToolsDir(), cfg.ToolsMirror, func(format string, args ...any) {
		logger.Infof(format, args...)
	})
	buildSvc.SetToolInstaller(toolInstaller)
	// 后台安装任务随进程结束而中止（下载中的半成品不会写标记，下次会重下）。
	defer toolInstaller.Close()
	toolsHandler := handler.NewToolsHandler(toolInstaller)

	rbacHandler := handler.NewRbacHandler(rbacSvc, authSvc)
	rbacHandler.SetTokens(apiTokenSvc)
	rbacHandler.SetPipelines(pipelineSvc)
	// 历史用户可能没有邮箱（旧版本允许留空）：启动时显式告警，
	// 否则「任务里选了人却收不到邮件」会变成静默故障。
	if missing := rbacSvc.UsersWithoutEmail(); len(missing) > 0 {
		logger.Warnf("以下用户缺少邮箱，构建通知的邮件无法送达，请在「系统管理 → 用户管理」补全: %s",
			strings.Join(missing, ", "))
	}

	// 构建结果通知：把「项目」与「人员/群」绑定（人员联系方式来自 RBAC 用户）。
	notifySvc := service.NewNotifyService(settingsSvc, rbacSvc)
	// 通知记录（data/notify.log）：结构化的投递结果，用于排查「谁漏收了告警」。
	notifySvc.SetRecorder(store.NewNotifyStore(cfg.DataHome))
	// 连续失败计数与抑制窗口需要构建历史。
	notifySvc.SetHistory(buildStore)
	buildSvc.SetNotifier(notifySvc)
	notifyHandler := handler.NewNotifyHandler(pipelineSvc, notifySvc, rbacSvc)
	// 个人资料：用户自助维护联系方式与邮箱验证。
	profileSvc := service.NewProfileService(rbacSvc, settingsSvc)
	profileHandler := handler.NewProfileHandler(profileSvc)
	tokenHandler := handler.NewTokenHandler(apiTokenSvc)
	// 视图：给任务归类（列表视图 + 我的视图），data/views.json。
	viewSvc := service.NewViewService(store.NewViewStore(cfg.DataHome), pipelineSvc, rbacSvc)
	viewHandler := handler.NewViewHandler(viewSvc, pipelineSvc, buildSvc)
	// 从 Jenkins 导入任务：XML → 平台任务定义，SSH 服务器名自动映射到 server_id。
	importHandler := handler.NewImportHandler(pipelineSvc, settingsSvc)
	// 任务配置导出/导入（平台自有 JSON）：导出脱敏，导入按服务器/凭据名称对齐。
	portabilitySvc := service.NewPortabilityService(pipelineSvc, settingsSvc, credentialSvc, buildVersion)
	portabilityHandler := handler.NewPortabilityHandler(portabilitySvc)

	handlerRouter := router.New(router.Deps{
		Config:      cfg,
		Pipeline:    pipelineHandler,
		Build:       buildHandler,
		Webhook:     webhookHandler,
		WebSocket:   wsHandler,
		Auth:        authHandler,
		System:      systemHandler,
		Credential:  credentialHandler,
		RBAC:        rbacHandler,
		View:        viewHandler,
		Notify:      notifyHandler,
		Profile:     profileHandler,
		Token:       tokenHandler,
		Import:      importHandler,
		Portability: portabilityHandler,
		AuthService: authSvc,
		Readiness: func(ctx context.Context) error {
			return credentialSvc.Health(ctx)
		},
		RBACService:     rbacSvc,
		Tools:           toolsHandler,
		Event:           eventHandler,
		PipelineService: pipelineSvc,
		AuditService:    auditSvc,
		WebAssets:       webAssets,
	})

	// 7. 根 context：收到 SIGINT/SIGTERM 时取消，worker 与清理协程随之退出
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// 旧任务工作区目录名从数字 ID 迁移为任务名，方便直接查看。
	migrateWorkspaceDirs(ctx, pipelineSvc)

	// 一次性迁移：旧版本默认“构建前清空工作区”，会导致每次构建重新 clone。
	migrateWorkspacePolicy(ctx, cfg.DataHome, pipelineSvc)

	// 一次性迁移：为步骤中明确写了输出文件的旧任务自动启用产物归档。
	migrateArtifactConfig(ctx, cfg.DataHome, pipelineSvc)

	// 启动前先把上次异常退出遗留的 pending/running 构建标记为失败，避免永远「运行中」。
	buildSvc.Recover(context.Background())

	// worker 使用独立 context：收到停机信号后先停止接收新构建，再给运行中的构建
	// 一段排空时间；超时才取消，避免正常部署直接杀掉构建。
	workerCtx, stopWorkers := context.WithCancel(context.Background())
	defer stopWorkers()
	scheduler.Start(workerCtx, cfg.Workers)
	// 定时构建：随 worker 上下文启停（优雅停机时一并退出）。
	cronTrigger.Start(workerCtx)
	// 发布审批超时扫描：无人处理的 pending_approval 必须按任务配置的
	// approval_timeout_minutes 自动取消，否则会永远挂在待审批。
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-workerCtx.Done():
				return
			case <-ticker.C:
				if n := buildSvc.SweepApprovals(workerCtx); n > 0 {
					logger.Infof("approval sweep: %d build(s) timed out", n)
				}
			}
		}
	}()
	// 构建保留策略定期扫描：每小时应用全局保留策略到全部任务
	// （构建后清理只覆盖有新构建的任务，旧任务需要定期补扫）。
	go func() {
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-workerCtx.Done():
				return
			case <-ticker.C:
				buildSvc.PruneAll(workerCtx)
			}
		}
	}()

	server := &http.Server{
		Addr:              cfg.Addr,
		Handler:           handlerRouter,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      10 * time.Minute,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}

	go func() {
		absDataHome, absErr := filepath.Abs(cfg.DataHome)
		if absErr != nil {
			absDataHome = cfg.DataHome
		}
		absConfig, cfgErr := filepath.Abs(cfg.ConfigFile)
		if cfgErr != nil {
			absConfig = cfg.ConfigFile
		}
		logger.Infof("ci-platform listening on %s (data_home=%s, config=%s, workers=%d, queue=%d, pid=%d)",
			cfg.Addr, absDataHome, absConfig, cfg.Workers, cfg.QueueSize, os.Getpid())
		logger.Infof("web console: %s", cfg.ListenURL())
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("http server: %v", err)
		}
	}()

	<-ctx.Done()
	logger.Infof("%s", "shutting down...")
	scheduler.SetQuiet(true)

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Errorf("graceful shutdown failed: %v", err)
	}
	cancel()

	if cfg.ShutdownTimeout > 0 {
		logger.Infof("waiting up to %s for running builds...", cfg.ShutdownTimeout)
		drainCtx, drainCancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
		scheduler.Drain(drainCtx)
		drainCancel()
	}
	stopWorkers()
	waitCtx, waitCancel := context.WithTimeout(context.Background(), 5*time.Second)
	scheduler.Wait(waitCtx)
	waitCancel()
	logger.Infof("%s", "bye")
}

// migrateWorkspaceDirs 把 data/workspace/{id} 迁移为可读的任务名目录。
func migrateWorkspaceDirs(ctx context.Context, svc *service.PipelineService) {
	n, err := svc.MigrateWorkspaceDirs(ctx)
	if n > 0 {
		logger.Infof("workspace dirs migrated: %d", n)
	}
	if err != nil {
		logger.Warnf("部分工作区目录名迁移失败（下次启动会重试）: %v", err)
	}
}

// migrateWorkspacePolicy 只执行一次；标记文件存在后，用户重新勾选“清空工作区”不会被再次覆盖。
func migrateWorkspacePolicy(ctx context.Context, dataHome string, svc *service.PipelineService) {
	marker := filepath.Join(dataHome, ".workspace-reuse-v1")
	if _, err := os.Stat(marker); err == nil {
		return
	} else if !errors.Is(err, os.ErrNotExist) {
		logger.Warnf("检查工作区迁移标记失败: %v", err)
		return
	}
	n, err := svc.MigrateWorkspacePolicy(ctx)
	if err != nil {
		logger.Warnf("迁移工作区策略失败: %v", err)
		return
	}
	if n > 0 {
		logger.Infof("workspace policy migrated: %d Git pipeline(s) now reuse workspace", n)
	}
	if err := os.WriteFile(marker, []byte(time.Now().UTC().Format(time.RFC3339)+"\n"), 0o644); err != nil {
		logger.Warnf("写入工作区迁移标记失败: %v", err)
	}
}

// migrateArtifactConfig 只执行一次；能从 go build -o / jar cf 推断出产物时，
// 自动为旧任务启用归档，避免升级后仍只保留日志。之后用户可自由关闭。
func migrateArtifactConfig(ctx context.Context, dataHome string, svc *service.PipelineService) {
	marker := filepath.Join(dataHome, ".artifact-config-v1")
	if _, err := os.Stat(marker); err == nil {
		return
	} else if !errors.Is(err, os.ErrNotExist) {
		logger.Warnf("检查产物归档迁移标记失败: %v", err)
		return
	}
	n, err := svc.MigrateArtifactConfig(ctx)
	if err != nil {
		logger.Warnf("迁移产物归档配置失败: %v", err)
		return
	}
	if n > 0 {
		logger.Infof("artifact config migrated: %d pipeline(s)", n)
	}
	if err := os.WriteFile(marker, []byte(time.Now().UTC().Format(time.RFC3339)+"\n"), 0o644); err != nil {
		logger.Warnf("写入产物归档迁移标记失败: %v", err)
	}
}

// defaultSettings 把启动参数转换为系统配置的初始默认值。
// 仅在 data/settings.json 不存在或缺少字段时生效，之后以文件为准。
func defaultSettings(cfg *config.Config) model.Settings {
	return model.Settings{
		DisplayName:    "CI Platform",
		DefaultBranch:  "main",
		DefaultImage:   "alpine:3.19",
		Shell:          model.DefaultShell(),
		ToolCategories: defaultToolCategories(),
		SshServers:     []model.SshServer{},
		Maven:          defaultMavenSettings(),
		Email: model.EmailSettings{
			SMTPPort:        25,
			NotifyOnFailure: true,
			SubjectTemplate: "$PROJECT_NAME - Build #$BUILD_NUMBER - $BUILD_STATUS",
		},
		AuthEnabled:          cfg.AuthEnabled,
		AuthUser:             cfg.AuthUser,
		AuthPassword:         cfg.AuthPassword,
		Workers:              cfg.Workers,
		QueueSize:            cfg.QueueSize,
		WorkspaceTTLMinutes:  int(cfg.WorkspaceTTL.Minutes()),
		CleanIntervalMinutes: int(cfg.CleanInterval.Minutes()),
		// 构建保留默认值：每个任务保留 200 条、90 天，避免新实例无限增长。
		// 0 仍表示「不限制」，旧 settings.json 里显式配置的 0 不会被覆盖。
		BuildRetentionMaxPerJob:  200,
		BuildRetentionMaxAgeDays: 90,
		// 构建日志单次上限：默认 100MB（0/缺失时归一化为该值），超长日志截断。
		LogMaxMB: 100,
	}
}

// defaultToolCategories 预置常用工具类别，对应 Jenkins「全局工具配置」。
// 平台直接使用服务器上已安装的工具，安装项填写工具主目录（JAVA_HOME / GOROOT 等）。
// defaultToolCategories 返回默认的「全局工具配置」。
//
// 默认就按**自动安装**填好（Jenkins 里对应「Install automatically」）：
// 新部署的机器上不用先去装 JDK/Node，第一次构建时平台自己下载解压。
// 只有被任务真正用到的工具才会下载（任务里选了该工具，或步骤里调用了对应命令），
// 所以预置这些条目不会让首次构建凭空下载几百 MB。
func defaultToolCategories() []model.ToolCategory {
	return []model.ToolCategory{
		{
			ID:    "jdk",
			Title: "JDK 安装",
			Hint:  "构建 Java 项目需要；也可改用手动路径指向服务器已装的 JDK",
			Installations: []model.ToolInstallation{
				{Name: "JDK 17", InstallMode: model.ToolInstallAuto, Version: "17.0.12+7",
					Note: "Temurin 17 LTS"},
				{Name: "JDK 8", InstallMode: model.ToolInstallAuto, Version: "8u422-b05",
					Note: "老项目用（1.8）"},
			},
		},
		{
			ID:    "git",
			Title: "Git installations",
			Hint:  "平台使用内置 Git 客户端拉取代码；如需在步骤里调用 git，可填写 Git 主目录",
			Installations: []model.ToolInstallation{
				{Name: "Git"},
			},
		},
		{
			ID:    "golang",
			Title: "Golang 安装",
			Hint:  "Go 项目；也可改用手动路径指向服务器已装的 Go",
			Installations: []model.ToolInstallation{
				{Name: "Go 1.23.2", InstallMode: model.ToolInstallAuto, Version: "1.23.2"},
			},
		},
		{
			ID:    "gradle",
			Title: "Gradle 安装",
			Hint:  "Gradle 项目；也可改用手动路径",
			Installations: []model.ToolInstallation{
				{Name: "Gradle 8.10.2", InstallMode: model.ToolInstallAuto, Version: "8.10.2"},
			},
		},
		{
			ID:    "ant",
			Title: "Ant 安装",
			Hint:  "Ant 项目；也可改用手动路径",
			Installations: []model.ToolInstallation{
				{Name: "Ant 1.10.14", InstallMode: model.ToolInstallAuto, Version: "1.10.14"},
			},
		},
		{
			ID:    "maven",
			Title: "Maven 安装",
			Hint:  "Maven 项目；也可改用手动路径指向服务器已装的 Maven",
			Installations: []model.ToolInstallation{
				{Name: "Maven 3.9.9", InstallMode: model.ToolInstallAuto, Version: "3.9.9"},
			},
		},
		{
			ID:    "nodejs",
			Title: "NodeJS 安装",
			Hint:  "前端项目；也可改用手动路径指向服务器已装的 Node.js",
			Installations: []model.ToolInstallation{
				{Name: "Node 20", InstallMode: model.ToolInstallAuto, Version: "20.18.0",
					Note: "LTS"},
			},
		},
	}
}

// defaultMavenSettings 返回 Maven 全局配置的默认值（MAVEN_OPTS / 本地仓库留空，
// 表示沿用服务器 ~/.m2 与 mvn 自身默认）。settings.xml 内容不在平台里维护：
// 它不会被写入磁盘也不会被构建使用，摆在界面上只会让人误以为已经生效。
func defaultMavenSettings() model.MavenSettings {
	return model.MavenSettings{}
}

// progName 返回可执行文件名，仅用于拼装帮助提示，不参与业务流程。
func progName() string {
	if len(os.Args) == 0 || os.Args[0] == "" {
		return "ci-platform"
	}
	return filepath.Base(os.Args[0])
}

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"ci-platform" // 根包 assets：内嵌 web/dist
	"ci-platform/internal/config"
	"ci-platform/internal/executor"
	"ci-platform/internal/handler"
	"ci-platform/internal/model"
	"ci-platform/internal/pkg"
	"ci-platform/internal/router"
	"ci-platform/internal/service"
	"ci-platform/internal/store"
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
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)

	// 系统日志缓冲：标准日志同时写入 stderr 与内存环形缓冲，
	// 供「系统管理 → 系统日志」页面回看，不额外落盘。
	logBuffer := pkg.NewLogBuffer(2000)
	log.SetOutput(io.MultiWriter(os.Stderr, logBuffer))

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
			log.Printf("data home from bootstrap %s: %s", cfg.ConfigFile, cfg.DataHome)
		}
	}

	if err := cfg.Validate(); err != nil {
		log.Fatalf("配置错误: %v", err)
	}

	log.Printf("ci-platform version=%s commit=%s built=%s", buildVersion, buildCommit, buildDate)

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

	// 4.1 认证服务（基础版单管理员账号，会话存内存；凭据以 settings.json 为准）
	authSvc := service.NewAuthService(settings.AuthEnabled, settings.AuthUser, settings.AuthPasswordHash, cfg.AuthTokenTTL)
	if authSvc.Enabled() {
		log.Printf("auth enabled: user=%s (可在「系统管理 → 全局安全配置」中修改，-no-auth 关闭)", settings.AuthUser)
		if authSvc.UsesPassword("admin") {
			log.Printf("WARN: 正在使用默认管理员密码 admin，请在首次登录后立即修改（系统管理 → 全局安全配置）")
		}
	} else {
		log.Printf("WARN: 登录校验已关闭（-no-auth / CI_AUTH_ENABLED=false），任何人都可直接访问")
	}

	// 4.2 凭据管理与系统管理服务
	credentialSvc := service.NewCredentialService(store.NewCredentialStore(cfg.DataHome))
	buildSvc.SetCredentialResolver(credentialSvc)
	systemSvc := service.NewSystemService(
		cfg, settingsSvc, pipelineStore, buildSvc, scheduler, logBuffer,
		pkg.BuildInfo{Version: buildVersion, Commit: buildCommit, Date: buildDate},
	)

	// 5. 前端资源：优先使用二进制内嵌的 web/dist，其次回退磁盘目录
	webAssets, source := assets.Resolve(cfg.WebDir)
	log.Printf("web assets source: %s", source)
	if webAssets == nil {
		log.Printf("WARN: 前端资源不可用（既未内嵌也未在 %s 找到 index.html），"+
			"Web 控制台将返回提示页。请执行 `cd web && npm install && npm run build` 后重新编译。", cfg.WebDir)
	}

	// 6. 接口层
	pipelineHandler := handler.NewPipelineHandler(pipelineSvc, buildSvc)
	buildHandler := handler.NewBuildHandler(buildSvc, artifactSvc)
	webhookHandler := handler.NewWebhookHandler(pipelineSvc, buildSvc, pipelineHandler)
	wsHandler := handler.NewWSHandler(hub, buildSvc, cfg.LogBufferSize, cfg.CORSOrigins)
	authHandler := handler.NewAuthHandler(authSvc, settingsSvc)
	systemHandler := handler.NewSystemHandler(systemSvc, authSvc)
	credentialHandler := handler.NewCredentialHandler(credentialSvc)

	handlerRouter := router.New(router.Deps{
		Config:      cfg,
		Pipeline:    pipelineHandler,
		Build:       buildHandler,
		Webhook:     webhookHandler,
		WebSocket:   wsHandler,
		Auth:        authHandler,
		System:      systemHandler,
		Credential:  credentialHandler,
		AuthService: authSvc,
		WebAssets:   webAssets,
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
		log.Printf("ci-platform listening on %s (data_home=%s, config=%s, workers=%d, queue=%d, pid=%d)",
			cfg.Addr, absDataHome, absConfig, cfg.Workers, cfg.QueueSize, os.Getpid())
		log.Printf("web console: %s", cfg.ListenURL())
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("http server: %v", err)
		}
	}()

	<-ctx.Done()
	log.Println("shutting down...")
	scheduler.SetQuiet(true)

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Printf("graceful shutdown failed: %v", err)
	}
	cancel()

	if cfg.ShutdownTimeout > 0 {
		log.Printf("waiting up to %s for running builds...", cfg.ShutdownTimeout)
		drainCtx, drainCancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
		scheduler.Drain(drainCtx)
		drainCancel()
	}
	stopWorkers()
	waitCtx, waitCancel := context.WithTimeout(context.Background(), 5*time.Second)
	scheduler.Wait(waitCtx)
	waitCancel()
	log.Println("bye")
}

// migrateWorkspaceDirs 把 data/workspace/{id} 迁移为可读的任务名目录。
func migrateWorkspaceDirs(ctx context.Context, svc *service.PipelineService) {
	n, err := svc.MigrateWorkspaceDirs(ctx)
	if n > 0 {
		log.Printf("workspace dirs migrated: %d", n)
	}
	if err != nil {
		log.Printf("WARN: 部分工作区目录名迁移失败（下次启动会重试）: %v", err)
	}
}

// migrateWorkspacePolicy 只执行一次；标记文件存在后，用户重新勾选“清空工作区”不会被再次覆盖。
func migrateWorkspacePolicy(ctx context.Context, dataHome string, svc *service.PipelineService) {
	marker := filepath.Join(dataHome, ".workspace-reuse-v1")
	if _, err := os.Stat(marker); err == nil {
		return
	} else if !errors.Is(err, os.ErrNotExist) {
		log.Printf("WARN: 检查工作区迁移标记失败: %v", err)
		return
	}
	n, err := svc.MigrateWorkspacePolicy(ctx)
	if err != nil {
		log.Printf("WARN: 迁移工作区策略失败: %v", err)
		return
	}
	if n > 0 {
		log.Printf("workspace policy migrated: %d Git pipeline(s) now reuse workspace", n)
	}
	if err := os.WriteFile(marker, []byte(time.Now().UTC().Format(time.RFC3339)+"\n"), 0o644); err != nil {
		log.Printf("WARN: 写入工作区迁移标记失败: %v", err)
	}
}

// migrateArtifactConfig 只执行一次；能从 go build -o / jar cf 推断出产物时，
// 自动为旧任务启用归档，避免升级后仍只保留日志。之后用户可自由关闭。
func migrateArtifactConfig(ctx context.Context, dataHome string, svc *service.PipelineService) {
	marker := filepath.Join(dataHome, ".artifact-config-v1")
	if _, err := os.Stat(marker); err == nil {
		return
	} else if !errors.Is(err, os.ErrNotExist) {
		log.Printf("WARN: 检查产物归档迁移标记失败: %v", err)
		return
	}
	n, err := svc.MigrateArtifactConfig(ctx)
	if err != nil {
		log.Printf("WARN: 迁移产物归档配置失败: %v", err)
		return
	}
	if n > 0 {
		log.Printf("artifact config migrated: %d pipeline(s)", n)
	}
	if err := os.WriteFile(marker, []byte(time.Now().UTC().Format(time.RFC3339)+"\n"), 0o644); err != nil {
		log.Printf("WARN: 写入产物归档迁移标记失败: %v", err)
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
	}
}

// defaultToolCategories 预置常用工具类别，对应 Jenkins「全局工具配置」。
// 平台直接使用服务器上已安装的工具，安装项填写工具主目录（JAVA_HOME / GOROOT 等）。
func defaultToolCategories() []model.ToolCategory {
	return []model.ToolCategory{
		{
			ID:    "jdk",
			Title: "JDK 安装",
			Hint:  "填写服务器上的 JDK 主目录（JAVA_HOME），如 C:/Program Files/Java/jdk-17",
			Installations: []model.ToolInstallation{
				{Name: "JDK 17"},
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
			Hint:  "填写服务器上的 Go 主目录（GOROOT），如 C:/Program Files/Go",
			Installations: []model.ToolInstallation{
				{Name: "Go"},
			},
		},
		{
			ID:            "gradle",
			Title:         "Gradle 安装",
			Hint:          "填写服务器上的 Gradle 主目录（GRADLE_HOME）",
			Installations: []model.ToolInstallation{},
		},
		{
			ID:            "ant",
			Title:         "Ant 安装",
			Hint:          "填写服务器上的 Ant 主目录（ANT_HOME）",
			Installations: []model.ToolInstallation{},
		},
		{
			ID:    "maven",
			Title: "Maven 安装",
			Hint:  "填写服务器上的 Maven 主目录（MAVEN_HOME）",
			Installations: []model.ToolInstallation{
				{Name: "Maven"},
			},
		},
		{
			ID:    "nodejs",
			Title: "NodeJS 安装",
			Hint:  "填写服务器上的 Node 主目录（NODE_HOME），如 C:/Program Files/nodejs",
			Installations: []model.ToolInstallation{
				{Name: "NodeJS"},
			},
		},
	}
}

// defaultMavenSettings 提供最小的 settings.xml 样板，对应 Jenkins「Maven 配置」。
func defaultMavenSettings() model.MavenSettings {
	return model.MavenSettings{
		DefaultSettings: "<settings xmlns=\"http://maven.apache.org/SETTINGS/1.0.0\">\n  <mirrors/>\n</settings>\n",
	}
}

// progName 返回可执行文件名，仅用于拼装帮助提示，不参与业务流程。
func progName() string {
	if len(os.Args) == 0 || os.Args[0] == "" {
		return "ci-platform"
	}
	return filepath.Base(os.Args[0])
}

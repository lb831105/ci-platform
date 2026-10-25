package service

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"ci-platform/internal/config"
	"ci-platform/internal/executor"
	"ci-platform/internal/model"
	"ci-platform/internal/pkg"
	"ci-platform/internal/pkg/logger"
)

// SystemService 汇总「系统管理」所需的只读信息与运维动作。
type SystemService struct {
	cfg       *config.Config
	settings  *SettingsService
	pipelines PipelineStore
	builds    *BuildService
	scheduler *Scheduler
	logbuf    *pkg.LogBuffer
	build     pkg.BuildInfo
	audit     *AuditService
	startedAt time.Time
}

// NewSystemService 构造系统管理服务。
func NewSystemService(
	cfg *config.Config,
	settings *SettingsService,
	pipelines PipelineStore,
	builds *BuildService,
	scheduler *Scheduler,
	logbuf *pkg.LogBuffer,
	build pkg.BuildInfo,
	audit *AuditService,
) *SystemService {
	return &SystemService{
		cfg:       cfg,
		settings:  settings,
		pipelines: pipelines,
		builds:    builds,
		scheduler: scheduler,
		logbuf:    logbuf,
		build:     build,
		audit:     audit,
		startedAt: time.Now(),
	}
}

// AuditEntries 返回最近 limit 条操作审计记录（时间倒序）。
func (s *SystemService) AuditEntries(limit int) ([]model.AuditEntry, error) {
	if s.audit == nil {
		return []model.AuditEntry{}, nil
	}
	return s.audit.Entries(limit)
}

// Info 返回系统环境信息。
func (s *SystemService) Info(ctx context.Context) (model.SystemInfo, error) {
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)

	pipelines, err := s.pipelines.List(ctx)
	if err != nil {
		return model.SystemInfo{}, err
	}
	all, err := s.builds.AllBuilds(ctx, pipelines)
	if err != nil {
		return model.SystemInfo{}, err
	}
	running := 0
	for _, item := range all {
		// 待审批也是「在途」构建（占队列语义上的位置），统计进在途数量不算错。
		if item.Status == model.BuildRunning || item.Status == model.BuildPending || item.Status == model.BuildPendingApproval {
			running++
		}
	}

	settings := s.settings.Current()

	return model.SystemInfo{
		AppName:       settings.DisplayName,
		Version:       s.build.Version,
		Commit:        s.build.Commit,
		BuildDate:     s.build.Date,
		GoVersion:     runtime.Version(),
		OS:            runtime.GOOS,
		Arch:          runtime.GOARCH,
		CPUs:          runtime.NumCPU(),
		Goroutines:    runtime.NumGoroutine(),
		MemoryAllocMB: bytesToMB(mem.Alloc),
		MemorySysMB:   bytesToMB(mem.Sys),
		UptimeSeconds: int64(time.Since(s.startedAt).Seconds()),
		DataHome:      s.cfg.DataHome,
		JobsDir:       s.cfg.JobsDir(),
		WorkspaceDir:  s.cfg.WorkspaceDir(),
		Workers:       s.scheduler.Workers(),
		QueueCapacity: s.scheduler.QueueCapacity(),
		QueueLength:   s.scheduler.QueueLength(),
		QuietMode:     s.scheduler.IsQuiet(),
		AuthEnabled:   settings.AuthEnabled,
		PipelineCount: len(pipelines),
		BuildCount:    len(all),
		RunningBuilds: running,
		// 启动参数以「实际生效值」为准：settings.json 里的副本可能因换过启动参数而过期。
		WorkspaceTTLMinutes:  int(s.cfg.WorkspaceTTL.Minutes()),
		CleanIntervalMinutes: int(s.cfg.CleanInterval.Minutes()),
		ServerTime:           time.Now().UTC(),
	}, nil
}

// Load 返回负载统计。
func (s *SystemService) Load(ctx context.Context) (model.LoadStats, error) {
	pipelines, err := s.pipelines.List(ctx)
	if err != nil {
		return model.LoadStats{}, err
	}
	all, err := s.builds.AllBuilds(ctx, pipelines)
	if err != nil {
		return model.LoadStats{}, err
	}

	stats := model.LoadStats{
		PipelineCount: len(pipelines),
		BuildCount:    len(all),
		QueueLength:   s.scheduler.QueueLength(),
		QueueCapacity: s.scheduler.QueueCapacity(),
	}
	var finished int
	var totalDuration int64
	for _, item := range all {
		switch item.Status {
		case model.BuildSuccess:
			stats.SuccessBuilds++
		case model.BuildFailed:
			stats.FailedBuilds++
		case model.BuildRunning:
			stats.RunningBuilds++
		case model.BuildPending, model.BuildPendingApproval:
			stats.PendingBuilds++
		}
		if item.Status == model.BuildSuccess || item.Status == model.BuildFailed {
			if ms := item.Duration().Milliseconds(); ms > 0 {
				finished++
				totalDuration += ms
			}
		}
	}
	if finished > 0 {
		stats.AverageDurationMS = totalDuration / int64(finished)
	}

	stats.WorkspaceCount, stats.WorkspaceBytes = dirUsage(s.cfg.WorkspaceDir())
	_, stats.JobsBytes = dirUsage(s.cfg.JobsDir())
	return stats, nil
}

// LogLines 返回最近 n 行系统日志。
func (s *SystemService) LogLines(n int) []string {
	return s.logbuf.Lines(n)
}

// LogEntries 返回带级别的结构化日志，供「系统日志」页按级别过滤。
func (s *SystemService) LogEntries(n int) []pkg.LogEntry {
	return s.logbuf.Entries(n)
}

// LogFile 返回当前正在写入的日志文件与保留策略，便于运维直接去服务器上看。
func (s *SystemService) LogFile() map[string]interface{} {
	return map[string]interface{}{
		"file":          logger.CurrentFile(),
		"level":         logger.LevelOf().String(),
		"retain_months": s.cfg.LogRetainMonths,
	}
}

// Settings 返回当前系统配置副本。
func (s *SystemService) Settings() model.Settings {
	return s.settings.Current()
}

// UpdateSettings 持久化配置并同步影响运行时的开关（静默模式）。
func (s *SystemService) UpdateSettings(in model.Settings) (model.Settings, error) {
	out, err := s.settings.Update(in)
	if err != nil {
		return model.Settings{}, err
	}
	s.scheduler.SetQuiet(out.QuietMode)
	return out, nil
}

// ReloadSettings 从磁盘重新读取配置并同步静默开关。
func (s *SystemService) ReloadSettings() (model.Settings, error) {
	out, err := s.settings.Reload()
	if err != nil {
		return model.Settings{}, err
	}
	s.scheduler.SetQuiet(out.QuietMode)
	return out, nil
}

// ClearLog 清空系统日志缓冲。
func (s *SystemService) ClearLog() {
	s.logbuf.Clear()
}

// SendTestEmail 使用当前 SMTP 配置发送一封测试邮件（「邮件通知」页的测试按钮）。
func (s *SystemService) SendTestEmail(to string) error {
	cfg := effectiveEmail(s.settings.Current())
	if cfg.SMTPHost == "" {
		return pkg.ErrBadRequest("请先填写 SMTP 服务器")
	}
	recipients := parseRecipients(to, cfg.DefaultSuffix)
	if len(recipients) == 0 {
		return pkg.ErrBadRequest("请填写测试收件人")
	}
	return executor.SendMail(cfg, recipients, "CI Platform 测试邮件", "这是一封来自 CI Platform 的测试邮件。\n")
}

// SendTestWebhookNotify 使用当前 Webhook 通知配置推送一条测试消息。
func (s *SystemService) SendTestWebhookNotify() error {
	cfg := s.settings.Current().WebhookNotify
	if !cfg.Enabled {
		return pkg.ErrBadRequest("请先启用 Webhook 通知")
	}
	p := &model.Pipeline{ID: "test", Name: "测试任务"}
	b := &model.Build{ID: "0", PipelineID: "test", Status: model.BuildSuccess, TriggerType: model.TriggerManual, TriggeredBy: "admin"}
	now := time.Now().UTC()
	b.StartedAt = &now
	b.FinishedAt = &now
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return executor.SendWebhookNotify(ctx, cfg, p, b, s.settings.Current().JenkinsURL)
}

// Features 返回平台内置能力清单（对应「插件管理」中的已安装组件）。
func (s *SystemService) Features() []model.Feature {
	return []model.Feature{
		{Name: "local-executor", Version: "builtin", Status: "active", Description: "直接在服务器上执行构建步骤，使用已安装的 Go / Maven / Node / JDK"},
		{Name: "git-scm", Version: "builtin", Status: "active", Description: "Git 仓库拉取（密钥 / 匿名）"},
		{Name: "publish-over-ssh", Version: "builtin", Status: "active", Description: "构建产物通过 SSH / SFTP 发布"},
		{Name: "webhook-trigger", Version: "builtin", Status: "active", Description: "通过 Webhook 触发构建（密钥校验）"},
		{Name: "websocket-console", Version: "builtin", Status: "active", Description: "实时控制台日志（WebSocket 流式推送）"},
		{Name: "file-store", Version: "builtin", Status: "active", Description: "纯文件系统存储，零数据库依赖"},
		{Name: "credential-store", Version: "builtin", Status: "active", Description: "凭据管理（用户名密码 / 密钥 / 私钥）"},
		{Name: "scheduler", Version: "builtin", Status: "active", Description: "构建队列与 worker 池调度"},
	}
}

// ScanOldData 扫描可清理的旧数据：过期工作区与孤儿任务目录。
func (s *SystemService) ScanOldData(ctx context.Context) (model.OldDataReport, error) {
	known := map[string]bool{}
	pipelines, err := s.pipelines.List(ctx)
	if err != nil {
		return model.OldDataReport{}, err
	}
	for _, p := range pipelines {
		known[p.ID] = true
	}
	// 仍存在的任务其工作区必须保留（增量构建缓存）；只把无主目录列为可清理项，
	// 避免“清理旧数据”误删正在使用的工作区。
	keepDirs := existingWorkspaceDirs(pipelines)

	report := model.OldDataReport{Items: []model.OldDataItem{}, ScannedAt: time.Now().UTC()}
	for _, name := range listSubDirs(s.cfg.WorkspaceDir()) {
		if keepDirs[strings.ToLower(name)] {
			continue
		}
		size := dirSize(filepath.Join(s.cfg.WorkspaceDir(), name))
		report.Items = append(report.Items, model.OldDataItem{
			Kind:   "workspace",
			Path:   filepath.Join(s.cfg.WorkspaceDir(), name),
			Bytes:  size,
			Detail: "无对应任务的孤儿工作区",
		})
		report.TotalBytes += size
	}
	for _, name := range listSubDirs(s.cfg.JobsDir()) {
		if known[name] {
			continue
		}
		path := filepath.Join(s.cfg.JobsDir(), name)
		size := dirSize(path)
		report.Items = append(report.Items, model.OldDataItem{
			Kind:   "orphan_job",
			Path:   path,
			Bytes:  size,
			Detail: "无对应任务的孤儿目录",
		})
		report.TotalBytes += size
	}
	return report, nil
}

// CleanOldData 删除全部工作区与孤儿任务目录，返回清理后的扫描结果。
func (s *SystemService) CleanOldData(ctx context.Context) (model.OldDataReport, error) {
	known := map[string]bool{}
	pipelines, err := s.pipelines.List(ctx)
	if err != nil {
		return model.OldDataReport{}, err
	}
	for _, p := range pipelines {
		known[p.ID] = true
	}
	keepDirs := existingWorkspaceDirs(pipelines)
	for _, name := range listSubDirs(s.cfg.WorkspaceDir()) {
		if keepDirs[strings.ToLower(name)] {
			continue
		}
		_ = pkg.RemoveAll(filepath.Join(s.cfg.WorkspaceDir(), name))
	}
	for _, name := range listSubDirs(s.cfg.JobsDir()) {
		if !known[name] {
			_ = pkg.RemoveAll(filepath.Join(s.cfg.JobsDir(), name))
		}
	}
	return s.ScanOldData(ctx)
}

// existingWorkspaceDirs 返回仍存在任务使用的工作区目录（大小写不敏感），
// 兼容尚未迁移、WorkspaceDir 为空的旧任务（回退到 ID）。
func existingWorkspaceDirs(pipelines []*model.Pipeline) map[string]bool {
	dirs := make(map[string]bool, len(pipelines))
	for _, p := range pipelines {
		dir := strings.TrimSpace(p.WorkspaceDir)
		if dir == "" {
			dir = p.ID
		}
		if dir != "" {
			dirs[strings.ToLower(dir)] = true
		}
	}
	return dirs
}

// DataHome 返回数据目录的生效值与待生效配置。
func (s *SystemService) DataHome() model.DataHomeInfo {
	effective := absPath(s.cfg.DataHome)
	configured := effective
	if b, err := config.LoadBootstrap(s.cfg.ConfigFile); err == nil && b != nil && b.DataHome != "" {
		configured = absPath(b.DataHome)
	}
	return model.DataHomeInfo{
		DataHome:           effective,
		ConfiguredDataHome: configured,
		ConfigFile:         absPath(s.cfg.ConfigFile),
		JobsDir:            absPath(s.cfg.JobsDir()),
		WorkspaceDir:       absPath(s.cfg.WorkspaceDir()),
		Explicit:           s.cfg.DataHomeSet,
		RestartRequired:    configured != effective,
	}
}

// UpdateDataHome 写入引导配置，指定新的数据目录（重启后生效）。
// migrate=true 时把现有 jobs / credentials / settings.json 复制到新目录。
func (s *SystemService) UpdateDataHome(ctx context.Context, newHome string, migrate bool) (model.DataHomeInfo, error) {
	newHome = strings.TrimSpace(newHome)
	if newHome == "" {
		return model.DataHomeInfo{}, pkg.ErrBadRequest("数据目录不能为空")
	}
	target := absPath(newHome)
	current := absPath(s.cfg.DataHome)

	if target != current {
		// 目标目录不能与当前数据目录互相嵌套：往子目录迁移会把 jobs 复制进自身，
		// 往父目录迁移可能让 src/dst 指向同一文件并截断源数据。
		if pkg.WithinDir(current, target) || pkg.WithinDir(target, current) {
			return model.DataHomeInfo{}, pkg.ErrBadRequest("数据目录不能与当前目录互相嵌套：%s", target)
		}
		if err := pkg.EnsureDir(target); err != nil {
			return model.DataHomeInfo{}, err
		}
		if migrate {
			if hasDataFiles(target) {
				return model.DataHomeInfo{}, pkg.ErrConflict("目标目录已包含数据（settings.json / jobs / credentials），请换一个空目录或取消迁移")
			}
			if err := copyDataEntries(ctx, current, target); err != nil {
				return model.DataHomeInfo{}, err
			}
		}
	}
	if err := config.SaveBootstrap(s.cfg.ConfigFile, config.Bootstrap{DataHome: target}); err != nil {
		return model.DataHomeInfo{}, pkg.Wrap(err, "写入引导配置")
	}
	return s.DataHome(), nil
}

// absPath 返回清理后的绝对路径，便于比较。
func absPath(p string) string {
	if a, err := filepath.Abs(p); err == nil {
		return filepath.Clean(a)
	}
	return filepath.Clean(p)
}

// hasDataFiles 判断目录是否已有需要保护的数据。
func hasDataFiles(dir string) bool {
	if pkg.Exists(filepath.Join(dir, "settings.json")) {
		return true
	}
	for _, sub := range []string{"jobs", "credentials"} {
		entries, err := os.ReadDir(filepath.Join(dir, sub))
		if err == nil && len(entries) > 0 {
			return true
		}
	}
	return false
}

// copyDataEntries 复制数据目录中的元数据与构建记录（不包含临时工作区）。
func copyDataEntries(ctx context.Context, src, dst string) error {
	for _, name := range []string{
		"settings.json",
		"rbac.json",
		"views.json",
		"credentials",
		"jobs",
		".workspace-reuse-v1",
		".artifact-config-v1",
	} {
		if err := ctx.Err(); err != nil {
			return err
		}
		srcPath := filepath.Join(src, name)
		if !pkg.Exists(srcPath) {
			continue
		}
		if err := copyPath(srcPath, filepath.Join(dst, name)); err != nil {
			return fmt.Errorf("迁移 %s: %w", name, err)
		}
	}
	return nil
}

// copyPath 递归复制文件或目录。
func copyPath(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	if info.IsDir() {
		if err = os.MkdirAll(dst, 0o755); err != nil {
			return err
		}
		entries, err := os.ReadDir(src)
		if err != nil {
			return err
		}
		for _, e := range entries {
			if err = copyPath(filepath.Join(src, e.Name()), filepath.Join(dst, e.Name())); err != nil {
				return err
			}
		}
		return nil
	}
	if err = os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, info.Mode())
	if err != nil {
		return err
	}
	if _, err = io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// dirUsage 返回目录下子目录数量与总字节数。
func dirUsage(root string) (int, int64) {
	names := listSubDirs(root)
	var total int64
	for _, name := range names {
		total += dirSize(filepath.Join(root, name))
	}
	return len(names), total
}

// listSubDirs 返回目录下子目录名，目录不存在时返回空。
func listSubDirs(root string) []string {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		// 跳过隐藏目录（如 Git 更新期间临时保存构建产物的 .ci-untracked-*）。
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			out = append(out, e.Name())
		}
	}
	return out
}

// dirSize 递归统计目录字节数。
func dirSize(root string) int64 {
	var total int64
	_ = filepath.WalkDir(root, func(_ string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if info, statErr := d.Info(); statErr == nil {
			total += info.Size()
		}
		return nil
	})
	return total
}

func bytesToMB(b uint64) float64 {
	return float64(b) / (1024 * 1024)
}

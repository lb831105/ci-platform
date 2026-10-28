package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"ci-platform/internal/model"
	"ci-platform/internal/pkg"
)

// ExportVersion 是「平台配置文档」的格式版本。导入时校验：
// 未来格式变更时旧版本服务能给出明确提示，而不是解析出半份配置。
const ExportVersion = 1

// SshPublishExport 在持久化字段之外附带 SSH 服务器名称。
// 跨实例导入时 server_id 无意义（另一个实例的 ID 不同），
// 名字才是可对齐的标识——这与 Jenkins 用 configName 引用服务器是同一思路。
type SshPublishExport struct {
	model.SshPublishConfig
	ServerName string `json:"server_name,omitempty"`
}

// PipelineExport 是任务的「可移植配置」文档（对应 Jenkins 的 config.xml）。
//
// 与 config.json 的关系：字段一一对应，但
//   - 不含任何密钥（SSH 密码、Webhook secret、群机器人 URL 里的 token）；
//   - 实例相关的 ID 附带名称提示（server_name / git_credential_name）；
//   - 顶部有 export_version，便于导入端判断兼容性。
//
// 之所以不直接吐 config.json：那份文件里有内联 SSH 密码与 Webhook 密钥，
// 导出即等于把密钥写进「可以被随意转发」的文件里。
type PipelineExport struct {
	ExportVersion int       `json:"export_version"`
	ExportedAt    time.Time `json:"exported_at"`
	ExportedFrom  string    `json:"exported_from,omitempty"` // 平台版本，便于排查

	Name        string `json:"name"`
	Description string `json:"description,omitempty"`

	Steps []model.Step `json:"steps"`

	GitURL            string `json:"git_url,omitempty"`
	GitBranch         string `json:"git_branch"`
	GitCredentialName string `json:"git_credential_name,omitempty"` // 凭据按名字对齐

	CronSpec string           `json:"cron_spec,omitempty"`
	Params   []model.ParamDef `json:"params,omitempty"`

	// Webhook 触发器策略：分支/事件白名单不涉及密钥，可随文档走。
	WebhookBranchMode string   `json:"webhook_branch_mode,omitempty"`
	WebhookBranches   []string `json:"webhook_branches,omitempty"`
	WebhookEvents     []string `json:"webhook_events,omitempty"`
	// CommitStatus 里的凭据只带名字（跨实例 ID 不同）。
	CommitStatus *CommitStatusExport `json:"commit_status,omitempty"`

	SshPublishes []SshPublishExport `json:"ssh_publishes,omitempty"`

	Artifacts model.ArtifactConfig `json:"artifacts"`

	// 发布审批：安全门禁必须随任务一起迁移，否则导入后生产发布会被静默放开。
	RequireApproval        bool `json:"require_approval"`
	ApprovalTimeoutMinutes int  `json:"approval_timeout_minutes"`

	BuildRetentionMaxPerJob    int   `json:"build_retention_max_per_job"`
	BuildRetentionMaxAgeDays   int   `json:"build_retention_max_age_days"`
	DeleteWorkspaceBeforeBuild *bool `json:"delete_workspace_before_build,omitempty"`
	// ToolSelections 让每个任务选自己的工具版本（JDK/NodeJS/Maven…），
	// 搬迁到另一实例时按「安装项名称」对齐，缺失的会在导入告警里指出。
	ToolSelections map[string]string `json:"tool_selections,omitempty"`

	// Notify 已脱敏：群机器人 URL 被清空（token 不落导出文件）。
	Notify *model.JobNotify `json:"notify,omitempty"`

	// Warnings 说明导出时被清空/降级的内容，导入端与用户都需要知道。
	Warnings []pkg.Warning `json:"warnings,omitempty"`
}

// CommitStatusExport 是「构建状态回写」的可搬运形式：凭据按名字对齐。
type CommitStatusExport struct {
	Enabled        bool   `json:"enabled"`
	Project        string `json:"project,omitempty"`
	CredentialName string `json:"credential_name,omitempty"`
}

// ImportOptions 是平台配置导入的参数。
type ImportOptions struct {
	Name   string // 覆盖文档里的任务名（可选）
	Mode   string // create / update / skip
	DryRun bool
}

// ImportOutcome 是导入结果。
type ImportOutcome struct {
	Pipeline *model.Pipeline
	Warnings []pkg.Warning
	Action   string
}

// CredentialLister 只需要「按 ID 取名字」，绝不能把密钥带到导出文档里。
type CredentialLister interface {
	List(ctx context.Context) ([]model.Credential, error)
}

// PortabilityService 负责任务的导出 / 导入（平台自有 JSON 格式）。
type PortabilityService struct {
	pipelines   *PipelineService
	settings    SettingsProvider
	credentials CredentialLister
	version     string
}

// NewPortabilityService 创建导入导出服务。
// credentials 可为 nil（此时不输出凭据名称提示，导入时也不会按名解析凭据）。
func NewPortabilityService(pipelines *PipelineService, settings SettingsProvider, credentials CredentialLister, version string) *PortabilityService {
	return &PortabilityService{pipelines: pipelines, settings: settings, credentials: credentials, version: version}
}

// Export 把任务导出为可移植配置文档。
// 只读快照：不修改 store 里的任务对象（脱敏必须作用在副本上）。
func (s *PortabilityService) Export(ctx context.Context, pipelineID string) (*PipelineExport, error) {
	p, err := s.pipelines.Get(ctx, pipelineID)
	if err != nil {
		return nil, err
	}
	var warnings []pkg.Warning
	add := func(level, field, format string, args ...interface{}) {
		warnings = append(warnings, pkg.Warning{Field: field, Level: level, Message: fmt.Sprintf(format, args...)})
	}

	doc := &PipelineExport{
		ExportVersion: ExportVersion,
		ExportedAt:    time.Now().UTC(),
		ExportedFrom:  s.version,
		Name:          p.Name,
		Description:   p.Description,
		Steps:         append([]model.Step(nil), p.Steps...),
		GitURL:        p.GitURL,
		GitBranch:     p.EffectiveBranch(),
		CronSpec:      p.CronSpec,
		Params:        append([]model.ParamDef(nil), p.Params...),
		Artifacts: model.ArtifactConfig{
			Enabled:    p.Artifacts.Enabled,
			Files:      append([]string(nil), p.Artifacts.Files...),
			AllowEmpty: p.Artifacts.AllowEmpty,
		},
		RequireApproval:          p.RequireApproval,
		ApprovalTimeoutMinutes:   p.ApprovalTimeoutMinutes,
		BuildRetentionMaxPerJob:  p.BuildRetentionMaxPerJob,
		BuildRetentionMaxAgeDays: p.BuildRetentionMaxAgeDays,
		WebhookBranchMode:        p.WebhookBranchMode,
		WebhookBranches:          append([]string(nil), p.WebhookBranches...),
		WebhookEvents:            append([]string(nil), p.WebhookEvents...),
	}
	if p.CommitStatus.Enabled || strings.TrimSpace(p.CommitStatus.Project) != "" {
		status := &CommitStatusExport{Enabled: p.CommitStatus.Enabled, Project: p.CommitStatus.Project}
		if p.CommitStatus.CredentialID != "" {
			if name := s.credentialName(ctx, p.CommitStatus.CredentialID); name != "" {
				status.CredentialName = name
			} else {
				add(pkg.LevelWarn, "commit_status",
					"构建状态回写的凭据 %s 未能解析出名称，导入后需要重新选择", p.CommitStatus.CredentialID)
			}
		}
		doc.CommitStatus = status
	}
	if len(p.ToolSelections) > 0 {
		doc.ToolSelections = make(map[string]string, len(p.ToolSelections))
		for key, value := range p.ToolSelections {
			doc.ToolSelections[key] = value
		}
	}
	if p.DeleteWorkspaceBeforeBuild != nil {
		keep := *p.DeleteWorkspaceBeforeBuild
		doc.DeleteWorkspaceBeforeBuild = &keep
	}

	// 凭据：只导出名字。没有名字提示时，导入端无法自动对齐。
	if p.GitCredential != "" {
		if name := s.credentialName(ctx, p.GitCredential); name != "" {
			doc.GitCredentialName = name
		} else {
			add(pkg.LevelWarn, "git_credential",
				"Git 凭据 %s 未能解析出名称，导入后需要重新选择凭据", p.GitCredential)
		}
	}

	// Webhook 密钥：导出文件可能被随意转发，绝不包含密钥。
	if strings.TrimSpace(p.WebhookSecret) != "" {
		add(pkg.LevelInfo, "webhook_secret",
			"Webhook 密钥不在导出文件中（避免随文件泄露），导入后请在任务配置里重新生成")
	}

	serverNames := s.sshServerNames()
	for _, cfg := range p.SshPublishes {
		item := SshPublishExport{SshPublishConfig: cfg}
		item.Password = ""
		if cfg.ServerID != "" {
			item.ServerName = serverNames[strings.ToLower(cfg.ServerID)]
			if item.ServerName == "" {
				add(pkg.LevelWarn, "ssh", "发布目标 %q 引用的 SSH 服务器 %s 已不存在，导入后需重新选择", cfg.Name, cfg.ServerID)
			}
		} else if cfg.Host != "" || cfg.Username != "" {
			// 内联主机配置（旧版）带着密码才能用；密码不导出，因此导出为停用状态，
			// 让导入端明确「需要人工补全」，而不是埋一个必定失败的启用目标。
			if item.Enabled {
				item.Enabled = false
			}
			add(pkg.LevelWarn, "ssh",
				"发布目标 %q 使用内联 SSH 账号（密码不导出），已导出为停用状态，请在目标环境改用「系统配置 → SSH 服务器」",
				cfg.Name)
		}
		doc.SshPublishes = append(doc.SshPublishes, item)
	}

	// 通知：群机器人 URL 内含 token（Slack 还在路径尾段），导出时清空。
	if p.Notify != nil {
		n := *p.Notify
		hadURL := false
		n.Targets, hadURL = stripNotifyURLs(p.Notify.Targets, hadURL)
		if p.Notify.Escalation != nil {
			esc := *p.Notify.Escalation
			esc.Targets, hadURL = stripNotifyURLs(p.Notify.Escalation.Targets, hadURL)
			n.Escalation = &esc
		}
		if len(p.Notify.Routes) > 0 {
			routes := make([]model.NotifyRoute, len(p.Notify.Routes))
			copy(routes, p.Notify.Routes)
			for i := range routes {
				routes[i].Targets, hadURL = stripNotifyURLs(routes[i].Targets, hadURL)
			}
			n.Routes = routes
		}
		doc.Notify = &n
		if hadURL {
			add(pkg.LevelInfo, "notify",
				"群机器人地址（含 access_token）不在导出文件中，导入后需重新填写")
		}
	}
	doc.Warnings = warnings
	return doc, nil
}

// stripNotifyURLs 返回 URL 被清空的副本，并标记是否存在需要提示的地址。
func stripNotifyURLs(in []model.NotifyTarget, had bool) ([]model.NotifyTarget, bool) {
	if len(in) == 0 {
		return nil, had
	}
	out := make([]model.NotifyTarget, len(in))
	copy(out, in)
	for i := range out {
		if strings.TrimSpace(out[i].URL) != "" {
			had = true
		}
		out[i].URL = ""
	}
	return out, had
}

// Import 解析导出的 JSON 文档（也接受原生 config.json 结构）并创建/更新任务。
//
// 兼容两种输入：
//   - 平台导出文档：带 export_version，ssh_publishes 里有 server_name、git_credential_name
//   - 原生 config.json：没有版本号，按模型反序列化（server_id 直接可用）
//
// 安全约束：文档里可能残留 *** 脱敏标记（例如手工改过的副本），
// 一律清空并告警——把 *** 当成真实值写进配置会让构建静默失败。
func (s *PortabilityService) Import(ctx context.Context, doc []byte, opts ImportOptions) (*ImportOutcome, error) {
	exported, raw, decodeWarnings, err := s.decode(doc)
	if err != nil {
		return nil, err
	}

	warnings := decodeWarnings
	add := func(level, field, format string, args ...interface{}) {
		warnings = append(warnings, pkg.Warning{Field: field, Level: level, Message: fmt.Sprintf(format, args...)})
	}
	// 导出文档自身的告警（导出时清空的内容）也要透传，用户才能知道丢了什么。
	warnings = append(warnings, exported.Warnings...)

	name := strings.TrimSpace(opts.Name)
	if name == "" {
		name = strings.TrimSpace(exported.Name)
	}
	if name == "" {
		name = strings.TrimSpace(raw.Name)
	}
	if name == "" {
		return nil, pkg.ErrBadRequest("配置文档缺少任务名（可用 name 参数覆盖）")
	}

	mode := strings.ToLower(strings.TrimSpace(opts.Mode))
	if mode == "" {
		mode = "create"
	}
	switch mode {
	case "create", "update", "skip":
	default:
		return nil, pkg.ErrBadRequest("mode 只能是 create / update / skip")
	}

	pipeline, err := s.toPipeline(ctx, exported, raw, add)
	if err != nil {
		return nil, err
	}
	pipeline.Name = name

	// 与创建/更新走同一条校验路径：预览阶段即可暴露 cron/参数/SSH 问题。
	if err := s.pipelines.Validate(ctx, pipeline); err != nil {
		return nil, err
	}
	outcome := &ImportOutcome{Pipeline: pipeline, Warnings: warnings, Action: "preview"}
	if opts.DryRun {
		return outcome, nil
	}

	existing, err := s.findByName(ctx, name)
	if err != nil {
		return nil, err
	}
	// 导入更新时保留现有审批门禁：旧文档可能没有 require_approval 字段，
	// 若直接按零值覆盖，生产发布会在迁移后被静默放开。需要关闭审批请走任务编辑接口。
	if existing != nil && mode == "update" && !pipeline.RequireApproval {
		pipeline.RequireApproval = existing.RequireApproval
		if pipeline.ApprovalTimeoutMinutes == 0 {
			pipeline.ApprovalTimeoutMinutes = existing.ApprovalTimeoutMinutes
		}
	}
	switch {
	case existing != nil && mode == "skip":
		outcome.Pipeline = existing
		outcome.Action = "skipped"
		outcome.Warnings = append(outcome.Warnings, pkg.Warning{
			Field: "name", Level: pkg.LevelInfo, Message: "同名任务已存在（mode=skip），未做任何修改",
		})
	case existing != nil && mode == "update":
		saved, err := s.pipelines.Update(ctx, existing.ID, pipeline)
		if err != nil {
			return nil, err
		}
		outcome.Pipeline = saved
		outcome.Action = "updated"
	default:
		if existing != nil {
			pipeline.Name = s.uniqueName(ctx, name)
			outcome.Warnings = append(outcome.Warnings, pkg.Warning{
				Field: "name", Level: pkg.LevelWarn, Message: "已存在同名任务，导入为 " + pipeline.Name,
			})
		}
		saved, err := s.pipelines.Create(ctx, pipeline)
		if err != nil {
			return nil, err
		}
		outcome.Pipeline = saved
		outcome.Action = "created"
	}
	return outcome, nil
}

// exportDoc 是解码后的导出文档（宽松结构：字段可缺省）。
type exportDoc PipelineExport

// rawDoc 是「原生 config.json」的宽松视图：只需要能取到版本号与基本字段。
type rawDoc struct {
	model.Pipeline
	ExportVersion int `json:"export_version"`
}

func (s *PortabilityService) decode(doc []byte) (*PipelineExport, *rawDoc, []pkg.Warning, error) {
	if len(bytes.TrimSpace(doc)) == 0 {
		return nil, nil, nil, pkg.ErrBadRequest("配置文档为空")
	}
	// 先按「对象」解析一次：json.Unmarshal 会拒绝尾随内容，
	// 防止一个文件里塞了两个文档却只导入第一个。
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(doc, &probe); err != nil {
		return nil, nil, nil, pkg.ErrBadRequest("配置文档解析失败：%v", err)
	}
	// 宽松解码两套结构：导出文档与原生 config.json 字段高度重合。
	// 未知字段不报错（跨版本导出时旧实例也要能导入），但会逐条列进告警。
	var exported exportDoc
	if err := json.Unmarshal(doc, &exported); err != nil {
		return nil, nil, nil, pkg.ErrBadRequest("配置文档解析失败：%v", err)
	}
	var raw rawDoc
	if err := json.Unmarshal(doc, &raw); err != nil {
		return nil, nil, nil, pkg.ErrBadRequest("配置文档解析失败：%v", err)
	}

	version := exported.ExportVersion
	if version == 0 {
		version = raw.ExportVersion
	}
	if version > ExportVersion {
		return nil, nil, nil, pkg.ErrBadRequest(
			"配置文档版本 %d 高于当前服务支持的版本 %d，请升级平台后再导入", version, ExportVersion)
	}

	var warnings []pkg.Warning
	for key := range probe {
		if knownDocFields[key] {
			continue
		}
		warnings = append(warnings, pkg.Warning{
			Field: key, Level: pkg.LevelWarn,
			Message: "配置文档字段 " + key + " 未被识别，已忽略（可能是更高版本的平台导出的）",
		})
	}
	exportedPtr := PipelineExport(exported)
	return &exportedPtr, &raw, warnings, nil
}

// knownDocFields 列出导入端认识的所有键：既包含导出文档，也包含原生 config.json
// （后者多出 id / 时间戳 / 工作区等实例字段——它们不参与导入，但不该报「未识别」）。
var knownDocFields = map[string]bool{
	// 导出文档
	"export_version": true, "exported_at": true, "exported_from": true,
	"name": true, "description": true, "steps": true,
	"git_url": true, "git_branch": true, "git_credential_name": true,
	"cron_spec": true, "params": true, "ssh_publishes": true, "artifacts": true,
	"build_retention_max_per_job": true, "build_retention_max_age_days": true,
	"delete_workspace_before_build": true, "notify": true, "warnings": true,
	"tool_selections":     true,
	"webhook_branch_mode": true, "webhook_branches": true, "webhook_events": true,
	"commit_status": true,
	// 原生 config.json 里的触发器/审批字段（导入时按需读取）
	"require_approval": true, "approval_timeout_minutes": true,
	// 原生 config.json 的实例字段（导入时忽略）
	"id": true, "created_at": true, "updated_at": true, "workspace_dir": true,
	"executor": true, "image": true, "git_credential_id": true, "ssh_publish": true,
	"webhook_secret": true,
}

// toPipeline 合并导出文档与原生文档，产出最终任务定义。
func (s *PortabilityService) toPipeline(ctx context.Context, exported *PipelineExport, raw *rawDoc, add func(level, field, format string, args ...interface{})) (*model.Pipeline, error) {
	p := &model.Pipeline{GitBranch: "main"}
	// 原生 config.json 优先：它没有「导出时清空」的语义，字段就是配置本身。
	p.Name = firstNonEmpty(exported.Name, raw.Name)
	p.Description = firstNonEmpty(exported.Description, raw.Description)
	p.Steps = exported.Steps
	if len(p.Steps) == 0 {
		p.Steps = raw.Steps
	}
	p.GitURL = firstNonEmpty(exported.GitURL, raw.GitURL)
	p.GitBranch = firstNonEmpty(exported.GitBranch, raw.GitBranch, "main")
	p.CronSpec = firstNonEmpty(exported.CronSpec, raw.CronSpec)
	p.Params = exported.Params
	if len(p.Params) == 0 {
		p.Params = raw.Params
	}
	if exported.Artifacts.Enabled || len(exported.Artifacts.Files) > 0 || exported.Artifacts.AllowEmpty {
		p.Artifacts = exported.Artifacts
	} else {
		p.Artifacts = raw.Artifacts
	}
	// allow_empty 是独立开关：旧文档没有该字段时从原生配置补齐。
	p.Artifacts.AllowEmpty = exported.Artifacts.AllowEmpty || raw.Artifacts.AllowEmpty
	p.WebhookBranchMode = firstNonEmpty(exported.WebhookBranchMode, raw.WebhookBranchMode)
	if len(exported.WebhookBranches) > 0 {
		p.WebhookBranches = append([]string(nil), exported.WebhookBranches...)
	} else {
		p.WebhookBranches = append([]string(nil), raw.WebhookBranches...)
	}
	if len(exported.WebhookEvents) > 0 {
		p.WebhookEvents = append([]string(nil), exported.WebhookEvents...)
	} else {
		p.WebhookEvents = append([]string(nil), raw.WebhookEvents...)
	}
	// 审批配置：导出文档与原生 config.json 都接受；任一侧开启即保留。
	p.RequireApproval = exported.RequireApproval || raw.RequireApproval
	p.ApprovalTimeoutMinutes = firstIntValue(exported.ApprovalTimeoutMinutes, raw.ApprovalTimeoutMinutes)
	p.BuildRetentionMaxPerJob = firstIntValue(exported.BuildRetentionMaxPerJob, raw.BuildRetentionMaxPerJob)
	p.BuildRetentionMaxAgeDays = firstIntValue(exported.BuildRetentionMaxAgeDays, raw.BuildRetentionMaxAgeDays)
	if len(exported.ToolSelections) > 0 {
		p.ToolSelections = exported.ToolSelections
	} else if len(raw.ToolSelections) > 0 {
		p.ToolSelections = raw.ToolSelections
	}
	if exported.DeleteWorkspaceBeforeBuild != nil {
		p.DeleteWorkspaceBeforeBuild = exported.DeleteWorkspaceBeforeBuild
	} else {
		p.DeleteWorkspaceBeforeBuild = raw.DeleteWorkspaceBeforeBuild
	}
	if len(p.Steps) == 0 {
		return nil, pkg.ErrBadRequest("配置文档没有构建步骤（steps）")
	}
	if p.Artifacts.Files != nil {
		p.Artifacts.Files = append([]string(nil), p.Artifacts.Files...)
	}

	// 凭据：按名字对齐（跨实例 ID 不同）。
	if credID := strings.TrimSpace(raw.GitCredential); credID != "" {
		if s.credentialExists(ctx, credID) {
			p.GitCredential = credID
		} else {
			add(pkg.LevelWarn, "git_credential", "Git 凭据 %s 在当前实例不存在，已清空，请重新选择", credID)
		}
	} else if credentialName := strings.TrimSpace(exported.GitCredentialName); credentialName != "" {
		if id := s.credentialID(ctx, credentialName); id != "" {
			p.GitCredential = id
		} else {
			add(pkg.LevelError, "git_credential",
				"当前实例没有名为 %q 的凭据，构建将无法拉取代码；请先在「凭据管理」创建同名凭据", credentialName)
		}
	}

	// 构建状态回写：凭据按名字对齐。
	if exported.CommitStatus != nil {
		p.CommitStatus.Project = strings.TrimSpace(exported.CommitStatus.Project)
	}
	if exported.CommitStatus != nil && exported.CommitStatus.Enabled {
		p.CommitStatus.Enabled = true
		if name := strings.TrimSpace(exported.CommitStatus.CredentialName); name != "" {
			if id := s.credentialID(ctx, name); id != "" {
				p.CommitStatus.CredentialID = id
			} else {
				add(pkg.LevelWarn, "commit_status",
					"当前实例没有名为 %q 的凭据，构建状态回写已关闭；如需启用请先创建同名凭据", name)
				p.CommitStatus.Enabled = false
			}
		} else {
			add(pkg.LevelWarn, "commit_status", "文档未提供回写凭据名，构建状态回写已关闭")
			p.CommitStatus.Enabled = false
		}
	} else if raw.CommitStatus.Enabled {
		// 原生 config.json：同实例导入，凭据 ID 直接沿用（不存在时下划线报错）。
		p.CommitStatus = raw.CommitStatus
		if p.CommitStatus.CredentialID != "" && !s.credentialExists(ctx, p.CommitStatus.CredentialID) {
			add(pkg.LevelWarn, "commit_status", "构建状态回写的凭据在当前实例不存在，已关闭回写")
			p.CommitStatus = model.CommitStatusConfig{}
		}
	}

	// Webhook 密钥不随文档走：留空，由用户导入后重新配置。
	if strings.TrimSpace(raw.WebhookSecret) != "" && raw.WebhookSecret != "***" {
		add(pkg.LevelInfo, "webhook_secret", "文档中的 Webhook 密钥已被丢弃（导出文件不应携带密钥），请重新生成")
	}

	// SSH 发布目标：server_id 优先（同实例），否则按 server_name / host 对齐。
	serverByID, serverByName, serverByHost := s.sshIndex()
	for _, item := range exported.SshPublishes {
		cfg := item.SshPublishConfig
		cfg.Password = ""
		if cfg.ServerID != "" {
			if _, ok := serverByID[strings.ToLower(cfg.ServerID)]; ok {
				p.SshPublishes = append(p.SshPublishes, cfg)
				continue
			}
			cfg.ServerID = ""
		}
		if item.ServerName != "" {
			if id, ok := serverByName[strings.ToLower(item.ServerName)]; ok {
				cfg.ServerID = id
				cfg.Enabled = true
				p.SshPublishes = append(p.SshPublishes, cfg)
				continue
			}
			add(pkg.LevelError, "ssh",
				"发布目标 %q 引用的 SSH 服务器 %q 在当前实例不存在，已保存为停用状态；请先创建同名服务器再启用",
				cfg.Name, item.ServerName)
			cfg.Enabled = false
			p.SshPublishes = append(p.SshPublishes, cfg)
			continue
		}
		if cfg.Host != "" {
			if id, ok := serverByHost[strings.ToLower(cfg.Host)]; ok {
				cfg.ServerID = id
				cfg.Host = ""
				cfg.Enabled = true
				p.SshPublishes = append(p.SshPublishes, cfg)
				continue
			}
			add(pkg.LevelError, "ssh",
				"发布目标 %q 使用内联主机 %q 且没有可用凭据（密码不随导出文件传递），已保存为停用状态",
				cfg.Name, cfg.Host)
			cfg.Enabled = false
			p.SshPublishes = append(p.SshPublishes, cfg)
			continue
		}
		cfg.Enabled = false
		p.SshPublishes = append(p.SshPublishes, cfg)
	}
	// 原生 config.json 的旧单目标字段也接受。
	if len(p.SshPublishes) == 0 && raw.SshPublish != nil {
		if id, ok := serverByID[strings.ToLower(raw.SshPublish.ServerID)]; ok && raw.SshPublish.ServerID != "" {
			cfg := *raw.SshPublish
			cfg.Password = ""
			cfg.ServerID = id
			p.SshPublishes = append(p.SshPublishes, cfg)
		}
	}
	if len(p.SshPublishes) == 0 && len(raw.SshPublishes) > 0 {
		for _, cfg := range raw.SshPublishes {
			cfg.Password = ""
			if cfg.ServerID != "" {
				if _, ok := serverByID[strings.ToLower(cfg.ServerID)]; ok {
					p.SshPublishes = append(p.SshPublishes, cfg)
					continue
				}
				add(pkg.LevelError, "ssh", "发布目标 %q 的 server_id %s 在当前实例不存在，已保存为停用状态", cfg.Name, cfg.ServerID)
				cfg.ServerID = ""
			}
			cfg.Enabled = false
			p.SshPublishes = append(p.SshPublishes, cfg)
		}
	}

	// 通知：清空 URL 的目标没有 token 可用，直接丢弃并提示（保留通道信息无意义）。
	notify := exported.Notify
	if notify == nil {
		notify = raw.Notify
	}
	if notify != nil {
		n := *notify
		n.Targets = pruneEmptyTargets(n.Targets, &warningsRef{add: add})
		if notify.Escalation != nil {
			esc := *notify.Escalation
			esc.Targets = pruneEmptyTargets(esc.Targets, &warningsRef{add: add})
			n.Escalation = &esc
		}
		if len(notify.Routes) > 0 {
			routes := make([]model.NotifyRoute, len(notify.Routes))
			copy(routes, notify.Routes)
			for i := range routes {
				routes[i].Targets = pruneEmptyTargets(routes[i].Targets, &warningsRef{add: add})
			}
			n.Routes = routes
		}
		p.Notify = &n
	}
	return p, nil
}

// warningsRef 让 pruneEmptyTargets 能复用外层的告警收集函数。
type warningsRef struct {
	add func(level, field, format string, args ...interface{})
}

// pruneEmptyTargets 丢掉「没有可用地址」的群目标：导出文件不含 token，
// 保留一个空地址（或脱敏占位 ***）只会在发送时失败，不如明确丢弃并提示重填。
func pruneEmptyTargets(in []model.NotifyTarget, w *warningsRef) []model.NotifyTarget {
	if len(in) == 0 {
		return nil
	}
	out := make([]model.NotifyTarget, 0, len(in))
	dropped := 0
	for _, t := range in {
		if !hasUsableURL(t.URL) {
			dropped++
			continue
		}
		out = append(out, t)
	}
	if dropped > 0 && w != nil {
		w.add(pkg.LevelWarn, "notify",
			"有 %d 个群机器人没有可用地址（token 不随文件传递，或为脱敏占位 ***），已丢弃，请在任务配置中重新填写", dropped)
	}
	return out
}

// hasUsableURL 判断地址是否可用于发送：空值与 *** 都不算。
// 有人会把 GET /api/pipelines/{id} 的响应直接粘进导入框，那里的 URL 已被脱敏成 ***，
// 把它当真实地址写进配置会造成「通知发不出去但没人知道」。
func hasUsableURL(raw string) bool {
	url := strings.TrimSpace(raw)
	if url == "" || url == "***" {
		return false
	}
	// 脱敏后形如 https://oapi.dingtalk.com/robot/send?access_token=***，同样不可用。
	return !strings.HasSuffix(url, "=***") && !strings.HasSuffix(url, "/***")
}

func (s *PortabilityService) credentialName(ctx context.Context, id string) string {
	if s.credentials == nil || id == "" {
		return ""
	}
	list, err := s.credentials.List(ctx)
	if err != nil {
		return ""
	}
	for _, c := range list {
		if c.ID == id {
			return c.Name
		}
	}
	return ""
}

func (s *PortabilityService) credentialID(ctx context.Context, name string) string {
	if s.credentials == nil || name == "" {
		return ""
	}
	list, err := s.credentials.List(ctx)
	if err != nil {
		return ""
	}
	for _, c := range list {
		if strings.EqualFold(strings.TrimSpace(c.Name), name) {
			return c.ID
		}
	}
	return ""
}

func (s *PortabilityService) credentialExists(ctx context.Context, id string) bool {
	if s.credentials == nil {
		return true // 没注入凭据库时不做存在性判断（测试/精简部署）
	}
	list, err := s.credentials.List(ctx)
	if err != nil {
		return false
	}
	for _, c := range list {
		if c.ID == id {
			return true
		}
	}
	return false
}

// sshServerNames 返回 server_id → 名称（小写 ID 作键）。
func (s *PortabilityService) sshServerNames() map[string]string {
	out := map[string]string{}
	if s.settings == nil {
		return out
	}
	for _, server := range s.settings.Current().SshServers {
		out[strings.ToLower(strings.TrimSpace(server.ID))] = server.Name
	}
	return out
}

// sshIndex 建立三种索引：按 ID、按名称、按主机，用于尽量自动对齐发布目标。
func (s *PortabilityService) sshIndex() (byID, byName, byHost map[string]string) {
	byID = map[string]string{}
	byName = map[string]string{}
	byHost = map[string]string{}
	if s.settings == nil {
		return byID, byName, byHost
	}
	for _, server := range s.settings.Current().SshServers {
		id := strings.TrimSpace(server.ID)
		byID[strings.ToLower(id)] = id
		if name := strings.ToLower(strings.TrimSpace(server.Name)); name != "" {
			byName[name] = id
		}
		if host := strings.ToLower(strings.TrimSpace(server.Host)); host != "" {
			byHost[host] = id
		}
	}
	return byID, byName, byHost
}

func (s *PortabilityService) findByName(ctx context.Context, name string) (*model.Pipeline, error) {
	list, err := s.pipelines.List(ctx)
	if err != nil {
		return nil, err
	}
	for _, p := range list {
		if strings.EqualFold(strings.TrimSpace(p.Name), name) {
			return p, nil
		}
	}
	return nil, nil
}

func (s *PortabilityService) uniqueName(ctx context.Context, name string) string {
	list, err := s.pipelines.List(ctx)
	if err != nil {
		return name
	}
	used := make(map[string]bool, len(list))
	for _, p := range list {
		used[strings.ToLower(strings.TrimSpace(p.Name))] = true
	}
	if !used[strings.ToLower(name)] {
		return name
	}
	for i := 2; i < 1000; i++ {
		candidate := fmt.Sprintf("%s-%d", name, i)
		if !used[strings.ToLower(candidate)] {
			return candidate
		}
	}
	return name
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func firstIntValue(values ...int) int {
	for _, v := range values {
		if v != 0 {
			return v
		}
	}
	return 0
}

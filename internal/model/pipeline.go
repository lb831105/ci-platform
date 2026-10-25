package model

import (
	"strings"
	"time"
)

// 构建执行方式。平台已统一为本地执行，保留常量用于兼容旧数据。
const ExecutorLocal = "local"

// SshPublishConfig 描述 SSH 发布阶段配置，语义对齐 Jenkins Publish Over SSH 插件。
// Enabled 为 false 时构建成功后直接结束，不发起任何 SSH 连接。
// ServerID 非空时，主机/端口/账号/认证方式来自系统配置中的 SSH 服务器，
// 任务只保留发布相关的 SourceFiles / RemovePrefix / ExecCommand / 远程目录覆盖。
type SshPublishConfig struct {
	// Name 是发布目标名称（多目标时用于日志区分与「重新发布」选择）。
	Name           string   `json:"name,omitempty"`
	Enabled        bool     `json:"enabled"`
	ServerID       string   `json:"server_id,omitempty"` // 引用系统配置中的 SSH 服务器
	Host           string   `json:"host,omitempty"`      // 以下为旧版任务内联配置（向后兼容）
	Port           int      `json:"port,omitempty"`
	Username       string   `json:"username,omitempty"`
	AuthType       string   `json:"auth_type,omitempty"` // key | password
	PrivateKeyPath string   `json:"private_key_path,omitempty"`
	Password       string   `json:"password,omitempty"`
	KnownHostsPath string   `json:"known_hosts_path,omitempty"` // 可选：启用服务器 host key 校验
	RemoteBaseDir  string   `json:"remote_base_dir"`
	SourceFiles    []string `json:"source_files"`
	RemovePrefix   string   `json:"remove_prefix"`
	ExecCommand    string   `json:"exec_command"`

	// 以下三项对齐 Jenkins Publish Over SSH 的 transfer 选项：
	//   clean_remote      = Clean remote directory（上传前清空目标目录，整目录替换部署）
	//   flatten           = Flatten files（不保留相对路径，全部放到 remote_base_dir）
	//   exec_timeout_sec  = Exec timeout（远程命令超时，0 表示沿用构建级超时）
	CleanRemote    bool `json:"clean_remote,omitempty"`
	Flatten        bool `json:"flatten,omitempty"`
	ExecTimeoutSec int  `json:"exec_timeout_sec,omitempty"`
}

// CommitStatusConfig 配置「构建状态回写代码平台」（目前实现 GitLab）。
// 为什么放在任务上：同一个 GitLab 项目可能对应多个任务（构建/部署），
// 状态名与凭据需要按任务区分。
type CommitStatusConfig struct {
	Enabled bool `json:"enabled"`
	// CredentialID 引用凭据（secret_text 存 Personal Access Token），需要 api 权限。
	CredentialID string `json:"credential_id,omitempty"`
	// Project 是 GitLab 项目 ID 或 group/app 路径；留空时使用 Webhook payload 里带的项目。
	Project string `json:"project,omitempty"`
}

// ArtifactConfig 描述构建产物的归档规则，语义对齐 Jenkins "Archive the artifacts"。
// 构建步骤成功后，按 Files 中的 glob（相对工作区）复制到
// data/jobs/{pid}/builds/{bid}/artifacts/，每次构建独立保留。
type ArtifactConfig struct {
	Enabled bool     `json:"enabled"`
	Files   []string `json:"files"`
}

// 构建参数类型（对齐 Jenkins 的 String / Boolean / Choice / Text）。
const (
	ParamString = "string"
	ParamBool   = "bool"
	ParamChoice = "choice"
	ParamText   = "text"
)

// ParamDef 定义一个构建参数：触发构建时由用户填值（或取默认值），
// 值会以「参数名」和「CI_PARAM_<大写名>」两种环境变量注入构建步骤。
type ParamDef struct {
	Name        string   `json:"name"`
	Type        string   `json:"type"` // string | bool | choice | text
	Default     string   `json:"default"`
	Options     []string `json:"options,omitempty"` // choice 专用
	Required    bool     `json:"required"`
	Description string   `json:"description,omitempty"`
}

// EnvName 返回该参数在高权限前缀下的环境变量名（CI_PARAM_XXX）。
func (p ParamDef) EnvName() string {
	return "CI_PARAM_" + strings.ToUpper(p.Name)
}

// Step 是单个构建步骤，串行执行，run 为 shell 命令。
type Step struct {
	Name string `json:"name"`
	Run  string `json:"run"`

	// 步骤级控制（对齐 GitLab CI / GitHub Actions 的常见能力）：
	// timeout_sec=0 表示不限；retry 是「额外重试次数」；allow_failure 失败不阻断；
	// when 取 success（默认）/ failure / always。
	TimeoutSec    int    `json:"timeout_sec"`
	Retry         int    `json:"retry"`
	RetryDelaySec int    `json:"retry_delay_sec"`
	AllowFailure  bool   `json:"allow_failure"`
	When          string `json:"when"`
}

// 步骤级控制的上限：防止误填（如 retry=1000）导致构建长时间卡住。
const (
	MaxStepTimeoutSec = 86400 // 24h
	MaxStepRetry      = 5
	MaxStepRetryDelay = 60 // 秒
)

// 步骤 when 取值。
const (
	StepWhenSuccess = "success"
	StepWhenFailure = "failure"
	StepWhenAlways  = "always"
)

// Pipeline 对应 Jenkins Job，持久化为 data/jobs/{id}/config.json。
type Pipeline struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Description   string `json:"description"`
	Image         string `json:"image"`
	Steps         []Step `json:"steps"`
	GitURL        string `json:"git_url"`
	GitBranch     string `json:"git_branch"`
	GitCredential string `json:"git_credential_id"` // 引用的凭据 ID（账号密码 / 令牌）
	WebhookSecret string `json:"webhook_secret"`

	// Webhook 分支策略：Webhook 触发时用哪个分支构建。
	//   ""      / "payload" → 用推送事件里的分支（默认，对齐 GitLab CI：推哪个分支就构建哪个）
	//   "fixed"            → 始终构建上面 git_branch 配置的分支
	WebhookBranchMode string `json:"webhook_branch_mode,omitempty"`
	// WebhookBranches 是允许触发的分支白名单（精确或 release/* / hotfix/** 通配），空表示任意分支。
	WebhookBranches []string `json:"webhook_branches,omitempty"`
	// WebhookEvents 是允许触发的事件类型（push / tag_push），空表示只接受 push。
	// 默认只认 push：MR/评论等事件触发构建几乎总是误配置。
	WebhookEvents []string `json:"webhook_events,omitempty"`
	// CommitStatus 把构建状态回写到代码平台（GitLab commit status），便于 MR 里看到结果。
	CommitStatus CommitStatusConfig `json:"commit_status"`

	// 定时构建（对齐 Jenkins Build periodically）：5 字段 cron 规格，
	// 支持 H 散列均衡；为空表示不定时触发。
	CronSpec string `json:"cron_spec,omitempty"`
	// RequireApproval 为 true 时，构建触发后先进入 pending_approval 状态，
	// 必须由拥有 job.approve 权限的人放行才会真正执行。
	// 典型用法：生产发布任务开启，测试环境任务不开启。
	RequireApproval bool `json:"require_approval"`
	// ApprovalTimeoutMinutes 审批等待上限（0 表示不超时），超时自动按「拒绝」落 aborted，
	// 避免没人处理的发布把构建历史永远挂在「待审批」。
	ApprovalTimeoutMinutes int `json:"approval_timeout_minutes"`

	// Params 是构建参数定义；为空则是普通「立即构建」，不弹参数表单。
	Params []ParamDef `json:"params,omitempty"`
	// SshPublish 是旧版单目标配置（向后兼容，读取时由 EffectiveSshPublishes 归一为列表）。
	SshPublish *SshPublishConfig `json:"ssh_publish,omitempty"`
	// SshPublishes 是多目标发布列表：一次构建可依次发布到多台服务器/多个目录。
	SshPublishes []SshPublishConfig `json:"ssh_publishes,omitempty"`
	Artifacts    ArtifactConfig     `json:"artifacts"`

	// 丢弃旧的构建（per-job 覆盖全局 Build Discarders）。
	BuildRetentionMaxPerJob  int `json:"build_retention_max_per_job"`
	BuildRetentionMaxAgeDays int `json:"build_retention_max_age_days"`

	// 构建环境：是否在构建前清空工作区。nil 视为 false（默认复用工作区）。
	DeleteWorkspaceBeforeBuild *bool `json:"delete_workspace_before_build,omitempty"`

	// 执行方式：已统一为 local（服务器本地执行）。字段保留仅为兼容旧数据。
	Executor string `json:"executor,omitempty"`

	// 通知：把该任务与人员（用户名，解析邮箱/企微 ID）或群机器人绑定。
	// 为 nil 时沿用「系统配置 → 邮件通知 / Webhook 通知」的全局设置。
	Notify *JobNotify `json:"notify,omitempty"`

	// WorkspaceDir 是 data/workspace 下的目录名，默认由任务名生成。
	// 单独存储以支持重命名、重名后缀与旧数据迁移。
	WorkspaceDir string `json:"workspace_dir,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// EffectiveBranch 返回归一化后的分支名，默认 main。
func (p *Pipeline) EffectiveBranch() string {
	if p.GitBranch == "" {
		return "main"
	}
	return p.GitBranch
}

// EffectiveSshPublishes 返回实际生效的发布目标（只含 enabled=true 的项）。
//
// 兼容旧数据：老任务的单目标配置存在 ssh_publish 字段里，这里归一为
// 一个名为「默认发布」的目标，构建/重新发布等调用方不需要关心两种存储形态。
func (p *Pipeline) EffectiveSshPublishes() []SshPublishConfig {
	if p == nil {
		return nil
	}
	if len(p.SshPublishes) > 0 {
		out := make([]SshPublishConfig, 0, len(p.SshPublishes))
		for _, c := range p.SshPublishes {
			if c.Enabled {
				out = append(out, c)
			}
		}
		return out
	}
	if p.SshPublish != nil && p.SshPublish.Enabled {
		legacy := *p.SshPublish
		if strings.TrimSpace(legacy.Name) == "" {
			legacy.Name = "默认发布"
		}
		return []SshPublishConfig{legacy}
	}
	return nil
}

// SSHEnabled 判断是否需要在构建成功后执行 SSH 发布（任一目标启用即可）。
func (p *Pipeline) SSHEnabled() bool {
	return len(p.EffectiveSshPublishes()) > 0
}

// ShouldCleanWorkspace 返回构建前是否需要清空工作区。
// 默认 false：复用工作区并增量更新 Git，保留上次构建产物；
// 只有显式勾选“构建前清空工作区”时才删除。
func (p *Pipeline) ShouldCleanWorkspace() bool {
	return p.DeleteWorkspaceBeforeBuild != nil && *p.DeleteWorkspaceBeforeBuild
}

// PortOf 返回归一化端口，默认 22。
func (c *SshPublishConfig) PortOf() int {
	if c.Port <= 0 {
		return 22
	}
	return c.Port
}

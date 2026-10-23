package model

import "time"

// 构建执行方式。平台已统一为本地执行，保留常量用于兼容旧数据。
const ExecutorLocal = "local"

// SshPublishConfig 描述 SSH 发布阶段配置，语义对齐 Jenkins Publish Over SSH 插件。
// Enabled 为 false 时构建成功后直接结束，不发起任何 SSH 连接。
// ServerID 非空时，主机/端口/账号/认证方式来自系统配置中的 SSH 服务器，
// 任务只保留发布相关的 SourceFiles / RemovePrefix / ExecCommand / 远程目录覆盖。
type SshPublishConfig struct {
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
}

// ArtifactConfig 描述构建产物的归档规则，语义对齐 Jenkins "Archive the artifacts"。
// 构建步骤成功后，按 Files 中的 glob（相对工作区）复制到
// data/jobs/{pid}/builds/{bid}/artifacts/，每次构建独立保留。
type ArtifactConfig struct {
	Enabled bool     `json:"enabled"`
	Files   []string `json:"files"`
}

// Step 是单个构建步骤，串行执行，run 为 shell 命令。
type Step struct {
	Name string `json:"name"`
	Run  string `json:"run"`
}

// Pipeline 对应 Jenkins Job，持久化为 data/jobs/{id}/config.json。
type Pipeline struct {
	ID            string            `json:"id"`
	Name          string            `json:"name"`
	Description   string            `json:"description"`
	Image         string            `json:"image"`
	Steps         []Step            `json:"steps"`
	GitURL        string            `json:"git_url"`
	GitBranch     string            `json:"git_branch"`
	GitCredential string            `json:"git_credential_id"` // 引用的凭据 ID（账号密码 / 令牌）
	WebhookSecret string            `json:"webhook_secret"`
	SshPublish    *SshPublishConfig `json:"ssh_publish,omitempty"`
	Artifacts     ArtifactConfig    `json:"artifacts"`

	// 丢弃旧的构建（per-job 覆盖全局 Build Discarders）。
	BuildRetentionMaxPerJob  int `json:"build_retention_max_per_job"`
	BuildRetentionMaxAgeDays int `json:"build_retention_max_age_days"`

	// 构建环境：是否在构建前清空工作区。nil 视为 false（默认复用工作区）。
	DeleteWorkspaceBeforeBuild *bool `json:"delete_workspace_before_build,omitempty"`

	// 执行方式：已统一为 local（服务器本地执行）。字段保留仅为兼容旧数据。
	Executor string `json:"executor,omitempty"`

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

// SSHEnabled 判断是否需要在构建成功后执行 SSH 发布。
func (p *Pipeline) SSHEnabled() bool {
	return p.SshPublish != nil && p.SshPublish.Enabled
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

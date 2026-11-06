package model

import (
	"runtime"
	"strings"
	"time"
)

// DefaultShell 返回运行平台的默认执行 shell。
func DefaultShell() string {
	if runtime.GOOS == "windows" {
		return "cmd.exe"
	}
	return "/bin/sh"
}

// ToolInstallation 是某个工具类别下的一个安装项。
// 平台直接使用运行服务的服务器上已安装的工具，home 为工具主目录（如 GOROOT / JAVA_HOME）。
type ToolInstallation struct {
	Name string `json:"name"`
	Home string `json:"home,omitempty"` // 宿主机上的工具主目录（手动模式必填，自动模式由平台下载后写入）
	Note string `json:"note,omitempty"`

	// InstallMode 对齐 Jenkins 的「自动安装」开关：
	//   manual（默认，兼容旧数据）：使用服务器上已装好的工具，只认 Home
	//   auto：由平台按 Version 下载安装到 data/tools，Home 由平台填写
	InstallMode string `json:"install_mode,omitempty"`
	// Version 是自动安装模式下的版本号（来自 /api/system/tools/catalog）。
	Version string `json:"version,omitempty"`
}

// 工具安装方式常量。
const (
	ToolInstallManual = "manual"
	ToolInstallAuto   = "auto"
)

// IsAutoInstall 判断是否为自动安装模式（空值视为手动，兼容旧配置）。
func (t ToolInstallation) IsAutoInstall() bool {
	return t.InstallMode == ToolInstallAuto
}

// ToolCategory 是一个工具类别（JDK / Git / Gradle / Ant / Maven / NodeJS），
// 对齐 Jenkins「全局工具配置」中每类工具的「新增 X」列表。
type ToolCategory struct {
	ID            string             `json:"id"`
	Title         string             `json:"title"`
	Hint          string             `json:"hint,omitempty"`
	Installations []ToolInstallation `json:"installations"`
}

// MavenSettings 对应 Jenkins「Maven 项目配置」。
type MavenSettings struct {
	MavenOpts string `json:"maven_opts"` // 全局 MAVEN_OPTS
	LocalRepo string `json:"local_repo"` // Local Maven Repository（服务器路径）
}

// ToolMount 为旧版 Docker 构建的挂载配置。
// 平台已改为本地构建，该字段仅用于兼容旧 settings.json 与旧客户端请求，不再参与构建。
type ToolMount struct {
	HostPath      string `json:"host_path"`
	ContainerPath string `json:"container_path"`
	ReadOnly      bool   `json:"read_only"`
	Note          string `json:"note,omitempty"`
}

// SshServer 是系统配置中集中维护的 SSH 发布目标（对齐 Jenkins Publish Over SSH 的全局服务器）。
// 任务只引用 ID，不再重复保存主机/账号/密码。
type SshServer struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Host           string `json:"host"`
	Port           int    `json:"port"`
	Username       string `json:"username"`
	AuthType       string `json:"auth_type"` // key | password
	PrivateKeyPath string `json:"private_key_path,omitempty"`
	Password       string `json:"password,omitempty"`
	KnownHostsPath string `json:"known_hosts_path,omitempty"` // 可选：known_hosts 文件路径，配置后校验服务器主机密钥
	RemoteBaseDir  string `json:"remote_base_dir,omitempty"`
	Note           string `json:"note,omitempty"`
	HasPassword    bool   `json:"has_password,omitempty"` // 仅接口视图使用
}

// EnvVar 是「全局属性 → 环境变量」中的一项，会注入到每次构建的环境。
type EnvVar struct {
	Key   string `json:"key"`
	Value string `json:"value"`
	// Secret 为 true 时值以 AES-GCM 落盘，API 不回传原文；
	// 构建时注入环境变量并参与日志遮罩。
	Secret bool `json:"secret,omitempty"`
}

// IsSecretEnvName 判断环境变量名是否属于密钥类。
// 系统配置加密、构建日志遮罩、API 脱敏共用同一套规则，避免一处漏掉。
func IsSecretEnvName(name string) bool {
	name = strings.ToLower(name)
	for _, part := range []string{"password", "passwd", "pwd", "token", "secret", "credential", "private_key", "access_key"} {
		if strings.Contains(name, part) {
			return true
		}
	}
	return false
}

// EmailSettings 对应 Jenkins「邮件通知」，用于构建结果通知。
type EmailSettings struct {
	Enabled         bool   `json:"enabled"`
	SMTPHost        string `json:"smtp_host"`
	SMTPPort        int    `json:"smtp_port"`
	UseTLS          bool   `json:"use_tls"`
	Username        string `json:"username"`
	Password        string `json:"password,omitempty"` // 读取时脱敏，更新时留空表示沿用
	From            string `json:"from"`
	DefaultSuffix   string `json:"default_suffix"` // 用户默认邮件后缀
	Recipients      string `json:"recipients"`     // 默认收件人（逗号分隔）
	NotifyOnFailure bool   `json:"notify_on_failure"`
	NotifyOnSuccess bool   `json:"notify_on_success"`
	SubjectTemplate string `json:"subject_template"`
}

// WebhookNotifySettings 构建结果 Webhook 通知（钉钉 / 企业微信 / Slack 机器人）。
// URL 含 access_token 等密钥：读取时脱敏、日志永不打印。
type WebhookNotifySettings struct {
	Enabled         bool   `json:"enabled"`
	Channel         string `json:"channel"` // dingtalk | wecom | slack
	URL             string `json:"url"`
	NotifyOnFailure bool   `json:"notify_on_failure"`
	NotifyOnSuccess bool   `json:"notify_on_success"`
}

// Settings 是可持久化的系统级配置（data/settings.json），对齐 Jenkins「系统配置」。
// 启动参数 / 环境变量提供初始默认值，此后以文件为准。
type Settings struct {
	// 主目录 / 系统消息 / Jenkins Location
	DisplayName   string `json:"display_name"` // Appearance：页面标题
	SystemMessage string `json:"system_message"`
	JenkinsURL    string `json:"jenkins_url"`
	AdminEmail    string `json:"admin_email"`

	// 全局设置
	DefaultImage   string `json:"default_image"`
	DefaultBranch  string `json:"default_branch"`
	JobNameRegex   string `json:"job_name_regex"` // 工程命名限制（正则）
	QuietPeriodSec int    `json:"quiet_period_seconds"`
	SCMRetryCount  int    `json:"scm_retry_count"`
	Shell          string `json:"shell"` // 执行步骤使用的 shell

	// Global Build Time Out / Global Build Discarders
	GlobalBuildTimeoutMinutes int `json:"global_build_timeout_minutes"`
	BuildRetentionMaxPerJob   int `json:"build_retention_max_per_job"`
	BuildRetentionMaxAgeDays  int `json:"build_retention_max_age_days"`
	// 产物归档保护：避免一次构建把磁盘写满。
	ArtifactMaxTotalMB int `json:"artifact_max_total_mb"`
	ArtifactMaxFiles   int `json:"artifact_max_files"`
	// 构建日志单次上限（MB）：超长日志拖慢读取与前端渲染，超过后截断。0 = 不限制。
	LogMaxMB int `json:"log_max_mb"`

	// 构建结果 Webhook 通知（钉钉 / 企业微信 / Slack）。
	WebhookNotify WebhookNotifySettings `json:"webhook_notify"`

	// GitLab 站点地址（如 https://gitlab.example.com），用于「回写构建状态」。
	GitlabBaseURL string `json:"gitlab_base_url,omitempty"`

	// 全局属性
	GlobalEnv    []EnvVar `json:"global_env"`
	GitUserName  string   `json:"git_user_name"`
	GitUserEmail string   `json:"git_user_email"`

	// 工具 / Maven / 邮件
	ToolCategories []ToolCategory `json:"tool_categories"`
	ToolMounts     []ToolMount    `json:"tool_mounts,omitempty"` // 已废弃：仅兼容旧数据
	SshServers     []SshServer    `json:"ssh_servers"`           // SSH 发布目标（集中维护）
	Maven          MavenSettings  `json:"maven"`
	Email          EmailSettings  `json:"email"`

	QuietMode bool `json:"quiet_mode"` // 准备关机：静默后不再接受新构建

	AuthEnabled      bool   `json:"auth_enabled"`
	AuthUser         string `json:"auth_user"`
	AuthPasswordHash string `json:"auth_password_hash,omitempty"` // bcrypt hash，生产环境不落明文
	AuthPassword     string `json:"auth_password,omitempty"`      // 兼容旧配置/首次启动输入；落盘前转为 hash

	// 以下为启动期配置，界面只读，修改后需重启生效。
	Workers              int `json:"workers"`
	QueueSize            int `json:"queue_size"`
	WorkspaceTTLMinutes  int `json:"workspace_ttl_minutes"`
	CleanIntervalMinutes int `json:"clean_interval_minutes"`

	UpdatedAt time.Time `json:"updated_at"`
}

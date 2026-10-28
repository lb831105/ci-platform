package model

import (
	"time"
)

const (
	// ArtifactDirName 是每次构建归档产物的目录名（位于 build 目录下）。
	ArtifactDirName = "artifacts"
	// ArtifactManifestName 记录产物清单，供列表/下载/校验使用。
	ArtifactManifestName = "artifacts.json"
)

// BuildStatus 构建状态枚举。
type BuildStatus string

const (
	BuildPending BuildStatus = "pending"
	BuildRunning BuildStatus = "running"
	BuildSuccess BuildStatus = "success"
	BuildFailed  BuildStatus = "failed"
	// BuildAborted 手动取消（对齐 Jenkins 的 aborted）：构建被用户主动停止，
	// 与失败区分开，便于区分“代码问题”与“人为干预”。
	BuildAborted BuildStatus = "aborted"
	// BuildPendingApproval 等待发布审批：任务开启了「发布审批」时，构建创建后先停在这里，
	// 由拥有 job.approve 权限的人放行才开始执行（此时不占用执行器，也不产生工作区）。
	BuildPendingApproval BuildStatus = "pending_approval"
)

// BuildApproval 记录一次发布审批的来龙去脉（谁申请、谁放行/拒绝、理由、时间）。
// 为什么落进 build.json：审批是发布链路的一部分，必须能和这次构建一起复盘与导出。
type BuildApproval struct {
	RequestedAt time.Time  `json:"requested_at"`
	RequestedBy string     `json:"requested_by,omitempty"` // 触发人
	DecidedAt   *time.Time `json:"decided_at,omitempty"`
	DecidedBy   string     `json:"decided_by,omitempty"` // 审批人
	Approved    bool       `json:"approved"`
	Comment     string     `json:"comment,omitempty"` // 审批意见（拒绝时必填）
}

// TriggerType 触发方式枚举：手动、Webhook 与定时（cron）。
type TriggerType string

const (
	TriggerManual  TriggerType = "manual"
	TriggerWebhook TriggerType = "webhook"
	TriggerTimer   TriggerType = "timer"
)

// ArtifactFile 描述一个已归档的构建产物。
// Path 为相对工作区（也相对 artifacts 目录）的斜杠路径。
type ArtifactFile struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256,omitempty"`
}

// ArtifactManifest 是 artifacts.json 的内容。
type ArtifactManifest struct {
	CreatedAt time.Time      `json:"created_at"`
	TotalSize int64          `json:"total_size"`
	Files     []ArtifactFile `json:"files"`
}

// Build 对应 Jenkins Build，持久化为 data/jobs/{pid}/builds/{id}/build.json。
type Build struct {
	ID          string      `json:"id"`
	PipelineID  string      `json:"pipeline_id"`
	Status      BuildStatus `json:"status"`
	TriggerType TriggerType `json:"trigger_type"`
	TriggeredBy string      `json:"triggered_by"` // 手动触发时的用户名；webhook 触发时为 webhook
	// CreatedAt 是触发/入队时间；StartedAt 是真正开始执行的时间。
	// Jenkins 的 “waiting” 就是这两者之间的差值；旧构建没有该字段时前端回退到 StartedAt。
	CreatedAt  *time.Time `json:"created_at,omitempty"`
	StartedAt  *time.Time `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at"`
	ExitCode   int        `json:"exit_code"`

	// StepResults 是本次构建每一步的状态与耗时（由 LocalExecutor 采集）。
	StepResults []StepResult `json:"step_results,omitempty"`
	// TestReport 是 JUnit 测试报告汇总；未启用/未收集时为空。
	TestReport *TestReport `json:"test_report,omitempty"`
	// Environment 是触发时任务环境的快照。
	Environment string `json:"environment,omitempty"`
	// Deployments 记录本次构建触发的 SSH 发布/重新发布结果。
	Deployments []DeploymentRecord `json:"deployments,omitempty"`

	// Description 是构建页的备注（对齐 Jenkins Build 的 Add description）。
	Description string `json:"description,omitempty"`
	// KeepForever 为 true 时构建保留策略跳过本条记录（对齐 Jenkins “Keep this build forever”）。
	KeepForever bool `json:"keep_forever,omitempty"`

	// Params 是本次构建实际使用的参数值（触发时快照，便于复盘「这次用的是什么版本号」）。
	// 密码参数在这里固定为 ***；真实值在 SecretParams 中加密保存。
	Params map[string]string `json:"params,omitempty"`
	// SecretParams 保存密码参数的真实值（enc:v1: 密文），只供执行器解密使用。
	// API 响应必须清空该字段；build.json 落盘只能是密文。
	SecretParams map[string]string `json:"secret_params,omitempty"`

	// Approval 仅在任务开启发布审批时有值。
	Approval *BuildApproval `json:"approval,omitempty"`

	// CommitProject 是本次构建对应的代码平台项目（如 GitLab 的 group/app），
	// 由 Webhook payload 带入：任务里没固定写项目路径时，回写状态用它定位。
	CommitProject string `json:"commit_project,omitempty"`

	// Advice 是给用户的可执行修复建议（目前用于"产物模式没匹配到文件"）：
	// 光在日志里说"匹配不到"还得让人自己去翻日志、改配置、再跑一次，
	// 前端据此直接提供「一键改成建议模式」。
	Advice *BuildAdvice `json:"advice,omitempty"`

	// SCM 现场信息：从实际签出的工作区读取，供「按分支路由」与「通知提交者」使用。
	// 非 Git 任务为空。
	Branch        string   `json:"branch,omitempty"`
	Commit        string   `json:"commit,omitempty"`         // 短 SHA
	CommitFull    string   `json:"commit_full,omitempty"`    // 完整 SHA（构建页展示）
	CommitMessage string   `json:"commit_message,omitempty"` // 提交说明（构建页的 Changes Summary）
	ChangedFiles  []string `json:"changed_files,omitempty"`  // 本次提交修改的文件（最多保留前 200 个）
	CommitAuthor  string   `json:"commit_author,omitempty"`  // 提交人姓名
	CommitEmail   string   `json:"commit_email,omitempty"`   // 提交人邮箱（路由到提交者用）
}

// TestCaseResult 是测试报告中的一个失败/错误用例（只保留需要展示的失败项）。
type TestCaseResult struct {
	Name      string `json:"name"`
	ClassName string `json:"class_name,omitempty"`
	Status    string `json:"status"` // failure | error | skipped
	TimeMS    int64  `json:"time_ms"`
	Message   string `json:"message,omitempty"`
}

// TestReport 是本次构建收集到的 JUnit 测试汇总。
type TestReport struct {
	Total       int              `json:"total"`
	Passed      int              `json:"passed"`
	Failed      int              `json:"failed"`
	Errors      int              `json:"errors"`
	Skipped     int              `json:"skipped"`
	Suites      int              `json:"suites"`
	DurationMS  int64            `json:"duration_ms"`
	Files       []string         `json:"files,omitempty"`
	FailedCases []TestCaseResult `json:"failed_cases,omitempty"`
	CreatedAt   time.Time        `json:"created_at"`
}

// DeploymentRecord 是一次 SSH 发布/重新发布的结果，用于构建页审计「发到了哪里、成功没有」。
type DeploymentRecord struct {
	Target        string    `json:"target,omitempty"`
	ServerID      string    `json:"server_id,omitempty"`
	Environment   string    `json:"environment,omitempty"`
	Status        string    `json:"status"` // success | failed
	DeployedAt    time.Time `json:"deployed_at"`
	DeployedBy    string    `json:"deployed_by,omitempty"`
	ArtifactCount int       `json:"artifact_count,omitempty"`
	Message       string    `json:"message,omitempty"`
}

// StepResult 是单个构建步骤的执行结果（Build 页的 Module Builds / 步骤耗时）。
// 步骤结果落进 build.json，构建结束后无需解析日志就能展示每步状态与耗时。
type StepResult struct {
	Name         string `json:"name"`
	Status       string `json:"status"` // success | failed | skipped | cancelled
	ExitCode     int    `json:"exit_code"`
	DurationMS   int64  `json:"duration_ms"`
	AllowFailure bool   `json:"allow_failure,omitempty"`
}

// BuildAdvice 是一次构建失败时给出的结构化修复建议。
type BuildAdvice struct {
	// Kind 目前只有 artifact_pattern（归档模式没匹配到文件）。
	Kind    string `json:"kind"`
	Message string `json:"message"`
	// Patterns 是本次配置的模式，Suggested 是建议改成什么。
	Patterns  []string `json:"patterns,omitempty"`
	Suggested []string `json:"suggested,omitempty"`
	// Files 是工作区里找到的同名文件（相对工作区路径），让用户确认"是不是这些"。
	Files []string `json:"files,omitempty"`
}

// LogKey 返回日志 Hub 的订阅键，格式 pipeline_id/build_id。
func LogKey(pipelineID, buildID string) string {
	return pipelineID + "/" + buildID
}

// Duration 返回构建耗时，未结束时返回 0。
func (b *Build) Duration() time.Duration {
	if b.StartedAt == nil || b.FinishedAt == nil {
		return 0
	}
	return b.FinishedAt.Sub(*b.StartedAt)
}

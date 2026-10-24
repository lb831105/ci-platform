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
)

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
	StartedAt   *time.Time  `json:"started_at"`
	FinishedAt  *time.Time  `json:"finished_at"`
	ExitCode    int         `json:"exit_code"`

	// SCM 现场信息：从实际签出的工作区读取，供「按分支路由」与「通知提交者」使用。
	// 非 Git 任务为空。
	Branch       string `json:"branch,omitempty"`
	Commit       string `json:"commit,omitempty"`        // 短 SHA
	CommitAuthor string `json:"commit_author,omitempty"` // 提交人姓名
	CommitEmail  string `json:"commit_email,omitempty"`  // 提交人邮箱（路由到提交者用）
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

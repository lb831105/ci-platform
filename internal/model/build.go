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
)

// TriggerType 触发方式枚举，仅支持手动与 Webhook 两种。
type TriggerType string

const (
	TriggerManual  TriggerType = "manual"
	TriggerWebhook TriggerType = "webhook"
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

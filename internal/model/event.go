package model

import "time"

// 出站事件类型。事件订阅用于把平台状态推给外部系统（IM、工单、发布系统），
// 与「任务级 IM 通知」不同：订阅是全局的，payload 是稳定的机器可读 JSON。
const (
	CIEventBuildStarted      = "build.started"
	CIEventBuildFinished     = "build.finished"
	CIEventDeployFinished    = "deploy.finished"
	CIEventApprovalRequested = "approval.requested"
)

// CIEventTypes 是允许订阅的全部事件。
var CIEventTypes = []string{
	CIEventBuildStarted,
	CIEventBuildFinished,
	CIEventDeployFinished,
	CIEventApprovalRequested,
}

// IsValidCIEvent 判断事件名是否受支持。
func IsValidCIEvent(event string) bool {
	for _, item := range CIEventTypes {
		if item == event {
			return true
		}
	}
	return false
}

// EventSubscription 是一个出站事件订阅。
// URL 与 Secret 落在 data/events.json，由 credentials.key 加密；
// HasURL/HasSecret 只用于接口视图，不参与持久化语义。
type EventSubscription struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	URL     string   `json:"url,omitempty"`
	Secret  string   `json:"secret,omitempty"`
	Events  []string `json:"events"`
	Enabled bool     `json:"enabled"`

	HasURL    bool `json:"has_url,omitempty"`
	HasSecret bool `json:"has_secret,omitempty"`
	// ClearSecret 为 true 时显式清空签名密钥；不传/留空表示沿用旧值，
	// 避免 GET 脱敏后再 PUT 把密钥误清空。
	ClearSecret bool `json:"clear_secret,omitempty"`
}

// CIEvent 是投递到订阅方的 JSON body。
// 字段一旦发布应保持向后兼容，只新增不重命名。
type CIEvent struct {
	ID        string    `json:"id"`
	Type      string    `json:"type"`
	Time      time.Time `json:"time"`
	Pipeline  string    `json:"pipeline_id"`
	JobName   string    `json:"job_name"`
	Build     string    `json:"build_id"`
	Status    string    `json:"status,omitempty"`
	Trigger   string    `json:"trigger_type,omitempty"`
	TriggerBy string    `json:"triggered_by,omitempty"`
	Branch    string    `json:"branch,omitempty"`
	Commit    string    `json:"commit,omitempty"`
	Env       string    `json:"environment,omitempty"`
	ExitCode  int       `json:"exit_code,omitempty"`
	Duration  int64     `json:"duration_ms,omitempty"`

	// 只有 deploy.finished 携带。
	Deployment *DeploymentRecord `json:"deployment,omitempty"`
	// 只有 approval.requested 携带。
	Approval *BuildApproval `json:"approval,omitempty"`
	// 只有 build.finished 携带测试汇总（有 JUnit 报告时）。
	TestReport *TestReport `json:"test_report,omitempty"`
}

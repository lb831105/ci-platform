package model

import "time"

// Dashboard 是首页仪表盘的聚合数据，供任务列表上方展示平台整体健康度。
// 只做聚合，不包含日志/密钥等敏感内容。
type Dashboard struct {
	UpdatedAt time.Time `json:"updated_at"`

	JobsTotal    int `json:"jobs_total"`
	JobsDisabled int `json:"jobs_disabled"`
	JobsRunning  int `json:"jobs_running"`

	BuildsToday   int `json:"builds_today"`
	SuccessToday  int `json:"success_today"`
	FailedToday   int `json:"failed_today"`
	AbortedToday  int `json:"aborted_today"`
	RunningBuilds int `json:"running_builds"`
	PendingBuilds int `json:"pending_builds"`

	SuccessRate   float64 `json:"success_rate"`
	AvgDurationMS int64   `json:"avg_duration_ms"`

	QueueLength   int  `json:"queue_length"`
	QueueCapacity int  `json:"queue_capacity"`
	Workers       int  `json:"workers"`
	Quiet         bool `json:"quiet"`

	DeploymentsToday        int `json:"deployments_today"`
	DeploymentFailuresToday int `json:"deployment_failures_today"`

	RecentDeployments []DashboardDeployment `json:"recent_deployments"`
	TopFailures       []DashboardFailure    `json:"top_failures"`
}

// DashboardDeployment 是仪表盘上的最近部署一行。
type DashboardDeployment struct {
	PipelineID   string    `json:"pipeline_id"`
	PipelineName string    `json:"pipeline_name"`
	BuildID      string    `json:"build_id"`
	Target       string    `json:"target,omitempty"`
	Environment  string    `json:"environment,omitempty"`
	Status       string    `json:"status"`
	DeployedAt   time.Time `json:"deployed_at"`
	DeployedBy   string    `json:"deployed_by,omitempty"`
}

// DashboardFailure 是最近 7 天失败次数较多的任务。
type DashboardFailure struct {
	PipelineID   string     `json:"pipeline_id"`
	PipelineName string     `json:"pipeline_name"`
	Failures     int        `json:"failures"`
	LastFailedAt *time.Time `json:"last_failed_at,omitempty"`
	LastBuildID  string     `json:"last_build_id,omitempty"`
}

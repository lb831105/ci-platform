package model

import "time"

// SystemInfo 对应 Jenkins「系统信息」，返回运行环境与平台装配参数。
type SystemInfo struct {
	AppName       string    `json:"app_name"`
	Version       string    `json:"version"`
	Commit        string    `json:"commit"`
	BuildDate     string    `json:"build_date"`
	GoVersion     string    `json:"go_version"`
	OS            string    `json:"os"`
	Arch          string    `json:"arch"`
	CPUs          int       `json:"cpus"`
	Goroutines    int       `json:"goroutines"`
	MemoryAllocMB float64   `json:"memory_alloc_mb"`
	MemorySysMB   float64   `json:"memory_sys_mb"`
	UptimeSeconds int64     `json:"uptime_seconds"`
	DataHome      string    `json:"data_home"`
	JobsDir       string    `json:"jobs_dir"`
	WorkspaceDir  string    `json:"workspace_dir"`
	Workers       int       `json:"workers"`
	QueueCapacity int       `json:"queue_capacity"`
	QueueLength   int       `json:"queue_length"`
	QuietMode     bool      `json:"quiet_mode"`
	AuthEnabled   bool      `json:"auth_enabled"`
	PipelineCount int       `json:"pipeline_count"`
	BuildCount    int       `json:"build_count"`
	RunningBuilds int       `json:"running_builds"`
	ServerTime    time.Time `json:"server_time"`
}

// LoadStats 对应 Jenkins「负载统计」。
type LoadStats struct {
	PipelineCount     int   `json:"pipeline_count"`
	BuildCount        int   `json:"build_count"`
	SuccessBuilds     int   `json:"success_builds"`
	FailedBuilds      int   `json:"failed_builds"`
	RunningBuilds     int   `json:"running_builds"`
	PendingBuilds     int   `json:"pending_builds"`
	QueueLength       int   `json:"queue_length"`
	QueueCapacity     int   `json:"queue_capacity"`
	AverageDurationMS int64 `json:"average_duration_ms"`
	WorkspaceCount    int   `json:"workspace_count"`
	WorkspaceBytes    int64 `json:"workspace_bytes"`
	JobsBytes         int64 `json:"jobs_bytes"`
}

// Feature 是平台内置能力，对应「插件管理」中的已安装组件（只读）。
type Feature struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	Status      string `json:"status"` // active | builtin
	Description string `json:"description"`
}

// OldDataItem 是「管理旧数据」扫描到的一项可清理内容。
type OldDataItem struct {
	Kind   string `json:"kind"` // workspace | orphan_job
	Path   string `json:"path"`
	Bytes  int64  `json:"bytes"`
	Detail string `json:"detail"`
}

// OldDataReport 是旧数据扫描结果。
type OldDataReport struct {
	Items      []OldDataItem `json:"items"`
	TotalBytes int64         `json:"total_bytes"`
	ScannedAt  time.Time     `json:"scanned_at"`
}

// DataHomeInfo 描述运行时数据目录的生效值与待生效配置。
type DataHomeInfo struct {
	DataHome           string `json:"data_home"`            // 当前进程实际使用的目录
	ConfiguredDataHome string `json:"configured_data_home"` // 引导配置中的目录（重启后生效）
	ConfigFile         string `json:"config_file"`          // 引导配置文件路径
	JobsDir            string `json:"jobs_dir"`
	WorkspaceDir       string `json:"workspace_dir"`
	Explicit           bool   `json:"explicit"`         // 是否被 -data-home / CI_DATA_HOME 覆盖
	RestartRequired    bool   `json:"restart_required"` // 配置值与生效值不一致
}

package model

import "time"

// BuildRef 是构建的精简引用，用于任务列表上的「上次成功 / 上次失败 / 上次持续时间」。
type BuildRef struct {
	ID         string      `json:"id"`
	Status     BuildStatus `json:"status"`
	StartedAt  *time.Time  `json:"started_at"`
	FinishedAt *time.Time  `json:"finished_at"`
	DurationMS int64       `json:"duration_ms"`
}

// PipelineSummary 是任务列表行所需的聚合视图（对齐 Jenkins 作业列表列）。
// 内嵌 Pipeline 使得 JSON 与 Pipeline 字段平铺，前端无需二次查找。
type PipelineSummary struct {
	Pipeline
	Status      BuildStatus   `json:"status"`          // 最近一次构建状态；无构建时为 none
	Running     bool          `json:"running"`         // 是否存在 pending/running 构建
	LastBuild   *BuildRef     `json:"last_build"`      // 最近一次构建
	LastSuccess *BuildRef     `json:"last_success"`    // 最近一次成功构建
	LastFailure *BuildRef     `json:"last_failure"`    // 最近一次失败构建
	Recent      []BuildStatus `json:"recent_statuses"` // 最近若干次构建状态，用于计算天气图标
}

// BuildListItem 是「全局构建历史」列表的一行：构建本身 + 所属任务名。
type BuildListItem struct {
	Build
	PipelineName string `json:"pipeline_name"`
	// 以下字段由服务端按任务内构建序列计算，供全局构建页在分页后仍能
	// 正确显示 Jenkins 的「返回到正常 / 自从 #N 后断开 / 断开很长时间」。
	PreviousStatus    BuildStatus `json:"previous_status,omitempty"`
	FailureStreak     int         `json:"failure_streak,omitempty"`
	FailingSinceBuild string      `json:"failing_since_build,omitempty"`
	FailingSinceAt    *time.Time  `json:"failing_since_at,omitempty"`
}

// Summarize 根据某个 pipeline 的构建列表（需按编号倒序）计算列表视图。
func Summarize(p Pipeline, builds []*Build) PipelineSummary {
	summary := PipelineSummary{
		Pipeline: p,
		Status:   BuildStatus("none"),
		Recent:   make([]BuildStatus, 0, 5),
	}
	for _, b := range builds {
		// 待审批同属「未完成」：列表里应显示为活动状态，而不是最后一条历史结果。
		if b.Status == BuildRunning || b.Status == BuildPending || b.Status == BuildPendingApproval {
			summary.Running = true
		}
	}
	if len(builds) == 0 {
		return summary
	}
	summary.LastBuild = buildRef(builds[0])
	summary.Status = builds[0].Status
	for _, b := range builds {
		if len(summary.Recent) < 5 {
			summary.Recent = append(summary.Recent, b.Status)
		}
		switch b.Status {
		case BuildSuccess:
			if summary.LastSuccess == nil {
				summary.LastSuccess = buildRef(b)
			}
		case BuildFailed:
			if summary.LastFailure == nil {
				summary.LastFailure = buildRef(b)
			}
		}
	}
	return summary
}

func buildRef(b *Build) *BuildRef {
	if b == nil {
		return nil
	}
	return &BuildRef{
		ID:         b.ID,
		Status:     b.Status,
		StartedAt:  b.StartedAt,
		FinishedAt: b.FinishedAt,
		DurationMS: b.Duration().Milliseconds(),
	}
}

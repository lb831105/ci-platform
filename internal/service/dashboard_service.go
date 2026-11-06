package service

import (
	"context"
	"sort"
	"time"

	"ci-platform/internal/model"
)

// QueueStats 是仪表盘需要的调度器状态（由 Scheduler 实现）。
type QueueStats interface {
	QueueLength() int
	QueueCapacity() int
	Workers() int
	IsQuiet() bool
}

// SetQueueStats 注入调度器状态；未注入时队列相关指标为 0。
func (s *BuildService) SetQueueStats(stats QueueStats) {
	s.queueStats = stats
}

// Dashboard 聚合任务/构建/部署数据。数据量按“任务 × 构建目录”扫描，
// 与现有构建历史一致；仪表盘只在首页请求时计算，不进入构建执行热路径。
func (s *BuildService) Dashboard(ctx context.Context, pipelines []*model.Pipeline) (model.Dashboard, error) {
	now := time.Now()
	startOfDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	weekAgo := now.AddDate(0, 0, -7)
	out := model.Dashboard{
		UpdatedAt:         now.UTC(),
		JobsTotal:         len(pipelines),
		RecentDeployments: make([]model.DashboardDeployment, 0, 10),
		TopFailures:       make([]model.DashboardFailure, 0, 5),
	}
	if s.queueStats != nil {
		out.QueueLength = s.queueStats.QueueLength()
		out.QueueCapacity = s.queueStats.QueueCapacity()
		out.Workers = s.queueStats.Workers()
		out.Quiet = s.queueStats.IsQuiet()
	}

	type failureCounter struct {
		count int
		last  *time.Time
		build string
	}
	failures := make(map[string]*failureCounter)
	runningJobs := make(map[string]bool)

	var durationSum int64
	var durationCount int64
	for _, p := range pipelines {
		if p.Disabled {
			out.JobsDisabled++
		}
		builds, err := s.builds.ListByPipeline(ctx, p.ID)
		if err != nil {
			return out, err
		}
		for _, b := range builds {
			switch b.Status {
			case model.BuildRunning:
				out.RunningBuilds++
				runningJobs[p.ID] = true
			case model.BuildPending:
				out.PendingBuilds++
				runningJobs[p.ID] = true
			case model.BuildPendingApproval:
				out.PendingBuilds++
				runningJobs[p.ID] = true
			}
			createdAt := b.CreatedAt
			if createdAt == nil {
				createdAt = b.StartedAt
			}
			if createdAt != nil && !createdAt.Before(startOfDay) {
				out.BuildsToday++
				switch b.Status {
				case model.BuildSuccess:
					out.SuccessToday++
				case model.BuildFailed:
					out.FailedToday++
				case model.BuildAborted:
					out.AbortedToday++
				}
				if b.FinishedAt != nil && (b.Status == model.BuildSuccess || b.Status == model.BuildFailed) {
					durationSum += b.Duration().Milliseconds()
					durationCount++
				}
			}
			if b.Status == model.BuildFailed && b.StartedAt != nil && !b.StartedAt.Before(weekAgo) {
				c := failures[p.ID]
				if c == nil {
					c = &failureCounter{}
					failures[p.ID] = c
				}
				c.count++
				if c.last == nil || b.StartedAt.After(*c.last) {
					t := *b.StartedAt
					c.last = &t
					c.build = b.ID
				}
			}
			for _, deploy := range b.Deployments {
				if deploy.DeployedAt.Before(startOfDay) {
					continue
				}
				out.DeploymentsToday++
				if deploy.Status != "success" {
					out.DeploymentFailuresToday++
				}
				out.RecentDeployments = append(out.RecentDeployments, model.DashboardDeployment{
					PipelineID: p.ID, PipelineName: p.Name, BuildID: b.ID,
					Target: deploy.Target, Environment: deploy.Environment,
					Status: deploy.Status, DeployedAt: deploy.DeployedAt, DeployedBy: deploy.DeployedBy,
				})
			}
		}
	}

	out.JobsRunning = len(runningJobs)
	finishedToday := out.SuccessToday + out.FailedToday
	if finishedToday > 0 {
		out.SuccessRate = float64(out.SuccessToday) / float64(finishedToday)
	}
	if durationCount > 0 {
		out.AvgDurationMS = durationSum / durationCount
	}
	sort.Slice(out.RecentDeployments, func(i, j int) bool {
		return out.RecentDeployments[i].DeployedAt.After(out.RecentDeployments[j].DeployedAt)
	})
	if len(out.RecentDeployments) > 10 {
		out.RecentDeployments = out.RecentDeployments[:10]
	}
	for _, p := range pipelines {
		if c := failures[p.ID]; c != nil {
			out.TopFailures = append(out.TopFailures, model.DashboardFailure{
				PipelineID: p.ID, PipelineName: p.Name, Failures: c.count,
				LastFailedAt: c.last, LastBuildID: c.build,
			})
		}
	}
	sort.Slice(out.TopFailures, func(i, j int) bool {
		if out.TopFailures[i].Failures != out.TopFailures[j].Failures {
			return out.TopFailures[i].Failures > out.TopFailures[j].Failures
		}
		return out.TopFailures[i].PipelineName < out.TopFailures[j].PipelineName
	})
	if len(out.TopFailures) > 5 {
		out.TopFailures = out.TopFailures[:5]
	}
	return out, nil
}

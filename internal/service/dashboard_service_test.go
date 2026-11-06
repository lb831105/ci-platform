package service

import (
	"context"
	"testing"
	"time"

	"ci-platform/internal/model"
)

type fakeQueueStats struct {
	queued, capacity, workers int
	quiet                     bool
}

func (f fakeQueueStats) QueueLength() int   { return f.queued }
func (f fakeQueueStats) QueueCapacity() int { return f.capacity }
func (f fakeQueueStats) Workers() int       { return f.workers }
func (f fakeQueueStats) IsQuiet() bool      { return f.quiet }

func TestDashboardAggregatesBuildsAndDeployments(t *testing.T) {
	svc := newDeleteTestService(t, t.TempDir())
	svc.queueStats = fakeQueueStats{queued: 3, capacity: 10, workers: 2, quiet: true}
	svc.pipelines = &memPipelineStore{items: map[string]*model.Pipeline{
		"p1": {ID: "p1", Name: "api"},
		"p2": {ID: "p2", Name: "web", Disabled: true},
	}}
	now := time.Now()
	start := now.Add(-10 * time.Minute)
	finish := now.Add(-5 * time.Minute)
	yesterday := now.Add(-26 * time.Hour)
	mk := func(pipelineID string, status model.BuildStatus, createdAt time.Time, deployments ...model.DeploymentRecord) {
		b := &model.Build{
			PipelineID:  pipelineID,
			Status:      status,
			CreatedAt:   &createdAt,
			StartedAt:   &createdAt,
			Deployments: deployments,
		}
		if status == model.BuildSuccess || status == model.BuildFailed {
			b.FinishedAt = &finish
		}
		if err := svc.builds.CreateNext(context.Background(), b); err != nil {
			t.Fatal(err)
		}
	}
	mk("p1", model.BuildSuccess, start)
	mk("p1", model.BuildFailed, start)
	mk("p1", model.BuildRunning, start)
	mk("p1", model.BuildFailed, start, model.DeploymentRecord{Status: "success", DeployedAt: now, Target: "prod"})
	mk("p2", model.BuildSuccess, yesterday, model.DeploymentRecord{Status: "failed", DeployedAt: yesterday})

	pipelines := []*model.Pipeline{
		{ID: "p1", Name: "api"},
		{ID: "p2", Name: "web", Disabled: true},
	}
	got, err := svc.Dashboard(context.Background(), pipelines)
	if err != nil {
		t.Fatal(err)
	}
	if got.JobsTotal != 2 || got.JobsDisabled != 1 {
		t.Fatalf("jobs = %+v", got)
	}
	if got.BuildsToday != 4 || got.SuccessToday != 1 || got.FailedToday != 2 || got.RunningBuilds != 1 {
		t.Fatalf("today = %+v", got)
	}
	if got.SuccessRate < 0.33 || got.SuccessRate > 0.34 {
		t.Fatalf("success rate = %v", got.SuccessRate)
	}
	if got.DeploymentsToday != 1 || got.DeploymentFailuresToday != 0 {
		t.Fatalf("deployments = %d/%d", got.DeploymentsToday, got.DeploymentFailuresToday)
	}
	if got.QueueLength != 3 || got.QueueCapacity != 10 || got.Workers != 2 || !got.Quiet {
		t.Fatalf("queue = %+v", got)
	}
	if len(got.TopFailures) != 1 || got.TopFailures[0].PipelineID != "p1" || got.TopFailures[0].Failures != 2 {
		t.Fatalf("top failures = %+v", got.TopFailures)
	}
	if len(got.RecentDeployments) != 1 || got.RecentDeployments[0].PipelineID != "p1" {
		t.Fatalf("recent deployments = %+v", got.RecentDeployments)
	}
}

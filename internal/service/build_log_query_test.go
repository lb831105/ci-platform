package service

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"ci-platform/internal/model"
	"ci-platform/internal/store"
)

// tail 只返回最后 N 行，grep 过滤后再应用 tail；返回的总行数用于前端提示。
func TestQueryLogTailAndGrep(t *testing.T) {
	ctx := context.Background()
	builds := store.NewBuildStore(t.TempDir())
	if err := builds.Create(ctx, &model.Build{PipelineID: "p1", ID: "1"}); err != nil {
		t.Fatal(err)
	}
	text := "alpha\nbeta ERROR\ngamma\ndelta ERROR\nepsilon\n"
	if err := builds.AppendLog(ctx, "p1", "1", []byte(text)); err != nil {
		t.Fatal(err)
	}
	svc := &BuildService{builds: builds}

	got, total, matched, truncated, err := svc.QueryLog(ctx, "p1", "1", 2, "")
	if err != nil {
		t.Fatal(err)
	}
	if total != 5 || matched != 5 || !truncated {
		t.Fatalf("total=%d matched=%d truncated=%v", total, matched, truncated)
	}
	if got != "delta ERROR\nepsilon" {
		t.Fatalf("tail log = %q", got)
	}

	got, total, matched, truncated, err = svc.QueryLog(ctx, "p1", "1", 1, "error")
	if err != nil {
		t.Fatal(err)
	}
	if total != 5 || matched != 2 || !truncated {
		t.Fatalf("filtered total=%d matched=%d truncated=%v", total, matched, truncated)
	}
	if got != "delta ERROR" {
		t.Fatalf("filtered tail = %q", got)
	}
	if strings.Contains(got, "alpha") {
		t.Fatal("grep must drop non-matching lines")
	}
}

// 全局构建页分页后仍需要每行的 previous_status / failure_streak。
func TestAllBuildsAnnotatesFailureStreak(t *testing.T) {
	ctx := context.Background()
	builds := store.NewBuildStore(t.TempDir())
	now := time.Now().UTC()
	for i, status := range []model.BuildStatus{
		model.BuildSuccess, model.BuildFailed, model.BuildFailed, model.BuildFailed,
	} {
		started := now.Add(time.Duration(i) * time.Minute)
		if err := builds.Create(ctx, &model.Build{
			PipelineID: "p1", ID: strconv.Itoa(i + 1), Status: status, StartedAt: &started,
		}); err != nil {
			t.Fatal(err)
		}
	}
	svc := &BuildService{builds: builds}
	items, err := svc.AllBuilds(ctx, []*model.Pipeline{{ID: "p1", Name: "demo"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 4 {
		t.Fatalf("items = %d, want 4", len(items))
	}
	// 返回按开始时间倒序：#4 是连续第 3 次失败。
	latest := items[0]
	if latest.ID != "4" || latest.FailureStreak != 3 || latest.FailingSinceBuild != "2" {
		t.Fatalf("latest = %+v", latest)
	}
	if latest.PreviousStatus != model.BuildFailed {
		t.Fatalf("previous_status = %q, want failed", latest.PreviousStatus)
	}
	if items[1].FailureStreak != 2 || items[2].FailureStreak != 1 {
		t.Fatalf("streaks = %d/%d", items[1].FailureStreak, items[2].FailureStreak)
	}
}

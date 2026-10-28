package service

import (
	"context"
	"testing"

	"ci-platform/internal/model"
)

func TestTriggerIdempotencyKeyReturnsSameBuild(t *testing.T) {
	svc := newDeleteTestService(t, t.TempDir())
	enq := &fakeEnqueuer{}
	svc.enqueuer = enq
	svc.pipelines = &memPipelineStore{items: map[string]*model.Pipeline{
		"p1": {ID: "p1", Name: "demo", WorkspaceDir: "demo", Steps: []model.Step{{Name: "b", Run: "echo ok"}}},
	}}

	first, err := svc.Trigger(context.Background(), "p1", model.TriggerManual, "alice",
		TriggerOptions{IdempotencyKey: "retry-1"})
	if err != nil {
		t.Fatalf("first trigger: %v", err)
	}
	second, err := svc.Trigger(context.Background(), "p1", model.TriggerManual, "alice",
		TriggerOptions{IdempotencyKey: "retry-1"})
	if err != nil {
		t.Fatalf("second trigger: %v", err)
	}
	if first.ID != second.ID {
		t.Fatalf("same key returned different builds: %s vs %s", first.ID, second.ID)
	}
	if len(enq.jobs) != 1 {
		t.Fatalf("idempotent retry enqueued %d jobs, want 1", len(enq.jobs))
	}

	// 不同用户/不同键必须创建新构建，避免跨用户互相复用。
	other, err := svc.Trigger(context.Background(), "p1", model.TriggerManual, "bob",
		TriggerOptions{IdempotencyKey: "retry-1"})
	if err != nil {
		t.Fatal(err)
	}
	if other.ID == first.ID {
		t.Fatal("idempotency key must be scoped by user")
	}
	if len(enq.jobs) != 2 {
		t.Fatalf("scoped key enqueued %d jobs, want 2", len(enq.jobs))
	}
}

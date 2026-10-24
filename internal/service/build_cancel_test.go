package service

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"ci-platform/internal/executor"
	"ci-platform/internal/model"
	"ci-platform/internal/store"
)

// blockingRunner 执行时阻塞在 ctx 上，用于测试取消。
type blockingRunner struct{}

func (blockingRunner) RunSteps(ctx context.Context, _ executor.RunSpec, out io.Writer) (int, error) {
	select {
	case <-ctx.Done():
		return -1, ctx.Err()
	case <-time.After(30 * time.Second):
		return 0, nil
	}
}

// stubPublisher / stubHub 满足构造参数。
type nopPublisher struct{}

func (nopPublisher) Publish(context.Context, *model.SshPublishConfig, string, io.Writer) error {
	return nil
}

func (nopPublisher) PublishArtifacts(context.Context, *model.SshPublishConfig, string, io.Writer) error {
	return nil
}

func newCancelTestService(t *testing.T, workspaceRoot string) *BuildService {
	t.Helper()
	pipelines := &memPipelineStore{items: map[string]*model.Pipeline{}}
	builds := store.NewBuildStore(t.TempDir())
	settings := &fakeSettingsProvider{settings: model.Settings{QuietPeriodSec: 0}}
	return NewBuildService(
		pipelines, builds, nil, blockingRunner{}, nopPublisher{},
		executor.NewHub(), NewWorkspaceManager(workspaceRoot), settings, nil, nil,
	)
}

// 取消运行中的构建：状态落 aborted，取消后 Run 立即返回。
func TestCancelRunningBuildMarksAborted(t *testing.T) {
	root := t.TempDir()
	svc := newCancelTestService(t, root)
	pipelineID := "p1"
	if err := svc.pipelines.Create(context.Background(), &model.Pipeline{ID: pipelineID, Name: "p1", Steps: []model.Step{{Name: "build", Run: "sleep 30"}}}); err != nil {
		t.Fatalf("create pipeline: %v", err)
	}
	b := &model.Build{ID: "1", PipelineID: pipelineID, Status: model.BuildPending}
	err := svc.builds.CreateNext(context.Background(), b)
	if err != nil {
		t.Fatalf("create build: %v", err)
	}

	done := make(chan struct{})
	go func() {
		svc.Run(context.Background(), Job{PipelineID: pipelineID, BuildID: b.ID})
		close(done)
	}()

	// 等构建进入 running 后取消（放宽超时：全量测试时机器可能较忙）。
	deadline := time.Now().Add(20 * time.Second)
	for {
		cur, err := svc.builds.Get(context.Background(), pipelineID, b.ID)
		if err == nil && cur.Status == model.BuildRunning {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("build never started")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := svc.Cancel(pipelineID, b.ID); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
	cur, err := svc.builds.Get(context.Background(), pipelineID, b.ID)
	if err != nil {
		t.Fatalf("get build: %v", err)
	}
	if cur.Status != model.BuildAborted {
		t.Fatalf("status = %s, want aborted", cur.Status)
	}
}

// 取消排队中的 pending 构建：直接落 aborted；worker 执行时跳过。
func TestCancelPendingBuildSkipsExecution(t *testing.T) {
	svc := newCancelTestService(t, t.TempDir())
	pipelineID := "p2"
	if err := svc.pipelines.Create(context.Background(), &model.Pipeline{ID: pipelineID, Name: "p2", Steps: []model.Step{{Name: "build", Run: "sleep 30"}}}); err != nil {
		t.Fatalf("create pipeline: %v", err)
	}
	b := &model.Build{ID: "1", PipelineID: pipelineID, Status: model.BuildPending}
	err := svc.builds.CreateNext(context.Background(), b)
	if err != nil {
		t.Fatalf("create build: %v", err)
	}
	if err := svc.Cancel(pipelineID, b.ID); err != nil {
		t.Fatalf("cancel pending: %v", err)
	}
	cur, err := svc.builds.Get(context.Background(), pipelineID, b.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if cur.Status != model.BuildAborted {
		t.Fatalf("status = %s, want aborted", cur.Status)
	}
	// worker 执行已取消的构建：直接跳过（不会阻塞 30 秒）。
	done := make(chan struct{})
	go func() {
		svc.Run(context.Background(), Job{PipelineID: pipelineID, BuildID: b.ID})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Run should skip aborted build immediately")
	}
}

// 取消已完成的构建：返回冲突错误。
func TestCancelFinishedBuildRejected(t *testing.T) {
	svc := newCancelTestService(t, t.TempDir())
	pipelineID := "p3"
	if err := svc.pipelines.Create(context.Background(), &model.Pipeline{ID: pipelineID, Name: "p3"}); err != nil {
		t.Fatalf("create pipeline: %v", err)
	}
	b := &model.Build{ID: "1", PipelineID: pipelineID, Status: model.BuildSuccess}
	err := svc.builds.CreateNext(context.Background(), b)
	if err != nil {
		t.Fatalf("create build: %v", err)
	}
	if err := svc.Cancel(pipelineID, b.ID); err == nil {
		t.Fatal("cancel finished build must fail")
	}
}

// 删除任务时，工作区目录也一并删除。
func TestPipelineDeleteRemovesWorkspace(t *testing.T) {
	root := t.TempDir()
	pipelines := &memPipelineStore{items: map[string]*model.Pipeline{}}
	ws := NewWorkspaceManager(root)
	svc := NewPipelineService(pipelines, nil, ws)
	created, err := svc.Create(context.Background(), &model.Pipeline{Name: "p1", WorkspaceDir: "p1", Steps: []model.Step{{Name: "build", Run: "echo ok"}}})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	id := created.ID
	wsDir := filepath.Join(root, "p1")
	if err := os.MkdirAll(filepath.Join(wsDir, "node_modules"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if !ws.Exists("p1") {
		t.Fatal("workspace should exist before delete")
	}
	if err := svc.Delete(context.Background(), id); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := os.Stat(wsDir); !os.IsNotExist(err) {
		t.Fatalf("workspace dir must be removed, stat err = %v", err)
	}
}

// PruneAll：全局保留策略启用时，清理超量的旧构建。
func TestPruneAllCleansOldBuilds(t *testing.T) {
	root := t.TempDir()
	pipelines := &memPipelineStore{items: map[string]*model.Pipeline{}}
	builds := store.NewBuildStore(t.TempDir())
	settings := &fakeSettingsProvider{settings: model.Settings{BuildRetentionMaxPerJob: 2}}
	svc := NewBuildService(pipelines, builds, nil, nil, nopPublisher{}, nil, NewWorkspaceManager(root), settings, nil, nil)
	ctx := context.Background()
	if err := pipelines.Create(ctx, &model.Pipeline{ID: "p1", Name: "p1", Steps: []model.Step{{Name: "b", Run: "echo ok"}}}); err != nil {
		t.Fatalf("create: %v", err)
	}
	for i := 1; i <= 5; i++ {
		b := &model.Build{PipelineID: "p1", Status: model.BuildSuccess, TriggerType: model.TriggerManual, TriggeredBy: "admin"}
		if err := builds.CreateNext(ctx, b); err != nil {
			t.Fatalf("create build %d: %v", i, err)
		}
	}
	svc.PruneAll(ctx)
	list, err := builds.ListByPipeline(ctx, "p1")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("builds after prune = %d, want 2", len(list))
	}
}

// PruneAll：全局策略未启用时不做任何清理。
func TestPruneAllNoopWithoutPolicy(t *testing.T) {
	pipelines := &memPipelineStore{items: map[string]*model.Pipeline{}}
	builds := store.NewBuildStore(t.TempDir())
	svc := NewBuildService(pipelines, builds, nil, nil, nopPublisher{}, nil, NewWorkspaceManager(t.TempDir()), &fakeSettingsProvider{}, nil, nil)
	ctx := context.Background()
	if err := pipelines.Create(ctx, &model.Pipeline{ID: "p1", Name: "p1", Steps: []model.Step{{Name: "b", Run: "echo ok"}}}); err != nil {
		t.Fatalf("create: %v", err)
	}
	for i := 1; i <= 3; i++ {
		b := &model.Build{PipelineID: "p1", Status: model.BuildSuccess, TriggerType: model.TriggerManual, TriggeredBy: "admin"}
		if err := builds.CreateNext(ctx, b); err != nil {
			t.Fatalf("create build %d: %v", i, err)
		}
	}
	svc.PruneAll(ctx)
	list, _ := builds.ListByPipeline(ctx, "p1")
	if len(list) != 3 {
		t.Fatalf("builds after noop prune = %d, want 3", len(list))
	}
}

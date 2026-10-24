package service

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"ci-platform/internal/executor"
	"ci-platform/internal/model"
	"ci-platform/internal/store"
)

// newDeleteTestService 构造带真实 BuildStore 的服务（删除要落到磁盘目录上验证）。
func newDeleteTestService(t *testing.T, jobsRoot string) *BuildService {
	t.Helper()
	pipelines := &memPipelineStore{items: map[string]*model.Pipeline{}}
	builds := store.NewBuildStore(jobsRoot)
	settings := &fakeSettingsProvider{settings: model.Settings{}}
	return NewBuildService(
		pipelines, builds, nil, blockingRunner{}, nopPublisher{},
		executor.NewHub(), NewWorkspaceManager(t.TempDir()), settings, nil, nil,
	)
}

// 删除构建要连同 data/jobs/{pid}/builds/{bid} 目录（build.json / log.txt / artifacts）一起删掉，
// 同时保留任务目录与其它构建。
func TestDeleteBuildRemovesFolder(t *testing.T) {
	jobsRoot := t.TempDir()
	svc := newDeleteTestService(t, jobsRoot)
	ctx := context.Background()
	pid := "p1"
	if err := svc.pipelines.Create(ctx, &model.Pipeline{ID: pid, Name: "p1"}); err != nil {
		t.Fatalf("create pipeline: %v", err)
	}
	// 两个已完成的构建。
	for _, id := range []string{"1", "2"} {
		b := &model.Build{ID: id, PipelineID: pid, Status: model.BuildSuccess}
		if err := svc.builds.CreateNext(ctx, b); err != nil {
			t.Fatalf("create build %s: %v", id, err)
		}
		if err := svc.builds.AppendLog(ctx, pid, id, []byte("hello")); err != nil {
			t.Fatalf("append log: %v", err)
		}
		// 归档产物目录也要能被一起删除。
		artifactDir := filepath.Join(jobsRoot, pid, "builds", id, model.ArtifactDirName)
		if err := os.MkdirAll(artifactDir, 0o755); err != nil {
			t.Fatalf("mkdir artifacts: %v", err)
		}
		if err := os.WriteFile(filepath.Join(artifactDir, "app.bin"), []byte("x"), 0o644); err != nil {
			t.Fatalf("write artifact: %v", err)
		}
	}

	deleted := filepath.Join(jobsRoot, pid, "builds", "2")
	if err := svc.DeleteBuild(ctx, pid, "2"); err != nil {
		t.Fatalf("delete build: %v", err)
	}
	if _, err := os.Stat(deleted); !os.IsNotExist(err) {
		t.Fatalf("build folder must be removed, stat err=%v", err)
	}
	// 其它构建与其产物仍存在。
	kept := filepath.Join(jobsRoot, pid, "builds", "1")
	if _, err := os.Stat(filepath.Join(kept, "build.json")); err != nil {
		t.Fatalf("other build must be kept: %v", err)
	}
	if _, err := os.Stat(filepath.Join(kept, model.ArtifactDirName, "app.bin")); err != nil {
		t.Fatalf("other build artifacts must be kept: %v", err)
	}
	// 列表里也不再有 #2。
	list, err := svc.builds.ListByPipeline(ctx, pid)
	if err != nil {
		t.Fatalf("list builds: %v", err)
	}
	for _, b := range list {
		if b.ID == "2" {
			t.Fatal("deleted build must not be listed")
		}
	}

	// 重复删除：幂等（不报错），方便前端重试。
	if err := svc.DeleteBuild(ctx, pid, "2"); err != nil {
		t.Fatalf("second delete must be no-op, got %v", err)
	}
}

// 运行中 / 排队中的构建不允许删除：文件还在写，删掉会留下 running 僵尸状态。
func TestDeleteRunningBuildRejected(t *testing.T) {
	svc := newDeleteTestService(t, t.TempDir())
	ctx := context.Background()
	pid := "p1"
	if err := svc.pipelines.Create(ctx, &model.Pipeline{ID: pid, Name: "p1"}); err != nil {
		t.Fatalf("create pipeline: %v", err)
	}
	for _, status := range []model.BuildStatus{model.BuildRunning, model.BuildPending} {
		b := &model.Build{ID: string(status), PipelineID: pid, Status: status}
		if err := svc.builds.CreateNext(ctx, b); err != nil {
			t.Fatalf("create build: %v", err)
		}
		if err := svc.DeleteBuild(ctx, pid, b.ID); err == nil {
			t.Fatalf("deleting %s build must be rejected", status)
		}
	}
}

// 删除最新构建后编号不能回退：否则同一任务会出现两个内容不同的 #N。
func TestDeleteBuildKeepsBuildNumberMonotonic(t *testing.T) {
	jobsRoot := t.TempDir()
	svc := newDeleteTestService(t, jobsRoot)
	ctx := context.Background()
	pid := "p1"
	if err := svc.pipelines.Create(ctx, &model.Pipeline{ID: pid, Name: "p1"}); err != nil {
		t.Fatalf("create pipeline: %v", err)
	}
	for _, id := range []string{"1", "2", "3"} {
		b := &model.Build{ID: id, PipelineID: pid, Status: model.BuildSuccess}
		if err := svc.builds.CreateNext(ctx, b); err != nil {
			t.Fatalf("create build %s: %v", id, err)
		}
	}
	// 删掉最新的 #3，下一个编号必须是 4（而不是复用 3）。
	if err := svc.DeleteBuild(ctx, pid, "3"); err != nil {
		t.Fatalf("delete newest build: %v", err)
	}
	next, err := svc.builds.NextBuildID(ctx, pid)
	if err != nil {
		t.Fatalf("next build id: %v", err)
	}
	if next != "4" {
		t.Fatalf("next build id = %q, want 4 (numbers must not be reused)", next)
	}
}

package service

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"
)

// 工作区按任务隔离：同一任务的不同构建复用同一目录，才能支持“不清空工作区”的增量构建。
func TestWorkspacePathIsPerPipeline(t *testing.T) {
	m := NewWorkspaceManager(filepath.Join("root"))
	a := m.Path(Job{PipelineID: "42", BuildID: "1"})
	b := m.Path(Job{PipelineID: "42", BuildID: "2"})
	if a != b {
		t.Fatalf("same pipeline must share workspace: %q != %q", a, b)
	}
	if filepath.Base(a) != "42" {
		t.Fatalf("workspace dir = %q, want base 42", a)
	}
}

// gateRunner 记录同一任务的并发执行情况。
type gateRunner struct {
	mu      sync.Mutex
	running map[string]int
	overlap bool
	done    *sync.WaitGroup
}

func (r *gateRunner) Run(_ context.Context, job Job) {
	r.mu.Lock()
	r.running[job.PipelineID]++
	if r.running[job.PipelineID] > 1 {
		r.overlap = true
	}
	r.mu.Unlock()

	time.Sleep(15 * time.Millisecond)

	r.mu.Lock()
	r.running[job.PipelineID]--
	r.mu.Unlock()
	r.done.Done()
}

// 同一任务的多个构建必须串行执行，避免并发写同一个工作区。
func TestSchedulerSerializesSamePipeline(t *testing.T) {
	var done sync.WaitGroup
	const total = 5
	done.Add(total)
	runner := &gateRunner{running: make(map[string]int), done: &done}
	s := NewScheduler(runner, 10, NewWorkspaceManager(t.TempDir()), time.Hour, time.Hour)

	for i := 0; i < total; i++ {
		go s.process(context.Background(), Job{PipelineID: "p1", BuildID: strconv.Itoa(i)})
	}

	finished := make(chan struct{})
	go func() { done.Wait(); close(finished) }()
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("builds did not finish in time")
	}

	runner.mu.Lock()
	overlap := runner.overlap
	runner.mu.Unlock()
	if overlap {
		t.Fatal("same pipeline builds must not run concurrently")
	}
}

// TTL 清理只删除已删除任务的孤儿工作区；仍存在的任务必须保留工作区以支持增量构建。
func TestCleanupKeepsExistingPipelineWorkspace(t *testing.T) {
	root := t.TempDir()
	keep := filepath.Join(root, "keep")
	orphan := filepath.Join(root, "orphan")
	for _, dir := range []string{keep, orphan} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
		old := time.Now().Add(-2 * time.Hour)
		if err := os.Chtimes(dir, old, old); err != nil {
			t.Fatalf("chtimes %s: %v", dir, err)
		}
	}

	s := NewScheduler(&gateRunner{running: make(map[string]int)}, 10, NewWorkspaceManager(root), time.Hour, time.Hour)
	s.SetWorkspaceKeeper(func() (map[string]bool, error) {
		return map[string]bool{"keep": true}, nil
	})
	s.cleanupOnce()

	if _, err := os.Stat(keep); err != nil {
		t.Fatalf("existing pipeline workspace should be kept: %v", err)
	}
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Fatalf("orphan workspace should be removed, stat err = %v", err)
	}
}

type drainRunner struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (r *drainRunner) Run(_ context.Context, job Job) {
	r.once.Do(func() { close(r.started) })
	<-r.release
}

// 优雅停机：Drain 必须等待运行中的构建结束；超时才返回。
func TestSchedulerDrainWaitsForRunningBuild(t *testing.T) {
	runner := &drainRunner{started: make(chan struct{}), release: make(chan struct{})}
	s := NewScheduler(runner, 10, NewWorkspaceManager(t.TempDir()), time.Hour, time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.Start(ctx, 1)

	if err := s.Enqueue(Job{PipelineID: "p1", BuildID: "1"}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	select {
	case <-runner.started:
	case <-time.After(5 * time.Second):
		t.Fatal("build did not start")
	}

	drained := make(chan struct{})
	go func() {
		s.Drain(context.Background())
		close(drained)
	}()
	select {
	case <-drained:
		t.Fatal("drain must wait while a build is running")
	case <-time.After(300 * time.Millisecond):
	}

	close(runner.release)
	select {
	case <-drained:
	case <-time.After(5 * time.Second):
		t.Fatal("drain should return after the build finishes")
	}
	cancel()
}

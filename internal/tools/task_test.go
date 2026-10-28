package tools

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// 后台安装：Start 立刻返回，任务在后台继续跑，最终 done（页面关掉也不影响）。
func TestStartRunsInBackgroundAndPersistsProgress(t *testing.T) {
	payload := makeTarGz(t, "node-v20.18.0-linux-x64", "#!/bin/sh\necho node\n")
	release := make(chan struct{})
	var once sync.Once
	open := func() { once.Do(func() { close(release) }) }
	// 测试失败时也要放行 handler，否则 srv.Close() 会永远等下去。
	defer open()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		half := len(payload) / 2
		w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
		_, _ = w.Write(payload[:half])
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-release // 卡在下载中途：此时 Start 必须已经返回
		_, _ = w.Write(payload[half:])
	}))
	defer srv.Close()

	withTestURL(t, "20.200.0", srv.URL+"/node.tar.gz")
	installer := NewInstaller(t.TempDir(), "", nil)
	defer installer.Close()

	started := time.Now()
	task, err := installer.Start("nodejs", "20.200.0")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("Start must return immediately, took %s", elapsed)
	}
	if task.State != TaskQueued && task.State != TaskDownloading {
		t.Fatalf("initial state = %s", task.State)
	}
	// 任务在下载中：页面刷新后仍能查到进度
	if _, ok := installer.Task("nodejs", "20.200.0"); !ok {
		t.Fatal("task must be visible while running")
	}
	if len(installer.Tasks()) == 0 {
		t.Fatal("Tasks() must list running task")
	}

	open()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		snapshot, ok := installer.Task("nodejs", "20.200.0")
		if ok && snapshot.State == TaskDone {
			if snapshot.Home == "" {
				t.Fatal("done task must carry home")
			}
			if snapshot.Total != int64(len(payload)) {
				t.Fatalf("total = %d, want %d", snapshot.Total, len(payload))
			}
			if _, ok := installer.Home("nodejs", "20.200.0"); !ok {
				t.Fatal("installed tool must be usable after background finish")
			}
			return
		}
		if ok && snapshot.State == TaskFailed {
			t.Fatalf("task failed: %s", snapshot.Error)
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("task did not finish, last=%+v", installer.Tasks())
}

// 同一版本并发点击只下载一次（单飞）。
func TestStartIsSingleFlight(t *testing.T) {
	payload := makeTarGz(t, "apache-maven-3.9.9", "#!/bin/sh\necho mvn\n")
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		time.Sleep(100 * time.Millisecond)
		_, _ = w.Write(payload)
	}))
	defer srv.Close()

	withTestURL(t, "3.9.9", srv.URL+"/maven.tar.gz")
	installer := NewInstaller(t.TempDir(), "", nil)
	defer installer.Close()

	var first Task
	for n := 0; n < 5; n++ {
		task, err := installer.Start("maven", "3.9.9")
		if err != nil {
			t.Fatalf("start: %v", err)
		}
		if n == 0 {
			first = task
		}
	}
	if _, err := installer.Ensure(context.Background(), "maven", "3.9.9"); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Fatalf("expected 1 download for 5 clicks, got %d (first=%+v)", got, first)
	}
}

// 失败要记录原因，且可以重试（重试清掉旧状态）。
func TestFailedTaskRecordsErrorAndCanRetry(t *testing.T) {
	var fail int32 = 1
	ok := makeTarGz(t, "node-v20.201.0-linux-x64", "#!/bin/sh\necho node\n")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.LoadInt32(&fail) == 1 {
			http.Error(w, "boom", http.StatusBadGateway)
			return
		}
		_, _ = w.Write(ok)
	}))
	defer srv.Close()

	withTestURL(t, "20.201.0", srv.URL+"/node.tar.gz")
	installer := NewInstaller(t.TempDir(), "", nil)
	defer installer.Close()

	task, err := installer.Start("nodejs", "20.201.0")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if task.State == TaskFailed {
		t.Fatal("first snapshot should be queued")
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		snapshot, _ := installer.Task("nodejs", "20.201.0")
		if snapshot.State == TaskFailed {
			if snapshot.Error == "" {
				t.Fatal("failed task must carry error message")
			}
			break
		}
		time.Sleep(30 * time.Millisecond)
	}
	if _, ok := installer.Home("nodejs", "20.201.0"); ok {
		t.Fatal("failed install must not be marked installed")
	}

	atomic.StoreInt32(&fail, 0)
	home, err := installer.Ensure(context.Background(), "nodejs", "20.201.0")
	if err != nil {
		t.Fatalf("retry after failure must succeed: %v", err)
	}
	if home == "" {
		t.Fatal("retry must return home")
	}
}

// 构建等待安装时，构建取消（ctx）能立刻返回，后台安装继续。
func TestEnsureHonorsContextCancellation(t *testing.T) {
	payload := makeTarGz(t, "node-v20.202.0-linux-x64", "#!/bin/sh")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 慢响应：确保 Ensure 的 200ms 超时先触发。
		w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
		_, _ = w.Write(payload[:len(payload)/2])
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		time.Sleep(300 * time.Millisecond)
		_, _ = w.Write(payload[len(payload)/2:])
	}))
	defer srv.Close()

	withTestURL(t, "20.202.0", srv.URL+"/node.tar.gz")
	installer := NewInstaller(t.TempDir(), "", nil)
	defer installer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, err := installer.Ensure(ctx, "nodejs", "20.202.0"); err == nil {
		t.Fatal("Ensure must fail when context is cancelled")
	}
	// 取消构建不影响后台安装本身：任务仍在（由 installer.Close 在退出时中止）
	if _, ok := installer.Task("nodejs", "20.202.0"); !ok {
		t.Fatal("background task must survive build cancellation")
	}
	// 后台任务随后自行完成（页面/构建都不再关心它）
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if snapshot, _ := installer.Task("nodejs", "20.202.0"); snapshot.State == TaskDone || snapshot.State == TaskFailed {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("background task should finish on its own: %+v", installer.Tasks())
}

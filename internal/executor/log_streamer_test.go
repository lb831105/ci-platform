package executor

import (
	"sync"
	"testing"
)

// fakeSubscriber 记录收到的日志帧，用于验证 Hub 广播与订阅生命周期。
type fakeSubscriber struct {
	mu     sync.Mutex
	frames []string
	closed bool
}

func (f *fakeSubscriber) Enqueue(data []byte) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return false
	}
	f.frames = append(f.frames, string(data))
	return true
}

func (f *fakeSubscriber) snapshot() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.frames))
	copy(out, f.frames)
	return out
}

// 阶段 2 验收：Hub 能让 key 下所有订阅者收到广播，注销后不再收到。
func TestHubBroadcastAndUnregister(t *testing.T) {
	hub := NewHub()
	key := "1703123456/1"

	a := &fakeSubscriber{}
	bubble := &fakeSubscriber{}
	hub.Register(key, a)
	hub.Register(key, bubble)

	if !hub.HasSubscribers(key) {
		t.Fatalf("hub should report subscribers")
	}

	hub.Broadcast(key, []byte("hello\n"))
	hub.Broadcast(key, []byte("world\n"))

	if got := a.snapshot(); len(got) != 2 || got[0] != "hello\n" || got[1] != "world\n" {
		t.Fatalf("subscriber a frames = %v", got)
	}
	if got := bubble.snapshot(); len(got) != 2 {
		t.Fatalf("subscriber b frames = %v", got)
	}

	hub.Unregister(key, a)
	hub.Broadcast(key, []byte("after-unregister\n"))

	if got := a.snapshot(); len(got) != 2 {
		t.Fatalf("unregistered subscriber must not receive frames, got %v", got)
	}
	if got := bubble.snapshot(); len(got) != 3 {
		t.Fatalf("remaining subscriber should receive frame, got %v", got)
	}

	hub.Unregister(key, bubble)
	if hub.HasSubscribers(key) {
		t.Fatalf("hub should recycle empty key set")
	}
}

// 无订阅者时广播不得 panic，也不应阻塞。
func TestHubBroadcastWithoutSubscribers(t *testing.T) {
	hub := NewHub()
	hub.Broadcast("nobody/1", []byte("noop\n"))
	if hub.HasSubscribers("nobody/1") {
		t.Fatalf("unexpected subscribers")
	}
}

// HubWriter 必须实现 io.Writer，供 io.MultiWriter 与文件写入并行。
func TestHubWriterFansOut(t *testing.T) {
	hub := NewHub()
	key := "1/1"
	sub := &fakeSubscriber{}
	hub.Register(key, sub)

	writer := NewHubWriter(hub, key)
	n, err := writer.Write([]byte("chunk"))
	if err != nil || n != len("chunk") {
		t.Fatalf("write = (%d, %v)", n, err)
	}
	if got := sub.snapshot(); len(got) != 1 || got[0] != "chunk" {
		t.Fatalf("writer did not fan out: %v", got)
	}
}

// 并发订阅/广播不应触发 data race（配合 -race 运行）。
func TestHubConcurrentAccess(t *testing.T) {
	hub := NewHub()
	key := "9/9"

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sub := &fakeSubscriber{}
			hub.Register(key, sub)
			for j := 0; j < 20; j++ {
				hub.Broadcast(key, []byte("x"))
			}
			hub.Unregister(key, sub)
		}()
	}
	wg.Wait()
}

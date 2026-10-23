package executor

import (
	"sync"
)

// Subscriber 是日志订阅者抽象。
// 用接口而非 *websocket.Conn，避免 executor 反向依赖 handler 层。
type Subscriber interface {
	// Enqueue 非阻塞投递一帧日志。返回 false 表示该订阅者已关闭或缓冲已满（主动丢弃慢消费者）。
	Enqueue(data []byte) bool
}

// Hub 按 pipeline_id/build_id 维度维护订阅关系并广播日志。
// 广播前拷贝订阅者快照，出锁再写，禁止锁内做慢 IO。
type Hub struct {
	mu   sync.RWMutex
	subs map[string]map[Subscriber]struct{}
}

// NewHub 创建日志广播中心。
func NewHub() *Hub {
	return &Hub{subs: make(map[string]map[Subscriber]struct{})}
}

// Register 注册一个订阅者到指定 key。
func (h *Hub) Register(key string, s Subscriber) {
	h.mu.Lock()
	defer h.mu.Unlock()
	set, ok := h.subs[key]
	if !ok {
		set = make(map[Subscriber]struct{})
		h.subs[key] = set
	}
	set[s] = struct{}{}
}

// Unregister 取消订阅，key 下无订阅者时回收 map。
func (h *Hub) Unregister(key string, s Subscriber) {
	h.mu.Lock()
	defer h.mu.Unlock()
	set, ok := h.subs[key]
	if !ok {
		return
	}
	delete(set, s)
	if len(set) == 0 {
		delete(h.subs, key)
	}
}

// Broadcast 向 key 下所有订阅者投递日志。
func (h *Hub) Broadcast(key string, data []byte) {
	h.mu.RLock()
	set, ok := h.subs[key]
	if !ok || len(set) == 0 {
		h.mu.RUnlock()
		return
	}
	// 拷贝快照后再投递，避免持锁执行 Enqueue。
	snapshot := make([]Subscriber, 0, len(set))
	for s := range set {
		snapshot = append(snapshot, s)
	}
	h.mu.RUnlock()

	for _, s := range snapshot {
		s.Enqueue(data)
	}
}

// HasSubscribers 判断 key 下是否存在活跃订阅者。
func (h *Hub) HasSubscribers(key string) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.subs[key]) > 0
}

// HubWriter 把 Write 转成 Hub 广播，实现 io.Writer，
// 便于用 io.MultiWriter 让同一份日志同时落盘与推送。
type HubWriter struct {
	hub *Hub
	key string
}

// NewHubWriter 创建面向指定 key 的广播 writer。
func NewHubWriter(h *Hub, key string) *HubWriter {
	return &HubWriter{hub: h, key: key}
}

// Write 实现 io.Writer，永远返回成功（日志不阻塞构建）。
func (w *HubWriter) Write(p []byte) (int, error) {
	w.hub.Broadcast(w.key, p)
	return len(p), nil
}

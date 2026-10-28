package pkg

import (
	"sync"
	"time"
)

// Dedupe 是一个带 TTL 的内存去重器，用于抵消「同一次投递被重复处理」。
//
// 典型场景：GitLab 在 Webhooks 页面点「Resend」、代理层重试、代码平台自身的重复投递。
// 没有它，一次点击就会多跑一次构建（生产环境就是多一次发布）。
// 只保留最近 N 个键，避免长时间运行后内存无限增长。
type Dedupe struct {
	mu    sync.Mutex
	ttl   time.Duration
	max   int
	seen  map[string]time.Time
	order []string // 粗略的插入顺序，用于淘汰最旧的键
}

// NewDedupe 创建去重器；ttl <= 0 时默认 5 分钟，max <= 0 时默认 1024。
func NewDedupe(ttl time.Duration, max int) *Dedupe {
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	if max <= 0 {
		max = 1024
	}
	return &Dedupe{ttl: ttl, max: max, seen: make(map[string]time.Time, max)}
}

// First 判断 key 是否是 TTL 窗口内第一次出现。true 表示应当处理，
// false 表示重复投递（调用方应直接忽略）。
func (d *Dedupe) First(key string) bool {
	if key == "" {
		return true // 没有投递标识时无法去重，按首次处理
	}
	now := time.Now()
	d.mu.Lock()
	defer d.mu.Unlock()

	d.pruneLocked(now)
	if _, ok := d.seen[key]; ok {
		return false
	}
	d.seen[key] = now
	d.order = append(d.order, key)
	// 超出容量时按插入顺序淘汰（key 可能已被 prune 删除，跳过即可）。
	for len(d.seen) > d.max && len(d.order) > 0 {
		oldest := d.order[0]
		d.order = d.order[1:]
		delete(d.seen, oldest)
	}
	return true
}

// pruneLocked 清理过期键；order 与 seen 保持近似同步即可。
func (d *Dedupe) pruneLocked(now time.Time) {
	if len(d.seen) == 0 {
		return
	}
	kept := d.order[:0]
	for _, key := range d.order {
		at, ok := d.seen[key]
		if !ok {
			continue
		}
		if now.Sub(at) > d.ttl {
			delete(d.seen, key)
			continue
		}
		kept = append(kept, key)
	}
	d.order = kept
}

// Forget 移除一个键；用于「首次处理失败」时允许代码平台重试。
// order 中残留的键会在 prune 时跳过，不影响容量。
func (d *Dedupe) Forget(key string) {
	if key == "" {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.seen, key)
}

// Len 返回当前保留的键数量（测试用）。
func (d *Dedupe) Len() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.seen)
}

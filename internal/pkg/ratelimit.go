package pkg

import (
	"context"
	"net"
	"net/http"
	"sync"
	"time"
)

// RateLimiter 是进程内固定窗口限流器，用于登录与 Webhook 防暴力/防刷。
// 轻量、无外部依赖；多实例部署时应改用 Redis 等共享存储。
type RateLimiter struct {
	mu      sync.Mutex
	limit   int
	window  time.Duration
	entries map[string]*rateEntry
	calls   int
}

type rateEntry struct {
	start time.Time
	count int
}

// NewRateLimiter 创建限流器；limit <= 0 时不限制。
func NewRateLimiter(limit int, window time.Duration) *RateLimiter {
	if window <= 0 {
		window = time.Minute
	}
	return &RateLimiter{limit: limit, window: window, entries: make(map[string]*rateEntry)}
}

// Allow 返回 key 在当前窗口内是否仍有配额。
func (l *RateLimiter) Allow(key string) bool {
	if l == nil || l.limit <= 0 {
		return true
	}
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls++
	if l.calls%1024 == 0 || len(l.entries) > 10000 {
		for k, entry := range l.entries {
			if now.Sub(entry.start) >= l.window {
				delete(l.entries, k)
			}
		}
	}
	entry, ok := l.entries[key]
	if !ok || now.Sub(entry.start) >= l.window {
		l.entries[key] = &rateEntry{start: now, count: 1}
		return true
	}
	if entry.count >= l.limit {
		return false
	}
	entry.count++
	return true
}

type clientIPKey struct{}

// WithClientIP 把已经过代理信任策略解析的真实 IP 放进 context。
func WithClientIP(ctx context.Context, ip string) context.Context {
	return context.WithValue(ctx, clientIPKey{}, ip)
}

// ClientIP 返回客户端 IP：优先使用 RealIP 中间件解析后的值，否则用直连地址。
// 默认不信任 X-Forwarded-For，避免客户端伪造 IP 绕过限流；只有显式开启
// trust-proxy 时中间件才会采用代理头。
func ClientIP(r *http.Request) string {
	if r == nil {
		return ""
	}
	if ip, ok := r.Context().Value(clientIPKey{}).(string); ok && ip != "" {
		return ip
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return r.RemoteAddr
}

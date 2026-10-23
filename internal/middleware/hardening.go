package middleware

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync/atomic"

	"ci-platform/internal/pkg"
)

type requestIDKey struct{}

// RequestID 为每个请求生成/透传 X-Request-ID，便于把访问日志与前端错误关联。
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimSpace(r.Header.Get("X-Request-ID"))
		if id == "" || len(id) > 64 || strings.ContainsAny(id, "\r\n") {
			id = newRequestID()
		}
		w.Header().Set("X-Request-ID", id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDKey{}, id)))
	})
}

// RequestIDFromContext 返回请求 ID，未注入时返回空串。
func RequestIDFromContext(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}

var fallbackRequestID atomic.Uint64

func newRequestID() string {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err == nil {
		return hex.EncodeToString(buf[:])
	}
	return fmt.Sprintf("fallback-%d", fallbackRequestID.Add(1))
}

// RealIP 在显式信任反向代理时解析 X-Forwarded-For / X-Real-IP。
// 仅应在服务不直接暴露公网、前置代理会覆盖该头时开启；否则客户端可伪造 IP。
func RealIP(trustProxy bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if trustProxy {
				if ip := forwardedIP(r); ip != "" {
					r = r.WithContext(pkg.WithClientIP(r.Context(), ip))
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

func forwardedIP(r *http.Request) string {
	if raw := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); raw != "" {
		for _, part := range strings.Split(raw, ",") {
			candidate := strings.TrimSpace(part)
			if net.ParseIP(candidate) != nil {
				return candidate
			}
		}
	}
	if candidate := strings.TrimSpace(r.Header.Get("X-Real-IP")); net.ParseIP(candidate) != nil {
		return candidate
	}
	return ""
}

// SecurityHeaders 设置浏览器侧的基础安全响应头。
// CSP 允许 Element Plus 的内联样式与 WebSocket 日志连接；不使用 eval。
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		w.Header().Set("Content-Security-Policy",
			"default-src 'self'; "+
				"script-src 'self'; "+
				"style-src 'self' 'unsafe-inline'; "+
				"img-src 'self' data: blob:; "+
				"font-src 'self' data:; "+
				"connect-src 'self' ws: wss:; "+
				"object-src 'none'; "+
				"base-uri 'self'; "+
				"form-action 'self'; "+
				"frame-ancestors 'none'")
		if r.TLS != nil {
			w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		next.ServeHTTP(w, r)
	})
}

package middleware

import (
	"bufio"
	"log"
	"net"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"ci-platform/internal/pkg"
	"ci-platform/internal/service"
)

// Recover 捕获 panic，统一转为业务错误响应，避免单个请求打挂进程。
func Recover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("panic recovered: %v\n%s", rec, debug.Stack())
				pkg.JSON(w, pkg.CodeInternal, "internal server error", nil)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// CORS 允许前端独立域名/端口访问（开发期 Vite 走 proxy 时同样适用）。
// origins 为空或包含 "*" 时保持兼容的开放策略；生产可配置精确来源。
func CORS(origins []string) func(http.Handler) http.Handler {
	allowAll := len(origins) == 0
	allowed := make(map[string]bool, len(origins))
	for _, origin := range origins {
		origin = strings.TrimSpace(origin)
		if origin == "*" {
			allowAll = true
		}
		if origin != "" {
			allowed[origin] = true
		}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if allowAll {
				w.Header().Set("Access-Control-Allow-Origin", "*")
			} else if origin != "" && allowed[origin] {
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Add("Vary", "Origin")
			}
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Webhook-Secret")
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// statusRecorder 记录实际写出的状态码与字节数。
type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	n, err := s.ResponseWriter.Write(b)
	s.bytes += n
	return n, err
}

// Hijack 透传底层连接，保证 WebSocket 升级可用。
func (s *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hijacker, ok := s.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, http.ErrNotSupported
	}
	return hijacker.Hijack()
}

// Flush 透传，保证流式响应可用。
func (s *statusRecorder) Flush() {
	if flusher, ok := s.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

// Auth 校验 Bearer token（或 WebSocket 的 token 查询参数）。
// 认证服务关闭时直接放行，保证本地开发无感。
func Auth(auth *service.AuthService) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if auth == nil || !auth.Enabled() {
				next.ServeHTTP(w, r.WithContext(pkg.WithUser(r.Context(), "anonymous")))
				return
			}
			username, ok := auth.Verify(pkg.BearerToken(r))
			if !ok {
				pkg.JSON(w, pkg.CodeUnauthorized, "登录状态已失效，请重新登录", nil)
				return
			}
			next.ServeHTTP(w, r.WithContext(pkg.WithUser(r.Context(), username)))
		})
	}
}

// Logger 记录访问日志。
func Logger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(rec, r)
		log.Printf("[%s] %s %s %d %dB %s",
			RequestIDFromContext(r.Context()), r.Method, r.URL.Path, rec.status, rec.bytes,
			time.Since(start).Round(time.Millisecond))
	})
}

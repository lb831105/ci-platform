package middleware

import (
	"bytes"
	"net/http"
	"strconv"
	"strings"

	"ci-platform/internal/model"
	"ci-platform/internal/pkg"
)

// 审计动作映射：METHOD → 动作名。
var auditActions = map[string]string{
	http.MethodPost:   "create",
	http.MethodPut:    "update",
	http.MethodDelete: "delete",
	http.MethodPatch:  "update",
}

// Audit 记录变更类 API 操作（登录/登出/构建触发/任务与配置增删改）。
// 必须挂在 Auth 中间件之后（取当前用户）；查询类请求（GET）不记录。
// 登录接口的用户名取自上下文（Login 成功后写入），密码等请求体绝不落盘。
func Audit(audit AuditPoster) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			action, ok := auditActions[r.Method]
			if !ok {
				next.ServeHTTP(w, r)
				return
			}
			// 包装 ResponseWriter 以捕获业务码与登录用户名（平台恒返回 HTTP 200，
			// 成败在 body 的 code 字段；登录成功后 username 在响应体中）。
			rw := &auditResponseWriter{ResponseWriter: w}
			// 用户名必须在 next 之前读：挂载点在 Auth 之后，请求进入本中间件时
			// context 已含用户名（内层写入的 context 不会传回外层）。
			user := pkg.UserFromContext(r.Context())
			next.ServeHTTP(rw, r)
			if r.URL.Path == "/api/auth/login" && rw.body.Len() > 0 {
				if name := parseLoginUsername(rw.body.Bytes()); name != "" {
					user = name
				}
			}
			// 登录/登出用专门的动作名，其余按 METHOD 归类。
			switch r.URL.Path {
			case "/api/auth/login":
				action = model.AuditLogin
			case "/api/auth/logout":
				action = model.AuditLogout
			}
			audit.Post(model.AuditEntry{
				User:   user,
				Action: action,
				Target: r.Method + " " + r.URL.Path,
				Status: rw.code,
				IP:     pkg.ClientIP(r),
			})
		})
	}
}

// AuditPoster 由 AuditService 实现：追加审计记录（异步安全）。
type AuditPoster interface {
	Post(e model.AuditEntry)
}

type auditResponseWriter struct {
	http.ResponseWriter
	code       int
	headerSeen bool         // 已捕获 HTTP 状态码
	bodySeen   bool         // 已解析响应体业务码
	body       bytes.Buffer // 响应体前缀（捕获业务码与登录用户名）
}

func (w *auditResponseWriter) WriteHeader(code int) {
	if !w.headerSeen {
		w.headerSeen = true
		w.code = code
	}
	w.ResponseWriter.WriteHeader(code)
}

// Write 拦截响应体：HTTP 200 时以 body 中的业务码为准（成败在 body），
// 非 200 用 HTTP 状态码。
func (w *auditResponseWriter) Write(p []byte) (int, error) {
	if w.body.Len() < 512 {
		n := 512 - w.body.Len()
		if n > len(p) {
			n = len(p)
		}
		w.body.Write(p[:n])
	}
	if !w.bodySeen && len(p) > 0 {
		w.bodySeen = true
		// HTTP 200 时以 body 中的业务码为准（含 0=成功）。
		if w.code == 0 || w.code == http.StatusOK {
			w.code = parseResponseCode(p)
		}
	}
	return w.ResponseWriter.Write(p)
}

// parseResponseCode 从统一响应体前缀提取业务码；非 JSON 或缺失视为 0。
func parseResponseCode(body []byte) int {
	return extractJSONInt(body, `"code":`)
}

// Unwrap 暴露底层 ResponseWriter，使 http.ResponseController 仍能访问
// Flush / Hijack / SetWriteDeadline 等可选能力（包装后不丢能力）。
func (w *auditResponseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// Flush 透传 http.Flusher：下游 handler 可能对 ResponseWriter 做 Flush 类型断言。
func (w *auditResponseWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// parseLoginUsername 从登录响应体提取用户名。
func parseLoginUsername(body []byte) string {
	const prefix = `"username":`
	s := string(body)
	i := strings.Index(s, prefix)
	if i < 0 {
		return ""
	}
	s = strings.TrimLeft(s[i+len(prefix):], " ")
	if !strings.HasPrefix(s, `"`) {
		return ""
	}
	s = s[1:]
	end := strings.Index(s, `"`)
	if end < 0 {
		return ""
	}
	return s[:end]
}

// extractJSONInt 从响应体前缀提取数字字段值；缺失/非法返回 0。
func extractJSONInt(body []byte, prefix string) int {
	if len(body) > 64 {
		body = body[:64]
	}
	s := string(body)
	i := strings.Index(s, prefix)
	if i < 0 {
		return 0
	}
	s = strings.TrimLeft(s[i+len(prefix):], " ")
	j := 0
	for j < len(s) && s[j] >= '0' && s[j] <= '9' {
		j++
	}
	if j == 0 {
		return 0
	}
	n, err := strconv.Atoi(s[:j])
	if err != nil {
		return 0
	}
	return n
}

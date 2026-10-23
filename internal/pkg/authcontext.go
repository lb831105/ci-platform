package pkg

import (
	"context"
	"net/http"
	"strings"
)

// userCtxKey 是请求上下文中登录用户名的私有 key 类型。
type userCtxKey struct{}

// WithUser 把登录用户名写入请求上下文。
func WithUser(ctx context.Context, username string) context.Context {
	return context.WithValue(ctx, userCtxKey{}, username)
}

// UserFromContext 读取登录用户名，未登录（认证关闭）时返回空串。
func UserFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(userCtxKey{}).(string); ok {
		return v
	}
	return ""
}

// BearerToken 从 Authorization 头或 token 查询参数中提取 token。
// 查询参数用于 WebSocket（浏览器无法为 WS 握手设置自定义头）。
func BearerToken(r *http.Request) string {
	header := r.Header.Get("Authorization")
	if header != "" {
		const prefix = "Bearer "
		if strings.HasPrefix(header, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(header, prefix))
		}
		return strings.TrimSpace(header)
	}
	return r.URL.Query().Get("token")
}

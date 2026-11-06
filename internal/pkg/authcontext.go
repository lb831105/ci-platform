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

// tokenScopesCtxKey 是 API Token 权限范围的私有 key。
type tokenScopesCtxKey struct{}

// apiTokenCtxKey 标记当前请求由 API Token 认证（而不是登录会话）。
type apiTokenCtxKey struct{}

// WithAPIToken 标记当前请求是 API Token 认证。
func WithAPIToken(ctx context.Context) context.Context {
	return context.WithValue(ctx, apiTokenCtxKey{}, true)
}

// IsAPIToken 判断当前请求是否由 API Token 认证。
func IsAPIToken(ctx context.Context) bool {
	v, _ := ctx.Value(apiTokenCtxKey{}).(bool)
	return v
}

// WithTokenScopes 把 API Token 的 scopes 写入请求上下文。
// 登录会话不设置该值，表示不受 Token scopes 限制。
func WithTokenScopes(ctx context.Context, scopes []string) context.Context {
	if len(scopes) == 0 {
		return ctx
	}
	return context.WithValue(ctx, tokenScopesCtxKey{}, append([]string(nil), scopes...))
}

// TokenScopesFromContext 返回 Token scopes 以及是否存在 scope 限制。
func TokenScopesFromContext(ctx context.Context) ([]string, bool) {
	scopes, ok := ctx.Value(tokenScopesCtxKey{}).([]string)
	return scopes, ok && len(scopes) > 0
}

// TokenAllows 判断当前请求（若由 API Token 认证）是否允许某项权限。
// 登录会话或旧 Token（无 scopes）返回 true，保持向后兼容。
func TokenAllows(ctx context.Context, permission string) bool {
	scopes, restricted := TokenScopesFromContext(ctx)
	if !restricted {
		return true
	}
	for _, scope := range scopes {
		if scope == "*" || scope == permission {
			return true
		}
	}
	return false
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

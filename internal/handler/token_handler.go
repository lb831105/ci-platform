package handler

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"ci-platform/internal/pkg"
	"ci-platform/internal/service"
)

// TokenHandler 暴露当前用户的 API Token 管理接口。
type TokenHandler struct {
	tokens *service.APITokenService
}

// NewTokenHandler 构造令牌接口处理器。
func NewTokenHandler(tokens *service.APITokenService) *TokenHandler {
	return &TokenHandler{tokens: tokens}
}

// createTokenRequest 是创建令牌请求体。
type createTokenRequest struct {
	Name          string   `json:"name"`
	ExpiresInDays int      `json:"expires_in_days"`
	Scopes        []string `json:"scopes"`
}

// List GET /api/profile/tokens
func (h *TokenHandler) List(w http.ResponseWriter, r *http.Request) {
	username := pkg.UserFromContext(r.Context())
	list, err := h.tokens.List(username)
	if err != nil {
		pkg.Fail(w, err)
		return
	}
	pkg.OK(w, list)
}

// Create POST /api/profile/tokens
func (h *TokenHandler) Create(w http.ResponseWriter, r *http.Request) {
	// 限制 Token 不能通过自身再创建新 Token：否则一个只有 job.read 的令牌
	// 可以自助铸造 * 权限令牌，scopes 形同虚设。令牌管理只允许登录会话操作。
	if pkg.IsAPIToken(r.Context()) {
		pkg.Fail(w, pkg.NewError(pkg.CodeForbidden, "API Token 不能创建新的访问令牌，请使用登录会话操作"))
		return
	}
	username := pkg.UserFromContext(r.Context())
	var in createTokenRequest
	if err := decodeJSON(w, r, &in); err != nil {
		pkg.Fail(w, err)
		return
	}
	result, err := h.tokens.Create(username, in.Name, in.ExpiresInDays, in.Scopes)
	if err != nil {
		pkg.Fail(w, err)
		return
	}
	pkg.OK(w, result)
}

// Delete DELETE /api/profile/tokens/{id}
func (h *TokenHandler) Delete(w http.ResponseWriter, r *http.Request) {
	if pkg.IsAPIToken(r.Context()) {
		pkg.Fail(w, pkg.NewError(pkg.CodeForbidden, "API Token 不能吊销访问令牌，请使用登录会话操作"))
		return
	}
	username := pkg.UserFromContext(r.Context())
	id := chi.URLParam(r, "id")
	if id == "" {
		pkg.Fail(w, pkg.ErrBadRequest("缺少令牌 ID"))
		return
	}
	if err := h.tokens.Revoke(username, id); err != nil {
		pkg.Fail(w, err)
		return
	}
	pkg.OK(w, nil)
}

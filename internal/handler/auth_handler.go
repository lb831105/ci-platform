package handler

import (
	"net/http"
	"strings"
	"time"

	"ci-platform/internal/model"
	"ci-platform/internal/pkg"
	"ci-platform/internal/service"
)

// AuthHandler 处理登录、登出与会话查询。
type AuthHandler struct {
	auth         *service.AuthService
	settings     *service.SettingsService
	rbac         *service.RBACService
	loginLimiter *pkg.RateLimiter
}

// SetRBAC 注入 RBAC 服务（供 /auth/me 返回用户有效权限，前端据此隐藏入口）。
func (h *AuthHandler) SetRBAC(rbac *service.RBACService) {
	h.rbac = rbac
}

// NewAuthHandler 构造认证接口处理器。settings 用于持久化密码修改。
func NewAuthHandler(auth *service.AuthService, settings *service.SettingsService) *AuthHandler {
	return &AuthHandler{
		auth:         auth,
		settings:     settings,
		loginLimiter: pkg.NewRateLimiter(10, 5*time.Minute),
	}
}

// loginRequest 是登录请求体。
type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// loginResponse 是登录成功响应体。
type loginResponse struct {
	Token     string    `json:"token"`
	Username  string    `json:"username"`
	ExpiresAt time.Time `json:"expires_at"`
}

// statusResponse 描述认证开关与当前请求的登录态，供前端路由守卫初始化。
type statusResponse struct {
	Enabled       bool     `json:"enabled"`
	Authenticated bool     `json:"authenticated"`
	Username      string   `json:"username"`
	Permissions   []string `json:"permissions,omitempty"` // 有效权限（前端隐藏入口用）
	// 任务角色的 Pattern 规则：前端据此判断「这一行任务」的按钮是否该显示，
	// 避免用全局权限一刀切（只被授予任务角色的用户会被误判为无权）。
	JobRoles []model.JobRoleRule `json:"job_roles,omitempty"`
}

// jobRolesOf 返回用户的任务角色规则；rbac 未注入时返回 nil。
func (h *AuthHandler) jobRolesOf(username string) []model.JobRoleRule {
	if h.rbac == nil {
		return nil
	}
	return h.rbac.JobRoleRules(username)
}

// permissionsOf 返回用户的有效权限；rbac 未注入时返回 nil（不隐藏任何入口）。
func (h *AuthHandler) permissionsOf(username string) []string {
	if h.rbac == nil {
		return nil
	}
	return h.rbac.EffectivePermissions(username)
}

// Login POST /api/auth/login
func (h *AuthHandler) Login(w http.ResponseWriter, r *http.Request) {
	var in loginRequest
	if err := decodeJSON(w, r, &in); err != nil {
		pkg.Fail(w, err)
		return
	}
	if h.auth.Enabled() && !h.loginLimiter.Allow(pkg.ClientIP(r)) {
		pkg.Fail(w, pkg.NewError(pkg.CodeTooManyRequests, "登录尝试过于频繁，请稍后再试"))
		return
	}
	token, username, expiresAt, err := h.auth.Login(strings.TrimSpace(in.Username), in.Password)
	if err != nil {
		pkg.Fail(w, err)
		return
	}
	pkg.OK(w, loginResponse{Token: token, Username: username, ExpiresAt: expiresAt})
}

// Logout POST /api/auth/logout
func (h *AuthHandler) Logout(w http.ResponseWriter, r *http.Request) {
	h.auth.Logout(pkg.BearerToken(r))
	pkg.OK(w, nil)
}

// Me GET /api/auth/me
func (h *AuthHandler) Me(w http.ResponseWriter, r *http.Request) {
	username, _ := h.auth.Verify(pkg.BearerToken(r))
	pkg.OK(w, statusResponse{
		Enabled:       h.auth.Enabled(),
		Authenticated: true,
		Username:      username,
		Permissions:   h.permissionsOf(username),
		JobRoles:      h.jobRolesOf(username),
	})
}

// Status GET /api/auth/status
//
// 公开接口：前端启动时调用，用于判断是否需要跳转登录页。
// 认证关闭时直接返回 authenticated=true，避免无谓的登录流程。
func (h *AuthHandler) Status(w http.ResponseWriter, r *http.Request) {
	if !h.auth.Enabled() {
		pkg.OK(w, statusResponse{
			Enabled:       false,
			Authenticated: true,
			Username:      "anonymous",
			Permissions:   h.permissionsOf(""),
		})
		return
	}
	username, ok := h.auth.Verify(pkg.BearerToken(r))
	perms := h.permissionsOf(username)
	if !ok {
		// 未登录：返回匿名主体被授予的权限（通常为空）。
		perms = h.permissionsOf(model.SubjectAnonymous)
	}
	pkg.OK(w, statusResponse{Enabled: true, Authenticated: ok, Username: username, Permissions: perms})
}

// changePasswordRequest 是修改密码请求体。
type changePasswordRequest struct {
	OldPassword string `json:"old_password"`
	NewPassword string `json:"new_password"`
}

// ChangePassword POST /api/auth/password
// ChangePassword POST /api/auth/password
// 修改「当前登录账号」的密码：内置管理员与平台内用户都走这个接口，
// 由 AuthService 按账号类型分流到 settings.json / rbac.json。
func (h *AuthHandler) ChangePassword(w http.ResponseWriter, r *http.Request) {
	var in changePasswordRequest
	if err := decodeJSON(w, r, &in); err != nil {
		pkg.Fail(w, err)
		return
	}
	username := pkg.UserFromContext(r.Context())
	if h.auth.Enabled() {
		if err := h.auth.ChangePassword(username, in.OldPassword, in.NewPassword, pkg.BearerToken(r)); err != nil {
			pkg.Fail(w, err)
			return
		}
		// 只有内置管理员的密码存在系统配置里，需要同步落盘保证重启后生效；
		// 平台内用户的 hash 已由 RBACService 写入 rbac.json，不必动 settings。
		if h.auth.IsAdmin(username) {
			current := h.settings.Current()
			current.AuthPassword = in.NewPassword
			if _, err := h.settings.Update(current); err != nil {
				pkg.Fail(w, err)
				return
			}
		}
	}
	pkg.OK(w, nil)
}

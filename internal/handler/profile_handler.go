package handler

import (
	"net/http"

	"ci-platform/internal/pkg"
	"ci-platform/internal/service"
)

// ProfileHandler 暴露「个人资料」自助接口（任何已登录用户都可用）：
//   - 查看自己的联系方式与邮箱验证状态
//   - 自助维护邮箱 / 企微 ID / 手机号（管理员不再代为填写）
//   - 邮箱验证码发送与确认
type ProfileHandler struct {
	profiles *service.ProfileService
}

// NewProfileHandler 构造个人资料接口处理器。
func NewProfileHandler(profiles *service.ProfileService) *ProfileHandler {
	return &ProfileHandler{profiles: profiles}
}

// Get GET /api/profile
func (h *ProfileHandler) Get(w http.ResponseWriter, r *http.Request) {
	user := pkg.UserFromContext(r.Context())
	if user == "" {
		pkg.Fail(w, pkg.NewError(pkg.CodeUnauthorized, "当前未登录，无法查看个人资料"))
		return
	}
	p, err := h.profiles.Profile(user)
	if err != nil {
		pkg.Fail(w, err)
		return
	}
	pkg.OK(w, p)
}

// profileContactRequest 是 PUT /api/profile/contact 的请求体
// （与管理员用的 contactRequest 区分：自助接口不接受用户名，身份取自登录态）。
type profileContactRequest struct {
	Email   string `json:"email"`
	WecomID string `json:"wecom_id"`
	Phone   string `json:"phone"`
}

// UpdateContact PUT /api/profile/contact
func (h *ProfileHandler) UpdateContact(w http.ResponseWriter, r *http.Request) {
	user := pkg.UserFromContext(r.Context())
	if user == "" {
		pkg.Fail(w, pkg.NewError(pkg.CodeUnauthorized, "当前未登录，无法修改个人资料"))
		return
	}
	var in profileContactRequest
	if err := decodeJSON(w, r, &in); err != nil {
		pkg.Fail(w, err)
		return
	}
	p, err := h.profiles.UpdateContact(user, in.Email, in.WecomID, in.Phone)
	if err != nil {
		pkg.Fail(w, err)
		return
	}
	pkg.OK(w, p)
}

// RequestEmailVerify POST /api/profile/email/verify
func (h *ProfileHandler) RequestEmailVerify(w http.ResponseWriter, r *http.Request) {
	user := pkg.UserFromContext(r.Context())
	if user == "" {
		pkg.Fail(w, pkg.NewError(pkg.CodeUnauthorized, "当前未登录，无法验证邮箱"))
		return
	}
	email, ttl, err := h.profiles.RequestEmailVerification(r.Context(), user)
	if err != nil {
		pkg.Fail(w, err)
		return
	}
	pkg.OK(w, map[string]interface{}{
		"email":          email,
		"expires_in_sec": int(ttl.Seconds()),
	})
}

// confirmEmailRequest 是 POST /api/profile/email/confirm 的请求体。
type confirmEmailRequest struct {
	Code string `json:"code"`
}

// ConfirmEmail POST /api/profile/email/confirm
func (h *ProfileHandler) ConfirmEmail(w http.ResponseWriter, r *http.Request) {
	user := pkg.UserFromContext(r.Context())
	if user == "" {
		pkg.Fail(w, pkg.NewError(pkg.CodeUnauthorized, "当前未登录，无法验证邮箱"))
		return
	}
	var in confirmEmailRequest
	if err := decodeJSON(w, r, &in); err != nil {
		pkg.Fail(w, err)
		return
	}
	p, err := h.profiles.ConfirmEmail(user, in.Code)
	if err != nil {
		pkg.Fail(w, err)
		return
	}
	pkg.OK(w, p)
}

package handler

import (
	"net/http"
	"strings"

	"ci-platform/internal/model"
	"ci-platform/internal/pkg"
	"ci-platform/internal/service"
)

// RbacHandler 暴露「Manage and Assign Roles」接口：角色、授权与平台内用户。
type RbacHandler struct {
	rbac *service.RBACService
	auth *service.AuthService
}

// NewRbacHandler 构造 RBAC 接口处理器。auth 用于删除用户/重置密码后撤销会话。
func NewRbacHandler(rbac *service.RBACService, auth *service.AuthService) *RbacHandler {
	return &RbacHandler{rbac: rbac, auth: auth}
}

// rbacView 是 GET /api/security/rbac 的响应：数据 + 权限矩阵元信息。
type rbacView struct {
	model.RbacData
	PermissionGroups []model.PermissionGroup `json:"permission_groups"`
}

// Get GET /api/security/rbac
func (h *RbacHandler) Get(w http.ResponseWriter, r *http.Request) {
	pkg.OK(w, rbacView{
		RbacData:         h.rbac.Data(),
		PermissionGroups: model.PermissionGroups(),
	})
}

// rolesRequest 是 PUT /api/security/rbac/roles 请求体。
type rolesRequest struct {
	Roles []model.RbacRole `json:"roles"`
}

// Users GET /api/security/users
// 用户管理页的数据源：内置管理员 + 平台用户 + 联系方式 + 角色摘要。
func (h *RbacHandler) Users(w http.ResponseWriter, r *http.Request) {
	pkg.OK(w, h.rbac.Users())
}

// PutRoles PUT /api/security/rbac/roles
func (h *RbacHandler) PutRoles(w http.ResponseWriter, r *http.Request) {
	var in rolesRequest
	if err := decodeJSON(w, r, &in); err != nil {
		pkg.Fail(w, err)
		return
	}
	if err := h.rbac.SetRoles(in.Roles); err != nil {
		pkg.Fail(w, err)
		return
	}
	pkg.OK(w, h.rbac.Data())
}

// assignmentsRequest 是 PUT /api/security/rbac/assignments 请求体。
type assignmentsRequest struct {
	Assignments []model.RbacAssignment `json:"assignments"`
}

// PutAssignments PUT /api/security/rbac/assignments
func (h *RbacHandler) PutAssignments(w http.ResponseWriter, r *http.Request) {
	var in assignmentsRequest
	if err := decodeJSON(w, r, &in); err != nil {
		pkg.Fail(w, err)
		return
	}
	if err := h.rbac.SetAssignments(in.Assignments); err != nil {
		pkg.Fail(w, err)
		return
	}
	pkg.OK(w, h.rbac.Data())
}

// addUserRequest 是 POST /api/security/users 请求体。
type addUserRequest struct {
	Name     string `json:"name"`
	Password string `json:"password"`
	// Email 必填：构建通知按人员邮箱发信，没有邮箱等于收不到告警。
	Email string `json:"email"`
}

// AddUser POST /api/security/users
func (h *RbacHandler) AddUser(w http.ResponseWriter, r *http.Request) {
	var in addUserRequest
	if err := decodeJSON(w, r, &in); err != nil {
		pkg.Fail(w, err)
		return
	}
	if err := h.rbac.AddUser(in.Name, in.Password, in.Email); err != nil {
		pkg.Fail(w, err)
		return
	}
	pkg.OK(w, h.rbac.Data())
}

// deleteUserRequest 是 POST /api/security/users/delete 请求体。
type deleteUserRequest struct {
	Name string `json:"name"`
}

// DeleteUser POST /api/security/users/delete
func (h *RbacHandler) DeleteUser(w http.ResponseWriter, r *http.Request) {
	var in deleteUserRequest
	if err := decodeJSON(w, r, &in); err != nil {
		pkg.Fail(w, err)
		return
	}
	if err := h.rbac.DeleteUser(in.Name); err != nil {
		pkg.Fail(w, err)
		return
	}
	// 用户已删除，撤销其全部登录会话。
	h.auth.RevokeUserSessions(strings.TrimSpace(in.Name))
	pkg.OK(w, h.rbac.Data())
}

// resetPasswordRequest 是 PUT /api/security/users/password 请求体（管理员重置）。
type resetPasswordRequest struct {
	Name     string `json:"name"`
	Password string `json:"password"`
}

// ResetUserPassword PUT /api/security/users/password
func (h *RbacHandler) ResetUserPassword(w http.ResponseWriter, r *http.Request) {
	var in resetPasswordRequest
	if err := decodeJSON(w, r, &in); err != nil {
		pkg.Fail(w, err)
		return
	}
	if err := h.rbac.ResetUserPassword(in.Name, in.Password); err != nil {
		pkg.Fail(w, err)
		return
	}
	h.auth.RevokeUserSessions(strings.TrimSpace(in.Name))
	pkg.OK(w, nil)
}

// contactRequest 是 PUT /api/security/users/contact 请求体。
type contactRequest struct {
	Name    string `json:"name"`
	Email   string `json:"email"`
	WecomID string `json:"wecom_id"`
	Phone   string `json:"phone"`
}

// SetUserContact PUT /api/security/users/contact
// 设置用户的通知联系方式（任务通知按用户名引用时用于解析邮箱与 @ 提醒）。
func (h *RbacHandler) SetUserContact(w http.ResponseWriter, r *http.Request) {
	var in contactRequest
	if err := decodeJSON(w, r, &in); err != nil {
		pkg.Fail(w, err)
		return
	}
	if err := h.rbac.SetUserContact(in.Name, in.Email, in.WecomID, in.Phone); err != nil {
		pkg.Fail(w, err)
		return
	}
	pkg.OK(w, h.rbac.Data())
}

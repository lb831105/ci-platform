package model

import (
	"sort"
	"time"
)

// 角色/授权的类型与内置主体，对齐 Jenkins Role Strategy 插件。
const (
	// RoleKindGlobal 全局角色：跨任务的平台级权限。
	RoleKindGlobal = "global"
	// RoleKindItem 任务角色：通过 Pattern（任务名正则）授予单个/一批任务的权限。
	RoleKindItem = "item"

	// SubjectAnonymous 未登录主体（认证关闭时才实际生效）。
	SubjectAnonymous = "anonymous"
	// SubjectAuthenticated 所有已登录用户（Jenkins 内置主体）。
	SubjectAuthenticated = "authenticated"
)

// 权限清单：只收录平台实际支持的粒度，避免伪造 Jenkins 未实现的矩阵。
const (
	// 全部（Overall）
	PermOverallAdminister = "overall.administer" // 系统管理全部能力（隐含以下全部权限）
	PermOverallRead       = "overall.read"       // 登录并查看平台页面

	// 系统管理（Jenkins / 系统）
	PermSystemRead   = "system.read"   // 查看系统配置/日志/负载
	PermSystemManage = "system.manage" // 修改系统配置、重载设置、数据目录迁移

	// 凭据（Credentials）
	PermCredentialView   = "credential.view"   // 查看凭据列表（不含密钥）
	PermCredentialCreate = "credential.create" // 新建凭据
	PermCredentialUpdate = "credential.update" // 修改凭据
	PermCredentialDelete = "credential.delete" // 删除凭据

	// 任务（Job）
	PermJobCreate    = "job.create"    // 新建任务
	PermJobRead      = "job.read"      // 查看任务/构建历史/日志/产物
	PermJobConfigure = "job.configure" // 修改任务配置/重命名
	PermJobDelete    = "job.delete"    // 删除任务
	PermJobBuild     = "job.build"     // 触发构建
	PermJobReplay    = "job.replay"    // Run.Replay：把历史产物重新发布到 SSH
	PermJobApprove   = "job.approve"   // 放行「待审批」的构建（发布审批）

	// 视图（Views）
	PermViewRead   = "view.read"   // 查看视图及视图内任务
	PermViewCreate = "view.create" // 新建视图
	PermViewUpdate = "view.update" // 编辑视图
	PermViewDelete = "view.delete" // 删除视图
)

// PermissionItem 是权限矩阵中的一个可勾选动作。
type PermissionItem struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

// PermissionGroup 是权限矩阵中的一列分类（对齐 Jenkins 的 全部/凭据/任务/…）。
// Supported=false 表示平台不支持该类能力（单机执行、无视图/SCM/Metrics），
// UI 展示为禁用并给出原因，绝不伪造。
type PermissionGroup struct {
	ID          string           `json:"id"`
	Title       string           `json:"title"`
	Hint        string           `json:"hint,omitempty"`
	Supported   bool             `json:"supported"`
	Permissions []PermissionItem `json:"permissions,omitempty"`
}

// PermissionGroups 返回权限矩阵全部分类（含禁用项，供 UI 渲染）。
func PermissionGroups() []PermissionGroup {
	return []PermissionGroup{
		{
			ID:        "overall",
			Title:     "全部",
			Supported: true,
			Permissions: []PermissionItem{
				{ID: PermOverallAdminister, Title: "Administer"},
				{ID: PermOverallRead, Title: "Read"},
			},
		},
		{
			ID:        "system",
			Title:     "系统管理",
			Supported: true,
			Permissions: []PermissionItem{
				{ID: PermSystemRead, Title: "Read"},
				{ID: PermSystemManage, Title: "Configure"},
			},
		},
		{
			ID:        "credential",
			Title:     "凭据",
			Supported: true,
			Permissions: []PermissionItem{
				{ID: PermCredentialView, Title: "View"},
				{ID: PermCredentialCreate, Title: "Create"},
				{ID: PermCredentialUpdate, Title: "Update"},
				{ID: PermCredentialDelete, Title: "Delete"},
			},
		},
		{
			ID:        "job",
			Title:     "任务",
			Supported: true,
			Permissions: []PermissionItem{
				{ID: PermJobCreate, Title: "Create"},
				{ID: PermJobRead, Title: "Read"},
				{ID: PermJobConfigure, Title: "Configure"},
				{ID: PermJobDelete, Title: "Delete"},
				{ID: PermJobBuild, Title: "Build"},
				{ID: PermJobReplay, Title: "Replay"},
				{ID: PermJobApprove, Title: "Approve"},
			},
		},
		// 以下分类平台不支持：保持矩阵完整可见但禁用，避免误以为已生效。
		{ID: "agent", Title: "代理", Hint: "平台为单机本地执行，无代理节点", Supported: false},
		{ID: "run", Title: "运行", Hint: "未单独拆出权限：取消/删除构建沿用「任务」列的 Build / Configure，Replay 归入任务列", Supported: false},
		// 视图（平台已实现 Jenkins Views 精简版：列表视图 + 我的视图）。
		{
			ID:        "view",
			Title:     "视图",
			Supported: true,
			Permissions: []PermissionItem{
				{ID: PermViewRead, Title: "Read"},
				{ID: PermViewCreate, Title: "Create"},
				{ID: PermViewUpdate, Title: "Update"},
				{ID: PermViewDelete, Title: "Delete"},
			},
		},
		{ID: "scm", Title: "SCM", Hint: "暂不支持 SCM Tag 等操作", Supported: false},
		{ID: "metrics", Title: "Metrics", Hint: "暂不支持 Metrics API", Supported: false},
	}
}

// AllPermissionIDs 返回全部受支持权限 ID（校验与「Administer 隐含全部」使用）。
func AllPermissionIDs() []string {
	out := make([]string, 0, 16)
	for _, group := range PermissionGroups() {
		if !group.Supported {
			continue
		}
		for _, item := range group.Permissions {
			out = append(out, item.ID)
		}
	}
	sort.Strings(out)
	return out
}

// IsKnownPermission 判断权限 ID 是否受支持。
func IsKnownPermission(id string) bool {
	for _, known := range AllPermissionIDs() {
		if known == id {
			return true
		}
	}
	return false
}

// RbacRole 是一个角色（全局或任务级）；Name 是唯一键。
type RbacRole struct {
	Name string `json:"name"`
	Kind string `json:"kind"` // global | item

	// Pattern 是任务角色的任务名匹配正则（忽略大小写、完全匹配）；模板角色为空。
	Pattern string `json:"pattern,omitempty"`
	// Template 引用另一个任务角色（必须是无 Pattern 的模板角色），权限取并集。
	Template string   `json:"template,omitempty"`
	Perms    []string `json:"permissions"`
}

// Has 判断角色（含其模板继承链）是否拥有权限；administer 隐含全部。
func (r RbacRole) Has(perm string, resolveTemplate func(name string) (RbacRole, bool), depth int) bool {
	if depth > 8 {
		return false // 模板循环由保存时校验，这里兜底
	}
	for _, p := range r.Perms {
		if p == PermOverallAdminister || p == perm {
			return true
		}
	}
	if r.Template != "" && resolveTemplate != nil {
		if parent, ok := resolveTemplate(r.Template); ok {
			return parent.Has(perm, resolveTemplate, depth+1)
		}
	}
	return false
}

// JobRoleRule 是「某个 Pattern 上拥有哪些任务权限」的对外投影。
//
// 前端不能用全局 permissions 判断「这一行任务的构建按钮该不该显示」：
// 只被授予任务角色的用户，权限是按任务名 Pattern 生效的。所以 /auth/me
// 一并返回规则，让按钮显示与实际可操作性一致。
type JobRoleRule struct {
	Pattern     string   `json:"pattern"`
	Permissions []string `json:"permissions"`
}

// RbacAssignment 把主体（用户/组/内置主体）绑定到角色。
type RbacAssignment struct {
	Role    string `json:"role"`
	Kind    string `json:"kind"` // global | item
	Subject string `json:"subject"`
}

// RbacUser 是平台内管理的用户（对齐 Jenkins 安全域中的用户列表）。
type RbacUser struct {
	Name string `json:"name"`
	// DisplayName 是管理端维护的汉字姓名（用户名是登录标识，不用于展示）。
	DisplayName  string    `json:"display_name,omitempty"`
	PasswordHash string    `json:"password_hash"` // bcrypt；接口返回时清空
	CreatedAt    time.Time `json:"created_at"`

	// 联系方式：任务通知按用户名引用用户时，用于解析收件地址与 @ 提醒。
	Email   string `json:"email,omitempty"`    // 邮件通知地址
	WecomID string `json:"wecom_id,omitempty"` // 企业微信群机器人 @userid
	Phone   string `json:"phone,omitempty"`    // 钉钉群机器人 @手机号

	// EmailVerified 标记邮箱是否通过验证码验证（自助维护时由用户确认）。
	// 未验证的邮箱仍可用于通知，但在用户管理页会标出，便于排查「收不到邮件」。
	EmailVerified bool `json:"email_verified"`
}

// Profile 是「个人资料」页的展示模型：当前登录用户的账号与联系方式。
// Builtin 用户（系统配置里的管理员）不在 rbac.json 中，联系方式只读。
type Profile struct {
	Name          string `json:"name"`
	DisplayName   string `json:"display_name,omitempty"`
	Email         string `json:"email,omitempty"`
	WecomID       string `json:"wecom_id,omitempty"`
	Phone         string `json:"phone,omitempty"`
	EmailVerified bool   `json:"email_verified"`
	Builtin       bool   `json:"builtin"`
}

// UserRoleRef 是用户被授予的角色摘要（用户管理页展示用）。
type UserRoleRef struct {
	Role    string `json:"role"`
	Kind    string `json:"kind"`
	Pattern string `json:"pattern,omitempty"`
}

// UserAccount 是「用户管理」页（系统管理 → 用户管理）的展示模型：
// 账号信息 + 联系方式 + 角色摘要。Builtin=true 表示系统配置里的内置管理员，
// 它不在 rbac.json 里，也不可删除/改密码（否则可能把自己锁在门外）。
type UserAccount struct {
	Name          string        `json:"name"`
	DisplayName   string        `json:"display_name,omitempty"`
	Email         string        `json:"email,omitempty"`
	WecomID       string        `json:"wecom_id,omitempty"`
	Phone         string        `json:"phone,omitempty"`
	CreatedAt     *time.Time    `json:"created_at,omitempty"` // 内置管理员没有创建时间
	EmailVerified bool          `json:"email_verified"`
	Builtin       bool          `json:"builtin"`
	Roles         []UserRoleRef `json:"roles"`
}

// RbacData 是 data/rbac.json 的完整内容：角色、授权与用户。
type RbacData struct {
	Roles       []RbacRole       `json:"roles"`
	Assignments []RbacAssignment `json:"assignments"`
	Users       []RbacUser       `json:"users"`
	UpdatedAt   time.Time        `json:"updated_at"`
}

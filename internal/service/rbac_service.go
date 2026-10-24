package service

import (
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"

	"ci-platform/internal/model"
	"ci-platform/internal/pkg"
)

// RbacStore 是 RBAC 存储抽象（由 store.RbacStore 实现）。
type RbacStore interface {
	Load() (*model.RbacData, error)
	Save(*model.RbacData) error
}

// AuthChecker 提供「认证是否开启 / 管理员用户名」，由 AuthService 实现。
// RBAC 依赖抽象而非具体服务，避免构造循环。
type AuthChecker interface {
	Enabled() bool
	AdminUser() string
}

// RBACService 管理角色、授权与用户：对齐 Jenkins「Manage and Assign Roles」。
// 管理员用户（系统配置中的 AuthUser）始终拥有 Administer，保证无法被锁在门外。
type RBACService struct {
	mu    sync.RWMutex
	store RbacStore
	auth  AuthChecker
	data  model.RbacData
}

// NewRBACService 载入 RBAC 数据；文件不存在时初始化默认角色/授权并落盘。
func NewRBACService(store RbacStore, auth AuthChecker) (*RBACService, error) {
	s := &RBACService{store: store, auth: auth}
	loaded, err := store.Load()
	if err != nil {
		return nil, err
	}
	if loaded == nil {
		s.data = s.defaultData()
		if err = store.Save(&s.data); err != nil {
			return nil, err
		}
		return s, nil
	}
	s.data = *loaded
	if err = s.normalizeLoaded(); err != nil {
		return nil, err
	}
	return s, nil
}

// defaultData 对齐 Jenkins 默认角色策略：全局角色 admin（Administer）授权给管理员用户。
func (s *RBACService) defaultData() model.RbacData {
	admin := "admin"
	if s.auth != nil && s.auth.AdminUser() != "" {
		admin = s.auth.AdminUser()
	}
	return model.RbacData{
		Roles: []model.RbacRole{
			{Name: "admin", Kind: model.RoleKindGlobal, Perms: []string{model.PermOverallAdminister}},
		},
		Assignments: []model.RbacAssignment{
			{Role: "admin", Kind: model.RoleKindGlobal, Subject: admin},
		},
		Users: []model.RbacUser{},
	}
}

// normalizeLoaded 兜底补齐空角色列表（旧数据兼容）。
func (s *RBACService) normalizeLoaded() error {
	if s.data.Roles == nil {
		s.data.Roles = s.defaultData().Roles
	}
	if s.data.Assignments == nil {
		s.data.Assignments = []model.RbacAssignment{}
	}
	if s.data.Users == nil {
		s.data.Users = []model.RbacUser{}
	}
	return nil
}

// Data 返回脱敏后的完整数据（用户不含密码 hash，仅保留联系方式）。
func (s *RBACService) Data() model.RbacData {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := s.data
	out.Users = make([]model.RbacUser, len(s.data.Users))
	for i, u := range s.data.Users {
		out.Users[i] = model.RbacUser{
			Name: u.Name, CreatedAt: u.CreatedAt,
			Email: u.Email, WecomID: u.WecomID, Phone: u.Phone,
		}
	}
	return out
}

// IsAdmin 判断是否为系统管理员（系统配置中的登录用户）。
func (s *RBACService) IsAdmin(user string) bool {
	return s.auth != nil && user != "" && user == s.auth.AdminUser()
}

// authDisabled 认证关闭时全部放行（与 Jenkins -Dhudson... 一致的本地方便性）。
func (s *RBACService) authDisabled() bool {
	return s.auth != nil && !s.auth.Enabled()
}

// HasPermission 判断用户是否拥有平台级权限。administer 隐含全部。
func (s *RBACService) HasPermission(user, perm string) bool {
	if s.authDisabled() {
		return true
	}
	if s.IsAdmin(user) {
		return true
	}
	for _, role := range s.rolesFor(user, model.RoleKindGlobal) {
		if role.Has(perm, s.resolveTemplate, 0) {
			return true
		}
	}
	return false
}

// HasJobPermission 判断用户对指定任务是否拥有权限：
// 全局角色直接授予（Jenkins 语义：全局 Job 权限作用于全部任务）；
// 任务角色按 Pattern 匹配任务名后授予。
func (s *RBACService) HasJobPermission(user, action, jobName string) bool {
	if s.authDisabled() {
		return true
	}
	if s.IsAdmin(user) {
		return true
	}
	for _, role := range s.rolesFor(user, model.RoleKindGlobal) {
		if role.Has(action, s.resolveTemplate, 0) {
			return true
		}
	}
	for _, role := range s.rolesFor(user, model.RoleKindItem) {
		if !role.Has(action, s.resolveTemplate, 0) {
			continue
		}
		if matchJobPattern(role.Pattern, jobName) {
			return true
		}
	}
	return false
}

// matchJobPattern 对齐 Jenkins Role Strategy：正则忽略大小写、完全匹配任务名。
func matchJobPattern(pattern, jobName string) bool {
	if strings.TrimSpace(pattern) == "" {
		return false
	}
	re, err := regexp.Compile(`(?i)^(?:` + pattern + `)$`)
	if err != nil {
		return false // 保存时已校验；损坏数据不授权，宁可拒绝
	}
	return re.MatchString(jobName)
}

// rolesFor 返回主体（用户 / authenticated）被授予的指定类型角色。
func (s *RBACService) rolesFor(user, kind string) []model.RbacRole {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []model.RbacRole
	for _, a := range s.data.Assignments {
		if a.Kind != kind {
			continue
		}
		if a.Subject != user && !(user != "" && a.Subject == model.SubjectAuthenticated) {
			continue
		}
		for _, role := range s.data.Roles {
			if role.Kind == kind && role.Name == a.Role {
				out = append(out, role)
				break
			}
		}
	}
	return out
}

// resolveTemplate 解析模板角色（必须是任务角色且无 Pattern）。
func (s *RBACService) resolveTemplate(name string) (model.RbacRole, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, role := range s.data.Roles {
		if role.Kind == model.RoleKindItem && role.Name == name {
			return role, true
		}
	}
	return model.RbacRole{}, false
}

// EffectivePermissions 返回用户的全部有效权限（供前端隐藏入口 /auth/me）。
func (s *RBACService) EffectivePermissions(user string) []string {
	all := model.AllPermissionIDs()
	if s.authDisabled() || s.IsAdmin(user) {
		return all
	}
	set := make(map[string]bool)
	administer := false
	for _, kind := range []string{model.RoleKindGlobal, model.RoleKindItem} {
		for _, role := range s.rolesFor(user, kind) {
			for _, p := range role.Perms {
				if p == model.PermOverallAdminister {
					administer = true
				}
				set[p] = true
			}
		}
	}
	if administer {
		return all
	}
	out := make([]string, 0, len(set))
	for _, p := range all {
		if set[p] {
			out = append(out, p)
		}
	}
	return out
}

// Profile 返回某个用户的个人资料（自助维护联系方式用）。
// 内置管理员不在 rbac.json 中：返回 Builtin=true，联系方式只读。
func (s *RBACService) Profile(name string) (model.Profile, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	admin := ""
	if s.auth != nil {
		admin = s.auth.AdminUser()
	}
	for _, u := range s.data.Users {
		if u.Name != name {
			continue
		}
		return model.Profile{
			Name:          u.Name,
			Email:         u.Email,
			WecomID:       u.WecomID,
			Phone:         u.Phone,
			EmailVerified: u.EmailVerified,
		}, nil
	}
	if admin != "" && name == admin {
		return model.Profile{Name: admin, Builtin: true}, nil
	}
	return model.Profile{}, pkg.ErrNotFound("用户 %s 不存在", name)
}

// SetEmailVerified 标记邮箱验证状态（验证成功后由 ProfileService 调用）。
func (s *RBACService) SetEmailVerified(name string, verified bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.data.Users {
		if s.data.Users[i].Name != name {
			continue
		}
		s.data.Users[i].EmailVerified = verified
		s.data.UpdatedAt = time.Now().UTC()
		return s.store.Save(&s.data)
	}
	return pkg.ErrNotFound("用户 %s 不存在", name)
}

// Users 汇总「用户管理」页需要的账号清单：内置管理员 + 平台内用户，附带角色摘要。
// 只返回展示字段，密码哈希等敏感信息不出服务层。
func (s *RBACService) Users() []model.UserAccount {
	s.mu.RLock()
	defer s.mu.RUnlock()

	admin := ""
	if s.auth != nil {
		admin = s.auth.AdminUser()
	}
	list := make([]model.UserAccount, 0, len(s.data.Users)+1)
	if admin != "" {
		// 内置管理员来自系统配置，不在 rbac.json 的用户里；不单独补一行的话，
		// 用户管理页会看不到实际拥有最高权限的账号。
		list = append(list, model.UserAccount{
			Name:    admin,
			Builtin: true,
			Roles:   s.rolesForLocked(admin),
		})
	}
	for _, u := range s.data.Users {
		if admin != "" && u.Name == admin {
			continue // 与内置管理员同名时不重复展示
		}
		created := u.CreatedAt
		list = append(list, model.UserAccount{
			Name:          u.Name,
			Email:         u.Email,
			WecomID:       u.WecomID,
			Phone:         u.Phone,
			EmailVerified: u.EmailVerified,
			CreatedAt:     &created,
			Roles:         s.rolesForLocked(u.Name),
		})
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].Builtin != list[j].Builtin {
			return list[i].Builtin // 内置管理员置顶
		}
		return list[i].Name < list[j].Name
	})
	return list
}

// rolesForLocked 返回主体的授权摘要（全局角色在前）；调用方需已持有读锁。
func (s *RBACService) rolesForLocked(subject string) []model.UserRoleRef {
	refs := make([]model.UserRoleRef, 0, 2)
	for _, a := range s.data.Assignments {
		if a.Subject != subject || a.Role == "" {
			continue
		}
		ref := model.UserRoleRef{Role: a.Role, Kind: a.Kind}
		for _, r := range s.data.Roles {
			if r.Name == a.Role && r.Kind == a.Kind {
				ref.Pattern = r.Pattern
				break
			}
		}
		refs = append(refs, ref)
	}
	sort.SliceStable(refs, func(i, j int) bool {
		if refs[i].Kind != refs[j].Kind {
			return refs[i].Kind == model.RoleKindGlobal
		}
		return refs[i].Role < refs[j].Role
	})
	return refs
}

// SetRoles 校验并替换全部角色（保存原子落盘）。
func (s *RBACService) SetRoles(roles []model.RbacRole) error {
	normalized, err := s.validateRoles(roles)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.Roles = normalized
	// 角色删除后同步清理悬空授权，避免脏数据。
	kept := s.data.Assignments[:0]
	for _, a := range s.data.Assignments {
		found := false
		for _, role := range normalized {
			if role.Kind == a.Kind && role.Name == a.Role {
				found = true
				break
			}
		}
		if found {
			kept = append(kept, a)
		}
	}
	s.data.Assignments = kept
	s.data.UpdatedAt = time.Now().UTC()
	return s.store.Save(&s.data)
}

// validateRoles 做唯一性 / 正则 / 模板引用 / 权限 ID 校验。
func (s *RBACService) validateRoles(roles []model.RbacRole) ([]model.RbacRole, error) {
	seen := make(map[string]bool, len(roles))
	normalized := make([]model.RbacRole, 0, len(roles))
	for i := range roles {
		role := roles[i]
		role.Name = strings.TrimSpace(role.Name)
		role.Pattern = strings.TrimSpace(role.Pattern)
		role.Template = strings.TrimSpace(role.Template)
		if role.Name == "" {
			return nil, pkg.ErrBadRequest("角色名称不能为空")
		}
		if role.Kind != model.RoleKindGlobal && role.Kind != model.RoleKindItem {
			return nil, pkg.ErrBadRequest("角色 %q 的类型必须是 global 或 item", role.Name)
		}
		if seen[role.Kind+"/"+role.Name] {
			return nil, pkg.ErrBadRequest("角色名重复: %s", role.Name)
		}
		seen[role.Kind+"/"+role.Name] = true
		if role.Pattern != "" {
			if _, err := regexp.Compile(`(?i)^(?:` + role.Pattern + `)$`); err != nil {
				return nil, pkg.ErrBadRequest("角色 %q 的 Pattern 不是合法正则: %v", role.Name, err)
			}
		}
		perms := make([]string, 0, len(role.Perms))
		permSeen := make(map[string]bool)
		for _, p := range role.Perms {
			p = strings.TrimSpace(p)
			if p == "" || permSeen[p] {
				continue
			}
			if !model.IsKnownPermission(p) {
				return nil, pkg.ErrBadRequest("角色 %q 包含未知权限: %s", role.Name, p)
			}
			permSeen[p] = true
			perms = append(perms, p)
		}
		role.Perms = perms
		normalized = append(normalized, role)
	}
	// 模板引用校验：必须指向任务角色且无 Pattern，且不能成环。
	byName := make(map[string]model.RbacRole, len(normalized))
	for _, role := range normalized {
		byName[role.Kind+"/"+role.Name] = role
	}
	for _, role := range normalized {
		if role.Kind != model.RoleKindItem || role.Template == "" {
			continue
		}
		parent, ok := byName[model.RoleKindItem+"/"+role.Template]
		if !ok {
			return nil, pkg.ErrBadRequest("角色 %q 引用的模板 %q 不存在", role.Name, role.Template)
		}
		if parent.Pattern != "" {
			return nil, pkg.ErrBadRequest("模板 %q 必须是不带 Pattern 的任务角色", role.Template)
		}
		visited := map[string]bool{role.Name: true}
		cur := parent
		for cur.Template != "" {
			if visited[cur.Template] {
				return nil, pkg.ErrBadRequest("模板继承不能成环: %s", role.Name)
			}
			visited[cur.Template] = true
			next, ok := byName[model.RoleKindItem+"/"+cur.Template]
			if !ok {
				break
			}
			cur = next
		}
	}
	return normalized, nil
}

// SetAssignments 校验并替换全部授权。
func (s *RBACService) SetAssignments(assignments []model.RbacAssignment) error {
	normalized := make([]model.RbacAssignment, 0, len(assignments))
	seen := make(map[string]bool)
	s.mu.RLock()
	roles := s.data.Roles
	s.mu.RUnlock()
	for _, a := range assignments {
		a.Role = strings.TrimSpace(a.Role)
		a.Subject = strings.TrimSpace(a.Subject)
		a.Kind = strings.TrimSpace(a.Kind)
		if a.Subject == "" || a.Role == "" {
			continue
		}
		if a.Kind != model.RoleKindGlobal && a.Kind != model.RoleKindItem {
			return pkg.ErrBadRequest("授权类型必须是 global 或 item")
		}
		found := false
		for _, role := range roles {
			if role.Kind == a.Kind && role.Name == a.Role {
				found = true
				break
			}
		}
		if !found {
			return pkg.ErrBadRequest("授权引用的角色不存在: %s", a.Role)
		}
		key := a.Kind + "/" + a.Role + "/" + a.Subject
		if seen[key] {
			continue
		}
		seen[key] = true
		normalized = append(normalized, a)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data.Assignments = normalized
	s.data.UpdatedAt = time.Now().UTC()
	return s.store.Save(&s.data)
}

// AddUser 创建平台内用户（密码 bcrypt 落盘）。与系统管理员同名的用户不允许创建。
//
// email 必填：构建结果的邮件通知按人员解析邮箱，没有邮箱的账号在被选为
// 通知对象时只会「静默收不到」，因此从创建入口就要求填对地址。
func (s *RBACService) AddUser(name, password, email string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return pkg.ErrBadRequest("用户名不能为空")
	}
	if s.IsAdmin(name) {
		return pkg.ErrConflict("用户名 %s 与系统管理员相同", name)
	}
	if err := validatePasswordStrength(password); err != nil {
		return err
	}
	email, err := normalizeUserEmail(email)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, u := range s.data.Users {
		if u.Name == name {
			return pkg.ErrConflict("用户 %s 已存在", name)
		}
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return pkg.ErrBadRequest("密码不符合要求: %v", err)
	}
	s.data.Users = append(s.data.Users, model.RbacUser{
		Name:         name,
		PasswordHash: string(hash),
		Email:        email,
		CreatedAt:    time.Now().UTC(),
	})
	s.data.UpdatedAt = time.Now().UTC()
	return s.store.Save(&s.data)
}

// normalizeUserEmail 校验并规范化用户邮箱。
// 用户邮箱是构建通知的发信地址，必须存在且形如 a@b（与通知配置保持一致的最小校验）。
func normalizeUserEmail(raw string) (string, error) {
	email := strings.TrimSpace(raw)
	if email == "" {
		return "", pkg.ErrBadRequest("邮箱不能为空（构建通知按人员邮箱发信）")
	}
	// 最小但足够严格的校验：单一 @、本地部分非空且不以 . 开头/结尾、
	// 域名至少两段且每段非空（a@.com / a@b..com 这类明显错误直接拦住）。
	if strings.Count(email, "@") != 1 {
		return "", pkg.ErrBadRequest("邮箱 %q 格式不正确", email)
	}
	at := strings.Index(email, "@")
	local, host := email[:at], email[at+1:]
	if local == "" || host == "" ||
		strings.HasPrefix(local, ".") || strings.HasSuffix(local, ".") ||
		strings.HasPrefix(host, ".") || strings.HasSuffix(host, ".") {
		return "", pkg.ErrBadRequest("邮箱 %q 格式不正确", email)
	}
	labels := strings.Split(host, ".")
	if len(labels) < 2 {
		return "", pkg.ErrBadRequest("邮箱 %q 格式不正确", email)
	}
	for _, label := range labels {
		if label == "" {
			return "", pkg.ErrBadRequest("邮箱 %q 格式不正确", email)
		}
	}
	if strings.ContainsAny(email, " \t\r\n") {
		return "", pkg.ErrBadRequest("邮箱 %q 不能包含空白字符", email)
	}
	return email, nil
}

// UsersWithoutEmail 返回缺少邮箱的历史用户（旧数据没有强制邮箱）。
// 启动时告警 + 用户管理页提示，避免「选了人却收不到邮件」这种沉默故障。
func (s *RBACService) UsersWithoutEmail() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]string, 0, 4)
	for _, u := range s.data.Users {
		if strings.TrimSpace(u.Email) == "" {
			out = append(out, u.Name)
		}
	}
	sort.Strings(out)
	return out
}

// DeleteUser 删除用户及其全部授权。
func (s *RBACService) DeleteUser(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return pkg.ErrBadRequest("用户名不能为空")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	kept := s.data.Users[:0]
	found := false
	for _, u := range s.data.Users {
		if u.Name == name {
			found = true
			continue
		}
		kept = append(kept, u)
	}
	if !found {
		return pkg.ErrNotFound("用户 %s 不存在", name)
	}
	s.data.Users = kept
	keptAssign := s.data.Assignments[:0]
	for _, a := range s.data.Assignments {
		if a.Subject != name {
			keptAssign = append(keptAssign, a)
		}
	}
	s.data.Assignments = keptAssign
	s.data.UpdatedAt = time.Now().UTC()
	return s.store.Save(&s.data)
}

// ResetUserPassword 由管理员重置用户密码。
func (s *RBACService) ResetUserPassword(name, password string) error {
	name = strings.TrimSpace(name)
	if err := validatePasswordStrength(password); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.data.Users {
		if s.data.Users[i].Name == name {
			hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
			if err != nil {
				return pkg.ErrBadRequest("密码不符合要求: %v", err)
			}
			s.data.Users[i].PasswordHash = string(hash)
			s.data.UpdatedAt = time.Now().UTC()
			return s.store.Save(&s.data)
		}
	}
	return pkg.ErrNotFound("用户 %s 不存在", name)
}

// SetUserContact 设置用户的通知联系方式（管理员在「用户管理」中维护）。
// 任务通知按用户名引用人员时，用这些信息解析收件邮箱与 @ 提醒对象。
func (s *RBACService) SetUserContact(name, email, wecomID, phone string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return pkg.ErrBadRequest("用户名不能为空")
	}
	email, err := normalizeUserEmail(email)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.data.Users {
		if s.data.Users[i].Name != name {
			continue
		}
		// 邮箱变更后原验证状态不再成立，必须重新验证。
		if !strings.EqualFold(s.data.Users[i].Email, email) {
			s.data.Users[i].EmailVerified = false
		}
		s.data.Users[i].Email = email
		s.data.Users[i].WecomID = strings.TrimSpace(wecomID)
		s.data.Users[i].Phone = strings.TrimSpace(phone)
		s.data.UpdatedAt = time.Now().UTC()
		return s.store.Save(&s.data)
	}
	return pkg.ErrNotFound("用户 %s 不存在", name)
}

// Contacts 按用户名批量返回用户联系方式（任务通知解析用）。
// 未找到的用户直接忽略（避免因引用已删除用户导致整次通知失败）。
func (s *RBACService) Contacts(names []string) map[string]model.RbacUser {
	if len(names) == 0 {
		return map[string]model.RbacUser{}
	}
	want := make(map[string]bool, len(names))
	for _, n := range names {
		want[strings.TrimSpace(n)] = true
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]model.RbacUser, len(want))
	for _, u := range s.data.Users {
		if !want[u.Name] {
			continue
		}
		// 不携带密码 hash：只暴露联系信息。
		out[u.Name] = model.RbacUser{Name: u.Name, Email: u.Email, WecomID: u.WecomID, Phone: u.Phone}
	}
	return out
}

// AllContacts 返回全部用户的通知联系方式（不含密码 hash），供任务通知选人。
func (s *RBACService) AllContacts() []model.RbacUser {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]model.RbacUser, 0, len(s.data.Users))
	for _, u := range s.data.Users {
		out = append(out, model.RbacUser{Name: u.Name, Email: u.Email, WecomID: u.WecomID, Phone: u.Phone})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// ChangeUserPassword 用户修改自己的密码（校验旧密码）。
func (s *RBACService) ChangeUserPassword(name, oldPassword, newPassword string) error {
	name = strings.TrimSpace(name)
	if err := validatePasswordStrength(newPassword); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.data.Users {
		if s.data.Users[i].Name != name {
			continue
		}
		if bcrypt.CompareHashAndPassword([]byte(s.data.Users[i].PasswordHash), []byte(oldPassword)) != nil {
			return pkg.NewError(pkg.CodeUnauthorized, "当前密码不正确")
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
		if err != nil {
			return pkg.ErrBadRequest("新密码不符合要求: %v", err)
		}
		s.data.Users[i].PasswordHash = string(hash)
		s.data.UpdatedAt = time.Now().UTC()
		return s.store.Save(&s.data)
	}
	return pkg.ErrNotFound("用户 %s 不存在", name)
}

// VerifyLogin 校验平台内用户登录（供 AuthService 在管理员凭据不匹配时回退调用）。
func (s *RBACService) VerifyLogin(name, password string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for i := range s.data.Users {
		if s.data.Users[i].Name == name &&
			bcrypt.CompareHashAndPassword([]byte(s.data.Users[i].PasswordHash), []byte(password)) == nil {
			return true
		}
	}
	return false
}

// UserExists 判断用户是否存在（用于删除后撤销会话）。
func (s *RBACService) UserExists(name string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, u := range s.data.Users {
		if u.Name == name {
			return true
		}
	}
	return false
}

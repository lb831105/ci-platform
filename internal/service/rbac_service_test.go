package service

import (
	"testing"

	"ci-platform/internal/model"
	"ci-platform/internal/pkg"
)

type fakeAuthChecker struct {
	enabled bool
	admin   string
}

func (f *fakeAuthChecker) Enabled() bool     { return f.enabled }
func (f *fakeAuthChecker) AdminUser() string { return f.admin }

type memRbacStore struct {
	data *model.RbacData
}

func (m *memRbacStore) Load() (*model.RbacData, error) { return m.data, nil }
func (m *memRbacStore) Save(d *model.RbacData) error   { m.data = d; return nil }

func newRbacForTest(t *testing.T) *RBACService {
	t.Helper()
	svc, err := NewRBACService(&memRbacStore{}, &fakeAuthChecker{enabled: true, admin: "root"})
	if err != nil {
		t.Fatal(err)
	}
	return svc
}

func TestRBACDefaultsGrantAdminRole(t *testing.T) {
	svc := newRbacForTest(t)
	if !svc.HasPermission("root", model.PermOverallAdminister) {
		t.Fatal("settings admin must always administer")
	}
	if svc.HasPermission("alice", model.PermOverallRead) {
		t.Fatal("unassigned user must not have permissions")
	}
	data := svc.Data()
	if len(data.Roles) != 1 || data.Roles[0].Name != "admin" {
		t.Fatalf("default roles = %+v", data.Roles)
	}
	if len(data.Assignments) != 1 || data.Assignments[0].Subject != "root" {
		t.Fatalf("default assignments = %+v", data.Assignments)
	}
}

func TestRBACAdministerImpliesAll(t *testing.T) {
	svc := newRbacForTest(t)
	if err := svc.SetRoles([]model.RbacRole{{Name: "ops", Kind: model.RoleKindGlobal, Perms: []string{model.PermOverallAdminister}}}); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetAssignments([]model.RbacAssignment{
		{Role: "ops", Kind: model.RoleKindGlobal, Subject: "alice"},
	}); err != nil {
		t.Fatal(err)
	}
	for _, perm := range model.AllPermissionIDs() {
		if !svc.HasPermission("alice", perm) {
			t.Fatalf("administer must imply %s", perm)
		}
	}
}

func TestRBACItemRolePatternMatching(t *testing.T) {
	svc := newRbacForTest(t)
	if err := svc.SetRoles([]model.RbacRole{
		{Name: "as-doc", Kind: model.RoleKindItem, Pattern: `as-doc.*`, Perms: []string{model.PermJobBuild, model.PermJobRead}},
		{Name: "web-admin", Kind: model.RoleKindItem, Pattern: `^pan-insur$`, Perms: []string{model.PermJobConfigure, model.PermJobDelete}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetAssignments([]model.RbacAssignment{
		{Role: "as-doc", Kind: model.RoleKindItem, Subject: "bob"},
		{Role: "web-admin", Kind: model.RoleKindItem, Subject: model.SubjectAuthenticated},
	}); err != nil {
		t.Fatal(err)
	}
	if !svc.HasJobPermission("bob", model.PermJobBuild, "as-doc") {
		t.Fatal("pattern must match job name")
	}
	if !svc.HasJobPermission("bob", model.PermJobBuild, "AS-DOC-FRONT") {
		t.Fatal("pattern must be case-insensitive")
	}
	if svc.HasJobPermission("bob", model.PermJobConfigure, "as-doc") {
		t.Fatal("role without configure must not grant it")
	}
	// authenticated 主体：任何登录用户都拿到 web-admin 的权限。
	if !svc.HasJobPermission("anyone", model.PermJobDelete, "pan-insur") {
		t.Fatal("authenticated subject must apply to all logged-in users")
	}
	if !svc.HasJobPermission("anyone", model.PermJobDelete, "PAN-INSUR") {
		t.Fatal("item pattern must be case-insensitive")
	}
	if svc.HasJobPermission("anyone", model.PermJobDelete, "pan-insur-web") {
		t.Fatal("pattern must fully match the job name")
	}
}

func TestRBACTemplateInheritance(t *testing.T) {
	svc := newRbacForTest(t)
	if err := svc.SetRoles([]model.RbacRole{
		{Name: "tpl-reader", Kind: model.RoleKindItem, Perms: []string{model.PermJobRead}},
		{Name: "as-doc", Kind: model.RoleKindItem, Pattern: `as-doc`, Template: "tpl-reader", Perms: []string{model.PermJobBuild}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetAssignments([]model.RbacAssignment{
		{Role: "as-doc", Kind: model.RoleKindItem, Subject: "bob"},
	}); err != nil {
		t.Fatal(err)
	}
	if !svc.HasJobPermission("bob", model.PermJobRead, "as-doc") {
		t.Fatal("template permissions must be inherited")
	}
	if !svc.HasJobPermission("bob", model.PermJobBuild, "as-doc") {
		t.Fatal("own permissions must be kept with template")
	}
	if svc.HasJobPermission("bob", model.PermJobRead, "other-job") {
		t.Fatal("template perms must only apply to matching pattern")
	}
}

func TestRBACRoleValidation(t *testing.T) {
	svc := newRbacForTest(t)
	if err := svc.SetRoles([]model.RbacRole{{Name: "r", Kind: model.RoleKindItem, Pattern: "("}}); err == nil {
		t.Fatal("invalid regex must be rejected")
	}
	if err := svc.SetRoles([]model.RbacRole{{Name: "r", Kind: model.RoleKindGlobal, Perms: []string{"nope"}}}); err == nil {
		t.Fatal("unknown permission must be rejected")
	}
	if err := svc.SetRoles([]model.RbacRole{
		{Name: "a", Kind: model.RoleKindItem, Template: "b"},
		{Name: "b", Kind: model.RoleKindItem, Template: "a"},
	}); err == nil {
		t.Fatal("template cycle must be rejected")
	}
	if err := svc.SetRoles([]model.RbacRole{
		{Name: "tpl", Kind: model.RoleKindItem, Pattern: `x`, Perms: []string{model.PermJobRead}},
		{Name: "r", Kind: model.RoleKindItem, Template: "tpl"},
	}); err == nil {
		t.Fatal("template with pattern must be rejected")
	}
}

func TestRBACUsers(t *testing.T) {
	svc := newRbacForTest(t)
	if err := svc.AddUser("alice", "password1", "alice@example.com", "李斌"); err != nil {
		t.Fatalf("add user: %v", err)
	}
	if err := svc.AddUser("alice", "password2", "alice2@example.com", "李斌"); err == nil {
		t.Fatal("duplicate user must be rejected")
	}
	if err := svc.AddUser("root", "password1", "root@example.com", "李斌"); err == nil {
		t.Fatal("user with admin name must be rejected")
	}
	if err := svc.AddUser("bob", "short", "bob@example.com", "李斌"); err == nil {
		t.Fatal("weak password must be rejected")
	}
	if !svc.VerifyLogin("alice", "password1") {
		t.Fatal("alice must be able to login")
	}
	if svc.VerifyLogin("alice", "wrong") {
		t.Fatal("wrong password must fail")
	}
	// 用户角色：分配后生效，删除用户后授权一并清理。
	if err := svc.SetRoles([]model.RbacRole{{Name: "dev", Kind: model.RoleKindGlobal, Perms: []string{model.PermJobBuild}}}); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetAssignments([]model.RbacAssignment{{Role: "dev", Kind: model.RoleKindGlobal, Subject: "alice"}}); err != nil {
		t.Fatal(err)
	}
	if !svc.HasPermission("alice", model.PermJobBuild) {
		t.Fatal("assigned permission must apply")
	}
	if err := svc.ResetUserPassword("alice", "newpassword"); err != nil {
		t.Fatal(err)
	}
	if !svc.VerifyLogin("alice", "newpassword") {
		t.Fatal("reset password must work")
	}
	if err := svc.DeleteUser("alice"); err != nil {
		t.Fatal(err)
	}
	if svc.VerifyLogin("alice", "newpassword") {
		t.Fatal("deleted user must not login")
	}
	if svc.HasPermission("alice", model.PermJobBuild) {
		t.Fatal("assignments must be cleaned after user deletion")
	}
	if err := svc.DeleteUser("ghost"); err == nil {
		t.Fatal("deleting missing user must fail")
	}
	var appErr *pkg.AppError
	if err := svc.DeleteUser("ghost"); !contains404(err, &appErr) {
		t.Fatal("missing user must be 40400")
	}
}

func contains404(err error, target **pkg.AppError) bool {
	if err == nil {
		return false
	}
	appErr, ok := err.(*pkg.AppError)
	if !ok {
		return false
	}
	*target = appErr
	return appErr.Code == pkg.CodeNotFound
}

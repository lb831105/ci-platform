package service

import (
	"testing"

	"ci-platform/internal/model"
)

// newTestRBAC 构造带指定角色与授权的 RBAC 服务（内置管理员 root）。
func newTestRBAC(t *testing.T, roles []model.RbacRole, assignments []model.RbacAssignment) *RBACService {
	t.Helper()
	svc, err := NewRBACService(&memRbacStore{data: &model.RbacData{
		Roles:       roles,
		Assignments: assignments,
		Users:       []model.RbacUser{{Name: "u1"}, {Name: "u2"}},
	}}, &fakeAuthChecker{enabled: true, admin: "root"})
	if err != nil {
		t.Fatal(err)
	}
	return svc
}

// 生效预览必须认同「administer 隐含全部权限」：
// 否则页面上会出现「有 admin 角色但可见任务 0 个」这种自相矛盾的显示。
func TestEffectiveViewAdministerImpliesAllJobs(t *testing.T) {
	svc := newTestRBAC(t, []model.RbacRole{
		{Name: "admin", Kind: model.RoleKindGlobal, Perms: []string{model.PermOverallAdminister}},
	}, []model.RbacAssignment{
		{Role: "admin", Kind: model.RoleKindGlobal, Subject: "libin"},
	})

	subjects, roles := svc.EffectiveView([]string{"as-doc-api", "prod-deploy"})
	var libin *SubjectEffective
	for i := range subjects {
		if subjects[i].Subject == "libin" {
			libin = &subjects[i]
		}
	}
	if libin == nil {
		t.Fatalf("subject libin missing: %+v", subjects)
	}
	if !libin.Administer || !libin.AllJobs {
		t.Fatalf("libin with administer must see all jobs, got %+v", libin)
	}
	if len(libin.VisibleJobs) != 2 {
		t.Fatalf("visible jobs = %v, want both", libin.VisibleJobs)
	}
	var adminRole *RoleMatch
	for i := range roles {
		if roles[i].Name == "admin" {
			adminRole = &roles[i]
		}
	}
	if adminRole == nil || len(adminRole.Permissions) != len(model.AllPermissionIDs()) {
		t.Fatalf("administer role must expand to all permissions, got %+v", adminRole)
	}
}

// 任务角色：Pattern 为空 → 覆盖 0 个任务（页面上会提示「不会生效」）。
func TestEffectiveViewItemRoleWithoutPatternMatchesNothing(t *testing.T) {
	svc := newTestRBAC(t, []model.RbacRole{
		{Name: "tpl", Kind: model.RoleKindItem, Perms: []string{model.PermJobRead}},
		{Name: "as-doc", Kind: model.RoleKindItem, Pattern: "as-doc-.+", Perms: []string{model.PermJobRead}},
	}, []model.RbacAssignment{
		{Role: "as-doc", Kind: model.RoleKindItem, Subject: "u1"},
	})

	subjects, roles := svc.EffectiveView([]string{"as-doc-api", "as-doc-web", "prod-deploy"})
	byName := map[string]RoleMatch{}
	for _, r := range roles {
		byName[r.Name] = r
	}
	if got := len(byName["tpl"].MatchedJobs); got != 0 {
		t.Fatalf("template role must match nothing, got %d", got)
	}
	if got := len(byName["as-doc"].MatchedJobs); got != 2 {
		t.Fatalf("as-doc matched %d jobs, want 2", got)
	}
	for _, s := range subjects {
		if s.Subject == "u1" && len(s.VisibleJobs) != 2 {
			t.Fatalf("u1 visible = %v, want 2 jobs", s.VisibleJobs)
		}
	}
}

// 模板继承：任务角色自己没有权限，靠 Template 拿到 job.read 也要能看见任务。
func TestEffectiveViewTemplateInheritance(t *testing.T) {
	svc := newTestRBAC(t, []model.RbacRole{
		{Name: "tpl-reader", Kind: model.RoleKindItem, Perms: []string{model.PermJobRead}},
		{Name: "prod", Kind: model.RoleKindItem, Pattern: "prod-.+", Template: "tpl-reader"},
	}, []model.RbacAssignment{
		{Role: "prod", Kind: model.RoleKindItem, Subject: "u2"},
	})

	subjects, _ := svc.EffectiveView([]string{"prod-deploy", "as-doc-api"})
	for _, s := range subjects {
		if s.Subject != "u2" {
			continue
		}
		if len(s.VisibleJobs) != 1 || s.VisibleJobs[0] != "prod-deploy" {
			t.Fatalf("u2 visible = %v, want [prod-deploy]", s.VisibleJobs)
		}
		return
	}
	t.Fatal("subject u2 missing")
}

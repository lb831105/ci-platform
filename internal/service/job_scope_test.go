package service

import (
	"testing"

	"ci-platform/internal/model"
)

// 任务可见性：任务角色的 Pattern 决定列表里出现哪些任务。
func TestJobScopeFiltersByItemRolePattern(t *testing.T) {
	scope := NewJobScope(false, []string{"as-doc-.+", `demo-\d+`})
	if scope.All() {
		t.Fatal("scoped filter must not be unrestricted")
	}
	cases := map[string]bool{
		"as-doc-api":  true, // 前缀匹配
		"AS-DOC-WEB":  true, // 忽略大小写
		"demo-1":      true, // 数字段（Pattern 是正则，不是 glob）
		"demo-12":     true,
		"demo-":       false,
		"as-docx":     false, // 必须完全匹配，不能只匹配前缀
		"prod-deploy": false,
		"as_doc-api":  false, // "." 是正则任意字符，但这里确实不匹配（下划线 vs 点）
	}
	for name, want := range cases {
		if got := scope.Allows(name); got != want {
			t.Fatalf("Allows(%q) = %v, want %v", name, got, want)
		}
	}
}

// 空/非法 Pattern 不该放大权限（宁可拒绝）。
func TestJobScopeIgnoresBadPatterns(t *testing.T) {
	scope := NewJobScope(false, []string{"", "a(b"})
	for _, name := range []string{"", "ab", "a(b"} {
		if scope.Allows(name) {
			t.Fatalf("Allows(%q) must be false for empty/invalid patterns", name)
		}
	}
}

func TestAllJobsAllowsEverything(t *testing.T) {
	scope := AllJobs()
	if !scope.All() || !scope.Allows("anything") {
		t.Fatal("AllJobs must allow everything")
	}
	// nil scope 也要安全（未装配 RBAC 的场景）。
	var nilScope *JobScope
	if !nilScope.Allows("x") {
		t.Fatal("nil scope must be treated as unrestricted")
	}
}

// Jenkins 里常见的内联标志与多选一共存写法要照旧可用：
// Pasted patterns like `(?i)as-doc.*` 或 `a|b` 不能因为外层加了锚点就失效。
func TestJobScopeAcceptsJenkinsStylePatterns(t *testing.T) {
	scope := NewJobScope(false, []string{`(?i)AS-DOC.+`, `clzg-admin|clzg_admin_front`})
	cases := map[string]bool{
		"as-doc-web":        true, // 内联 (?i) 与外层忽略大小写叠加无副作用
		"AS-DOC-WEB":        true,
		"clzg-admin":        true, // 分组选择
		"clzg_admin_front":  true,
		"clzg_admin_front2": false,
		"other":             false,
	}
	for name, want := range cases {
		if got := scope.Allows(name); got != want {
			t.Fatalf("Allows(%q) = %v, want %v", name, got, want)
		}
	}
}

// 任务配置者需要读凭据清单与通知联系人（这两个接口不属于具体任务）。
func TestHasAnyOfAllowsJobConfigureUsers(t *testing.T) {
	svc := newTestRBAC(t, []model.RbacRole{
		{Name: "ops", Kind: model.RoleKindItem, Pattern: "as-doc-.+", Perms: []string{model.PermJobConfigure}},
		{Name: "viewer", Kind: model.RoleKindGlobal, Perms: []string{model.PermSystemRead}},
	}, []model.RbacAssignment{
		{Role: "ops", Kind: model.RoleKindItem, Subject: "dev"},
		{Role: "viewer", Kind: model.RoleKindGlobal, Subject: "auditor"},
	})

	// 有任意任务的 configure → 可以读凭据清单 / 联系人
	if !svc.HasAnyOf("dev", []string{model.PermCredentialView}, []string{model.PermJobConfigure}) {
		t.Fatal("job.configure user must pass HasAnyOf")
	}
	// 全局权限命中也可
	if !svc.HasAnyOf("auditor", []string{model.PermSystemRead}, nil) {
		t.Fatal("global perm must pass HasAnyOf")
	}
	// 什么都没有必须拒绝（普通只读用户不能读凭据清单）
	if svc.HasAnyOf("nobody", []string{model.PermCredentialView}, []string{model.PermJobConfigure}) {
		t.Fatal("unassigned user must be denied")
	}
	// 内置管理员始终通过
	if !svc.HasAnyOf("root", []string{model.PermCredentialView}, nil) {
		t.Fatal("builtin admin must pass")
	}
}

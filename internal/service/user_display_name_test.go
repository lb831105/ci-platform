package service

import (
	"fmt"
	"strings"
	"testing"
)

// 姓名是展示字段，必须与登录用户名分开：libin 是账号，李斌是姓名。
// 这里同时锁住后端校验，避免只靠前端绕过。
func TestRBACUserDisplayName(t *testing.T) {
	svc := newRbacForTest(t)

	bad := []string{
		"",
		"LiBin",
		"李",
		"李1",
		"李 斌",
		"·李斌",
		"李斌·",
		strings.Repeat("李", 21),
	}
	for i, name := range bad {
		if err := svc.AddUser(fmt.Sprintf("bad%d", i), "password1", fmt.Sprintf("bad%d@example.com", i), name); err == nil {
			t.Fatalf("display name %q must be rejected", name)
		}
	}

	if err := svc.AddUser("libin", "password1", "libin@example.com", "李斌"); err != nil {
		t.Fatalf("add user with Chinese display name: %v", err)
	}
	if err := svc.AddUser("ayiguli", "password1", "ayiguli@example.com", "阿依古丽·买买提"); err != nil {
		t.Fatalf("ethnic name with middle dot must be accepted: %v", err)
	}

	found := false
	for _, u := range svc.Users() {
		if u.Name == "libin" {
			found = true
			if u.DisplayName != "李斌" {
				t.Fatalf("display name = %q, want 李斌", u.DisplayName)
			}
		}
	}
	if !found {
		t.Fatal("created user must appear in user list")
	}
	if p, err := svc.Profile("libin"); err != nil || p.DisplayName != "李斌" {
		t.Fatalf("profile display name = %q (err=%v), want 李斌", p.DisplayName, err)
	}

	// 用户在个人资料页改联系方式时不能顺手改姓名，原姓名必须保留。
	if err := svc.SetUserContact("libin", "libin@example.com", "wx-id", "13800000000"); err != nil {
		t.Fatalf("update contact: %v", err)
	}
	if p, err := svc.Profile("libin"); err != nil || p.DisplayName != "李斌" {
		t.Fatalf("contact update must preserve display name: %+v err=%v", p, err)
	}

	// 管理员可以在「用户管理」中补填/纠正姓名（历史用户没有姓名时尤其需要）。
	if err := svc.SetUserContactAndDisplayName("libin", "李彬", "libin@example.com", "", ""); err != nil {
		t.Fatalf("admin update display name: %v", err)
	}
	if p, err := svc.Profile("libin"); err != nil || p.DisplayName != "李彬" {
		t.Fatalf("admin display name update = %q (err=%v), want 李彬", p.DisplayName, err)
	}
	if err := svc.SetUserContactAndDisplayName("libin", "libin", "libin@example.com", "", ""); err == nil {
		t.Fatal("admin update must reject non-Chinese display name")
	}
}

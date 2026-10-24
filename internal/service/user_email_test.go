package service

import (
	"strings"
	"testing"

	"ci-platform/internal/model"
)

// 用户邮箱必填：没有邮箱的账号收不到构建通知邮件，必须在入口拦住。
func TestRBACUserEmailRequired(t *testing.T) {
	svc := newRbacForTest(t)

	if err := svc.AddUser("noemail", "password1", ""); err == nil {
		t.Fatal("creating a user without email must be rejected")
	}
	for _, bad := range []string{"not-an-email", "a@b", "@b.com", "a@.com", "a b@c.com"} {
		if err := svc.AddUser("u1", "password1", bad); err == nil {
			t.Fatalf("invalid email %q must be rejected", bad)
		}
	}
	if err := svc.AddUser("u1", "password1", "  u1@example.com  "); err != nil {
		t.Fatalf("add user with valid email: %v", err)
	}

	// 创建时写入的邮箱要能立即用于通知（Contacts 返回）。
	contact, ok := svc.Contacts([]string{"u1"})["u1"]
	if !ok || strings.TrimSpace(contact.Email) != "u1@example.com" {
		t.Fatalf("contact email = %q (ok=%v), want trimmed address", contact.Email, ok)
	}

	// 管理员改联系方式同样不允许清空邮箱。
	if err := svc.SetUserContact("u1", "", "", ""); err == nil {
		t.Fatal("clearing email via SetUserContact must be rejected")
	}
	if err := svc.SetUserContact("u1", "u1b@example.com", "wecom", "138"); err != nil {
		t.Fatalf("update contact: %v", err)
	}
	// 改邮箱后验证状态必须失效（否则「已验证」会继承到新地址）。
	if p, err := svc.Profile("u1"); err != nil || p.EmailVerified {
		t.Fatalf("email change must reset verification: %+v err=%v", p, err)
	}
}

// 历史数据兜底：旧用户可能没有邮箱，需要能列举出来（启动告警 + UI 提示）。
func TestRBACUsersWithoutEmail(t *testing.T) {
	svc := newRbacForTest(t)
	if err := svc.AddUser("ok", "password1", "ok@example.com"); err != nil {
		t.Fatal(err)
	}
	// 模拟旧数据：直接塞一个没有邮箱的用户。
	svc.mu.Lock()
	svc.data.Users = append(svc.data.Users, model.RbacUser{Name: "legacy"})
	svc.mu.Unlock()

	missing := svc.UsersWithoutEmail()
	if len(missing) != 1 || missing[0] != "legacy" {
		t.Fatalf("UsersWithoutEmail = %v, want [legacy]", missing)
	}
	// 用户管理页能看出未配置（email 为空）。
	found := false
	for _, u := range svc.Users() {
		if u.Name == "legacy" {
			found = true
			if u.Email != "" {
				t.Fatalf("legacy email = %q, want empty", u.Email)
			}
		}
	}
	if !found {
		t.Fatal("legacy user must appear in user list")
	}
}

// 通知计划要显式指出「谁收不到邮件」，避免选了人却静默失败。
func TestNotifyPlanWarnsMissingEmail(t *testing.T) {
	users := &fakeUserDir{users: map[string]model.RbacUser{
		"noemail": {Name: "noemail"}, // 历史用户：没有邮箱
		"ok":      {Name: "ok", Email: "ok@example.com"},
	}}
	svc := NewNotifyService(&fakeSettingsProvider{settings: model.Settings{
		Email: model.EmailSettings{Enabled: true, NotifyOnFailure: true},
	}}, users)
	p := testPipelineWithNotify(&model.JobNotify{
		Enabled: true,
		Events:  model.NotifyEvents{OnFailure: true},
		People:  []string{"noemail", "ok", "ghost"},
	})
	plan := svc.plan(p, &model.Build{ID: "1", Status: model.BuildFailed}, nil)

	warns := strings.Join(plan.warnings(), " ")
	if !strings.Contains(warns, "noemail") || !strings.Contains(warns, "未配置邮箱") {
		t.Fatalf("warnings must mention missing email: %v", plan.warnings())
	}
	if !strings.Contains(warns, "ghost") || !strings.Contains(warns, "不存在") {
		t.Fatalf("warnings must mention unknown user: %v", plan.warnings())
	}
	// 有邮箱的人照常收信；缺邮箱的人仍然参与群 @ 提醒。
	if len(plan.emails) != 1 || plan.emails[0] != "ok@example.com" {
		t.Fatalf("emails = %v, want only ok@example.com", plan.emails)
	}
	people := make([]string, 0, len(plan.people))
	for _, u := range plan.people {
		people = append(people, u.Name)
	}
	if strings.Join(people, ",") != "noemail,ok" {
		t.Fatalf("people = %v, want noemail+ok", people)
	}

	// 构建日志摘要要带上警告。
	res := NotifyResult{Event: model.NotifyEventFailure, Warnings: plan.warnings()}
	if line := res.Line(); !strings.Contains(line, "警告") {
		t.Fatalf("result line must include warning: %q", line)
	}
}

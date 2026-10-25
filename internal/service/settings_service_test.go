package service

import (
	"testing"

	"golang.org/x/crypto/bcrypt"

	"ci-platform/internal/model"
	"ci-platform/internal/store"
)

func TestValidateSettingsSshServers(t *testing.T) {
	ok := model.Settings{SshServers: []model.SshServer{
		{Name: "prod", Host: "deploy.example.com", Username: "deploy", AuthType: "password", Password: "p"},
	}}
	if err := validateSettings(&ok); err != nil {
		t.Fatalf("valid server rejected: %v", err)
	}
	if ok.SshServers[0].ID == "" {
		t.Fatal("new server should get an id")
	}
	if ok.SshServers[0].Port != 22 {
		t.Fatalf("port = %d, want 22", ok.SshServers[0].Port)
	}
	if ok.SshServers[0].HasPassword {
		t.Fatal("has_password must not be persisted")
	}

	bad := []model.SshServer{
		{Name: "", Host: "h", Username: "u", AuthType: "password", Password: "p"},
		{Name: "x", Host: "", Username: "u", AuthType: "password", Password: "p"},
		{Name: "x", Host: "h", Username: "", AuthType: "password", Password: "p"},
		{Name: "x", Host: "h", Username: "u", AuthType: "password"},
		{Name: "x", Host: "h", Username: "u", AuthType: "key"},
		{Name: "x", Host: "h", Username: "u", AuthType: "bogus"},
	}
	for _, server := range bad {
		in := model.Settings{SshServers: []model.SshServer{server}}
		if err := validateSettings(&in); err == nil {
			t.Errorf("server %+v should be rejected", server)
		}
	}
}

// 初始密码只影响「首次初始化」：改过之后，即使再次带着 -auth-password 启动，
// 也不能把密码覆盖回启动参数里的值——否则重启就复位，等于改了个寂寞。
func TestAdminPasswordChangeSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	settingsStore := store.NewSettingsStore(dir)
	defaults := model.Settings{AuthUser: "admin", AuthPassword: "admin"}

	svc, err := NewSettingsService(settingsStore, defaults)
	if err != nil {
		t.Fatalf("init settings: %v", err)
	}
	// 等同于「个人资料 → 修改密码 / 全局安全配置 → 设置管理员密码」。
	current := svc.Current()
	current.AuthPassword = "new-password-1"
	updated, err := svc.Update(current)
	if err != nil {
		t.Fatalf("update password: %v", err)
	}
	if updated.AuthPasswordHash == "" || updated.AuthPassword != "" {
		t.Fatal("password must be stored as bcrypt hash only")
	}
	if bcrypt.CompareHashAndPassword([]byte(updated.AuthPasswordHash), []byte("new-password-1")) != nil {
		t.Fatal("stored hash must match the new password")
	}

	// 模拟重启：同一数据目录 + 同样的启动参数（仍带着旧口令）。
	restarted, err := NewSettingsService(settingsStore, defaults)
	if err != nil {
		t.Fatalf("reload settings: %v", err)
	}
	got := restarted.Current()
	if bcrypt.CompareHashAndPassword([]byte(got.AuthPasswordHash), []byte("new-password-1")) != nil {
		t.Fatal("changed password must survive restart")
	}
	if bcrypt.CompareHashAndPassword([]byte(got.AuthPasswordHash), []byte("admin")) == nil {
		t.Fatal("startup flag must not override the changed password")
	}
}

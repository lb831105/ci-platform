package handler

import (
	"testing"

	"ci-platform/internal/model"
)

// 返回设置视图时必须清除 SSH 密码，且不能修改服务内存中的原配置。
func TestToSettingsViewMasksSshPassword(t *testing.T) {
	original := model.Settings{SshServers: []model.SshServer{
		{ID: "s1", Name: "prod", Password: "secret"},
	}}
	view := toSettingsView(original)
	if original.SshServers[0].Password != "secret" {
		t.Fatal("original settings must not be mutated (slice shares backing array)")
	}
	if view.SshServers[0].Password != "" {
		t.Fatalf("view password = %q, want empty", view.SshServers[0].Password)
	}
	if !view.SshServers[0].HasPassword {
		t.Fatal("view should flag has_password")
	}
}

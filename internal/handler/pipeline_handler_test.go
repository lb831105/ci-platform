package handler

import (
	"testing"

	"ci-platform/internal/model"
)

// 读取接口不得回显 SSH 发布密码。
func TestSanitizePipelineClearsSshPassword(t *testing.T) {
	p := &model.Pipeline{
		Name: "demo",
		SshPublish: &model.SshPublishConfig{
			Enabled:  true,
			Host:     "example.com",
			Username: "deploy",
			Password: "s3cret",
		},
	}
	sanitizePipeline(p)
	if p.SshPublish.Password != "" {
		t.Fatalf("ssh password must be cleared, got %q", p.SshPublish.Password)
	}
}

func TestSanitizePipelineNilSafe(t *testing.T) {
	if got := sanitizePipeline(nil); got != nil {
		t.Fatalf("nil pipeline must stay nil")
	}
	plain := &model.Pipeline{Name: "no-ssh"}
	if got := sanitizePipeline(plain); got != plain {
		t.Fatalf("pipeline without ssh config must be returned as-is")
	}
}

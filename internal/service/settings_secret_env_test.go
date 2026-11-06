package service

import (
	"testing"

	"ci-platform/internal/model"
	"ci-platform/internal/store"
)

// API 不回传密钥变量原文；前端留空保存时必须沿用旧值，不能把密钥清空。
func TestUpdateSettingsKeepsSecretEnvWhenBlank(t *testing.T) {
	svc, err := NewSettingsService(store.NewSettingsStore(t.TempDir()), model.Settings{})
	if err != nil {
		t.Fatal(err)
	}
	current := svc.Current()
	current.GlobalEnv = []model.EnvVar{
		{Key: "NPM_TOKEN", Value: "secret-value", Secret: true},
		{Key: "LANG", Value: "en_US.UTF-8"},
	}
	if _, err = svc.Update(current); err != nil {
		t.Fatalf("seed settings: %v", err)
	}

	next := svc.Current()
	next.GlobalEnv = []model.EnvVar{
		{Key: "NPM_TOKEN", Value: "", Secret: true},
		{Key: "LANG", Value: "zh_CN.UTF-8"},
	}
	updated, err := svc.Update(next)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.GlobalEnv[0].Value != "secret-value" {
		t.Fatalf("secret env lost: %+v", updated.GlobalEnv)
	}
	if updated.GlobalEnv[1].Value != "zh_CN.UTF-8" {
		t.Fatalf("normal env should update: %+v", updated.GlobalEnv)
	}
}

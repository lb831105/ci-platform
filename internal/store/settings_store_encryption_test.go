package store

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ci-platform/internal/model"
)

func TestSettingsStoreEncryptsSecrets(t *testing.T) {
	dir := t.TempDir()
	store := NewSettingsStore(dir)
	in := &model.Settings{
		Email: model.EmailSettings{Password: "smtp-secret"},
		WebhookNotify: model.WebhookNotifySettings{
			URL: "https://hooks.example.com/secret-token",
		},
		SshServers: []model.SshServer{{ID: "s1", Name: "prod", Password: "ssh-secret"}},
		GlobalEnv: []model.EnvVar{
			{Key: "NPM_TOKEN", Value: "npm-secret"},
			{Key: "LANG", Value: "en_US.UTF-8"},
			{Key: "CUSTOM", Value: "custom-secret", Secret: true},
		},
	}
	if err := store.Save(in); err != nil {
		t.Fatalf("save: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, plain := range []string{"smtp-secret", "secret-token", "ssh-secret", "npm-secret", "custom-secret"} {
		if strings.Contains(string(raw), plain) {
			t.Fatalf("settings.json leaked %q", plain)
		}
	}
	if !strings.Contains(string(raw), "enc:v1:") {
		t.Fatal("settings.json should contain encrypted values")
	}
	if in.Email.Password != "smtp-secret" || in.SshServers[0].Password != "ssh-secret" {
		t.Fatal("Save must not mutate caller secrets")
	}
	loaded, err := store.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if loaded.Email.Password != "smtp-secret" || loaded.SshServers[0].Password != "ssh-secret" ||
		loaded.WebhookNotify.URL != "https://hooks.example.com/secret-token" {
		t.Fatalf("decrypted settings = %+v", loaded)
	}
	if loaded.GlobalEnv[0].Value != "npm-secret" || loaded.GlobalEnv[1].Value != "en_US.UTF-8" ||
		loaded.GlobalEnv[2].Value != "custom-secret" {
		t.Fatalf("global env = %+v", loaded.GlobalEnv)
	}
}

func TestPipelineStoreEncryptsSecrets(t *testing.T) {
	jobs := filepath.Join(t.TempDir(), "data", "jobs")
	store := NewPipelineStore(jobs)
	p := &model.Pipeline{
		ID: "p1", Name: "demo",
		WebhookSecret: "webhook-secret",
		SshPublish:    &model.SshPublishConfig{Password: "inline-secret"},
		SshPublishes:  []model.SshPublishConfig{{Password: "multi-secret"}},
		Notify: &model.JobNotify{Targets: []model.NotifyTarget{{
			Channel: "slack", URL: "https://hooks.example.com/notify-secret",
		}}},
	}
	if err := store.Create(context.Background(), p); err != nil {
		t.Fatalf("create: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(jobs, "p1", "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, plain := range []string{"webhook-secret", "inline-secret", "multi-secret", "notify-secret"} {
		if strings.Contains(string(raw), plain) {
			t.Fatalf("config.json leaked %q", plain)
		}
	}
	loaded, err := store.Get(context.Background(), "p1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if loaded.WebhookSecret != "webhook-secret" || loaded.SshPublish.Password != "inline-secret" ||
		loaded.SshPublishes[0].Password != "multi-secret" ||
		loaded.Notify.Targets[0].URL != "https://hooks.example.com/notify-secret" {
		t.Fatalf("decrypted pipeline = %+v", loaded)
	}
	if p.WebhookSecret != "webhook-secret" || p.SshPublishes[0].Password != "multi-secret" {
		t.Fatal("Create must not mutate caller pipeline")
	}
}

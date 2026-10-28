package service

import (
	"context"
	"strings"
	"testing"

	"ci-platform/internal/model"
	"ci-platform/internal/store"
)

func TestCreateCredentialRequiresSecret(t *testing.T) {
	svc := NewCredentialService(store.NewCredentialStore(t.TempDir()))
	_, err := svc.Create(context.Background(), &model.Credential{
		Name:     "empty",
		Type:     model.CredentialUsernamePassword,
		Username: "u",
	})
	if err == nil {
		t.Fatal("credential without secret must be rejected")
	}
}

// SSH 私钥凭据没有任何使用方（Git 走 HTTPS、SSH 发布的私钥路径配在服务器上），
// 必须在创建时明确拒绝，而不是让人存一份永远不会生效的密钥。
func TestCreateCredentialRejectsSSHKey(t *testing.T) {
	svc := NewCredentialService(store.NewCredentialStore(t.TempDir()))
	_, err := svc.Create(context.Background(), &model.Credential{
		Name:   "deploy-key",
		Type:   model.CredentialSSHKey,
		Secret: "-----BEGIN OPENSSH PRIVATE KEY-----",
	})
	if err == nil {
		t.Fatal("ssh_key credential must be rejected with a guidance message")
	}
	if !strings.Contains(err.Error(), "SSH 服务器") {
		t.Fatalf("error should point to the SSH server config, got %v", err)
	}
}

func TestCredentialAllowedHosts(t *testing.T) {
	hosts, err := normalizeCredentialHosts([]string{" Git.Example.com ", "https://git.example.com/group/repo", "*.corp.example.com", "git.example.com"})
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if len(hosts) != 2 || hosts[0] != "git.example.com" || hosts[1] != "*.corp.example.com" {
		t.Fatalf("hosts = %+v", hosts)
	}
	c := &model.Credential{Name: "git", AllowedHosts: []string{"git.example.com", "*.corp.example.com"}}
	if err := ensureCredentialHost(c, "https://git.example.com/group/repo.git"); err != nil {
		t.Fatalf("exact host should be allowed: %v", err)
	}
	if err := ensureCredentialHost(c, "https://ci.corp.example.com/repo.git"); err != nil {
		t.Fatalf("wildcard host should be allowed: %v", err)
	}
	if err := ensureCredentialHost(c, "https://evil.example.net/repo.git"); err == nil {
		t.Fatal("unlisted host must be rejected")
	}
}

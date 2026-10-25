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

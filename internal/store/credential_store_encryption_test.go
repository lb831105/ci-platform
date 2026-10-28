package store

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ci-platform/internal/model"
	"ci-platform/internal/pkg"
)

// 凭据 Secret 落盘必须是密文，且读取时能还原。
func TestCredentialStoreEncryptsSecretAtRest(t *testing.T) {
	dir := t.TempDir()
	s := NewCredentialStore(dir)
	ctx := context.Background()

	secret := "super-secret-token-value"
	c := &model.Credential{ID: "cred-1", Name: "git", Type: model.CredentialSecretText, Secret: secret}
	if err := s.Create(ctx, c); err != nil {
		t.Fatalf("Create: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "credentials", "cred-1.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), secret) {
		t.Fatalf("secret leaked to disk: %s", raw)
	}
	if !strings.Contains(string(raw), encryptedSecretPrefix) {
		t.Fatalf("encrypted marker missing: %s", raw)
	}
	if !pkg.Exists(filepath.Join(dir, "credentials.key")) {
		t.Fatal("credentials.key was not created")
	}

	got, err := s.Get(ctx, "cred-1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Secret != secret {
		t.Fatalf("secret = %q, want %q", got.Secret, secret)
	}
	if c.Secret != secret {
		t.Fatal("Create must not mutate caller's plaintext credential")
	}
}

// 旧版本明文凭据读取后应自动升级为密文，避免升级后继续明文落盘。
func TestCredentialStoreMigratesLegacyPlaintext(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "credentials", "legacy.json")
	legacy := &model.Credential{ID: "legacy", Name: "old", Type: model.CredentialUsernamePassword, Username: "u", Secret: "legacy-plain"}
	if err := pkg.WriteJSONAtomic(path, legacy); err != nil {
		t.Fatal(err)
	}

	s := NewCredentialStore(dir)
	got, err := s.Get(context.Background(), "legacy")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Secret != "legacy-plain" {
		t.Fatalf("secret = %q", got.Secret)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "legacy-plain") {
		t.Fatalf("legacy plaintext was not migrated: %s", raw)
	}
}

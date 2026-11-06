package store

import (
	"os"
	"path/filepath"
	"testing"

	"ci-platform/internal/pkg"
)

func TestCredentialStoreKeyReadyDoesNotCreateMissingKey(t *testing.T) {
	home := t.TempDir()
	s := NewCredentialStore(home)
	if err := s.KeyReady(); err != nil {
		t.Fatalf("fresh data home: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, "credentials.key")); !os.IsNotExist(err) {
		t.Fatalf("KeyReady must not create credentials.key: %v", err)
	}
}

func TestCredentialStoreKeyReadyFailsWhenEncryptedDataExists(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, "credentials")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "c1.json"), []byte(`{"id":"c1","secret":"enc:v1:AAAA"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	s := NewCredentialStore(home)
	if err := s.KeyReady(); err == nil {
		t.Fatal("missing key with encrypted data must fail readiness")
	}
	if _, err := os.Stat(filepath.Join(home, "credentials.key")); !os.IsNotExist(err) {
		t.Fatalf("KeyReady must not create credentials.key: %v", err)
	}
}

func TestCredentialStoreUnsealDoesNotCreateKey(t *testing.T) {
	home := t.TempDir()
	s := NewCredentialStore(home)
	if _, err := s.UnsealSecret(pkg.EncryptedSecretPrefix + "AAAA"); err == nil {
		t.Fatal("unseal without key must fail")
	}
	if _, err := os.Stat(filepath.Join(home, "credentials.key")); !os.IsNotExist(err) {
		t.Fatalf("unseal must not create credentials.key: %v", err)
	}
}

package pkg

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSecretBoxUnsealDoesNotCreateMissingKey(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "credentials.key")
	box := NewSecretBox(keyPath)
	if _, err := box.Unseal(EncryptedSecretPrefix + "AAAA"); err == nil {
		t.Fatal("unseal without key must fail")
	}
	if _, err := os.Stat(keyPath); !os.IsNotExist(err) {
		t.Fatalf("unseal must not create credentials.key: %v", err)
	}
	if err := box.KeyReady(); err == nil {
		t.Fatal("KeyReady must fail when key is missing")
	}
}

func TestSecretBoxRoundTripCreatesKeyOnSeal(t *testing.T) {
	dir := t.TempDir()
	box := NewSecretBox(filepath.Join(dir, "credentials.key"))
	sealed, err := box.Seal("hunter2")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(sealed, EncryptedSecretPrefix) {
		t.Fatalf("sealed = %q", sealed)
	}
	plain, err := box.Unseal(sealed)
	if err != nil || plain != "hunter2" {
		t.Fatalf("unseal = %q, %v", plain, err)
	}
	if err := box.KeyReady(); err != nil {
		t.Fatalf("KeyReady after Seal: %v", err)
	}
}

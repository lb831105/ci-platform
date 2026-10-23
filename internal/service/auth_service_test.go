package service

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ci-platform/internal/model"
	"ci-platform/internal/store"
)

func TestAuthServiceUsesBcryptHash(t *testing.T) {
	auth := NewAuthService(true, "admin", "secret", time.Hour)
	if auth.passwordHash == "" || auth.passwordHash == "secret" || !strings.HasPrefix(auth.passwordHash, "$2") {
		t.Fatalf("password must be bcrypt hashed, got %q", auth.passwordHash)
	}
	token, user, _, err := auth.Login("admin", "secret")
	if err != nil || user != "admin" || token == "" {
		t.Fatalf("login: token=%q user=%q err=%v", token, user, err)
	}
	if _, _, _, err = auth.Login("admin", "wrong"); err == nil {
		t.Fatal("wrong password must fail")
	}
	if err = auth.ChangePassword("secret", "new-secret", token); err != nil {
		t.Fatalf("change password: %v", err)
	}
	if _, _, _, err = auth.Login("admin", "secret"); err == nil {
		t.Fatal("old password must fail after change")
	}
	if _, _, _, err = auth.Login("admin", "new-secret"); err != nil {
		t.Fatalf("new password login: %v", err)
	}
}

func TestSettingsServiceMigratesPlaintextAuthPassword(t *testing.T) {
	dir := t.TempDir()
	settingsStore := store.NewSettingsStore(dir)
	legacy := model.Settings{AuthEnabled: true, AuthUser: "admin", AuthPassword: "legacy"}
	if err := settingsStore.Save(&legacy); err != nil {
		t.Fatal(err)
	}
	svc, err := NewSettingsService(settingsStore, model.Settings{AuthUser: "admin", AuthPassword: "default"})
	if err != nil {
		t.Fatalf("new settings: %v", err)
	}
	current := svc.Current()
	if current.AuthPassword != "" || current.AuthPasswordHash == "" {
		t.Fatalf("plaintext not migrated: password=%q hash=%q", current.AuthPassword, current.AuthPasswordHash)
	}
	var persisted model.Settings
	data, readErr := os.ReadFile(filepath.Join(dir, "settings.json"))
	if readErr != nil {
		t.Fatal(readErr)
	}
	if err = json.Unmarshal(data, &persisted); err != nil {
		t.Fatal(err)
	}
	if persisted.AuthPassword != "" || persisted.AuthPasswordHash == "" {
		t.Fatalf("plaintext persisted: %+v", persisted)
	}
	auth := NewAuthService(true, "admin", current.AuthPasswordHash, time.Hour)
	if _, _, _, err = auth.Login("admin", "legacy"); err != nil {
		t.Fatalf("legacy password should still work: %v", err)
	}
}

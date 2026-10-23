package service

import (
	"context"
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

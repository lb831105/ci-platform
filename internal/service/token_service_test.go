package service

import (
	"testing"
	"time"

	"ci-platform/internal/model"
)

// memTokenStore 是测试用的内存令牌存储。
type memTokenStore struct{ items []model.APIToken }

func (s *memTokenStore) Load() ([]model.APIToken, error) {
	out := make([]model.APIToken, len(s.items))
	copy(out, s.items)
	return out, nil
}

func (s *memTokenStore) Save(items []model.APIToken) error {
	s.items = make([]model.APIToken, len(items))
	copy(s.items, items)
	return nil
}

func TestAPITokenLifecycle(t *testing.T) {
	store := &memTokenStore{}
	svc := NewAPITokenService(store)

	created, err := svc.Create("alice", "deploy", 30, []string{"job.read", "job.build"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.Secret == "" || created.Token.Prefix == "" {
		t.Fatalf("empty secret/prefix: %+v", created)
	}
	if len(store.items) != 1 || store.items[0].TokenHash == created.Secret {
		t.Fatal("store must persist hash instead of raw token")
	}
	if user, scopes, ok := svc.VerifyAPIToken(created.Secret); !ok || user != "alice" || len(scopes) != 2 {
		t.Fatalf("verify = %q, %v, %v", user, scopes, ok)
	}
	if _, _, ok := svc.VerifyAPIToken(created.Secret + "x"); ok {
		t.Fatal("tampered token must be rejected")
	}

	list, err := svc.List("alice")
	if err != nil || len(list) != 1 || list[0].ID != created.Token.ID {
		t.Fatalf("list = %+v, %v", list, err)
	}
	if err := svc.Revoke("bob", created.Token.ID); err == nil {
		t.Fatal("other user must not revoke token")
	}
	if err := svc.Revoke("alice", created.Token.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if _, _, ok := svc.VerifyAPIToken(created.Secret); ok {
		t.Fatal("revoked token must be rejected")
	}
}

func TestAPITokenScopes(t *testing.T) {
	svc := NewAPITokenService(&memTokenStore{})
	created, err := svc.Create("alice", "reader", 0, []string{"job.read", "job.read"})
	if err != nil {
		t.Fatalf("create scoped: %v", err)
	}
	if len(created.Token.Scopes) != 1 || created.Token.Scopes[0] != "job.read" {
		t.Fatalf("scopes = %+v", created.Token.Scopes)
	}
	if _, _, ok := svc.VerifyAPIToken(created.Secret); !ok {
		t.Fatal("scoped token should verify")
	}
	if _, err := svc.Create("alice", "bad", 0, []string{"root"}); err == nil {
		t.Fatal("unknown scope must be rejected")
	}
	full, err := svc.Create("alice", "full", 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(full.Token.Scopes) != 1 || full.Token.Scopes[0] != "*" {
		t.Fatalf("default scopes = %+v", full.Token.Scopes)
	}
}

func TestAPITokenExpiryAndUserDeletion(t *testing.T) {
	store := &memTokenStore{}
	svc := NewAPITokenService(store)
	now := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	svc.now = func() time.Time { return now }

	created, err := svc.Create("alice", "short", 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	svc.now = func() time.Time { return now.Add(2 * 24 * time.Hour) }
	if _, _, ok := svc.VerifyAPIToken(created.Secret); ok {
		t.Fatal("expired token must be rejected")
	}

	svc.now = func() time.Time { return now }
	created, err = svc.Create("alice", "long", 30, nil)
	if err != nil {
		t.Fatal(err)
	}
	svc.SetUserChecker(func(name string) bool { return name != "alice" })
	if _, _, ok := svc.VerifyAPIToken(created.Secret); ok {
		t.Fatal("token of deleted user must be rejected")
	}
}

func TestAPITokenLimitsPerUser(t *testing.T) {
	svc := NewAPITokenService(&memTokenStore{})
	for i := 0; i < maxAPITokensPerUser; i++ {
		if _, err := svc.Create("alice", "t", 0, nil); err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
	}
	if _, err := svc.Create("alice", "overflow", 0, nil); err == nil {
		t.Fatal("token count limit must be enforced")
	}
	if _, err := svc.Create("bob", "other", 0, nil); err != nil {
		t.Fatalf("other user must not be affected: %v", err)
	}
}

// 认证服务必须同时接受登录会话和 API Token。
func TestAuthServiceVerifiesAPIToken(t *testing.T) {
	auth := NewAuthService(true, "admin", "password123", time.Hour)
	auth.SetAPITokenVerifier(tokenVerifierFunc(func(raw string) (string, bool) {
		if raw == "cip_valid" {
			return "alice", true
		}
		return "", false
	}))
	if user, ok := auth.Verify("cip_valid"); !ok || user != "alice" {
		t.Fatalf("verify token = %q, %v", user, ok)
	}
	if _, ok := auth.Verify("cip_bad"); ok {
		t.Fatal("invalid token must be rejected")
	}
}

type tokenVerifierFunc func(string) (string, bool)

func (f tokenVerifierFunc) VerifyAPIToken(raw string) (string, []string, bool) {
	user, ok := f(raw)
	return user, []string{"job.read"}, ok
}

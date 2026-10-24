package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"ci-platform/internal/model"
	"ci-platform/internal/pkg"
)

type fakeAuditPoster struct {
	entries []model.AuditEntry
}

func (f *fakeAuditPoster) Post(e model.AuditEntry) {
	f.entries = append(f.entries, e)
}

func auditTestHandler(poster *fakeAuditPoster) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/pipelines", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"code":0,"msg":"ok","data":null}`))
	})
	mux.HandleFunc("GET /api/pipelines", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"code":0,"msg":"ok","data":[]}`))
	})
	mux.HandleFunc("POST /api/auth/login", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"code":0,"msg":"ok","data":{"token":"t","username":"alice","expires_at":"x"}}`))
	})
	mux.HandleFunc("POST /api/auth/login-fail", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"code":40100,"msg":"bad credentials"}`))
	})
	// 真实链路：Auth 在外包住 Audit（请求先过 Auth 写入用户，再进 Audit）。
	audited := Audit(poster)(mux)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/pipelines" {
			r = r.WithContext(pkg.WithUser(r.Context(), "dev1"))
		}
		audited.ServeHTTP(w, r)
	})
}

// 变更类请求被记录（用户 + 业务码）。
func TestAuditRecordsMutations(t *testing.T) {
	poster := &fakeAuditPoster{}
	h := auditTestHandler(poster)
	req := httptest.NewRequest(http.MethodPost, "/api/pipelines", nil)
	h.ServeHTTP(httptest.NewRecorder(), req)
	if len(poster.entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(poster.entries))
	}
	e := poster.entries[0]
	if e.User != "dev1" || e.Action != "create" || e.Status != 0 {
		t.Fatalf("entry wrong: %+v", e)
	}
	if e.Target != "POST /api/pipelines" {
		t.Fatalf("target = %q", e.Target)
	}
}

// 查询类请求不记录。
func TestAuditSkipsGET(t *testing.T) {
	poster := &fakeAuditPoster{}
	h := auditTestHandler(poster)
	req := httptest.NewRequest(http.MethodGet, "/api/pipelines", nil)
	h.ServeHTTP(httptest.NewRecorder(), req)
	if len(poster.entries) != 0 {
		t.Fatalf("GET should not be audited, got %d", len(poster.entries))
	}
}

// 登录成功：动作 login + 用户名取自响应体。
func TestAuditLoginUsername(t *testing.T) {
	poster := &fakeAuditPoster{}
	h := auditTestHandler(poster)
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", nil)
	h.ServeHTTP(httptest.NewRecorder(), req)
	if len(poster.entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(poster.entries))
	}
	e := poster.entries[0]
	if e.Action != model.AuditLogin || e.User != "alice" || e.Status != 0 {
		t.Fatalf("entry wrong: %+v", e)
	}
}

// 业务失败（40100）被捕获。
func TestAuditCapturesBizCode(t *testing.T) {
	poster := &fakeAuditPoster{}
	h := auditTestHandler(poster)
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login-fail", nil)
	h.ServeHTTP(httptest.NewRecorder(), req)
	if len(poster.entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(poster.entries))
	}
	if poster.entries[0].Status != 40100 {
		t.Fatalf("status = %d, want 40100", poster.entries[0].Status)
	}
	// 确保响应体未被破坏（仍是合法 JSON）
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/auth/login-fail", nil))
	var out map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("response body broken: %v", err)
	}
}

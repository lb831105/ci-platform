package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"ci-platform/internal/pkg"
)

func TestRequestIDGeneratedAndEchoed(t *testing.T) {
	handler := RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if id := rec.Header().Get("X-Request-ID"); id == "" {
		t.Fatal("X-Request-ID must be set")
	}
	if len(rec.Header().Values("X-Request-ID")) != 1 {
		t.Fatal("exactly one request id expected")
	}
}

func TestRequestIDTrustsShortIncomingID(t *testing.T) {
	var got string
	handler := RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = RequestIDFromContext(r.Context())
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Request-ID", "abc-123")
	handler.ServeHTTP(httptest.NewRecorder(), req)
	if got != "abc-123" {
		t.Fatalf("incoming id should be trusted, got %q", got)
	}
}

func TestRequestIDRejectsMalformedIncomingID(t *testing.T) {
	var got string
	handler := RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = RequestIDFromContext(r.Context())
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Request-ID", "bad\r\nid")
	handler.ServeHTTP(httptest.NewRecorder(), req)
	if got == "bad\r\nid" {
		t.Fatal("malformed id must be replaced")
	}
}

func TestSecurityHeaders(t *testing.T) {
	handler := SecurityHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("X-Content-Type-Options missing")
	}
	if rec.Header().Get("Content-Security-Policy") == "" {
		t.Fatal("CSP missing")
	}
}

func TestRealIPUsesForwardedHeaderWhenTrusted(t *testing.T) {
	var ip string
	handler := RealIP(true)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip = pkg.ClientIP(r)
	}))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.2:5000"
	req.Header.Set("X-Forwarded-For", "203.0.113.7, 10.0.0.1")
	handler.ServeHTTP(httptest.NewRecorder(), req)
	if ip != "203.0.113.7" {
		t.Fatalf("ClientIP = %q, want forwarded value", ip)
	}

	// 不信任代理时必须使用直连地址，避免伪造绕过限流。
	ip = ""
	handler = RealIP(false)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip = pkg.ClientIP(r)
	}))
	handler.ServeHTTP(httptest.NewRecorder(), req)
	if ip != "10.0.0.2" {
		t.Fatalf("ClientIP = %q, want remote addr", ip)
	}
}

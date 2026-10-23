package pkg

import (
	"net/http"
	"testing"
	"time"
)

func TestRateLimiterBlocksAfterLimit(t *testing.T) {
	limiter := NewRateLimiter(2, time.Minute)
	if !limiter.Allow("a") || !limiter.Allow("a") {
		t.Fatal("first two requests must pass")
	}
	if limiter.Allow("a") {
		t.Fatal("third request in the same window must be blocked")
	}
	if !limiter.Allow("b") {
		t.Fatal("different key must have its own quota")
	}
}

func TestRateLimiterDisabled(t *testing.T) {
	limiter := NewRateLimiter(0, time.Minute)
	for i := 0; i < 100; i++ {
		if !limiter.Allow("a") {
			t.Fatal("limit <= 0 must disable limiting")
		}
	}
}

func TestClientIPStripsPort(t *testing.T) {
	r, _ := http.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "192.0.2.10:54321"
	if got := ClientIP(r); got != "192.0.2.10" {
		t.Fatalf("ClientIP = %q", got)
	}
}

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestClientResolveTriggerAndWait(t *testing.T) {
	var mu sync.Mutex
	polls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Errorf("authorization = %q", got)
		}
		switch {
		case r.URL.Path == "/api/pipelines" && r.Method == http.MethodGet:
			writeEnvelope(t, w, []map[string]any{{"id": "p1", "name": "demo", "git_branch": "main"}})
		case r.URL.Path == "/api/pipelines/p1/build" && r.Method == http.MethodPost:
			if got := r.Header.Get("Idempotency-Key"); got != "" {
				t.Errorf("unexpected idempotency header: %q", got)
			}
			var body struct {
				Params map[string]string `json:"params"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode body: %v", err)
			}
			if body.Params["version"] != "1.2.3" {
				t.Errorf("params = %+v", body.Params)
			}
			writeEnvelope(t, w, map[string]any{"id": "7", "pipeline_id": "p1", "status": "pending"})
		case r.URL.Path == "/api/pipelines/p1/builds/7" && r.Method == http.MethodGet:
			mu.Lock()
			polls++
			n := polls
			mu.Unlock()
			status := "running"
			if n >= 2 {
				status = "success"
			}
			writeEnvelope(t, w, map[string]any{"id": "7", "pipeline_id": "p1", "status": status, "exit_code": 0})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c, err := newClient(srv.URL, "test-token", 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	p, err := c.resolvePipeline(ctx, "demo")
	if err != nil || p.ID != "p1" {
		t.Fatalf("resolve = %+v, %v", p, err)
	}
	b, err := c.trigger(ctx, p.ID, map[string]string{"version": "1.2.3"}, "")
	if err != nil || b.ID != "7" {
		t.Fatalf("trigger = %+v, %v", b, err)
	}
	final, err := c.waitBuild(ctx, p.ID, b.ID, time.Millisecond)
	if err != nil || final.Status != "success" {
		t.Fatalf("wait = %+v, %v", final, err)
	}
	if polls < 2 {
		t.Fatalf("expected at least 2 polls, got %d", polls)
	}
}

func TestClientLogsAndAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/pipelines" {
			writeEnvelope(t, w, []map[string]any{{"id": "p1", "name": "demo"}})
			return
		}
		if r.URL.Path == "/api/pipelines/p1/builds/3/log" {
			if r.URL.Query().Get("tail") != "20" || r.URL.Query().Get("grep") != "ERROR" {
				t.Errorf("query = %s", r.URL.RawQuery)
			}
			writeEnvelope(t, w, map[string]any{"log": "ERROR boom\n", "total_lines": 1})
			return
		}
		writeCode(w, 40300, "没有权限")
	}))
	defer srv.Close()

	c, err := newClient(srv.URL, "t", 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	out, err := c.logs(context.Background(), "p1", "3", 20, "ERROR")
	if err != nil || out.Log != "ERROR boom\n" || out.TotalLines != 1 {
		t.Fatalf("logs = %+v, %v", out, err)
	}
	_, err = c.getBuild(context.Background(), "p1", "404")
	var apiErr *apiError
	if !asAPIError(err, &apiErr) || apiErr.Code != 40300 {
		t.Fatalf("expected api error 40300, got %v", err)
	}
}

func TestConfigRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	t.Setenv("CI_CONFIG", path)
	if err := saveConfig(cliConfig{Server: "http://127.0.0.1:8080", Token: "cip_x"}); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadConfig()
	if err != nil || cfg.Server != "http://127.0.0.1:8080" || cfg.Token != "cip_x" {
		t.Fatalf("config = %+v, %v", cfg, err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "\n\n") {
		t.Fatalf("unexpected config content: %s", data)
	}
}

func asAPIError(err error, target **apiError) bool {
	e, ok := err.(*apiError)
	if ok {
		*target = e
	}
	return ok
}

func writeEnvelope(t *testing.T, w http.ResponseWriter, data any) {
	t.Helper()
	writeCodeData(w, 0, "ok", data)
}

func writeCode(w http.ResponseWriter, code int, msg string) {
	writeCodeData(w, code, msg, nil)
}

func writeCodeData(w http.ResponseWriter, code int, msg string, data any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"code": code, "msg": msg, "data": data})
}

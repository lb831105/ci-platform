package executor

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// 校验请求形态：路径转义、token 走 header（不进 URL/日志）、必填字段。
func TestPostGitlabCommitStatus(t *testing.T) {
	var gotPath, gotToken, gotState, gotTarget, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.EscapedPath()
		gotToken = r.Header.Get("PRIVATE-TOKEN")
		_ = r.ParseForm()
		gotState = r.Form.Get("state")
		gotTarget = r.Form.Get("target_url")
		gotBody = r.Form.Get("description")
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	err := PostGitlabCommitStatus(context.Background(), GitlabCommitStatus{
		BaseURL:     srv.URL + "/api/v4", // 用户常把 /api/v4 一起粘进来
		Token:       "glpat-secret",
		Project:     "ops/order-api",
		SHA:         "5f2c1a9abc",
		State:       "success",
		Name:        "ci-platform",
		TargetURL:   "https://ci.example.com/pipelines/p1/builds/12",
		Description: "#12 success (main)",
	})
	if err != nil {
		t.Fatalf("post status: %v", err)
	}
	if !strings.HasSuffix(gotPath, "/api/v4/projects/ops%2Forder-api/statuses/5f2c1a9abc") {
		t.Fatalf("unexpected path: %s", gotPath)
	}
	if gotToken != "glpat-secret" {
		t.Fatalf("token must be sent in PRIVATE-TOKEN header, got %q", gotToken)
	}
	if gotState != "success" || gotTarget == "" || !strings.Contains(gotBody, "#12") {
		t.Fatalf("form values: state=%q target=%q desc=%q", gotState, gotTarget, gotBody)
	}
}

func TestPostGitlabCommitStatusRejectsIncomplete(t *testing.T) {
	if err := PostGitlabCommitStatus(context.Background(), GitlabCommitStatus{Project: "a/b", SHA: "x"}); err == nil {
		t.Fatal("missing base url must be rejected")
	}
	if err := PostGitlabCommitStatus(context.Background(), GitlabCommitStatus{BaseURL: "http://x", SHA: "abc"}); err == nil {
		t.Fatal("missing project must be rejected")
	}
}

// 非 2xx 必须回错误（调用方据此写日志），且错误里带上状态码便于排查权限问题。
func TestPostGitlabCommitStatusHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()
	err := PostGitlabCommitStatus(context.Background(), GitlabCommitStatus{
		BaseURL: srv.URL, Token: "t", Project: "a/b", SHA: "abc", State: "failed",
	})
	if err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("expected 403 error, got %v", err)
	}
}

package executor

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"ci-platform/internal/model"
)

func testBuild() (*model.Pipeline, *model.Build) {
	now := time.Now().UTC()
	p := &model.Pipeline{ID: "p1", Name: "demo"}
	b := &model.Build{ID: "7", PipelineID: "p1", Status: model.BuildSuccess, TriggerType: model.TriggerManual, TriggeredBy: "admin"}
	b.StartedAt = &now
	b.FinishedAt = &now
	return p, b
}

// 钉钉渠道：markdown 包装 + 业务码透传。
func TestSendWebhookNotifyDingTalk(t *testing.T) {
	var received map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &received)
		_, _ = w.Write([]byte(`{"errcode":0,"errmsg":"ok"}`))
	}))
	defer srv.Close()
	p, b := testBuild()
	cfg := model.WebhookNotifySettings{Enabled: true, Channel: "dingtalk", URL: srv.URL}
	if err := SendWebhookNotify(context.Background(), cfg, p, b, ""); err != nil {
		t.Fatalf("dingtalk: %v", err)
	}
	if received["msgtype"] != "markdown" {
		t.Fatalf("msgtype = %v, want markdown", received["msgtype"])
	}
	md := received["markdown"].(map[string]interface{})
	text := md["text"].(string)
	if !containsAll(text, "demo", "#7", "success") {
		t.Fatalf("text missing fields: %s", text)
	}
}

// 企业微信渠道：markdown.content。
func TestSendWebhookNotifyWecom(t *testing.T) {
	var received map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &received)
		_, _ = w.Write([]byte(`{"errcode":0,"errmsg":"ok"}`))
	}))
	defer srv.Close()
	p, b := testBuild()
	cfg := model.WebhookNotifySettings{Enabled: true, Channel: "wecom", URL: srv.URL}
	if err := SendWebhookNotify(context.Background(), cfg, p, b, ""); err != nil {
		t.Fatalf("wecom: %v", err)
	}
	md := received["markdown"].(map[string]interface{})
	if !containsAll(md["content"].(string), "demo", "#7") {
		t.Fatalf("content missing fields: %v", md)
	}
}

// Slack 渠道：text 字段 + 详情链接。
func TestSendWebhookNotifySlack(t *testing.T) {
	var received map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &received)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()
	p, b := testBuild()
	cfg := model.WebhookNotifySettings{Enabled: true, Channel: "slack", URL: srv.URL}
	if err := SendWebhookNotify(context.Background(), cfg, p, b, "http://ci.example.com"); err != nil {
		t.Fatalf("slack: %v", err)
	}
	text := received["text"].(string)
	if !containsAll(text, "demo", "#7", "http://ci.example.com/pipelines/p1/builds/7") {
		t.Fatalf("text missing fields: %s", text)
	}
}

// HTTP 错误：非 2xx 返回错误。
func TestSendWebhookNotifyHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()
	p, b := testBuild()
	cfg := model.WebhookNotifySettings{Enabled: true, Channel: "slack", URL: srv.URL}
	if err := SendWebhookNotify(context.Background(), cfg, p, b, ""); err == nil {
		t.Fatal("403 should fail")
	}
}

// 未知渠道：拒绝。
func TestSendWebhookNotifyUnknownChannel(t *testing.T) {
	p, b := testBuild()
	cfg := model.WebhookNotifySettings{Enabled: true, Channel: "teams", URL: "http://example.invalid"}
	if err := SendWebhookNotify(context.Background(), cfg, p, b, ""); err == nil {
		t.Fatal("unknown channel should fail")
	}
}

// 业务错误（钉钉限流/关键词不匹配 errcode != 0）：返回错误。
func TestSendWebhookNotifyBizError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"errcode":310000,"errmsg":"keywords not found"}`))
	}))
	defer srv.Close()
	p, b := testBuild()
	cfg := model.WebhookNotifySettings{Enabled: true, Channel: "dingtalk", URL: srv.URL}
	if err := SendWebhookNotify(context.Background(), cfg, p, b, ""); err == nil {
		t.Fatal("errcode != 0 should fail")
	}
}

func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		if !bytes.Contains([]byte(s), []byte(sub)) {
			return false
		}
	}
	return true
}

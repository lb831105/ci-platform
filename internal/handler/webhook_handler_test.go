package handler

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ci-platform/internal/webhook"
)

// githubSignature 计算测试用的 GitHub HMAC 签名。
func githubSignature(secret, body string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(body))
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// verifySecretCases 直接测 verifySecret 的三分支（不依赖 store）。

// 平台自定义 header（curl / CI 脚本）。
func TestVerifySecretAcceptsWebhookSecretHeader(t *testing.T) {
	req := httptest.NewRequest("POST", "/api/webhook/123", nil)
	req.Header.Set("X-Webhook-Secret", "s3cret")
	h := &WebhookHandler{}
	if !h.verifySecret(httptest.NewRecorder(), req, "s3cret", bodyOf(req)) {
		t.Fatal("X-Webhook-Secret should be accepted")
	}
}

// GitLab Webhook 发送 X-Gitlab-Token（明文 Secret Token），必须与之兼容。
func TestVerifySecretAcceptsGitlabToken(t *testing.T) {
	req := httptest.NewRequest("POST", "/api/webhook/123", nil)
	req.Header.Set("X-Gitlab-Token", "s3cret")
	h := &WebhookHandler{}
	if !h.verifySecret(httptest.NewRecorder(), req, "s3cret", bodyOf(req)) {
		t.Fatal("X-Gitlab-Token should be accepted (GitLab webhooks)")
	}
}

// GitHub Webhook 发送 X-Hub-Signature-256: sha256=<hmac(body)>。
func TestVerifySecretAcceptsGithubSignature(t *testing.T) {
	body := `{"object_kind":"push","ref":"refs/heads/main"}`
	req := httptest.NewRequest("POST", "/api/webhook/123", strings.NewReader(body))
	req.Header.Set("X-Hub-Signature-256", githubSignature("s3cret", body))
	h := &WebhookHandler{}
	if !h.verifySecret(httptest.NewRecorder(), req, "s3cret", bodyOf(req)) {
		t.Fatal("X-Hub-Signature-256 should be accepted")
	}
}

// 错误签名 / 错误密钥 / 无 header 一律拒绝。
func TestVerifySecretRejectsWrongSecret(t *testing.T) {
	req := httptest.NewRequest("POST", "/api/webhook/123", nil)
	req.Header.Set("X-Webhook-Secret", "wrong")
	req.Header.Set("X-Gitlab-Token", "wrong")
	req.Header.Set("X-Hub-Signature-256", "sha256=deadbeef")
	h := &WebhookHandler{}
	if h.verifySecret(httptest.NewRecorder(), req, "s3cret", bodyOf(req)) {
		t.Fatal("wrong secret must be rejected")
	}
	// 无任何 header。
	req2 := httptest.NewRequest("POST", "/api/webhook/123", nil)
	if h.verifySecret(httptest.NewRecorder(), req2, "s3cret", bodyOf(req2)) {
		t.Fatal("missing headers must be rejected")
	}
	// GitHub 签名与 body 不匹配（body 被篡改）。
	body := `{"object_kind":"push","ref":"refs/heads/main"}`
	req3 := httptest.NewRequest("POST", "/api/webhook/123", strings.NewReader(body))
	req3.Header.Set("X-Hub-Signature-256", githubSignature("s3cret", `{"object_kind":"push","ref":"refs/heads/dev"}`))
	if h.verifySecret(httptest.NewRecorder(), req3, "s3cret", bodyOf(req3)) {
		t.Fatal("signature mismatch with body must be rejected")
	}
}

// bodyOf 读出测试请求体（verifySecret 现在接收已读取的 body，
// 真实 handler 里只读一次，避免签名校验与事件解析重复读流）。
func bodyOf(r *http.Request) []byte {
	if r.Body == nil {
		return nil
	}
	data, _ := io.ReadAll(r.Body)
	return data
}

func TestWebhookDeliveryKeyIsPerPipeline(t *testing.T) {
	ev := webhook.Event{Delivery: "same-uuid", Branch: "main", Commit: "abc"}
	first := webhookDeliveryKey("p1", ev)
	second := webhookDeliveryKey("p2", ev)
	if first == second {
		t.Fatalf("delivery key must include pipeline: %q", first)
	}
	if first != webhookDeliveryKey("p1", ev) {
		t.Fatal("same pipeline/delivery must produce stable key")
	}
}

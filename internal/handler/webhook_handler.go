package handler

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"io"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"ci-platform/internal/model"
	"ci-platform/internal/pkg"
	"ci-platform/internal/service"
)

// WebhookHandler 处理 POST /api/webhook/{pipeline_id}。
type WebhookHandler struct {
	pipelines *service.PipelineService
	builds    *service.BuildService
	trigger   *PipelineHandler
	limiter   *pkg.RateLimiter
}

// NewWebhookHandler 构造 Webhook 处理器。
func NewWebhookHandler(pipelines *service.PipelineService, builds *service.BuildService, trigger *PipelineHandler) *WebhookHandler {
	return &WebhookHandler{
		pipelines: pipelines,
		builds:    builds,
		trigger:   trigger,
		limiter:   pkg.NewRateLimiter(120, time.Minute),
	}
}

// Handle POST /api/webhook/{pipeline_id}
// 密钥校验兼容三种来源（任一匹配即通过，均为恒定时间比较，见 verifySecret）：
//   - X-Webhook-Secret: 平台自定义 header（curl / CI 脚本）
//   - X-Gitlab-Token:   GitLab Webhook 的 Secret Token
//   - X-Hub-Signature-256: GitHub Webhook 的 HMAC-SHA256 签名
//
// pipeline 未配置 secret 时视为开放触发。
func (h *WebhookHandler) Handle(w http.ResponseWriter, r *http.Request) {
	pipelineID := chi.URLParam(r, "pipeline_id")
	if !h.limiter.Allow(pkg.ClientIP(r) + "|" + pipelineID) {
		pkg.Fail(w, pkg.NewError(pkg.CodeTooManyRequests, "Webhook 请求过于频繁，请稍后再试"))
		return
	}

	p, err := h.pipelines.Get(r.Context(), pipelineID)
	if err != nil {
		pkg.Fail(w, err)
		return
	}

	if p.WebhookSecret != "" {
		if !h.verifySecret(w, r, p.WebhookSecret) {
			pkg.Fail(w, pkg.NewError(pkg.CodeBadRequest, "invalid webhook secret"))
			return
		}
	}

	h.trigger.trigger(w, r, pipelineID, model.TriggerWebhook, "webhook")
}

// verifySecret 校验 webhook 密钥，兼容 GitLab（明文 token）与 GitHub（HMAC 签名）：
// 任一匹配即通过。GitLab 不支持自定义 header，所以只认 X-Gitlab-Token 就无法兼容；
// 反之平台自定义 header 也必须保留。恒定时间比较防止计时侧信道。
func (h *WebhookHandler) verifySecret(w http.ResponseWriter, r *http.Request, secret string) bool {
	// 1. 平台自定义 header。
	if subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Webhook-Secret")), []byte(secret)) == 1 {
		return true
	}
	// 2. GitLab: Secret Token 明文比较。
	if subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Gitlab-Token")), []byte(secret)) == 1 {
		return true
	}
	// 3. GitHub: X-Hub-Signature-256: sha256=<hex hmac(secret, body)>；签名校验需要请求体。
	sig := r.Header.Get("X-Hub-Signature-256")
	if sig == "" {
		return false
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	want := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	return subtle.ConstantTimeCompare([]byte(sig), []byte(want)) == 1
}

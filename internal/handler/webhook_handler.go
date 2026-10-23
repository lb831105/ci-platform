package handler

import (
	"crypto/subtle"
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
// 校验 X-Webhook-Secret 后触发构建；pipeline 未配置 secret 时视为开放触发。
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
		got := r.Header.Get("X-Webhook-Secret")
		if subtle.ConstantTimeCompare([]byte(got), []byte(p.WebhookSecret)) != 1 {
			pkg.Fail(w, pkg.NewError(pkg.CodeBadRequest, "invalid webhook secret"))
			return
		}
	}

	h.trigger.trigger(w, r, pipelineID, model.TriggerWebhook, "webhook")
}

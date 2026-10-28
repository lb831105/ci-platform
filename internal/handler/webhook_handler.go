package handler

import (
	"ci-platform/internal/pkg/logger"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"ci-platform/internal/model"
	"ci-platform/internal/pkg"
	"ci-platform/internal/service"
	"ci-platform/internal/webhook"
)

// WebhookHandler 处理 POST /api/webhook/{pipeline_id}。
type WebhookHandler struct {
	pipelines *service.PipelineService
	builds    *service.BuildService
	trigger   *PipelineHandler
	limiter   *pkg.RateLimiter
	// dedupe 抵消重复投递（GitLab 的 Resend、代理重试）：同一次投递只构建一次。
	dedupe *pkg.Dedupe
	// audit 记录未经过 Auth 中间件的 Webhook 触发（含来源 IP 与结果）。
	audit *service.AuditService
}

// NewWebhookHandler 构造 Webhook 处理器。
func NewWebhookHandler(pipelines *service.PipelineService, builds *service.BuildService, trigger *PipelineHandler, audit ...*service.AuditService) *WebhookHandler {
	h := &WebhookHandler{
		pipelines: pipelines,
		builds:    builds,
		trigger:   trigger,
		limiter:   pkg.NewRateLimiter(120, time.Minute),
		dedupe:    pkg.NewDedupe(10*time.Minute, 4096),
	}
	if len(audit) > 0 {
		h.audit = audit[0]
	}
	return h
}

// webhookIgnored 是「策略忽略」的响应体：明确告诉调用方为什么没构建。
type webhookIgnored struct {
	Ignored bool   `json:"ignored"`
	Reason  string `json:"reason"`
	Event   string `json:"event,omitempty"`
	Branch  string `json:"branch,omitempty"`
}

// Handle POST /api/webhook/{pipeline_id}
//
// 与平台其它接口的区别：**这个接口返回真实 HTTP 状态码**。
// 代码平台（GitLab/GitHub）按 HTTP 状态判断投递成功与否，恒 200 会让
// 「密钥错」「队列满」「任务不存在」全部显示成功，运维无法在代码平台侧排查，
// 代码平台也不会重试或告警。响应体仍是统一的 {code,msg,data}。
func (h *WebhookHandler) Handle(w http.ResponseWriter, r *http.Request) {
	pipelineID := chi.URLParam(r, "pipeline_id")
	if !h.limiter.Allow(pkg.ClientIP(r) + "|" + pipelineID) {
		pkg.FailStatus(w, pkg.NewError(pkg.CodeTooManyRequests,
			"Webhook 请求过于频繁（每分钟上限 120 次），请稍后再试"))
		return
	}

	p, err := h.pipelines.Get(r.Context(), pipelineID)
	if err != nil {
		pkg.FailStatus(w, err)
		return
	}

	// 先读请求体（限 1MB）：GitLab/GitHub 的签名校验和事件解析都需要它。
	// 读失败时保持旧行为（当作无 body），不能让一次畸形请求变成 500。
	body, _ := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))

	// 空密钥 = 禁止远程触发，而不是「跳过校验」。新建任务默认没有密钥，
	// 绝不能因此变成任何人知道任务 ID 就能触发构建/发布。
	if strings.TrimSpace(p.WebhookSecret) == "" {
		h.recordAudit(r, pipelineID, pkg.CodeForbidden, "secret-not-configured")
		pkg.FailStatus(w, pkg.NewError(pkg.CodeForbidden,
			"该任务未配置 Webhook 密钥，远程触发已禁用；请在任务配置中设置密钥"))
		return
	}
	if !h.verifySecret(w, r, p.WebhookSecret, body) {
		h.recordAudit(r, pipelineID, pkg.CodeUnauthorized, "invalid-secret")
		pkg.FailStatus(w, pkg.NewError(pkg.CodeUnauthorized, "invalid webhook secret"))
		return
	}

	ev := webhook.Parse(r.Header, body)
	logger.Debugf("[webhook] pipeline=%s parsed event=%s branch=%s commit=%s project=%s skip_ci=%t",
		pipelineID, ev.Kind, ev.BranchRef(), ev.Commit, ev.Project, ev.SkipCI)
	decision := service.DecideWebhook(p, ev)
	if !decision.Trigger {
		h.recordAudit(r, pipelineID, pkg.CodeOK, "ignored:"+decision.Reason)
		// 正常忽略 → 2xx，避免代码平台把它记成投递失败。
		logger.Debugf("[webhook] pipeline=%s ignored: %s (event=%s branch=%s provider=%s)",
			pipelineID, decision.Reason, ev.Kind, ev.Branch, ev.Provider)
		pkg.OK(w, webhookIgnored{Ignored: true, Reason: decision.Reason, Event: ev.Kind, Branch: ev.Branch})
		return
	}

	// 幂等：同一次投递（UUID）在窗口内只构建一次。
	// 没有投递标识时用「任务+分支+提交」组合兜底，仍能挡住明显的重复投递。
	deliveryKey := webhookDeliveryKey(pipelineID, ev)
	if !h.dedupe.First(deliveryKey) {
		h.recordAudit(r, pipelineID, pkg.CodeOK, "duplicate:"+deliveryKey)
		pkg.OK(w, webhookIgnored{Ignored: true, Reason: "重复投递（同一事件已处理）", Event: ev.Kind, Branch: ev.Branch})
		return
	}

	b, err := h.builds.Trigger(r.Context(), pipelineID, model.TriggerWebhook, "webhook",
		service.TriggerOptions{
			Params:        webhookParams(p, r),
			Branch:        decision.Branch,
			CommitProject: ev.Project,
		})
	if err != nil {
		// 去重键必须在失败时释放：否则队列满/任务禁用等可重试错误发生后，
		// 代码平台按 5xx/429 重试会被当成「重复投递」永久忽略。
		h.dedupe.Forget(deliveryKey)
		logger.Errorf("[webhook] pipeline=%s trigger failed: %v", pipelineID, err)
		pkg.FailStatus(w, err)
		return
	}
	logger.Infof("[webhook] pipeline=%s build=%s branch=%s commit=%s event=%s provider=%s",
		pipelineID, b.ID, orDash(b.Branch), orDash(ev.Commit), ev.Kind, ev.Provider)
	h.recordAudit(r, pipelineID, pkg.CodeOK, "build:"+b.ID)
	pkg.OK(w, b)
}

// webhookDeliveryKey 生成去重键。
// GitLab/GitHub 的投递 UUID 是项目级的：同一事件会分别投递到多个任务 URL，
// 因此必须带 pipelineID，否则第二个任务会被误判为重复投递。
func webhookDeliveryKey(pipelineID string, ev webhook.Event) string {
	if ev.Delivery != "" {
		return "uuid:" + pipelineID + ":" + ev.Delivery
	}
	if ev.Commit != "" {
		return "sha:" + pipelineID + ":" + ev.Kind + ":" + ev.Branch + ":" + ev.Commit
	}
	return ""
}

// recordAudit 记录一次 Webhook 请求；Webhook 不走 Auth/审计中间件，
// 但生产发布来源必须可追溯（谁在什么 IP 触发了哪个任务）。
func (h *WebhookHandler) recordAudit(r *http.Request, pipelineID string, status int, detail string) {
	if h.audit == nil {
		return
	}
	target := "POST /api/webhook/" + pipelineID
	if detail != "" {
		target += " (" + detail + ")"
	}
	h.audit.Record("webhook", "webhook", target, status, pkg.ClientIP(r))
}

// verifySecret 校验 webhook 密钥，兼容三类来源（任一匹配即通过）：
//   - X-Webhook-Secret: 平台自定义 header（curl / 脚本）
//   - X-Gitlab-Token:   GitLab Webhook 的 Secret Token（明文）
//   - X-Hub-Signature-256: GitHub 的 HMAC-SHA256 签名
//
// 全部使用恒定时间比较，避免通过响应时间猜测密钥。
func (h *WebhookHandler) verifySecret(w http.ResponseWriter, r *http.Request, secret string, body []byte) bool {
	if subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Webhook-Secret")), []byte(secret)) == 1 {
		return true
	}
	if subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Gitlab-Token")), []byte(secret)) == 1 {
		return true
	}
	sig := r.Header.Get("X-Hub-Signature-256")
	if sig == "" {
		return false
	}
	if len(body) == 0 {
		// 调用方只给了 header 却没有 body 时无法验签；保持历史行为（拒绝）。
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	want := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	return subtle.ConstantTimeCompare([]byte(sig), []byte(want)) == 1
}

// webhookParams 从查询串里挑出任务已定义的参数，作为本次构建的参数值。
func webhookParams(p *model.Pipeline, r *http.Request) map[string]string {
	if len(p.Params) == 0 {
		return nil
	}
	query := r.URL.Query()
	params := make(map[string]string, len(p.Params))
	for _, def := range p.Params {
		if values, ok := query[def.Name]; ok && len(values) > 0 {
			params[def.Name] = values[0]
		}
	}
	if len(params) == 0 {
		return nil
	}
	return params
}

// orDash 把空字符串显示为 -，便于日志阅读。
func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}

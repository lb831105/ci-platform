package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"ci-platform/internal/model"
	"ci-platform/internal/pkg"
	"ci-platform/internal/service"
)

// PipelineHandler 只做参数解析与响应封装，业务全部委托给 service。
type PipelineHandler struct {
	pipelines *service.PipelineService
	builds    *service.BuildService
}

// NewPipelineHandler 构造 Pipeline 接口处理器。
func NewPipelineHandler(pipelines *service.PipelineService, builds *service.BuildService) *PipelineHandler {
	return &PipelineHandler{pipelines: pipelines, builds: builds}
}

// sanitizePipeline 清除对外响应中的敏感字段（SSH 发布密码）。
// 密码只用于构建时连接目标机，任何读取接口（列表/详情/概览）都不得回显。
func sanitizePipeline(p *model.Pipeline) *model.Pipeline {
	if p == nil {
		return p
	}
	// Webhook 密钥只用于校验来源，绝不能通过列表/详情/创建/更新响应回显。
	// HasWebhookSecret 让前端仍能显示「已配置」，但值本身永远是空。
	p.HasWebhookSecret = strings.TrimSpace(p.WebhookSecret) != ""
	p.WebhookSecret = ""
	if p.SshPublish != nil {
		p.SshPublish.Password = ""
	}
	if len(p.SshPublishes) > 0 {
		// 多目标：旧版内联配置可能带密码，逐个清空（不改动共享切片指向的原数据）。
		list := make([]model.SshPublishConfig, len(p.SshPublishes))
		copy(list, p.SshPublishes)
		for i := range list {
			list[i].Password = ""
		}
		p.SshPublishes = list
	}
	// 任务级通知的群机器人 URL 含 access_token（Slack 还在 path 尾段）：
	// 返回时按渠道脱敏（保留结构，值置 ***）。
	// 升级群与分支路由的群同样含密钥，必须一并脱敏，否则等于把 token 直接吐给前端。
	// 这里整体拷贝一层，避免脱敏结果反写进共享的 Pipeline 对象（通知发送仍需真实 URL）。
	if p.Notify != nil {
		n := *p.Notify
		n.Targets = maskNotifyTargets(p.Notify.Targets)
		if p.Notify.Escalation != nil {
			esc := *p.Notify.Escalation
			esc.Targets = maskNotifyTargets(p.Notify.Escalation.Targets)
			n.Escalation = &esc
		}
		if len(p.Notify.Routes) > 0 {
			routes := make([]model.NotifyRoute, len(p.Notify.Routes))
			copy(routes, p.Notify.Routes)
			for i := range routes {
				routes[i].Targets = maskNotifyTargets(routes[i].Targets)
			}
			n.Routes = routes
		}
		p.Notify = &n
	}
	return p
}

// maskNotifyTargets 返回脱敏副本（不改动传入切片元素指向的原数据）。
func maskNotifyTargets(in []model.NotifyTarget) []model.NotifyTarget {
	out := make([]model.NotifyTarget, len(in))
	copy(out, in)
	for i := range out {
		out[i].URL = pkg.MaskWebhookURL(out[i].Channel, out[i].URL)
	}
	return out
}

// List GET /api/pipelines
func (h *PipelineHandler) List(w http.ResponseWriter, r *http.Request) {
	items, err := h.pipelines.ListFor(r.Context(), pkg.UserFromContext(r.Context()))
	if err != nil {
		pkg.Fail(w, err)
		return
	}
	for _, p := range items {
		sanitizePipeline(p)
	}
	pkg.OK(w, items)
}

// Overview GET /api/overview
//
// 返回带构建聚合信息的任务列表，供 Jenkins 风格作业列表一次性渲染。
func (h *PipelineHandler) Overview(w http.ResponseWriter, r *http.Request) {
	pipelines, err := h.pipelines.ListFor(r.Context(), pkg.UserFromContext(r.Context()))
	if err != nil {
		pkg.Fail(w, err)
		return
	}
	summaries, err := h.builds.Summaries(r.Context(), pipelines)
	if err != nil {
		pkg.Fail(w, err)
		return
	}
	for i := range summaries {
		sanitizePipeline(&summaries[i].Pipeline)
	}
	pkg.OK(w, summaries)
}

// Dashboard GET /api/dashboard
//
// 返回首页仪表盘聚合数据；只统计当前用户可见的任务，避免泄露无权任务。
func (h *PipelineHandler) Dashboard(w http.ResponseWriter, r *http.Request) {
	pipelines, err := h.pipelines.ListFor(r.Context(), pkg.UserFromContext(r.Context()))
	if err != nil {
		pkg.Fail(w, err)
		return
	}
	data, err := h.builds.Dashboard(r.Context(), pipelines)
	if err != nil {
		pkg.Fail(w, err)
		return
	}
	pkg.OK(w, data)
}

// Metrics GET /metrics
//
// Prometheus 抓取入口：只暴露聚合指标，不包含任务名/日志等业务数据。
// 保持公开是因为 Prometheus 通常无法带登录会话；如需要可在外层网关限制来源。
func (h *PipelineHandler) Metrics(w http.ResponseWriter, r *http.Request) {
	pipelines, err := h.pipelines.List(r.Context())
	if err != nil {
		http.Error(w, "metrics unavailable", http.StatusServiceUnavailable)
		return
	}
	data, err := h.builds.Dashboard(r.Context(), pipelines)
	if err != nil {
		http.Error(w, "metrics unavailable", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	fmt.Fprintln(w, "# HELP ci_platform_jobs_total Number of pipelines.")
	fmt.Fprintln(w, "# TYPE ci_platform_jobs_total gauge")
	fmt.Fprintln(w, "ci_platform_jobs_total", data.JobsTotal)
	fmt.Fprintln(w, "# HELP ci_platform_jobs_disabled Number of disabled pipelines.")
	fmt.Fprintln(w, "# TYPE ci_platform_jobs_disabled gauge")
	fmt.Fprintln(w, "ci_platform_jobs_disabled", data.JobsDisabled)
	fmt.Fprintln(w, "# HELP ci_platform_builds_today Number of builds created today.")
	fmt.Fprintln(w, "# TYPE ci_platform_builds_today gauge")
	fmt.Fprintln(w, "ci_platform_builds_today", data.BuildsToday)
	fmt.Fprintln(w, "# HELP ci_platform_builds_today_by_status Builds created today by status.")
	fmt.Fprintln(w, "# TYPE ci_platform_builds_today_by_status gauge")
	fmt.Fprintln(w, `ci_platform_builds_today_by_status{status="success"}`, data.SuccessToday)
	fmt.Fprintln(w, `ci_platform_builds_today_by_status{status="failed"}`, data.FailedToday)
	fmt.Fprintln(w, `ci_platform_builds_today_by_status{status="aborted"}`, data.AbortedToday)
	fmt.Fprintln(w, "# HELP ci_platform_builds_running Running builds.")
	fmt.Fprintln(w, "# TYPE ci_platform_builds_running gauge")
	fmt.Fprintln(w, "ci_platform_builds_running", data.RunningBuilds)
	fmt.Fprintln(w, "# HELP ci_platform_builds_pending Pending builds.")
	fmt.Fprintln(w, "# TYPE ci_platform_builds_pending gauge")
	fmt.Fprintln(w, "ci_platform_builds_pending", data.PendingBuilds)
	fmt.Fprintln(w, "# HELP ci_platform_build_success_rate_today Successful / finished builds today.")
	fmt.Fprintln(w, "# TYPE ci_platform_build_success_rate_today gauge")
	fmt.Fprintln(w, "ci_platform_build_success_rate_today", fmt.Sprintf("%.4f", data.SuccessRate))
	fmt.Fprintln(w, "# HELP ci_platform_build_duration_avg_seconds Average finished build duration today.")
	fmt.Fprintln(w, "# TYPE ci_platform_build_duration_avg_seconds gauge")
	fmt.Fprintln(w, "ci_platform_build_duration_avg_seconds", fmt.Sprintf("%.3f", float64(data.AvgDurationMS)/1000))
	fmt.Fprintln(w, "# HELP ci_platform_queue_length Queued builds.")
	fmt.Fprintln(w, "# TYPE ci_platform_queue_length gauge")
	fmt.Fprintln(w, "ci_platform_queue_length", data.QueueLength)
	fmt.Fprintln(w, "# HELP ci_platform_queue_capacity Build queue capacity.")
	fmt.Fprintln(w, "# TYPE ci_platform_queue_capacity gauge")
	fmt.Fprintln(w, "ci_platform_queue_capacity", data.QueueCapacity)
	fmt.Fprintln(w, "# HELP ci_platform_workers Configured build workers.")
	fmt.Fprintln(w, "# TYPE ci_platform_workers gauge")
	fmt.Fprintln(w, "ci_platform_workers", data.Workers)
	fmt.Fprintln(w, "# HELP ci_platform_quiet_mode Whether the instance is in quiet mode.")
	fmt.Fprintln(w, "# TYPE ci_platform_quiet_mode gauge")
	fmt.Fprintln(w, "ci_platform_quiet_mode", boolToInt(data.Quiet))
	fmt.Fprintln(w, "# HELP ci_platform_deployments_today SSH deployments today.")
	fmt.Fprintln(w, "# TYPE ci_platform_deployments_today gauge")
	fmt.Fprintln(w, "ci_platform_deployments_today", data.DeploymentsToday)
	fmt.Fprintln(w, "# HELP ci_platform_deployment_failures_today Failed SSH deployments today.")
	fmt.Fprintln(w, "# TYPE ci_platform_deployment_failures_today gauge")
	fmt.Fprintln(w, "ci_platform_deployment_failures_today", data.DeploymentFailuresToday)
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

// AllBuilds GET /api/builds
//
// 返回跨任务的构建历史，按开始时间倒序，供全局「构建历史」页使用。
// 支持搜索/状态/任务过滤与分页；总数通过 X-Total-Count 返回，保持数组信封不变。
func (h *PipelineHandler) AllBuilds(w http.ResponseWriter, r *http.Request) {
	pipelines, err := h.pipelines.ListFor(r.Context(), pkg.UserFromContext(r.Context()))
	if err != nil {
		pkg.Fail(w, err)
		return
	}
	list, err := h.builds.AllBuilds(r.Context(), pipelines)
	if err != nil {
		pkg.Fail(w, err)
		return
	}

	query := r.URL.Query()
	search := strings.ToLower(strings.TrimSpace(query.Get("q")))
	statuses := make(map[string]struct{})
	for _, raw := range strings.Split(query.Get("status"), ",") {
		if status := strings.TrimSpace(raw); status != "" {
			statuses[status] = struct{}{}
		}
	}
	pipeline := strings.TrimSpace(query.Get("pipeline"))
	filtered := make([]model.BuildListItem, 0, len(list))
	for _, item := range list {
		if len(statuses) > 0 {
			if _, ok := statuses[string(item.Status)]; !ok {
				continue
			}
		}
		if pipeline != "" && !strings.EqualFold(item.PipelineID, pipeline) && !strings.EqualFold(item.PipelineName, pipeline) {
			continue
		}
		if search != "" {
			haystack := strings.ToLower(strings.Join([]string{
				item.ID, item.PipelineID, item.PipelineName, item.TriggeredBy, item.Branch, item.Commit,
			}, "\n"))
			if !strings.Contains(haystack, search) {
				continue
			}
		}
		filtered = append(filtered, item)
	}

	// 只在调用方确实传了分页参数时切片：老调用方 GET /api/builds 仍返回全部。
	_, hasLimit := query["limit"]
	_, hasPageSize := query["page_size"]
	_, hasOffset := query["offset"]
	_, hasPage := query["page"]
	if hasLimit || hasPageSize || hasOffset || hasPage {
		limit := 20
		if raw := firstNonEmptyQuery(query.Get("limit"), query.Get("page_size")); raw != "" {
			if n, parseErr := strconv.Atoi(raw); parseErr == nil && n > 0 {
				limit = n
			}
		}
		if limit > 200 {
			limit = 200
		}
		offset := 0
		if raw := query.Get("offset"); raw != "" {
			if n, parseErr := strconv.Atoi(raw); parseErr == nil && n > 0 {
				offset = n
			}
		}
		if offset == 0 && query.Get("page") != "" {
			if page, parseErr := strconv.Atoi(query.Get("page")); parseErr == nil && page > 1 {
				offset = (page - 1) * limit
			}
		}
		total := len(filtered)
		if offset >= total {
			filtered = []model.BuildListItem{}
		} else {
			end := offset + limit
			if end > total {
				end = total
			}
			filtered = filtered[offset:end]
		}
		w.Header().Set("X-Total-Count", strconv.Itoa(total))
	}
	pkg.OK(w, filtered)
}

func firstNonEmptyQuery(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

// Create POST /api/pipelines
func (h *PipelineHandler) Create(w http.ResponseWriter, r *http.Request) {
	var in model.Pipeline
	if err := decodeJSON(w, r, &in); err != nil {
		pkg.Fail(w, err)
		return
	}
	created, err := h.pipelines.Create(r.Context(), &in)
	if err != nil {
		pkg.Fail(w, err)
		return
	}
	pkg.OK(w, sanitizePipeline(created))
}

// Get GET /api/pipelines/{id}
func (h *PipelineHandler) Get(w http.ResponseWriter, r *http.Request) {
	p, err := h.pipelines.Get(r.Context(), chi.URLParam(r, "pid"))
	if err != nil {
		pkg.Fail(w, err)
		return
	}
	pkg.OK(w, sanitizePipeline(p))
}

// Update PUT /api/pipelines/{id}
func (h *PipelineHandler) Update(w http.ResponseWriter, r *http.Request) {
	var in model.Pipeline
	if err := decodeJSON(w, r, &in); err != nil {
		pkg.Fail(w, err)
		return
	}
	updated, err := h.pipelines.Update(r.Context(), chi.URLParam(r, "pid"), &in)
	if err != nil {
		pkg.Fail(w, err)
		return
	}
	pkg.OK(w, sanitizePipeline(updated))
}

// Delete DELETE /api/pipelines/{id}
func (h *PipelineHandler) Delete(w http.ResponseWriter, r *http.Request) {
	pipelineID := chi.URLParam(r, "pid")
	// 有未结束构建时拒绝删除：先删工作区会让 worker 写日志/归档失败，
	// 构建状态可能永久停在 running。
	builds, err := h.builds.ListByPipeline(r.Context(), pipelineID)
	if err != nil {
		pkg.Fail(w, err)
		return
	}
	for _, b := range builds {
		if b.Status == model.BuildRunning || b.Status == model.BuildPending || b.Status == model.BuildPendingApproval {
			pkg.Fail(w, pkg.ErrConflict("任务还有未结束的构建 #%s（%s），请先停止或等待结束后再删除", b.ID, b.Status))
			return
		}
	}
	if err := h.pipelines.Delete(r.Context(), pipelineID); err != nil {
		pkg.Fail(w, err)
		return
	}
	pkg.OK(w, nil)
}

// TriggerBuild POST /api/pipelines/{id}/build
func (h *PipelineHandler) TriggerBuild(w http.ResponseWriter, r *http.Request) {
	// body 可选（老前端/CI 脚本可能不带 body）；带参数时才解析。
	var in triggerRequest
	if r.ContentLength != 0 {
		if err := decodeJSON(w, r, &in); err != nil {
			pkg.Fail(w, err)
			return
		}
	}
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" {
		key = in.IdempotencyKey
	}
	h.trigger(w, r, chi.URLParam(r, "pid"), model.TriggerManual, pkg.UserFromContext(r.Context()), in.Params, key)
}

// triggerRequest 是触发构建的请求体：params 为参数化构建的用户填值。
// idempotency_key 与请求头二选一，方便不能用自定义头的客户端。
type triggerRequest struct {
	Params         map[string]string `json:"params"`
	IdempotencyKey string            `json:"idempotency_key"`
}

// Rename POST /api/pipelines/{id}/rename
func (h *PipelineHandler) Rename(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name string `json:"name"`
	}
	if err := decodeJSON(w, r, &in); err != nil {
		pkg.Fail(w, err)
		return
	}
	updated, err := h.pipelines.Rename(r.Context(), chi.URLParam(r, "pid"), in.Name)
	if err != nil {
		pkg.Fail(w, err)
		return
	}
	pkg.OK(w, sanitizePipeline(updated))
}

// trigger 是手动与 Webhook 共用的触发入口。
func (h *PipelineHandler) trigger(w http.ResponseWriter, r *http.Request, pipelineID string, t model.TriggerType, triggeredBy string, params map[string]string, idempotencyKey string) {
	b, err := h.builds.Trigger(r.Context(), pipelineID, t, triggeredBy, service.TriggerOptions{
		Params:         params,
		IdempotencyKey: idempotencyKey,
	})
	if err != nil {
		pkg.Fail(w, err)
		return
	}
	pkg.OK(w, b)
}

// decodeJSON 解析请求体（限制 1MB），禁止未知字段以便尽早暴露前后端契约不一致。
func decodeJSON(w http.ResponseWriter, r *http.Request, v interface{}) error {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return pkg.ErrBadRequest("invalid request body: %v", err)
	}
	return nil
}

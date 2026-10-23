package handler

import (
	"encoding/json"
	"net/http"

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
	if p != nil && p.SshPublish != nil {
		p.SshPublish.Password = ""
	}
	return p
}

// List GET /api/pipelines
func (h *PipelineHandler) List(w http.ResponseWriter, r *http.Request) {
	items, err := h.pipelines.List(r.Context())
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
	pipelines, err := h.pipelines.List(r.Context())
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

// AllBuilds GET /api/builds
//
// 返回跨任务的构建历史，按开始时间倒序，供全局「构建历史」页使用。
func (h *PipelineHandler) AllBuilds(w http.ResponseWriter, r *http.Request) {
	pipelines, err := h.pipelines.List(r.Context())
	if err != nil {
		pkg.Fail(w, err)
		return
	}
	list, err := h.builds.AllBuilds(r.Context(), pipelines)
	if err != nil {
		pkg.Fail(w, err)
		return
	}
	pkg.OK(w, list)
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
	if err := h.pipelines.Delete(r.Context(), chi.URLParam(r, "pid")); err != nil {
		pkg.Fail(w, err)
		return
	}
	pkg.OK(w, nil)
}

// TriggerBuild POST /api/pipelines/{id}/build
func (h *PipelineHandler) TriggerBuild(w http.ResponseWriter, r *http.Request) {
	h.trigger(w, r, chi.URLParam(r, "pid"), model.TriggerManual, pkg.UserFromContext(r.Context()))
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
func (h *PipelineHandler) trigger(w http.ResponseWriter, r *http.Request, pipelineID string, t model.TriggerType, triggeredBy string) {
	b, err := h.builds.Trigger(r.Context(), pipelineID, t, triggeredBy)
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

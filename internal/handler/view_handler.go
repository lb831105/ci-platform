package handler

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"ci-platform/internal/model"
	"ci-platform/internal/pkg"
	"ci-platform/internal/service"
)

// ViewHandler 视图接口：新建/编辑/删除视图与视图内任务列表。
// 视图本质是给任务归类（列表视图 + 我的视图），对齐 Jenkins Views。
type ViewHandler struct {
	views     *service.ViewService
	pipelines *service.PipelineService
	builds    *service.BuildService
}

// NewViewHandler 构造视图 handler；pipelines 用于全量任务枚举，builds 用于构建聚合。
func NewViewHandler(views *service.ViewService, pipelines *service.PipelineService, builds *service.BuildService) *ViewHandler {
	return &ViewHandler{views: views, pipelines: pipelines, builds: builds}
}

// List GET /api/views：全部视图。
// 视图的 Jobs 字段可能包含用户无 job.read 权限的任务名，必须按当前用户过滤；
// 否则拥有 view.read 但没有任务权限的人可借视图列表枚举任务。
func (h *ViewHandler) List(w http.ResponseWriter, r *http.Request) {
	user := pkg.UserFromContext(r.Context())
	views := h.views.List()
	for i := range views {
		names, err := h.views.JobNames(r.Context(), &views[i], user)
		if err != nil {
			pkg.Fail(w, err)
			return
		}
		views[i].Jobs = names
	}
	pkg.OK(w, views)
}

// Jobs GET /api/views/{name}/jobs：视图内任务列表（含构建聚合）。
// 任务按视图类型与当前用户权限解析（我的视图自动按权限过滤）。
func (h *ViewHandler) Jobs(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	view, err := h.views.Get(name)
	if err != nil {
		pkg.Fail(w, err)
		return
	}
	user := pkg.UserFromContext(r.Context())
	names, err := h.views.JobNames(r.Context(), &view, user)
	if err != nil {
		pkg.Fail(w, err)
		return
	}
	selected := make(map[string]bool, len(names))
	for _, n := range names {
		selected[n] = true
	}
	// 视图里的任务同样按任务角色过滤：无权限的任务不应借视图列表暴露。
	pipelines, err := h.pipelines.ListFor(r.Context(), user)
	if err != nil {
		pkg.Fail(w, err)
		return
	}
	inView := make([]*model.Pipeline, 0, len(names))
	for _, p := range pipelines {
		if selected[p.Name] {
			inView = append(inView, p)
		}
	}
	summaries, err := h.builds.Summaries(r.Context(), inView)
	if err != nil {
		pkg.Fail(w, err)
		return
	}
	pkg.OK(w, summaries)
}

// Create POST /api/views：新建视图。
func (h *ViewHandler) Create(w http.ResponseWriter, r *http.Request) {
	var in model.View
	if err := decodeJSON(w, r, &in); err != nil {
		pkg.Fail(w, err)
		return
	}
	out, err := h.views.Create(r.Context(), &in)
	if err != nil {
		pkg.Fail(w, err)
		return
	}
	pkg.OK(w, out)
}

// Update PUT /api/views/{name}：编辑视图（名称不可改）。
func (h *ViewHandler) Update(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	var in model.View
	if err := decodeJSON(w, r, &in); err != nil {
		pkg.Fail(w, err)
		return
	}
	out, err := h.views.Update(r.Context(), name, &in)
	if err != nil {
		pkg.Fail(w, err)
		return
	}
	pkg.OK(w, out)
}

// Delete DELETE /api/views/{name}：删除视图（不影响任务本身）。
func (h *ViewHandler) Delete(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	if err := h.views.Delete(name); err != nil {
		pkg.Fail(w, err)
		return
	}
	pkg.OK(w, nil)
}

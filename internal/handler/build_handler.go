package handler

import (
	"ci-platform/internal/pkg/logger"
	"fmt"
	"mime"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"

	"ci-platform/internal/model"
	"ci-platform/internal/pkg"
	"ci-platform/internal/service"
)

// BuildHandler 处理构建历史、详情与控制台日志读取。
type BuildHandler struct {
	builds    *service.BuildService
	artifacts *service.ArtifactService
	rbac      *service.RBACService
}

// NewBuildHandler 构造构建接口处理器。
func NewBuildHandler(builds *service.BuildService, artifacts *service.ArtifactService) *BuildHandler {
	return &BuildHandler{builds: builds, artifacts: artifacts}
}

// SetRBAC 注入权限服务，用于跨任务产物晋级时校验源任务读权限
// （目标任务的 job.build 已由路由中间件校验，源任务仍需单独确认）。
func (h *BuildHandler) SetRBAC(rbac *service.RBACService) {
	h.rbac = rbac
}

// List GET /api/pipelines/{pid}/builds 遍历构建目录返回历史（编号倒序）。
func (h *BuildHandler) List(w http.ResponseWriter, r *http.Request) {
	items, err := h.builds.ListByPipeline(r.Context(), chi.URLParam(r, "pid"))
	if err != nil {
		pkg.Fail(w, err)
		return
	}
	for _, item := range items {
		sanitizeBuild(item)
	}
	pkg.OK(w, items)
}

// sanitizeBuild 清除 API 响应中的密码参数密文。
// Params 中对应值已是 ***；SecretParams 只供执行器解密，不能出服务端。
func sanitizeBuild(b *model.Build) *model.Build {
	if b != nil {
		b.SecretParams = nil
	}
	return b
}

// Get GET /api/pipelines/{pid}/builds/{bid}
func (h *BuildHandler) Get(w http.ResponseWriter, r *http.Request) {
	b, err := h.builds.Get(r.Context(), chi.URLParam(r, "pid"), chi.URLParam(r, "bid"))
	if err != nil {
		pkg.Fail(w, err)
		return
	}
	pkg.OK(w, sanitizeBuild(b))
}

// Rerun POST /api/pipelines/{pid}/builds/{bid}/rerun
// 用历史构建的参数重新触发；密码参数由服务端从加密快照还原，不经过前端。
func (h *BuildHandler) Rerun(w http.ResponseWriter, r *http.Request) {
	b, err := h.builds.Rerun(r.Context(),
		chi.URLParam(r, "pid"), chi.URLParam(r, "bid"), pkg.UserFromContext(r.Context()))
	if err != nil {
		pkg.Fail(w, err)
		return
	}
	pkg.OK(w, sanitizeBuild(b))
}

// updateBuildRequest 是 PUT /api/pipelines/{pid}/builds/{bid} 的请求体。
// 指针字段用于区分“没传”和“显式清空/关闭”：只改描述时不能把 KeepForever 重置。
type updateBuildRequest struct {
	Description *string `json:"description"`
	KeepForever *bool   `json:"keep_forever"`
}

// Update PUT /api/pipelines/{pid}/builds/{bid}
// 更新构建备注与「永久保留」标记（对齐 Jenkins Build 页的 Add description / Keep forever）。
func (h *BuildHandler) Update(w http.ResponseWriter, r *http.Request) {
	var in updateBuildRequest
	if err := decodeJSON(w, r, &in); err != nil {
		pkg.Fail(w, err)
		return
	}
	b, err := h.builds.UpdateMetadata(r.Context(),
		chi.URLParam(r, "pid"), chi.URLParam(r, "bid"), in.Description, in.KeepForever)
	if err != nil {
		pkg.Fail(w, err)
		return
	}
	pkg.OK(w, sanitizeBuild(b))
}

// Log GET /api/pipelines/{pid}/builds/{bid}/log?tail=&grep=
// 默认返回 log.txt 全文；带 tail 时只返回最近 N 行，带 grep 时按子串过滤。
func (h *BuildHandler) Log(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	tail := model.ParsePositiveInt(query.Get("tail"))
	if tail > 5000 {
		tail = 5000
	}
	grep := strings.TrimSpace(query.Get("grep"))
	if runes := []rune(grep); len(runes) > 200 {
		grep = string(runes[:200])
	}
	text, total, matched, truncated, err := h.builds.QueryLog(r.Context(),
		chi.URLParam(r, "pid"), chi.URLParam(r, "bid"), tail, grep)
	if err != nil {
		pkg.Fail(w, err)
		return
	}
	pkg.OK(w, map[string]any{
		"log": text, "total_lines": total, "matched_lines": matched, "truncated": truncated,
	})
}

// DownloadLog GET /api/pipelines/{pid}/builds/{bid}/log/download
// 以纯文本附件下载控制台日志，便于离线排查与贴给同事。
func (h *BuildHandler) DownloadLog(w http.ResponseWriter, r *http.Request) {
	text, err := h.builds.ReadLog(r.Context(), chi.URLParam(r, "pid"), chi.URLParam(r, "bid"))
	if err != nil {
		pkg.Fail(w, err)
		return
	}
	buildID := chi.URLParam(r, "bid")
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", attachmentName(fmt.Sprintf("build-%s-log.txt", buildID)))
	_, _ = fmt.Fprint(w, text)
}

// Delete DELETE /api/pipelines/{pid}/builds/{bid}
// 删除构建及其目录（包含日志与已归档产物）；运行中的构建拒绝删除。
func (h *BuildHandler) Delete(w http.ResponseWriter, r *http.Request) {
	err := h.builds.DeleteBuild(r.Context(),
		chi.URLParam(r, "pid"), chi.URLParam(r, "bid"))
	if err != nil {
		pkg.Fail(w, err)
		return
	}
	pkg.OK(w, nil)
}

// Artifacts GET /api/pipelines/{pid}/builds/{bid}/artifacts 返回产物清单。
func (h *BuildHandler) Artifacts(w http.ResponseWriter, r *http.Request) {
	manifest, err := h.artifacts.Manifest(r.Context(), chi.URLParam(r, "pid"), chi.URLParam(r, "bid"))
	if err != nil {
		pkg.Fail(w, err)
		return
	}
	pkg.OK(w, manifest)
}

// DownloadArtifact GET /api/pipelines/{pid}/builds/{bid}/artifacts/download?file=...
// 返回单个产物文件（二进制，不套统一 JSON 响应）。
func (h *BuildHandler) DownloadArtifact(w http.ResponseWriter, r *http.Request) {
	file, artifact, err := h.artifacts.Open(
		r.Context(),
		chi.URLParam(r, "pid"),
		chi.URLParam(r, "bid"),
		r.URL.Query().Get("file"),
	)
	if err != nil {
		pkg.Fail(w, err)
		return
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		pkg.Fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", attachmentName(filepath.Base(artifact.Path)))
	http.ServeContent(w, r, filepath.Base(artifact.Path), info.ModTime(), file)
}

// DownloadArtifactsZip GET /api/pipelines/{pid}/builds/{bid}/artifacts.zip
// 把该次构建的全部产物打包下载，便于整体回滚。
func (h *BuildHandler) DownloadArtifactsZip(w http.ResponseWriter, r *http.Request) {
	pipelineID := chi.URLParam(r, "pid")
	buildID := chi.URLParam(r, "bid")
	manifest, err := h.artifacts.Manifest(r.Context(), pipelineID, buildID)
	if err != nil {
		pkg.Fail(w, err)
		return
	}
	if len(manifest.Files) == 0 {
		pkg.Fail(w, pkg.ErrNotFound("该构建没有归档产物"))
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", attachmentName(fmt.Sprintf("build-%s-artifacts.zip", buildID)))
	if err = h.artifacts.WriteZip(r.Context(), pipelineID, buildID, manifest, w); err != nil {
		// 响应体可能已开始写入，无法再改状态码；记录日志便于排查。
		logger.Infof("download artifacts zip %s/%s: %v", pipelineID, buildID, err)
	}
}

// promoteArtifactsRequest 是跨任务产物晋级请求体。
type promoteArtifactsRequest struct {
	SourcePipelineID string   `json:"source_pipeline_id"`
	SourceBuildID    string   `json:"source_build_id"`
	Prefix           string   `json:"prefix"`
	Files            []string `json:"files"`
	Overwrite        bool     `json:"overwrite"`
}

// PromoteArtifacts POST /api/pipelines/{pid}/builds/{bid}/artifacts/promote
// 把源构建的产物复制到目标任务（bid=latest 时取最近一次成功构建）。
func (h *BuildHandler) PromoteArtifacts(w http.ResponseWriter, r *http.Request) {
	var in promoteArtifactsRequest
	if err := decodeJSON(w, r, &in); err != nil {
		pkg.Fail(w, err)
		return
	}
	in.SourcePipelineID = strings.TrimSpace(in.SourcePipelineID)
	in.SourceBuildID = strings.TrimSpace(in.SourceBuildID)
	if in.SourcePipelineID == "" || in.SourceBuildID == "" {
		pkg.Fail(w, pkg.ErrBadRequest("必须指定源任务和源构建"))
		return
	}
	// 源任务读权限不能因为“目标可构建”而被绕过。
	if h.rbac != nil {
		name, err := h.builds.PipelineName(r.Context(), in.SourcePipelineID)
		if err != nil {
			pkg.Fail(w, err)
			return
		}
		user := pkg.UserFromContext(r.Context())
		if !h.rbac.HasJobPermission(user, model.PermJobRead, name) {
			pkg.Fail(w, pkg.ErrForbidden("没有源任务 %q 的读取权限", name))
			return
		}
	}
	targetID := chi.URLParam(r, "bid")
	if targetID == "latest" {
		targetID = ""
	}
	manifest, err := h.artifacts.Promote(r.Context(),
		in.SourcePipelineID, in.SourceBuildID,
		chi.URLParam(r, "pid"), targetID,
		in.Prefix, in.Files, in.Overwrite)
	if err != nil {
		pkg.Fail(w, err)
		return
	}
	pkg.OK(w, manifest)
}

// republishRequest 是重新发布请求体；server_id 留空时使用任务配置的服务器。
type republishRequest struct {
	ServerID string `json:"server_id"`
	// Publish 选择任务里的哪个发布目标（名称）；为空时取第一个启用的目标。
	Publish string `json:"publish"`
}

// Republish POST /api/pipelines/{pid}/builds/{bid}/republish
// 把该次构建已归档的产物直接发布到 SSH 服务器（不重新编译）。
func (h *BuildHandler) Republish(w http.ResponseWriter, r *http.Request) {
	var in republishRequest
	if err := decodeJSON(w, r, &in); err != nil {
		pkg.Fail(w, err)
		return
	}
	if err := h.builds.Republish(r.Context(), chi.URLParam(r, "pid"),
		chi.URLParam(r, "bid"), in.ServerID, in.Publish); err != nil {
		pkg.Fail(w, err)
		return
	}
	pkg.OK(w, nil)
}

// approvalRequest 是发布审批请求体；拒绝时必须提供理由。
type approvalRequest struct {
	Comment string `json:"comment"`
}

// Approve POST /api/pipelines/{pid}/builds/{bid}/approve
// 放行 pending_approval 构建：状态回到 pending 并重新入队。
func (h *BuildHandler) Approve(w http.ResponseWriter, r *http.Request) {
	var in approvalRequest
	if r.ContentLength != 0 {
		if err := decodeJSON(w, r, &in); err != nil {
			pkg.Fail(w, err)
			return
		}
	}
	b, err := h.builds.DecideApproval(r.Context(),
		chi.URLParam(r, "pid"), chi.URLParam(r, "bid"), true, in.Comment,
		pkg.UserFromContext(r.Context()))
	if err != nil {
		pkg.Fail(w, err)
		return
	}
	pkg.OK(w, b)
}

// Reject POST /api/pipelines/{pid}/builds/{bid}/reject
// 拒绝 pending_approval 构建：直接落 aborted，并记录审批理由。
func (h *BuildHandler) Reject(w http.ResponseWriter, r *http.Request) {
	var in approvalRequest
	if err := decodeJSON(w, r, &in); err != nil {
		pkg.Fail(w, err)
		return
	}
	b, err := h.builds.DecideApproval(r.Context(),
		chi.URLParam(r, "pid"), chi.URLParam(r, "bid"), false, in.Comment,
		pkg.UserFromContext(r.Context()))
	if err != nil {
		pkg.Fail(w, err)
		return
	}
	pkg.OK(w, b)
}

// Cancel POST /api/pipelines/{pid}/builds/{bid}/cancel
// 手动取消运行中/等待中的构建（对齐 Jenkins Stop Build）。
func (h *BuildHandler) Cancel(w http.ResponseWriter, r *http.Request) {
	if err := h.builds.Cancel(chi.URLParam(r, "pid"), chi.URLParam(r, "bid")); err != nil {
		pkg.Fail(w, err)
		return
	}
	pkg.OK(w, nil)
}

// attachmentName 生成兼容中文文件名的 Content-Disposition。
func attachmentName(name string) string {
	value := mime.FormatMediaType("attachment", map[string]string{"filename": name})
	if value == "" {
		return "attachment"
	}
	return value
}

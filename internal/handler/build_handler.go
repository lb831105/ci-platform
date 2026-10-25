package handler

import (
	"ci-platform/internal/pkg/logger"
	"fmt"
	"mime"
	"net/http"
	"path/filepath"

	"github.com/go-chi/chi/v5"

	"ci-platform/internal/pkg"
	"ci-platform/internal/service"
)

// BuildHandler 处理构建历史、详情与控制台日志读取。
type BuildHandler struct {
	builds    *service.BuildService
	artifacts *service.ArtifactService
}

// NewBuildHandler 构造构建接口处理器。
func NewBuildHandler(builds *service.BuildService, artifacts *service.ArtifactService) *BuildHandler {
	return &BuildHandler{builds: builds, artifacts: artifacts}
}

// List GET /api/pipelines/{pid}/builds 遍历构建目录返回历史（编号倒序）。
func (h *BuildHandler) List(w http.ResponseWriter, r *http.Request) {
	items, err := h.builds.ListByPipeline(r.Context(), chi.URLParam(r, "pid"))
	if err != nil {
		pkg.Fail(w, err)
		return
	}
	pkg.OK(w, items)
}

// Get GET /api/pipelines/{pid}/builds/{bid}
func (h *BuildHandler) Get(w http.ResponseWriter, r *http.Request) {
	b, err := h.builds.Get(r.Context(), chi.URLParam(r, "pid"), chi.URLParam(r, "bid"))
	if err != nil {
		pkg.Fail(w, err)
		return
	}
	pkg.OK(w, b)
}

// Log GET /api/pipelines/{pid}/builds/{bid}/log 返回 log.txt 全文。
func (h *BuildHandler) Log(w http.ResponseWriter, r *http.Request) {
	text, err := h.builds.ReadLog(r.Context(), chi.URLParam(r, "pid"), chi.URLParam(r, "bid"))
	if err != nil {
		pkg.Fail(w, err)
		return
	}
	pkg.OK(w, map[string]string{"log": text})
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

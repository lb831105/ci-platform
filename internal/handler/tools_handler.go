package handler

import (
	"net/http"
	"strings"

	"ci-platform/internal/pkg"
	"ci-platform/internal/tools"
)

// ToolsHandler 暴露「全局工具配置」的自动安装能力：
// 版本清单（下拉用）与「立即下载」。
//
// 为什么下载不放在保存设置时：工具包几十上百 MB，
// 保存配置是个轻操作，卡在那一步会让人以为页面挂了。
type ToolsHandler struct {
	installer *tools.Installer
}

// NewToolsHandler 构造工具处理器。
func NewToolsHandler(installer *tools.Installer) *ToolsHandler {
	return &ToolsHandler{installer: installer}
}

// catalogEntry 是某工具某个版本的对外状态。
type catalogEntry struct {
	Version string `json:"version"`
	Note    string `json:"note,omitempty"`
	// Supported 表示当前服务器平台是否有预置下载包。
	Supported bool   `json:"supported"`
	Installed bool   `json:"installed"`
	Home      string `json:"home,omitempty"`
	// Custom 为 true 表示这是用户手填的版本（按 URL 模板推导下载地址）。
	Custom bool `json:"custom,omitempty"`
	// Task 是正在进行的安装任务（含进度），没有则为 null。
	Task *tools.Task `json:"task,omitempty"`
}

// catalogTool 是一类工具的可选版本。
type catalogTool struct {
	Type        string         `json:"type"`
	Title       string         `json:"title"`
	HomeEnv     string         `json:"home_env,omitempty"`
	Hint        string         `json:"hint,omitempty"`
	AutoInstall bool           `json:"auto_install"`
	Versions    []catalogEntry `json:"versions"`
	// VersionHint 告诉前端「手填版本长什么样」（如 Node 的 18.20.4）。
	VersionHint string `json:"version_hint,omitempty"`
}

// Catalog GET /api/system/tools/catalog
//
// 返回各工具的预置版本、当前平台是否可下载、是否已安装（含主目录）。
func (h *ToolsHandler) Catalog(w http.ResponseWriter, r *http.Request) {
	out := make([]catalogTool, 0, 8)
	for _, tool := range tools.Catalog() {
		entry := catalogTool{
			Type: tool.Type, Title: tool.Title, HomeEnv: tool.HomeEnv,
			Hint: tool.Hint, AutoInstall: tool.AutoInstall, VersionHint: tool.VersionHint,
			Versions: make([]catalogEntry, 0, len(tool.Versions)),
		}
		for _, version := range tool.Versions {
			_, err := version.DownloadURL(h.mirror())
			item := catalogEntry{Version: version.Version, Note: version.Note, Supported: err == nil, Custom: version.Custom}
			if h.installer != nil {
				if home, ok := h.installer.Home(tool.Type, version.Version); ok {
					item.Installed = true
					item.Home = home
				}
				if task, ok := h.installer.Task(tool.Type, version.Version); ok {
					snapshot := task
					item.Task = &snapshot
				}
			}
			entry.Versions = append(entry.Versions, item)
		}
		out = append(out, entry)
	}
	pkg.OK(w, out)
}

// Tasks GET /api/system/tools/tasks
//
// 返回后台安装任务的进度。前端据此显示百分比：
// 关掉页面/刷新都不会中断安装（任务跑在服务端后台 context 上）。
func (h *ToolsHandler) Tasks(w http.ResponseWriter, r *http.Request) {
	if h.installer == nil {
		pkg.OK(w, []tools.Task{})
		return
	}
	pkg.OK(w, h.installer.Tasks())
}

// installRequest 是 POST /api/system/tools/install 的请求体。
type installRequest struct {
	Type    string `json:"type"`
	Version string `json:"version"`
}

// Install POST /api/system/tools/install
//
// 启动**后台**下载安装任务并立刻返回任务状态（不等下载完成）。
// 这样用户点完就能离开页面，安装继续；前端轮询 /tools/tasks 看进度。
func (h *ToolsHandler) Install(w http.ResponseWriter, r *http.Request) {
	var in installRequest
	if err := decodeJSON(w, r, &in); err != nil {
		pkg.Fail(w, err)
		return
	}
	if h.installer == nil {
		pkg.Fail(w, pkg.ErrBadRequest("当前实例未启用工具安装器"))
		return
	}
	tool, ok := tools.FindTool(in.Type)
	if !ok {
		pkg.Fail(w, pkg.ErrBadRequest("未知的工具类型：%s", in.Type))
		return
	}
	if !tool.AutoInstall {
		pkg.Fail(w, pkg.ErrBadRequest("%s 不支持自动安装，请手动填写主目录", tool.Title))
		return
	}
	task, err := h.installer.Start(strings.TrimSpace(in.Type), strings.TrimSpace(in.Version))
	if err != nil {
		pkg.Fail(w, pkg.NewError(pkg.CodeBadRequest, "%v", err))
		return
	}
	pkg.OK(w, task)
}

// mirror 返回下载镜像前缀（未启用安装器时为空）。
func (h *ToolsHandler) mirror() string {
	if h.installer == nil {
		return ""
	}
	return h.installer.Mirror()
}

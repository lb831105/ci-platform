package handler

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/go-chi/chi/v5"

	"ci-platform/internal/model"
	"ci-platform/internal/pkg"
	"ci-platform/internal/service"
)

// maxConfigDocBytes 限制导入文档大小：任务的 JSON 配置通常几 KB，
// 1MB 足够容纳超大参数集，同时避免内存被打满。
const maxConfigDocBytes = 1 << 20

// PortabilityProvider 是导出/导入依赖的服务能力（接口便于测试注入）。
type PortabilityProvider interface {
	Export(ctx context.Context, pipelineID string) (*service.PipelineExport, error)
	Import(ctx context.Context, doc []byte, opts service.ImportOptions) (*service.ImportOutcome, error)
}

// PortabilityHandler 处理任务配置的导出与导入（平台自有 JSON 格式）。
type PortabilityHandler struct {
	portability PortabilityProvider
}

// NewPortabilityHandler 创建导入导出处理器。
func NewPortabilityHandler(portability PortabilityProvider) *PortabilityHandler {
	return &PortabilityHandler{portability: portability}
}

// ExportConfig 处理 GET /api/pipelines/{pid}/config.json。
//
// 与 Jenkins 的 `job/{name}/config.xml` 对应：**返回裸文档**，不套 {code,msg,data} 包装。
// 这样 `curl -O` 得到的文件可以直接作为导入接口的输入，便于脚本化搬迁。
func (h *PortabilityHandler) ExportConfig(w http.ResponseWriter, r *http.Request) {
	doc, err := h.portability.Export(r.Context(), chi.URLParam(r, "pid"))
	if err != nil {
		pkg.Fail(w, err) // 错误仍走统一包装，前端按 code 处理
		return
	}
	body, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		pkg.Fail(w, pkg.NewError(pkg.CodeInternal, "序列化配置失败: %v", err))
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	// 文件名同时给出 ASCII 名与 RFC 5987 形式，兼容中文任务名。
	ascii := asciiFallbackName(doc.Name)
	w.Header().Set("Content-Disposition",
		`attachment; filename="`+ascii+`-config.json"; filename*=UTF-8''`+url.PathEscape(doc.Name+".config.json"))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(append(body, '\n'))
}

// ImportConfig 处理 POST /api/system/import/config。
//
// 入参为平台导出文档（或原生 config.json）：
//   - Content-Type: application/json，body 即文档
//   - multipart/form-data：file=配置文档
//
// query：name（覆盖任务名）、mode=create|update|skip、dry_run=true|false
func (h *PortabilityHandler) ImportConfig(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxConfigDocBytes))
	if err != nil {
		pkg.Fail(w, pkg.ErrBadRequest("读取请求体失败（上限 %d KB）：%v", maxConfigDocBytes/1024, err))
		return
	}

	opts := service.ImportOptions{
		Name:   strings.TrimSpace(r.URL.Query().Get("name")),
		Mode:   strings.ToLower(strings.TrimSpace(r.URL.Query().Get("mode"))),
		DryRun: isTruthy(r.URL.Query().Get("dry_run")),
	}
	if opts.Mode == "" {
		opts.Mode = "create"
	}

	if strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "multipart/form-data") {
		r.Body = io.NopCloser(strings.NewReader(string(body)))
		if err := r.ParseMultipartForm(maxConfigDocBytes); err != nil {
			pkg.Fail(w, pkg.ErrBadRequest("表单解析失败：%v", err))
			return
		}
		file, _, err := r.FormFile("file")
		if err != nil {
			pkg.Fail(w, pkg.ErrBadRequest("缺少配置文件（表单字段 file）"))
			return
		}
		defer file.Close()
		body, err = io.ReadAll(io.LimitReader(file, maxConfigDocBytes+1))
		if err != nil {
			pkg.Fail(w, pkg.ErrBadRequest("读取上传文件失败：%v", err))
			return
		}
		if len(body) > maxConfigDocBytes {
			pkg.Fail(w, pkg.ErrBadRequest("配置文件超过 %d KB 上限", maxConfigDocBytes/1024))
			return
		}
		if opts.Name == "" {
			opts.Name = strings.TrimSpace(r.FormValue("name"))
		}
		if v := r.FormValue("mode"); v != "" {
			opts.Mode = strings.ToLower(strings.TrimSpace(v))
		}
		if v := r.FormValue("dry_run"); v != "" {
			opts.DryRun = !isFalsey(v)
		}
	}

	outcome, err := h.portability.Import(r.Context(), body, opts)
	if err != nil {
		pkg.Fail(w, err)
		return
	}
	pkg.OK(w, struct {
		Pipeline *model.Pipeline `json:"pipeline"`
		Warnings []pkg.Warning   `json:"warnings"`
		Action   string          `json:"action"`
		DryRun   bool            `json:"dry_run"`
	}{
		Pipeline: sanitizePipeline(outcome.Pipeline),
		Warnings: outcome.Warnings,
		Action:   outcome.Action,
		DryRun:   opts.DryRun,
	})
}

// asciiFallbackName 生成 Content-Disposition 里的 ASCII 文件名（去中文与特殊字符）。
func asciiFallbackName(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	out := strings.Trim(b.String(), "_")
	if out == "" {
		out = "pipeline"
	}
	if len(out) > 64 {
		out = out[:64]
	}
	return out
}

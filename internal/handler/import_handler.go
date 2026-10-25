package handler

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"ci-platform/internal/importer"
	"ci-platform/internal/model"
	"ci-platform/internal/pkg"
)

// maxImportBytes 限制导入请求体：Jenkins config.xml 通常在几 KB，
// 但插件可能写入大量配置；1MB 足够并避免内存被打满。
const maxImportBytes = 1 << 20

// 导入模式：重名时如何处理。
const (
	importModeCreate = "create" // 默认：自动改名（name-2）
	importModeUpdate = "update" // 覆盖同名任务（迁移中断后重跑用）
	importModeSkip   = "skip"   // 已存在则跳过
)

// PipelineImporter 是导入功能依赖的任务服务能力（只列用到的方法，便于测试注入）。
type PipelineImporter interface {
	List(ctx context.Context) ([]*model.Pipeline, error)
	Create(ctx context.Context, in *model.Pipeline) (*model.Pipeline, error)
	Update(ctx context.Context, id string, in *model.Pipeline) (*model.Pipeline, error)
	Validate(ctx context.Context, in *model.Pipeline) error
	SaveJenkinsSource(ctx context.Context, id string, data []byte) error
}

// SettingsReader 提供系统配置（导入时用于把 SSH 服务器名映射成 server_id）。
type SettingsReader interface {
	Current() model.Settings
}

// ImportHandler 处理「从其它 CI 导入任务」的请求。
type ImportHandler struct {
	pipelines PipelineImporter
	settings  SettingsReader
}

// NewImportHandler 创建导入处理器。
func NewImportHandler(pipelines PipelineImporter, settings SettingsReader) *ImportHandler {
	return &ImportHandler{pipelines: pipelines, settings: settings}
}

// jenkinsImportResult 是导入接口的响应数据。
type jenkinsImportResult struct {
	Pipeline   *model.Pipeline    `json:"pipeline"`
	Warnings   []importer.Warning `json:"warnings"`
	Action     string             `json:"action"` // preview / created / updated / skipped
	DryRun     bool               `json:"dry_run"`
	SourceKept bool               `json:"source_kept"` // 是否保存了原始 config.xml
}

// Jenkins 处理 POST /api/system/import/jenkins。
//
// 入参支持两种形式：
//   - Content-Type: application/xml（或 text/xml）直接放 config.xml，任务名用 ?name=
//   - multipart/form-data：file=config.xml（任务名取文件名或 name 字段）
//
// query/form 参数：name、mode=create|update|skip、dry_run=true|false、keep_source=true|false
func (h *ImportHandler) Jenkins(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxImportBytes))
	if err != nil {
		pkg.Fail(w, pkg.ErrBadRequest("读取请求体失败（上限 %d KB）：%v", maxImportBytes/1024, err))
		return
	}

	opts, err := parseImportOptions(r, body)
	if err != nil {
		pkg.Fail(w, err)
		return
	}

	// 解析 → 翻译。servers 用于把 Jenkins 的发布服务器名映射到平台 server_id。
	var servers []importer.SshServerRef
	if h.settings != nil {
		for _, s := range h.settings.Current().SshServers {
			servers = append(servers, importer.SshServerRef{ID: s.ID, Name: s.Name, Host: s.Host})
		}
	}
	pipeline, warnings, err := importer.FromJenkinsXML(opts.xml, servers)
	if err != nil {
		pkg.Fail(w, err)
		return
	}
	pipeline.Name = opts.name

	ctx := r.Context()
	// 与 Create/Update 走同一套校验：预览阶段就能发现「参数名非法 / cron 不支持」等问题。
	if err := h.pipelines.Validate(ctx, pipeline); err != nil {
		pkg.Fail(w, err)
		return
	}

	result := jenkinsImportResult{Pipeline: sanitizePipeline(pipeline), Warnings: warnings, DryRun: opts.dryRun}
	if opts.dryRun {
		result.Action = "preview"
		pkg.OK(w, result)
		return
	}

	existing, err := h.findByName(ctx, opts.name)
	if err != nil {
		pkg.Fail(w, err)
		return
	}

	var saved *model.Pipeline
	switch {
	case existing != nil && opts.mode == importModeSkip:
		result.Pipeline = sanitizePipeline(existing)
		result.Action = "skipped"
		result.Warnings = append(result.Warnings, importer.Warning{
			Field: "name", Level: importer.LevelInfo,
			Message: "同名任务已存在（mode=skip），未做任何修改",
		})
	case existing != nil && opts.mode == importModeUpdate:
		saved, err = h.pipelines.Update(ctx, existing.ID, pipeline)
		result.Action = "updated"
	default:
		if existing != nil {
			// mode=create 且重名：自动加后缀，避免一次批量导入因个别重名整批失败。
			pipeline.Name = uniqueJobName(ctx, h.pipelines, opts.name)
			result.Warnings = append(result.Warnings, importer.Warning{
				Field: "name", Level: importer.LevelWarn,
				Message: "已存在同名任务，导入为 " + pipeline.Name,
			})
		}
		saved, err = h.pipelines.Create(ctx, pipeline)
		result.Action = "created"
	}
	if err != nil {
		pkg.Fail(w, err)
		return
	}

	if opts.keepSource && saved != nil && saved.ID != "" {
		// 保存失败不影响导入结果：源文件只是「可追溯/可重导」的附加能力。
		if err := h.pipelines.SaveJenkinsSource(ctx, saved.ID, opts.xml); err != nil {
			result.Warnings = append(result.Warnings, importer.Warning{
				Field: "source", Level: importer.LevelWarn,
				Message: "原始 config.xml 保存失败：" + err.Error(),
			})
		} else {
			result.SourceKept = true
		}
	}
	result.Pipeline = sanitizePipeline(saved)
	pkg.OK(w, result)
}

// importOptions 是解析后的导入参数。
type importOptions struct {
	xml        []byte
	name       string
	mode       string
	dryRun     bool
	keepSource bool
}

// parseImportOptions 从 query / multipart 表单中取出参数。
// 注意：multipart 会把 body 读走，因此只有确认是 multipart 时才解析表单。
func parseImportOptions(r *http.Request, body []byte) (importOptions, error) {
	opts := importOptions{mode: importModeCreate, keepSource: true}
	contentType := strings.ToLower(r.Header.Get("Content-Type"))
	multipart := strings.HasPrefix(contentType, "multipart/form-data")

	if multipart {
		// 用已读取的 body 重新构造 multipart reader，避免二次读流。
		r.Body = io.NopCloser(bytes.NewReader(body))
		if err := r.ParseMultipartForm(maxImportBytes); err != nil {
			return opts, pkg.ErrBadRequest("表单解析失败：%v", err)
		}
		file, header, err := r.FormFile("file")
		if err != nil {
			return opts, pkg.ErrBadRequest("缺少 config.xml 文件（表单字段 file）")
		}
		defer file.Close()
		// 表单里的 XML 以文件为准；限制读取大小防止绕过 body 上限。
		opts.xml, err = io.ReadAll(io.LimitReader(file, maxImportBytes+1))
		if err != nil {
			return opts, pkg.ErrBadRequest("读取上传文件失败：%v", err)
		}
		if len(opts.xml) > maxImportBytes {
			return opts, pkg.ErrBadRequest("config.xml 超过 %d KB 上限", maxImportBytes/1024)
		}
		opts.name = strings.TrimSpace(r.FormValue("name"))
		if opts.name == "" && header != nil {
			// 用文件名兜底：Jenkins 导出的常见命名就是 <job>.xml。
			opts.name = strings.TrimSuffix(filepath.Base(header.Filename), filepath.Ext(header.Filename))
		}
		if v := r.FormValue("mode"); v != "" {
			opts.mode = strings.ToLower(strings.TrimSpace(v))
		}
		if v := r.FormValue("keep_source"); v != "" {
			opts.keepSource = !isFalsey(v)
		}
		if v := r.FormValue("dry_run"); v != "" {
			opts.dryRun = !isFalsey(v)
		}
	} else {
		opts.xml = body
		opts.name = strings.TrimSpace(r.URL.Query().Get("name"))
		if v := r.URL.Query().Get("mode"); v != "" {
			opts.mode = strings.ToLower(strings.TrimSpace(v))
		}
		if v := r.URL.Query().Get("keep_source"); v != "" {
			opts.keepSource = !isFalsey(v)
		}
		opts.dryRun = isTruthy(r.URL.Query().Get("dry_run"))
	}

	switch opts.mode {
	case importModeCreate, importModeUpdate, importModeSkip:
	default:
		return opts, pkg.ErrBadRequest("mode 只能是 create / update / skip")
	}
	if opts.name == "" {
		return opts, pkg.ErrBadRequest("缺少任务名：Jenkins 的 config.xml 不含任务名，请用 ?name=任务名 指定（multipart 可用文件名）")
	}
	if len(opts.xml) == 0 {
		return opts, pkg.ErrBadRequest("config.xml 内容为空")
	}
	return opts, nil
}

// findByName 按任务名查找（不区分大小写），用于 create/update/skip 决策。
func (h *ImportHandler) findByName(ctx context.Context, name string) (*model.Pipeline, error) {
	list, err := h.pipelines.List(ctx)
	if err != nil {
		return nil, err
	}
	for _, p := range list {
		if strings.EqualFold(strings.TrimSpace(p.Name), name) {
			return p, nil
		}
	}
	return nil, nil
}

// uniqueJobName 生成不冲突的任务名：name-2、name-3 …
func uniqueJobName(ctx context.Context, pipelines PipelineImporter, name string) string {
	list, err := pipelines.List(ctx)
	if err != nil {
		return name
	}
	used := make(map[string]bool, len(list))
	for _, p := range list {
		used[strings.ToLower(strings.TrimSpace(p.Name))] = true
	}
	if !used[strings.ToLower(name)] {
		return name
	}
	for i := 2; i < 1000; i++ {
		candidate := name + "-" + strconv.Itoa(i)
		if !used[strings.ToLower(candidate)] {
			return candidate
		}
	}
	return name
}

func isTruthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

func isFalsey(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "0", "false", "no", "off":
		return true
	}
	return false
}

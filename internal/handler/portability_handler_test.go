package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"ci-platform/internal/model"
	"ci-platform/internal/pkg"
	"ci-platform/internal/service"
)

// fakePortability 记录调用并返回预置结果。
type fakePortability struct {
	doc      *service.PipelineExport
	exported []string
	outcome  *service.ImportOutcome
	imported []string
	opts     []service.ImportOptions
	err      error
}

func (f *fakePortability) Export(_ context.Context, pipelineID string) (*service.PipelineExport, error) {
	f.exported = append(f.exported, pipelineID)
	if f.err != nil {
		return nil, f.err
	}
	return f.doc, nil
}

func (f *fakePortability) Import(_ context.Context, doc []byte, opts service.ImportOptions) (*service.ImportOutcome, error) {
	f.imported = append(f.imported, string(doc))
	f.opts = append(f.opts, opts)
	if f.err != nil {
		return nil, f.err
	}
	return f.outcome, nil
}

func withPipelineParam(req *http.Request, id string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("pid", id)
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
}

// 导出返回裸 JSON 文档（不是统一包装），并带下载文件名。
func TestExportConfigReturnsRawDocument(t *testing.T) {
	fake := &fakePortability{doc: &service.PipelineExport{
		ExportVersion: service.ExportVersion,
		Name:          "订单服务",
		GitBranch:     "main",
		Steps:         []model.Step{{Name: "build", Run: "echo hi"}},
		Warnings:      []pkg.Warning{{Field: "webhook_secret", Level: pkg.LevelInfo, Message: "密钥不导出"}},
	}}
	h := NewPortabilityHandler(fake)
	req := withPipelineParam(httptest.NewRequest(http.MethodGet, "/api/pipelines/1/config.json", nil), "1")
	rec := httptest.NewRecorder()
	h.ExportConfig(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Fatalf("content-type = %q", ct)
	}
	disposition := rec.Header().Get("Content-Disposition")
	if !strings.Contains(disposition, "attachment") || !strings.Contains(disposition, "filename*=UTF-8''") {
		t.Fatalf("content-disposition = %q", disposition)
	}
	// 裸文档：能直接反序列化成导出结构，且没有 code/msg 包装。
	var doc service.PipelineExport
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("body 不是裸 JSON 文档: %v\n%s", err, rec.Body.String())
	}
	if doc.Name != "订单服务" || doc.ExportVersion == 0 {
		t.Fatalf("doc = %+v", doc)
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if _, ok := envelope["code"]; ok {
		t.Fatal("导出接口不应使用 {code,msg,data} 包装")
	}
	if len(fake.exported) != 1 || fake.exported[0] != "1" {
		t.Fatalf("exported = %v", fake.exported)
	}
}

// 导出失败（任务不存在）时仍返回统一错误包装。
func TestExportConfigErrorUsesEnvelope(t *testing.T) {
	fake := &fakePortability{err: pkg.ErrNotFound("任务不存在")}
	h := NewPortabilityHandler(fake)
	rec := httptest.NewRecorder()
	h.ExportConfig(rec, withPipelineParam(httptest.NewRequest(http.MethodGet, "/api/pipelines/1/config.json", nil), "1"))

	var out struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Code != pkg.CodeNotFound {
		t.Fatalf("code = %d, want %d", out.Code, pkg.CodeNotFound)
	}
}

// 导入：body 是文档本身，query 参数传到 service，响应是统一包装（含脱敏后的 pipeline）。
func TestImportConfigPassesOptionsAndSanitizes(t *testing.T) {
	fake := &fakePortability{outcome: &service.ImportOutcome{
		Action: "created",
		Pipeline: &model.Pipeline{
			ID: "id-1", Name: "order",
			Steps: []model.Step{{Name: "b", Run: "echo hi"}},
			Notify: &model.JobNotify{Enabled: true, Targets: []model.NotifyTarget{{
				Channel: "dingtalk", URL: "https://oapi.dingtalk.com/robot/send?access_token=leak",
			}}},
		},
		Warnings: []pkg.Warning{{Field: "ssh", Level: pkg.LevelError, Message: "服务器不存在"}},
	}}
	h := NewPortabilityHandler(fake)
	body := `{"export_version":1,"name":"order","steps":[{"name":"b","run":"echo hi"}]}`
	req := httptest.NewRequest(http.MethodPost, "/api/system/import/config?name=override&mode=update&dry_run=true", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ImportConfig(rec, req)

	var out struct {
		Code int `json:"code"`
		Data struct {
			Pipeline *model.Pipeline `json:"pipeline"`
			Warnings []pkg.Warning   `json:"warnings"`
			Action   string          `json:"action"`
			DryRun   bool            `json:"dry_run"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v\n%s", err, rec.Body.String())
	}
	if out.Code != 0 {
		t.Fatalf("code = %d", out.Code)
	}
	if out.Data.Action != "created" || !out.Data.DryRun {
		t.Fatalf("action/dryRun = %s/%v", out.Data.Action, out.Data.DryRun)
	}
	if len(out.Data.Warnings) != 1 || out.Data.Warnings[0].Level != pkg.LevelError {
		t.Fatalf("warnings = %+v", out.Data.Warnings)
	}
	// 响应必须脱敏（群机器人 token 不回显）。
	if strings.Contains(rec.Body.String(), "access_token=leak") {
		t.Fatal("响应泄露了群机器人 token")
	}
	if len(fake.opts) != 1 {
		t.Fatalf("opts = %+v", fake.opts)
	}
	got := fake.opts[0]
	if got.Name != "override" || got.Mode != "update" || !got.DryRun {
		t.Fatalf("opts = %+v", got)
	}
	if fake.imported[0] != body {
		t.Fatalf("body 未原样传给 service: %q", fake.imported[0])
	}
}

// 导入：非法文档/空内容由 service 报错，handler 只负责包装。
func TestImportConfigErrorAndEmptyBody(t *testing.T) {
	fake := &fakePortability{err: pkg.ErrBadRequest("配置文档没有构建步骤（steps）")}
	h := NewPortabilityHandler(fake)
	req := httptest.NewRequest(http.MethodPost, "/api/system/import/config", strings.NewReader(`{"name":"x"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ImportConfig(rec, req)

	var out struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Code != pkg.CodeBadRequest || !strings.Contains(out.Msg, "构建步骤") {
		t.Fatalf("code=%d msg=%s", out.Code, out.Msg)
	}
}

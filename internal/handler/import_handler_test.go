package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ci-platform/internal/model"
	"ci-platform/internal/pkg"
)

// fakeImporter 记录导入过程中的调用，便于断言 create/update/skip 行为。
type fakeImporter struct {
	items    []*model.Pipeline
	created  []*model.Pipeline
	updated  []string
	sources  map[string]string
	validate func(*model.Pipeline) error
}

func (f *fakeImporter) List(context.Context) ([]*model.Pipeline, error) { return f.items, nil }

func (f *fakeImporter) Create(_ context.Context, in *model.Pipeline) (*model.Pipeline, error) {
	in.ID = "id-" + in.Name
	f.created = append(f.created, in)
	f.items = append(f.items, in)
	return in, nil
}

func (f *fakeImporter) Update(_ context.Context, id string, in *model.Pipeline) (*model.Pipeline, error) {
	f.updated = append(f.updated, id)
	in.ID = id
	for i := range f.items {
		if f.items[i].ID == id {
			f.items[i] = in
		}
	}
	return in, nil
}

func (f *fakeImporter) Validate(_ context.Context, in *model.Pipeline) error {
	if f.validate != nil {
		return f.validate(in)
	}
	return nil
}

func (f *fakeImporter) SaveJenkinsSource(_ context.Context, id string, data []byte) error {
	if f.sources == nil {
		f.sources = map[string]string{}
	}
	f.sources[id] = string(data)
	return nil
}

// fakeSettings 只提供导入用到的 SSH 服务器列表。
type fakeSettings struct{ settings model.Settings }

func (f *fakeSettings) Current() model.Settings { return f.settings }

type importResp struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
	Data struct {
		Pipeline *model.Pipeline `json:"pipeline"`
		Warnings []struct {
			Field   string `json:"field"`
			Level   string `json:"level"`
			Message string `json:"message"`
		} `json:"warnings"`
		Action     string `json:"action"`
		DryRun     bool   `json:"dry_run"`
		SourceKept bool   `json:"source_kept"`
	} `json:"data"`
}

const simpleConfigXML = `<project>
  <scm class="hudson.plugins.git.GitSCM">
    <userRemoteConfigs><hudson.plugins.git.UserRemoteConfig><url>https://git/x.git</url></hudson.plugins.git.UserRemoteConfig></userRemoteConfigs>
    <branches><hudson.plugins.git.BranchSpec><name>*/main</name></hudson.plugins.git.BranchSpec></branches>
  </scm>
  <builders><hudson.tasks.Shell><command>echo hi</command></hudson.tasks.Shell></builders>
</project>`

func newImportHandler(items ...*model.Pipeline) (*ImportHandler, *fakeImporter) {
	fake := &fakeImporter{items: items}
	settings := &fakeSettings{settings: model.Settings{SshServers: []model.SshServer{
		{ID: "s1", Name: "prod", Host: "10.0.0.1"},
	}}}
	return NewImportHandler(fake, settings), fake
}

func postXML(h http.HandlerFunc, xml, query string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/system/import/jenkins"+query, strings.NewReader(xml))
	req.Header.Set("Content-Type", "application/xml")
	rec := httptest.NewRecorder()
	h(rec, req)
	return rec
}

func decodeImport(t *testing.T, rec *httptest.ResponseRecorder) importResp {
	t.Helper()
	var out importResp
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response: %v\n%s", err, rec.Body.String())
	}
	return out
}

// dry_run 只返回翻译结果，不落盘。
func TestJenkinsImportDryRun(t *testing.T) {
	h, fake := newImportHandler()
	out := decodeImport(t, postXML(h.Jenkins, simpleConfigXML, "?name=demo&dry_run=true"))
	if out.Code != 0 {
		t.Fatalf("code = %d, msg = %s", out.Code, out.Msg)
	}
	if out.Data.Action != "preview" || !out.Data.DryRun {
		t.Fatalf("action/dryRun = %s/%v", out.Data.Action, out.Data.DryRun)
	}
	if out.Data.Pipeline == nil || out.Data.Pipeline.GitURL != "https://git/x.git" {
		t.Fatalf("pipeline = %+v", out.Data.Pipeline)
	}
	if len(fake.created) != 0 || len(fake.sources) != 0 {
		t.Fatal("dry_run 不能创建任务或写文件")
	}
}

// 正常导入：创建任务 + 保存原始 XML。
func TestJenkinsImportCreatesJob(t *testing.T) {
	h, fake := newImportHandler()
	out := decodeImport(t, postXML(h.Jenkins, simpleConfigXML, "?name=order"))
	if out.Code != 0 {
		t.Fatalf("code = %d, msg = %s", out.Code, out.Msg)
	}
	if out.Data.Action != "created" || !out.Data.SourceKept {
		t.Fatalf("action = %s, source_kept = %v", out.Data.Action, out.Data.SourceKept)
	}
	if len(fake.created) != 1 || fake.created[0].Name != "order" {
		t.Fatalf("created = %+v", fake.created)
	}
	if len(fake.created[0].Steps) != 1 || fake.created[0].Steps[0].Run != "echo hi" {
		t.Fatalf("steps = %+v", fake.created[0].Steps)
	}
	if fake.sources["id-order"] != simpleConfigXML {
		t.Fatal("原始 config.xml 应保存到任务目录")
	}

	// keep_source=false 时不保存。
	h2, fake2 := newImportHandler()
	decodeImport(t, postXML(h2.Jenkins, simpleConfigXML, "?name=no-source&keep_source=false"))
	if len(fake2.sources) != 0 {
		t.Fatal("keep_source=false 不应写源文件")
	}
}

// 重名：create 自动加后缀、update 覆盖、skip 不动。
func TestJenkinsImportNameConflicts(t *testing.T) {
	existing := &model.Pipeline{ID: "p1", Name: "order"}

	h, fake := newImportHandler(existing)
	out := decodeImport(t, postXML(h.Jenkins, simpleConfigXML, "?name=order"))
	if out.Code != 0 {
		t.Fatalf("code = %d, msg = %s", out.Code, out.Msg)
	}
	if out.Data.Pipeline.Name != "order-2" {
		t.Fatalf("name = %q, want order-2", out.Data.Pipeline.Name)
	}
	if len(fake.created) != 1 || fake.created[0].Name != "order-2" {
		t.Fatalf("created = %+v", fake.created)
	}

	h2, fake2 := newImportHandler(existing)
	out = decodeImport(t, postXML(h2.Jenkins, simpleConfigXML, "?name=ORDER&mode=update"))
	if out.Code != 0 {
		t.Fatalf("update code = %d, msg = %s", out.Code, out.Msg)
	}
	if out.Data.Action != "updated" || len(fake2.updated) != 1 || fake2.updated[0] != "p1" {
		t.Fatalf("action = %s, updated = %v", out.Data.Action, fake2.updated)
	}

	h3, fake3 := newImportHandler(existing)
	out = decodeImport(t, postXML(h3.Jenkins, simpleConfigXML, "?name=order&mode=skip"))
	if out.Code != 0 || out.Data.Action != "skipped" {
		t.Fatalf("skip: code=%d action=%s", out.Code, out.Data.Action)
	}
	if len(fake3.created) != 0 || len(fake3.updated) != 0 {
		t.Fatal("skip 不能创建或更新任务")
	}
}

// 参数/模式错误与不支持的任务类型必须给出可读的 40000。
func TestJenkinsImportRejectsBadRequests(t *testing.T) {
	h, _ := newImportHandler()

	if out := decodeImport(t, postXML(h.Jenkins, simpleConfigXML, "")); out.Code != pkg.CodeBadRequest {
		t.Fatalf("缺少任务名: code = %d", out.Code)
	}
	if out := decodeImport(t, postXML(h.Jenkins, simpleConfigXML, "?name=x&mode=merge")); out.Code != pkg.CodeBadRequest {
		t.Fatalf("非法 mode: code = %d", out.Code)
	}
	if out := decodeImport(t, postXML(h.Jenkins, "", "?name=x")); out.Code != pkg.CodeBadRequest {
		t.Fatalf("空内容: code = %d", out.Code)
	}
	flow := `<flow-definition><definition class="org.jenkinsci.plugins.workflow.cps.CpsFlowDefinition"/></flow-definition>`
	if out := decodeImport(t, postXML(h.Jenkins, flow, "?name=pipeline")); out.Code != pkg.CodeBadRequest {
		t.Fatalf("Jenkinsfile: code = %d", out.Code)
	}

	// 校验失败（例如 cron 不支持）要回传服务的错误信息。
	h2, fake := newImportHandler()
	fake.validate = func(*model.Pipeline) error { return pkg.ErrBadRequest("定时构建规格非法: bad") }
	out := decodeImport(t, postXML(h2.Jenkins, simpleConfigXML, "?name=x"))
	if out.Code != pkg.CodeBadRequest || !strings.Contains(out.Msg, "定时构建规格非法") {
		t.Fatalf("validate error: code=%d msg=%s", out.Code, out.Msg)
	}
	if len(fake.created) != 0 {
		t.Fatal("校验失败不能创建任务")
	}
}

// multipart 上传：任务名可从文件名兜底。
func TestJenkinsImportMultipart(t *testing.T) {
	h, fake := newImportHandler()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "order-service.xml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte(simpleConfigXML)); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/system/import/jenkins", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	rec := httptest.NewRecorder()
	h.Jenkins(rec, req)
	out := decodeImport(t, rec)
	if out.Code != 0 {
		t.Fatalf("code = %d, msg = %s", out.Code, out.Msg)
	}
	if out.Data.Pipeline.Name != "order-service" {
		t.Fatalf("name = %q, want order-service（来自文件名）", out.Data.Pipeline.Name)
	}
	if len(fake.created) != 1 {
		t.Fatalf("created = %d", len(fake.created))
	}
}

package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"ci-platform/internal/model"
	"ci-platform/internal/pkg"
	"ci-platform/internal/store"
)

// newPortabilityService 组装真实 pipeline store（临时目录）+ 假的设置/凭据来源。
func newPortabilityService(t *testing.T, servers []model.SshServer, creds []model.Credential) (*PortabilityService, *PipelineService) {
	t.Helper()
	root := t.TempDir()
	pipelines := NewPipelineService(store.NewPipelineStore(root+"/jobs"), nil, NewWorkspaceManager(root+"/workspace"))
	settings := &fakeSettingsProvider{settings: model.Settings{SshServers: servers}}
	svc := NewPortabilityService(pipelines, settings, &fakeCredentialLister{items: creds}, "test")
	return svc, pipelines
}

type fakeCredentialLister struct{ items []model.Credential }

func (f *fakeCredentialLister) List(context.Context) ([]model.Credential, error) { return f.items, nil }

func createPipeline(t *testing.T, svc *PipelineService, p *model.Pipeline) *model.Pipeline {
	t.Helper()
	saved, err := svc.Create(context.Background(), p)
	if err != nil {
		t.Fatalf("create pipeline %q: %v", p.Name, err)
	}
	return saved
}

func basePipeline(name string) *model.Pipeline {
	return &model.Pipeline{
		Name:  name,
		Steps: []model.Step{{Name: "build", Run: "echo hi"}},
	}
}

// 导出必须脱敏：SSH 密码、Webhook 密钥、群机器人 URL 都不能出现在文档里。
func TestExportSanitizesSecrets(t *testing.T) {
	servers := []model.SshServer{{ID: "s1", Name: "prod", Host: "10.0.0.1"}}
	svc, pipelines := newPortabilityService(t, servers, []model.Credential{{ID: "c1", Name: "gitlab-token"}})

	p := basePipeline("order")
	p.GitURL = "https://git/x.git"
	p.GitCredential = "c1"
	p.WebhookSecret = "super-secret"
	p.SshPublishes = []model.SshPublishConfig{
		{Name: "prod", Enabled: true, ServerID: "s1", Password: "hidden", SourceFiles: []string{"*"}, RemoteBaseDir: "/srv"},
	}
	p.Notify = &model.JobNotify{
		Enabled: true,
		People:  []string{"alice"},
		Targets: []model.NotifyTarget{{Channel: "dingtalk", URL: "https://oapi.dingtalk.com/robot/send?access_token=abc"}},
	}
	createPipeline(t, pipelines, p)

	doc, err := svc.Export(context.Background(), savedID(t, pipelines, "order"))
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	for _, secret := range []string{"hidden", "super-secret", "access_token=abc"} {
		if strings.Contains(body, secret) {
			t.Fatalf("导出文档泄露了密钥 %q：%s", secret, body)
		}
	}
	if doc.ExportVersion != ExportVersion || doc.GitCredentialName != "gitlab-token" {
		t.Fatalf("doc = %+v", doc)
	}
	if len(doc.SshPublishes) != 1 || doc.SshPublishes[0].ServerName != "prod" {
		t.Fatalf("ssh publishes = %+v", doc.SshPublishes)
	}
	// 存在需要提示的差异时必须有告警。
	assertLevel(t, doc.Warnings, "webhook_secret", pkg.LevelInfo)
	assertLevel(t, doc.Warnings, "notify", pkg.LevelInfo)
}

// 内联 SSH 账号（旧版）没有密码就不能用：导出为停用 + 告警。
func TestExportDisablesInlineSSH(t *testing.T) {
	svc, pipelines := newPortabilityService(t, nil, nil)
	p := basePipeline("legacy")
	p.SshPublishes = []model.SshPublishConfig{
		{Name: "inline", Enabled: true, Host: "1.2.3.4", Username: "root", AuthType: "password", Password: "x",
			RemoteBaseDir: "/srv/app", SourceFiles: []string{"*"}},
	}
	createPipeline(t, pipelines, p)

	doc, err := svc.Export(context.Background(), savedID(t, pipelines, "legacy"))
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if doc.SshPublishes[0].Enabled || doc.SshPublishes[0].Password != "" {
		t.Fatalf("内联目标应导出为停用且无密码：%+v", doc.SshPublishes[0])
	}
	assertLevel(t, doc.Warnings, "ssh", pkg.LevelWarn)
}

// 导出的文档能被导入到「另一个实例」：服务器/凭据按名字对齐。
func TestImportRoundTripResolvesByName(t *testing.T) {
	srcServers := []model.SshServer{{ID: "s1", Name: "prod", Host: "10.0.0.1"}}
	src, srcPipelines := newPortabilityService(t, srcServers, []model.Credential{{ID: "c1", Name: "gitlab-token"}})
	p := basePipeline("order")
	p.Steps = []model.Step{{Name: "build", Run: "echo hi", Retry: 2, TimeoutSec: 60}}
	p.GitURL = "https://git/x.git"
	p.GitCredential = "c1"
	p.CronSpec = "H 2 * * *"
	p.Params = []model.ParamDef{{Name: "VERSION", Type: model.ParamString, Default: "1.0.0"}}
	p.Artifacts = model.ArtifactConfig{Enabled: true, Files: []string{"dist/**"}}
	p.BuildRetentionMaxPerJob = 10
	p.SshPublishes = []model.SshPublishConfig{
		{Name: "prod", Enabled: true, ServerID: "s1", SourceFiles: []string{"dist/**"}, RemoteBaseDir: "app", ExecCommand: "restart"},
	}
	createPipeline(t, srcPipelines, p)
	doc, err := src.Export(context.Background(), savedID(t, srcPipelines, "order"))
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	payload, _ := json.Marshal(doc)

	// 目标实例：服务器/凭据 ID 完全不同，只有名字一致。
	dstServers := []model.SshServer{{ID: "target-9", Name: "Prod", Host: "10.0.0.9"}}
	dst, dstPipelines := newPortabilityService(t, dstServers, []model.Credential{{ID: "target-c1", Name: "GitLab-Token"}})
	out, err := dst.Import(context.Background(), payload, ImportOptions{})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if out.Action != "created" || out.Pipeline.Name != "order" {
		t.Fatalf("outcome = %s/%s", out.Action, out.Pipeline.Name)
	}
	got := out.Pipeline
	if got.GitCredential != "target-c1" {
		t.Fatalf("git credential = %q, want target-c1（按名字对齐）", got.GitCredential)
	}
	if len(got.SshPublishes) != 1 || got.SshPublishes[0].ServerID != "target-9" || !got.SshPublishes[0].Enabled {
		t.Fatalf("ssh publishes = %+v", got.SshPublishes)
	}
	if got.CronSpec != "H 2 * * *" || got.BuildRetentionMaxPerJob != 10 ||
		!got.Artifacts.Enabled || got.Steps[0].Retry != 2 || got.Steps[0].TimeoutSec != 60 {
		t.Fatalf("配置丢失：%+v", got)
	}
	// 落盘后能再导出（幂等往返）。
	if _, err := dst.Export(context.Background(), got.ID); err != nil {
		t.Fatalf("re-export: %v", err)
	}
	_ = dstPipelines
}

// 目标实例缺少服务器/凭据时：保存为停用 + error 告警，而不是让导入失败。
func TestImportUnresolvedResourcesWarn(t *testing.T) {
	doc := []byte(`{
      "export_version": 1,
      "name": "order",
      "git_url": "https://git/x.git",
      "git_credential_name": "missing-cred",
      "steps": [{"name":"b","run":"echo hi"}],
      "ssh_publishes": [{"name":"prod","enabled":true,"server_name":"missing-server","source_files":["*"]}]
    }`)
	svc, _ := newPortabilityService(t, nil, nil)
	out, err := svc.Import(context.Background(), doc, ImportOptions{})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if out.Pipeline.GitCredential != "" {
		t.Fatalf("git credential = %q, want 空", out.Pipeline.GitCredential)
	}
	if out.Pipeline.SshPublishes[0].Enabled || out.Pipeline.SshPublishes[0].ServerID != "" {
		t.Fatalf("ssh publish = %+v", out.Pipeline.SshPublishes[0])
	}
	assertLevel(t, out.Warnings, "git_credential", pkg.LevelError)
	assertLevel(t, out.Warnings, "ssh", pkg.LevelError)
}

// 导入导出文档时：群机器人空地址被丢弃（token 不导出）、webhook 密钥被忽略。
func TestImportprunesNotifyAndDropsWebhookSecret(t *testing.T) {
	doc := []byte(`{
      "export_version": 1,
      "name": "order",
      "webhook_secret": "should-be-dropped",
      "steps": [{"name":"b","run":"echo hi"}],
      "notify": {"enabled": true, "people": ["alice"],
        "targets": [{"channel":"dingtalk","url":""},{"channel":"slack","url":"https://hooks.slack.com/x"}],
        "events": {"failure": true}}
    }`)
	svc, _ := newPortabilityService(t, nil, nil)
	out, err := svc.Import(context.Background(), doc, ImportOptions{})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if out.Pipeline.WebhookSecret != "" {
		t.Fatalf("webhook secret = %q, want 空", out.Pipeline.WebhookSecret)
	}
	if out.Pipeline.Notify == nil || len(out.Pipeline.Notify.Targets) != 1 ||
		out.Pipeline.Notify.Targets[0].Channel != "slack" {
		t.Fatalf("notify targets = %+v", out.Pipeline.Notify)
	}
	assertLevel(t, out.Warnings, "notify", pkg.LevelWarn)
	assertLevel(t, out.Warnings, "webhook_secret", pkg.LevelInfo)
}

// 原生 config.json（带 id/created_at 等字段）也能导入，凭据按 ID 校验。
func TestImportAcceptsNativeConfig(t *testing.T) {
	servers := []model.SshServer{{ID: "s1", Name: "prod", Host: "10.0.0.1"}}
	svc, _ := newPortabilityService(t, servers, []model.Credential{{ID: "c1", Name: "gitlab"}})
	doc := []byte(`{
      "id": "123", "name": "native", "created_at": "2024-01-01T00:00:00Z",
      "steps": [{"name":"b","run":"echo hi"}],
      "git_url": "https://git/x.git", "git_branch": "main", "git_credential_id": "c1",
      "webhook_secret": "keep-should-drop", "cron_spec": "0 1 * * *",
      "artifacts": {"enabled": false, "files": []},
      "ssh_publish": {"enabled": true, "server_id": "s1", "source_files": ["*"], "password": "x"}
    }`)
	out, err := svc.Import(context.Background(), doc, ImportOptions{Name: "renamed"})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	got := out.Pipeline
	if got.Name != "renamed" {
		t.Fatalf("name = %q, want renamed（参数覆盖）", got.Name)
	}
	if got.GitCredential != "c1" || got.CronSpec != "0 1 * * *" {
		t.Fatalf("pipeline = %+v", got)
	}
	if len(got.SshPublishes) != 1 || got.SshPublishes[0].ServerID != "s1" || got.SshPublishes[0].Password != "" {
		t.Fatalf("ssh = %+v", got.SshPublishes)
	}
	if got.WebhookSecret != "" {
		t.Fatalf("webhook secret = %q, want 空（导入不继承密钥）", got.WebhookSecret)
	}
}

// 文档版本高于服务、缺步骤、坏 JSON 都必须明确拒绝。
func TestImportRejectsBadDocuments(t *testing.T) {
	svc, _ := newPortabilityService(t, nil, nil)
	cases := map[string]string{
		"版本过高":   `{"export_version": 99, "name":"x", "steps":[{"name":"b","run":"echo"}]}`,
		"没有步骤":   `{"export_version": 1, "name":"x"}`,
		"坏 JSON": `{`,
		"多个对象":   `{"export_version":1,"name":"x","steps":[{"run":"echo","name":"b"}]}{"a":1}`,
	}
	for name, doc := range cases {
		if _, err := svc.Import(context.Background(), []byte(doc), ImportOptions{}); err == nil {
			t.Fatalf("%s: 应返回错误", name)
		}
	}
	// 缺任务名
	if _, err := svc.Import(context.Background(), []byte(`{"export_version":1,"steps":[{"name":"b","run":"echo"}]}`), ImportOptions{}); err == nil {
		t.Fatal("缺任务名应报错")
	}
}

// 同名任务的三种策略。
func TestImportNameConflictModes(t *testing.T) {
	doc := []byte(`{"export_version":1,"name":"dup","steps":[{"name":"b","run":"echo v2"}]}`)
	svc, pipelines := newPortabilityService(t, nil, nil)
	createPipeline(t, pipelines, basePipeline("dup"))

	out, err := svc.Import(context.Background(), doc, ImportOptions{})
	if err != nil {
		t.Fatalf("create mode: %v", err)
	}
	if out.Pipeline.Name != "dup-2" || out.Action != "created" {
		t.Fatalf("create mode = %s/%s", out.Action, out.Pipeline.Name)
	}

	out, err = svc.Import(context.Background(), doc, ImportOptions{Mode: "update"})
	if err != nil {
		t.Fatalf("update mode: %v", err)
	}
	if out.Action != "updated" || out.Pipeline.Steps[0].Run != "echo v2" {
		t.Fatalf("update mode = %s/%+v", out.Action, out.Pipeline.Steps)
	}

	out, err = svc.Import(context.Background(), doc, ImportOptions{Mode: "skip"})
	if err != nil {
		t.Fatalf("skip mode: %v", err)
	}
	if out.Action != "skipped" {
		t.Fatalf("skip mode = %s", out.Action)
	}
	if _, err := svc.Import(context.Background(), doc, ImportOptions{Mode: "merge"}); err == nil {
		t.Fatal("非法 mode 应报错")
	}
}

// dry-run 不落盘。
func TestImportDryRunDoesNotPersist(t *testing.T) {
	doc := []byte(`{"export_version":1,"name":"dry","steps":[{"name":"b","run":"echo hi"}]}`)
	svc, pipelines := newPortabilityService(t, nil, nil)
	out, err := svc.Import(context.Background(), doc, ImportOptions{DryRun: true})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if out.Action != "preview" {
		t.Fatalf("action = %s", out.Action)
	}
	list, err := pipelines.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Fatalf("dry-run 落盘了 %d 个任务", len(list))
	}
}

func savedID(t *testing.T, svc *PipelineService, name string) string {
	t.Helper()
	list, err := svc.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range list {
		if p.Name == name {
			return p.ID
		}
	}
	t.Fatalf("找不到任务 %q", name)
	return ""
}

func assertLevel(t *testing.T, warnings []pkg.Warning, field, level string) {
	t.Helper()
	for _, w := range warnings {
		if w.Field == field && w.Level == level {
			return
		}
	}
	t.Fatalf("缺少告警 field=%s level=%s，实际: %+v", field, level, warnings)
}

// 有人会把 GET /api/pipelines/{id} 的响应（URL 已脱敏成 ***）直接粘进导入框：
// 必须丢弃这些目标并告警，绝不能把 *** 当真实地址写进配置。
func TestImportDropsMaskedNotifyURLs(t *testing.T) {
	doc := []byte(`{
      "name": "order",
      "steps": [{"name":"b","run":"echo hi"}],
      "notify": {"enabled": true, "targets": [
        {"channel":"dingtalk","url":"***"},
        {"channel":"wecom","url":"https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=***"},
        {"channel":"slack","url":"https://hooks.slack.com/services/T000/B000/***"}
      ]}
    }`)
	svc, _ := newPortabilityService(t, nil, nil)
	out, err := svc.Import(context.Background(), doc, ImportOptions{})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if got := out.Pipeline.Notify.Targets; len(got) != 0 {
		t.Fatalf("脱敏地址应被丢弃，实际: %+v", got)
	}
	assertLevel(t, out.Warnings, "notify", pkg.LevelWarn)
}

// Webhook 触发器策略与「状态回写」必须能跨实例搬迁：
// 分支/事件白名单是配置而非密钥，应随文档走；回写凭据按名字对齐。
func TestPortabilityKeepsWebhookPolicy(t *testing.T) {
	ctx := context.Background()
	svc, pipeSvc := newPortabilityService(t, nil, nil)

	p := &model.Pipeline{
		Name:              "gl-job",
		Steps:             []model.Step{{Name: "s", Run: "echo hi"}},
		WebhookBranchMode: "payload",
		WebhookBranches:   []string{"main", "release/*"},
		WebhookEvents:     []string{"push", "tag_push"},
		CommitStatus:      model.CommitStatusConfig{Enabled: false, Project: "ops/gl-job"},
	}
	if _, err := pipeSvc.Create(ctx, p); err != nil {
		t.Fatalf("create: %v", err)
	}

	doc, err := svc.Export(ctx, p.ID)
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	out, err := svc.Import(ctx, data, ImportOptions{Name: "gl-job-copy", Mode: "create"})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	got := out.Pipeline
	if got.WebhookBranchMode != "payload" {
		t.Fatalf("branch mode lost: %q", got.WebhookBranchMode)
	}
	if len(got.WebhookBranches) != 2 || got.WebhookBranches[1] != "release/*" {
		t.Fatalf("branches lost: %v", got.WebhookBranches)
	}
	if len(got.WebhookEvents) != 2 {
		t.Fatalf("events lost: %v", got.WebhookEvents)
	}
	if got.CommitStatus.Project != "ops/gl-job" {
		t.Fatalf("commit status project lost: %q", got.CommitStatus.Project)
	}
}

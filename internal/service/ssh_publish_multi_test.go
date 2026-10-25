package service

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"ci-platform/internal/executor"
	"ci-platform/internal/model"
)

// recordingPublisher 记录每次发布调用（验证多目标发布行为）。
type recordingPublisher struct {
	calls  []model.SshPublishConfig
	failOn map[string]bool // 按目标名称失败
}

func (p *recordingPublisher) Publish(_ context.Context, cfg *model.SshPublishConfig, _ string, _ io.Writer) error {
	p.calls = append(p.calls, *cfg)
	if p.failOn[cfg.Name] {
		return errors.New("publish " + cfg.Name + " failed")
	}
	return nil
}

func (p *recordingPublisher) PublishArtifacts(_ context.Context, cfg *model.SshPublishConfig, _ string, _ io.Writer) error {
	p.calls = append(p.calls, *cfg)
	if p.failOn[cfg.Name] {
		return errors.New("publish artifacts " + cfg.Name + " failed")
	}
	return nil
}

// okStepsRunner 立即成功（本测试只关心发布阶段）。
type okStepsRunner struct{}

func (okStepsRunner) RunSteps(context.Context, executor.RunSpec, io.Writer) (int, error) {
	return 0, nil
}

// 旧版单目标配置在归一化后迁移为列表，且不再保留 ssh_publish。
func TestNormalizeSshPublishesMigratesLegacy(t *testing.T) {
	p := &model.Pipeline{
		Name:  "demo",
		Steps: []model.Step{{Name: "b", Run: "echo ok"}},
		SshPublish: &model.SshPublishConfig{
			Enabled: true, ServerID: "s1", SourceFiles: []string{"*"}, RemoteBaseDir: "/opt/app",
		},
	}
	if err := normalizeAndValidate(p, ""); err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if p.SshPublish != nil {
		t.Fatal("legacy ssh_publish must be cleared after normalization")
	}
	if len(p.SshPublishes) != 1 || p.SshPublishes[0].Name != "默认发布" {
		t.Fatalf("publishes = %+v, want single 默认发布", p.SshPublishes)
	}
	if got := p.EffectiveSshPublishes(); len(got) != 1 || got[0].ServerID != "s1" {
		t.Fatalf("effective = %+v", got)
	}

	// 重名自动加后缀；未命名按序号；超过上限报错。
	p2 := &model.Pipeline{
		Name:  "demo",
		Steps: []model.Step{{Name: "b", Run: "echo ok"}},
		SshPublishes: []model.SshPublishConfig{
			{Enabled: true, ServerID: "s1", SourceFiles: []string{"*"}, RemoteBaseDir: "/a"},
			{Enabled: true, ServerID: "s2", SourceFiles: []string{"*"}, RemoteBaseDir: "/b", Name: "发布 1"},
			{Enabled: true, ServerID: "s3", SourceFiles: []string{"*"}, RemoteBaseDir: "/c"},
		},
	}
	if err := normalizeAndValidate(p2, ""); err != nil {
		t.Fatalf("normalize multi: %v", err)
	}
	seen := map[string]bool{}
	for _, item := range p2.SshPublishes {
		if item.Name == "" || seen[item.Name] {
			t.Fatalf("publish names must be non-empty and unique: %+v", p2.SshPublishes)
		}
		seen[item.Name] = true
	}

	many := make([]model.SshPublishConfig, 0, maxSshPublishes+1)
	for i := 0; i < maxSshPublishes+1; i++ {
		many = append(many, model.SshPublishConfig{
			Enabled: true, ServerID: "s1", SourceFiles: []string{"*"}, RemoteBaseDir: "/a",
		})
	}
	p3 := &model.Pipeline{Name: "demo", Steps: []model.Step{{Name: "b", Run: "echo ok"}}, SshPublishes: many}
	if err := normalizeAndValidate(p3, ""); err == nil {
		t.Fatalf("more than %d publishes must be rejected", maxSshPublishes)
	}
}

// 多目标发布：逐个调用 publisher；单个失败不阻断其它目标，但构建最终失败。
func TestExecuteStagesPublishesToMultipleTargets(t *testing.T) {
	pub := &recordingPublisher{failOn: map[string]bool{}}
	svc := newDeleteTestService(t, t.TempDir())
	svc.publisher = pub
	svc.runner = okStepsRunner{}

	p := &model.Pipeline{
		ID: "p1", Name: "demo",
		Steps: []model.Step{{Name: "b", Run: "echo ok"}},
		SshPublishes: []model.SshPublishConfig{
			{Name: "prod", Enabled: true, Host: "h1", Username: "u", RemoteBaseDir: "/prod", SourceFiles: []string{"*"}},
			{Name: "backup", Enabled: true, Host: "h2", Username: "u", RemoteBaseDir: "/backup", SourceFiles: []string{"*"}},
			{Name: "disabled", Enabled: false, Host: "h3", Username: "u", RemoteBaseDir: "/x", SourceFiles: []string{"*"}},
		},
	}
	b := &model.Build{ID: "1", PipelineID: "p1"}
	code, err := svc.executeStages(context.Background(), p, b,
		Job{PipelineID: "p1", BuildID: "1"}, model.Settings{}, io.Discard)
	if err != nil || code != 0 {
		t.Fatalf("executeStages = %d, %v", code, err)
	}
	if len(pub.calls) != 2 {
		t.Fatalf("publish calls = %d, want 2 (disabled target skipped)", len(pub.calls))
	}
	if pub.calls[0].Name != "prod" || pub.calls[1].Name != "backup" {
		t.Fatalf("publish order/names = %s,%s", pub.calls[0].Name, pub.calls[1].Name)
	}

	// 第一个目标失败：第二个仍然会被尝试，构建最终返回失败并带上目标名。
	pub2 := &recordingPublisher{failOn: map[string]bool{"prod": true}}
	svc.publisher = pub2
	code, err = svc.executeStages(context.Background(), p, b,
		Job{PipelineID: "p1", BuildID: "1"}, model.Settings{}, io.Discard)
	if err == nil || code == 0 {
		t.Fatal("one failing target must fail the build")
	}
	if len(pub2.calls) != 2 {
		t.Fatalf("both targets must be attempted, got %d", len(pub2.calls))
	}
	if !strings.Contains(err.Error(), "prod") {
		t.Fatalf("error must name the failing target: %v", err)
	}
}

// Republish 支持按名称选择目标；未知名称报错；未指定时取第一个启用目标。
func TestRepublishSelectsPublishTarget(t *testing.T) {
	svc, pub := newRepublishService(t)
	svc.pipelines = &memPipelineStore{items: map[string]*model.Pipeline{
		"p1": {
			ID: "p1", Name: "demo",
			SshPublishes: []model.SshPublishConfig{
				{Name: "prod", Enabled: true, ServerID: "s1", SourceFiles: []string{"*"}, RemoteBaseDir: "/prod"},
				{Name: "backup", Enabled: true, ServerID: "s2", SourceFiles: []string{"*"}, RemoteBaseDir: "/backup"},
			},
		},
	}}
	started := time.Now().UTC()
	b := &model.Build{ID: "1", PipelineID: "p1", Status: model.BuildSuccess, StartedAt: &started}
	if err := svc.builds.CreateNext(context.Background(), b); err != nil {
		t.Fatal(err)
	}
	b.Status = model.BuildSuccess
	if err := svc.builds.Update(context.Background(), b); err != nil {
		t.Fatal(err)
	}
	svc.artifactReader = &fakeArtifactReader{manifest: &model.ArtifactManifest{
		Files: []model.ArtifactFile{{Path: "app", Size: 1}},
	}}

	if err := svc.Republish(context.Background(), "p1", "1", "", "backup"); err != nil {
		t.Fatalf("republish by name: %v", err)
	}
	if pub.cfg == nil || pub.cfg.Name != "backup" || pub.cfg.ServerID != "s2" {
		t.Fatalf("cfg = %+v, want backup/s2", pub.cfg)
	}

	if err := svc.Republish(context.Background(), "p1", "1", "", "nope"); err == nil {
		t.Fatal("unknown publish target must be rejected")
	}

	// 未指定目标：取第一个启用目标（prod）。
	pub.cfg = nil
	if err := svc.Republish(context.Background(), "p1", "1", "", ""); err != nil {
		t.Fatalf("republish default: %v", err)
	}
	if pub.cfg == nil || pub.cfg.Name != "prod" {
		t.Fatalf("default target = %+v, want prod", pub.cfg)
	}
}

// Jenkins transfer 选项的映射与安全校验：exec_timeout 有上限；「清空远程目录」必须指定明确目录。
func TestSshPublishTransferOptionsValidation(t *testing.T) {
	base := func(cfg model.SshPublishConfig) *model.Pipeline {
		return &model.Pipeline{
			Name:         "demo",
			Steps:        []model.Step{{Name: "b", Run: "echo ok"}},
			SshPublishes: []model.SshPublishConfig{cfg},
		}
	}
	ok := model.SshPublishConfig{
		Name: "prod", Enabled: true, ServerID: "s1", RemoteBaseDir: "/srv/app",
		SourceFiles: []string{"*"}, CleanRemote: true, Flatten: true, ExecTimeoutSec: 120,
	}
	if err := normalizeAndValidate(base(ok), ""); err != nil {
		t.Fatalf("合法配置: %v", err)
	}

	noDir := ok
	noDir.RemoteBaseDir = ""
	err := normalizeAndValidate(base(noDir), "")
	if err == nil || !strings.Contains(err.Error(), "清空远程目录") {
		t.Fatalf("clean_remote 没目录应拒绝，实际: %v", err)
	}

	tooLong := ok
	tooLong.ExecTimeoutSec = model.MaxStepTimeoutSec + 1
	if err := normalizeAndValidate(base(tooLong), ""); err == nil {
		t.Fatal("exec_timeout 超上限应拒绝")
	}
	neg := ok
	neg.ExecTimeoutSec = -1
	if err := normalizeAndValidate(base(neg), ""); err == nil {
		t.Fatal("exec_timeout 负数应拒绝")
	}
}

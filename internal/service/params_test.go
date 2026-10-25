package service

import (
	"context"
	"io"
	"testing"

	"ci-platform/internal/executor"
	"ci-platform/internal/model"
)

// captureRunner 记录 RunSpec（用于验证参数是否注入环境变量）。
type captureRunner struct {
	spec executor.RunSpec
}

func (r *captureRunner) RunSteps(_ context.Context, spec executor.RunSpec, _ io.Writer) (int, error) {
	r.spec = spec
	return 0, nil
}

func TestNormalizeParamsValidation(t *testing.T) {
	base := func(params ...model.ParamDef) *model.Pipeline {
		return &model.Pipeline{Name: "demo", Steps: []model.Step{{Name: "b", Run: "echo ok"}}, Params: params}
	}

	// 合法配置：类型/默认值归一化。
	p := base(
		model.ParamDef{Name: "VERSION", Type: "", Default: " 1.0.0 "},
		model.ParamDef{Name: "DEBUG", Type: "bool"},
		model.ParamDef{Name: "ENV", Type: "choice", Options: []string{"dev", " ", "prod"}},
		model.ParamDef{Name: "NOTE", Type: "text", Default: "hello"},
	)
	if err := normalizeAndValidate(p, ""); err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if p.Params[0].Type != model.ParamString || p.Params[0].Default != "1.0.0" {
		t.Fatalf("string param = %+v", p.Params[0])
	}
	if p.Params[1].Default != "false" {
		t.Fatalf("bool default = %q, want false", p.Params[1].Default)
	}
	if p.Params[2].Default != "dev" || len(p.Params[2].Options) != 2 {
		t.Fatalf("choice param = %+v", p.Params[2])
	}

	bad := []struct {
		name  string
		param model.ParamDef
	}{
		{"empty name", model.ParamDef{Name: "  "}},
		{"illegal name", model.ParamDef{Name: "1version"}},
		{"illegal char", model.ParamDef{Name: "ver-sion"}},
		{"reserved prefix", model.ParamDef{Name: "CI_BUILD_ID"}},
		{"bad type", model.ParamDef{Name: "X", Type: "file"}},
		{"choice without options", model.ParamDef{Name: "X", Type: "choice"}},
		{"default not in options", model.ParamDef{Name: "X", Type: "choice", Options: []string{"a"}, Default: "b"}},
		{"bad bool default", model.ParamDef{Name: "X", Type: "bool", Default: "yes"}},
	}
	for _, tc := range bad {
		if err := normalizeAndValidate(base(tc.param), ""); err == nil {
			t.Fatalf("%s: want error, got nil", tc.name)
		}
	}

	// 环境变量大小写不敏感 → version / VERSION 视为重复。
	if err := normalizeAndValidate(base(
		model.ParamDef{Name: "version"},
		model.ParamDef{Name: "VERSION"},
	), ""); err == nil {
		t.Fatal("case-insensitive duplicate params must be rejected")
	}

	many := make([]model.ParamDef, 0, maxBuildParams+1)
	for i := 0; i < maxBuildParams+1; i++ {
		many = append(many, model.ParamDef{Name: "P" + string(rune('A'+i))})
	}
	if err := normalizeAndValidate(base(many...), ""); err == nil {
		t.Fatalf("more than %d params must be rejected", maxBuildParams)
	}
}

func TestResolveBuildParams(t *testing.T) {
	defs := []model.ParamDef{
		{Name: "VERSION", Type: model.ParamString, Default: "1.0.0"},
		{Name: "DEBUG", Type: model.ParamBool, Default: "false"},
		{Name: "ENV", Type: model.ParamChoice, Options: []string{"dev", "prod"}, Default: "dev"},
		{Name: "TOKEN", Type: model.ParamString, Required: true},
		{Name: "NOTE", Type: model.ParamText},
	}

	got, err := ResolveBuildParams(defs, map[string]string{
		"version": "2.0.0", // 大小写不敏感
		"DEBUG":   "TRUE",
		"ENV":     "prod",
		"TOKEN":   "abc",
		"NOTE":    "line1",
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	want := map[string]string{"VERSION": "2.0.0", "DEBUG": "true", "ENV": "prod", "TOKEN": "abc", "NOTE": "line1"}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("%s = %q, want %q", k, got[k], v)
		}
	}

	// 未传参数取默认值；必填无默认值 → 报错。
	got, err = ResolveBuildParams([]model.ParamDef{{Name: "VERSION", Default: "1.0.0"}}, nil)
	if err != nil || got["VERSION"] != "1.0.0" {
		t.Fatalf("defaults = %v, %v", got, err)
	}
	if _, err := ResolveBuildParams(defs, map[string]string{"DEBUG": "true"}); err == nil {
		t.Fatal("missing required param must be rejected")
	}

	// 单行参数去换行，text 保留换行。
	got, err = ResolveBuildParams([]model.ParamDef{
		{Name: "A", Type: model.ParamString},
		{Name: "B", Type: model.ParamText},
	}, map[string]string{"A": "x\ny", "B": "x\ny"})
	if err != nil {
		t.Fatal(err)
	}
	if got["A"] != "x y" || got["B"] != "x\ny" {
		t.Fatalf("A=%q B=%q", got["A"], got["B"])
	}

	// 未知参数 / 非法取值 / 未定义参数的任务传入值 → 40000。
	if _, err := ResolveBuildParams(defs, map[string]string{"NOPE": "1"}); err == nil {
		t.Fatal("unknown param must be rejected")
	}
	if _, err := ResolveBuildParams(defs, map[string]string{"ENV": "staging", "TOKEN": "t"}); err == nil {
		t.Fatal("choice out of range must be rejected")
	}
	if _, err := ResolveBuildParams(nil, map[string]string{"A": "1"}); err == nil {
		t.Fatal("params on non-parameterized job must be rejected")
	}
}

// 参数在触发时快照进构建记录，并作为环境变量注入步骤执行。
func TestExecuteStagesInjectsParamEnv(t *testing.T) {
	svc := newDeleteTestService(t, t.TempDir())
	runner := &captureRunner{}
	svc.runner = runner
	svc.pipelines = &memPipelineStore{items: map[string]*model.Pipeline{
		"p1": {
			ID: "p1", Name: "demo",
			Steps:  []model.Step{{Name: "b", Run: "echo ok"}},
			Params: []model.ParamDef{{Name: "VERSION", Default: "1.0.0"}},
		},
	}}

	code, err := svc.executeStages(context.Background(),
		&model.Pipeline{
			ID: "p1", Name: "demo",
			Steps:  []model.Step{{Name: "b", Run: "echo ok"}},
			Params: []model.ParamDef{{Name: "VERSION", Default: "1.0.0"}},
		},
		&model.Build{ID: "1", PipelineID: "p1", Params: map[string]string{"VERSION": "2.0.0"}},
		Job{PipelineID: "p1", BuildID: "1"}, model.Settings{}, io.Discard)
	if err != nil || code != 0 {
		t.Fatalf("executeStages = %d, %v", code, err)
	}
	if runner.spec.Env["VERSION"] != "2.0.0" {
		t.Fatalf("env VERSION = %q", runner.spec.Env["VERSION"])
	}
	if runner.spec.Env["CI_PARAM_VERSION"] != "2.0.0" {
		t.Fatalf("env CI_PARAM_VERSION = %q", runner.spec.Env["CI_PARAM_VERSION"])
	}
}

// fakeEnqueuer 只记录投递的任务，验证 Trigger 的行为（不真正执行）。
type fakeEnqueuer struct{ jobs []Job }

func (f *fakeEnqueuer) Enqueue(job Job) error {
	f.jobs = append(f.jobs, job)
	return nil
}

// 参数在触发时解析并快照进构建记录；未知参数直接拒绝且不产生构建。
func TestTriggerSnapshotsResolvedParams(t *testing.T) {
	svc := newDeleteTestService(t, t.TempDir())
	enq := &fakeEnqueuer{}
	svc.enqueuer = enq
	svc.pipelines = &memPipelineStore{items: map[string]*model.Pipeline{
		"p1": {
			ID: "p1", Name: "demo", WorkspaceDir: "demo",
			Steps: []model.Step{{Name: "b", Run: "echo ok"}},
			Params: []model.ParamDef{
				{Name: "VERSION", Default: "1.0.0"},
				{Name: "DEBUG", Type: model.ParamBool, Default: "false"},
			},
		},
	}}

	b, err := svc.Trigger(context.Background(), "p1", model.TriggerManual, "alice",
		TriggerOptions{Params: map[string]string{"VERSION": "2.0.0"}})
	if err != nil {
		t.Fatalf("trigger: %v", err)
	}
	if b.Params["VERSION"] != "2.0.0" || b.Params["DEBUG"] != "false" {
		t.Fatalf("params = %v", b.Params)
	}
	if len(enq.jobs) != 1 {
		t.Fatalf("enqueued = %d, want 1", len(enq.jobs))
	}

	// 未知参数：拒绝，且不再投递新任务。
	if _, err := svc.Trigger(context.Background(), "p1", model.TriggerManual, "alice",
		TriggerOptions{Params: map[string]string{"TYPO": "x"}}); err == nil {
		t.Fatal("unknown param must be rejected")
	}
	if len(enq.jobs) != 1 {
		t.Fatalf("rejected trigger must not enqueue, got %d", len(enq.jobs))
	}
}

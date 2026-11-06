package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ci-platform/internal/model"
)

// fakeSecretCodec 用可识别前缀模拟 AES-GCM，只验证“落盘不是明文”的接线。
type fakeSecretCodec struct{}

func (fakeSecretCodec) SealSecret(plain string) (string, error) {
	return "enc:" + base64.StdEncoding.EncodeToString([]byte(plain)), nil
}
func (fakeSecretCodec) UnsealSecret(value string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(value, "enc:"))
	return string(raw), err
}

func passwordPipeline() *model.Pipeline {
	return &model.Pipeline{
		ID: "p1", Name: "demo", WorkspaceDir: "demo",
		Steps: []model.Step{{Name: "b", Run: "echo ok"}},
		Params: []model.ParamDef{
			{Name: "TOKEN", Type: model.ParamPassword, Required: true},
			{Name: "VERSION", Type: model.ParamString, Default: "1.0.0"},
		},
	}
}

func TestPasswordParamEncryptedAndInjected(t *testing.T) {
	jobsRoot := t.TempDir()
	svc := newDeleteTestService(t, jobsRoot)
	svc.secrets = fakeSecretCodec{}
	enq := &fakeEnqueuer{}
	svc.enqueuer = enq
	p := passwordPipeline()
	svc.pipelines = &memPipelineStore{items: map[string]*model.Pipeline{"p1": p}}

	b, err := svc.Trigger(context.Background(), "p1", model.TriggerManual, "alice",
		TriggerOptions{Params: map[string]string{"TOKEN": " super secret "}})
	if err != nil {
		t.Fatalf("trigger: %v", err)
	}
	if b.Params["TOKEN"] != "***" || b.Params["VERSION"] != "1.0.0" {
		t.Fatalf("public params = %v", b.Params)
	}
	if b.SecretParams["TOKEN"] == "" || strings.Contains(b.SecretParams["TOKEN"], "super secret") {
		t.Fatalf("secret params not sealed = %v", b.SecretParams)
	}
	raw, err := os.ReadFile(filepath.Join(jobsRoot, "p1", "builds", b.ID, "build.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "super secret") {
		t.Fatal("build.json must not contain password plaintext")
	}
	var stored model.Build
	if err = json.Unmarshal(raw, &stored); err != nil {
		t.Fatal(err)
	}
	if stored.SecretParams["TOKEN"] != b.SecretParams["TOKEN"] {
		t.Fatalf("stored secret = %v", stored.SecretParams)
	}

	runner := &captureRunner{}
	svc.runner = runner
	code, err := svc.executeStages(context.Background(), p, b, Job{PipelineID: "p1", BuildID: b.ID}, model.Settings{}, io.Discard)
	if err != nil || code != 0 {
		t.Fatalf("executeStages = %d, %v", code, err)
	}
	if runner.spec.Env["TOKEN"] != " super secret " {
		t.Fatalf("TOKEN env = %q", runner.spec.Env["TOKEN"])
	}
	if runner.spec.Env["CI_PARAM_TOKEN"] != " super secret " {
		t.Fatalf("CI_PARAM_TOKEN env = %q", runner.spec.Env["CI_PARAM_TOKEN"])
	}
}

func TestPasswordParamRejectsDefault(t *testing.T) {
	p := passwordPipeline()
	p.Params[0].Default = "oops"
	err := normalizeAndValidate(p, "", "main")
	if err == nil {
		t.Fatal("password param with default must be rejected")
	}
}

func TestRerunRestoresPasswordFromSnapshot(t *testing.T) {
	svc := newDeleteTestService(t, t.TempDir())
	svc.secrets = fakeSecretCodec{}
	enq := &fakeEnqueuer{}
	svc.enqueuer = enq
	svc.pipelines = &memPipelineStore{items: map[string]*model.Pipeline{"p1": passwordPipeline()}}

	first, err := svc.Trigger(context.Background(), "p1", model.TriggerManual, "alice",
		TriggerOptions{Params: map[string]string{"TOKEN": "abc"}})
	if err != nil {
		t.Fatal(err)
	}
	next, err := svc.Rerun(context.Background(), "p1", first.ID, "bob")
	if err != nil {
		t.Fatalf("rerun: %v", err)
	}
	plain, err := (fakeSecretCodec{}).UnsealSecret(next.SecretParams["TOKEN"])
	if err != nil || plain != "abc" {
		t.Fatalf("rerun secret = %q, %v (build %+v)", plain, err, next)
	}
	if next.ID == first.ID {
		t.Fatal("rerun must create a new build")
	}
	if len(enq.jobs) != 2 {
		t.Fatalf("enqueued = %d, want 2", len(enq.jobs))
	}
}

// 防止误把密码参数当成普通参数注入 ***。
func TestExecuteStagesFailsWhenSecretCodecMissing(t *testing.T) {
	svc := newDeleteTestService(t, t.TempDir())
	p := passwordPipeline()
	b := &model.Build{ID: "1", PipelineID: "p1", SecretParams: map[string]string{"TOKEN": "enc:abc"}}
	_, err := svc.executeStages(context.Background(), p, b, Job{PipelineID: "p1", BuildID: "1"}, model.Settings{}, io.Discard)
	if err == nil {
		t.Fatal("missing secret codec must fail instead of injecting ***")
	}
}

func TestCollectMaskSecretsCoversExplicitSecrets(t *testing.T) {
	svc := &BuildService{secrets: fakeSecretCodec{}}
	b := &model.Build{
		SecretParams: map[string]string{
			"FOO": "enc:" + base64.StdEncoding.EncodeToString([]byte("hunter2")),
		},
	}
	settings := model.Settings{
		GlobalEnv: []model.EnvVar{{Key: "PLAIN", Value: "s3cr3t", Secret: true}},
	}
	got := svc.collectMaskSecrets(context.Background(), &model.Pipeline{}, b, settings)
	all := strings.Join(got, "\n")
	if !strings.Contains(all, "hunter2") {
		t.Fatalf("password param named FOO must be masked: %v", got)
	}
	if !strings.Contains(all, "s3cr3t") {
		t.Fatalf("globally marked secret must be masked: %v", got)
	}
}

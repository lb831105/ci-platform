package service

import (
	"context"
	"io"
	"testing"

	"ci-platform/internal/executor"
	"ci-platform/internal/model"
)

type recordingGit struct {
	branch string
}

func (g *recordingGit) Clone(_ context.Context, _, branch, _ string, _ *executor.GitAuth, _ io.Writer) error {
	g.branch = branch
	return nil
}

// Webhook 的分支可能是推送事件里的分支；执行阶段必须使用构建记录里的快照，
// 否则“按推送分支构建”只会体现在记录里，实际仍 clone 任务配置分支。
func TestRunUsesBuildBranchSnapshot(t *testing.T) {
	svc := newDeleteTestService(t, t.TempDir())
	ctx := context.Background()
	if err := svc.pipelines.Create(ctx, &model.Pipeline{
		ID: "p1", Name: "demo", GitURL: "https://example.com/repo.git",
		GitBranch: "main", Steps: []model.Step{{Name: "build", Run: "echo ok"}},
	}); err != nil {
		t.Fatal(err)
	}
	b := &model.Build{
		PipelineID: "p1", Status: model.BuildPending, Branch: "refs/tags/v1.2.3",
		TriggerType: model.TriggerWebhook,
	}
	if err := svc.builds.CreateNext(ctx, b); err != nil {
		t.Fatal(err)
	}
	git := &recordingGit{}
	svc.git = git
	svc.runner = &captureRunner{}
	svc.Run(ctx, Job{PipelineID: "p1", BuildID: b.ID, WorkspaceDir: "demo"})
	if git.branch != "refs/tags/v1.2.3" {
		t.Fatalf("clone branch = %q, want build snapshot", git.branch)
	}
	got, err := svc.builds.Get(ctx, "p1", b.ID)
	if err != nil || got.Status != model.BuildSuccess {
		t.Fatalf("build = %+v, err=%v", got, err)
	}
}

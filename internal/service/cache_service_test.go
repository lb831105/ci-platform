package service

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"ci-platform/internal/executor"
	"ci-platform/internal/model"
)

type cacheTestRunner struct {
	onRun func(workspace string)
}

func (r *cacheTestRunner) RunSteps(_ context.Context, spec executor.RunSpec, _ io.Writer) (int, error) {
	r.onRun(spec.Workspace)
	return 0, nil
}

func TestCacheRestoresBetweenBuilds(t *testing.T) {
	svc := newDeleteTestService(t, t.TempDir())
	p := &model.Pipeline{
		ID: "p1", Name: "demo", WorkspaceDir: "demo",
		Steps:  []model.Step{{Name: "b", Run: "echo ok"}},
		Cache:  &model.CacheConfig{Enabled: true, Key: "node-${VERSION}", Paths: []string{"node_modules"}},
		Params: []model.ParamDef{{Name: "VERSION", Type: model.ParamString, Default: "20"}},
	}
	svc.pipelines = &memPipelineStore{items: map[string]*model.Pipeline{"p1": p}}

	first := true
	runner := &cacheTestRunner{onRun: func(workspace string) {
		if first {
			if err := os.MkdirAll(filepath.Join(workspace, "node_modules", "pkg"), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(workspace, "node_modules", "pkg", "index.js"), []byte("cached"), 0o644); err != nil {
				t.Fatal(err)
			}
			return
		}
		data, err := os.ReadFile(filepath.Join(workspace, "node_modules", "pkg", "index.js"))
		if err != nil || string(data) != "cached" {
			t.Fatalf("cache not restored: %q, %v", data, err)
		}
	}}
	svc.runner = runner
	b1 := &model.Build{ID: "1", PipelineID: "p1", Params: map[string]string{"VERSION": "20"}}
	if code, err := svc.executeStages(context.Background(), p, b1, Job{PipelineID: "p1", BuildID: "1"}, model.Settings{}, io.Discard); err != nil || code != 0 {
		t.Fatalf("first build = %d, %v", code, err)
	}
	// 模拟 clean workspace：删除工作区后第二次构建必须从缓存恢复。
	workspace := svc.workspace.Path(Job{PipelineID: "p1", BuildID: "1"})
	if err := os.RemoveAll(workspace); err != nil {
		t.Fatal(err)
	}
	first = false
	b2 := &model.Build{ID: "2", PipelineID: "p1", Params: map[string]string{"VERSION": "20"}}
	if code, err := svc.executeStages(context.Background(), p, b2, Job{PipelineID: "p1", BuildID: "2"}, model.Settings{}, io.Discard); err != nil || code != 0 {
		t.Fatalf("second build = %d, %v", code, err)
	}
	if _, err := os.Stat(svc.builds.CacheDir("p1", cacheStorageKey(p, b2))); err != nil {
		t.Fatalf("cache dir missing: %v", err)
	}
}

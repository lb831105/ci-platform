package store

import (
	"context"
	"testing"

	"ci-platform/internal/model"
)

// 阶段 1 验收：能在 data/jobs/ 下创建、读取、更新、删除 pipeline 目录与 JSON 文件。
func TestPipelineStoreCRUD(t *testing.T) {
	ctx := context.Background()
	s := NewPipelineStore(t.TempDir())

	p := &model.Pipeline{ID: "1703123456", Name: "java-demo", Image: "maven:3.9-eclipse-temurin-17"}
	p.Steps = []model.Step{{Name: "build", Run: "mvn -v"}}

	if err := s.Create(ctx, p); err != nil {
		t.Fatalf("create: %v", err)
	}
	if !s.Exists(p.ID) {
		t.Fatalf("job dir should exist after create")
	}

	got, err := s.Get(ctx, p.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != p.Name || len(got.Steps) != 1 {
		t.Fatalf("unexpected pipeline content: %+v", got)
	}

	got.Name = "renamed"
	if err = s.Update(ctx, got); err != nil {
		t.Fatalf("update: %v", err)
	}
	again, err := s.Get(ctx, p.ID)
	if err != nil {
		t.Fatalf("get after update: %v", err)
	}
	if again.Name != "renamed" {
		t.Fatalf("update not persisted, got %q", again.Name)
	}

	list, err := s.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 pipeline, got %d", len(list))
	}

	if err = s.Delete(ctx, p.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if s.Exists(p.ID) {
		t.Fatalf("job dir should be removed recursively")
	}
}

// 非法 id 必须被拒绝，避免路径穿越。
func TestPipelineStoreRejectsTraversal(t *testing.T) {
	s := NewPipelineStore(t.TempDir())
	if _, err := s.Get(context.Background(), "../etc"); err == nil {
		t.Fatalf("expected error for traversal id")
	}
}

// 阶段 1 验收：build_id 自增（对应 Jenkins #1、#2）且日志可追加与读取。
func TestBuildStoreSequenceAndLog(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	ps := NewPipelineStore(root)
	bs := NewBuildStore(root)

	if err := ps.Create(ctx, &model.Pipeline{ID: "100", Name: "demo", Image: "alpine"}); err != nil {
		t.Fatalf("seed pipeline: %v", err)
	}

	for i, want := range []string{"1", "2", "3"} {
		b := &model.Build{PipelineID: "100", Status: model.BuildPending, TriggerType: model.TriggerManual}
		if err := bs.CreateNext(ctx, b); err != nil {
			t.Fatalf("createNext #%d: %v", i+1, err)
		}
		if b.ID != want {
			t.Fatalf("build id = %q, want %q", b.ID, want)
		}
	}

	if err := bs.AppendLog(ctx, "100", "1", []byte("line-1\n")); err != nil {
		t.Fatalf("append log: %v", err)
	}
	if err := bs.AppendLog(ctx, "100", "1", []byte("line-2\n")); err != nil {
		t.Fatalf("append log: %v", err)
	}
	text, err := bs.ReadLog(ctx, "100", "1")
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	if text != "line-1\nline-2\n" {
		t.Fatalf("unexpected log content: %q", text)
	}

	history, err := bs.ListByPipeline(ctx, "100")
	if err != nil {
		t.Fatalf("list builds: %v", err)
	}
	if len(history) != 3 {
		t.Fatalf("expected 3 builds, got %d", len(history))
	}
	// 编号倒序：最新的排在最前
	if history[0].ID != "3" || history[2].ID != "1" {
		t.Fatalf("history should be descending by build id, got %s..%s", history[0].ID, history[2].ID)
	}
}

// 删除 pipeline 时其构建记录目录也应一并清空（由 PipelineStore 递归完成）。
func TestDeletePipelineRemovesBuilds(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	ps := NewPipelineStore(root)
	bs := NewBuildStore(root)

	if err := ps.Create(ctx, &model.Pipeline{ID: "200", Name: "demo", Image: "alpine"}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := bs.CreateNext(ctx, &model.Build{PipelineID: "200", Status: model.BuildPending}); err != nil {
		t.Fatalf("createNext: %v", err)
	}
	if err := ps.Delete(ctx, "200"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	history, err := bs.ListByPipeline(ctx, "200")
	if err != nil {
		t.Fatalf("list after delete: %v", err)
	}
	if len(history) != 0 {
		t.Fatalf("builds should be gone, got %d", len(history))
	}
}

// "." 会解析为 jobs 根目录，必须和 ".." 一样拒绝，否则 DELETE 会删掉全部任务。
func TestPipelineStoreRejectsDotID(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	s := NewPipelineStore(root)
	if err := s.Create(ctx, &model.Pipeline{ID: "1", Name: "demo"}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := s.Delete(ctx, "."); err == nil {
		t.Fatal("delete with id '.' must be rejected")
	}
	if !s.Exists("1") {
		t.Fatal("existing pipeline must survive rejected delete")
	}
}

func TestBuildStoreRejectsUnsafeIDs(t *testing.T) {
	ctx := context.Background()
	s := NewBuildStore(t.TempDir())
	for _, tc := range [][2]string{{"..", "1"}, {"1", ".."}, {".", "1"}, {"1", "."}} {
		if _, err := s.Get(ctx, tc[0], tc[1]); err == nil {
			t.Fatalf("Get(%q,%q) must be rejected", tc[0], tc[1])
		}
	}
}

func TestCredentialStoreRejectsTraversal(t *testing.T) {
	ctx := context.Background()
	s := NewCredentialStore(t.TempDir())
	if _, err := s.Get(ctx, "../settings"); err == nil {
		t.Fatal("credential traversal id must be rejected")
	}
}

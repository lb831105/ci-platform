package service

import (
	"context"
	"testing"
	"time"

	"ci-platform/internal/model"
	"ci-platform/internal/store"
)

func TestUpdateMetadataAndKeepForever(t *testing.T) {
	svc, _ := newRepublishService(t)
	builds := store.NewBuildStore(t.TempDir())
	svc.builds = builds
	ctx := context.Background()

	old := time.Now().UTC().Add(-48 * time.Hour)
	for _, id := range []string{"1", "2", "3"} {
		if err := builds.Create(ctx, &model.Build{
			ID: id, PipelineID: "p1", Status: model.BuildSuccess, StartedAt: &old,
		}); err != nil {
			t.Fatal(err)
		}
	}

	description := "  发布说明  "
	keep := true
	updated, err := svc.UpdateMetadata(ctx, "p1", "1", &description, &keep)
	if err != nil {
		t.Fatalf("update metadata: %v", err)
	}
	if updated.Description != "发布说明" || !updated.KeepForever {
		t.Fatalf("updated = %+v, want trimmed description and keep forever", updated)
	}

	// 保留策略只保留 1 条：未标记的 #2 删除，#1 因 KeepForever 必须留下，#3 是最新一条。
	svc.pruneBuilds(ctx, &model.Pipeline{ID: "p1"}, model.Settings{BuildRetentionMaxPerJob: 1})
	remaining, err := builds.ListByPipeline(ctx, "p1")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, b := range remaining {
		got[b.ID] = true
	}
	if !got["1"] || !got["3"] || got["2"] {
		t.Fatalf("remaining builds = %v, want #1 (kept forever) and #3 only", got)
	}

	// 清空描述与保留标记也必须生效（指针显式传入 false/空串）。
	empty := ""
	clear := false
	updated, err = svc.UpdateMetadata(ctx, "p1", "1", &empty, &clear)
	if err != nil {
		t.Fatalf("clear metadata: %v", err)
	}
	if updated.Description != "" || updated.KeepForever {
		t.Fatalf("cleared = %+v, want empty description and keep_forever=false", updated)
	}
}

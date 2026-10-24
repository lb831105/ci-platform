package service

import (
	"context"
	"path/filepath"
	"testing"

	"ci-platform/internal/model"
	"ci-platform/internal/store"
)

// memViewPipelineStore 视图测试用任务枚举。
type memViewPipelineStore struct{ names []string }

func (m *memViewPipelineStore) List(context.Context) ([]*model.Pipeline, error) {
	out := make([]*model.Pipeline, 0, len(m.names))
	for _, n := range m.names {
		out = append(out, &model.Pipeline{ID: n, Name: n})
	}
	return out, nil
}

// memViewPerm 按任务名前缀授予权限的假实现。
type memViewPerm struct{ allow map[string]bool }

func (m *memViewPerm) HasJobPermission(_, _, jobName string) bool { return m.allow[jobName] }

func newTestViewService(t *testing.T, names []string, allow map[string]bool) *ViewService {
	t.Helper()
	return NewViewService(
		store.NewViewStore(t.TempDir()),
		&memViewPipelineStore{names: names},
		&memViewPerm{allow: allow},
	)
}

func TestViewServiceCreateAndList(t *testing.T) {
	s := newTestViewService(t, []string{"a", "b"}, nil)
	in := &model.View{Name: "前端", Type: model.ViewTypeList, Jobs: []string{"a", " b ", "a"}}
	out, err := s.Create(context.Background(), in)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// 任务名去重去空白（" b " 去空白后是独立任务 b）。
	if len(out.Jobs) != 2 || out.Jobs[0] != "a" || out.Jobs[1] != "b" {
		t.Fatalf("jobs = %v, want [a b]", out.Jobs)
	}
	if out.Columns == nil {
		t.Fatal("columns should default to all")
	}
	if got := s.List(); len(got) != 1 || got[0].Name != "前端" {
		t.Fatalf("list = %+v", got)
	}
	// 名称重复。
	if _, err := s.Create(context.Background(), &model.View{Name: "前端"}); err == nil {
		t.Fatal("duplicate name should fail")
	}
}

func TestViewServiceValidation(t *testing.T) {
	s := newTestViewService(t, []string{"a"}, nil)
	cases := []model.View{
		{Name: "  "},
		{Name: "x/y"},
		{Name: "ok", Type: "agent"},
		{Name: "ok", JobRegex: "(["},            // 非法正则
		{Name: "ok", Columns: []string{"nope"}}, // 非法列
	}
	for i, v := range cases {
		if _, err := s.Create(context.Background(), &v); err == nil {
			t.Fatalf("case %d should fail, got nil", i)
		}
	}
}

func TestViewServiceJobNamesListAndRegex(t *testing.T) {
	// 全部任务对 admin 可读。
	allow := map[string]bool{"as-doc": true, "as-doc-admin": true, "pan-insur": true}
	// gone-job 只出现在视图里（模拟已删除的任务），不在任务枚举中。
	s := newTestViewService(t, []string{"as-doc", "as-doc-admin", "pan-insur"}, allow)
	v, err := s.Create(context.Background(), &model.View{
		Name: "as", Type: model.ViewTypeList,
		Jobs:     []string{"as-doc", "gone-job"},
		JobRegex: "^as-doc",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	names, err := s.JobNames(context.Background(), v, "admin")
	if err != nil {
		t.Fatalf("jobnames: %v", err)
	}
	// gone-job 不存在被剔除；正则命中 as-doc-admin；已删除/不存在的任务被忽略。
	if len(names) != 2 || names[0] != "as-doc" || names[1] != "as-doc-admin" {
		t.Fatalf("names = %v, want [as-doc as-doc-admin]", names)
	}
}

func TestViewServiceJobNamesMyViewAndPermFilter(t *testing.T) {
	// 仅 as-doc 对 dev1 可读。
	s := newTestViewService(t, []string{"as-doc", "pan-insur"},
		map[string]bool{"as-doc": true, "pan-insur": false})
	v, err := s.Create(context.Background(), &model.View{Name: "我的视图", Type: model.ViewTypeMy})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	names, err := s.JobNames(context.Background(), v, "dev1")
	if err != nil {
		t.Fatalf("jobnames: %v", err)
	}
	if len(names) != 1 || names[0] != "as-doc" {
		t.Fatalf("my view names = %v, want [as-doc]", names)
	}

	// 列表视图同样按 job.read 过滤未授权任务。
	v2, err := s.Create(context.Background(), &model.View{
		Name: "all", Type: model.ViewTypeList, Jobs: []string{"as-doc", "pan-insur"},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	names2, err := s.JobNames(context.Background(), v2, "dev1")
	if err != nil {
		t.Fatalf("jobnames: %v", err)
	}
	if len(names2) != 1 || names2[0] != "as-doc" {
		t.Fatalf("list view names = %v, want [as-doc]", names2)
	}
}

func TestViewServiceUpdateAndDelete(t *testing.T) {
	dir := t.TempDir()
	s := NewViewService(store.NewViewStore(filepath.Join(dir, "data")),
		&memViewPipelineStore{names: []string{"a"}}, nil)
	if _, err := s.Create(context.Background(), &model.View{
		Name: "v", Type: model.ViewTypeList, Jobs: []string{"a"},
	}); err != nil {
		t.Fatalf("create: %v", err)
	}
	// 名称不可改；请求体中的 name 被忽略。
	out, err := s.Update(context.Background(), "v", &model.View{Name: "other", Description: "d"})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if out.Name != "v" || out.Description != "d" {
		t.Fatalf("updated = %+v", out)
	}
	if _, err := s.Update(context.Background(), "nope", &model.View{Name: "x"}); err == nil {
		t.Fatal("update missing view should fail")
	}
	if err := s.Delete("v"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := s.Delete("v"); err == nil {
		t.Fatal("double delete should fail")
	}
	if n := len(s.List()); n != 0 {
		t.Fatalf("list len = %d, want 0", n)
	}
}

func TestViewServiceGet(t *testing.T) {
	s := newTestViewService(t, []string{"a"}, nil)
	if _, err := s.Get("nope"); err == nil {
		t.Fatal("get missing view should fail")
	}
	if _, err := s.Create(context.Background(), &model.View{Name: "v"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	v, err := s.Get("v")
	if err != nil || v.Name != "v" {
		t.Fatalf("get = %+v, err = %v", v, err)
	}
}

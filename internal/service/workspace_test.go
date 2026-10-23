package service

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSafeWorkspaceName(t *testing.T) {
	cases := map[string]string{
		"my/project": "my_project",
		"  hello  ":  "hello",
		"CON":        "_CON",
		"a:b*c?":     "a_b_c_",
		"我的项目":       "我的项目",
		"   ":        "",
		"..":         "",
	}
	for in, want := range cases {
		if got := safeWorkspaceName(in); got != want {
			t.Fatalf("safeWorkspaceName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestWorkspacePathUsesPipelineName(t *testing.T) {
	m := NewWorkspaceManager(filepath.Join("root"))
	if got := m.Path(Job{PipelineID: "42", WorkspaceDir: "my-project"}); got != filepath.Join("root", "my-project") {
		t.Fatalf("Path = %q, want root/my-project", got)
	}
	// 兼容旧 Job：没有 WorkspaceDir 时退回任务 ID。
	if got := m.Path(Job{PipelineID: "42"}); got != filepath.Join("root", "42") {
		t.Fatalf("legacy Path = %q, want root/42", got)
	}
}

func TestWorkspaceManagerRename(t *testing.T) {
	root := t.TempDir()
	m := NewWorkspaceManager(root)
	oldDir := filepath.Join(root, "old-name")
	if err := os.MkdirAll(oldDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(oldDir, "artifact.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := m.Rename("old-name", "new-name"); err != nil {
		t.Fatalf("rename: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "new-name", "artifact.txt")); err != nil {
		t.Fatalf("artifact missing after rename: %v", err)
	}
	if _, err := os.Stat(oldDir); !os.IsNotExist(err) {
		t.Fatalf("old dir should be gone, err = %v", err)
	}
}

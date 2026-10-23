package executor

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// 复用工作区场景：dest 已是仓库时必须 fetch + reset 更新，而不是报“目录非空”。
func TestGitCloneReuseUpdatesWorkspace(t *testing.T) {
	ctx := context.Background()
	src := t.TempDir()
	repo, err := git.PlainInit(src, false)
	if err != nil {
		t.Fatalf("init src repo: %v", err)
	}
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatalf("worktree: %v", err)
	}
	// 空仓库需先把 HEAD 指向 main，否则无法在 main 上提交。
	if err = repo.Storer.SetReference(plumbing.NewSymbolicReference(plumbing.HEAD, plumbing.NewBranchReferenceName("main"))); err != nil {
		t.Fatalf("set HEAD to main: %v", err)
	}
	commit := func(content, msg string) {
		t.Helper()
		if writeErr := os.WriteFile(filepath.Join(src, "hello.txt"), []byte(content), 0o644); writeErr != nil {
			t.Fatalf("write src: %v", writeErr)
		}
		if _, addErr := wt.Add("hello.txt"); addErr != nil {
			t.Fatalf("add: %v", addErr)
		}
		if _, commitErr := wt.Commit(msg, &git.CommitOptions{
			Author: &object.Signature{Name: "tester", Email: "tester@example.com", When: time.Now()},
		}); commitErr != nil {
			t.Fatalf("commit: %v", commitErr)
		}
	}
	commit("v1", "first")

	client := NewGitClient()
	dest := filepath.Join(t.TempDir(), "workspace")
	var out bytes.Buffer
	if err = client.Clone(ctx, src, "main", dest, nil, &out); err != nil {
		t.Fatalf("initial clone: %v\n%s", err, out.String())
	}
	if got := readFile(t, filepath.Join(dest, "hello.txt")); got != "v1" {
		t.Fatalf("initial content = %q, want v1", got)
	}

	commit("v2", "second")
	out.Reset()
	if err = client.Clone(ctx, src, "main", dest, nil, &out); err != nil {
		t.Fatalf("reuse clone: %v\n%s", err, out.String())
	}
	if got := readFile(t, filepath.Join(dest, "hello.txt")); got != "v2" {
		t.Fatalf("updated content = %q, want v2\nlog:\n%s", got, out.String())
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

// 远端没有新提交时不应重写工作区；未跟踪的构建产物必须保留。
func TestGitCloneReuseSkipsUnchangedCheckout(t *testing.T) {
	ctx := context.Background()
	src := t.TempDir()
	repo, err := git.PlainInit(src, false)
	if err != nil {
		t.Fatalf("init src repo: %v", err)
	}
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatalf("worktree: %v", err)
	}
	if err = repo.Storer.SetReference(plumbing.NewSymbolicReference(plumbing.HEAD, plumbing.NewBranchReferenceName("main"))); err != nil {
		t.Fatalf("set HEAD to main: %v", err)
	}
	if err = os.WriteFile(filepath.Join(src, ".gitignore"), []byte("dist/\nnode_modules/\n"), 0o644); err != nil {
		t.Fatalf("write .gitignore: %v", err)
	}
	if err = os.WriteFile(filepath.Join(src, "hello.txt"), []byte("v1"), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	if _, err = wt.Add(".gitignore"); err != nil {
		t.Fatalf("add .gitignore: %v", err)
	}
	if _, err = wt.Add("hello.txt"); err != nil {
		t.Fatalf("add: %v", err)
	}
	if _, err = wt.Commit("first", &git.CommitOptions{
		Author: &object.Signature{Name: "tester", Email: "tester@example.com", When: time.Now()},
	}); err != nil {
		t.Fatalf("commit: %v", err)
	}

	client := NewGitClient()
	dest := filepath.Join(t.TempDir(), "workspace")
	var out bytes.Buffer
	if err = client.Clone(ctx, src, "main", dest, nil, &out); err != nil {
		t.Fatalf("initial clone: %v\n%s", err, out.String())
	}

	// 构建产物：未跟踪文件，不应阻止跳过 reset。
	artifact := filepath.Join(dest, "dist", "app.bin")
	if err = os.MkdirAll(filepath.Dir(artifact), 0o755); err != nil {
		t.Fatalf("mkdir artifact: %v", err)
	}
	if err = os.WriteFile(artifact, []byte("artifact"), 0o644); err != nil {
		t.Fatalf("write artifact: %v", err)
	}
	nodeArtifact := filepath.Join(dest, "node_modules", "pkg", "index.js")
	if err = os.MkdirAll(filepath.Dir(nodeArtifact), 0o755); err != nil {
		t.Fatalf("mkdir node artifact: %v", err)
	}
	if err = os.WriteFile(nodeArtifact, []byte("module"), 0o644); err != nil {
		t.Fatalf("write node artifact: %v", err)
	}

	out.Reset()
	if err = client.Clone(ctx, src, "main", dest, nil, &out); err != nil {
		t.Fatalf("reuse unchanged: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "already up to date") {
		t.Fatalf("expected already-up-to-date log, got:\n%s", out.String())
	}
	if got := readFile(t, filepath.Join(dest, "hello.txt")); got != "v1" {
		t.Fatalf("content = %q, want v1", got)
	}
	if got := readFile(t, artifact); got != "artifact" {
		t.Fatalf("artifact = %q, want artifact", got)
	}
	if got := readFile(t, nodeArtifact); got != "module" {
		t.Fatalf("node artifact = %q, want module", got)
	}

	// 受版本控制的文件被本地修改时应 hard reset 回远端。
	if err = os.WriteFile(filepath.Join(dest, "hello.txt"), []byte("dirty"), 0o644); err != nil {
		t.Fatalf("dirty tracked: %v", err)
	}
	out.Reset()
	if err = client.Clone(ctx, src, "main", dest, nil, &out); err != nil {
		t.Fatalf("reuse dirty: %v\n%s", err, out.String())
	}
	if got := readFile(t, filepath.Join(dest, "hello.txt")); got != "v1" {
		t.Fatalf("dirty content = %q, want v1\n%s", got, out.String())
	}
	if got := readFile(t, artifact); got != "artifact" {
		t.Fatalf("artifact after reset = %q, want artifact", got)
	}
	if got := readFile(t, nodeArtifact); got != "module" {
		t.Fatalf("node artifact after reset = %q, want module", got)
	}

	// 远端有新提交时仍应保留缓存/产物，只更新跟踪文件。
	if err = os.WriteFile(filepath.Join(src, "hello.txt"), []byte("v2"), 0o644); err != nil {
		t.Fatalf("write v2: %v", err)
	}
	if _, err = wt.Add("hello.txt"); err != nil {
		t.Fatalf("add v2: %v", err)
	}
	if _, err = wt.Commit("second", &git.CommitOptions{
		Author: &object.Signature{Name: "tester", Email: "tester@example.com", When: time.Now()},
	}); err != nil {
		t.Fatalf("commit v2: %v", err)
	}
	out.Reset()
	if err = client.Clone(ctx, src, "main", dest, nil, &out); err != nil {
		t.Fatalf("reuse v2: %v\n%s", err, out.String())
	}
	if got := readFile(t, filepath.Join(dest, "hello.txt")); got != "v2" {
		t.Fatalf("content after v2 = %q, want v2\n%s", got, out.String())
	}
	if got := readFile(t, artifact); got != "artifact" {
		t.Fatalf("artifact after v2 = %q, want artifact\n%s", got, out.String())
	}
	if got := readFile(t, nodeArtifact); got != "module" {
		t.Fatalf("node artifact after v2 = %q, want module\n%s", got, out.String())
	}
}

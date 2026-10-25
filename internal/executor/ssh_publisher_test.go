package executor

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"ci-platform/internal/model"
)

// 阶段 3 验收相关：remove_prefix 必须能正确剥离待上传路径前缀。
func TestApplyRemovePrefix(t *testing.T) {
	cases := []struct {
		rel    string
		prefix string
		want   string
	}{
		{"target/app.jar", "target/", "app.jar"},
		{"target/app.jar", "target", "app.jar"},
		{"target/nested/app.jar", "target/", "nested/app.jar"},
		{"dist/app.jar", "target/", "dist/app.jar"},
		{"app.jar", "", "app.jar"},
		{"target", "target/", "target"},
	}
	for _, c := range cases {
		if got := applyRemovePrefix(c.rel, c.prefix); got != c.want {
			t.Errorf("applyRemovePrefix(%q, %q) = %q, want %q", c.rel, c.prefix, got, c.want)
		}
	}
}

// 阶段 3 验收：glob 能匹配产物文件，且自动过滤目录。
func TestMatchFiles(t *testing.T) {
	workspace := t.TempDir()
	target := filepath.Join(workspace, "target")
	if err := os.MkdirAll(filepath.Join(target, "classes"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	for _, name := range []string{"app.jar", "app-sources.jar"} {
		if err := os.WriteFile(filepath.Join(target, name), []byte("jar"), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	files, err := matchFiles(workspace, []string{"target/*.jar"})
	if err != nil {
		t.Fatalf("matchFiles: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("expected 2 jar files, got %d (%v)", len(files), files)
	}

	// 目录必须被过滤掉
	dirs, err := matchFiles(workspace, []string{"target/*"})
	if err != nil {
		t.Fatalf("matchFiles: %v", err)
	}
	if len(dirs) != 2 {
		t.Fatalf("directories must be filtered out, got %d (%v)", len(dirs), dirs)
	}

	if _, err = matchFiles(workspace, nil); err == nil {
		t.Fatalf("empty source_files must be rejected")
	}
}

// ** 递归通配与目录模式必须能匹配到全部产物（对齐 Publish Over SSH 语义）。
func TestMatchFilesRecursive(t *testing.T) {
	workspace := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workspace, "dist", "assets"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	for _, p := range []string{"dist/index.html", "dist/assets/app.js", "other.txt"} {
		if err := os.WriteFile(filepath.Join(workspace, filepath.FromSlash(p)), []byte("x"), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	cases := []struct {
		pattern string
		want    int
	}{
		{"dist/**", 2},
		{"dist/", 2},
		{"**/*.js", 1},
		{"**/*.html", 1},
		{"other.txt", 1},
	}
	for _, c := range cases {
		files, err := matchFiles(workspace, []string{c.pattern})
		if err != nil {
			t.Fatalf("matchFiles(%q): %v", c.pattern, err)
		}
		if len(files) != c.want {
			t.Errorf("matchFiles(%q) = %d files (%v), want %d", c.pattern, len(files), files, c.want)
		}
	}
}

// 相对路径必须统一为斜杠，且不逃逸出工作区。
func TestRelativeTo(t *testing.T) {
	if got := relativeTo("/w", "/w/target/app.jar"); got != "target/app.jar" {
		t.Fatalf("relativeTo = %q", got)
	}
	if got := relativeTo("/w", "/elsewhere/app.jar"); got != "app.jar" {
		t.Fatalf("outside workspace should fall back to base name, got %q", got)
	}
}

// ssh_publish.enabled=false 时不得发起任何连接（阶段 5.4 约束）。
func TestPublishDisabledIsNoop(t *testing.T) {
	p := NewSshPublisher()
	if err := p.Publish(context.Background(), nil, t.TempDir(), os.Stdout); err != nil {
		t.Fatalf("nil config should be noop: %v", err)
	}
}

// 剥离前缀必须按完整路径段匹配，不能把 targetx/ 误当成 target/。
func TestApplyRemovePrefixSegmentAware(t *testing.T) {
	if got := applyRemovePrefix("targetx/app.jar", "target"); got != "targetx/app.jar" {
		t.Fatalf("prefix must match whole segment, got %q", got)
	}
}

// source_files 不得通过 ../ 或绝对路径逃逸出工作区（防止上传服务器任意文件）。
func TestMatchFilesRejectsTraversal(t *testing.T) {
	workspace := t.TempDir()
	outsideDir := filepath.Dir(workspace)
	if err := os.WriteFile(filepath.Join(outsideDir, "secret.txt"), []byte("secret"), 0o644); err != nil {
		t.Fatalf("write outside: %v", err)
	}
	for _, pattern := range []string{"../secret.txt", "../../etc/passwd", "/etc/passwd", "..", "../"} {
		if _, err := matchFiles(workspace, []string{pattern}); err == nil {
			t.Errorf("pattern %q should be rejected (path traversal)", pattern)
		}
	}
}

func TestSSHHostKeyCallback(t *testing.T) {
	// 未配置：保持兼容（不校验），但回调可用。
	callback, err := sshHostKeyCallback("")
	if err != nil || callback == nil {
		t.Fatalf("empty known_hosts should fall back to permissive callback: %v", err)
	}
	// 配置了不存在的文件：构建应失败，而不是静默跳过校验。
	if _, err = sshHostKeyCallback(filepath.Join(t.TempDir(), "missing_known_hosts")); err == nil {
		t.Fatal("missing known_hosts file must fail")
	}
}

func TestExpandHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no user home")
	}
	if got := expandHome("~/foo"); got != filepath.Join(home, "foo") {
		t.Fatalf("expandHome = %q", got)
	}
	if got := expandHome("/abs/path"); got != "/abs/path" {
		t.Fatalf("expandHome = %q", got)
	}
}

func TestPublishArtifactsDisabledIsNoop(t *testing.T) {
	// enabled=false：不发起任何连接，也不读产物目录。
	if err := NewSshPublisher().PublishArtifacts(context.Background(), &model.SshPublishConfig{Enabled: false},
		t.TempDir(), &bytes.Buffer{}); err != nil {
		t.Fatalf("disabled publish must be a noop: %v", err)
	}
}

// flatten 只保留文件名（对齐 Jenkins Flatten files），关闭时保持相对路径。
func TestApplyFlatten(t *testing.T) {
	cases := []struct {
		rel     string
		flatten bool
		want    string
	}{
		{"dist/assets/app.js", false, "dist/assets/app.js"},
		{"dist/assets/app.js", true, "app.js"},
		{"app", true, "app"},
		{"target/", true, "target"}, // 目录项在 matchFiles 阶段会被过滤，这里只验证函数本身
	}
	for _, tc := range cases {
		if got := applyFlatten(tc.rel, tc.flatten); got != tc.want {
			t.Fatalf("applyFlatten(%q, %v) = %q, want %q", tc.rel, tc.flatten, got, tc.want)
		}
	}
}

package executor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 目录名写错时（ruoyi-adm/...）：建议必须指向**真实存在且能命中**的路径。
//
// 关键点：不能建议 **/<原模式> —— 匹配器已经自动试过它，失败说明它也匹配不到。
func TestNoMatchSuggestionPointsAtRealFile(t *testing.T) {
	workspace := t.TempDir()
	jarDir := filepath.Join(workspace, "ruoyi-admin", "target")
	if err := os.MkdirAll(jarDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(jarDir, "ruoyi-admin.jar"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := newNoMatchError(workspace, []string{"ruoyi-adm/target/*.jar"})
	if !strings.Contains(err.Error(), "ruoyi-admin/target/ruoyi-admin.jar") {
		t.Fatalf("hint must list the similar file: %s", err.Error())
	}
	if !err.RecursiveTried || !strings.Contains(err.Error(), "递归匹配") {
		t.Fatalf("hint must admit recursive matching was already tried: %s", err.Error())
	}
	if len(err.Suggested) != 1 || err.Suggested[0] != "**/target/*.jar" {
		t.Fatalf("suggested = %v, want [**/target/*.jar]", err.Suggested)
	}
	// 建议模式必须真的能归档到文件（否则"一键修正"是个死按钮）
	files, _, matchErr := matchWorkspaceFilesDetailed(workspace, err.Suggested, "artifact")
	if matchErr != nil {
		t.Fatalf("suggested pattern must be valid: %v", matchErr)
	}
	if len(files) != 1 || !strings.HasSuffix(files[0], "ruoyi-admin.jar") {
		t.Fatalf("suggested pattern should match the jar, got %v", files)
	}
}

// 工作区里压根没有同名文件 → 提示"确认步骤是否产出"，不硬给模式建议。
func TestNoMatchHintWithoutCandidates(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "README.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := newNoMatchError(workspace, []string{"target/*.jar"})
	if len(err.Suggested) != 0 {
		t.Fatalf("must not suggest a pattern when nothing similar exists: %v", err.Suggested)
	}
	if !strings.Contains(err.Error(), "构建步骤") {
		t.Fatalf("must ask the user to verify the build steps: %s", err.Error())
	}
}

// 根目录有匹配的情况由匹配器自己处理（自动兜底），不该再走"没匹配到"的错误路径。
func TestNestedTargetDoesNotProduceNoMatchError(t *testing.T) {
	workspace := t.TempDir()
	jarDir := filepath.Join(workspace, "ruoyi-admin", "target")
	if err := os.MkdirAll(jarDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(jarDir, "ruoyi-admin.jar"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	files, notes, err := matchWorkspaceFilesDetailed(workspace, []string{"target/*.jar"}, "artifact")
	if err != nil || len(files) != 1 {
		t.Fatalf("nested jar must be archived: %v %v", files, err)
	}
	if len(notes) != 1 {
		t.Fatalf("fallback must be logged: %v", notes)
	}
}

// allow_empty：没匹配到只告警、返回 nil 错误（构建继续成功）。
func TestCollectAllowEmptyDoesNotFail(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "pom.xml"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	buildDir := t.TempDir()
	var out strings.Builder
	files, err := NewArtifactCollector().Collect(context.Background(), workspace, buildDir,
		[]string{"target/*.zip"}, ArtifactLimits{}, true, &out)
	if err != nil {
		t.Fatalf("allowEmpty must not fail: %v", err)
	}
	if len(files) != 0 {
		t.Fatalf("expected no files, got %v", files)
	}
	if !strings.Contains(out.String(), "允许无产物") {
		t.Fatalf("must log that empty is allowed, got:\n%s", out.String())
	}

	// 关闭 allowEmpty 时仍然失败，但错误里带建议
	_, err = NewArtifactCollector().Collect(context.Background(), workspace, buildDir,
		[]string{"target/*.zip"}, ArtifactLimits{}, false, &strings.Builder{})
	if err == nil {
		t.Fatal("allowEmpty=false must still fail")
	}
	if !strings.Contains(err.Error(), "没有匹配到任何文件") {
		t.Fatalf("error must be actionable: %v", err)
	}
}

// 常用 glob 语义（与 Jenkins 一致）：* 不跨目录，** 跨任意层级。
func TestGlobSemantics(t *testing.T) {
	cases := []struct {
		pattern  string
		path     string
		expected bool
	}{
		{"target/*.jar", "target/app.jar", true},
		{"target/*.jar", "ruoyi-admin/target/app.jar", false},
		{"**/target/*.jar", "ruoyi-admin/target/app.jar", true},
		{"**/target/*.jar", "a/b/target/app.jar", true},
		{"**/*.jar", "app.jar", true},
		{"**/*.jar", "a/b/app.jar", true},
		{"*.jar", "a/app.jar", false},
		{"dist/**", "dist/js/app.js", true},
	}
	for _, c := range cases {
		re, err := globToRegexp(c.pattern)
		if err != nil {
			t.Fatalf("compile %q: %v", c.pattern, err)
		}
		if got := re.MatchString(c.path); got != c.expected {
			t.Fatalf("%q vs %q = %v, want %v", c.pattern, c.path, got, c.expected)
		}
	}
}

// 结构化错误：service 需要拿到 patterns/suggested/files 存进 build.json。
func TestNoMatchErrorExposesStructuredFields(t *testing.T) {
	workspace := t.TempDir()
	dir := filepath.Join(workspace, "ruoyi-admin", "target")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ruoyi-admin.jar"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	var out strings.Builder
	// 用真正不存在的类型（jar 存在、zip 不存在），才会走到"没匹配到"分支
	_, err := NewArtifactCollector().Collect(context.Background(), workspace, t.TempDir(),
		[]string{"target/*.zip"}, ArtifactLimits{}, false, &out)
	if err == nil {
		t.Fatal("expected no-match error")
	}

	// errors.As 能取出模式、建议与找到的文件（供前端"一键修正"）
	var noMatch *NoMatchError
	if !errors.As(err, &noMatch) {
		t.Fatalf("error must be *NoMatchError, got %T", err)
	}
	if len(noMatch.Patterns) != 1 || noMatch.Patterns[0] != "target/*.zip" {
		t.Fatalf("patterns = %v", noMatch.Patterns)
	}
	// 没有同名文件时不建议模式（一键改也不会有产物）
	if len(noMatch.Suggested) != 0 {
		t.Fatalf("suggested should be empty when nothing similar, got %v", noMatch.Suggested)
	}
	// jar 存在但模式是 *.zip：没有同名文件 → Files 为空，文案转为"确认步骤是否产出"
	if len(noMatch.Files) != 0 {
		t.Fatalf("files should be empty for *.zip, got %v", noMatch.Files)
	}
	// 人读文案仍需完整（构建日志靠它）
	for _, want := range []string{"没有匹配到任何文件", "构建步骤"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error text missing %q:\n%s", want, err.Error())
		}
	}
}

// 多模块工程：jar 只存在于 ruoyi-admin/target/ 下，target/*.jar 也要能匹配到
// （这是用户实际报的问题：Maven BUILD SUCCESS，归档却判定"没匹配到"）。
func TestMatchFallsBackToRecursiveForNestedTarget(t *testing.T) {
	workspace := t.TempDir()
	nested := filepath.Join(workspace, "ruoyi-admin", "target")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, "ruoyi-admin.jar"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	files, notes, err := matchWorkspaceFilesDetailed(workspace, []string{"target/*.jar"}, "artifact")
	if err != nil {
		t.Fatalf("match: %v", err)
	}
	if len(files) != 1 || filepath.Base(files[0]) != "ruoyi-admin.jar" {
		t.Fatalf("nested jar must be matched, got %v", files)
	}
	if len(notes) != 1 || !strings.Contains(notes[0], "**/target/*.jar") {
		t.Fatalf("fallback must be explained in the log, got %v", notes)
	}
}

// 根目录有匹配时优先用根目录结果（保持可预期，不被子模块的 jar 混进来）。
func TestMatchPrefersRootOverRecursive(t *testing.T) {
	workspace := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workspace, "target"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "target", "app.jar"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(workspace, "module", "target")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, "other.jar"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	files, notes, err := matchWorkspaceFilesDetailed(workspace, []string{"target/*.jar"}, "artifact")
	if err != nil {
		t.Fatalf("match: %v", err)
	}
	if len(files) != 1 || filepath.Base(files[0]) != "app.jar" {
		t.Fatalf("root match must win, got %v", files)
	}
	if len(notes) != 0 {
		t.Fatalf("no fallback note expected, got %v", notes)
	}
}

// 显式写了 ** 就不再做回退（避免把用户明确的意图改掉）。
func TestMatchRecursivePatternHasNoFallbackNote(t *testing.T) {
	workspace := t.TempDir()
	nested := filepath.Join(workspace, "a", "target")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, "a.jar"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	files, notes, err := matchWorkspaceFilesDetailed(workspace, []string{"**/target/*.jar"}, "artifact")
	if err != nil || len(files) != 1 {
		t.Fatalf("recursive pattern should match: %v %v", files, err)
	}
	if len(notes) != 0 {
		t.Fatalf("explicit ** must not produce fallback note: %v", notes)
	}
}

// 找到的文件分散在不同目录名/不同扩展名时，无法给一条通用建议 → 按目录分别建议。
func TestSuggestedPatternsPerDirectoryWhenNoCommonSuffix(t *testing.T) {
	workspace := t.TempDir()
	paths := []string{
		filepath.Join(workspace, "module-a", "build", "a.jar"),
		filepath.Join(workspace, "module-b", "out", "b.jar"),
	}
	for _, p := range paths {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	err := newNoMatchError(workspace, []string{"dist/*.jar"})
	if len(err.Suggested) != 2 {
		t.Fatalf("suggested = %v, want two directory patterns", err.Suggested)
	}
	for _, candidate := range err.Suggested {
		files, _, matchErr := matchWorkspaceFilesDetailed(workspace, []string{candidate}, "artifact")
		if matchErr != nil || len(files) != 1 {
			t.Fatalf("suggestion %q must match exactly one file, got %v (%v)", candidate, files, matchErr)
		}
	}
}

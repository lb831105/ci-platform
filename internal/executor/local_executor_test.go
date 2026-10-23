package executor

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"ci-platform/internal/model"
)

// 本地执行器在服务器上直接运行步骤，并返回真实退出码。
func TestLocalExecutorRunsSteps(t *testing.T) {
	dir := t.TempDir()
	var out bytes.Buffer
	exec := NewLocalExecutor()

	code, err := exec.RunSteps(context.Background(), RunSpec{
		Workspace: dir,
		Steps:     []model.Step{{Name: "hello", Run: "echo hello-local"}},
		Env:       map[string]string{"CI": "true"},
	}, &out)
	if err != nil {
		t.Fatalf("RunSteps error: %v\n%s", err, out.String())
	}
	if code != 0 {
		t.Fatalf("exit code = %d, want 0\n%s", code, out.String())
	}
	if !strings.Contains(out.String(), "hello-local") {
		t.Fatalf("output missing step output:\n%s", out.String())
	}
}

func TestLocalExecutorNonZeroExit(t *testing.T) {
	var out bytes.Buffer
	code, err := NewLocalExecutor().RunSteps(context.Background(), RunSpec{
		Workspace: t.TempDir(),
		Steps:     []model.Step{{Name: "fail", Run: "exit 3"}},
	}, &out)
	if err != nil {
		t.Fatalf("RunSteps error: %v", err)
	}
	if code != 3 {
		t.Fatalf("exit code = %d, want 3\n%s", code, out.String())
	}
}

// 额外环境变量应覆盖基础环境（大小写不敏感，兼容 Windows）。
func TestMergeEnvOverride(t *testing.T) {
	got := mergeEnv([]string{"Path=/base", "FOO=1"}, map[string]string{"PATH": "/override", "BAR": "2"})
	values := make(map[string]string, len(got))
	for _, kv := range got {
		key, value, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		values[strings.ToUpper(key)] = value
	}
	if values["PATH"] != "/override" {
		t.Fatalf("PATH = %q, want /override", values["PATH"])
	}
	if values["FOO"] != "1" || values["BAR"] != "2" {
		t.Fatalf("unexpected merged env: %v", values)
	}
}

// Windows cmd 曾因命令行转义把引号变成 \"，脚本方式必须原样保留。
func TestLocalExecutorPreservesQuotes(t *testing.T) {
	var out bytes.Buffer
	code, err := NewLocalExecutor().RunSteps(context.Background(), RunSpec{
		Workspace: t.TempDir(),
		Steps:     []model.Step{{Name: "quoted", Run: `echo "hello quoted"`}},
	}, &out)
	if err != nil {
		t.Fatalf("RunSteps error: %v\n%s", err, out.String())
	}
	if code != 0 {
		t.Fatalf("exit code = %d, want 0\n%s", code, out.String())
	}
	if !strings.Contains(out.String(), "hello quoted") {
		t.Fatalf("output missing quoted text:\n%s", out.String())
	}
	if strings.Contains(out.String(), `\"`) {
		t.Fatalf("quotes were escaped by the shell wrapper:\n%s", out.String())
	}
}

// Windows 批处理调用另一个 .cmd（如 npm.cmd / mvn.cmd）后必须能继续执行 || / &&。
func TestLocalExecutorCmdChaining(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("cmd.exe only")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "fake.cmd"), []byte("@echo off\r\nexit /b 1\r\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	code, err := NewLocalExecutor().RunSteps(context.Background(), RunSpec{
		Workspace: dir,
		Steps:     []model.Step{{Name: "chain", Run: `fake.cmd || echo fallback-ran`}},
	}, &out)
	if err != nil {
		t.Fatalf("RunSteps error: %v\n%s", err, out.String())
	}
	if code != 0 {
		t.Fatalf("exit code = %d, want 0\n%s", code, out.String())
	}
	if !strings.Contains(out.String(), "fallback-ran") {
		t.Fatalf("fallback not executed:\n%s", out.String())
	}
}

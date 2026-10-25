package executor

import (
	"bytes"
	"context"
	"runtime"
	"strings"
	"testing"
	"time"

	"ci-platform/internal/model"
)

// failCmd 返回「立即以非零码退出」的命令（cmd 与 sh 通用）。
func failCmd() string { return "exit 3" }

// echoCmd 返回输出指定文本的命令。
func echoCmd(text string) string { return "echo " + text }

// sleepCmd 返回睡眠指定秒数的命令；sleep 需要能被超时杀掉（子进程）。
func sleepCmd(sec int) string {
	if runtime.GOOS == "windows" {
		return "ping -n " + itoa(sec+1) + " 127.0.0.1 > NUL"
	}
	return "sleep " + itoa(sec)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

// 重试：失败后按 retry 次数再试，日志里能看到尝试次数。
func TestStepRetry(t *testing.T) {
	var out bytes.Buffer
	code, err := NewLocalExecutor().RunSteps(context.Background(), RunSpec{
		Workspace: t.TempDir(),
		Steps: []model.Step{{
			Name: "flaky", Run: failCmd(), Retry: 2,
		}},
	}, &out)
	if err != nil {
		t.Fatalf("RunSteps error: %v", err)
	}
	if code != 3 {
		t.Fatalf("exit code = %d, want 3\n%s", code, out.String())
	}
	if got := strings.Count(out.String(), "次尝试"); got != 3 {
		t.Fatalf("attempt banners = %d, want 3\n%s", got, out.String())
	}
	if got := strings.Count(out.String(), "重试 flaky"); got != 2 {
		t.Fatalf("retry lines = %d, want 2\n%s", got, out.String())
	}
}

// 超时：步骤被强杀，退出码 124，且不会等到命令自然结束。
func TestStepTimeout(t *testing.T) {
	var out bytes.Buffer
	started := time.Now()
	code, err := NewLocalExecutor().RunSteps(context.Background(), RunSpec{
		Workspace: t.TempDir(),
		Steps:     []model.Step{{Name: "slow", Run: sleepCmd(10), TimeoutSec: 1}},
	}, &out)
	elapsed := time.Since(started)
	if err != nil {
		t.Fatalf("RunSteps error: %v\n%s", err, out.String())
	}
	if code != 124 {
		t.Fatalf("exit code = %d, want 124\n%s", code, out.String())
	}
	if elapsed > 8*time.Second {
		t.Fatalf("timeout did not kill the step, elapsed=%s", elapsed)
	}
	if !strings.Contains(out.String(), "超时") {
		t.Fatalf("output must explain the timeout:\n%s", out.String())
	}
}

// allow_failure：失败只警告，后续步骤照常执行，构建整体成功。
func TestStepAllowFailureContinues(t *testing.T) {
	var out bytes.Buffer
	code, err := NewLocalExecutor().RunSteps(context.Background(), RunSpec{
		Workspace: t.TempDir(),
		Steps: []model.Step{
			{Name: "lint", Run: failCmd(), AllowFailure: true},
			{Name: "build", Run: echoCmd("built-ok")},
		},
	}, &out)
	if err != nil {
		t.Fatalf("RunSteps error: %v", err)
	}
	if code != 0 {
		t.Fatalf("exit code = %d, want 0\n%s", code, out.String())
	}
	if !strings.Contains(out.String(), "built-ok") {
		t.Fatalf("step after allow_failure must run:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "allow_failure=true") {
		t.Fatalf("log must mark allow_failure:\n%s", out.String())
	}
}

// 致命失败：中止后续 success 步骤，但 when=always / when=failure 仍然执行（清理场景）。
func TestStepWhenAlwaysRunsAfterFailure(t *testing.T) {
	var out bytes.Buffer
	code, err := NewLocalExecutor().RunSteps(context.Background(), RunSpec{
		Workspace: t.TempDir(),
		Steps: []model.Step{
			{Name: "build", Run: failCmd()},
			{Name: "cleanup", Run: echoCmd("cleanup-ran"), When: model.StepWhenAlways},
			{Name: "report", Run: echoCmd("failure-report"), When: model.StepWhenFailure},
			{Name: "publish", Run: echoCmd("should-not-run"), When: model.StepWhenSuccess},
		},
	}, &out)
	if err != nil {
		t.Fatalf("RunSteps error: %v", err)
	}
	if code != 3 {
		t.Fatalf("exit code = %d, want 3\n%s", code, out.String())
	}
	log := out.String()
	if !strings.Contains(log, "cleanup-ran") || !strings.Contains(log, "failure-report") {
		t.Fatalf("when=always/failure steps must run after failure:\n%s", log)
	}
	if strings.Contains(log, "should-not-run") {
		t.Fatalf("when=success step must be skipped after failure:\n%s", log)
	}
	if !strings.Contains(log, "skipped") {
		t.Fatalf("skip must be logged explicitly:\n%s", log)
	}
}

// when=failure 在全部成功时不执行。
func TestStepWhenFailureSkippedOnSuccess(t *testing.T) {
	var out bytes.Buffer
	code, err := NewLocalExecutor().RunSteps(context.Background(), RunSpec{
		Workspace: t.TempDir(),
		Steps: []model.Step{
			{Name: "build", Run: echoCmd("ok")},
			{Name: "report", Run: echoCmd("failure-report"), When: model.StepWhenFailure},
		},
	}, &out)
	if err != nil || code != 0 {
		t.Fatalf("RunSteps = %d, %v\n%s", code, err, out.String())
	}
	if strings.Contains(out.String(), "failure-report") {
		t.Fatalf("when=failure step must not run on success:\n%s", out.String())
	}
}

// 取消优先级高于超时：父 context 取消时返回 error（构建记为 aborted）。
func TestStepCancelBeatsTimeout(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(300 * time.Millisecond)
		cancel()
	}()
	var out bytes.Buffer
	_, err := NewLocalExecutor().RunSteps(ctx, RunSpec{
		Workspace: t.TempDir(),
		Steps:     []model.Step{{Name: "slow", Run: sleepCmd(10), TimeoutSec: 30}},
	}, &out)
	if err == nil {
		t.Fatalf("cancelled build must return error:\n%s", out.String())
	}
}

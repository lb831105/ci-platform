package executor

import (
	"context"
	"strings"
	"testing"
	"time"

	"ci-platform/internal/model"
)

// 取消构建步骤：cmd.exe 进程树（含 sleep 孙进程）应被整体终止，RunSteps 快速返回。
func TestRunStepsCancelKillsTree(t *testing.T) {
	l := NewLocalExecutor()
	ctx, cancel := context.WithCancel(context.Background())
	var out strings.Builder
	spec := RunSpec{
		Workspace: t.TempDir(),
		Steps:     []model.Step{{Name: "build", Run: "sleep 30"}},
	}
	done := make(chan error, 1)
	go func() {
		_, err := l.RunSteps(ctx, spec, &out)
		done <- err
	}()
	time.Sleep(1 * time.Second)
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("RunSteps should fail after cancel")
		}
		t.Logf("returned after cancel: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatalf("RunSteps did not return after cancel; output so far:\n%s", out.String())
	}
}

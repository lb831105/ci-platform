package service

import (
	"testing"

	"ci-platform/internal/model"
)

// 步骤级控制项的归一化与上限校验。
func TestNormalizeStepControls(t *testing.T) {
	base := func(step model.Step) *model.Pipeline {
		return &model.Pipeline{Name: "demo", Steps: []model.Step{step}}
	}

	// 默认 when=success；合法范围照常通过。
	p := base(model.Step{
		Name: "b", Run: "echo ok",
		TimeoutSec: 600, Retry: 2, RetryDelaySec: 5, AllowFailure: true,
	})
	if err := normalizeAndValidate(p, ""); err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if p.Steps[0].When != model.StepWhenSuccess {
		t.Fatalf("when = %q, want success", p.Steps[0].When)
	}

	p = base(model.Step{Name: "cleanup", Run: "echo ok", When: "ALWAYS"})
	if err := normalizeAndValidate(p, ""); err != nil {
		t.Fatalf("normalize when=ALWAYS: %v", err)
	}
	if p.Steps[0].When != model.StepWhenAlways {
		t.Fatalf("when = %q, want always", p.Steps[0].When)
	}

	bad := []struct {
		name string
		step model.Step
	}{
		{"negative timeout", model.Step{Run: "x", TimeoutSec: -1}},
		{"timeout too large", model.Step{Run: "x", TimeoutSec: model.MaxStepTimeoutSec + 1}},
		{"retry too large", model.Step{Run: "x", Retry: model.MaxStepRetry + 1}},
		{"negative retry", model.Step{Run: "x", Retry: -1}},
		{"delay too large", model.Step{Run: "x", RetryDelaySec: model.MaxStepRetryDelay + 1}},
		{"bad when", model.Step{Run: "x", When: "sometimes"}},
		{"failure+allow_failure", model.Step{Run: "x", When: model.StepWhenFailure, AllowFailure: true}},
	}
	for _, tc := range bad {
		tc.step.Name = "s"
		if err := normalizeAndValidate(base(tc.step), ""); err == nil {
			t.Fatalf("%s: want error, got nil", tc.name)
		}
	}
}

package service

import (
	"strings"
	"testing"

	"ci-platform/internal/model"
	"ci-platform/internal/webhook"
)

func TestDecideWebhookDefaultsToPushOnly(t *testing.T) {
	p := &model.Pipeline{}
	// 默认（未配置事件白名单）只接受 push：MR 事件不能误触发构建。
	if d := DecideWebhook(p, webhook.Event{Kind: webhook.KindMergeRequest}); d.Trigger {
		t.Fatalf("merge_request must be ignored by default: %+v", d)
	} else if !strings.Contains(d.Reason, "merge_request") {
		t.Fatalf("reason should mention the event, got %q", d.Reason)
	}
	if d := DecideWebhook(p, webhook.Event{Kind: webhook.KindPush, Branch: "feature/x"}); !d.Trigger {
		t.Fatalf("push must trigger: %+v", d)
	} else if d.Branch != "feature/x" {
		t.Fatalf("payload branch must be used by default, got %q", d.Branch)
	}
}

func TestDecideWebhookBranchWhitelist(t *testing.T) {
	p := &model.Pipeline{WebhookBranches: []string{"main", "release/*"}}
	if d := DecideWebhook(p, webhook.Event{Kind: webhook.KindPush, Branch: "feature/x"}); d.Trigger {
		t.Fatal("branch outside whitelist must be ignored")
	} else if !strings.Contains(d.Reason, "feature/x") {
		t.Fatalf("reason should name the branch, got %q", d.Reason)
	}
	if d := DecideWebhook(p, webhook.Event{Kind: webhook.KindPush, Branch: "release/1.2"}); !d.Trigger {
		t.Fatal("branch in whitelist must trigger")
	}
}

func TestDecideWebhookModeFixed(t *testing.T) {
	p := &model.Pipeline{WebhookBranchMode: "fixed", GitBranch: "main"}
	d := DecideWebhook(p, webhook.Event{Kind: webhook.KindPush, Branch: "dev"})
	if !d.Trigger || d.Branch != "" {
		// Branch 为空表示"沿用任务配置的分支"
		t.Fatalf("fixed mode must not override branch: %+v", d)
	}
}

func TestDecideWebhookSkipCI(t *testing.T) {
	p := &model.Pipeline{}
	d := DecideWebhook(p, webhook.Event{Kind: webhook.KindPush, Branch: "main", SkipCI: true})
	if d.Trigger || !strings.Contains(d.Reason, "skip") {
		t.Fatalf("skip ci must be honored: %+v", d)
	}
}

func TestDecideWebhookTagEvent(t *testing.T) {
	p := &model.Pipeline{WebhookEvents: []string{"push", "tag_push"}}
	d := DecideWebhook(p, webhook.Event{Kind: webhook.KindTagPush, Branch: "v1.0.0"})
	if !d.Trigger || d.Branch != "refs/tags/v1.0.0" {
		t.Fatalf("tag_push must be allowed when whitelisted: %+v", d)
	}
	// 未勾选 tag_push 时同样事件应被忽略。
	if d := DecideWebhook(&model.Pipeline{}, webhook.Event{Kind: webhook.KindTagPush}); d.Trigger {
		t.Fatal("tag_push must be ignored unless whitelisted")
	}
}

// 分支名来自外部请求，必须拒绝会污染 git 命令/路径的形态。
func TestValidateTriggerBranch(t *testing.T) {
	bad := []string{"dev branch", "-upload-pack", "a..b", "/lead", "trail/", strings.Repeat("x", 300)}
	for _, b := range bad {
		if err := validateTriggerBranch(b); err == nil {
			t.Fatalf("branch %q must be rejected", b)
		}
	}
	for _, b := range []string{"main", "release/1.2", "feature/a-b_c"} {
		if err := validateTriggerBranch(b); err != nil {
			t.Fatalf("branch %q should be accepted: %v", b, err)
		}
	}
}

func TestNormalizeWebhookTrigger(t *testing.T) {
	p := &model.Pipeline{
		WebhookBranchMode: "PAYLOAD",
		WebhookBranches:   []string{" main ", "", "MAIN", "release/*"},
		WebhookEvents:     []string{"Push", "push", ""},
	}
	if err := normalizeWebhookTrigger(p); err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if p.WebhookBranchMode != "payload" {
		t.Fatalf("mode = %q", p.WebhookBranchMode)
	}
	if len(p.WebhookBranches) != 2 || p.WebhookBranches[0] != "main" {
		t.Fatalf("branches not normalized: %v", p.WebhookBranches)
	}
	if len(p.WebhookEvents) != 1 || p.WebhookEvents[0] != "push" {
		t.Fatalf("events not normalized: %v", p.WebhookEvents)
	}

	// 非法值直接拒绝，而不是"保存成功但永远不触发"。
	if err := normalizeWebhookTrigger(&model.Pipeline{WebhookBranchMode: "branch"}); err == nil {
		t.Fatal("invalid mode must be rejected")
	}
	if err := normalizeWebhookTrigger(&model.Pipeline{WebhookEvents: []string{"merge_request"}}); err == nil {
		t.Fatal("unsupported event must be rejected")
	}
	if err := normalizeWebhookTrigger(&model.Pipeline{CommitStatus: model.CommitStatusConfig{Enabled: true}}); err == nil {
		t.Fatal("commit status without credential must be rejected")
	}
}

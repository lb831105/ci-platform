package service

import (
	"fmt"
	"strings"

	"ci-platform/internal/model"
	"ci-platform/internal/webhook"
)

// WebhookDecision 是一次 Webhook 请求的处理结论。
type WebhookDecision struct {
	// Trigger 为 false 表示这条投递被策略忽略（属于「正常忽略」，必须回 2xx，
	// 否则 GitLab 会把它记成投递失败、运维天天收到红点）。
	Trigger bool
	// Branch 是本次构建要用的分支；空表示用任务配置的分支。
	Branch string
	// Reason 是不触发的原因，写进响应与平台日志，用来回答「为什么没构建」。
	Reason string
}

// DecideWebhook 按任务配置的触发器策略判断这次投递要不要构建。
//
// 顺序是有意的：先看事件类型（最便宜、最容易误配），再看 skip ci（用户显式要求跳过），
// 最后看分支白名单。任何一个环节拒绝都给出人类可读的理由。
func DecideWebhook(p *model.Pipeline, ev webhook.Event) WebhookDecision {
	allowed := p.WebhookEvents
	if len(allowed) == 0 {
		allowed = []string{webhook.KindPush}
	}
	if !webhook.TypeAllowed(allowed, ev.Kind) {
		return WebhookDecision{Reason: fmt.Sprintf("事件类型 %s 不在允许列表（%s）",
			ev.Kind, strings.Join(allowed, ", "))}
	}
	if ev.SkipCI {
		return WebhookDecision{Reason: "提交说明包含跳过 CI 的标记（如 [ci skip]）"}
	}
	if !webhook.BranchAllowed(p.WebhookBranches, ev.Branch) {
		return WebhookDecision{Reason: fmt.Sprintf("分支 %q 不在允许列表（%s）",
			ev.Branch, strings.Join(p.WebhookBranches, ", "))}
	}

	// 分支来源：fixed 用任务配置；payload 用推送事件的分支。
	if strings.EqualFold(p.WebhookBranchMode, "fixed") || ev.Branch == "" {
		return WebhookDecision{Trigger: true}
	}
	branch := ev.Branch
	if ev.Kind == webhook.KindTagPush {
		// GitClient 用 refs/tags/ 前缀区分 tag 与同名分支。
		branch = "refs/tags/" + branch
	}
	if err := validateTriggerBranch(branch); err != nil {
		return WebhookDecision{Reason: err.Error()}
	}
	return WebhookDecision{Trigger: true, Branch: branch}
}

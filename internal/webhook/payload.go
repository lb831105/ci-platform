// Package webhook 把各代码平台的 Webhook 请求归一化成内部事件，
// 供「触发器策略」（事件白名单 / 分支过滤 / skip ci）判断。
//
// 为什么单独成包：payload 解析是纯粹的格式翻译，既不碰存储也不碰调度，
// 放在 handler 里会和身份校验、触发逻辑搅在一起，也很难单测。
package webhook

import (
	"encoding/json"
	"net/http"
	"path"
	"strings"
)

// 事件类型（内部统一命名，不直接暴露各平台的叫法）。
const (
	KindPush         = "push"
	KindTagPush      = "tag_push"
	KindMergeRequest = "merge_request"
	KindNote         = "note"
	KindPipeline     = "pipeline"
	KindPing         = "ping"
	KindOther        = "other"
)

// 支持的提供方。
const (
	ProviderGitlab  = "gitlab"
	ProviderGithub  = "github"
	ProviderGeneric = "generic"
)

// Event 是一次 Webhook 投递归一化后的事件。
type Event struct {
	Provider string // gitlab / github / generic
	Kind     string // push / tag_push / merge_request / ...
	Ref      string // 原始 ref，如 refs/heads/main
	Branch   string // 归一化后的分支名（tag_push 时为 tag 名）
	Commit   string // 本次推送后的提交 SHA
	Message  string // 提交说明（多条提交拼接，用于识别 skip ci）
	Project  string // 项目路径（如 group/app）或仓库全名
	// Delivery 是投递唯一标识（GitLab: X-Gitlab-Event-UUID / GitHub: X-GitHub-Delivery），
	// 用于幂等去重：GitLab 上点「Resend」不会重复构建。
	Delivery string
	SkipCI   bool // 提交说明里声明了跳过 CI
}

// BranchRef 返回分支引用（用于日志）。
func (e Event) BranchRef() string {
	if e.Branch == "" {
		return "-"
	}
	if e.Kind == KindTagPush {
		return "tag:" + e.Branch
	}
	return e.Branch
}

// Parse 解析一次 Webhook 请求。body 为空或非 JSON 时退化为 generic 事件，
// 这样 curl 直接 POST（不带请求体）仍可触发构建，保持旧行为。
func Parse(h http.Header, body []byte) Event {
	ev := Event{Provider: ProviderGeneric, Kind: KindPush}
	gitlabEvent := strings.TrimSpace(h.Get("X-Gitlab-Event"))
	githubEvent := strings.TrimSpace(h.Get("X-GitHub-Event"))

	if gitlabEvent != "" || looksLikeGitlab(body) {
		ev.Provider = ProviderGitlab
		ev.Delivery = strings.TrimSpace(h.Get("X-Gitlab-Event-UUID"))
		parseGitlab(&ev, body, gitlabEvent)
	} else if githubEvent != "" || looksLikeGithub(body) {
		ev.Provider = ProviderGithub
		ev.Delivery = strings.TrimSpace(h.Get("X-GitHub-Delivery"))
		parseGithub(&ev, body, githubEvent)
	}

	ev.Branch = branchFromRef(ev.Ref)
	ev.SkipCI = hasSkipCI(ev.Message)
	return ev
}

// gitlabPayload 只声明用得到的字段：解析失败/字段缺失都不能影响触发（宁可构建也不静默丢弃）。
type gitlabPayload struct {
	ObjectKind string `json:"object_kind"`
	Ref        string `json:"ref"`
	After      string `json:"after"`
	CheckoutSa string `json:"checkout_sha"`
	Commits    []struct {
		Message string `json:"message"`
		Title   string `json:"title"`
	} `json:"commits"`
	Project struct {
		PathWithNamespace string `json:"path_with_namespace"`
		ID                int    `json:"id"`
	} `json:"project"`
}

type githubPayload struct {
	Ref        string `json:"ref"`
	After      string `json:"after"`
	Zen        string `json:"zen"`
	HeadCommit *struct {
		ID      string `json:"id"`
		Message string `json:"message"`
	} `json:"head_commit"`
	Commits []struct {
		Message string `json:"message"`
	} `json:"commits"`
	Repository struct {
		FullName string `json:"full_name"`
	} `json:"repository"`
}

func parseGitlab(ev *Event, body []byte, gitlabEvent string) {
	var p gitlabPayload
	if len(body) > 0 && json.Unmarshal(body, &p) == nil {
		ev.Ref = p.Ref
		ev.Commit = p.After
		if ev.Commit == "" {
			ev.Commit = p.CheckoutSa
		}
		ev.Project = p.Project.PathWithNamespace
		for _, c := range p.Commits {
			ev.Message += c.Message + "\n" + c.Title + "\n"
		}
		ev.Kind = gitlabKind(p.ObjectKind, p.Ref)
		if p.ObjectKind == "" {
			ev.Kind = gitlabKindFromHeader(gitlabEvent, p.Ref)
		}
		return
	}
	// 有 header 但 body 解析不了：仍按 header 判定事件类型，但不带分支信息。
	ev.Kind = gitlabKindFromHeader(gitlabEvent, "")
}

func parseGithub(ev *Event, body []byte, githubEvent string) {
	var p githubPayload
	if len(body) > 0 && json.Unmarshal(body, &p) == nil {
		ev.Ref = p.Ref
		ev.Commit = p.After
		if p.HeadCommit != nil {
			if ev.Commit == "" {
				ev.Commit = p.HeadCommit.ID
			}
			ev.Message += p.HeadCommit.Message + "\n"
		}
		ev.Project = p.Repository.FullName
		for _, c := range p.Commits {
			ev.Message += c.Message + "\n"
		}
	}
	ev.Kind = githubKind(githubEvent, ev.Ref)
}

// gitlabKind 由 object_kind + ref 推导事件类型（tag 推送在 GitLab 里 object_kind 仍可能是 push）。
func gitlabKind(objectKind, ref string) string {
	switch strings.ToLower(strings.TrimSpace(objectKind)) {
	case "push":
		if strings.HasPrefix(ref, "refs/tags/") {
			return KindTagPush
		}
		return KindPush
	case "tag_push", "tagpush":
		return KindTagPush
	case "merge_request":
		return KindMergeRequest
	case "note":
		return KindNote
	case "pipeline":
		return KindPipeline
	case "":
		return KindPush
	}
	return KindOther
}

// gitlabKindFromHeader 在 body 无法解析时按 X-Gitlab-Event 判定（"Push Hook"/"Tag Push Hook"）。
func gitlabKindFromHeader(header, ref string) string {
	switch strings.ToLower(strings.TrimSpace(header)) {
	case "push hook", "push":
		if strings.HasPrefix(ref, "refs/tags/") {
			return KindTagPush
		}
		return KindPush
	case "tag push hook", "tag push":
		return KindTagPush
	case "merge request hook", "merge request":
		return KindMergeRequest
	case "note hook", "note":
		return KindNote
	case "pipeline hook", "pipeline":
		return KindPipeline
	case "":
		return KindPush
	}
	return KindOther
}

func githubKind(header, ref string) string {
	switch strings.ToLower(strings.TrimSpace(header)) {
	case "push":
		if strings.HasPrefix(ref, "refs/tags/") {
			return KindTagPush
		}
		return KindPush
	case "ping":
		return KindPing
	case "pull_request", "pull_request_review":
		return KindMergeRequest
	case "issue_comment", "pull_request_review_comment":
		return KindNote
	}
	return KindOther
}

func looksLikeGitlab(body []byte) bool {
	return strings.Contains(string(body), `"object_kind"`)
}

func looksLikeGithub(body []byte) bool {
	s := string(body)
	return strings.Contains(s, `"zen"`) || strings.Contains(s, `"head_commit"`)
}

// branchFromRef 把 refs/heads/main → main，refs/tags/v1.0.0 → v1.0.0。
func branchFromRef(ref string) string {
	ref = strings.TrimSpace(ref)
	switch {
	case strings.HasPrefix(ref, "refs/heads/"):
		return strings.TrimPrefix(ref, "refs/heads/")
	case strings.HasPrefix(ref, "refs/tags/"):
		return strings.TrimPrefix(ref, "refs/tags/")
	}
	return ref
}

// skipCITokens 是各平台约定的"跳过 CI"标记（大小写不敏感）。
var skipCITokens = []string{"[ci skip]", "[skip ci]", "[ci-skip]", "[skip-ci]", "[no ci]", "[no-ci]"}

// TypeAllowed 判断事件类型是否被允许；events 为空时默认只接受 push（与 Jenkins/CI 惯例一致，
// 避免 MR/评论事件误触发构建）。tag_push 需要显式勾选。
func TypeAllowed(events []string, kind string) bool {
	if len(events) == 0 {
		return kind == KindPush
	}
	for _, want := range events {
		if strings.EqualFold(strings.TrimSpace(want), kind) {
			return true
		}
	}
	return false
}

// BranchAllowed 判断分支是否命中白名单；白名单为空表示允许任意分支。
// 支持精确匹配、glob（path.Match，* 不跨 /）与前缀通配 release/**。
func BranchAllowed(patterns []string, branch string) bool {
	if len(patterns) == 0 {
		return true
	}
	// tag 推送时 branch 是 tag 名；没有 ref 信息时不做分支判断（无从判断，交给后续步骤失败）。
	if branch == "" {
		return true
	}
	for _, raw := range patterns {
		p := strings.TrimSpace(raw)
		if p == "" {
			continue
		}
		if strings.HasSuffix(p, "/**") {
			if strings.HasPrefix(branch, strings.TrimSuffix(p, "**")) {
				return true
			}
			continue
		}
		if p == branch {
			return true
		}
		if ok, err := path.Match(p, branch); err == nil && ok {
			return true
		}
	}
	return false
}

// hasSkipCI 判断提交说明里是否声明跳过 CI（任一条提交命中即可）。
func hasSkipCI(message string) bool {
	if message == "" {
		return false
	}
	lower := strings.ToLower(message)
	for _, token := range skipCITokens {
		if strings.Contains(lower, token) {
			return true
		}
	}
	return false
}

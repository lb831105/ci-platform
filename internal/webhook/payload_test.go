package webhook

import (
	"net/http"
	"testing"
)

// realGitlabPush 是 GitLab Push Hook 的精简真实样本。
const realGitlabPush = `{
  "object_kind": "push",
  "ref": "refs/heads/release/1.2",
  "before": "0000000000000000000000000000000000000000",
  "after": "5f2c1a9a1b4c8f0e2b3d4c5a6f708192a3b4c5d6",
  "project_id": 12,
  "project": {"id": 12, "path_with_namespace": "ops/order-api", "default_branch": "main"},
  "commits": [{"id": "5f2c1a9", "message": "fix: 修复订单超时\n\n[skip ci]", "title": "fix: 修复订单超时"}],
  "total_commits_count": 1
}`

func gitlabHeader(event string) http.Header {
	h := http.Header{}
	if event != "" {
		h.Set("X-Gitlab-Event", event)
	}
	return h
}

func TestParseGitlabPush(t *testing.T) {
	ev := Parse(gitlabHeader("Push Hook"), []byte(realGitlabPush))
	if ev.Provider != ProviderGitlab || ev.Kind != KindPush {
		t.Fatalf("provider/kind = %s/%s", ev.Provider, ev.Kind)
	}
	if ev.Branch != "release/1.2" {
		t.Fatalf("branch = %q", ev.Branch)
	}
	if ev.Commit == "" || ev.Project != "ops/order-api" {
		t.Fatalf("commit/project = %q/%q", ev.Commit, ev.Project)
	}
	if !ev.SkipCI {
		t.Fatal("[skip ci] in commit message must be detected")
	}
}

func TestParseGitlabTagPush(t *testing.T) {
	body := `{"object_kind":"tag_push","ref":"refs/tags/v1.4.0","after":"abc123","commits":[]}`
	ev := Parse(gitlabHeader("Tag Push Hook"), []byte(body))
	if ev.Kind != KindTagPush || ev.Branch != "v1.4.0" {
		t.Fatalf("kind/branch = %s/%s", ev.Kind, ev.Branch)
	}
	// push 事件的 ref 指向 tags 时同样按 tag 处理（GitLab 实际行为）。
	body = `{"object_kind":"push","ref":"refs/tags/v1.4.0"}`
	if ev = Parse(gitlabHeader("Push Hook"), []byte(body)); ev.Kind != KindTagPush {
		t.Fatalf("tag ref in push event must be tag_push, got %s", ev.Kind)
	}
}

func TestParseGitlabNonPushEvents(t *testing.T) {
	cases := map[string]string{
		"Merge Request Hook": `{"object_kind":"merge_request","object_attributes":{"source_branch":"dev"}}`,
		"Note Hook":          `{"object_kind":"note"}`,
		"Pipeline Hook":      `{"object_kind":"pipeline"}`,
		"":                   `{"object_kind":"deployment"}`,
	}
	want := map[string]string{
		"Merge Request Hook": KindMergeRequest,
		"Note Hook":          KindNote,
		"Pipeline Hook":      KindPipeline,
		"":                   KindOther,
	}
	for header, body := range cases {
		if ev := Parse(gitlabHeader(header), []byte(body)); ev.Kind != want[header] {
			t.Fatalf("%s: kind = %s, want %s", header, ev.Kind, want[header])
		}
	}
}

// body 解析失败但 header 明确：仍按 header 归类（不能被静默当成 push 触发构建）。
func TestParseGitlabBrokenBodyUsesHeader(t *testing.T) {
	ev := Parse(gitlabHeader("Merge Request Hook"), []byte("<html>500</html>"))
	if ev.Kind != KindMergeRequest {
		t.Fatalf("kind = %s", ev.Kind)
	}
}

func TestParseGithubPush(t *testing.T) {
	body := `{"ref":"refs/heads/main","after":"deadbeef","head_commit":{"id":"deadbeef","message":"docs: update"},"repository":{"full_name":"ops/order-api"}}`
	h := http.Header{}
	h.Set("X-GitHub-Event", "push")
	h.Set("X-GitHub-Delivery", "delivery-1")
	ev := Parse(h, []byte(body))
	if ev.Provider != ProviderGithub || ev.Branch != "main" || ev.Delivery != "delivery-1" {
		t.Fatalf("provider/branch/delivery = %s/%s/%s", ev.Provider, ev.Branch, ev.Delivery)
	}
	if ev.Kind != KindPush {
		t.Fatalf("kind = %s", ev.Kind)
	}
}

// 不带任何 header/body 的 curl 调用：退化为 generic push，保持旧行为可触发。
func TestParseGeneric(t *testing.T) {
	ev := Parse(http.Header{}, nil)
	if ev.Provider != ProviderGeneric || ev.Kind != KindPush || ev.Branch != "" {
		t.Fatalf("generic event = %+v", ev)
	}
}

func TestTypeAllowed(t *testing.T) {
	if !TypeAllowed(nil, KindPush) {
		t.Fatal("empty whitelist must allow push")
	}
	if TypeAllowed(nil, KindMergeRequest) || TypeAllowed(nil, KindTagPush) {
		t.Fatal("empty whitelist must reject non-push events")
	}
	events := []string{"push", "tag_push"}
	if !TypeAllowed(events, KindTagPush) || TypeAllowed(events, KindMergeRequest) {
		t.Fatal("explicit whitelist not honored")
	}
}

func TestBranchAllowed(t *testing.T) {
	if !BranchAllowed(nil, "anything") {
		t.Fatal("empty whitelist must allow any branch")
	}
	patterns := []string{"main", "release/*", "hotfix/**", "v1.2.3"}
	cases := map[string]bool{
		"main":          true,
		"release/1.2":   true,
		"release/1.2/x": false, // path.Match 的 * 不跨 /
		"hotfix/urgent": true,
		"hotfix/a/b/c":  true, // /** 前缀匹配
		"v1.2.3":        true,
		"feature/foo":   false,
		"release":       false,
		"maintenance":   false,
	}
	for branch, want := range cases {
		if got := BranchAllowed(patterns, branch); got != want {
			t.Fatalf("BranchAllowed(%q) = %v, want %v", branch, got, want)
		}
	}
	if !BranchAllowed(patterns, "") {
		t.Fatal("no branch info must not be filtered")
	}
}

func TestHasSkipCI(t *testing.T) {
	if !hasSkipCI("chore: bump deps [CI SKIP]") {
		t.Fatal("case-insensitive [ci skip] must match")
	}
	if hasSkipCI("fix: real change") {
		t.Fatal("normal message must not skip")
	}
}

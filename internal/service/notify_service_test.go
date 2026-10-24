package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"ci-platform/internal/model"
)

// fakeUserDir 模拟平台用户联系方式。
type fakeUserDir struct{ users map[string]model.RbacUser }

func (f *fakeUserDir) Contacts(names []string) map[string]model.RbacUser {
	out := map[string]model.RbacUser{}
	for _, n := range names {
		if u, ok := f.users[n]; ok {
			out[n] = u
		}
	}
	return out
}

func testPipelineWithNotify(n *model.JobNotify) *model.Pipeline {
	return &model.Pipeline{ID: "p1", Name: "demo", Notify: n}
}

func testBuildPair() (*model.Build, *model.Build) {
	now := time.Now().UTC()
	cur := &model.Build{ID: "2", PipelineID: "p1", Status: model.BuildSuccess, TriggerType: model.TriggerManual, TriggeredBy: "dev1"}
	cur.StartedAt = &now
	cur.FinishedAt = &now
	prev := &model.Build{ID: "1", PipelineID: "p1", Status: model.BuildFailed}
	return cur, prev
}

// 事件判定：失败 / 成功 / 修复 / 取消。
func TestBuildEvent(t *testing.T) {
	cases := []struct {
		cur, prev model.BuildStatus
		want      string
	}{
		{model.BuildFailed, model.BuildSuccess, model.NotifyEventFailure},
		{model.BuildSuccess, model.BuildSuccess, model.NotifyEventSuccess},
		{model.BuildSuccess, model.BuildFailed, model.NotifyEventFixed},
		{model.BuildSuccess, model.BuildAborted, model.NotifyEventFixed},
		{model.BuildAborted, model.BuildSuccess, model.NotifyEventAborted},
	}
	for _, c := range cases {
		cur := &model.Build{Status: c.cur}
		prev := &model.Build{Status: c.prev}
		if got := model.BuildEvent(cur, prev); got != c.want {
			t.Fatalf("cur=%s prev=%s: got %q want %q", c.cur, c.prev, got, c.want)
		}
	}
}

// 只订阅「修复」时：普通成功不通知，修复通知。
func TestNotifyFixedOnly(t *testing.T) {
	svc := NewNotifyService(&fakeSettingsProvider{settings: model.Settings{
		Email: model.EmailSettings{Enabled: true},
	}}, nil)
	p := testPipelineWithNotify(&model.JobNotify{
		Enabled: true,
		Emails:  []string{"dev@example.com"},
		Events:  model.NotifyEvents{OnFixed: true},
		// 关闭全局默认：避免依赖环境
		UseGlobal: false,
	})
	cur, prev := testBuildPair()
	// 普通成功 → 不发送
	plan := svc.plan(p, cur, &model.Build{Status: model.BuildSuccess})
	if plan.event != model.NotifyEventNone {
		t.Fatalf("plain success should not notify when only fixed is on, event=%q", plan.event)
	}
	// 修复 → 发送
	plan = svc.plan(p, cur, prev)
	if plan.event != model.NotifyEventFixed || len(plan.emails) != 1 {
		t.Fatalf("fixed should notify, event=%q emails=%v", plan.event, plan.emails)
	}
}

// 人员 → 邮箱解析 + 企微 @ 提醒（仅失败时 @）。
func TestNotifyPeopleResolution(t *testing.T) {
	users := &fakeUserDir{users: map[string]model.RbacUser{
		"zhangsan": {Name: "zhangsan", Email: "zs@example.com", WecomID: "zs-id", Phone: "13800000000"},
		"lisi":     {Name: "lisi"}, // 未配置联系方式
	}}
	// 邮件需启用（未启用 SMTP 时不发送邮件，这是预期行为）。
	svc := NewNotifyService(&fakeSettingsProvider{settings: model.Settings{
		Email: model.EmailSettings{Enabled: true},
	}}, users)
	p := testPipelineWithNotify(&model.JobNotify{
		Enabled: true,
		People:  []string{"zhangsan", "lisi"},
		Targets: []model.NotifyTarget{{Channel: "wecom", URL: "http://example.com/hook"}},
		Events:  model.NotifyEvents{OnFailure: true},
	})
	cur := &model.Build{ID: "3", PipelineID: "p1", Status: model.BuildFailed}
	plan := svc.plan(p, cur, nil)
	if plan.event != model.NotifyEventFailure {
		t.Fatalf("event = %q", plan.event)
	}
	if len(plan.emails) != 1 || plan.emails[0] != "zs@example.com" {
		t.Fatalf("emails = %v, want only zhangsan's", plan.emails)
	}
	// 失败事件 @ 企微 userid（未配置的人被跳过）
	mentions := plan.mentionsFor("wecom")
	if len(mentions) != 1 || mentions[0] != "zs-id" {
		t.Fatalf("mentions = %v, want [zs-id]", mentions)
	}
	// 钉钉用手机号
	if ding := plan.mentionsFor("dingtalk"); len(ding) != 1 || ding[0] != "13800000000" {
		t.Fatalf("dingtalk mentions = %v", ding)
	}
	// 成功事件不 @ 人
	curOK := &model.Build{ID: "4", PipelineID: "p1", Status: model.BuildSuccess}
	planOK := svc.plan(p, curOK, nil)
	if planOK.event != model.NotifyEventNone {
		t.Fatalf("success should be filtered (only on_failure), event=%q", planOK.event)
	}
}

// 任务未启用通知 → 回落全局默认收件人与机器人。
func TestNotifyGlobalFallback(t *testing.T) {
	settings := model.Settings{
		Email: model.EmailSettings{
			Enabled: true, Recipients: "ops@example.com", NotifyOnFailure: true,
		},
		WebhookNotify: model.WebhookNotifySettings{
			Enabled: true, Channel: "dingtalk", URL: "http://example.com/global", NotifyOnFailure: true,
		},
	}
	svc := NewNotifyService(&fakeSettingsProvider{settings: settings}, nil)
	p := &model.Pipeline{ID: "p1", Name: "demo"} // 未配置 notify
	cur := &model.Build{ID: "1", PipelineID: "p1", Status: model.BuildFailed}
	plan := svc.plan(p, cur, nil)
	if len(plan.emails) != 1 || plan.emails[0] != "ops@example.com" {
		t.Fatalf("emails = %v", plan.emails)
	}
	if len(plan.targets) != 1 || plan.targets[0].url != "http://example.com/global" {
		t.Fatalf("targets = %+v", plan.targets)
	}
}

// 任务启用 + use_global → 任务收件人与全局收件人叠加且去重。
func TestNotifyUseGlobalMerges(t *testing.T) {
	settings := model.Settings{
		Email: model.EmailSettings{
			Enabled: true, Recipients: "ops@example.com,dev@example.com", NotifyOnFailure: true,
		},
	}
	svc := NewNotifyService(&fakeSettingsProvider{settings: settings}, nil)
	p := testPipelineWithNotify(&model.JobNotify{
		Enabled:   true,
		UseGlobal: true,
		Emails:    []string{"dev@example.com", "team@example.com"},
		Events:    model.NotifyEvents{OnFailure: true},
	})
	cur := &model.Build{ID: "1", PipelineID: "p1", Status: model.BuildFailed}
	plan := svc.plan(p, cur, nil)
	got := strings.Join(plan.emails, ",")
	if !strings.Contains(got, "team@example.com") || !strings.Contains(got, "ops@example.com") {
		t.Fatalf("emails = %v, want merged", plan.emails)
	}
	// dev@example.com 去重后只出现一次
	if strings.Count(got, "dev@example.com") != 1 {
		t.Fatalf("duplicate email not removed: %v", plan.emails)
	}
}

// 群目标可单独订阅事件：只订阅成功的群不收失败通知。
func TestNotifyPerTargetEvents(t *testing.T) {
	svc := NewNotifyService(&fakeSettingsProvider{settings: model.Settings{}}, nil)
	p := testPipelineWithNotify(&model.JobNotify{
		Enabled: true,
		Targets: []model.NotifyTarget{
			{Channel: "dingtalk", URL: "http://a/hook", Events: model.NotifyEvents{OnSuccess: true}},
			{Channel: "wecom", URL: "http://b/hook", Events: model.NotifyEvents{OnFailure: true}},
		},
		Events: model.NotifyEvents{OnFailure: true, OnSuccess: true},
	})
	cur := &model.Build{ID: "1", PipelineID: "p1", Status: model.BuildFailed}
	plan := svc.plan(p, cur, nil)
	if len(plan.targets) != 1 || plan.targets[0].channel != "wecom" {
		t.Fatalf("targets = %+v, want only wecom", plan.targets)
	}
}

// 端到端发送：webhook 收到含事件标签与 @ 的消息。
func TestNotifySendsWebhook(t *testing.T) {
	var got map[string]interface{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		_, _ = w.Write([]byte(`{"errcode":0}`))
	}))
	defer srv.Close()

	users := &fakeUserDir{users: map[string]model.RbacUser{
		"zhangsan": {Name: "zhangsan", WecomID: "zs-id"},
	}}
	svc := NewNotifyService(&fakeSettingsProvider{settings: model.Settings{}}, users)
	p := testPipelineWithNotify(&model.JobNotify{
		Enabled: true,
		People:  []string{"zhangsan"},
		Targets: []model.NotifyTarget{{Channel: "wecom", URL: srv.URL}},
		Events:  model.NotifyEvents{OnFailure: true},
	})
	cur := &model.Build{ID: "7", PipelineID: "p1", Status: model.BuildFailed}
	res := svc.Notify(context.Background(), p, cur, nil)
	if len(res.Errors) > 0 {
		t.Fatalf("notify errors: %v", res.Errors)
	}
	if len(res.Targets) != 1 {
		t.Fatalf("targets sent = %v", res.Targets)
	}
	content := got["markdown"].(map[string]interface{})["content"].(string)
	for _, want := range []string{"构建失败", "demo", "#7", "<@zs-id>"} {
		if !strings.Contains(content, want) {
			t.Fatalf("content missing %q:\n%s", want, content)
		}
	}
	if line := res.Line(); !strings.Contains(line, "event=failure") {
		t.Fatalf("summary line = %q", line)
	}
}

// 全局事件开关按渠道独立：邮件只发失败、群只发成功时，失败构建不应推送到群。
func TestNotifyGlobalEventsPerChannel(t *testing.T) {
	settings := model.Settings{
		Email:         model.EmailSettings{Enabled: true, Recipients: "a@x.com", NotifyOnFailure: true, NotifyOnSuccess: false},
		WebhookNotify: model.WebhookNotifySettings{Enabled: true, Channel: "dingtalk", URL: "http://h/?access_token=t", NotifyOnFailure: false, NotifyOnSuccess: true},
	}
	svc := NewNotifyService(&fakeSettingsProvider{settings: settings}, nil)
	p := &model.Pipeline{ID: "p", Name: "p"}

	fail := svc.plan(p, &model.Build{ID: "1", Status: model.BuildFailed}, nil)
	if len(fail.emails) != 1 {
		t.Fatalf("failure: emails = %v, want global recipient", fail.emails)
	}
	if len(fail.targets) != 0 {
		t.Fatalf("failure: webhook must be skipped (notify_on_failure=false), got %d", len(fail.targets))
	}

	ok := svc.plan(p, &model.Build{ID: "2", Status: model.BuildSuccess}, nil)
	if len(ok.targets) != 1 {
		t.Fatalf("success: targets = %d, want 1", len(ok.targets))
	}
	if len(ok.emails) != 0 {
		t.Fatalf("success: email must be skipped (notify_on_success=false), got %v", ok.emails)
	}
}

// 多个群并发发送：全部成功时结果汇总且顺序稳定。
func TestNotifyParallelTargets(t *testing.T) {
	var mu sync.Mutex
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits++
		mu.Unlock()
		_, _ = w.Write([]byte(`{"errcode":0}`))
	}))
	defer srv.Close()

	svc := NewNotifyService(&fakeSettingsProvider{settings: model.Settings{}}, nil)
	targets := make([]model.NotifyTarget, 0, 6)
	for i := 0; i < 6; i++ {
		targets = append(targets, model.NotifyTarget{Channel: "wecom", URL: fmt.Sprintf("%s/hook%d", srv.URL, i)})
	}
	p := testPipelineWithNotify(&model.JobNotify{
		Enabled: true,
		Targets: targets,
		Events:  model.NotifyEvents{OnFailure: true},
	})
	res := svc.Notify(context.Background(), p, &model.Build{ID: "9", PipelineID: "p", Status: model.BuildFailed}, nil)
	if len(res.Errors) != 0 {
		t.Fatalf("errors: %v", res.Errors)
	}
	if len(res.Targets) != 6 {
		t.Fatalf("targets = %v, want 6", res.Targets)
	}
	if hits != 6 {
		t.Fatalf("hits = %d, want 6", hits)
	}
	if !sort.StringsAreSorted(res.Targets) {
		t.Fatalf("targets not sorted: %v", res.Targets)
	}
}

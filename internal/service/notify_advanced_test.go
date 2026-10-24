package service

import (
	"fmt"
	"testing"
	"time"

	"ci-platform/internal/model"
)

// fakeRecorder 记录写入的通知记录（模拟 data/notify.log）。
type fakeRecorder struct {
	records []model.NotifyRecord
}

func (f *fakeRecorder) Append(records []model.NotifyRecord) error {
	f.records = append(f.records, records...)
	return nil
}

func (f *fakeRecorder) Tail(limit int) ([]model.NotifyRecord, error) {
	out := make([]model.NotifyRecord, 0, limit)
	for i := len(f.records) - 1; i >= 0 && len(out) < limit; i-- {
		out = append(out, f.records[i])
	}
	return out, nil
}

// failedBuild 构造一次失败的构建（带完成时间），用于连败/抑制测试。
func failedBuild(id string, finished time.Time) *model.Build {
	start := finished.Add(-time.Minute)
	return &model.Build{
		ID:         id,
		PipelineID: "p1",
		Status:     model.BuildFailed,
		StartedAt:  &start,
		FinishedAt: &finished,
	}
}

func testUsers() *fakeUserDir {
	return &fakeUserDir{users: map[string]model.RbacUser{
		"alice": {Name: "alice", Email: "alice@example.com", WecomID: "alice-wecom", Phone: "13800000001"},
		"bob":   {Name: "bob", Email: "bob@example.com", WecomID: "bob-wecom", Phone: "13800000002"},
		"boss":  {Name: "boss", Email: "boss@example.com"},
	}}
}

// 值班轮值：失败时把当周值班人加入收件人并 @ 他。
func TestNotifyOnCallMentions(t *testing.T) {
	start := time.Now().AddDate(0, 0, -1).Format("2006-01-02")
	svc := NewNotifyService(&fakeSettingsProvider{settings: model.Settings{
		Email: model.EmailSettings{Enabled: true, NotifyOnFailure: true},
	}}, testUsers())
	p := testPipelineWithNotify(&model.JobNotify{
		Enabled: true,
		Events:  model.NotifyEvents{OnFailure: true},
		OnCall: &model.OnCallConfig{
			Enabled:    true,
			Rotation:   []string{"alice", "bob"},
			StartDate:  start,
			PeriodDays: 7,
		},
	})
	cur := &model.Build{ID: "1", PipelineID: "p1", Status: model.BuildFailed}
	plan := svc.plan(p, cur, nil)
	if plan.onCall != "alice" {
		t.Fatalf("onCall = %q, want alice", plan.onCall)
	}
	if len(plan.emails) != 1 || plan.emails[0] != "alice@example.com" {
		t.Fatalf("emails = %v, want on-call email", plan.emails)
	}
	if got := plan.mentionsFor("wecom"); len(got) != 1 || got[0] != "alice-wecom" {
		t.Fatalf("wecom mentions = %v, want alice-wecom", got)
	}
	// 成功事件不 @ 值班人，避免刷屏。
	okPlan := svc.plan(&model.Pipeline{ID: "p1", Name: "demo", Notify: &model.JobNotify{
		Enabled: true,
		Events:  model.NotifyEvents{OnSuccess: true},
		OnCall:  p.Notify.OnCall,
	}}, &model.Build{ID: "2", Status: model.BuildSuccess}, nil)
	if okPlan.onCall != "" || len(okPlan.emails) != 0 {
		t.Fatalf("success must not notify on-call: onCall=%q emails=%v", okPlan.onCall, okPlan.emails)
	}
}

// 连续失败计数：从历史中往前数，遇到非失败即停止。
func TestFailureStreak(t *testing.T) {
	now := time.Now()
	cur := failedBuild("4", now)
	history := []*model.Build{
		cur,
		failedBuild("3", now.Add(-2*time.Minute)),
		failedBuild("2", now.Add(-4*time.Minute)),
		{ID: "1", Status: model.BuildSuccess},
	}
	streak, prevAt := failureStreak(cur, history)
	if streak != 3 {
		t.Fatalf("streak = %d, want 3", streak)
	}
	if prevAt == nil || !prevAt.Equal(*history[1].FinishedAt) {
		t.Fatalf("prevFailedAt = %v, want build 3 finish", prevAt)
	}
	// 成功构建没有连败概念。
	if s, _ := failureStreak(&model.Build{ID: "5", Status: model.BuildSuccess}, history); s != 0 {
		t.Fatalf("success streak = %d, want 0", s)
	}
	// 当前构建尚未落盘时，把整个列表当作更早的构建。
	if s, _ := failureStreak(failedBuild("6", now), history); s != 4 {
		t.Fatalf("unlisted build streak = %d, want 4", s)
	}
}

// 抑制窗口：窗口内的重复失败被合并（不发邮件/群），窗口外照常发送。
func TestNotifySuppressionWindow(t *testing.T) {
	svc := NewNotifyService(&fakeSettingsProvider{settings: model.Settings{
		Email: model.EmailSettings{Enabled: true, Recipients: "team@example.com", NotifyOnFailure: true},
	}}, testUsers())
	p := testPipelineWithNotify(&model.JobNotify{
		Enabled:           true,
		Emails:            []string{"team@example.com"},
		Events:            model.NotifyEvents{OnFailure: true},
		SuppressWindowMin: 10,
	})
	now := time.Now()
	cur := failedBuild("2", now)
	// 上一轮失败在 3 分钟前 → 在 10 分钟窗口内 → 抑制。
	near := []*model.Build{cur, failedBuild("1", now.Add(-3*time.Minute))}
	if plan := svc.planWithHistory(p, cur, nil, near); !plan.suppressed {
		t.Fatalf("expected suppressed within window, got streak=%d", plan.streak)
	}
	// 上一轮失败在 30 分钟前 → 窗口外 → 正常发送。
	far := []*model.Build{cur, failedBuild("1", now.Add(-30*time.Minute))}
	if plan := svc.planWithHistory(p, cur, nil, far); plan.suppressed {
		t.Fatal("must not suppress outside the window")
	}
	// 窗口为 0 = 关闭抑制。
	p.Notify.SuppressWindowMin = 0
	if plan := svc.planWithHistory(p, cur, nil, near); plan.suppressed {
		t.Fatal("window=0 must disable suppression")
	}
}

// 升级策略：连败达到阈值时追加升级对象；且升级优先于抑制。
func TestNotifyEscalation(t *testing.T) {
	svc := NewNotifyService(&fakeSettingsProvider{settings: model.Settings{
		Email: model.EmailSettings{Enabled: true, NotifyOnFailure: true},
	}}, testUsers())
	p := testPipelineWithNotify(&model.JobNotify{
		Enabled:           true,
		People:            []string{"alice"},
		Events:            model.NotifyEvents{OnFailure: true},
		SuppressWindowMin: 10,
		Escalation: &model.EscalationConfig{
			Enabled: true,
			After:   3,
			People:  []string{"boss"},
			Targets: []model.NotifyTarget{{Channel: "dingtalk", URL: "http://h/escalate"}},
		},
	})
	now := time.Now()
	cur := failedBuild("3", now)
	history := []*model.Build{
		cur,
		failedBuild("2", now.Add(-time.Minute)),
		failedBuild("1", now.Add(-2*time.Minute)),
	}
	plan := svc.planWithHistory(p, cur, nil, history)
	if !plan.escalated {
		t.Fatalf("expected escalation at streak=%d", plan.streak)
	}
	if plan.suppressed {
		t.Fatal("escalation must override suppression")
	}
	if len(plan.emails) != 2 {
		t.Fatalf("emails = %v, want alice+boss", plan.emails)
	}
	found := false
	for _, tg := range plan.targets {
		if tg.url == "http://h/escalate" {
			found = true
		}
	}
	if !found {
		t.Fatalf("escalation target missing: %+v", plan.targets)
	}

	// 未达阈值（2 次）不升级。
	cur2 := failedBuild("2", now)
	history2 := []*model.Build{cur2, failedBuild("1", now.Add(-time.Minute))}
	if plan := svc.planWithHistory(p, cur2, nil, history2); plan.escalated {
		t.Fatalf("must not escalate at streak=%d", plan.streak)
	}

	// 阈值下限为 2（1 次失败不需要升级）。
	if got := (model.EscalationConfig{Enabled: true, After: 1}).MinFailures(); got != 3 {
		t.Fatalf("MinFailures(1) = %d, want default 3", got)
	}
}

// 分支路由：命中分支才叠加人员/邮箱，提交者路由使用 git author 邮箱。
func TestNotifyBranchRouting(t *testing.T) {
	svc := NewNotifyService(&fakeSettingsProvider{settings: model.Settings{
		Email: model.EmailSettings{Enabled: true, NotifyOnFailure: true},
	}}, testUsers())
	p := testPipelineWithNotify(&model.JobNotify{
		Enabled: true,
		People:  []string{"alice"},
		Events:  model.NotifyEvents{OnFailure: true},
		Routes: []model.NotifyRoute{
			{Match: "main", People: []string{"bob"}},
			{Match: "feature/*", Committer: true},
		},
	})
	// main 分支：叠加 bob，不带提交者。
	mainBuild := &model.Build{
		ID: "1", Status: model.BuildFailed, Branch: "main",
		Commit: "abc12345", CommitAuthor: "dev", CommitEmail: "dev@example.com",
	}
	plan := svc.plan(p, mainBuild, nil)
	if len(plan.emails) != 2 {
		t.Fatalf("main emails = %v, want alice+bob", plan.emails)
	}
	// feature 分支：只叠加提交者邮箱（alice 来自基础配置）。
	featBuild := &model.Build{
		ID: "2", Status: model.BuildFailed, Branch: "feature/login",
		CommitEmail: "dev@example.com",
	}
	plan = svc.plan(p, featBuild, nil)
	if len(plan.emails) != 2 {
		t.Fatalf("feature emails = %v, want alice+committer", plan.emails)
	}
	hasCommitter := false
	for _, e := range plan.emails {
		if e == "dev@example.com" {
			hasCommitter = true
		}
	}
	if !hasCommitter {
		t.Fatalf("feature emails must include committer: %v", plan.emails)
	}
	if plan.branch != "feature/login" {
		t.Fatalf("branch = %q", plan.branch)
	}
}

// 通知记录：抑制也会落一条 suppressed 记录，投递失败逐收件人记录。
func TestNotifyRecordsSuppressed(t *testing.T) {
	rec := &fakeRecorder{}
	svc := NewNotifyService(&fakeSettingsProvider{settings: model.Settings{}}, testUsers())
	svc.SetRecorder(rec)
	now := time.Now()
	p := testPipelineWithNotify(&model.JobNotify{
		Enabled:           true,
		Emails:            []string{"team@example.com"},
		Events:            model.NotifyEvents{OnFailure: true},
		SuppressWindowMin: 10,
	})
	cur := failedBuild("2", now)
	svc.planWithHistory(p, cur, nil, []*model.Build{cur, failedBuild("1", now.Add(-time.Minute))})
	// 直接走 Notify 会读取 history（未注入），这里验证 record 路径：手动标记抑制。
	plan := svc.planWithHistory(p, cur, nil, []*model.Build{cur, failedBuild("1", now.Add(-time.Minute))})
	if !plan.suppressed {
		t.Fatalf("expected suppressed, streak=%d", plan.streak)
	}
	svc.record(p, cur, plan, model.NotifyRecord{
		Channel:   "",
		Recipient: "suppressed",
		Status:    model.NotifyStatusSuppressed,
	})
	if len(rec.records) != 1 {
		t.Fatalf("records = %d, want 1", len(rec.records))
	}
	r := rec.records[0]
	if r.Status != model.NotifyStatusSuppressed || r.ID == 0 || r.PipelineID != "p1" || r.BuildID != "2" {
		t.Fatalf("unexpected record: %+v", r)
	}
	if r.Streak != 2 {
		t.Fatalf("record streak = %d, want 2", r.Streak)
	}
}

// 通知历史的过滤：按任务 / 收件人 / 状态。
func TestNotifyHistoryFilter(t *testing.T) {
	rec := &fakeRecorder{}
	svc := NewNotifyService(&fakeSettingsProvider{settings: model.Settings{}}, testUsers())
	svc.SetRecorder(rec)
	base := model.NotifyRecord{Time: time.Now().UTC(), Event: model.NotifyEventFailure}
	mk := func(id int64, pipeline, recipient, status string) model.NotifyRecord {
		r := base
		r.ID = id
		r.PipelineID = pipeline
		r.PipelineName = pipeline
		r.Recipient = recipient
		r.Status = status
		return r
	}
	_ = rec.Append([]model.NotifyRecord{
		mk(1, "web", "alice@example.com", model.NotifyStatusSent),
		mk(2, "web", "bob@example.com", model.NotifyStatusFailed),
		mk(3, "api", "alice@example.com", model.NotifyStatusSent),
	})

	all, err := svc.History(10, "", "", "")
	if err != nil || len(all) != 3 {
		t.Fatalf("all = %d (err=%v), want 3", len(all), err)
	}
	web, _ := svc.History(10, "web", "", "")
	if len(web) != 2 {
		t.Fatalf("pipeline filter = %d, want 2", len(web))
	}
	failed, _ := svc.History(10, "", "", model.NotifyStatusFailed)
	if len(failed) != 1 || failed[0].Recipient != "bob@example.com" {
		t.Fatalf("status filter = %+v", failed)
	}
	byPerson, _ := svc.History(10, "", "ALICE", "")
	if len(byPerson) != 2 {
		t.Fatalf("recipient filter = %d, want 2 (case-insensitive)", len(byPerson))
	}
	if limited, _ := svc.History(1, "", "", ""); len(limited) != 1 {
		t.Fatalf("limit = %d, want 1", len(limited))
	}
}

// 任务级消息模板：占位符替换，未知占位符保持原样。
func TestNotifyTemplateRendering(t *testing.T) {
	svc := NewNotifyService(&fakeSettingsProvider{settings: model.Settings{}}, testUsers())
	now := time.Now()
	start := now.Add(-90 * time.Second)
	b := &model.Build{
		ID: "7", PipelineID: "p1", Status: model.BuildFailed,
		StartedAt: &start, FinishedAt: &now, Branch: "main", Commit: "abc12345", CommitAuthor: "dev",
	}
	p := testPipelineWithNotify(&model.JobNotify{
		Enabled:           true,
		Events:            model.NotifyEvents{OnFailure: true},
		SuppressWindowMin: 10,
		Template:          "任务 {{job}} #{{build}} 在 {{branch}} 失败（连败 {{streak}}）{{unknown}}",
	})
	plan := svc.planWithHistory(p, b, nil, []*model.Build{b, failedBuild("6", now.Add(-time.Minute))})
	body := renderNotifyMailBody(p, b, model.NotifyEventFailure, "http://ci.example.com/", plan)
	for _, want := range []string{"任务 demo #7 在 main 失败", "连败 2", "{{unknown}}", "/pipelines/p1/builds/7"} {
		if !contains(body, want) {
			t.Fatalf("body missing %q:\n%s", want, body)
		}
	}
}

func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && fmt.Sprintf("%s", s) != "" && indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

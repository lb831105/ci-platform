package service

import (
	"ci-platform/internal/pkg/logger"
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"ci-platform/internal/executor"
	"ci-platform/internal/model"
	"ci-platform/internal/pkg"
)

// UserDirectory 提供平台内用户的联系方式（由 RBACService 实现）。
// 任务通知按用户名引用人员时，用它解析邮箱 / 企微 ID / 手机号。
type UserDirectory interface {
	Contacts(names []string) map[string]model.RbacUser
}

// BuildLister 提供任务的历史构建（用于「连续失败计数」与「抑制窗口」判断）。
// 由 BuildService 的 BuildStore 满足。
type BuildLister interface {
	ListByPipeline(ctx context.Context, pipelineID string) ([]*model.Build, error)
}

// NotifyRecorder 持久化通知投递记录（data/notify.log），用于「谁漏收了告警」。
type NotifyRecorder interface {
	Append([]model.NotifyRecord) error
	Tail(limit int) ([]model.NotifyRecord, error)
}

// NotifyService 构建结果通知：把「项目」与「人员/群」绑定后统一发送。
//
// 解析优先级（对齐 Jenkins 的 per-project notification 语义）：
//   - 任务配置了通知（notify.enabled）→ 以任务级收件人/群为准；UseGlobal 时再叠加全局默认。
//   - 任务未配置 → 回落到「系统配置」的全局邮件与全局 Webhook 机器人（兼容旧行为）。
//
// 在此基础上叠加四种运维能力（仅任务级配置生效）：
//   - 值班轮值（on_call）：失败时 @ 当周值班人；
//   - 升级（escalation）：连续失败达到阈值追加通知上级/更大群；
//   - 抑制（suppress_window_min）：窗口内重复失败合并，不再刷屏；
//   - 分支路由（routes）：按分支把人员/群叠加进来（如 feature/* 只通知提交者）。
//
// 事件过滤：failure / success / fixed / aborted；每个群可单独订阅事件。
type NotifyService struct {
	settings SettingsProvider
	users    UserDirectory
	history  BuildLister
	records  NotifyRecorder

	seqMu sync.Mutex
	seq   int64 // 通知记录 ID 单调递增（重启后从文件恢复）
}

// NewNotifyService 创建通知服务；users 为 nil 时无法解析用户名（仅支持原始邮箱）。
func NewNotifyService(settings SettingsProvider, users UserDirectory) *NotifyService {
	return &NotifyService{settings: settings, users: users}
}

// SetHistory 注入构建历史读取器（用于连续失败计数与抑制窗口）。
func (s *NotifyService) SetHistory(h BuildLister) { s.history = h }

// SetRecorder 注入通知记录存储（data/notify.log）。
func (s *NotifyService) SetRecorder(r NotifyRecorder) { s.records = r }

// NotifyResult 是一次通知发送的摘要（写入构建日志，便于追溯「通知了谁」）。
type NotifyResult struct {
	Event      string
	Emails     []string
	Targets    []string // 形如 "dingtalk#1"
	Errors     []string
	Streak     int    // 连续失败次数（失败事件）
	Escalated  bool   // 是否触发升级
	Suppressed bool   // 是否被抑制窗口合并
	OnCall     string // 当周值班人
	// Warnings 是配置质量问题（人员缺邮箱 / 用户名不存在），写入构建日志便于排查「谁没收到」。
	Warnings []string
}

// Line 渲染为一行人类可读摘要。
func (r NotifyResult) Line() string {
	if r.Event == "" {
		return ""
	}
	if r.Suppressed {
		return fmt.Sprintf("event=%s；连续失败 %d 次，处于抑制窗口内已合并（未重复发送）", r.Event, r.Streak)
	}
	parts := make([]string, 0, 5)
	if r.OnCall != "" {
		parts = append(parts, "值班 "+r.OnCall)
	}
	if r.Streak > 1 {
		parts = append(parts, fmt.Sprintf("连续失败 %d 次", r.Streak))
	}
	if r.Escalated {
		parts = append(parts, "已升级通知")
	}
	if len(r.Emails) > 0 {
		parts = append(parts, fmt.Sprintf("邮件 %d 人（%s）", len(r.Emails), strings.Join(r.Emails, ", ")))
	}
	if len(r.Targets) > 0 {
		parts = append(parts, fmt.Sprintf("群 %d 个（%s）", len(r.Targets), strings.Join(r.Targets, ", ")))
	}
	if len(r.Errors) > 0 {
		parts = append(parts, fmt.Sprintf("失败 %d 个（%s）", len(r.Errors), strings.Join(r.Errors, "；")))
	}
	if len(r.Warnings) > 0 {
		parts = append(parts, "警告："+strings.Join(r.Warnings, "；"))
	}
	if len(parts) == 0 {
		return ""
	}
	return "event=" + r.Event + "；" + strings.Join(parts, "；")
}

// notifyPlan 是解析后的发送计划。
type notifyPlan struct {
	event   string
	emails  []string
	targets []notifyTargetPlan
	people  []model.RbacUser // 被引用的人员（用于 @ 提醒）

	streak     int
	escalated  bool
	suppressed bool
	onCall     string
	branch     string
	template   string
	windowMin  int

	// 配置质量：被引用但收不到邮件的人员（缺邮箱）与根本不存在的用户名。
	// 这些是「选了人却静默收不到」的根因，必须显式暴露到构建日志。
	missingEmail  []string
	unknownPeople []string
}

type notifyTargetPlan struct {
	channel string
	url     string
	label   string
}

// plan 解析本次构建应发送的通知（无构建历史，测试与兼容用）。
func (s *NotifyService) plan(p *model.Pipeline, b *model.Build, prev *model.Build) notifyPlan {
	return s.planWithHistory(p, b, prev, nil)
}

// planWithHistory 是 plan 的实现：history 为按时间倒序的构建列表（可为 nil）。
func (s *NotifyService) planWithHistory(p *model.Pipeline, b *model.Build, prev *model.Build, history []*model.Build) notifyPlan {
	settings := s.settings.Current()
	event := model.BuildEvent(b, prev)
	if event == model.NotifyEventNone {
		return notifyPlan{}
	}
	job := p.Notify
	jobEnabled := job != nil && job.Enabled

	// 事件集合按渠道分别计算：任务级统一使用 job.Events；
	// 未配置时各自回落自己的全局开关（邮件与 Webhook 的失败/成功开关互相独立，
	// 不能合并——否则「邮件只发失败、群只发成功」的配置会被互相污染）。
	var emailEvents, webhookEvents model.NotifyEvents
	if jobEnabled {
		emailEvents = job.Events
		webhookEvents = job.Events
	} else {
		emailEvents = model.NotifyEvents{
			OnFailure: settings.Email.NotifyOnFailure,
			OnSuccess: settings.Email.NotifyOnSuccess,
			// 全局开关只有成功/失败两个维度：修复视为成功，取消失不通知（避免噪音）。
			OnFixed: settings.Email.NotifyOnSuccess,
		}
		webhookEvents = model.NotifyEvents{
			OnFailure: settings.WebhookNotify.NotifyOnFailure,
			OnSuccess: settings.WebhookNotify.NotifyOnSuccess,
			OnFixed:   settings.WebhookNotify.NotifyOnSuccess,
		}
	}
	if !emailEvents.Matches(event) && !webhookEvents.Matches(event) {
		return notifyPlan{}
	}

	out := notifyPlan{event: event}
	if b != nil {
		out.branch = b.Branch
	}
	if jobEnabled {
		out.template = job.Template
		out.windowMin = job.SuppressWindowMin
	}

	// ---- 邮件收件人 ----
	emailSet := map[string]bool{}
	addEmail := func(addr string) {
		addr = strings.TrimSpace(addr)
		if addr == "" || !strings.Contains(addr, "@") {
			return
		}
		key := strings.ToLower(addr)
		if emailSet[key] {
			return
		}
		emailSet[key] = true
		out.emails = append(out.emails, addr)
	}
	// addPerson 按用户名解析联系方式：记录人员（用于 @）并追加其邮箱。
	addPerson := func(name string) {
		name = strings.TrimSpace(name)
		if name == "" {
			return
		}
		if s.users == nil {
			// 无法解析用户名时按邮箱处理（值班表允许直接写邮箱）。
			if strings.Contains(name, "@") {
				addEmail(name)
			}
			return
		}
		contacts := s.users.Contacts([]string{name})
		u, ok := contacts[name]
		if !ok {
			// 用户名不存在：若本身就是邮箱则直接使用，否则记录下来（避免静默丢告警）。
			if strings.Contains(name, "@") {
				addEmail(name)
			} else {
				out.unknownPeople = append(out.unknownPeople, name)
			}
			return
		}
		for _, existing := range out.people {
			if existing.Name == u.Name {
				return
			}
		}
		out.people = append(out.people, u)
		if u.Email != "" {
			addEmail(u.Email)
		} else {
			// 人员仍参与群 @ 提醒，但邮件肯定收不到——记录下来让日志可解释。
			out.missingEmail = append(out.missingEmail, u.Name)
		}
	}
	if jobEnabled {
		for _, name := range job.People {
			addPerson(name)
		}
		for _, e := range job.Emails {
			addEmail(e)
		}
		if job.UseGlobal && settings.Email.Enabled {
			for _, e := range parseRecipients(settings.Email.Recipients, settings.Email.DefaultSuffix) {
				addEmail(e)
			}
		}
	} else if settings.Email.Enabled {
		for _, e := range parseRecipients(settings.Email.Recipients, settings.Email.DefaultSuffix) {
			addEmail(e)
		}
	}

	// ---- Webhook 群目标 ----
	targetSet := map[string]bool{}
	addTarget := func(channel, url string, targetEvents model.NotifyEvents) {
		url = strings.TrimSpace(url)
		if url == "" {
			return
		}
		// 目标未单独配置事件时继承该渠道的事件集合。
		ev := targetEvents
		if ev.IsZero() {
			ev = webhookEvents
		}
		if !ev.Matches(event) {
			return
		}
		key := strings.ToLower(channel + "|" + url)
		if targetSet[key] {
			return
		}
		targetSet[key] = true
		ch := channel
		if ch == "" {
			ch = "slack"
		}
		out.targets = append(out.targets, notifyTargetPlan{
			channel: ch,
			url:     url,
			label:   fmt.Sprintf("%s#%d", ch, len(out.targets)+1),
		})
	}
	if jobEnabled {
		for _, t := range job.Targets {
			addTarget(t.Channel, t.URL, t.Events)
		}
		if job.UseGlobal && settings.WebhookNotify.Enabled {
			// 全局目标也尊重它自己的失败/成功开关。
			addTarget(settings.WebhookNotify.Channel, settings.WebhookNotify.URL, globalWebhookEvents(settings))
		}
	} else if settings.WebhookNotify.Enabled {
		addTarget(settings.WebhookNotify.Channel, settings.WebhookNotify.URL, model.NotifyEvents{})
	}

	// ---- 连续失败计数（失败事件才有意义）----
	var prevFailedAt *time.Time
	if event == model.NotifyEventFailure {
		out.streak, prevFailedAt = failureStreak(b, history)
	}

	// ---- 值班轮值：失败/取消时 @ 当周值班人 ----
	failureish := event == model.NotifyEventFailure || event == model.NotifyEventAborted
	onCall := ""
	if jobEnabled && job.OnCall != nil && job.OnCall.Enabled {
		onCall = job.OnCall.Current(time.Now())
	}
	// 只在失败/取消时把值班人写入计划：成功通知里出现「值班 XXX」是噪音。
	if onCall != "" && failureish {
		out.onCall = onCall
		addPerson(onCall)
	}

	// ---- 分支路由：命中分支时把该路由的人员/群叠加进来 ----
	if jobEnabled && b != nil {
		for _, r := range job.Routes {
			if !r.MatchBranch(out.branch) {
				continue
			}
			for _, name := range r.People {
				addPerson(name)
			}
			for _, e := range r.Emails {
				addEmail(e)
			}
			for _, t := range r.Targets {
				addTarget(t.Channel, t.URL, t.Events)
			}
			// 提交者路由：把 git author 的邮箱作为收件人（feature/* 只通知提交者）。
			if r.Committer && b.CommitEmail != "" && failureish {
				addEmail(b.CommitEmail)
			}
			if r.OnCall && onCall != "" && failureish {
				addPerson(onCall)
			}
		}
	}

	// ---- 抑制窗口：窗口内同一任务的连续失败合并，避免刷屏 ----
	if event == model.NotifyEventFailure && jobEnabled && job.SuppressWindowMin > 0 &&
		b != nil && b.FinishedAt != nil && prevFailedAt != nil && out.streak > 1 {
		window := time.Duration(job.SuppressWindowMin) * time.Minute
		if b.FinishedAt.Sub(*prevFailedAt) <= window {
			out.suppressed = true
		}
	}

	// ---- 升级策略：连续失败达到阈值 → 追加通知上级/更大群 ----
	// 升级优先于抑制：已经「连败 N 次」还继续静默就失去升级的意义。
	if event == model.NotifyEventFailure && jobEnabled && job.Escalation != nil {
		if after := job.Escalation.MinFailures(); after > 0 && out.streak >= after {
			out.escalated = true
			out.suppressed = false
			for _, name := range job.Escalation.People {
				addPerson(name)
			}
			for _, e := range job.Escalation.Emails {
				addEmail(e)
			}
			for _, t := range job.Escalation.Targets {
				// 升级目标只用于失败告警（正是触发条件的场景）。
				addTarget(t.Channel, t.URL, model.NotifyEvents{OnFailure: true})
			}
		}
	}

	// 邮件未启用或该渠道未订阅此事件时清空收件人
	// （避免配置了人但 SMTP 未开时反复失败）。
	if !settings.Email.Enabled || !emailEvents.Matches(event) {
		out.emails = nil
	}
	return out
}

// warnings 汇总配置质量问题（去重、稳定顺序），供构建日志与测试发送提示。
func (p notifyPlan) warnings() []string {
	out := make([]string, 0, 2)
	if len(p.missingEmail) > 0 {
		names := dedupeStrings(p.missingEmail)
		out = append(out, fmt.Sprintf("人员 %s 未配置邮箱，收不到邮件（请到用户管理/个人资料补全）",
			strings.Join(names, ", ")))
	}
	if len(p.unknownPeople) > 0 {
		names := dedupeStrings(p.unknownPeople)
		out = append(out, fmt.Sprintf("用户名 %s 不存在，已跳过（外部人员请改用「额外邮箱」）",
			strings.Join(names, ", ")))
	}
	return out
}

// dedupeStrings 去重并保持顺序。
func dedupeStrings(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, v := range in {
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

// failureStreak 返回「截至当前构建的连续失败次数」与「上一次失败构建的完成时间」。
// history 必须按时间倒序（最新在前）。当前构建可能已不在列表里（测试/边界），
// 此时按 b.Status 从最新一条往前数。
func failureStreak(b *model.Build, history []*model.Build) (int, *time.Time) {
	if b == nil || b.Status != model.BuildFailed {
		return 0, nil
	}
	if len(history) == 0 {
		return 1, nil
	}
	// 定位当前构建：找到 ID 相同的项，从它的下一条（更早）开始计数。
	start := -1
	for i, item := range history {
		if item != nil && item.ID == b.ID {
			start = i + 1
			break
		}
	}
	if start < 0 {
		start = 0 // 当前构建尚未落盘：把整个列表视为更早的构建
	}
	streak := 1
	var prevFailedAt *time.Time
	for i := start; i < len(history); i++ {
		item := history[i]
		if item == nil {
			continue
		}
		if item.Status != model.BuildFailed {
			break
		}
		if streak == 1 && item.FinishedAt != nil {
			prevFailedAt = item.FinishedAt
		}
		streak++
	}
	return streak, prevFailedAt
}

// globalWebhookEvents 把全局 Webhook 的成功/失败开关转为事件集合。
func globalWebhookEvents(settings model.Settings) model.NotifyEvents {
	return model.NotifyEvents{
		OnFailure: settings.WebhookNotify.NotifyOnFailure,
		OnSuccess: settings.WebhookNotify.NotifyOnSuccess,
		OnFixed:   settings.WebhookNotify.NotifyOnSuccess,
	}
}

// mentionsFor 返回某个渠道要 @ 的人员标识（钉钉用手机号、企微用 userid）。
// 失败与取消事件才 @ 人，成功/修复不打扰。
func (p notifyPlan) mentionsFor(channel string) []string {
	if p.event != model.NotifyEventFailure && p.event != model.NotifyEventAborted {
		return nil
	}
	return mentionsForPeople(channel, p.people)
}

// Notify 发送构建结果通知；失败只记录，绝不影响构建结果。
//
// 性能约束：此函数在构建 worker 内同步执行，慢群（每个 10s 超时）
// 会拖慢后续排队构建与优雅停机。因此：
//   - 全次发送限制在 notifyTotalTimeout 内；
//   - 多个群并发发送（上限 notifyParallel），失败互不影响。
func (s *NotifyService) Notify(ctx context.Context, p *model.Pipeline, b *model.Build, prev *model.Build) NotifyResult {
	var history []*model.Build
	if s.history != nil && p != nil {
		if list, err := s.history.ListByPipeline(ctx, p.ID); err == nil {
			history = list
		}
	}
	plan := s.planWithHistory(p, b, prev, history)
	res := NotifyResult{
		Event:      plan.event,
		Streak:     plan.streak,
		Escalated:  plan.escalated,
		Suppressed: plan.suppressed,
		OnCall:     plan.onCall,
		Warnings:   plan.warnings(),
	}
	if plan.event == model.NotifyEventNone {
		return res
	}
	settings := s.settings.Current()

	// 抑制窗口内：不重复发送，但仍落一条记录，便于解释「为什么没收到第二条」。
	if plan.suppressed {
		s.record(p, b, plan, model.NotifyRecord{
			Channel:   "",
			Recipient: "suppressed",
			Status:    model.NotifyStatusSuppressed,
			Error:     fmt.Sprintf("连续失败 %d 次，%d 分钟抑制窗口内已合并", plan.streak, plan.windowMin),
		})
		return res
	}

	ctx, cancel := context.WithTimeout(ctx, notifyTotalTimeout)
	defer cancel()

	// 邮件
	if len(plan.emails) > 0 {
		mailCtx, mailCancel := context.WithTimeout(ctx, notifyMailTimeout)
		subject := renderSubject(settings.Email.SubjectTemplate, p, b)
		if label := notifyEventLabel(plan.event); label != "" {
			subject = fmt.Sprintf("[%s] %s", label, subject)
		}
		body := renderNotifyMailBody(p, b, plan.event, settings.JenkinsURL, plan)
		if err := executor.SendMailContext(mailCtx, effectiveEmail(settings), plan.emails, subject, body); err != nil {
			res.Errors = append(res.Errors, "email: "+err.Error())
			logger.Errorf("[notify] email failed (pipeline=%s build=%s): %v", p.ID, b.ID, err)
			for _, addr := range plan.emails {
				s.record(p, b, plan, model.NotifyRecord{
					Channel:   "email",
					Recipient: addr,
					Status:    model.NotifyStatusFailed,
					Error:     err.Error(),
				})
			}
		} else {
			res.Emails = plan.emails
			for _, addr := range plan.emails {
				s.record(p, b, plan, model.NotifyRecord{
					Channel:   "email",
					Recipient: addr,
					Status:    model.NotifyStatusSent,
				})
			}
		}
		mailCancel()
	}

	// Webhook 群：并发发送，结果汇总需要加锁。
	var (
		mu  sync.Mutex
		sem = make(chan struct{}, notifyParallel)
		wg  sync.WaitGroup
	)
	for _, t := range plan.targets {
		wg.Add(1)
		sem <- struct{}{}
		go func(t notifyTargetPlan) {
			defer wg.Done()
			defer func() { <-sem }()
			sendCtx, sendCancel := context.WithTimeout(ctx, notifyWebhookTimeout)
			err := executor.SendWebhookNotifyMsg(sendCtx, t.channel, t.url, executor.NotifyMessage{
				Pipeline:   p,
				Build:      b,
				JenkinsURL: settings.JenkinsURL,
				Event:      plan.event,
				Mentions:   plan.mentionsFor(t.channel),
				Template:   plan.template,
				Streak:     plan.streak,
				OnCall:     plan.onCall,
			})
			sendCancel()
			mu.Lock()
			if err != nil {
				res.Errors = append(res.Errors, t.channel+": "+err.Error())
				mu.Unlock()
				logger.Errorf("[notify] webhook failed (channel=%s pipeline=%s build=%s): %v",
					t.channel, p.ID, b.ID, err)
				s.record(p, b, plan, model.NotifyRecord{
					Channel:   t.channel,
					Recipient: t.label,
					Status:    model.NotifyStatusFailed,
					Error:     err.Error(),
				})
				return
			}
			res.Targets = append(res.Targets, t.label)
			mu.Unlock()
			s.record(p, b, plan, model.NotifyRecord{
				Channel:   t.channel,
				Recipient: t.label,
				Status:    model.NotifyStatusSent,
			})
		}(t)
	}
	wg.Wait()
	// 并发完成后按 label 排序，保证日志可重现。
	sort.Strings(res.Targets)
	return res
}

// record 追加一条投递记录（失败只记日志，绝不影响构建）。
func (s *NotifyService) record(p *model.Pipeline, b *model.Build, plan notifyPlan, rec model.NotifyRecord) {
	if s.records == nil {
		return
	}
	rec.ID = s.nextID()
	rec.Time = time.Now().UTC()
	if p != nil {
		rec.PipelineID = p.ID
		rec.PipelineName = p.Name
	}
	if b != nil {
		rec.BuildID = b.ID
		rec.Branch = b.Branch
	}
	rec.Event = plan.event
	rec.Streak = plan.streak
	rec.Escalated = plan.escalated
	rec.OnCall = plan.onCall
	if err := s.records.Append([]model.NotifyRecord{rec}); err != nil {
		logger.Errorf("[notify] record append failed: %v", err)
	}
}

// nextID 返回单调递增的记录 ID；首次调用时从文件尾部恢复最大值，
// 保证重启后 ID 继续增长（与审计日志同一策略）。
func (s *NotifyService) nextID() int64 {
	s.seqMu.Lock()
	defer s.seqMu.Unlock()
	if s.seq == 0 && s.records != nil {
		if tail, err := s.records.Tail(1); err == nil && len(tail) > 0 {
			s.seq = tail[0].ID
		}
	}
	s.seq++
	return s.seq
}

// History 返回最近的通知记录（时间倒序），支持按任务/收件人/状态过滤。
// 过滤在服务层完成：记录量有限（limit 上限 1000），避免为每种组合加索引。
func (s *NotifyService) History(limit int, pipeline, recipient, status string) ([]model.NotifyRecord, error) {
	if s.records == nil {
		return []model.NotifyRecord{}, nil
	}
	if limit <= 0 || limit > 1000 {
		limit = 300
	}
	// 过滤条件可能过滤掉大量记录，先多取一些再截断。
	fetch := limit
	if pipeline != "" || recipient != "" || status != "" {
		fetch = 1000
	}
	all, err := s.records.Tail(fetch)
	if err != nil {
		return nil, err
	}
	out := make([]model.NotifyRecord, 0, limit)
	for _, r := range all {
		if pipeline != "" && r.PipelineID != pipeline && r.PipelineName != pipeline {
			continue
		}
		if recipient != "" && !strings.Contains(strings.ToLower(r.Recipient), strings.ToLower(recipient)) {
			continue
		}
		if status != "" && r.Status != status {
			continue
		}
		out = append(out, r)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

// 通知发送的超时与并发约束（避免阻塞构建 worker）。
const (
	notifyTotalTimeout   = 30 * time.Second
	notifyMailTimeout    = 20 * time.Second
	notifyWebhookTimeout = 8 * time.Second
	notifyParallel       = 4
)

// SendTest 向该任务当前配置的收件人与群发送一条测试通知（不受事件过滤）。
// 用于「发送测试通知」按钮：确认项目与人员/群的绑定是否正确。
func (s *NotifyService) SendTest(ctx context.Context, p *model.Pipeline) NotifyResult {
	settings := s.settings.Current()
	now := time.Now().UTC()
	b := &model.Build{
		ID:          "test",
		PipelineID:  p.ID,
		Status:      model.BuildSuccess,
		TriggerType: model.TriggerManual,
		TriggeredBy: "test",
		StartedAt:   &now,
		FinishedAt:  &now,
	}
	res := NotifyResult{Event: model.NotifyEventSuccess}
	job := p.Notify
	jobEnabled := job != nil && job.Enabled

	emailSet := map[string]bool{}
	var emails []string
	addEmail := func(addr string) {
		addr = strings.TrimSpace(addr)
		if addr == "" || !strings.Contains(addr, "@") || emailSet[strings.ToLower(addr)] {
			return
		}
		emailSet[strings.ToLower(addr)] = true
		emails = append(emails, addr)
	}
	var people []model.RbacUser
	addPerson := func(name string) {
		name = strings.TrimSpace(name)
		if name == "" {
			return
		}
		if s.users == nil {
			addEmail(name)
			return
		}
		u, ok := s.users.Contacts([]string{name})[name]
		if !ok {
			if strings.Contains(name, "@") {
				addEmail(name) // 值班表允许直接写邮箱
			} else {
				res.Errors = append(res.Errors, fmt.Sprintf("人员 %s 不存在（外部人员请改用额外邮箱）", name))
			}
			return
		}
		if u.Email == "" {
			res.Errors = append(res.Errors, fmt.Sprintf("人员 %s 未配置邮箱，收不到邮件", name))
		}
		for _, existing := range people {
			if existing.Name == u.Name {
				return
			}
		}
		people = append(people, u)
		addEmail(u.Email)
	}
	if jobEnabled {
		for _, name := range job.People {
			addPerson(name)
		}
		for _, e := range job.Emails {
			addEmail(e)
		}
		// 测试通知也覆盖值班人与升级目标，否则「点了测试没收到」难以排查。
		if job.OnCall != nil && job.OnCall.Enabled {
			if onCall := job.OnCall.Current(time.Now()); onCall != "" {
				res.OnCall = onCall
				addPerson(onCall)
			}
		}
		if job.Escalation != nil && job.Escalation.Enabled {
			for _, name := range job.Escalation.People {
				addPerson(name)
			}
			for _, e := range job.Escalation.Emails {
				addEmail(e)
			}
		}
		if job.UseGlobal {
			for _, e := range parseRecipients(settings.Email.Recipients, settings.Email.DefaultSuffix) {
				addEmail(e)
			}
		}
	} else {
		for _, e := range parseRecipients(settings.Email.Recipients, settings.Email.DefaultSuffix) {
			addEmail(e)
		}
	}
	if !settings.Email.Enabled {
		emails = nil
	}

	type target struct{ channel, url string }
	var targets []target
	if jobEnabled {
		for _, t := range job.Targets {
			if strings.TrimSpace(t.URL) != "" {
				targets = append(targets, target{t.Channel, t.URL})
			}
		}
		if job.Escalation != nil && job.Escalation.Enabled {
			for _, t := range job.Escalation.Targets {
				if strings.TrimSpace(t.URL) != "" {
					targets = append(targets, target{t.Channel, t.URL})
				}
			}
		}
		if job.UseGlobal && settings.WebhookNotify.Enabled {
			targets = append(targets, target{settings.WebhookNotify.Channel, settings.WebhookNotify.URL})
		}
	} else if settings.WebhookNotify.Enabled {
		targets = append(targets, target{settings.WebhookNotify.Channel, settings.WebhookNotify.URL})
	}

	if len(emails) == 0 && len(targets) == 0 {
		res.Errors = append(res.Errors, "该任务未配置任何收件人或群机器人（且未启用全局默认）")
		return res
	}

	if len(emails) > 0 {
		mailCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
		subject := fmt.Sprintf("[测试] %s - 通知配置测试", p.Name)
		body := fmt.Sprintf("这是一条来自 CI Platform 的测试通知。\n\n任务: %s（%s）\n接收人: %s\n",
			p.Name, p.ID, strings.Join(emails, ", "))
		if err := executor.SendMailContext(mailCtx, effectiveEmail(settings), emails, subject, body); err != nil {
			res.Errors = append(res.Errors, "email: "+err.Error())
		} else {
			res.Emails = emails
		}
		cancel()
	}
	for i, t := range targets {
		tctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		err := executor.SendWebhookNotifyMsg(tctx, t.channel, t.url, executor.NotifyMessage{
			Pipeline:   p,
			Build:      b,
			JenkinsURL: settings.JenkinsURL,
			Event:      model.NotifyEventSuccess,
			Mentions:   mentionsForPeople(t.channel, people),
			Template:   "",
		})
		cancel()
		if err != nil {
			res.Errors = append(res.Errors, t.channel+": "+err.Error())
			continue
		}
		res.Targets = append(res.Targets, fmt.Sprintf("%s#%d", t.channel, i+1))
	}
	return res
}

// mentionsForPeople 按渠道挑选 @ 标识（测试通知也用同一规则）。
func mentionsForPeople(channel string, people []model.RbacUser) []string {
	out := make([]string, 0, len(people))
	for _, u := range people {
		switch channel {
		case "dingtalk":
			if u.Phone != "" {
				out = append(out, u.Phone)
			}
		case "wecom":
			if u.WecomID != "" {
				out = append(out, u.WecomID)
			}
		}
	}
	return out
}

// effectiveEmail 返回实际用于发信的邮件配置。
//
// SMTP 服务普遍要求 From 与登录账号同域，因此「系统配置 → 系统管理员邮件地址」在
// 未单独填写发件人时作为兜底——否则界面上那一栏填了也不会有任何效果。
func effectiveEmail(settings model.Settings) model.EmailSettings {
	cfg := settings.Email
	if strings.TrimSpace(cfg.From) == "" {
		cfg.From = strings.TrimSpace(settings.AdminEmail)
	}
	return cfg
}

// notifyEventLabel 事件中文标签（与 executor 保持一致，供标题与摘要使用）。
func notifyEventLabel(event string) string {
	switch event {
	case model.NotifyEventFailure:
		return "失败"
	case model.NotifyEventSuccess:
		return "成功"
	case model.NotifyEventFixed:
		return "已修复"
	case model.NotifyEventAborted:
		return "已取消"
	}
	return ""
}

// renderNotifyMailBody 渲染邮件正文：任务配置了模板时优先用模板。
func renderNotifyMailBody(p *model.Pipeline, b *model.Build, event, jenkinsURL string, plan notifyPlan) string {
	url := ""
	if base := strings.TrimRight(jenkinsURL, "/"); base != "" {
		url = fmt.Sprintf("%s/pipelines/%s/builds/%s", base, p.ID, b.ID)
	}
	if tpl := strings.TrimSpace(plan.template); tpl != "" {
		text := pkg.RenderNotifyTemplate(tpl, notifyTemplateVars(p, b, event, url, plan))
		if url != "" {
			text += "\n\n查看详情: " + url
		}
		return text
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "任务: %s\n构建: #%s\n事件: %s\n状态: %s\n", p.Name, b.ID, notifyEventLabel(event), string(b.Status))
	if b.TriggeredBy != "" {
		fmt.Fprintf(&sb, "触发: %s（%s）\n", b.TriggeredBy, string(b.TriggerType))
	}
	if b.Branch != "" {
		fmt.Fprintf(&sb, "分支: %s", b.Branch)
		if b.Commit != "" {
			fmt.Fprintf(&sb, "（%s", b.Commit)
			if b.CommitAuthor != "" {
				fmt.Fprintf(&sb, " by %s", b.CommitAuthor)
			}
			sb.WriteString("）")
		}
		sb.WriteString("\n")
	}
	if plan.streak > 1 {
		fmt.Fprintf(&sb, "连续失败: %d 次\n", plan.streak)
	}
	if plan.onCall != "" {
		fmt.Fprintf(&sb, "当周值班: %s\n", plan.onCall)
	}
	if b.StartedAt != nil && b.FinishedAt != nil {
		fmt.Fprintf(&sb, "耗时: %s\n", b.FinishedAt.Sub(*b.StartedAt).Round(time.Second))
	}
	if url != "" {
		fmt.Fprintf(&sb, "\n查看详情: %s\n", url)
	}
	return sb.String()
}

// notifyTemplateVars 构造任务级消息模板的变量表（与 pkg.NotifyTemplateVars 对应）。
func notifyTemplateVars(p *model.Pipeline, b *model.Build, event, url string, plan notifyPlan) map[string]string {
	duration := "-"
	if b.StartedAt != nil && b.FinishedAt != nil {
		duration = b.FinishedAt.Sub(*b.StartedAt).Round(time.Second).String()
	}
	streak := ""
	if plan.streak > 1 {
		streak = fmt.Sprintf("%d", plan.streak)
	}
	return map[string]string{
		"job":      p.Name,
		"build":    b.ID,
		"event":    event,
		"status":   string(b.Status),
		"branch":   b.Branch,
		"commit":   b.Commit,
		"author":   b.CommitAuthor,
		"duration": duration,
		"url":      url,
		"oncall":   plan.onCall,
		"streak":   streak,
		"message":  plan.message(),
	}
}

// message 给模板用的「一行摘要」，包含连续失败/升级等高价值信息。
func (p notifyPlan) message() string {
	parts := []string{notifyEventLabel(p.event) + "：" + p.event}
	if p.streak > 1 {
		parts = append(parts, fmt.Sprintf("连续失败 %d 次", p.streak))
	}
	if p.escalated {
		parts = append(parts, "已升级")
	}
	if p.onCall != "" {
		parts = append(parts, "值班 "+p.onCall)
	}
	return strings.Join(parts, "；")
}

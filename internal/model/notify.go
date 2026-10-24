package model

import (
	"strconv"
	"strings"
	"time"
)

// 通知事件：按构建结果决定是否告警，对齐 Jenkins 邮件通知的事件语义。
// Fixed 表示「上次失败/取消 → 本次成功」，用于通知修复，避免成功通知淹没群聊。
type NotifyEvents struct {
	OnFailure bool `json:"on_failure"`
	OnSuccess bool `json:"on_success"`
	OnFixed   bool `json:"on_fixed"`
	OnAborted bool `json:"on_aborted"`
}

// IsZero 判断事件集合是否全为 false（任务级目标未单独配置时继承任务事件）。
func (e NotifyEvents) IsZero() bool {
	return !e.OnFailure && !e.OnSuccess && !e.OnFixed && !e.OnAborted
}

// NotifyTarget 是一个 Webhook 群目标（钉钉 / 企业微信 / Slack 机器人）。
// URL 含 access_token 等密钥：接口返回时按查询参数脱敏，保存时以 *** 占位沿用旧值。
type NotifyTarget struct {
	Channel string       `json:"channel"` // dingtalk | wecom | slack
	URL     string       `json:"url"`
	Events  NotifyEvents `json:"events,omitempty"` // 为空表示继承任务级事件
}

// JobNotify 是任务级通知配置：把「项目」与「人员 / 群」绑定。
// 未配置（nil 或 !Enabled）时沿用全局默认收件人与全局 Webhook 机器人。
type JobNotify struct {
	Enabled bool `json:"enabled"`
	// UseGlobal 表示在任务级收件人/群之外，同时发送全局默认（叠加而非替换）。
	UseGlobal bool `json:"use_global"`

	// People 引用平台内用户（用户名），发送时解析其邮箱 / 企业微信 ID / 手机号。
	People []string `json:"people"`
	// Emails 是额外的原始收件邮箱（外部人员、邮件组）。
	Emails []string `json:"emails"`
	// Targets 是任务专属的群机器人。
	Targets []NotifyTarget `json:"targets"`

	// Events 是任务级事件开关；Targets 未单独配置事件时继承此值。
	Events NotifyEvents `json:"events"`

	// OnCall 值班轮值：失败时 @ 当周值班人（而非固定名单）。
	OnCall *OnCallConfig `json:"on_call,omitempty"`
	// Escalation 升级策略：连续失败达到阈值后追加通知上级/更大群。
	Escalation *EscalationConfig `json:"escalation,omitempty"`
	// Routes 按分支/环境路由：分支匹配时把该路由的人员/群叠加到通知目标。
	Routes []NotifyRoute `json:"routes,omitempty"`
	// SuppressWindowMin 失败抑制窗口（分钟）：窗口内同一任务连续失败只发首条，
	// 后续合并为「已连续失败 N 次」的计数（0 表示关闭抑制）。
	SuppressWindowMin int `json:"suppress_window_min"`
	// Template 任务级消息模板（占位符 {{job}} {{build}} {{event}} 等）；
	// 为空时使用内置渲染，保证默认输出与旧版本一致。
	Template string `json:"template,omitempty"`
}

// OnCallConfig 是值班轮值表：从 StartDate 起每人连续值班 PeriodDays 天，循环轮转。
// 轮值状态由日期计算得出（无隐藏状态），因此重启/部署后结果一致。
type OnCallConfig struct {
	Enabled    bool     `json:"enabled"`
	Rotation   []string `json:"rotation"`    // 用户名或邮箱，按顺序轮转
	StartDate  string   `json:"start_date"`  // 轮值起点 YYYY-MM-DD（空则取今天）
	PeriodDays int      `json:"period_days"` // 每人值班天数，默认 7
}

// period 返回有效的值班周期（天），非法值回落到 7 天。
func (c OnCallConfig) period() int {
	if c.PeriodDays <= 0 {
		return 7
	}
	return c.PeriodDays
}

// start 解析轮值起点；空或非法时返回零值时间并由调用方回落到「今天」。
func (c OnCallConfig) start() (time.Time, bool) {
	raw := strings.TrimSpace(c.StartDate)
	if raw == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{"2006-01-02", time.RFC3339, "2006-01-02 15:04:05"} {
		if t, err := time.ParseInLocation(layout, raw, time.Local); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// CurrentIndex 返回 now 时刻值班人在 Rotation 中的下标（-1 表示无人值班）。
// 用「距起点天数 / 周期」取模，负数天（起点在未来）也归一到合法区间。
func (c OnCallConfig) CurrentIndex(now time.Time) int {
	n := len(c.Rotation)
	if !c.Enabled || n == 0 {
		return -1
	}
	start, ok := c.start()
	if !ok {
		// 未配置起点：用固定锚点（2024-01-01）而不是「今天」。
		// 用今天会让轮值永远停在第 0 个值班人（每次计算基准都在变）。
		start = time.Date(2024, 1, 1, 0, 0, 0, 0, now.Location())
	}
	days := int(now.Sub(start).Hours() / 24)
	if days < 0 {
		// Go 的取模对负数返回负值，先补一个足够大的周期整数倍再取模。
		period := c.period() * n
		days = (days%period + period) % period
	}
	return (days / c.period()) % n
}

// Current 返回 now 时刻的值班人（无人值班时返回空串）。
func (c OnCallConfig) Current(now time.Time) string {
	idx := c.CurrentIndex(now)
	if idx < 0 {
		return ""
	}
	return strings.TrimSpace(c.Rotation[idx])
}

// EscalationConfig 是升级策略：连续失败达到 After 次后，追加通知「上级/更大群」。
// 升级目标与基础目标叠加（不是替换），避免漏掉原有收件人。
type EscalationConfig struct {
	Enabled bool           `json:"enabled"`
	After   int            `json:"after"` // 连续失败次数阈值，默认 3
	People  []string       `json:"people,omitempty"`
	Emails  []string       `json:"emails,omitempty"`
	Targets []NotifyTarget `json:"targets,omitempty"`
}

// MinFailures 返回有效的升级阈值（默认 3，最小 2：1 次失败就是首条告警，无需升级）。
func (c EscalationConfig) MinFailures() int {
	if !c.Enabled {
		return 0
	}
	if c.After < 2 {
		return 3
	}
	return c.After
}

// NotifyRoute 按分支/环境路由：Match 匹配本次构建分支时，
// 把该路由的人员/邮箱/群叠加到通知目标（与基础配置并集）。
// 典型用法：main → 值班群；feature/* → 仅通知提交作者。
type NotifyRoute struct {
	Match     string         `json:"match"` // main / feature/* / release/**（glob，支持 * 与 **）
	People    []string       `json:"people,omitempty"`
	Emails    []string       `json:"emails,omitempty"`
	Targets   []NotifyTarget `json:"targets,omitempty"`
	Committer bool           `json:"committer,omitempty"` // 追加通知本次提交作者
	OnCall    bool           `json:"on_call,omitempty"`   // 追加通知当周值班人
}

// MatchBranch 判断分支是否匹配该路由的 glob。
// 规则：`*` 匹配不含 `/` 的一段（feature/* 不匹配 feature/a/b），
// `**` 匹配任意字符（含 `/`），其余字符按字面量处理（不引入正则，避免配置出错）。
// 空 Match 视为匹配任意分支。
func (r NotifyRoute) MatchBranch(branch string) bool {
	pattern := strings.TrimSpace(r.Match)
	if pattern == "" {
		return true
	}
	if branch == "" {
		// 非 Git 任务没有分支概念：显式写了分支匹配的路由不生效，
		// 否则「feature/* 只通知提交者」会误伤所有本地任务。
		return false
	}
	return globMatch(pattern, branch)
}

// globMatch 是 `*` / `**` 的极简 glob 实现（大小写敏感，分支名本身区分大小写）。
func globMatch(pattern, name string) bool {
	// 递归回溯实现：分支名通常很短，无需预编译。
	switch {
	case pattern == "":
		return name == ""
	case strings.HasPrefix(pattern, "**"):
		rest := pattern[2:]
		rest = strings.TrimPrefix(rest, "/")
		for i := 0; i <= len(name); i++ {
			if globMatch(rest, name[i:]) {
				return true
			}
		}
		return false
	case strings.HasPrefix(pattern, "*"):
		rest := pattern[1:]
		for i := 0; i <= len(name); i++ {
			if i > 0 && name[i-1] == '/' {
				break // `*` 不跨目录分隔符
			}
			if globMatch(rest, name[i:]) {
				return true
			}
		}
		return false
	default:
		if name == "" || pattern[0] != name[0] {
			return false
		}
		return globMatch(pattern[1:], name[1:])
	}
}

// NotifyRecord 是一条通知投递记录（data/notify.log，JSONL）。
// 按「收件人/群」粒度落盘，才能回答「谁漏收了告警」。
type NotifyRecord struct {
	ID           int64     `json:"id"`
	Time         time.Time `json:"time"`
	PipelineID   string    `json:"pipeline_id"`
	PipelineName string    `json:"pipeline_name"`
	BuildID      string    `json:"build_id"`
	Event        string    `json:"event"`
	Channel      string    `json:"channel"`   // email | dingtalk | wecom | slack | ""
	Recipient    string    `json:"recipient"` // 邮箱 / "dingtalk#1" / "suppressed"
	Status       string    `json:"status"`    // sent | failed | suppressed
	Error        string    `json:"error,omitempty"`
	Streak       int       `json:"streak,omitempty"`    // 连续失败次数（失败事件）
	Escalated    bool      `json:"escalated,omitempty"` // 本次是否触发了升级
	OnCall       string    `json:"on_call,omitempty"`   // 当周值班人
	Branch       string    `json:"branch,omitempty"`
}

// 通知投递状态。
const (
	NotifyStatusSent       = "sent"
	NotifyStatusFailed     = "failed"
	NotifyStatusSuppressed = "suppressed"
)

// ParsePositiveInt 解析正整数参数（查询过滤用），非法值返回 0。
func ParsePositiveInt(raw string) int {
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

// NotifyEvent 是构建结果对应的事件名（用于消息标题与日志）。
const (
	NotifyEventFailure = "failure"
	NotifyEventSuccess = "success"
	NotifyEventFixed   = "fixed"
	NotifyEventAborted = "aborted"
	NotifyEventNone    = ""
)

// BuildEvent 计算本次构建的通知事件；prev 为上一次构建（可为 nil）。
// 返回 NotifyEventNone 表示该结果不产生事件（如 pending/running）。
func BuildEvent(cur *Build, prev *Build) string {
	if cur == nil {
		return NotifyEventNone
	}
	switch cur.Status {
	case BuildFailed:
		return NotifyEventFailure
	case BuildAborted:
		return NotifyEventAborted
	case BuildSuccess:
		if prev != nil && (prev.Status == BuildFailed || prev.Status == BuildAborted) {
			return NotifyEventFixed
		}
		return NotifyEventSuccess
	}
	return NotifyEventNone
}

// Matches 判断事件集合是否订阅了给定事件。
// 「修复」是成功的特例：订阅了成功的用户也收到修复通知；
// 但只订阅「修复」时，普通成功不通知——这正是区分两者的意义。
func (e NotifyEvents) Matches(event string) bool {
	switch event {
	case NotifyEventFailure:
		return e.OnFailure
	case NotifyEventSuccess:
		return e.OnSuccess
	case NotifyEventFixed:
		return e.OnFixed || e.OnSuccess
	case NotifyEventAborted:
		return e.OnAborted
	}
	return false
}

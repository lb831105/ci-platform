package model

import (
	"testing"
	"time"
)

// 值班轮值：按周期天数从起始日期轮转，起点在未来也能归一到合法下标。
func TestOnCallRotation(t *testing.T) {
	start := time.Date(2024, 3, 4, 0, 0, 0, 0, time.Local) // 周一
	c := OnCallConfig{
		Enabled:    true,
		Rotation:   []string{"alice", "bob", "carol"},
		StartDate:  "2024-03-04",
		PeriodDays: 7,
	}
	cases := []struct {
		day  int
		want string
	}{
		{0, "alice"},
		{6, "alice"},
		{7, "bob"},
		{13, "bob"},
		{14, "carol"},
		{21, "alice"},
		{7 * 3 * 5, "alice"}, // 五个完整轮次后回到起点
	}
	for _, tc := range cases {
		at := start.AddDate(0, 0, tc.day)
		if got := c.Current(at); got != tc.want {
			t.Fatalf("day %d: got %q, want %q", tc.day, got, tc.want)
		}
	}

	// 起点在未来：负数天数不能产生负下标。
	if got := c.Current(start.AddDate(0, 0, -1)); got != "carol" {
		t.Fatalf("before start: got %q, want carol", got)
	}

	// 未启用 / 空轮值表 → 无人值班。
	if got := (OnCallConfig{Enabled: false, Rotation: []string{"a"}}).Current(start); got != "" {
		t.Fatalf("disabled: got %q, want empty", got)
	}
	if got := (OnCallConfig{Enabled: true}).Current(start); got != "" {
		t.Fatalf("empty rotation: got %q, want empty", got)
	}

	// 周期非法时回落到 7 天；起始日期缺失时用固定锚点，轮值仍随时间推进。
	noStart := OnCallConfig{Enabled: true, Rotation: []string{"a", "b"}, PeriodDays: 0}
	now := time.Now()
	first := noStart.Current(now)
	second := noStart.Current(now.AddDate(0, 0, 7))
	if first == "" || second == "" {
		t.Fatalf("no start: got empty rotation (%q, %q)", first, second)
	}
	if first == second {
		t.Fatalf("no start: rotation must advance weekly, both %q", first)
	}
	if third := noStart.Current(now.AddDate(0, 0, 14)); third != first {
		t.Fatalf("no start: 2-person rotation must cycle, got %q then %q", first, third)
	}
}

// 分支 glob：`*` 不跨目录，`**` 跨目录，空模式匹配任意分支。
func TestNotifyRouteMatchBranch(t *testing.T) {
	cases := []struct {
		match  string
		branch string
		want   bool
	}{
		{"main", "main", true},
		{"main", "mainline", false},
		{"feature/*", "feature/login", true},
		{"feature/*", "feature/a/b", false}, // `*` 不跨 `/`
		{"feature/**", "feature/a/b", true},
		{"release/**", "release/1.0/hotfix", true},
		{"release/**", "main", false},
		{"", "anything", true},    // 未写分支 = 任意分支
		{"main", "", false},       // 非 Git 任务：显式分支路由不生效
		{"", "", true},            // 未写分支且无分支信息：仍然生效
		{"*", "main", true},       // 单段通配
		{"*", "feature/x", false}, // 不跨目录
	}
	for _, tc := range cases {
		got := NotifyRoute{Match: tc.match}.MatchBranch(tc.branch)
		if got != tc.want {
			t.Fatalf("match=%q branch=%q: got %v, want %v", tc.match, tc.branch, got, tc.want)
		}
	}
}

// 失败事件与连续失败计数配合的事件语义保持不变。
func TestNotifyEventsMatchesFixed(t *testing.T) {
	onlySuccess := NotifyEvents{OnSuccess: true}
	if !onlySuccess.Matches(NotifyEventFixed) {
		t.Fatal("subscribing success must also receive fixed")
	}
	onlyFixed := NotifyEvents{OnFixed: true}
	if onlyFixed.Matches(NotifyEventSuccess) {
		t.Fatal("subscribing only fixed must not receive plain success")
	}
	if !onlyFixed.Matches(NotifyEventFixed) {
		t.Fatal("subscribing only fixed must receive fixed")
	}
}

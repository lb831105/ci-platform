package pkg

import (
	"strings"
	"testing"
)

// 模板渲染：只替换已知占位符，未知占位符保持原样（便于发现拼写错误）。
func TestRenderNotifyTemplate(t *testing.T) {
	vars := map[string]string{
		"job":    "web",
		"build":  "42",
		"event":  "failure",
		"streak": "3",
		"oncall": "alice",
	}
	cases := []struct {
		tpl  string
		want string
	}{
		{"", ""},
		{"   ", ""},
		{"构建 {{job}} #{{build}} 失败", "构建 web #42 失败"},
		{"{{ job }} / {{build}}", "web / 42"},
		{"连败 {{streak}} 次，值班 {{oncall}}", "连败 3 次，值班 alice"},
		{"未知 {{nope}} 占位符", "未知 {{nope}} 占位符"},
		{"值里带占位符 {{job}}", "值里带占位符 web"},
		{"没有占位符", "没有占位符"},
	}
	for _, tc := range cases {
		if got := RenderNotifyTemplate(tc.tpl, vars); got != tc.want {
			t.Fatalf("tpl=%q: got %q, want %q", tc.tpl, got, tc.want)
		}
	}
}

// 占位符声明与实际支持保持一致（前端提示依赖该清单）。
func TestNotifyTemplateVarsCovered(t *testing.T) {
	vars := NotifyTemplateVars()
	joined := strings.Join(vars, ",")
	for _, want := range []string{"job", "build", "event", "status", "branch", "commit", "author", "duration", "url", "oncall", "streak", "message"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("模板变量清单缺少 %q: %v", want, vars)
		}
	}
}

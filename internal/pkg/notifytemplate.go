package pkg

import (
	"regexp"
	"strings"
)

// notifyPlaceholder 匹配 {{ key }} 形式的消息模板占位符（允许内部空格）。
var notifyPlaceholder = regexp.MustCompile(`\{\{\s*([a-zA-Z_][a-zA-Z0-9_]*)\s*\}\}`)

// RenderNotifyTemplate 用变量表渲染任务级消息模板。
//
// 选择「自己替换」而不是 text/template，原因：
//   - 模板由任务配置者填写，只需占位符能力，不需要控制流；
//   - 未知占位符保持原样（便于发现拼写错误），而不是渲染成 "<no value>"；
//   - 值里带 {{ }} 不会被二次渲染（单遍替换）。
func RenderNotifyTemplate(tpl string, vars map[string]string) string {
	if strings.TrimSpace(tpl) == "" {
		return ""
	}
	return notifyPlaceholder.ReplaceAllStringFunc(tpl, func(m string) string {
		key := notifyPlaceholder.FindStringSubmatch(m)[1]
		if v, ok := vars[key]; ok {
			return v
		}
		return m
	})
}

// NotifyTemplateVars 列出任务级消息模板支持的占位符（供前端提示，单一事实来源）。
func NotifyTemplateVars() []string {
	return []string{
		"job", "build", "event", "status", "branch", "commit",
		"author", "duration", "url", "oncall", "streak", "message",
	}
}

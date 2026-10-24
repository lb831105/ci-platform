package executor

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"ci-platform/internal/model"
	"ci-platform/internal/pkg"
)

// NotifyMessage 是一条构建通知的内容（任务 + 构建 + 事件 + @ 提醒对象）。
type NotifyMessage struct {
	Pipeline   *model.Pipeline
	Build      *model.Build
	JenkinsURL string
	Event      string   // failure / success / fixed / aborted
	Mentions   []string // 钉钉手机号 / 企业微信 userid（失败时 @ 相关人）

	// Template 任务级消息模板（占位符 {{job}} {{build}} 等）；为空用内置渲染。
	Template string
	// Streak 连续失败次数（失败事件），用于「已连续失败 N 次」提示。
	Streak int
	// OnCall 当周值班人（用户名/邮箱），仅用于展示。
	OnCall string
}

// SendWebhookNotifyMsg 向指定渠道的机器人推送一条构建通知。
// channel: dingtalk | wecom | slack（空视为 slack）。
// URL 含 access_token 等密钥：失败日志只打印渠道与错误，永不打印 URL。
func SendWebhookNotifyMsg(ctx context.Context, channel, url string, msg NotifyMessage) error {
	text := RenderNotifyText(msg)
	var payload []byte
	switch channel {
	case "dingtalk":
		body := map[string]interface{}{
			"msgtype":  "markdown",
			"markdown": map[string]string{"title": notifyTitle(msg), "text": text},
		}
		// 钉钉 @ 人需在 at.atMobiles 中声明，正文用 @手机号 触发。
		if len(msg.Mentions) > 0 {
			body["at"] = map[string]interface{}{"atMobiles": msg.Mentions, "isAtAll": false}
		}
		payload, _ = json.Marshal(body)
	case "wecom":
		// 企业微信 markdown 支持行内 <@userid> 提醒。
		payload, _ = json.Marshal(map[string]interface{}{
			"msgtype":  "markdown",
			"markdown": map[string]string{"content": text},
		})
	case "slack", "":
		payload, _ = json.Marshal(map[string]string{"text": text})
	default:
		return fmt.Errorf("未知的 Webhook 通知渠道: %q", channel)
	}
	url = strings.TrimSpace(url)
	if url == "" {
		return fmt.Errorf("webhook URL is empty")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
	if resp.StatusCode >= 300 {
		return fmt.Errorf("webhook 返回 %s: %s", resp.Status,
			strings.TrimSpace(string(body[:min(len(body), 200)])))
	}
	// 钉钉/企微成功时业务码在响应体（errcode != 0 表示被限流/关键词不匹配）。
	var biz struct {
		ErrCode int    `json:"errcode"`
		ErrMsg  string `json:"errmsg"`
	}
	if err := json.Unmarshal(body, &biz); err == nil && biz.ErrCode != 0 {
		return fmt.Errorf("webhook 业务错误 %d: %s", biz.ErrCode, biz.ErrMsg)
	}
	return nil
}

// SendWebhookNotify 兼容旧签名（全局通知配置）；内部走 SendWebhookNotifyMsg。
func SendWebhookNotify(ctx context.Context, cfg model.WebhookNotifySettings, p *model.Pipeline, b *model.Build, jenkinsURL string) error {
	return SendWebhookNotifyMsg(ctx, cfg.Channel, cfg.URL, NotifyMessage{
		Pipeline: p, Build: b, JenkinsURL: jenkinsURL,
		Event: model.BuildEvent(b, nil),
	})
}

// notifyTitle 群消息标题（含事件，便于在群里一眼区分）。
func notifyTitle(msg NotifyMessage) string {
	return "构建通知 - " + notifyEventLabel(msg.Event)
}

// notifyEventLabel 事件中文标签。
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
	return string(event)
}

// RenderNotifyText 渲染通知正文（markdown）。
func RenderNotifyText(msg NotifyMessage) string {
	p, b := msg.Pipeline, msg.Build
	if p == nil || b == nil {
		return ""
	}
	url := ""
	if base := strings.TrimRight(msg.JenkinsURL, "/"); base != "" {
		url = fmt.Sprintf("%s/pipelines/%s/builds/%s", base, p.ID, b.ID)
	}
	// 任务级模板：交给配置者自由排版；@ 提醒仍追加在末尾，
	// 否则「值班人被 @」这种关键提醒会因为自定义模板而丢失。
	if tpl := strings.TrimSpace(msg.Template); tpl != "" {
		duration := "-"
		if b.StartedAt != nil && b.FinishedAt != nil {
			duration = b.FinishedAt.Sub(*b.StartedAt).Round(time.Second).String()
		}
		streak := ""
		if msg.Streak > 1 {
			streak = fmt.Sprintf("%d", msg.Streak)
		}
		text := pkg.RenderNotifyTemplate(tpl, map[string]string{
			"job":      p.Name,
			"build":    b.ID,
			"event":    msg.Event,
			"status":   string(b.Status),
			"branch":   b.Branch,
			"commit":   b.Commit,
			"author":   b.CommitAuthor,
			"duration": duration,
			"url":      url,
			"oncall":   msg.OnCall,
			"streak":   streak,
			"message":  notifySummary(msg),
		})
		if len(msg.Mentions) > 0 {
			text += "\n\n请关注：" + mentionText(msg.Mentions)
		}
		return text
	}
	icon := "❌"
	switch msg.Event {
	case model.NotifyEventSuccess:
		icon = "✅"
	case model.NotifyEventFixed:
		icon = "🛠"
	case model.NotifyEventAborted:
		icon = "⏹"
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "%s **构建%s**：%s #%s\n\n", icon, notifyEventLabel(msg.Event), p.Name, b.ID)
	fmt.Fprintf(&sb, "- 状态: %s\n", string(b.Status))
	if b.TriggeredBy != "" {
		fmt.Fprintf(&sb, "- 触发: %s（%s）\n", b.TriggeredBy, string(b.TriggerType))
	}
	if b.StartedAt != nil && b.FinishedAt != nil {
		fmt.Fprintf(&sb, "- 耗时: %s\n", b.FinishedAt.Sub(*b.StartedAt).Round(time.Second))
	}
	if b.StartedAt != nil {
		fmt.Fprintf(&sb, "- 开始时间: %s\n", b.StartedAt.Format("2006-01-02 15:04:05"))
	}
	if url := strings.TrimRight(msg.JenkinsURL, "/"); url != "" {
		fmt.Fprintf(&sb, "\n[查看详情](%s/pipelines/%s/builds/%s)", url, p.ID, b.ID)
	}
	// @ 提醒：钉钉用手机号，企微用 userid（行内 <@id> 语法）。
	if len(msg.Mentions) > 0 {
		fmt.Fprintf(&sb, "\n\n请关注：%s", mentionText(msg.Mentions))
	}
	return sb.String()
}

// mentionText 渲染 @ 提醒（钉钉用手机号、企微用 userid，均用 <@id> 行内语法）。
func mentionText(mentions []string) string {
	parts := make([]string, 0, len(mentions))
	for _, m := range mentions {
		parts = append(parts, "<@"+m+">")
	}
	return strings.Join(parts, " ")
}

// notifySummary 给模板变量 message 用的一行摘要。
func notifySummary(msg NotifyMessage) string {
	parts := []string{notifyEventLabel(msg.Event)}
	if msg.Streak > 1 {
		parts = append(parts, fmt.Sprintf("连续失败 %d 次", msg.Streak))
	}
	if msg.OnCall != "" {
		parts = append(parts, "值班 "+msg.OnCall)
	}
	return strings.Join(parts, "；")
}

// NotifyChannelName 渠道名（日志辅助；避免直接打印 URL）。
func NotifyChannelName(cfg model.WebhookNotifySettings) string {
	if cfg.Channel == "" {
		return "slack"
	}
	return cfg.Channel
}

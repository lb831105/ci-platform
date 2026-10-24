package pkg

import (
	"strings"
	"testing"
)

// 密钥类查询参数被脱敏，其余保留。
func TestMaskTokenQuery(t *testing.T) {
	in := "https://oapi.dingtalk.com/robot/send?access_token=abc123&foo=bar"
	want := "https://oapi.dingtalk.com/robot/send?access_token=***&foo=bar"
	if got := MaskTokenQuery(in); got != want {
		t.Fatalf("got %q want %q", got, want)
	}
	// 企业微信 key 同样脱敏
	if got := MaskTokenQuery("https://qyapi.weixin.qq.com/x?key=s3cr3t"); got != "https://qyapi.weixin.qq.com/x?key=***" {
		t.Fatalf("wecom key not masked: %q", got)
	}
	// 无密钥查询时原样返回
	plain := "https://hooks.slack.com/services/T/B/X"
	if got := MaskTokenQuery(plain); got != plain {
		t.Fatalf("url changed: %q", got)
	}
}

// Slack 密钥在 path 尾段，必须一并脱敏并支持回填。
func TestMaskWebhookURLSlack(t *testing.T) {
	slack := "https://hooks.slack.com/services/T000/B000/SECRETPART"
	masked := MaskWebhookURL("slack", slack)
	if masked != "https://hooks.slack.com/services/T000/B000/***" {
		t.Fatalf("slack masked = %q", masked)
	}
	if got := ResolveMaskedURL("slack", masked, slack); got != slack {
		t.Fatalf("slack url not restored: %q", got)
	}
	// 非 slack 渠道不动 path
	ding := "https://oapi.dingtalk.com/robot/send?access_token=T"
	if got := MaskWebhookURL("dingtalk", ding); got != "https://oapi.dingtalk.com/robot/send?access_token=***" {
		t.Fatalf("dingtalk masked = %q", got)
	}
}

// 畸形 URL 也必须脱敏（不能因解析失败而泄露）。
func TestMaskTokenQueryMalformed(t *testing.T) {
	got := MaskTokenQuery("http://x/send?access_token=abc%")
	if !strings.Contains(got, "***") {
		t.Fatalf("malformed url not masked: %q", got)
	}
	if strings.Contains(got, "abc") {
		t.Fatalf("secret leaked: %q", got)
	}
}

// 保存时用旧值还原被脱敏的 URL。
func TestResolveMaskedURL(t *testing.T) {
	old := "https://oapi.dingtalk.com/robot/send?access_token=REAL"
	masked := MaskTokenQuery(old)
	if got := ResolveMaskedURL("dingtalk", masked, old); got != old {
		t.Fatalf("masked url not restored: %q", got)
	}
	// 用户真正改了 URL（不再等于脱敏形态）→ 采用新值
	if got := ResolveMaskedURL("dingtalk", "https://other/send?access_token=***", old); got == old {
		t.Fatal("changed url should not be replaced by old")
	}
	// 明文提交 → 原样采用
	if got := ResolveMaskedURL("dingtalk", "https://new/send?access_token=NEW", old); got != "https://new/send?access_token=NEW" {
		t.Fatalf("plain url changed: %q", got)
	}
}

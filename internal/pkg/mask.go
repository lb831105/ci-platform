package pkg

import (
	"net/url"
	"strings"
)

// MaskTokenQuery 脱敏 URL 中密钥类查询参数（access_token / key / secret / sign 等），
// 值替换为 ***。用于「任务通知 → 群机器人 URL」的接口返回：结构保持可读，
// 密钥不外泄；保存时若检测到 *** 则沿用旧值（见 ResolveMaskedURL）。
//
// 实现要点：
//   - 不依赖 url.Parse 成功：畸形 URL 也必须脱敏，否则密钥会随解析失败一起泄露；
//   - 不能用 url.Values.Encode() 重新编码——它会把 *** 转义成 %2A%2A%2A，
//     前端无法识别为「未修改」的占位符。因此按原始文本替换，保留其余参数的原始编码。
func MaskTokenQuery(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	// 分离 fragment，避免把 # 之后的片段当作查询参数。
	body, frag := raw, ""
	if i := strings.Index(raw, "#"); i >= 0 {
		body, frag = raw[:i], raw[i:]
	}
	q := strings.Index(body, "?")
	if q < 0 {
		return raw
	}
	masked, changed := maskQueryString(body[q+1:])
	if !changed {
		return raw
	}
	return body[:q+1] + masked + frag
}

// maskQueryString 按 & 拆分查询串，把密钥类参数的值替换为 ***。
func maskQueryString(query string) (string, bool) {
	parts := strings.Split(query, "&")
	changed := false
	for i, part := range parts {
		if part == "" {
			continue
		}
		key, _, hasValue := strings.Cut(part, "=")
		if !hasValue {
			continue
		}
		// 键可能被编码（如 access%5Ftoken），解码后再判断。
		decodedKey, err := url.QueryUnescape(key)
		if err != nil {
			decodedKey = key
		}
		if !isSecretQueryKey(decodedKey) {
			continue
		}
		parts[i] = key + "=***"
		changed = true
	}
	return strings.Join(parts, "&"), changed
}

// isSecretQueryKey 判断查询参数是否为密钥类。
func isSecretQueryKey(key string) bool {
	k := strings.ToLower(key)
	for _, part := range []string{"token", "key", "secret", "sign", "password", "pwd"} {
		if strings.Contains(k, part) {
			return true
		}
	}
	return false
}

// MaskURLUserInfo 脱敏 URL 中的 userinfo（https://user:token@host/repo）。
// 用于 Git 日志与错误信息：URL 本身可能是用户填写的带 Token 地址。
func MaskURLUserInfo(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	scheme := strings.Index(raw, "://")
	if scheme < 0 {
		return raw
	}
	rest := raw[scheme+3:]
	end := len(rest)
	if i := strings.IndexAny(rest, "/?#"); i >= 0 {
		end = i
	}
	authority := rest[:end]
	at := strings.LastIndex(authority, "@")
	if at < 0 {
		return raw
	}
	return raw[:scheme+3] + "***:***@" + rest[at+1:]
}

// MaskWebhookURL 按渠道脱敏群机器人 URL。
// Slack 的密钥在 path 最后一段（https://hooks.slack.com/services/T/B/SECRET），
// 仅脱敏查询参数不够，必须一并掩码路径尾段。
func MaskWebhookURL(channel, raw string) string {
	masked := MaskTokenQuery(raw)
	if !strings.EqualFold(strings.TrimSpace(channel), "slack") {
		return masked
	}
	return maskLastPathSegment(masked)
}

// maskLastPathSegment 把 URL 路径的最后一段替换为 ***（保留主机与前缀，结构可读）。
// 用文本切片而非 url.URL.String()：后者会把 *** 转义成 %2A%2A%2A，
// 前端无法识别“未修改”占位符。
func maskLastPathSegment(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	body, frag := raw, ""
	if i := strings.Index(raw, "#"); i >= 0 {
		body, frag = raw[:i], raw[i:]
	}
	schemeIdx := strings.Index(body, "://")
	if schemeIdx < 0 {
		return "***" // 非标准 URL：整体视为敏感
	}
	slash := strings.Index(body[schemeIdx+3:], "/")
	if slash < 0 {
		return raw // 只有主机，无 path
	}
	pathStart := schemeIdx + 3 + slash
	pathEnd := len(body)
	if q := strings.Index(body, "?"); q > pathStart {
		pathEnd = q
	}
	path := strings.TrimRight(body[pathStart:pathEnd], "/")
	idx := strings.LastIndex(path, "/")
	if idx < 0 {
		return raw
	}
	return body[:pathStart] + path[:idx+1] + "***" + body[pathEnd:] + frag
}

// ResolveMaskedURL 用旧 URL 补齐脱敏后的新 URL（仅当二者“脱敏形态”一致时）。
// 这样前端可以把 URL 原样提交（含 ***），后端仍保留真实密钥。
func ResolveMaskedURL(channel, incoming, old string) string {
	incoming = strings.TrimSpace(incoming)
	if !strings.Contains(incoming, "***") {
		return incoming
	}
	if MaskWebhookURL(channel, old) == incoming {
		return old
	}
	return incoming
}

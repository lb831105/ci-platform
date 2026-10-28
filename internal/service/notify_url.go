package service

import (
	"fmt"
	"net"
	"net/url"
	"strings"

	"ci-platform/internal/pkg"
)

// ValidateNotifyTargetURL 在保存任务通知配置时做基础 SSRF 防护：
// 拒绝 loopback、私网、链路本地与常见云元数据主机。
//
// 这里只校验 URL 里的字面主机；DNS 名称仍可能在发送时解析到内网。
// 运行时的完整防护需要自定义 DialContext（见后续网络层加固），
// 但先把最常见的 http://127.0.0.1 / http://10.0.0.5 / metadata 挡住。
func ValidateNotifyTargetURL(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fmt.Errorf("URL 不能为空")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return fmt.Errorf("URL 格式不正确")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("只支持 http:// 或 https://")
	}
	host := strings.ToLower(strings.TrimSpace(u.Hostname()))
	if host == "" {
		return fmt.Errorf("URL 缺少主机名")
	}
	if host == "localhost" || strings.HasSuffix(host, ".localhost") ||
		host == "metadata.google.internal" || host == "metadata" {
		return fmt.Errorf("不允许访问本机/元数据地址 %q", host)
	}
	if ip := net.ParseIP(host); ip != nil {
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
			ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
			return fmt.Errorf("不允许访问内网/本机地址 %q", host)
		}
	}
	return nil
}

// notifyURLValidationError 把底层错误包装成统一业务错误。
func notifyURLValidationError(label, raw string, err error) error {
	return pkg.ErrBadRequest("%s URL %q 不安全：%v", label, raw, err)
}

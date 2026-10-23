package executor

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/smtp"
	"strings"
	"time"

	"ci-platform/internal/model"
)

// SendMail 使用系统配置中的 SMTP 参数发送纯文本邮件。
// 支持可选认证与 STARTTLS；失败原样返回错误，由调用方决定是否忽略。
func SendMail(cfg model.EmailSettings, to []string, subject, body string) error {
	return SendMailContext(context.Background(), cfg, to, subject, body)
}

// SendMailContext 在 context 到期时中断 SMTP 会话，避免邮件服务器无响应时
// 长时间占用构建 worker。整个会话（含 TLS/AUTH/DATA）都受同一个 deadline 约束。
func SendMailContext(ctx context.Context, cfg model.EmailSettings, to []string, subject, body string) error {
	if cfg.SMTPHost == "" {
		return fmt.Errorf("smtp host is not configured")
	}
	if len(to) == 0 {
		return fmt.Errorf("no recipients")
	}
	port := cfg.SMTPPort
	if port <= 0 {
		port = 25
	}
	addr := net.JoinHostPort(cfg.SMTPHost, fmt.Sprintf("%d", port))
	from := cfg.From
	if from == "" {
		from = cfg.Username
	}
	if from == "" {
		from = "ci-platform@localhost"
	}

	msg := buildMessage(from, to, subject, body)

	dialer := net.Dialer{Timeout: 10 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("smtp dial %s: %w", addr, err)
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	} else {
		_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
	}
	client, err := smtp.NewClient(conn, cfg.SMTPHost)
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("smtp client: %w", err)
	}
	defer func() { _ = client.Close() }()

	if cfg.UseTLS {
		if ok, _ := client.Extension("STARTTLS"); !ok {
			return fmt.Errorf("smtp server %s does not support STARTTLS", cfg.SMTPHost)
		}
		if err = client.StartTLS(&tls.Config{ServerName: cfg.SMTPHost}); err != nil {
			return fmt.Errorf("smtp starttls: %w", err)
		}
	}
	if cfg.Username != "" {
		auth := smtp.PlainAuth("", cfg.Username, cfg.Password, cfg.SMTPHost)
		if err = client.Auth(auth); err != nil {
			return fmt.Errorf("smtp auth: %w", err)
		}
	}
	if err = client.Mail(from); err != nil {
		return fmt.Errorf("smtp mail from: %w", err)
	}
	for _, rcpt := range to {
		if err = client.Rcpt(rcpt); err != nil {
			return fmt.Errorf("smtp rcpt %s: %w", rcpt, err)
		}
	}
	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("smtp data: %w", err)
	}
	if _, err = w.Write([]byte(msg)); err != nil {
		_ = w.Close()
		return fmt.Errorf("smtp write: %w", err)
	}
	if err = w.Close(); err != nil {
		return fmt.Errorf("smtp close data: %w", err)
	}
	return client.Quit()
}

// sanitizeMailHeader 阻止 CR/LF 注入邮件头。
func sanitizeMailHeader(value string) string {
	value = strings.ReplaceAll(value, "\r", " ")
	value = strings.ReplaceAll(value, "\n", " ")
	return strings.TrimSpace(value)
}

// buildMessage 组装符合 RFC 5322 的纯文本邮件。
func buildMessage(from string, to []string, subject, body string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "From: %s\r\n", sanitizeMailHeader(from))
	for i, rcpt := range to {
		to[i] = sanitizeMailHeader(rcpt)
	}
	fmt.Fprintf(&b, "To: %s\r\n", strings.Join(to, ", "))
	fmt.Fprintf(&b, "Subject: %s\r\n", sanitizeMailHeader(subject))
	fmt.Fprintf(&b, "Date: %s\r\n", time.Now().Format(time.RFC1123Z))
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
	b.WriteString("\r\n")
	// 统一换行，避免裸 \n 被部分 MTA 处理异常。
	b.WriteString(strings.ReplaceAll(body, "\n", "\r\n"))
	return b.String()
}

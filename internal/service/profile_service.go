package service

import (
	"context"
	"crypto/rand"
	"fmt"
	"math/big"
	"strings"
	"sync"
	"time"

	"ci-platform/internal/executor"
	"ci-platform/internal/model"
	"ci-platform/internal/pkg"
)

// ProfileAccountStore 是个人资料页依赖的用户存储能力（由 RBACService 实现）。
type ProfileAccountStore interface {
	Profile(name string) (model.Profile, error)
	SetUserContact(name, email, wecomID, phone string) error
	SetEmailVerified(name string, verified bool) error
}

// ProfileService 提供用户自助维护联系方式与邮箱验证：
// 管理员不必逐个代填邮箱，用户自己维护，验证码确认后打上「已验证」标记。
type ProfileService struct {
	accounts ProfileAccountStore
	settings SettingsProvider

	mu     sync.Mutex
	codes  map[string]emailCode // username -> 待确认的验证码
	lastAt map[string]time.Time // username -> 上次发码时间（限流）
}

// emailCode 是一次邮箱验证请求的状态。
type emailCode struct {
	Code    string
	Email   string
	Expires time.Time
}

// emailCodeTTL 验证码有效期；emailCodeInterval 同一用户发码最小间隔（防刷）。
const (
	emailCodeTTL      = 10 * time.Minute
	emailCodeInterval = 60 * time.Second
)

// NewProfileService 创建个人资料服务。
func NewProfileService(accounts ProfileAccountStore, settings SettingsProvider) *ProfileService {
	return &ProfileService{
		accounts: accounts,
		settings: settings,
		codes:    map[string]emailCode{},
		lastAt:   map[string]time.Time{},
	}
}

// Profile 返回当前用户的资料（内置管理员只读）。
func (s *ProfileService) Profile(name string) (model.Profile, error) {
	return s.accounts.Profile(name)
}

// UpdateContact 保存当前用户的联系方式；修改邮箱会清除「已验证」标记，
// 避免用户把已验证状态「继承」到一个新地址上。
func (s *ProfileService) UpdateContact(name, email, wecomID, phone string) (model.Profile, error) {
	email = strings.TrimSpace(email)
	wecomID = strings.TrimSpace(wecomID)
	phone = strings.TrimSpace(phone)
	// 邮箱必填：它同时是构建结果邮件通知的收件地址，允许留空等于允许「静默收不到」。
	if email == "" {
		return model.Profile{}, pkg.NewError(pkg.CodeBadRequest, "邮箱不能为空（构建通知会用该邮箱发信）")
	}
	if !strings.Contains(email, "@") {
		return model.Profile{}, pkg.NewError(pkg.CodeBadRequest, "邮箱格式不正确")
	}
	before, err := s.accounts.Profile(name)
	if err != nil {
		return model.Profile{}, err
	}
	if before.Builtin {
		return model.Profile{}, pkg.NewError(pkg.CodeForbidden, "内置管理员的联系方式请在系统配置中维护")
	}
	if err = s.accounts.SetUserContact(name, email, wecomID, phone); err != nil {
		return model.Profile{}, err
	}
	if !strings.EqualFold(before.Email, email) {
		if err = s.accounts.SetEmailVerified(name, false); err != nil {
			return model.Profile{}, err
		}
	}
	return s.accounts.Profile(name)
}

// RequestEmailVerification 向当前用户填写的邮箱发送 6 位验证码。
// 返回收件邮箱与有效期；SMTP 未启用或未填邮箱时返回可读错误。
func (s *ProfileService) RequestEmailVerification(ctx context.Context, name string) (string, time.Duration, error) {
	profile, err := s.accounts.Profile(name)
	if err != nil {
		return "", 0, err
	}
	if profile.Builtin {
		return "", 0, pkg.NewError(pkg.CodeForbidden, "内置管理员不支持邮箱验证")
	}
	if profile.Email == "" {
		return "", 0, pkg.NewError(pkg.CodeBadRequest, "请先填写邮箱再验证")
	}
	settings := s.settings.Current()
	if !settings.Email.Enabled || settings.Email.SMTPHost == "" {
		return "", 0, pkg.NewError(pkg.CodeBadRequest, "系统未启用邮件服务，无法发送验证码（请在系统配置中启用 SMTP）")
	}

	now := time.Now()
	s.mu.Lock()
	if last, ok := s.lastAt[name]; ok && now.Sub(last) < emailCodeInterval {
		wait := emailCodeInterval - now.Sub(last)
		s.mu.Unlock()
		return "", 0, pkg.NewError(pkg.CodeTooManyRequests, "发送过于频繁，请 %d 秒后再试", int(wait.Seconds())+1)
	}
	code, err := randomCode()
	if err != nil {
		s.mu.Unlock()
		return "", 0, err
	}
	s.codes[name] = emailCode{Code: code, Email: profile.Email, Expires: now.Add(emailCodeTTL)}
	s.lastAt[name] = now
	s.mu.Unlock()

	body := fmt.Sprintf("你在 CI Platform 申请验证邮箱，验证码：%s\n\n有效期 %d 分钟。若非本人操作请忽略本邮件。\n",
		code, int(emailCodeTTL.Minutes()))
	mailCtx, cancel := context.WithTimeout(ctx, notifyMailTimeout)
	defer cancel()
	subject := fmt.Sprintf("[CI Platform] 邮箱验证码：%s", code)
	if err = executor.SendMailContext(mailCtx, settings.Email, []string{profile.Email}, subject, body); err != nil {
		// 发送失败要清掉验证码，否则用户拿不到码却占用了一次限流额度。
		s.mu.Lock()
		delete(s.codes, name)
		delete(s.lastAt, name)
		s.mu.Unlock()
		return "", 0, fmt.Errorf("发送验证码失败：%w", err)
	}
	return profile.Email, emailCodeTTL, nil
}

// ConfirmEmail 校验验证码并标记邮箱已验证。
// 邮箱在校验期间被改动时拒绝（验证码只对发码时的地址有效）。
func (s *ProfileService) ConfirmEmail(name, code string) (model.Profile, error) {
	code = strings.TrimSpace(code)
	if code == "" {
		return model.Profile{}, pkg.NewError(pkg.CodeBadRequest, "请输入验证码")
	}
	s.mu.Lock()
	pending, ok := s.codes[name]
	s.mu.Unlock()
	if !ok {
		return model.Profile{}, pkg.NewError(pkg.CodeBadRequest, "没有待验证的邮箱，请先发送验证码")
	}
	if time.Now().After(pending.Expires) {
		s.mu.Lock()
		delete(s.codes, name)
		s.mu.Unlock()
		return model.Profile{}, pkg.NewError(pkg.CodeBadRequest, "验证码已过期，请重新发送")
	}
	if pending.Code != code {
		return model.Profile{}, pkg.NewError(pkg.CodeBadRequest, "验证码不正确")
	}
	profile, err := s.accounts.Profile(name)
	if err != nil {
		return model.Profile{}, err
	}
	if !strings.EqualFold(profile.Email, pending.Email) {
		return model.Profile{}, pkg.NewError(pkg.CodeBadRequest, "邮箱已变更，请重新发送验证码")
	}
	if err = s.accounts.SetEmailVerified(name, true); err != nil {
		return model.Profile{}, err
	}
	s.mu.Lock()
	delete(s.codes, name)
	s.mu.Unlock()
	return s.accounts.Profile(name)
}

// randomCode 生成 6 位数字验证码（crypto/rand，避免可预测）。
func randomCode() (string, error) {
	max := big.NewInt(1000000)
	n, err := rand.Int(rand.Reader, max)
	if err != nil {
		return "", fmt.Errorf("生成验证码失败：%w", err)
	}
	return fmt.Sprintf("%06d", n.Int64()), nil
}

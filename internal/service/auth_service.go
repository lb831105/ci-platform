package service

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"log"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"

	"ci-platform/internal/pkg"
)

// session 是一个登录会话：token → 用户名 + 过期时间。
// 会话保存在内存中，进程重启即失效（基础版无数据库，符合项目定位）。
type session struct {
	username  string
	expiresAt time.Time
}

// UserVerifier 校验平台内管理的用户（由 RBACService 实现），
// 登录时在管理员凭据不匹配后回退调用。
type UserVerifier interface {
	VerifyLogin(name, password string) bool
}

// AuthService 负责管理员凭据校验与会话签发/校验。
// 凭据与开关可在运行时通过「全局安全配置」修改。
type AuthService struct {
	ttl time.Duration

	mu           sync.Mutex
	enabled      bool
	user         string
	passwordHash string // bcrypt hash；绝不保存明文
	userVerifier UserVerifier
	sessions     map[string]session
}

// NewAuthService 构造认证服务。ttl <= 0 时回退为 12 小时。
func NewAuthService(enabled bool, user, password string, ttl time.Duration) *AuthService {
	if ttl <= 0 {
		ttl = 12 * time.Hour
	}
	if user == "" {
		user = "admin"
	}
	hash, err := normalizePasswordHash(password)
	if err != nil {
		// 配置密码过长等极端情况：保持空 hash，认证必然失败，避免静默使用明文。
		log.Printf("WARN: 初始化管理员密码失败（登录将被拒绝）: %v", err)
	}
	return &AuthService{
		enabled:      enabled,
		user:         user,
		passwordHash: hash,
		ttl:          ttl,
		sessions:     make(map[string]session),
	}
}

// validatePasswordStrength 对新设置的密码做最小强度校验。
// 登录仍兼容历史上已存在的短密码，避免升级后把管理员锁在门外。
func validatePasswordStrength(password string) error {
	if password == "" {
		return pkg.ErrBadRequest("密码不能为空")
	}
	if len(password) < 8 {
		return pkg.ErrBadRequest("密码至少需要 8 个字符")
	}
	if len([]byte(password)) > 72 {
		return pkg.ErrBadRequest("密码过长（最多 72 字节）")
	}
	return nil
}

// normalizePasswordHash 接受已有 bcrypt hash 或首次启动的明文密码。
// 明文只在这里短暂存在，随后立即转为 bcrypt。
func normalizePasswordHash(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	if _, err := bcrypt.Cost([]byte(value)); err == nil {
		return value, nil
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(value), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}

// SetUserVerifier 注入平台内用户校验（多用户登录）。
func (a *AuthService) SetUserVerifier(v UserVerifier) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.userVerifier = v
}

// AdminUser 返回系统管理员用户名（RBAC 据此保证管理员始终 Administer）。
func (a *AuthService) AdminUser() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.user
}

// RevokeUserSessions 撤销指定用户的全部会话（删除用户/重置密码后调用）。
func (a *AuthService) RevokeUserSessions(username string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for token, s := range a.sessions {
		if s.username == username {
			delete(a.sessions, token)
		}
	}
}

// Enabled 返回是否开启认证。
func (a *AuthService) Enabled() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.enabled
}

// TTL 返回会话有效期。
func (a *AuthService) TTL() time.Duration { return a.ttl }

// SetEnabled 运行时切换认证开关。
func (a *AuthService) SetEnabled(enabled bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.enabled != enabled {
		// 关闭或重新开启认证时清空旧会话，避免重新开启后旧 token 仍然有效。
		a.sessions = make(map[string]session)
	}
	a.enabled = enabled
}

// SetCredentials 运行时更新管理员凭据（用于同步「系统配置」的修改）。
func (a *AuthService) SetCredentials(user, passwordHash string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	changed := false
	if user != "" && user != a.user {
		a.user = user
		changed = true
	}
	if passwordHash != "" {
		hash, err := normalizePasswordHash(passwordHash)
		if err != nil {
			log.Printf("WARN: 更新管理员密码失败: %v", err)
			return
		}
		if hash != a.passwordHash {
			a.passwordHash = hash
			changed = true
		}
	}
	if changed {
		// 修改管理员用户名/密码后，旧会话必须失效。
		a.sessions = make(map[string]session)
	}
}

// ChangePassword 校验旧密码后更新密码，并使除当前 token 外的会话失效。
func (a *AuthService) ChangePassword(oldPassword, newPassword, keepToken string) error {
	if err := validatePasswordStrength(newPassword); err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.credentialsMatchLocked(a.user, oldPassword) {
		return pkg.NewError(pkg.CodeUnauthorized, "当前密码不正确")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return pkg.ErrBadRequest("新密码不符合要求: %v", err)
	}
	a.passwordHash = string(hash)
	for token := range a.sessions {
		if token != keepToken {
			delete(a.sessions, token)
		}
	}
	return nil
}

// Login 校验凭据并签发 token。认证关闭时直接返回一个匿名会话。
func (a *AuthService) Login(username, password string) (string, string, time.Time, error) {
	expiresAt := time.Now().Add(a.ttl)

	a.mu.Lock()
	if !a.enabled {
		a.mu.Unlock()
		return "anonymous", "anonymous", expiresAt, nil
	}
	ok := a.credentialsMatchLocked(username, password)
	if !ok {
		// 管理员凭据不匹配时回退到平台内用户（Manage and Assign Roles 中创建）。
		if a.userVerifier != nil {
			ok = a.userVerifier.VerifyLogin(username, password)
		}
	}
	user := username
	if !ok {
		a.mu.Unlock()
		return "", "", time.Time{}, pkg.NewError(pkg.CodeUnauthorized, "用户名或密码错误")
	}
	a.purgeExpiredLocked(time.Now())
	token, err := newToken()
	if err != nil {
		a.mu.Unlock()
		return "", "", time.Time{}, err
	}
	a.sessions[token] = session{username: user, expiresAt: expiresAt}
	a.mu.Unlock()
	return token, user, expiresAt, nil
}

// UsesPassword 判断当前是否仍在使用指定明文密码（用于启动时提示默认口令风险）。
func (a *AuthService) UsesPassword(plain string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.passwordHash == "" {
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(a.passwordHash), []byte(plain)) == nil
}

// Verify 校验 token，返回用户名与是否有效。认证关闭时恒为有效。
func (a *AuthService) Verify(token string) (string, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.enabled {
		return "anonymous", true
	}
	if token == "" {
		return "", false
	}
	now := time.Now()
	s, ok := a.sessions[token]
	if !ok || now.After(s.expiresAt) {
		delete(a.sessions, token)
		return "", false
	}
	return s.username, true
}

// Logout 主动销毁会话。
func (a *AuthService) Logout(token string) {
	if token == "" {
		return
	}
	a.mu.Lock()
	delete(a.sessions, token)
	a.mu.Unlock()
}

// credentialsMatchLocked 使用常量时间比较，需在持锁状态下调用。
func (a *AuthService) credentialsMatchLocked(username, password string) bool {
	userOK := subtle.ConstantTimeCompare([]byte(username), []byte(a.user)) == 1
	if a.passwordHash == "" {
		return false
	}
	// 无论用户名是否正确都执行 bcrypt，避免通过响应时间枚举用户名。
	passOK := bcrypt.CompareHashAndPassword([]byte(a.passwordHash), []byte(password)) == nil
	return userOK && passOK
}

// purgeExpiredLocked 清理过期会话，需在持锁状态下调用。
func (a *AuthService) purgeExpiredLocked(now time.Time) {
	for token, s := range a.sessions {
		if now.After(s.expiresAt) {
			delete(a.sessions, token)
		}
	}
}

// newToken 生成 32 字节随机 token 的十六进制串。
func newToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", pkg.Wrap(err, "generate session token")
	}
	return hex.EncodeToString(buf), nil
}

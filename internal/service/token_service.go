package service

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"sort"
	"strings"
	"sync"
	"time"

	"ci-platform/internal/model"
	"ci-platform/internal/pkg"
)

// TokenStore 是 API Token 的持久化能力（由 store.TokenStore 实现）。
type TokenStore interface {
	Load() ([]model.APIToken, error)
	Save([]model.APIToken) error
}

// APITokenService 管理长期访问令牌。
//
// 设计取舍：令牌本身不落盘，只保存 SHA-256；即使用户数据目录泄露，
// 攻击者也无法还原出可用的 Bearer token。令牌继承创建者的权限，
// 因此删除用户或降权后，令牌的权限也会随 RBAC 实时变化。
type APITokenService struct {
	store TokenStore
	// userExists 用于在每次校验时确认账号仍然存在；为 nil 时跳过（测试/兼容）。
	userExists func(string) bool

	mu  sync.Mutex
	now func() time.Time
}

// NewAPITokenService 构造令牌服务。
func NewAPITokenService(store TokenStore) *APITokenService {
	return &APITokenService{store: store, now: time.Now}
}

// SetUserChecker 注入账号存在性检查（通常是「内置管理员或 RBAC 用户」）。
func (s *APITokenService) SetUserChecker(check func(string) bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.userExists = check
}

// APITokenCreateResult 是创建令牌的返回：Raw 只出现这一次。
type APITokenCreateResult struct {
	Token  model.APITokenView `json:"token"`
	Secret string             `json:"secret"`
}

const (
	maxAPITokensPerUser  = 20
	maxAPITokenNameRunes = 64
)

// Create 为指定用户创建令牌。expiresInDays<=0 表示永不过期（仍可手动吊销）。
func (s *APITokenService) Create(username, name string, expiresInDays int) (APITokenCreateResult, error) {
	username = strings.TrimSpace(username)
	name = strings.TrimSpace(name)
	if username == "" {
		return APITokenCreateResult{}, pkg.NewError(pkg.CodeUnauthorized, "未登录，无法创建访问令牌")
	}
	if name == "" {
		return APITokenCreateResult{}, pkg.ErrBadRequest("令牌名称不能为空")
	}
	if len([]rune(name)) > maxAPITokenNameRunes {
		return APITokenCreateResult{}, pkg.ErrBadRequest("令牌名称最多 %d 个字符", maxAPITokenNameRunes)
	}
	if expiresInDays < 0 || expiresInDays > 3650 {
		return APITokenCreateResult{}, pkg.ErrBadRequest("有效期必须在 1–3650 天之间，或传 0 表示永不过期")
	}

	raw, err := newAPITokenSecret()
	if err != nil {
		return APITokenCreateResult{}, err
	}
	now := s.now().UTC()
	var expiresAt *time.Time
	if expiresInDays > 0 {
		t := now.Add(time.Duration(expiresInDays) * 24 * time.Hour)
		expiresAt = &t
	}
	token := model.APIToken{
		ID:        randomID(),
		Name:      name,
		Username:  username,
		TokenHash: hashAPIToken(raw),
		Prefix:    tokenPrefix(raw),
		CreatedAt: now,
		ExpiresAt: expiresAt,
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	list, err := s.store.Load()
	if err != nil {
		return APITokenCreateResult{}, err
	}
	count := 0
	for _, t := range list {
		if t.Username == username {
			count++
		}
	}
	if count >= maxAPITokensPerUser {
		return APITokenCreateResult{}, pkg.ErrBadRequest("每个用户最多保留 %d 个访问令牌，请先吊销不用的令牌", maxAPITokensPerUser)
	}
	list = append(list, token)
	if err := s.store.Save(list); err != nil {
		return APITokenCreateResult{}, err
	}
	return APITokenCreateResult{Token: token.View(), Secret: raw}, nil
}

// List 返回指定用户的令牌（脱敏视图），按创建时间倒序。
func (s *APITokenService) List(username string) ([]model.APITokenView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	list, err := s.store.Load()
	if err != nil {
		return nil, err
	}
	out := make([]model.APITokenView, 0, len(list))
	for _, t := range list {
		if t.Username == username {
			out = append(out, t.View())
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

// Revoke 吊销指定用户自己的令牌。
func (s *APITokenService) Revoke(username, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	list, err := s.store.Load()
	if err != nil {
		return err
	}
	for i, t := range list {
		if t.ID == id && t.Username == username {
			list = append(list[:i], list[i+1:]...)
			return s.store.Save(list)
		}
	}
	return pkg.ErrNotFound("访问令牌不存在")
}

// RevokeUserTokens 删除某个用户的全部令牌（删除用户/重置密码后调用）。
func (s *APITokenService) RevokeUserTokens(username string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	list, err := s.store.Load()
	if err != nil {
		return err
	}
	out := list[:0]
	for _, t := range list {
		if t.Username != username {
			out = append(out, t)
		}
	}
	return s.store.Save(out)
}

// VerifyAPIToken 校验原始 token，返回归属用户。
// LastUsedAt 做一分钟节流，避免每个 API 请求都写一次磁盘。
func (s *APITokenService) VerifyAPIToken(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if !strings.HasPrefix(raw, "cip_") {
		return "", false
	}
	hash := hashAPIToken(raw)

	s.mu.Lock()
	defer s.mu.Unlock()
	list, err := s.store.Load()
	if err != nil {
		return "", false
	}
	now := s.now().UTC()
	for i := range list {
		t := &list[i]
		if subtle.ConstantTimeCompare([]byte(t.TokenHash), []byte(hash)) != 1 {
			continue
		}
		if t.ExpiresAt != nil && now.After(*t.ExpiresAt) {
			return "", false
		}
		if s.userExists != nil && !s.userExists(t.Username) {
			return "", false
		}
		if t.LastUsedAt == nil || now.Sub(*t.LastUsedAt) >= time.Minute {
			t.LastUsedAt = &now
			if err := s.store.Save(list); err != nil {
				// 记录最后使用时间失败不应让一个本来有效的 token 失效。
				return t.Username, true
			}
		}
		return t.Username, true
	}
	return "", false
}

// newAPITokenSecret 生成 32 字节随机 secret，前缀便于在日志中识别并做泄露扫描。
func newAPITokenSecret() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", pkg.Wrap(err, "generate api token")
	}
	return "cip_" + hex.EncodeToString(buf), nil
}

func hashAPIToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func tokenPrefix(raw string) string {
	raw = strings.TrimPrefix(raw, "cip_")
	if len(raw) > 8 {
		raw = raw[:8]
	}
	return "cip_" + raw + "…"
}

func randomID() string {
	buf := make([]byte, 12)
	if _, err := rand.Read(buf); err != nil {
		return hex.EncodeToString([]byte(time.Now().UTC().Format(time.RFC3339Nano)))
	}
	return hex.EncodeToString(buf)
}

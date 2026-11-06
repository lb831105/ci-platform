package model

import "time"

// APIToken 是给脚本/外部系统使用的长期令牌。
// 只持久化 SHA-256 哈希；原始 token 只在创建响应里返回一次，之后无法再读取。
type APIToken struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Username   string     `json:"username"`
	TokenHash  string     `json:"token_hash"`
	Prefix     string     `json:"prefix"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
	// Scopes 是令牌可执行的操作（如 job.read/job.build）；空表示继承用户全部权限，
	// "*" 也表示全部权限。旧令牌没有该字段时按空处理，保持向后兼容。
	Scopes []string `json:"scopes,omitempty"`
}

// APITokenView 是接口返回的脱敏视图：绝不包含 TokenHash。
type APITokenView struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Prefix     string     `json:"prefix"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at,omitempty"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
	Scopes     []string   `json:"scopes,omitempty"`
}

// View 返回脱敏副本。
func (t APIToken) View() APITokenView {
	return APITokenView{
		ID: t.ID, Name: t.Name, Prefix: t.Prefix,
		CreatedAt: t.CreatedAt, LastUsedAt: t.LastUsedAt, ExpiresAt: t.ExpiresAt,
		Scopes: append([]string(nil), t.Scopes...),
	}
}

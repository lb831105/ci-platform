package store

import (
	"path/filepath"
	"sync"

	"ci-platform/internal/model"
	"ci-platform/internal/pkg"
)

// TokenStore 把 API Token 元数据（只含哈希）保存到 data/tokens.json。
type TokenStore struct {
	path string
	mu   sync.Mutex
}

// NewTokenStore 创建令牌存储。
func NewTokenStore(dataHome string) *TokenStore {
	return &TokenStore{path: filepath.Join(dataHome, "tokens.json")}
}

// Load 读取全部令牌；文件不存在时返回空列表。
func (s *TokenStore) Load() ([]model.APIToken, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !pkg.Exists(s.path) {
		return []model.APIToken{}, nil
	}
	var out []model.APIToken
	if err := pkg.ReadJSON(s.path, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// Save 原子写入全部令牌（文件权限 0600）。
func (s *TokenStore) Save(tokens []model.APIToken) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return pkg.WriteJSONAtomic(s.path, tokens)
}

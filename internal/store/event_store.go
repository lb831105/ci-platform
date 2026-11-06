package store

import (
	"path/filepath"
	"sync"

	"ci-platform/internal/model"
	"ci-platform/internal/pkg"
)

// EventStore 把全局出站事件订阅保存到 data/events.json。
// URL（可能含 token）与 HMAC Secret 使用 credentials.key 加密落盘。
type EventStore struct {
	path string
	box  *pkg.SecretBox
	mu   sync.Mutex
}

// NewEventStore 创建事件订阅存储。
func NewEventStore(dataHome string) *EventStore {
	return &EventStore{
		path: filepath.Join(dataHome, "events.json"),
		box:  pkg.NewSecretBox(filepath.Join(dataHome, "credentials.key")),
	}
}

// Load 读取并解密全部订阅；文件不存在时返回空列表。
func (s *EventStore) Load() ([]model.EventSubscription, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !pkg.Exists(s.path) {
		return []model.EventSubscription{}, nil
	}
	var out []model.EventSubscription
	if err := pkg.ReadJSON(s.path, &out); err != nil {
		return nil, err
	}
	for i := range out {
		url, err := s.box.Unseal(out[i].URL)
		if err != nil {
			return nil, err
		}
		secret, err := s.box.Unseal(out[i].Secret)
		if err != nil {
			return nil, err
		}
		out[i].URL = url
		out[i].Secret = secret
		out[i].HasURL = false
		out[i].HasSecret = false
		out[i].ClearSecret = false
	}
	return out, nil
}

// Save 在副本上加密后原子写入，避免修改 service 内存中的明文。
func (s *EventStore) Save(items []model.EventSubscription) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]model.EventSubscription, len(items))
	copy(out, items)
	for i := range out {
		url, err := s.box.Seal(out[i].URL)
		if err != nil {
			return err
		}
		secret, err := s.box.Seal(out[i].Secret)
		if err != nil {
			return err
		}
		out[i].URL = url
		out[i].Secret = secret
		out[i].HasURL = false
		out[i].HasSecret = false
		out[i].ClearSecret = false
	}
	return pkg.WriteJSONAtomic(s.path, out)
}

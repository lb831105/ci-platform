package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

	"ci-platform/internal/model"
	"ci-platform/internal/pkg"
)

// NotifyStore 通知投递记录存储：JSONL 追加写（data/notify.log），每行一条 JSON。
// 按「收件人/群」粒度记录，才能回答「谁漏收了告警」这类问题；
// 追加型数据用 JSONL，断电最多丢最后一行。
type NotifyStore struct {
	path     string
	maxBytes int64
	mu       sync.Mutex
}

// NewNotifyStore 创建通知记录存储，dataHome 为 data 目录。
func NewNotifyStore(dataHome string) *NotifyStore {
	return &NotifyStore{path: filepath.Join(dataHome, "notify.log"), maxBytes: maxNotifyBytes}
}

// maxNotifyBytes 单个通知记录文件上限，超过后轮转为 notify.log.1（只留一份历史）。
const maxNotifyBytes = 16 << 20

// Append 追加一批通知记录（一次构建一次写入，减少 fsync 次数）。
func (s *NotifyStore) Append(records []model.NotifyRecord) error {
	if len(records) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := pkg.EnsureDir(filepath.Dir(s.path)); err != nil {
		return err
	}
	// 轮转：先删旧备份再改名（Windows 不允许 rename 覆盖已存在文件）。
	if info, err := os.Stat(s.path); err == nil && s.maxBytes > 0 && info.Size() >= s.maxBytes {
		_ = os.Remove(s.path + ".1")
		_ = os.Rename(s.path, s.path+".1")
	}
	f, err := os.OpenFile(s.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	buf := make([]byte, 0, 512*len(records))
	for i := range records {
		line, err := json.Marshal(&records[i])
		if err != nil {
			continue
		}
		buf = append(buf, line...)
		buf = append(buf, '\n')
	}
	if _, err := f.Write(buf); err != nil {
		return err
	}
	// 通知记录是排查「漏收告警」的依据，尽量落盘。
	_ = f.Sync()
	return nil
}

// Tail 返回最近 limit 条通知记录（时间倒序，最新在前）。
func (s *NotifyStore) Tail(limit int) ([]model.NotifyRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if limit <= 0 {
		limit = 200
	}
	if !pkg.Exists(s.path) {
		return []model.NotifyRecord{}, nil
	}
	f, err := os.Open(s.path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	lines := tailLines(f, limit)
	out := make([]model.NotifyRecord, 0, len(lines))
	for _, line := range lines {
		if len(out) >= limit {
			break
		}
		var r model.NotifyRecord
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			continue // 损坏行跳过，不影响其余记录
		}
		out = append(out, r)
	}
	return out, nil
}

// Path 返回存储文件路径。
func (s *NotifyStore) Path() string { return s.path }

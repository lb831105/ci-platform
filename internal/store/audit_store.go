package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"ci-platform/internal/model"
	"ci-platform/internal/pkg"
)

// AuditStore 操作审计日志存储：JSONL 追加写（data/audit.log），
// 每行一条 JSON。审计是追加型数据，JSONL 比 JSON 数组原子写更合适：
// 追加不重写全文件，且断电最多丢最后一行。
type AuditStore struct {
	path     string
	maxBytes int64 // 轮转阈值（测试可调小）
	mu       sync.Mutex
}

// NewAuditStore 创建审计存储，dataHome 为 data 目录。
func NewAuditStore(dataHome string) *AuditStore {
	return &AuditStore{path: filepath.Join(dataHome, "audit.log"), maxBytes: maxAuditBytes}
}

// maxAuditBytes 单个审计文件上限；超过后轮转为 audit.log.1（只保留一份历史）。
// 审计是纯追加数据，无上限会持续增长并最终写满磁盘。
const maxAuditBytes = 16 << 20 // 16MB

// Append 追加一条审计记录（带文件锁，多 goroutine 安全）。
func (s *AuditStore) Append(e *model.AuditEntry) error {
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
	f, err := os.OpenFile(s.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	line, err := json.Marshal(e)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		return err
	}
	// 审计日志要求尽量落盘（审计价值高于性能）。
	_ = f.Sync()
	return nil
}

// Tail 返回最近 limit 条审计记录（时间倒序，最新在前）。
// 文件按行从后往前读取，避免整文件载入内存。
func (s *AuditStore) Tail(limit int) ([]model.AuditEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if limit <= 0 {
		limit = 200
	}
	if !pkg.Exists(s.path) {
		return []model.AuditEntry{}, nil
	}
	f, err := os.Open(s.path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	lines := tailLines(f, limit)
	out := make([]model.AuditEntry, 0, len(lines))
	for _, line := range lines {
		if len(out) >= limit {
			break
		}
		var e model.AuditEntry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			continue // 损坏行跳过，不影响其余记录
		}
		out = append(out, e)
	}
	return out, nil
}

// Path 返回存储文件路径，供「系统信息」展示。
func (s *AuditStore) Path() string { return s.path }

// tailLines 从文件尾部倒序读取最多 limit 条非空行（最新在前）。
// 审计与通知记录共用：按 4KB 块向前扫描，避免整文件载入内存。
func tailLines(f *os.File, limit int) []string {
	const chunk = 4096
	stat, err := f.Stat()
	if err != nil {
		return nil
	}
	var lines []string
	buf := make([]byte, chunk)
	end := stat.Size()
	var carry []byte // 向前块带来的不完整行
	for end > 0 && len(lines) <= limit {
		start := end - chunk
		if start < 0 {
			start = 0
		}
		n, err := f.ReadAt(buf[:end-start], start)
		if err != nil && n == 0 {
			break
		}
		block := append([]byte(nil), buf[:n]...)
		block = append(block, carry...)
		// 按行拆分：最后一段是本块最前面可能不完整的行，留到下一轮。
		parts := strings.Split(string(block), "\n")
		carry = []byte(parts[0])
		// 从尾部往前收集完整行。
		for i := len(parts) - 1; i >= 1; i-- {
			if strings.TrimSpace(parts[i]) != "" {
				lines = append(lines, parts[i])
				if len(lines) > limit {
					break
				}
			}
		}
		end = start
	}
	if end == 0 && len(carry) > 0 && strings.TrimSpace(string(carry)) != "" {
		lines = append(lines, string(carry))
	}
	return lines
}

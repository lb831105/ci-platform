package store

import (
	"os"
	"testing"

	"ci-platform/internal/model"
)

// JSONL 追加与倒序读取。
func TestAuditStoreAppendTail(t *testing.T) {
	s := NewAuditStore(t.TempDir())
	for i := 1; i <= 5; i++ {
		e := &model.AuditEntry{
			ID:     int64(i),
			User:   "admin",
			Action: "login",
			Target: "POST /api/auth/login",
			Status: 0,
			IP:     "127.0.0.1",
		}
		if err := s.Append(e); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}
	entries, err := s.Tail(10)
	if err != nil {
		t.Fatalf("tail: %v", err)
	}
	if len(entries) != 5 {
		t.Fatalf("entries = %d, want 5", len(entries))
	}
	// 最新在前
	if entries[0].ID != 5 || entries[4].ID != 1 {
		t.Fatalf("order wrong: first=%d last=%d", entries[0].ID, entries[4].ID)
	}
	if entries[0].User != "admin" || entries[0].Action != "login" {
		t.Fatalf("fields wrong: %+v", entries[0])
	}
}

// limit 截断：只返回最近 N 条。
func TestAuditStoreTailLimit(t *testing.T) {
	s := NewAuditStore(t.TempDir())
	for i := 1; i <= 10; i++ {
		_ = s.Append(&model.AuditEntry{ID: int64(i), User: "u", Action: "create"})
	}
	entries, err := s.Tail(3)
	if err != nil {
		t.Fatalf("tail: %v", err)
	}
	if len(entries) != 3 || entries[0].ID != 10 || entries[2].ID != 8 {
		t.Fatalf("entries = %d, first=%d, want 3/10", len(entries), entries[0].ID)
	}
}

// 损坏行跳过，不影响其余记录。
func TestAuditStoreSkipsCorruptLines(t *testing.T) {
	s := NewAuditStore(t.TempDir())
	_ = s.Append(&model.AuditEntry{ID: 1, User: "u", Action: "create"})
	// 直接追加损坏行
	if err := appendRawLine(s.path, "not-a-json-line"); err != nil {
		t.Fatalf("raw append: %v", err)
	}
	_ = s.Append(&model.AuditEntry{ID: 2, User: "u", Action: "delete"})
	entries, err := s.Tail(10)
	if err != nil {
		t.Fatalf("tail: %v", err)
	}
	if len(entries) != 2 || entries[0].ID != 2 || entries[1].ID != 1 {
		t.Fatalf("entries = %+v, want 2 valid entries", entries)
	}
}

// appendRawLine 直接追加一行原始文本（测试损坏行）。
func appendRawLine(path, line string) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(line + "\n")
	return err
}

// 超过阈值时轮转为 audit.log.1，避免审计文件无限增长。
func TestAuditStoreRotates(t *testing.T) {
	dir := t.TempDir()
	s := NewAuditStore(dir)
	s.maxBytes = 512 // 调小阈值便于测试
	for i := 1; i <= 20; i++ {
		if err := s.Append(&model.AuditEntry{ID: int64(i), User: "u", Action: "create", Target: "POST /api/x"}); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}
	if _, err := os.Stat(s.path + ".1"); err != nil {
		t.Fatalf("rotate backup missing: %v", err)
	}
	// 轮转后仍能正常读取当前文件
	entries, err := s.Tail(5)
	if err != nil || len(entries) == 0 {
		t.Fatalf("tail after rotate: %d entries, err=%v", len(entries), err)
	}
	if entries[0].ID != 20 {
		t.Fatalf("latest id = %d, want 20", entries[0].ID)
	}
}

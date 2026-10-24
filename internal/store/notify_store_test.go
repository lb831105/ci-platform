package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"ci-platform/internal/model"
)

// 通知记录：追加后能倒序读回，损坏行跳过，超过阈值轮转。
func TestNotifyStoreAppendTailAndRotate(t *testing.T) {
	dir := t.TempDir()
	s := NewNotifyStore(dir)
	now := time.Now().UTC()
	if err := s.Append([]model.NotifyRecord{
		{ID: 1, Time: now, PipelineID: "p", Recipient: "a@x.com", Status: model.NotifyStatusSent},
		{ID: 2, Time: now, PipelineID: "p", Recipient: "b@x.com", Status: model.NotifyStatusFailed},
	}); err != nil {
		t.Fatalf("append: %v", err)
	}
	got, err := s.Tail(10)
	if err != nil {
		t.Fatalf("tail: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("tail = %d records, want 2", len(got))
	}
	// 倒序：最新的在前。
	if got[0].ID != 2 || got[1].ID != 1 {
		t.Fatalf("order = %d,%d; want 2,1", got[0].ID, got[1].ID)
	}
	// 限制条数。
	if one, _ := s.Tail(1); len(one) != 1 || one[0].ID != 2 {
		t.Fatalf("limit tail = %+v", one)
	}
	// 空列表不写入、不报错。
	if err := s.Append(nil); err != nil {
		t.Fatalf("append empty: %v", err)
	}

	// 损坏行跳过：手工追加一行非法 JSON。
	f, err := os.OpenFile(filepath.Join(dir, "notify.log"), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err = f.WriteString("{not json}\n"); err != nil {
		t.Fatalf("write: %v", err)
	}
	_ = f.Close()
	if got, err = s.Tail(10); err != nil || len(got) != 2 {
		t.Fatalf("corrupt line must be skipped: %d records, err=%v", len(got), err)
	}

	// 轮转：把阈值调小后追加，旧文件改名。
	s.maxBytes = 10
	if err := s.Append([]model.NotifyRecord{{ID: 3, Time: now}}); err != nil {
		t.Fatalf("append after rotate: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "notify.log.1")); err != nil {
		t.Fatalf("expected rotation to notify.log.1: %v", err)
	}
	if got, _ := s.Tail(10); len(got) != 1 || got[0].ID != 3 {
		t.Fatalf("after rotate tail = %+v", got)
	}
}

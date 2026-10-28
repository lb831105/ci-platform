package service

import (
	"testing"
	"time"

	"ci-platform/internal/model"
)

type captureAuditRecorder struct {
	entries []model.AuditEntry
}

func (c *captureAuditRecorder) Append(e *model.AuditEntry) error {
	c.entries = append(c.entries, *e)
	return nil
}

func (c *captureAuditRecorder) Tail(int) ([]model.AuditEntry, error) { return nil, nil }

func TestAuditPostFillsTime(t *testing.T) {
	rec := &captureAuditRecorder{}
	svc := NewAuditService(rec, 0)
	svc.Record("alice", "create", "POST /api/pipelines", 0, "127.0.0.1")
	if len(rec.entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(rec.entries))
	}
	got := rec.entries[0]
	if got.Time.IsZero() {
		t.Fatal("audit time must be filled")
	}
	if time.Since(got.Time) > time.Minute {
		t.Fatalf("audit time = %v, want recent UTC time", got.Time)
	}
	if got.ID != 1 {
		t.Fatalf("id = %d, want 1", got.ID)
	}
}

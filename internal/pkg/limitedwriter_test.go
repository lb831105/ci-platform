package pkg

import (
	"bytes"
	"strings"
	"testing"
)

// 不超限：全部透传。
func TestLimitedWriterPassthrough(t *testing.T) {
	var buf bytes.Buffer
	lw := NewLimitedWriter(&buf, 100, []byte("[TRUNCATED]"))
	data := strings.Repeat("a", 100)
	if n, err := lw.Write([]byte(data)); err != nil || n != 100 {
		t.Fatalf("write: n=%d err=%v", n, err)
	}
	if buf.Len() != 100 || strings.Contains(buf.String(), "[TRUNCATED]") {
		t.Fatalf("buf = %d bytes", buf.Len())
	}
}

// 超限：写入在 max 处截断并追加标记，后续写入全部丢弃但不报错。
func TestLimitedWriterTruncates(t *testing.T) {
	var buf bytes.Buffer
	lw := NewLimitedWriter(&buf, 50, []byte("\n[TRUNCATED]\n"))
	_, _ = lw.Write([]byte(strings.Repeat("a", 80)))
	_, _ = lw.Write([]byte(strings.Repeat("b", 80))) // 应全部丢弃
	out := buf.String()
	if len(out) != 50+len("\n[TRUNCATED]\n") {
		t.Fatalf("out = %d bytes, want %d", len(out), 50+len("\n[TRUNCATED]\n"))
	}
	if !strings.Contains(out, "[TRUNCATED]") {
		t.Fatal("truncation marker missing")
	}
	// 标记只写一次
	if strings.Count(out, "[TRUNCATED]") != 1 {
		t.Fatal("marker should appear once")
	}
}

// max <= 0：不限制。
func TestLimitedWriterUnlimited(t *testing.T) {
	var buf bytes.Buffer
	lw := NewLimitedWriter(&buf, 0, nil)
	_, _ = lw.Write([]byte(strings.Repeat("a", 1000)))
	if buf.Len() != 1000 {
		t.Fatalf("buf = %d bytes, want 1000", buf.Len())
	}
}

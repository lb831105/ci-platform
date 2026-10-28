package executor

import (
	"strings"
	"testing"
)

func TestTruncateRunes(t *testing.T) {
	long := strings.Repeat("提交", maxCommitMessageRunes+10)
	got := truncateRunes(long, maxCommitMessageRunes)
	if []rune(got)[0] != '提' || !strings.HasSuffix(got, "…") {
		t.Fatalf("unexpected truncation: %q", got[:20])
	}
	if len([]rune(got)) != maxCommitMessageRunes+1 {
		t.Fatalf("runes = %d, want %d", len([]rune(got)), maxCommitMessageRunes+1)
	}
}

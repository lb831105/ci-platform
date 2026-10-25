package pkg

import (
	"testing"
	"time"
)

func TestDedupeFirst(t *testing.T) {
	d := NewDedupe(time.Minute, 8)
	if !d.First("delivery-1") {
		t.Fatal("first delivery must be accepted")
	}
	if d.First("delivery-1") {
		t.Fatal("duplicate delivery must be rejected")
	}
	if !d.First("delivery-2") {
		t.Fatal("another delivery must be accepted")
	}
	// 空 key（平台没给投递标识）不做去重，避免把所有请求当成重复。
	if !d.First("") || !d.First("") {
		t.Fatal("empty key must always be accepted")
	}
}

func TestDedupeTTLExpiry(t *testing.T) {
	d := NewDedupe(20*time.Millisecond, 8)
	if !d.First("k") {
		t.Fatal("first must pass")
	}
	time.Sleep(40 * time.Millisecond)
	if !d.First("k") {
		t.Fatal("key must be accepted again after TTL")
	}
}

func TestDedupeCapacity(t *testing.T) {
	d := NewDedupe(time.Minute, 2)
	d.First("a")
	d.First("b")
	d.First("c") // 超出容量，淘汰最旧的 a
	if got := d.Len(); got != 2 {
		t.Fatalf("len = %d, want 2", got)
	}
	if !d.First("a") {
		t.Fatal("evicted key must be treated as new")
	}
}

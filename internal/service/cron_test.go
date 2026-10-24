package service

import (
	"testing"
	"time"
)

// 标准 5 字段解析：各字段取值集合正确。
func TestParseCronStandard(t *testing.T) {
	s, err := ParseCron("*/15 9-17 1,15 * 1-5", "job")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !s.minutes[0] || !s.minutes[15] || !s.minutes[45] || s.minutes[10] {
		t.Fatalf("minutes wrong: %v", s.minutes)
	}
	if !s.hours[9] || !s.hours[17] || s.hours[8] {
		t.Fatalf("hours wrong: %v", s.hours)
	}
	if !s.doms[1] || !s.doms[15] {
		t.Fatalf("doms wrong: %v", s.doms)
	}
	if s.dowStar {
		t.Fatal("dow 1-5 should be restricted")
	}
	if s.domStar {
		t.Fatal("dom should be restricted")
	}
}

// H 散列：同一任务名偏移稳定，且落在合法范围内。
func TestParseCronHash(t *testing.T) {
	s1, err := ParseCron("H/15 * * * *", "my-job")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	s2, _ := ParseCron("H/15 * * * *", "my-job")
	// 同名任务散列一致（重启后偏移不变）
	if len(s1.minutes) != 4 || len(s2.minutes) != 4 {
		t.Fatalf("H/15 should yield 4 values, got %v", s1.minutes)
	}
	minOffset := 60
	for m := range s1.minutes {
		if !s2.minutes[m] {
			t.Fatal("hash offset unstable")
		}
		if m < minOffset {
			minOffset = m
		}
	}
	if minOffset >= 15 {
		t.Fatalf("H/15 offset %d should be < 15", minOffset)
	}
	// 不同任务名散列不同（负载均衡）
	s3, _ := ParseCron("H/15 * * * *", "other-job")
	same := len(s3.minutes) == len(s1.minutes)
	if same {
		equal := true
		for m := range s1.minutes {
			if !s3.minutes[m] {
				equal = false
			}
		}
		if equal {
			t.Log("different jobs hashed to same offset (possible, not an error)")
		}
	}
}

// 非法规格：字段数不对 / 范围越界。
func TestParseCronInvalid(t *testing.T) {
	if _, err := ParseCron("* * * *", "j"); err == nil {
		t.Fatal("4 fields should fail")
	}
	if _, err := ParseCron("60 * * * *", "j"); err == nil {
		t.Fatal("minute 60 should fail")
	}
	if _, err := ParseCron("* 24 * * *", "j"); err == nil {
		t.Fatal("hour 24 should fail")
	}
	if _, err := ParseCron("* * 0 * *", "j"); err == nil {
		t.Fatal("dom 0 should fail")
	}
}

// NextCron：精确时刻命中。
func TestNextCronExact(t *testing.T) {
	after := time.Date(2026, 1, 1, 10, 0, 30, 0, time.Local)
	next, err := NextCron("30 10 1 1 *", "j", after)
	if err != nil {
		t.Fatalf("next: %v", err)
	}
	// after 是 10:00:30，当年 10:30 尚未到，应命中 2026-01-01 10:30。
	if next.Year() != 2026 || next.Month() != time.January || next.Hour() != 10 || next.Minute() != 30 {
		t.Fatalf("next = %v, want 2026-01-01 10:30", next)
	}
}

// NextCron：每分钟规格立即命中下一分钟。
func TestNextCronEveryMinute(t *testing.T) {
	after := time.Date(2026, 5, 10, 8, 30, 15, 0, time.Local)
	next, err := NextCron("* * * * *", "j", after)
	if err != nil {
		t.Fatalf("next: %v", err)
	}
	if next.Hour() != 8 || next.Minute() != 31 {
		t.Fatalf("next = %v, want 08:31", next)
	}
}

// dom 与 dow 都受限：取并集（Vixie cron 语义）。
func TestNextCronDomDowUnion(t *testing.T) {
	// 2026-05-10 是周日；dom=1、dow=7（周日）——5 月 1 日（周五）应命中（dom）
	after := time.Date(2026, 4, 20, 0, 0, 0, 0, time.Local)
	next, err := NextCron("0 12 1 * 0", "j", after)
	if err != nil {
		t.Fatalf("next: %v", err)
	}
	// 4/26 是周日（dow 命中），早于 5/1（dom 命中）——并集语义应先触发周日。
	if next.Day() != 26 || next.Month() != time.April {
		t.Fatalf("next = %v, want Apr 26 (dow match, union semantics)", next)
	}
}

// 一年内无命中的规格（2 月 30 日）返回错误。
func TestNextCronImpossible(t *testing.T) {
	after := time.Date(2026, 1, 1, 0, 0, 0, 0, time.Local)
	if _, err := NextCron("0 0 30 2 *", "j", after); err == nil {
		t.Fatal("Feb 30 should never match")
	}
}

// H(a-b) 范围散列。
func TestParseCronHashRange(t *testing.T) {
	s, err := ParseCron("H(30-40) * * * *", "j")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	count := 0
	for m := range s.minutes {
		if m < 30 || m > 40 {
			t.Fatalf("value %d out of [30,40]", m)
		}
		count++
	}
	if count != 1 {
		t.Fatalf("H(30-40) should yield exactly 1 value, got %d", count)
	}
}

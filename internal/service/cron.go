package service

import (
	"fmt"
	"hash/fnv"
	"strconv"
	"strings"
	"time"
)

// cronSchedule 解析后的 5 字段 cron 规格（分 时 日 月 周）。
// 支持：* 、n 、a-b 、a-b/n 、*/n 、H 、H/n 、H(a-b)。
// H 为 Jenkins 散列语义：不落在整点，而是按任务名散列到区间内固定偏移，
// 避免大量任务配置相同规格时同一瞬间集中触发。
type cronSchedule struct {
	minutes map[int]bool
	hours   map[int]bool
	doms    map[int]bool
	months  map[int]bool
	dows    map[int]bool
	domStar bool // 日字段为 *
	dowStar bool // 周字段为 *
}

// cronFieldRanges 各字段合法取值范围（分 时 日 月 周；周日为 0）。
var cronFieldRanges = [5][2]int{{0, 59}, {0, 23}, {1, 31}, {1, 12}, {0, 6}}

// ParseCron 解析 5 字段 cron 规格；key 用于 H 字段的散列。
func ParseCron(spec string, key string) (*cronSchedule, error) {
	fields := strings.Fields(spec)
	if len(fields) != 5 {
		return nil, fmt.Errorf("cron 规格需要 5 个字段（分 时 日 月 周），收到 %d 个", len(fields))
	}
	s := &cronSchedule{
		minutes: map[int]bool{}, hours: map[int]bool{}, doms: map[int]bool{},
		months: map[int]bool{}, dows: map[int]bool{},
	}
	parsers := []struct {
		field string
		rng   [2]int
		out   *map[int]bool
		star  *bool
	}{
		{fields[0], cronFieldRanges[0], &s.minutes, nil},
		{fields[1], cronFieldRanges[1], &s.hours, nil},
		{fields[2], cronFieldRanges[2], &s.doms, &s.domStar},
		{fields[3], cronFieldRanges[3], &s.months, nil},
		{fields[4], cronFieldRanges[4], &s.dows, &s.dowStar},
	}
	for _, p := range parsers {
		star, err := parseCronField(p.field, p.rng, key, p.out)
		if err != nil {
			return nil, fmt.Errorf("字段 %q 非法: %w", p.field, err)
		}
		if p.star != nil {
			*p.star = star
		}
	}
	// 周字段允许 7 表示周日（归一到 0）。
	if s.dows[7] {
		s.dows[0] = true
	}
	return s, nil
}

// parseCronField 解析单个字段（已按 ',' 拆分的整体），返回字段是否为 *。
func parseCronField(field string, rng [2]int, key string, out *map[int]bool) (bool, error) {
	min, max := rng[0], rng[1]
	isStar := false
	for _, part := range strings.Split(field, ",") {
		if err := parseCronPart(strings.TrimSpace(part), min, max, key, out, &isStar); err != nil {
			return false, err
		}
	}
	return isStar, nil
}

// parseCronPart 解析单个逗号分隔项。
func parseCronPart(part string, min, max int, key string, out *map[int]bool, isStar *bool) error {
	add := func(v int) { (*out)[v] = true }

	// H 散列：H / H/n / H(a-b)。
	if strings.HasPrefix(part, "H") {
		offset := int(hashKey(key))
		rest := strings.TrimPrefix(part, "H")
		// H(a-b) 或 H(a-b)/n（/n 暂不支持，语义歧义）
		if strings.HasPrefix(rest, "(") {
			close := strings.Index(rest, ")")
			if close < 0 {
				return fmt.Errorf("H(a-b) 缺少右括号")
			}
			r := rest[1:close]
			if strings.Contains(rest[close:], "/") {
				return fmt.Errorf("暂不支持 H(a-b)/n")
			}
			bounds := strings.SplitN(r, "-", 2)
			if len(bounds) != 2 {
				return fmt.Errorf("H(a-b) 范围非法: %q", r)
			}
			a, err1 := strconv.Atoi(bounds[0])
			b, err2 := strconv.Atoi(bounds[1])
			if err1 != nil || err2 != nil || a < min || b > max || a > b {
				return fmt.Errorf("H(%s) 超出取值范围 [%d,%d]", r, min, max)
			}
			add(a + offset%(b-a+1))
			return nil
		}
		if rest == "" {
			add(min + offset%(max-min+1))
			return nil
		}
		if strings.HasPrefix(rest, "/") {
			step, err := strconv.Atoi(rest[1:])
			if err != nil || step <= 0 {
				return fmt.Errorf("H/%s 步长非法", rest[1:])
			}
			// Jenkins 语义：散列偏移落在每个步长区间内。
			start := offset % step
			for v := min + start; v <= max; v += step {
				add(v)
			}
			return nil
		}
		return fmt.Errorf("无法解析 H 变体: %q", part)
	}

	// 标准语法：* / */n / a-b / a-b/n / n
	step := 1
	body := part
	if idx := strings.Index(part, "/"); idx >= 0 {
		body = part[:idx]
		n, err := strconv.Atoi(part[idx+1:])
		if err != nil || n <= 0 {
			return fmt.Errorf("步长非法: %q", part[idx+1:])
		}
		step = n
	}
	if body == "*" {
		if idx := strings.Index(part, "/"); idx < 0 {
			*isStar = true
		}
		for v := min; v <= max; v += step {
			add(v)
		}
		return nil
	}
	bounds := strings.SplitN(body, "-", 2)
	if len(bounds) == 2 {
		a, err1 := strconv.Atoi(bounds[0])
		b, err2 := strconv.Atoi(bounds[1])
		if err1 != nil || err2 != nil || a < min || b > max || a > b {
			return fmt.Errorf("范围 %q 超出取值范围 [%d,%d]", body, min, max)
		}
		for v := a; v <= b; v += step {
			add(v)
		}
		return nil
	}
	v, err := strconv.Atoi(body)
	if err != nil || v < min || v > max {
		return fmt.Errorf("值 %q 超出取值范围 [%d,%d]", body, min, max)
	}
	add(v)
	return nil
}

// hashKey 计算任务名的 FNV-1a 散列（稳定，重启后偏移不变）。
func hashKey(key string) uint32 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(key))
	return h.Sum32()
}

// matches 判断时刻 t 是否命中规格。
func (s *cronSchedule) matches(t time.Time) bool {
	if !s.months[int(t.Month())] || !s.hours[t.Hour()] || !s.minutes[t.Minute()] {
		return false
	}
	domOK := s.doms[t.Day()]
	dowOK := s.dows[int(t.Weekday())]
	// Vixie cron 语义：dom 与 dow 都受限时取并集（任一匹配即触发）。
	if !s.domStar && !s.dowStar {
		return domOK || dowOK
	}
	return domOK && dowOK
}

// NextCron 返回 after 之后下一次命中时刻；一年内无匹配返回 false
// （如 2 月 30 日这类永不存在组合），由调用方记录并跳过。
func NextCron(spec string, key string, after time.Time) (time.Time, error) {
	s, err := ParseCron(spec, key)
	if err != nil {
		return time.Time{}, err
	}
	limit := after.Add(366 * 24 * time.Hour)
	// 逐分钟前进；先按月跳转减少无谓遍历。
	t := after.Truncate(time.Minute).Add(time.Minute)
	for t.Before(limit) {
		if !s.months[int(t.Month())] {
			// 跳到下月 1 日 0:00
			t = time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, t.Location()).AddDate(0, 1, 0)
			continue
		}
		if s.matches(t) {
			return t, nil
		}
		t = t.Add(time.Minute)
	}
	return time.Time{}, fmt.Errorf("cron 规格 %q 在一年内无命中时刻（如 2 月 30 日）", spec)
}

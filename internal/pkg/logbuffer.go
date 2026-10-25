package pkg

import (
	"strings"
	"sync"
	"time"
)

// LogEntry 是「系统日志」页面展示的一条日志。
type LogEntry struct {
	At      time.Time `json:"at"`
	Level   string    `json:"level"`
	Message string    `json:"message"`
}

// LogBuffer 是一个定长环形日志缓冲：实现 io.Writer，可接在标准 log 后面，
// 用于「系统日志」页面回看最近日志而不落盘。
//
// 除了 Lines()（兼容旧用法），还实现 logger.EntrySink，把带级别的条目存下来，
// 让页面可以按级别过滤与着色——没有级别的"一坨文本"在排障时几乎不可用。
type LogBuffer struct {
	mu      sync.Mutex
	max     int
	lines   []string
	entries []LogEntry
	partial []byte
}

// NewLogBuffer 创建最多保留 max 行的日志缓冲。
func NewLogBuffer(max int) *LogBuffer {
	if max <= 0 {
		max = 1000
	}
	return &LogBuffer{max: max, lines: make([]string, 0, max), entries: make([]LogEntry, 0, max)}
}

// Write 按行拆分输入并写入缓冲，返回写入字节数。
func (b *LogBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.partial = append(b.partial, p...)
	for {
		idx := indexByte(b.partial, '\n')
		if idx < 0 {
			break
		}
		line := strings.TrimRight(string(b.partial[:idx]), "\r")
		b.appendLocked(line)
		b.partial = b.partial[idx+1:]
	}
	// 防止长时间没有换行导致 partial 无限增长。
	if len(b.partial) > 64*1024 {
		b.appendLocked(string(b.partial))
		b.partial = b.partial[:0]
	}
	return len(p), nil
}

// Push 实现 logger.EntrySink：记录带级别的条目。
func (b *LogBuffer) Push(at time.Time, level, message string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.entries = append(b.entries, LogEntry{At: at, Level: level, Message: message})
	if len(b.entries) > b.max {
		b.entries = b.entries[len(b.entries)-b.max:]
	}
}

// Entries 返回最近 n 条结构化日志（n<=0 表示全部），按时间正序。
func (b *LogBuffer) Entries(n int) []LogEntry {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]LogEntry, len(b.entries))
	copy(out, b.entries)
	if n > 0 && len(out) > n {
		out = out[len(out)-n:]
	}
	return out
}

// Lines 返回最近 n 行（n<=0 表示全部），按时间正序。
func (b *LogBuffer) Lines(n int) []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]string, len(b.lines))
	copy(out, b.lines)
	if n > 0 && len(out) > n {
		out = out[len(out)-n:]
	}
	return out
}

// Len 返回当前保留的行数。
func (b *LogBuffer) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.lines)
}

// Clear 清空缓冲。
func (b *LogBuffer) Clear() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.lines = b.lines[:0]
	b.partial = b.partial[:0]
}

func (b *LogBuffer) appendLocked(line string) {
	if len(b.lines) >= b.max {
		copy(b.lines, b.lines[1:])
		b.lines = b.lines[:len(b.lines)-1]
	}
	b.lines = append(b.lines, line)
}

func indexByte(data []byte, c byte) int {
	for i, b := range data {
		if b == c {
			return i
		}
	}
	return -1
}

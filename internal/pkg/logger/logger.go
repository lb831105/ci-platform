// Package logger 提供带级别的文件日志：**按天切文件、按月轮换**。
//
// 目录结构：
//
//	data/logs/2026-10/app-2026-10-25.log    ← 当月的每天一个文件
//	data/logs/2026-09/app-2026-09-30.log    ← 上个月（满一个月后整体归档）
//
// 为什么"天文件 + 月目录"：排障是按天定位的（`grep 2026-10-25`），
// 而清理与归档是按月的（否则一年就是 365 个文件堆在一个目录里）。
//
// 为什么不引入 zap/zerolog：本平台定位是零依赖单二进制，日志量级是"单机 CI"，
// 需要的能力只有三项——级别过滤、按月切文件、保留 N 个月。标准库 + 200 行足够，
// 且不引入任何第三方依赖与配置复杂度。
package logger

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Level 是日志级别，数值越大越严重。
type Level int

const (
	LevelDebug Level = iota
	LevelInfo
	LevelWarn
	LevelError
)

// String 返回级别名（定宽 5 字符，便于日志对齐阅读）。
func (l Level) String() string {
	switch l {
	case LevelDebug:
		return "DEBUG"
	case LevelWarn:
		return "WARN "
	case LevelError:
		return "ERROR"
	default:
		return "INFO "
	}
}

// ParseLevel 解析级别名（大小写不敏感）。
func ParseLevel(s string) (Level, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return LevelDebug, nil
	case "", "info":
		return LevelInfo, nil
	case "warn", "warning":
		return LevelWarn, nil
	case "error":
		return LevelError, nil
	}
	return LevelInfo, fmt.Errorf("未知日志级别 %q（可选 debug / info / warn / error）", s)
}

// EntrySink 接收结构化日志条目（「系统日志」页面的环形缓冲实现它）。
// 用接口而不是直接依赖 pkg.LogBuffer，避免 logger 包反向依赖。
type EntrySink interface {
	Push(at time.Time, level string, message string)
}

// Options 是日志初始化参数。
type Options struct {
	// Dir 是日志目录（通常 data/logs）；为空时只输出到 Stdout。
	Dir string
	// Level 是最低输出级别。
	Level Level
	// RetainMonths 是保留的月份数（<=0 时默认 12，<=0 且显式 -1 表示永久保留）。
	RetainMonths int
	// RetainForever 为 true 时不清理历史月份文件。
	RetainForever bool
	// Stdout 是控制台输出目标（默认 os.Stderr，保持与原 log 包一致）。
	Stdout io.Writer
	// Sink 可选：把条目同时推给内存缓冲。
	Sink EntrySink
	// Now 可选：注入时钟（测试用）。
	Now func() time.Time
}

// Logger 是分级日志器。零值不可用，必须用 New。
type Logger struct {
	mu     sync.Mutex
	opt    Options
	file   *os.File
	day    string // 当前文件对应的日期，格式 2006-01-02
	month  string // 当前文件所属月份（目录名），格式 2006-01
	out    io.Writer
	closed bool
}

// defaultLogger 是包级默认实例：未调用 Init 时退化为只写控制台的 INFO 日志，
// 保证任何调用点都不会因为"忘了初始化"而 panic。
var (
	globalMu sync.RWMutex
	global   = &Logger{opt: Options{Level: LevelInfo, Stdout: os.Stderr, Now: time.Now}, out: os.Stderr}
)

// Init 初始化（或替换）全局日志器。
func Init(opt Options) error {
	if opt.Stdout == nil {
		opt.Stdout = os.Stderr
	}
	if opt.Now == nil {
		opt.Now = time.Now
	}
	l := &Logger{opt: opt, out: opt.Stdout}
	if err := l.openLocked(opt.Now()); err != nil {
		return err
	}
	l.dropOldLocked(opt.Now())

	globalMu.Lock()
	old := global
	global = l
	globalMu.Unlock()
	if old != nil {
		_ = old.Close()
	}
	return nil
}

// Close 关闭当前日志文件（进程退出前调用）。
func Close() error {
	globalMu.Lock()
	l := global
	globalMu.Unlock()
	return l.Close()
}

// SetLevel 运行时调整级别（供未来做成可配置项）。
func SetLevel(level Level) {
	globalMu.Lock()
	global.opt.Level = level
	globalMu.Unlock()
}

// LevelOf 返回当前级别。
func LevelOf() Level {
	globalMu.RLock()
	defer globalMu.RUnlock()
	return global.opt.Level
}

// CurrentFile 返回当前正在写入的日志文件路径（空表示未落盘）。
func CurrentFile() string {
	globalMu.RLock()
	l := global
	globalMu.RUnlock()
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return ""
	}
	return l.file.Name()
}

// Debugf / Infof / Warnf / Errorf 是四个级别的写入口。
func Debugf(format string, args ...interface{}) { output(LevelDebug, format, args...) }
func Infof(format string, args ...interface{})  { output(LevelInfo, format, args...) }
func Warnf(format string, args ...interface{})  { output(LevelWarn, format, args...) }
func Errorf(format string, args ...interface{}) { output(LevelError, format, args...) }

// output 按级别过滤后写出：控制台 + 文件 + 内存缓冲三路复用。
func output(level Level, format string, args ...interface{}) {
	globalMu.RLock()
	l := global
	globalMu.RUnlock()
	if l == nil {
		return
	}
	l.write(level, format, args...)
}

func (l *Logger) write(level Level, format string, args ...interface{}) {
	// 低于当前级别直接丢弃：既不落盘也不进缓冲，避免"debug 日志把磁盘写满"。
	if level < l.opt.Level {
		return
	}
	now := l.opt.Now()
	msg := format
	if len(args) > 0 {
		msg = fmt.Sprintf(format, args...)
	}
	// 一行一条：多行消息（如 panic 堆栈）缩进续行，保证按行 grep 可用。
	msg = strings.TrimRight(msg, "\n")
	msg = strings.ReplaceAll(msg, "\n", "\n    ")
	line := fmt.Sprintf("%s %s %s\n", now.Format("2006-01-02 15:04:05.000"), level.String(), msg)

	l.mu.Lock()
	if !l.closed {
		if l.opt.Dir != "" {
			if err := l.ensureFileLocked(now); err != nil {
				// 落盘失败不能让日志调用 panic，也不能静默：降级到控制台并打一行错误。
				_, _ = fmt.Fprintf(l.out, "%s ERROR 写日志文件失败（已降级为仅控制台）: %v\n", now.Format("2006-01-02 15:04:05.000"), err)
				l.opt.Dir = ""
			}
		}
		if l.file != nil {
			_, _ = l.file.WriteString(line)
		}
		_, _ = l.out.Write([]byte(line))
	}
	l.mu.Unlock()

	if l.opt.Sink != nil {
		l.opt.Sink.Push(now, strings.TrimSpace(level.String()), msg)
	}
}

// openLocked 打开指定日期的文件（月目录 + 天文件），调用方需保证已加锁或尚未共享。
func (l *Logger) openLocked(now time.Time) error {
	if l.opt.Dir == "" {
		return nil
	}
	day := now.Format("2006-01-02")
	month := now.Format("2006-01")
	dir := filepath.Join(l.opt.Dir, month)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("创建日志目录 %s: %w", dir, err)
	}
	name := filepath.Join(dir, "app-"+day+".log")
	f, err := os.OpenFile(name, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("打开日志文件 %s: %w", name, err)
	}
	if l.file != nil {
		_ = l.file.Close()
	}
	l.file = f
	l.day = day
	l.month = month
	return nil
}

// ensureFileLocked 检查是否跨天（换文件）或跨月（换目录 = 一个月一轮换）。
func (l *Logger) ensureFileLocked(now time.Time) error {
	day := now.Format("2006-01-02")
	if l.file != nil && l.day == day {
		return nil
	}
	prevMonth := l.month
	if err := l.openLocked(now); err != nil {
		return err
	}
	// 跨月时顺手做过期清理（每月只用扫一次目录，不在每次写入时扫）。
	if prevMonth != l.month {
		l.dropOldLocked(now)
	}
	return nil
}

// dropOldLocked 按月清理：整月目录一起删。
// 按月轮换的前提是有人清理，否则一年后就是 12 个目录堆积（每个都可能很大）。
func (l *Logger) dropOldLocked(now time.Time) {
	if l.opt.Dir == "" || l.opt.RetainForever || l.opt.RetainMonths <= 0 {
		return
	}
	// 保留到 now 往前推 RetainMonths-1 个月（含当月）。
	cutoff := now.AddDate(0, -(l.opt.RetainMonths - 1), 0).Format("2006-01")
	entries, err := os.ReadDir(l.opt.Dir)
	if err != nil {
		return
	}
	var months []string
	for _, e := range entries {
		name := e.Name()
		// 只处理形如 2026-10 的月目录，其它内容（人工放的文件）一律不碰。
		if !e.IsDir() || len(name) != len("2006-01") || name[4] != '-' {
			continue
		}
		if !isMonthName(name) {
			continue
		}
		months = append(months, name)
	}
	sort.Strings(months)
	for _, name := range months {
		// 月份是定长字符串，字典序即时间序。
		if name < cutoff {
			_ = os.RemoveAll(filepath.Join(l.opt.Dir, name))
		}
	}
}

// isMonthName 判断目录名是否是 YYYY-MM（避免误删用户自己建的目录）。
func isMonthName(name string) bool {
	for i, r := range name {
		if i == 4 {
			if r != '-' {
				return false
			}
			continue
		}
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// Close 关闭文件句柄。
func (l *Logger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return nil
	}
	l.closed = true
	if l.file != nil {
		err := l.file.Close()
		l.file = nil
		return err
	}
	return nil
}

package logger

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeSink 记录推给内存缓冲的条目。
type fakeSink struct{ entries []string }

func (f *fakeSink) Push(_ time.Time, level, message string) {
	f.entries = append(f.entries, level+" "+message)
}

// dayPath 返回「月目录 / 天文件」的路径（按天记录、按月轮换）。
func dayPath(dir string, at time.Time) string {
	return filepath.Join(dir, at.Format("2006-01"), "app-"+at.Format("2006-01-02")+".log")
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func TestLevelFiltering(t *testing.T) {
	dir := t.TempDir()
	sink := &fakeSink{}
	if err := Init(Options{Dir: dir, Level: LevelWarn, Stdout: os.Stderr, Sink: sink}); err != nil {
		t.Fatalf("init: %v", err)
	}
	defer func() { _ = Close() }()

	Debugf("debug 不应出现")
	Infof("info 也不应出现")
	Warnf("warn 应出现")
	Errorf("error 应出现")

	content := readFile(t, dayPath(dir, time.Now()))
	if strings.Contains(content, "不应出现") {
		t.Fatalf("below-level logs must be dropped, got:\n%s", content)
	}
	if !strings.Contains(content, "WARN  warn 应出现") || !strings.Contains(content, "ERROR error 应出现") {
		t.Fatalf("expected warn/error lines with level column, got:\n%s", content)
	}
	if len(sink.entries) != 2 {
		t.Fatalf("sink should receive only passing levels, got %v", sink.entries)
	}
}

// 按天切文件、按月换目录：同一天写同一文件，跨天换文件，跨月换目录并清理过期月份。
func TestDailyFilesInMonthlyDirs(t *testing.T) {
	dir := t.TempDir()
	// 预置一个很老的月份目录，验证保留策略会整月删除。
	oldDir := filepath.Join(dir, "2020-01")
	if err := os.MkdirAll(oldDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(oldDir, "app-2020-01-05.log"), []byte("ancient"), 0o644); err != nil {
		t.Fatal(err)
	}
	// 预置一个非月份目录，必须原样保留（不能误删用户自己建的东西）。
	keepDir := filepath.Join(dir, "custom")
	if err := os.MkdirAll(keepDir, 0o755); err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 3, 30, 10, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	if err := Init(Options{
		Dir: dir, Level: LevelInfo, RetainDays: 2, Stdout: os.Stderr, Now: clock,
	}); err != nil {
		t.Fatalf("init: %v", err)
	}
	defer func() { _ = Close() }()

	Infof("三月三十日")
	if _, err := os.Stat(oldDir); !os.IsNotExist(err) {
		t.Fatalf("expired month dir must be removed, err=%v", err)
	}
	if _, err := os.Stat(keepDir); err != nil {
		t.Fatalf("non-month dir must be kept: %v", err)
	}
	march30 := dayPath(dir, now)
	if !strings.Contains(readFile(t, march30), "三月三十日") {
		t.Fatal("same-day log must append to one file")
	}

	// 同一天再写：仍是同一个文件（不按大小/条数切）。
	Infof("三月三十日第二条")
	content := readFile(t, march30)
	if strings.Count(content, "\n") != 2 {
		t.Fatalf("same day must reuse the file, got %s", content)
	}

	// 跨天：换文件，同目录。
	now = time.Date(2026, 3, 31, 9, 0, 0, 0, time.UTC)
	Infof("三月三十一日")
	if !strings.Contains(readFile(t, dayPath(dir, now)), "三月三十一日") {
		t.Fatal("new day must open a new file")
	}
	if !strings.Contains(readFile(t, march30), "三月三十日第二条") {
		t.Fatal("previous day file must be kept")
	}

	// 跨月：换目录（一个月一轮换），上月目录保留。
	now = time.Date(2026, 4, 1, 0, 5, 0, 0, time.UTC)
	Infof("四月一日")
	if !strings.Contains(readFile(t, dayPath(dir, now)), "四月一日") {
		t.Fatal("new month must open a new directory")
	}
	for _, month := range []string{"2026-03", "2026-04"} {
		if _, err := os.Stat(filepath.Join(dir, month)); err != nil {
			t.Fatalf("%s must be kept: %v", month, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "2026-03", "app-2026-03-31.log")); err != nil {
		t.Fatalf("march files must be kept after rotation: %v", err)
	}
}

// 保留 N 天：过期天文件被删、范围内保留；空月份目录一并清理；
// 用户自己的目录不能碰。
func TestRetentionByDays(t *testing.T) {
	dir := t.TempDir()
	// 预置历史文件：10-20 到 10-25（当天是 10-25），保留 3 天 → 只留 23/24/25
	for _, day := range []string{"20", "21", "22", "23", "24"} {
		monthDir := filepath.Join(dir, "2026-10")
		if err := os.MkdirAll(monthDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(monthDir, "app-2026-10-"+day+".log"), []byte("old\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// 整月过期目录（应被删空后移除）
	if err := os.MkdirAll(filepath.Join(dir, "2026-08"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "2026-08", "app-2026-08-01.log"), []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// 用户自己建的目录：不碰
	if err := os.MkdirAll(filepath.Join(dir, "custom"), 0o755); err != nil {
		t.Fatal(err)
	}
	// 旧版本写在根目录的月文件：过期要清掉
	if err := os.WriteFile(filepath.Join(dir, "app-2026-01.log"), []byte("legacy\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 10, 25, 9, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	if err := Init(Options{Dir: dir, Level: LevelInfo, RetainDays: 3, Stdout: os.Stderr, Now: clock}); err != nil {
		t.Fatalf("init: %v", err)
	}
	defer func() { _ = Close() }()

	keep := []string{"2026-10-23", "2026-10-24", "2026-10-25"}
	for _, day := range keep {
		if _, err := os.Stat(filepath.Join(dir, "2026-10", "app-"+day+".log")); err != nil {
			t.Fatalf("%s must be kept: %v", day, err)
		}
	}
	for _, day := range []string{"20", "21", "22"} {
		if _, err := os.Stat(filepath.Join(dir, "2026-10", "app-2026-10-"+day+".log")); !os.IsNotExist(err) {
			t.Fatalf("app-2026-10-%s must be removed, err=%v", day, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "2026-08")); !os.IsNotExist(err) {
		t.Fatalf("empty month dir must be removed, err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "custom")); err != nil {
		t.Fatalf("non-month dir must be kept: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "app-2026-01.log")); !os.IsNotExist(err) {
		t.Fatalf("legacy month file must be removed, err=%v", err)
	}
}

// 默认保留 30 天：第 29 天前的文件被清，30 天内的保留。
func TestRetentionDefaultsTo30Days(t *testing.T) {
	dir := t.TempDir()
	monthDir := filepath.Join(dir, "2026-09")
	if err := os.MkdirAll(monthDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// 35 天前（超期）与 27 天前（保留）
	for _, name := range []string{"app-2026-09-20.log", "app-2026-09-28.log"} {
		if err := os.WriteFile(filepath.Join(monthDir, name), []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Date(2026, 10, 25, 9, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	if err := Init(Options{Dir: dir, Level: LevelInfo, Stdout: os.Stderr, Now: clock}); err != nil {
		t.Fatalf("init: %v", err)
	}
	defer func() { _ = Close() }()
	if _, err := os.Stat(filepath.Join(monthDir, "app-2026-09-20.log")); !os.IsNotExist(err) {
		t.Fatalf("35-day-old file must be removed, err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(monthDir, "app-2026-09-28.log")); err != nil {
		t.Fatalf("27-day-old file must be kept: %v", err)
	}
}

// 多行消息（panic 堆栈）必须缩进续行，保证一行一条、可按行 grep。
func TestMultilineIndented(t *testing.T) {
	dir := t.TempDir()
	if err := Init(Options{Dir: dir, Level: LevelInfo, Stdout: os.Stderr}); err != nil {
		t.Fatalf("init: %v", err)
	}
	defer func() { _ = Close() }()
	Errorf("第一行\n第二行")
	content := readFile(t, dayPath(dir, time.Now()))
	if !strings.Contains(content, "第一行\n    第二行") {
		t.Fatalf("continuation lines must be indented, got:\n%q", content)
	}
}

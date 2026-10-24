package store

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	"ci-platform/internal/model"
	"ci-platform/internal/pkg"
)

const (
	buildFileName = "build.json"
	logFileName   = "log.txt"
)

// BuildStore 负责 data/jobs/{pid}/builds/{bid}/ 下 build.json 与 log.txt 的读写。
// 只做文件读写，不含状态流转校验。
type BuildStore struct {
	jobsRoot string
	mu       sync.Mutex // 保护自增编号分配与并发建目录
}

// NewBuildStore 创建构建记录文件存储，jobsRoot 为 data/jobs 目录。
func NewBuildStore(jobsRoot string) *BuildStore {
	return &BuildStore{jobsRoot: jobsRoot}
}

// buildsDir 返回某个 pipeline 的 builds 目录。
func (s *BuildStore) buildsDir(pipelineID string) string {
	return filepath.Join(s.jobsRoot, pipelineID, "builds")
}

// buildDir 返回某次构建的目录。
func (s *BuildStore) buildDir(pipelineID, buildID string) string {
	return filepath.Join(s.buildsDir(pipelineID), buildID)
}

// nextBuildFile 记录「下一个构建编号」（data/jobs/{pid}/next_build）。
//
// 为什么需要它：编号原本由「扫描构建目录取最大 +1」得出，一旦用户删除最新的
// 构建（或保留策略删掉目录），下一次构建会复用同一个编号，导致同一任务出现
// 两个内容不同的 #5，历史/产物/通知对不上账。Jenkins 用 nextBuildNumber 文件
// 保证单调递增，这里保持一致。
func (s *BuildStore) nextBuildFile(pipelineID string) string {
	return filepath.Join(s.jobsRoot, pipelineID, "next_build")
}

// BuildDir 返回某次构建目录（供产物归档与读取使用）。
func (s *BuildStore) BuildDir(pipelineID, buildID string) string {
	return s.buildDir(pipelineID, buildID)
}

// checkKeys 统一校验 pid/bid，拒绝 "." / ".." / 路径分隔符等，
// 防止构建接口把读写路径逃逸出 data/jobs。
func (s *BuildStore) checkKeys(pipelineID, buildID string) error {
	if err := sanitizeID(pipelineID); err != nil {
		return err
	}
	if buildID != "" {
		if err := sanitizeID(buildID); err != nil {
			return err
		}
	}
	return nil
}

// LogPath 返回日志文件绝对路径。
func (s *BuildStore) LogPath(pipelineID, buildID string) string {
	return filepath.Join(s.buildDir(pipelineID, buildID), logFileName)
}

// NextBuildID 扫描 builds/ 下最大数字编号并 +1，对应 Jenkins 的构建编号。
func (s *BuildStore) NextBuildID(ctx context.Context, pipelineID string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := s.checkKeys(pipelineID, ""); err != nil {
		return "", err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.nextBuildIDLocked(pipelineID)
}

// CreateNext 在锁内完成「分配自增编号 + 创建构建目录」，
// 避免并发触发时两次构建拿到同一个编号（编号即目录名，冲突会互相覆盖）。
func (s *BuildStore) CreateNext(ctx context.Context, b *model.Build) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.checkKeys(b.PipelineID, ""); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	id, err := s.nextBuildIDLocked(b.PipelineID)
	if err != nil {
		return err
	}
	b.ID = id
	if err = pkg.EnsureDir(s.buildDir(b.PipelineID, b.ID)); err != nil {
		return err
	}
	return pkg.WriteJSONAtomic(filepath.Join(s.buildDir(b.PipelineID, b.ID), buildFileName), b)
}

// nextBuildIDLocked 需在持有 s.mu 的前提下调用。
func (s *BuildStore) nextBuildIDLocked(pipelineID string) (string, error) {
	max := 0
	if n, ok := s.readNextBuildMark(pipelineID); ok && n > max {
		max = n - 1 // 标记存的是「下一个编号」，这里换算成已用最大值
	}
	dir := s.buildsDir(pipelineID)
	if pkg.IsDir(dir) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return "", fmt.Errorf("read builds dir %s: %w", dir, err)
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			if n, err := strconv.Atoi(e.Name()); err == nil && n > max {
				max = n
			}
		}
	}
	next := max + 1
	// 分配即落盘（标记存「下一个编号」= next+1）：
	// 即使随后最新构建目录被删除，编号也不会回退复用。
	_ = s.writeNextBuildMark(pipelineID, strconv.Itoa(next+1))
	return strconv.Itoa(next), nil
}

// readNextBuildMark 读取编号标记；不存在或损坏时返回 false。
func (s *BuildStore) readNextBuildMark(pipelineID string) (int, bool) {
	raw, err := os.ReadFile(s.nextBuildFile(pipelineID))
	if err != nil {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

// writeNextBuildMark 写入「下一个编号」；失败不影响构建（编号仍可由目录推导）。
func (s *BuildStore) writeNextBuildMark(pipelineID, next string) error {
	n, err := strconv.Atoi(next)
	if err != nil || n <= 0 {
		return nil
	}
	if err := pkg.EnsureDir(filepath.Join(s.jobsRoot, pipelineID)); err != nil {
		return err
	}
	return os.WriteFile(s.nextBuildFile(pipelineID), []byte(strconv.Itoa(n)+"\n"), 0o644)
}

// Create 创建构建目录并写入 build.json。
func (s *BuildStore) Create(ctx context.Context, b *model.Build) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.checkKeys(b.PipelineID, b.ID); err != nil {
		return err
	}
	if err := pkg.EnsureDir(s.buildDir(b.PipelineID, b.ID)); err != nil {
		return err
	}
	return pkg.WriteJSONAtomic(filepath.Join(s.buildDir(b.PipelineID, b.ID), buildFileName), b)
}

// Get 读取构建元数据。
func (s *BuildStore) Get(ctx context.Context, pipelineID, buildID string) (*model.Build, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := s.checkKeys(pipelineID, buildID); err != nil {
		return nil, err
	}
	path := filepath.Join(s.buildDir(pipelineID, buildID), buildFileName)
	if !pkg.Exists(path) {
		return nil, pkg.ErrNotFound("build %s/%s not found", pipelineID, buildID)
	}
	var b model.Build
	if err := pkg.ReadJSON(path, &b); err != nil {
		return nil, err
	}
	return &b, nil
}

// Update 覆盖写入构建元数据。
func (s *BuildStore) Update(ctx context.Context, b *model.Build) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.checkKeys(b.PipelineID, b.ID); err != nil {
		return err
	}
	path := filepath.Join(s.buildDir(b.PipelineID, b.ID), buildFileName)
	if !pkg.Exists(path) {
		return pkg.ErrNotFound("build %s/%s not found", b.PipelineID, b.ID)
	}
	return pkg.WriteJSONAtomic(path, b)
}

// ListByPipeline 遍历 builds 目录，按构建编号倒序返回。
func (s *BuildStore) ListByPipeline(ctx context.Context, pipelineID string) ([]*model.Build, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := s.checkKeys(pipelineID, ""); err != nil {
		return nil, err
	}
	dir := s.buildsDir(pipelineID)
	if !pkg.IsDir(dir) {
		return []*model.Build{}, nil
	}
	names, err := pkg.ListDirs(dir)
	if err != nil {
		return nil, err
	}
	out := make([]*model.Build, 0, len(names))
	for _, name := range names {
		var b model.Build
		if err := pkg.ReadJSON(filepath.Join(dir, name, buildFileName), &b); err != nil {
			continue
		}
		out = append(out, &b)
	}
	sort.Slice(out, func(i, j int) bool { return numericGT(out[i].ID, out[j].ID) })
	return out, nil
}

// LogWriter 以追加模式打开日志文件，返回可流式写入并关闭的 writer。
func (s *BuildStore) LogWriter(ctx context.Context, pipelineID, buildID string) (io.WriteCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := s.checkKeys(pipelineID, buildID); err != nil {
		return nil, err
	}
	path := s.LogPath(pipelineID, buildID)
	if err := pkg.EnsureDir(filepath.Dir(path)); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open log %s: %w", path, err)
	}
	return f, nil
}

// AppendLog 追加一段日志内容。
func (s *BuildStore) AppendLog(ctx context.Context, pipelineID, buildID string, data []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.checkKeys(pipelineID, buildID); err != nil {
		return err
	}
	path := s.LogPath(pipelineID, buildID)
	if err := pkg.EnsureDir(filepath.Dir(path)); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open log %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	if _, err = f.Write(data); err != nil {
		return fmt.Errorf("append log %s: %w", path, err)
	}
	return nil
}

// ReadLog 读取完整日志文本，文件不存在时返回空串。
func (s *BuildStore) ReadLog(ctx context.Context, pipelineID, buildID string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := s.checkKeys(pipelineID, buildID); err != nil {
		return "", err
	}
	data, err := os.ReadFile(s.LogPath(pipelineID, buildID))
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", fmt.Errorf("read log: %w", err)
	}
	return string(data), nil
}

// DeleteByPipeline 删除某个 pipeline 的全部构建目录。
func (s *BuildStore) DeleteByPipeline(ctx context.Context, pipelineID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.checkKeys(pipelineID, ""); err != nil {
		return err
	}
	if !pkg.Exists(s.buildsDir(pipelineID)) {
		return nil
	}
	return pkg.RemoveAll(s.buildsDir(pipelineID))
}

// DeleteBuild 删除单次构建目录（用于构建保留策略）。
func (s *BuildStore) DeleteBuild(ctx context.Context, pipelineID, buildID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := s.checkKeys(pipelineID, buildID); err != nil {
		return err
	}
	dir := s.buildDir(pipelineID, buildID)
	if !pkg.Exists(dir) {
		return nil
	}
	return pkg.RemoveAll(dir)
}

// numericGT 比较两个数字字符串的大小，非数字按字典序兜底。
func numericGT(a, b string) bool {
	ai, aerr := strconv.Atoi(a)
	bi, berr := strconv.Atoi(b)
	if aerr == nil && berr == nil {
		return ai > bi
	}
	return a > b
}

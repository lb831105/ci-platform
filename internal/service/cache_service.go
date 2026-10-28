package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"ci-platform/internal/executor"
	"ci-platform/internal/model"
	"ci-platform/internal/pkg"
)

const (
	cacheMaxFiles = 100000
	cacheMaxBytes = int64(8 << 30) // 8GB：防止误把整个依赖盘写进 data 目录
)

// restoreCache 在构建步骤前把缓存复制回工作区。缓存损坏/不存在只告警，
// 不能让一个可选优化阻断构建（下次成功构建会重新生成）。
func (s *BuildService) restoreCache(ctx context.Context, p *model.Pipeline, b *model.Build, workspace string, out io.Writer) error {
	if p.Cache == nil || !p.Cache.Enabled {
		return nil
	}
	dir := s.builds.CacheDir(p.ID, cacheStorageKey(p, b))
	if !pkg.IsDir(dir) {
		fmt.Fprintf(out, "[cache] miss (%s)\n", cacheStorageKey(p, b))
		return nil
	}
	files := 0
	var total int64
	err := filepath.WalkDir(dir, func(src string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		// 缓存目录只应包含普通文件；符号链接可能指向数据目录外，直接跳过。
		if !d.Type().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(dir, src)
		if err != nil {
			return err
		}
		dst := filepath.Join(workspace, rel)
		if !pkg.WithinDir(workspace, dst) {
			return fmt.Errorf("缓存路径越界: %s", rel)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		total += info.Size()
		if total > cacheMaxBytes {
			return fmt.Errorf("缓存超过 %d GB 上限", cacheMaxBytes>>30)
		}
		if err = pkg.EnsureDir(filepath.Dir(dst)); err != nil {
			return err
		}
		if err = copyCacheFile(src, dst, info.Mode().Perm()); err != nil {
			return err
		}
		files++
		return nil
	})
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "[cache] restored %d file(s) from %s\n", files, cacheStorageKey(p, b))
	return nil
}

// saveCache 在构建成功后保存缓存。这里用“先清空再写”避免旧文件残留
// （例如依赖升级后旧模块仍被下一次构建读到）。
func (s *BuildService) saveCache(ctx context.Context, p *model.Pipeline, b *model.Build, workspace string, out io.Writer) error {
	if p.Cache == nil || !p.Cache.Enabled {
		return nil
	}
	files, notes, err := executor.MatchWorkspaceFiles(workspace, p.Cache.Paths, "cache")
	if err != nil {
		return err
	}
	for _, note := range notes {
		fmt.Fprintf(out, "[cache] %s\n", note)
	}
	dir := s.builds.CacheDir(p.ID, cacheStorageKey(p, b))
	// 先删后建：缓存目录是平台自己的，不包含用户其它数据。
	if err = pkg.RemoveAll(dir); err != nil {
		return err
	}
	if err = pkg.EnsureDir(dir); err != nil {
		return err
	}
	var total int64
	written := 0
	for _, src := range files {
		if err := ctx.Err(); err != nil {
			return err
		}
		if written >= cacheMaxFiles {
			_ = pkg.RemoveAll(dir)
			return fmt.Errorf("缓存文件数超过 %d 上限，已跳过本次缓存", cacheMaxFiles)
		}
		info, err := os.Lstat(src)
		if err != nil || !info.Mode().IsRegular() {
			continue // 跳过符号链接与已消失的文件
		}
		total += info.Size()
		if total > cacheMaxBytes {
			_ = pkg.RemoveAll(dir)
			return fmt.Errorf("缓存超过 %d GB 上限，已跳过本次缓存", cacheMaxBytes>>30)
		}
		rel, err := filepath.Rel(workspace, src)
		if err != nil || strings.HasPrefix(rel, "..") {
			continue
		}
		dst := filepath.Join(dir, rel)
		if !pkg.WithinDir(dir, dst) {
			continue
		}
		if err = pkg.EnsureDir(filepath.Dir(dst)); err != nil {
			return err
		}
		if err = copyCacheFile(src, dst, info.Mode().Perm()); err != nil {
			return err
		}
		written++
	}
	if written == 0 {
		_ = pkg.RemoveAll(dir)
		return nil
	}
	fmt.Fprintf(out, "[cache] saved %d file(s) to %s\n", written, cacheStorageKey(p, b))
	return nil
}

func copyCacheFile(src, dst string, mode fs.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err = io.Copy(out, in); err != nil {
		_ = out.Close()
		_ = os.Remove(dst)
		return err
	}
	return out.Close()
}

// cacheStorageKey 把用户 key（可含 ${PARAM}）展开后哈希成安全目录名。
// 不直接使用用户输入做路径，避免 key 里的 ../ 或非法字符逃逸 jobs 目录。
func cacheStorageKey(p *model.Pipeline, b *model.Build) string {
	key := p.Cache.Key
	if strings.TrimSpace(key) == "" {
		key = "default"
	}
	if b != nil {
		for name, value := range b.Params {
			key = strings.ReplaceAll(key, "${"+name+"}", value)
		}
	}
	key = strings.ReplaceAll(key, "${CI_PIPELINE_NAME}", p.Name)
	if b != nil {
		key = strings.ReplaceAll(key, "${CI_BUILD_ID}", b.ID)
	}
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:16])
}

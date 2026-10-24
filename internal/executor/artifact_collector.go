package executor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"

	"ci-platform/internal/model"
	"ci-platform/internal/pkg"
)

// ArtifactLimits 限制单次构建归档的规模，防止磁盘被异常产物写满。
// 零值表示不限制。
type ArtifactLimits struct {
	MaxFiles      int
	MaxTotalBytes int64
}

// ArtifactCollector 把构建产物从工作区复制到 build 目录，供每次构建独立保留。
// 只做本地文件复制，不连接远程服务器；SSH 发布仍由 SshPublisher 负责。
type ArtifactCollector struct{}

// NewArtifactCollector 创建产物收集器。
func NewArtifactCollector() *ArtifactCollector { return &ArtifactCollector{} }

// Collect 按 patterns 收集 workspace 中的文件，复制到 buildDir/artifacts/，
// 并写入 buildDir/artifacts.json 清单。
// 至少匹配一个文件，否则返回错误，避免“构建成功但没有任何可回滚产物”。
func (c *ArtifactCollector) Collect(
	ctx context.Context,
	workspace string,
	buildDir string,
	patterns []string,
	limits ArtifactLimits,
	out io.Writer,
) ([]model.ArtifactFile, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if len(patterns) == 0 {
		return nil, errors.New("artifact: files is empty")
	}
	files, err := matchWorkspaceFiles(workspace, patterns, "artifact")
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("artifact: no files matched %v under the workspace", patterns)
	}
	if limits.MaxFiles > 0 && len(files) > limits.MaxFiles {
		return nil, fmt.Errorf("artifact: matched %d files, exceeding artifact_max_files (%d); "+
			"raise it in 系统配置 → 全局构建丢弃器, or narrow the archive patterns", len(files), limits.MaxFiles)
	}

	dest := filepath.Join(buildDir, model.ArtifactDirName)
	if err = os.RemoveAll(dest); err != nil {
		return nil, fmt.Errorf("artifact: clean %s: %w", dest, err)
	}
	if err = pkg.EnsureDir(dest); err != nil {
		return nil, err
	}

	fmt.Fprintln(out)
	fmt.Fprintf(out, "[artifact] archiving %d file(s) to %s/", len(files), model.ArtifactDirName)
	fmt.Fprintln(out)

	result := make([]model.ArtifactFile, 0, len(files))
	var total int64
	for _, local := range files {
		if err = ctx.Err(); err != nil {
			_ = os.RemoveAll(dest)
			return nil, err
		}
		rel := relativeTo(workspace, local)
		if rel == "" || rel == "." {
			continue
		}
		target := filepath.Join(dest, filepath.FromSlash(rel))
		if !pkg.WithinDir(dest, target) {
			_ = os.RemoveAll(dest)
			return nil, fmt.Errorf("artifact: %q escapes artifact directory", rel)
		}

		info, statErr := os.Lstat(local)
		if statErr != nil {
			_ = os.RemoveAll(dest)
			return nil, fmt.Errorf("artifact: stat %s: %w", rel, statErr)
		}
		// 不跟随符号链接：避免把工作区外的服务器文件复制进产物目录。
		if info.Mode()&os.ModeSymlink != 0 {
			fmt.Fprintf(out, "[artifact] WARN: skip symlink %s", rel)
			fmt.Fprintln(out)
			continue
		}
		if !info.Mode().IsRegular() {
			continue
		}
		if limits.MaxTotalBytes > 0 && total+info.Size() > limits.MaxTotalBytes {
			_ = os.RemoveAll(dest)
			return nil, fmt.Errorf("artifact: total size of matched files exceeds artifact_max_total_mb (%s); "+
				"raise it in 系统配置 → 全局构建丢弃器, or narrow the archive patterns", artifactBytes(limits.MaxTotalBytes))
		}

		if err = pkg.EnsureDir(filepath.Dir(target)); err != nil {
			_ = os.RemoveAll(dest)
			return nil, err
		}
		size, sum, copyErr := copyArtifactFile(ctx, local, target, info.Mode().Perm())
		if copyErr != nil {
			_ = os.RemoveAll(dest)
			return nil, fmt.Errorf("artifact: copy %s: %w", rel, copyErr)
		}
		result = append(result, model.ArtifactFile{Path: rel, Size: size, SHA256: sum})
		total += size
		// 文件可能在复制期间增长，复制后再校验一次实际大小。
		if limits.MaxTotalBytes > 0 && total > limits.MaxTotalBytes {
			_ = os.RemoveAll(dest)
			return nil, fmt.Errorf("artifact: total size exceeds the limit of %s", artifactBytes(limits.MaxTotalBytes))
		}
		if len(result) <= 50 {
			fmt.Fprintf(out, "[artifact] %s (%s)", rel, artifactBytes(size))
			fmt.Fprintln(out)
		}
	}
	if len(result) == 0 {
		_ = os.RemoveAll(dest)
		return nil, fmt.Errorf("artifact: no regular files matched %v", patterns)
	}
	if len(result) > 50 {
		fmt.Fprintf(out, "[artifact] ... %d more file(s)", len(result)-50)
		fmt.Fprintln(out)
	}

	sort.Slice(result, func(i, j int) bool { return result[i].Path < result[j].Path })
	manifest := model.ArtifactManifest{
		CreatedAt: time.Now().UTC(),
		TotalSize: total,
		Files:     result,
	}
	if err = pkg.WriteJSONAtomic(filepath.Join(buildDir, model.ArtifactManifestName), manifest); err != nil {
		_ = os.RemoveAll(dest)
		return nil, fmt.Errorf("artifact: write manifest: %w", err)
	}
	fmt.Fprintf(out, "[artifact] archived %d file(s), %s", len(result), artifactBytes(total))
	fmt.Fprintln(out)
	return result, nil
}

// copyArtifactFile 复制单个文件并计算 SHA-256；成功后保留可执行位。
func copyArtifactFile(ctx context.Context, src, dst string, mode os.FileMode) (int64, string, error) {
	in, err := os.Open(src)
	if err != nil {
		return 0, "", err
	}
	defer func() { _ = in.Close() }()

	perm := mode.Perm()
	if perm == 0 {
		perm = 0o644
	}
	file, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return 0, "", err
	}
	hash := sha256.New()
	n, err := io.Copy(io.MultiWriter(file, hash), contextReader{ctx: ctx, r: in})
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(dst)
		return 0, "", err
	}
	_ = os.Chmod(dst, perm)
	return n, hex.EncodeToString(hash.Sum(nil)), nil
}

// contextReader 让大文件复制也能响应构建取消/超时。
type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}

// artifactBytes 把字节数格式化为可读大小，仅用于构建日志。
func artifactBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	value := float64(n)
	units := []string{"KB", "MB", "GB", "TB"}
	for _, name := range units {
		value /= unit
		if value < unit {
			return fmt.Sprintf("%.1f %s", value, name)
		}
	}
	return fmt.Sprintf("%.1f PB", value/unit)
}

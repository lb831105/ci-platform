package executor

import (
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"ci-platform/internal/pkg"
)

// matchWorkspaceFiles 按 glob 收集工作区内文件，结果按路径排序。
// 约定：
//   - "dist/**"          递归匹配 dist 下所有文件
//   - "**/*.jar"         匹配任意层级的 jar
//   - "dist" / "dist/"   目录模式，等价于 dist/**
//   - "app.tar.gz"       精确文件名
//
// label 只用于错误前缀（ssh publish / artifact），不影响匹配语义。
func matchWorkspaceFiles(workspace string, patterns []string, label string) ([]string, error) {
	if len(patterns) == 0 {
		return nil, fmt.Errorf("%s: no patterns", label)
	}
	seen := make(map[string]bool)
	var out []string

	for _, raw := range patterns {
		pattern, err := normalizeWorkspacePattern(raw)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", label, err)
		}
		if pattern == "" {
			continue
		}

		// 目录模式：直接收集目录下全部文件。
		dirCandidate := filepath.Join(workspace, filepath.FromSlash(pattern))
		if !pkg.WithinDir(workspace, dirCandidate) {
			return nil, fmt.Errorf("%s: pattern %q escapes workspace", label, raw)
		}
		if pkgIsDir(dirCandidate) && !strings.ContainsAny(pattern, "*?[") {
			if err = collectDir(dirCandidate, seen, &out); err != nil {
				return nil, err
			}
			continue
		}

		// 精确文件：无需遍历整个工作区（大仓库/依赖目录下可显著提速）。
		if !strings.ContainsAny(pattern, "*?[") {
			if info, statErr := os.Stat(dirCandidate); statErr == nil && info.Mode().IsRegular() && !seen[dirCandidate] {
				seen[dirCandidate] = true
				out = append(out, dirCandidate)
				continue
			}
		}

		re, err := globToRegexp(pattern)
		if err != nil {
			return nil, fmt.Errorf("%s: invalid pattern %q: %w", label, raw, err)
		}
		err = filepath.WalkDir(workspace, func(p string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil || d.IsDir() {
				return nil
			}
			rel, relErr := filepath.Rel(workspace, p)
			if relErr != nil {
				return nil
			}
			if re.MatchString(filepath.ToSlash(rel)) && !seen[p] {
				seen[p] = true
				out = append(out, p)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}

	sort.Strings(out)
	return out, nil
}

// normalizeWorkspacePattern 归一化并校验单个 glob：
// 只允许工作区内的相对路径，拒绝绝对路径与 ../ 越界，
// 防止具备任务编辑权限的人读取服务器任意本地文件。
func normalizeWorkspacePattern(raw string) (string, error) {
	pattern := strings.TrimSpace(filepath.ToSlash(raw))
	if pattern == "" {
		return "", nil
	}
	pattern = path.Clean(strings.TrimPrefix(pattern, "./"))
	if pattern == "." || pattern == ".." || strings.HasPrefix(pattern, "../") ||
		strings.HasPrefix(pattern, "/") || filepath.IsAbs(filepath.FromSlash(pattern)) {
		return "", fmt.Errorf("invalid pattern %q (must be a path inside the workspace)", raw)
	}
	if _, err := globToRegexp(pattern); err != nil {
		return "", fmt.Errorf("invalid pattern %q: %w", raw, err)
	}
	return pattern, nil
}

// ValidateWorkspacePatterns 供 service 在保存任务时提前校验产物/上传模式。
func ValidateWorkspacePatterns(patterns []string) error {
	for _, raw := range patterns {
		if _, err := normalizeWorkspacePattern(raw); err != nil {
			return err
		}
	}
	return nil
}

// globToRegexp 把 glob 转成正则：** 跨目录，* / ? 不跨目录。
func globToRegexp(pattern string) (*regexp.Regexp, error) {
	var b strings.Builder
	b.WriteString("^")
	for i := 0; i < len(pattern); i++ {
		c := pattern[i]
		switch c {
		case '*':
			if i+1 < len(pattern) && pattern[i+1] == '*' {
				i++
				if i+1 < len(pattern) && pattern[i+1] == '/' {
					i++
					b.WriteString("(?:.*/)?")
				} else {
					b.WriteString(".*")
				}
			} else {
				b.WriteString("[^/]*")
			}
		case '?':
			b.WriteString("[^/]")
		case '.', '+', '(', ')', '|', '^', '$', '{', '}', '[', ']', '\\':
			b.WriteByte('\\')
			b.WriteByte(c)
		default:
			b.WriteByte(c)
		}
	}
	b.WriteString("$")
	return regexp.Compile(b.String())
}

// collectDir 递归收集目录下全部文件。
func collectDir(dir string, seen map[string]bool, out *[]string) error {
	return filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if !seen[p] {
			seen[p] = true
			*out = append(*out, p)
		}
		return nil
	})
}

// pkgIsDir 判断路径是否为目录（本地小工具，避免过度暴露 pkg 依赖）。
func pkgIsDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

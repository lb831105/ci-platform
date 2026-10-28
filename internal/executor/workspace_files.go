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

// matchWorkspaceFilesDetailed 按 glob 收集工作区内文件，结果按路径排序。
// 约定：
//   - "dist/**"          递归匹配 dist 下所有文件
//   - "**/*.jar"         匹配任意层级的 jar
//   - "dist" / "dist/"   目录模式，等价于 dist/**
//   - "app.tar.gz"       精确文件名
//   - "target/*.jar"     先按根目录匹配；根目录没有时**自动按 **/target/*.jar 再试一次**
//
// 最后一条是刻意的"宽容"：多模块 Maven 工程（ruoyi）的 jar 在
// ruoyi-admin/target/ 下，用户写 target/*.jar 的意图显然不是"只看根目录"，
// 让他因为目录层级而构建失败、还得自己发现 `**` 语法，属于产品缺陷而不是配置错误。
// 根目录有匹配时仍然优先用根目录结果（保持 Jenkins 语义可预期）。
//
// label 只用于错误前缀（ssh publish / artifact），不影响匹配语义。
// 返回值 notes 是"自动回退"的说明，调用方需要打进构建日志，
// 否则用户会疑惑"我写的 target/*.jar 怎么归档了子目录的"。
func matchWorkspaceFilesDetailed(workspace string, patterns []string, label string) ([]string, []string, error) {
	if len(patterns) == 0 {
		return nil, nil, fmt.Errorf("%s: no patterns", label)
	}
	seen := make(map[string]bool)
	var out []string
	var notes []string

	for _, raw := range patterns {
		pattern, err := normalizeWorkspacePattern(raw)
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %w", label, err)
		}
		if pattern == "" {
			continue
		}

		// 目录模式：直接收集目录下全部文件。
		dirCandidate := filepath.Join(workspace, filepath.FromSlash(pattern))
		if !pkg.WithinDir(workspace, dirCandidate) {
			return nil, nil, fmt.Errorf("%s: pattern %q escapes workspace", label, raw)
		}
		if pkgIsDir(dirCandidate) && !strings.ContainsAny(pattern, "*?[") {
			if err = collectDir(dirCandidate, seen, &out); err != nil {
				return nil, nil, err
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

		matched, matchedPattern, err := scanWorkspaceForPattern(workspace, pattern, false)
		if err != nil {
			return nil, nil, fmt.Errorf("%s: invalid pattern %q: %w", label, raw, err)
		}
		if len(matched) == 0 {
			// 根目录没匹配到：容易踩的就是多模块工程的嵌套目录，自动按递归再试一次。
			if recursive := suggestPattern(pattern); recursive != "" {
				// 兜底扫描跳过依赖缓存目录：快，且不会误抓 node_modules 里的同名文件。
				recursiveMatched, _, scanErr := scanWorkspaceForPattern(workspace, recursive, true)
				if scanErr != nil {
					return nil, nil, fmt.Errorf("%s: invalid pattern %q: %w", label, raw, scanErr)
				}
				if len(recursiveMatched) > 0 {
					matched = recursiveMatched
					matchedPattern = recursive
				}
			}
		}
		if matchedPattern != "" && matchedPattern != pattern {
			notes = append(notes, fmt.Sprintf("模式 %s 在工作区根目录没有匹配，已按递归匹配 %s 处理（命中 %d 个文件）",
				pattern, matchedPattern, len(matched)))
		}
		for _, file := range matched {
			if !seen[file] {
				seen[file] = true
				out = append(out, file)
			}
		}
	}
	sort.Strings(out)
	return out, notes, nil
}

// noiseDirs 是"找提示 / 自动兜底"时跳过的依赖与元数据目录。
//
// 只用于这两类**额外**扫描，显式配置的模式（含 **）仍然遍历全部目录，
// 以免把用户明确写下的匹配语义偷偷改掉。
var noiseDirs = map[string]bool{
	".git": true, "node_modules": true, ".ci-tmp": true,
	".gradle": true, ".mvn": true, ".idea": true,
}

// scanWorkspaceForPattern 遍历工作区，返回匹配的文件与"实际生效的模式"。
//
// skipNoise 仅由自动兜底使用：遍历 node_modules 既慢，又可能把依赖里的
// 同名文件当成产物（比如 node_modules/x/target/app.jar）发到生产。
func scanWorkspaceForPattern(workspace, pattern string, skipNoise bool) ([]string, string, error) {
	var matched []string
	re, err := globToRegexp(pattern)
	if err != nil {
		return nil, "", err
	}
	err = filepath.WalkDir(workspace, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if d.IsDir() {
			if skipNoise && noiseDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		rel, relErr := filepath.Rel(workspace, p)
		if relErr != nil {
			return nil
		}
		if re.MatchString(filepath.ToSlash(rel)) {
			matched = append(matched, p)
		}
		return nil
	})
	if err != nil {
		return nil, "", err
	}
	return matched, pattern, nil
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

// MatchWorkspaceFiles 是 matchWorkspaceFilesDetailed 的导出包装，
// 供缓存等需要在工作区内按 glob 收集文件的服务复用同一套路径安全规则。
func MatchWorkspaceFiles(workspace string, patterns []string, label string) ([]string, []string, error) {
	return matchWorkspaceFilesDetailed(workspace, patterns, label)
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

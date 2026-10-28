package executor

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// 产物/上传模式没匹配到文件时，给"下一步该怎么做"的具体建议。
//
// 典型场景：Maven 多模块项目，jar 在 ruoyi-admin/target/ruoyi-admin.jar，
// 而任务里写的是 target/*.jar（相对工作区根目录）。只回一句 "no files matched"
// 等于让用户自己猜 —— 直接把找到的相似文件和建议的模式打出来，省掉一次试错构建。

// maxHintFiles 限制建议列表长度，避免把构建日志刷满。
const maxHintFiles = 5

// similarFiles 找出与模式"同名但更靠里"的文件，返回相对工作区路径。
//
// 判定只看模式最后一段（如 *.jar），再比较文件名：
// 这样 target/*.jar 能提示 ruoyi-admin/target/ruoyi-admin.jar。
func similarFiles(workspace string, patterns []string) []string {
	if len(patterns) == 0 {
		return nil
	}
	// 每个模式取最后一段作为文件名模式
	var tails []string
	for _, raw := range patterns {
		normalized, err := normalizeWorkspacePattern(raw)
		if err != nil || normalized == "" {
			continue
		}
		parts := strings.Split(normalized, "/")
		tails = append(tails, parts[len(parts)-1])
	}
	if len(tails) == 0 {
		return nil
	}

	var matches []string
	_ = filepath.WalkDir(workspace, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			// 跳过明显无关的大目录：找提示不值得遍历依赖缓存。
			if noiseDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		rel, relErr := filepath.Rel(workspace, p)
		if relErr != nil {
			return nil
		}
		base := d.Name()
		for _, tail := range tails {
			if ok, _ := path.Match(tail, base); ok {
				matches = append(matches, filepath.ToSlash(rel))
				break
			}
		}
		if len(matches) > 200 {
			return filepath.SkipAll
		}
		return nil
	})
	sort.Strings(matches)
	if len(matches) > maxHintFiles {
		matches = matches[:maxHintFiles]
	}
	return matches
}

// suggestPattern 给没匹配到的模式提出"再往外找一层"的写法（**/<原模式>）。
// 只用于两处：
//  1. 匹配器在根目录没命中时自动兜底再扫一次；
//  2. 提示用户"根目录之外还有没有同名文件"。
//
// 注意：它**不能**拿来当"一键修正"的建议 —— 匹配器已经试过了，
// 失败时说明它也匹配不到（见 suggestedPatterns）。
func suggestPattern(pattern string) string {
	normalized, err := normalizeWorkspacePattern(pattern)
	if err != nil || normalized == "" {
		return ""
	}
	if strings.Contains(normalized, "**") {
		return ""
	}
	return "**/" + normalized
}

// suggestedPatterns 从"工作区里找到的文件"反推一个**确实能命中它们**的模式。
//
// 为什么不直接建议 **/<原模式>：匹配器在根目录没命中时已经自动试过它，
// 走到"没匹配到"这里通常说明它也匹配不到（目录名写错时更明显：
// 兜底试的是 **/ruoyi-adm/target/*.jar，有用的是 **/target/*.jar）。
// 所以建议一律由真实文件反推，并用 glob 现场验证。
func suggestedPatterns(files []string) []string {
	if len(files) == 0 {
		return nil
	}
	// 优先给一条通用建议（**/目录名/*扩展名）：多模块工程只改一处就够。
	if common := commonSuffixPattern(files); common != "" {
		return []string{common}
	}
	// 退化成按文件所在目录分别建议，最多两条。
	var out []string
	seen := make(map[string]bool)
	for _, file := range files {
		candidate := patternForFile(file)
		if candidate == "" || seen[candidate] {
			continue
		}
		// 建议必须真的匹配到它来自的那个文件，否则宁可不给建议。
		re, err := globToRegexp(candidate)
		if err != nil || !re.MatchString(file) {
			continue
		}
		seen[candidate] = true
		out = append(out, candidate)
		if len(out) >= 2 {
			break
		}
	}
	return out
}

// commonSuffixPattern 在"所有找到的文件都在同名目录、同扩展名"时给出
// 一条通用模式（如 **/target/*.jar），并验证它确实命中全部文件。
func commonSuffixPattern(files []string) string {
	candidate := ""
	for i, file := range files {
		dir := path.Base(path.Dir(file))
		ext := path.Ext(file)
		if ext == "" || ext == "." || dir == "." || dir == "/" {
			return ""
		}
		want := "**/" + dir + "/*" + ext
		if i == 0 {
			candidate = want
			continue
		}
		if want != candidate {
			return ""
		}
	}
	re, err := globToRegexp(candidate)
	if err != nil {
		return ""
	}
	for _, file := range files {
		if !re.MatchString(file) {
			return ""
		}
	}
	return candidate
}

// patternForFile 把 <目录>/<文件名> 折叠成 <目录>/*<扩展名>，
// 这样一个模块里的多个 jar 能被同一条建议覆盖。
func patternForFile(file string) string {
	dir := path.Dir(file)
	ext := path.Ext(file)
	if ext == "" || ext == "." {
		return file
	}
	if dir == "." || dir == "/" {
		return "*" + ext
	}
	return dir + "/*" + ext
}

// NoMatchError 是"模式没匹配到任何文件"的结构化错误。
//
// 为什么要类型化：service 需要把「当前模式 / 建议模式 / 找到的文件」
// 存进 build.json 给前端做诊断与一键修复，而不是让前端去正则解析日志。
type NoMatchError struct {
	Patterns  []string
	Suggested []string
	// Files 是工作区里与模式"最后一段同名"的文件（最多 maxHintFiles 个）。
	Files []string
	// RecursiveTried 表示匹配器已经自动按 **/<模式> 兜底扫过一次。
	RecursiveTried bool
}

// Error 返回给人看的完整提示（含相似文件与建议模式）。
func (e *NoMatchError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "没有匹配到任何文件：%s", strings.Join(e.Patterns, ", "))
	if e.RecursiveTried {
		b.WriteString("\n已先按根目录匹配、再自动按 **/ 递归匹配，两者都没有命中。")
	}
	if len(e.Files) == 0 {
		b.WriteString("\n工作区里也没有同名文件：请确认构建步骤真的产出了该文件（先看上面的步骤日志），或把模式写对。")
		return b.String()
	}
	b.WriteString("\n工作区里找到同名文件（模式是相对工作区根目录匹配的）：")
	for _, file := range e.Files {
		fmt.Fprintf(&b, "\n  - %s", file)
	}
	if len(e.Suggested) > 0 {
		fmt.Fprintf(&b, "\n如果它们就是想要的产物，把模式改成：%s（* 不跨目录，** 任意层级）",
			strings.Join(e.Suggested, ", "))
	}
	return b.String()
}

// newNoMatchError 构造结构化错误（files/suggestions 由工作区扫描得出）。
func newNoMatchError(workspace string, patterns []string) *NoMatchError {
	err := &NoMatchError{
		Patterns:       patterns,
		Files:          similarFiles(workspace, patterns),
		RecursiveTried: false,
	}
	for _, pattern := range patterns {
		if suggestPattern(pattern) != "" {
			err.RecursiveTried = true
			break
		}
	}
	// 连同名文件都没有时不给"建议模式"：那种情况是步骤没产出，
	// 给个一键改模式的按钮只会让人点了还是失败。
	err.Suggested = suggestedPatterns(err.Files)
	return err
}

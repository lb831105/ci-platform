package executor

import (
	"fmt"
	"sort"
	"strings"

	"github.com/go-git/go-git/v5"
)

// CheckoutInfo 是一次签出后的 SCM 现场信息，供通知/日志使用。
type CheckoutInfo struct {
	Branch     string   // 分支名（去 refs/heads/ 前缀）
	Commit     string   // 短 SHA（前 8 位）
	CommitFull string   // 完整 SHA（构建页 Revision）
	Message    string   // 提交说明（构建页 Changes Summary）
	Files      []string // 本次提交修改的文件（按路径排序）
	Author     string   // 提交人姓名
	Email      string   // 提交人邮箱
}

// maxChangedFiles 限制构建元数据里保存的文件数，避免超大提交把 build.json 撑大。
const maxChangedFiles = 200

// maxCommitMessageRunes 限制提交说明长度：git 元数据可被构造为超长文本，
// 原样写入 build.json 会让构建详情接口和前端渲染都被拖慢。
const maxCommitMessageRunes = 4000
const maxCommitAuthorRunes = 200
const maxCommitEmailRunes = 320

// ReadCheckoutInfo 从工作区读取当前 HEAD 的分支与提交人信息。
// 不调用 git 命令（避免依赖 PATH），直接读 .git 元数据；
// 非 Git 工作区或读取失败时返回空结构（通知功能继续工作，只是不带分支信息）。
func ReadCheckoutInfo(workdir string) CheckoutInfo {
	info := CheckoutInfo{}
	if strings.TrimSpace(workdir) == "" {
		return info
	}
	repo, err := git.PlainOpen(workdir)
	if err != nil {
		return info
	}
	if head, err := repo.Head(); err == nil {
		// HEAD 可能是分支引用（refs/heads/main）或游离 HEAD（直接是提交）。
		if head.Name().IsBranch() {
			info.Branch = head.Name().Short()
		}
		sha := head.Hash().String()
		info.CommitFull = sha
		if len(sha) >= 8 {
			info.Commit = sha[:8]
		} else {
			info.Commit = sha
		}
		if commit, err := repo.CommitObject(head.Hash()); err == nil {
			info.Author = truncateRunes(strings.TrimSpace(commit.Author.Name), maxCommitAuthorRunes)
			info.Email = truncateRunes(strings.TrimSpace(commit.Author.Email), maxCommitEmailRunes)
			info.Message = truncateRunes(strings.TrimSpace(commit.Message), maxCommitMessageRunes)
			// Stats 会与第一父提交做 diff；merge/root 提交失败时只是没有 Changes 列表，不影响构建。
			if stats, statErr := commit.Stats(); statErr == nil && len(stats) > 0 {
				files := make([]string, 0, len(stats))
				for _, stat := range stats {
					files = append(files, stat.Name)
				}
				sort.Strings(files)
				if len(files) > maxChangedFiles {
					files = files[:maxChangedFiles]
				}
				info.Files = files
			}
		}
	}
	return info
}

// truncateRunes 按字符截断，避免把 UTF-8 字节截断成乱码。
func truncateRunes(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "…"
}

// String 便于日志输出（不涉及密钥）。
func (c CheckoutInfo) String() string {
	if c.Branch == "" && c.Commit == "" {
		return "none"
	}
	parts := make([]string, 0, 3)
	if c.Branch != "" {
		parts = append(parts, "branch="+c.Branch)
	}
	if c.Commit != "" {
		parts = append(parts, "commit="+c.Commit)
	}
	if c.Author != "" {
		parts = append(parts, fmt.Sprintf("author=%s", c.Author))
	}
	return strings.Join(parts, " ")
}

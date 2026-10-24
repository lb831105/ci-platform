package executor

import (
	"fmt"
	"strings"

	"github.com/go-git/go-git/v5"
)

// CheckoutInfo 是一次签出后的 SCM 现场信息，供通知/日志使用。
type CheckoutInfo struct {
	Branch string // 分支名（去 refs/heads/ 前缀）
	Commit string // 短 SHA（前 8 位）
	Author string // 提交人姓名
	Email  string // 提交人邮箱
}

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
		if len(sha) >= 8 {
			info.Commit = sha[:8]
		} else {
			info.Commit = sha
		}
		if commit, err := repo.CommitObject(head.Hash()); err == nil {
			info.Author = strings.TrimSpace(commit.Author.Name)
			info.Email = strings.TrimSpace(commit.Author.Email)
		}
	}
	return info
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

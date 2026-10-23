package executor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	githttp "github.com/go-git/go-git/v5/plumbing/transport/http"
)

// GitAuth 是 HTTPS 拉取所需的认证信息。
// Username+Password 对应账号密码；访问令牌场景下 Password 存放 token。
type GitAuth struct {
	Username string
	Password string
}

// GitClient 封装仓库拉取，不访问 store，输出统一走 io.Writer。
type GitClient struct{}

// NewGitClient 创建 Git 客户端。
func NewGitClient() *GitClient { return &GitClient{} }

// Clone 浅克隆指定分支到 dest，进度实时写入 out；auth 为 nil 时匿名拉取。
// 若 dest 已是仓库（工作区复用场景），则 fetch + hard reset，实现增量构建。
func (g *GitClient) Clone(ctx context.Context, url, branch, dest string, auth *GitAuth, out io.Writer) error {
	if url == "" {
		return nil
	}
	if branch == "" {
		branch = "main"
	}
	authed := auth != nil && (auth.Username != "" || auth.Password != "")

	// 复用工作区：已有 .git 时更新而不是重新克隆，否则 PlainClone 会因目录非空失败。
	if _, statErr := os.Stat(filepath.Join(dest, ".git")); statErr == nil {
		fmt.Fprintf(out, "[git] updating existing checkout %s (branch=%s, auth=%t)\n", dest, branch, authed)
		return g.update(ctx, url, branch, dest, auth, out)
	}

	// 没有 .git 的目录可能是上次失败克隆或旧的无 Git 工作区；
	// PlainClone 要求目标为空，先清理再克隆。
	if entries, readErr := os.ReadDir(dest); readErr == nil && len(entries) > 0 {
		fmt.Fprintf(out, "[git] workspace has no .git, clearing before clone: %s\n", dest)
		if rmErr := os.RemoveAll(dest); rmErr != nil {
			return fmt.Errorf("clear workspace before clone: %w", rmErr)
		}
	}

	fmt.Fprintf(out, "[git] cloning %s (branch=%s, auth=%t) -> %s\n", url, branch, authed, dest)
	opts := &git.CloneOptions{
		URL:           url,
		ReferenceName: plumbing.NewBranchReferenceName(branch),
		SingleBranch:  true,
		Depth:         1,
		Progress:      out,
	}
	if authed {
		opts.Auth = &githttp.BasicAuth{Username: auth.Username, Password: auth.Password}
	}
	if _, err := git.PlainCloneContext(ctx, dest, false, opts); err != nil {
		return fmt.Errorf("git clone %s: %w", url, err)
	}
	fmt.Fprintln(out, "[git] clone finished")
	return nil
}

// update 在已有仓库上执行 fetch → checkout → hard reset。
// 删除并重建 origin，确保任务里修改的仓库地址能立即生效。
func (g *GitClient) update(ctx context.Context, url, branch, dest string, auth *GitAuth, out io.Writer) error {
	repo, err := git.PlainOpen(dest)
	if err != nil {
		return fmt.Errorf("git open existing repo %s: %w", dest, err)
	}
	if err = repo.DeleteRemote("origin"); err != nil && !errors.Is(err, git.ErrRemoteNotFound) {
		return fmt.Errorf("git reset remote origin: %w", err)
	}
	if _, err = repo.CreateRemote(&config.RemoteConfig{Name: "origin", URLs: []string{url}}); err != nil {
		return fmt.Errorf("git create remote origin: %w", err)
	}

	fetchOpts := &git.FetchOptions{
		RemoteName: "origin",
		Depth:      1,
		Force:      true,
		RefSpecs: []config.RefSpec{
			config.RefSpec(fmt.Sprintf("+refs/heads/%s:refs/remotes/origin/%s", branch, branch)),
		},
	}
	if auth != nil && (auth.Username != "" || auth.Password != "") {
		fetchOpts.Auth = &githttp.BasicAuth{Username: auth.Username, Password: auth.Password}
	}
	fetchErr := repo.FetchContext(ctx, fetchOpts)
	alreadyUpToDate := errors.Is(fetchErr, git.NoErrAlreadyUpToDate)
	if fetchErr != nil && !alreadyUpToDate {
		return fmt.Errorf("git fetch %s: %w", url, fetchErr)
	}

	remoteRef, err := repo.Reference(plumbing.NewRemoteReferenceName("origin", branch), true)
	if err != nil {
		return fmt.Errorf("git branch %q not found on origin: %w", branch, err)
	}
	// 远端未变化且本地受版本控制的文件干净时，直接跳过 checkout/reset：
	// 不重写源码文件，保留未跟踪的构建产物，构建更快。
	if alreadyUpToDate {
		if head, headErr := repo.Head(); headErr == nil && head.Name() == plumbing.NewBranchReferenceName(branch) && head.Hash() == remoteRef.Hash() && trackedWorktreeClean(repo) {
			fmt.Fprintf(out, "[git] already up to date at origin/%s (%s), skip reset\n", branch, shortHash(remoteRef.Hash()))
			return nil
		}
	}
	wt, err := repo.Worktree()
	if err != nil {
		return fmt.Errorf("git worktree %s: %w", dest, err)
	}

	// go-git 的 Checkout/Reset 会删除未跟踪与 .gitignore 忽略的文件。
	// 构建缓存（node_modules / target / dist / Go 产物）通常属于这类文件，
	// 因此在更新前临时移出，更新完成后再移回。
	backupDir, stashErr := stashUntracked(repo, dest, remoteRef.Hash())
	if stashErr != nil {
		return fmt.Errorf("git preserve untracked files: %w", stashErr)
	}
	if backupDir != "" {
		fmt.Fprintln(out, "[git] preserving untracked/ignored files during update")
		defer func() {
			if restoreErr := restoreUntracked(backupDir, dest); restoreErr != nil {
				fmt.Fprintf(out, "[git] WARN: restore untracked files failed: %v (backup kept at %s)\n", restoreErr, backupDir)
				return
			}
			_ = os.RemoveAll(backupDir)
		}()
	}

	localRef := plumbing.NewBranchReferenceName(branch)
	// 已在目标分支时跳过 checkout：go-git 的 Force checkout 会清理未跟踪文件，
	// 导致上次构建产物丢失。只做 hard reset 即可更新受版本控制的文件。
	head, headErr := repo.Head()
	onBranch := headErr == nil && head.Name() == localRef
	if !onBranch {
		if err = wt.Checkout(&git.CheckoutOptions{Branch: localRef, Force: true}); err != nil {
			if !errors.Is(err, plumbing.ErrReferenceNotFound) {
				return fmt.Errorf("git checkout %s: %w", branch, err)
			}
			if err = wt.Checkout(&git.CheckoutOptions{Branch: localRef, Create: true, Hash: remoteRef.Hash(), Force: true}); err != nil {
				return fmt.Errorf("git create branch %s: %w", branch, err)
			}
		}
	}
	// hard reset 保证每次构建基于远端最新提交，同时保留未跟踪的构建产物（增量构建）。
	if err = wt.Reset(&git.ResetOptions{Mode: git.HardReset, Commit: remoteRef.Hash()}); err != nil {
		return fmt.Errorf("git reset to origin/%s: %w", branch, err)
	}
	fmt.Fprintf(out, "[git] workspace updated to origin/%s (%s)\n", branch, shortHash(remoteRef.Hash()))
	return nil
}

// trackedWorktreeClean 判断受版本控制的文件是否干净。
// 未跟踪文件（构建产物）不算脏，避免每次构建都因产物存在而 hard reset。
func trackedWorktreeClean(repo *git.Repository) bool {
	wt, err := repo.Worktree()
	if err != nil {
		return false
	}
	status, err := wt.Status()
	if err != nil {
		return false
	}
	for _, s := range status {
		if s.Staging == git.Untracked || s.Worktree == git.Untracked {
			continue
		}
		if s.Staging != git.Unmodified || s.Worktree != git.Unmodified {
			return false
		}
	}
	return true
}

// shortHash 截断提交哈希便于日志展示。
func shortHash(h plumbing.Hash) string {
	s := h.String()
	if len(s) > 8 {
		return s[:8]
	}
	return s
}

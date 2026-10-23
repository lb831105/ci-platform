package executor

import (
	"fmt"
	"os"
	"path"
	"path/filepath"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// stashUntracked 把工作区中未被 Git 跟踪（含 .gitignore 忽略）的文件/目录
// 临时移动到工作区外的备份目录。go-git 的 Checkout/Reset 会删除这些文件，
// 但它们往往是 node_modules、target、dist、Go 编译产物等增量构建缓存。
//
// 备份目录创建在工作区的父目录，保证同一磁盘卷，移动只是 rename。
// 只遍历包含跟踪文件的目录；整目录都未跟踪时直接移动目录，避免逐文件搬运。
func stashUntracked(repo *git.Repository, worktree string, target plumbing.Hash) (string, error) {
	tracked := make(map[string]bool)
	idx, err := repo.Storer.Index()
	if err != nil {
		return "", fmt.Errorf("read index: %w", err)
	}
	for _, entry := range idx.Entries {
		tracked[filepath.ToSlash(entry.Name)] = true
	}
	commit, err := repo.CommitObject(target)
	if err != nil {
		return "", fmt.Errorf("read target commit: %w", err)
	}
	tree, err := commit.Tree()
	if err != nil {
		return "", fmt.Errorf("read target tree: %w", err)
	}
	if err = tree.Files().ForEach(func(f *object.File) error {
		tracked[f.Name] = true
		return nil
	}); err != nil {
		return "", fmt.Errorf("walk target tree: %w", err)
	}

	// 记录“包含跟踪文件的目录”，用于决定是否可以整目录搬运。
	trackedDirs := make(map[string]bool)
	for name := range tracked {
		for dir := path.Dir(name); dir != "." && dir != "/"; dir = path.Dir(dir) {
			trackedDirs[dir] = true
		}
	}

	backup, err := os.MkdirTemp(filepath.Dir(worktree), ".ci-untracked-")
	if err != nil {
		return "", fmt.Errorf("create backup dir: %w", err)
	}

	moved := false
	moveEntry := func(rel string) error {
		src := filepath.Join(worktree, filepath.FromSlash(rel))
		dst := filepath.Join(backup, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		if err := os.Rename(src, dst); err != nil {
			return err
		}
		moved = true
		return nil
	}

	var walk func(rel string) error
	walk = func(rel string) error {
		entries, readErr := os.ReadDir(filepath.Join(worktree, filepath.FromSlash(rel)))
		if readErr != nil {
			return readErr
		}
		for _, entry := range entries {
			if rel == "" && entry.Name() == ".git" {
				continue
			}
			child := path.Join(rel, entry.Name())
			if entry.IsDir() {
				if !trackedDirs[child] {
					if err := moveEntry(child); err != nil {
						return err
					}
					continue
				}
				if err := walk(child); err != nil {
					return err
				}
				continue
			}
			if !tracked[child] {
				if err := moveEntry(child); err != nil {
					return err
				}
			}
		}
		return nil
	}

	if err = walk(""); err != nil {
		// 尽量还原已移动的文件；若还原失败则保留备份目录，避免数据丢失。
		if restoreErr := restoreUntracked(backup, worktree); restoreErr != nil {
			return "", fmt.Errorf("stash untracked: %w (restore failed: %v, backup: %s)", err, restoreErr, backup)
		}
		_ = os.RemoveAll(backup)
		return "", fmt.Errorf("stash untracked: %w", err)
	}
	if !moved {
		_ = os.RemoveAll(backup)
		return "", nil
	}
	return backup, nil
}

// restoreUntracked 把备份目录中的文件移回工作区。
// 若远端本次新增了同名文件，则以远端文件为准，跳过对应的未跟踪文件。
func restoreUntracked(backup, worktree string) error {
	return restoreDir(backup, worktree, "")
}

func restoreDir(backup, worktree, rel string) error {
	entries, err := os.ReadDir(filepath.Join(backup, filepath.FromSlash(rel)))
	if err != nil {
		return err
	}
	for _, entry := range entries {
		child := path.Join(rel, entry.Name())
		src := filepath.Join(backup, filepath.FromSlash(child))
		dst := filepath.Join(worktree, filepath.FromSlash(child))
		if entry.IsDir() {
			if info, statErr := os.Stat(dst); statErr == nil {
				if info.IsDir() {
					// 远端在目标目录下也有文件：递归合并，远端同名文件优先。
					if err := restoreDir(backup, worktree, child); err != nil {
						return err
					}
				}
				continue
			}
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				return err
			}
			if err := os.Rename(src, dst); err != nil {
				return err
			}
			continue
		}
		if _, statErr := os.Stat(dst); statErr == nil {
			// 远端跟踪了同名文件，保留远端内容。
			continue
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		if err := os.Rename(src, dst); err != nil {
			return err
		}
	}
	return nil
}

package service

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// windowsReservedNames 是 Windows 不允许作为文件/目录名的设备名。
// 即使带扩展名（如 CON.txt）也会被系统特殊处理，因此按第一个点前的主体判断。
var windowsReservedNames = map[string]bool{
	"CON": true, "PRN": true, "AUX": true, "NUL": true,
	"COM1": true, "COM2": true, "COM3": true, "COM4": true, "COM5": true,
	"COM6": true, "COM7": true, "COM8": true, "COM9": true,
	"LPT1": true, "LPT2": true, "LPT3": true, "LPT4": true, "LPT5": true,
	"LPT6": true, "LPT7": true, "LPT8": true, "LPT9": true,
}

// safeWorkspaceName 把任务名转换为可跨 Windows/Linux 使用的目录名。
// 允许中文、空格与常见符号；只替换文件系统非法字符并处理 Windows 保留名。
func safeWorkspaceName(name string) string {
	name = strings.TrimSpace(name)
	var b strings.Builder
	for _, r := range name {
		switch {
		case r < 0x20 || r == 0x7f || strings.ContainsRune(`<>:"/\|?*`, r):
			b.WriteRune('_')
		default:
			b.WriteRune(r)
		}
	}
	out := strings.Trim(b.String(), " .")
	if out == "" {
		return ""
	}
	base := strings.ToUpper(strings.SplitN(out, ".", 2)[0])
	if windowsReservedNames[base] {
		out = "_" + out
	}
	// 控制目录名长度，留出 -{id} 后缀的空间（文件系统通常限制 255 字节/字符）。
	if runes := []rune(out); len(runes) > 80 {
		out = string(runes[:80])
	}
	out = strings.TrimRight(out, " .")
	return out
}

// workspaceDirName 返回任务默认的工作区目录名；名称无有效字符时退回 pipeline-{id}。
func workspaceDirName(name, id string) string {
	if dir := safeWorkspaceName(name); dir != "" {
		return dir
	}
	return "pipeline-" + id
}

// workspaceDirOf 返回 Job 实际使用的工作区目录名，兼容旧 Job（只带 PipelineID）。
func workspaceDirOf(job Job) string {
	if dir := strings.TrimSpace(job.WorkspaceDir); dir != "" {
		return filepath.Base(dir)
	}
	return job.PipelineID
}

// Path 返回某个任务的工作区目录 data/workspace/{workspace_dir}。
func (m *WorkspaceManager) Path(job Job) string {
	dir := workspaceDirOf(job)
	if dir == "." || dir == ".." || dir == "" {
		dir = job.PipelineID
	}
	return filepath.Join(m.root, dir)
}

// Exists 判断工作区目录是否存在。
func (m *WorkspaceManager) Exists(dir string) bool {
	dir = filepath.Base(strings.TrimSpace(dir))
	if dir == "" || dir == "." || dir == ".." {
		return false
	}
	_, err := os.Stat(filepath.Join(m.root, dir))
	return err == nil
}

// Rename 在任务重命名时同步移动工作区，保留增量构建状态。
// 旧目录不存在时视为成功；新目录已存在时返回冲突，避免覆盖其他任务。
func (m *WorkspaceManager) Rename(oldDir, newDir string) error {
	oldDir = filepath.Base(strings.TrimSpace(oldDir))
	newDir = filepath.Base(strings.TrimSpace(newDir))
	if oldDir == "" || newDir == "" || oldDir == "." || newDir == "." || oldDir == ".." || newDir == ".." {
		return fmt.Errorf("invalid workspace dir: %q -> %q", oldDir, newDir)
	}
	if oldDir == newDir {
		return nil
	}
	oldPath := filepath.Join(m.root, oldDir)
	newPath := filepath.Join(m.root, newDir)
	if _, err := os.Stat(oldPath); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if _, err := os.Stat(newPath); err == nil {
		return fmt.Errorf("workspace %q already exists", newDir)
	} else if !os.IsNotExist(err) {
		return err
	}
	return os.Rename(oldPath, newPath)
}

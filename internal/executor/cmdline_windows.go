//go:build windows

package executor

import (
	"context"
	"fmt"
	"os/exec"
	"syscall"
)

// configureCmd 用 SysProcAttr.CmdLine 原样传递命令。
// 若通过 exec.Command 的参数传递，Go 会按 CreateProcess 规则转义引号，
// 导致 cmd.exe 收到 \" 且 && / || 管道行为异常；直接给出命令行可避免该问题。
func configureCmd(cmd *exec.Cmd, run string) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: `/d /s /c "` + run + `"`}
}

// prepareProcessGroup Windows 上无需进程组（taskkill /T 已覆盖进程树）。
func prepareProcessGroup(cmd *exec.Cmd) {}

// killProcessTree 终止整个进程树（cmd.exe 及其全部子进程）。
// Windows 上 exec.CommandContext 的 kill 只杀直接子进程：cmd.exe 被杀后，
// 它启动的 sleep/npm/mvn 等孙进程会继续运行并占用工作区文件，
// 导致取消构建后目录无法删除。taskkill /T /F 递归杀树，彻底释放资源。
func killProcessTree(ctx context.Context, cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	kill := exec.CommandContext(context.WithoutCancel(ctx), "taskkill", "/T", "/F", "/PID",
		fmt.Sprintf("%d", cmd.Process.Pid))
	kill.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	// 输出丢弃：杀进程失败时由上层 ctx.Err 兜底报错。
	_, _ = kill.CombinedOutput()
	return nil
}

//go:build windows

package executor

import (
	"os/exec"
	"syscall"
)

// configureCmd 用 SysProcAttr.CmdLine 原样传递命令。
// 若通过 exec.Command 的参数传递，Go 会按 CreateProcess 规则转义引号，
// 导致 cmd.exe 收到 \" 且 && / || 管道行为异常；直接给出命令行可避免该问题。
func configureCmd(cmd *exec.Cmd, run string) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: `/d /s /c "` + run + `"`}
}

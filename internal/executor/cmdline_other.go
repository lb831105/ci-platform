//go:build !windows

package executor

import (
	"context"
	"os/exec"
	"syscall"
)

// configureCmd 在非 Windows 平台不使用 cmd.exe，保留空实现以便跨平台编译。
func configureCmd(cmd *exec.Cmd, run string) {}

// prepareProcessGroup 让子进程独立成组（Unix），取消时能对整组发信号，
// 杀掉 shell 启动的全部孙进程（npm/mvn 等）。
func prepareProcessGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
}

// killProcessTree 终止整个进程树。Unix 上给子进程单独建进程组（Setpgid，
// 组 ID 即子进程 PID），取消时对进程组发 SIGKILL，杀掉 shell 及其全部子进程。
func killProcessTree(ctx context.Context, cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}
	if cmd.SysProcAttr != nil && cmd.SysProcAttr.Setpgid {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	return cmd.Process.Kill()
}

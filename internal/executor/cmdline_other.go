//go:build !windows

package executor

import "os/exec"

// configureCmd 在非 Windows 平台不使用 cmd.exe，保留空实现以便跨平台编译。
func configureCmd(cmd *exec.Cmd, run string) {}

package executor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// LocalExecutor 直接在运行服务的服务器上执行构建步骤（不依赖容器）。
// 注意：它以 CI 服务进程的用户身份运行命令，能力等同于在该用户下执行 shell，
// 请只把任务编辑权限授予可信用户。
type LocalExecutor struct{}

// NewLocalExecutor 创建本地执行器。
func NewLocalExecutor() *LocalExecutor { return &LocalExecutor{} }

// RunSteps 在工作区目录下串行执行步骤，返回最终退出码。
func (l *LocalExecutor) RunSteps(ctx context.Context, spec RunSpec, out io.Writer) (int, error) {
	if len(spec.Steps) == 0 {
		fmt.Fprintln(out, "[local] no steps defined, nothing to run")
		return 0, nil
	}
	if spec.Workspace == "" {
		return -1, errors.New("local exec: workspace is required")
	}
	if err := os.MkdirAll(spec.Workspace, 0o755); err != nil {
		return -1, fmt.Errorf("local exec: prepare workspace: %w", err)
	}

	// Windows 单行命令直接通过 CmdLine 交给 cmd.exe，保留引号与 && / || 语义；
	// 多行命令或 PowerShell / 自定义 shell 才写入临时脚本。
	tmpDir := filepath.Join(spec.Workspace, ".ci-tmp")
	if err := os.RemoveAll(tmpDir); err != nil {
		return -1, fmt.Errorf("local exec: clean temp dir: %w", err)
	}
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		return -1, fmt.Errorf("local exec: create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	for i, step := range spec.Steps {
		if err := ctx.Err(); err != nil {
			return -1, err
		}
		name := step.Name
		if name == "" {
			name = fmt.Sprintf("step-%d", i+1)
		}

		// Windows 单行命令直接走 cmd.exe，避免脚本文件里调用 npm.cmd / mvn.cmd
		// 时控制流不返回导致 && / || 后续命令不执行。
		_, kind := resolveShell(spec.Shell)
		var cmd *exec.Cmd
		scriptPath := ""
		if kind == "cmd" && !strings.ContainsAny(step.Run, "\r\n") {
			cmd = exec.CommandContext(ctx, "cmd.exe")
			configureCmd(cmd, step.Run)
		} else {
			shell, args, path, err := writeStepScript(tmpDir, i+1, spec.Shell, step.Run)
			if err != nil {
				return -1, err
			}
			scriptPath = path
			cmd = exec.CommandContext(ctx, shell, args...)
		}
		cmd.Dir = spec.Workspace
		cmd.Env = mergeEnv(os.Environ(), spec.Env)
		cmd.Stdout = out
		cmd.Stderr = out

		fmt.Fprintf(out, "\n[local] === step %d/%d: %s ===\n$ %s\n", i+1, len(spec.Steps), name, step.Run)
		err := cmd.Run()
		if scriptPath != "" {
			_ = os.Remove(scriptPath)
		}
		if err == nil {
			continue
		}
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			code := exitErr.ExitCode()
			fmt.Fprintf(out, "[local] step %q failed with exit code %d\n", name, code)
			return code, nil
		}
		if ctx.Err() != nil {
			return -1, fmt.Errorf("local exec: cancelled: %w", ctx.Err())
		}
		return -1, fmt.Errorf("local exec %q: %w", name, err)
	}
	fmt.Fprintln(out, "\n[local] all steps succeeded")
	return 0, nil
}

// writeStepScript 把步骤命令写成临时脚本，并返回 shell、参数与脚本路径。
// 脚本方式能完整保留引号、重定向与多行命令，且不受 Windows 命令行转义规则影响。
func writeStepScript(dir string, index int, shell, run string) (string, []string, string, error) {
	exe, kind := resolveShell(shell)
	ext := ".sh"
	content := run + "\n"
	switch kind {
	case "cmd":
		ext = ".cmd"
		content = "@echo off\r\n" + run + "\r\n"
	case "powershell":
		ext = ".ps1"
		// PowerShell 5.1 对无 BOM 的 UTF-8 脚本按 ANSI 解析，带 BOM 才能正确处理中文。
		content = "\ufeff" + run + "\r\n"
	}
	path := filepath.Join(dir, fmt.Sprintf("step-%d%s", index, ext))
	if err := os.WriteFile(path, []byte(content), 0o700); err != nil {
		return "", nil, "", fmt.Errorf("local exec: write step script: %w", err)
	}
	switch kind {
	case "cmd":
		// call 保证路径含空格时仍能正确执行 .cmd 文件。
		return "cmd.exe", []string{"/d", "/s", "/c", "call", path}, path, nil
	case "powershell":
		return exe, []string{"-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", path}, path, nil
	default:
		return exe, []string{path}, path, nil
	}
}

// resolveShell 返回实际执行的 shell 与脚本类型（cmd / powershell / sh）。
// Windows 默认 cmd.exe；配置为 powershell/pwsh 时用 -File 执行，其他 shell（如 Git Bash）按脚本执行。
func resolveShell(shell string) (string, string) {
	shell = strings.TrimSpace(shell)
	if runtime.GOOS == "windows" {
		base := strings.ToLower(filepath.Base(strings.ReplaceAll(shell, "/", `\`)))
		switch {
		case strings.Contains(base, "powershell"), base == "pwsh", base == "pwsh.exe":
			return shell, "powershell"
		case base == "cmd", base == "cmd.exe", shell == "":
			return "cmd.exe", "cmd"
		// 系统配置的默认值是 /bin/sh；Windows 上映射为 cmd.exe。
		case strings.EqualFold(shell, "/bin/sh"), base == "sh":
			return "cmd.exe", "cmd"
		default:
			return shell, "sh"
		}
	}
	if shell == "" {
		shell = "/bin/sh"
	}
	return shell, "sh"
}

// mergeEnv 合并基础环境与额外变量（大小写不敏感覆盖，兼容 Windows）。
func mergeEnv(base []string, extra map[string]string) []string {
	out := make([]string, 0, len(base)+len(extra))
	index := make(map[string]int, len(base))
	for _, kv := range base {
		key, _, ok := strings.Cut(kv, "=")
		if !ok {
			continue
		}
		index[strings.ToUpper(key)] = len(out)
		out = append(out, kv)
	}
	for key, value := range extra {
		if i, ok := index[strings.ToUpper(key)]; ok {
			out[i] = key + "=" + value
		} else {
			out = append(out, key+"="+value)
		}
	}
	return out
}

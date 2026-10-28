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
	"time"

	"ci-platform/internal/model"
)

// LocalExecutor 直接在运行服务的服务器上执行构建步骤（不依赖容器）。
// 注意：它以 CI 服务进程的用户身份运行命令，能力等同于在该用户下执行 shell，
// 请只把任务编辑权限授予可信用户。
type LocalExecutor struct{}

// NewLocalExecutor 创建本地执行器。
func NewLocalExecutor() *LocalExecutor { return &LocalExecutor{} }

// stepState 记录已执行步骤的失败情况，用于 when / allow_failure 的决策。
// 区分 anyFailed（含允许失败的步骤）与 fatalFailed（未被允许的失败）：
// when=failure 的清理步骤在两种情况下都应执行。
type stepState struct {
	anyFailed   bool
	fatalFailed bool
	fatalCode   int
}

// shouldRunStep 判断步骤在当前状态下是否执行（对齐 GitHub Actions / GitLab CI 的 when 语义）。
func shouldRunStep(when string, s stepState) bool {
	switch strings.ToLower(strings.TrimSpace(when)) {
	case "always":
		return true
	case "failure":
		return s.anyFailed
	default: // "" / "success"：前置步骤都成功才执行
		return !s.fatalFailed
	}
}

// RunSteps 在工作区目录下串行执行步骤，返回最终退出码。
// 这是兼容旧调用方的简化入口；需要步骤耗时/状态时使用 RunStepsWithResults。
func (l *LocalExecutor) RunSteps(ctx context.Context, spec RunSpec, out io.Writer) (int, error) {
	_, code, err := l.RunStepsWithResults(ctx, spec, out)
	return code, err
}

// RunStepsWithResults 执行步骤并同时返回每一步的状态与耗时。
//
// 步骤级控制：
//   - timeout_sec：单次尝试的执行时限，超时杀掉整个进程树并视为失败（退出码 124）
//   - retry / retry_delay_sec：失败后额外重试次数与间隔
//   - allow_failure：失败只记警告，不阻断后续步骤
//   - when：success（默认，前置成功才跑）/ failure（有失败才跑）/ always（总是跑）
//
// 未被允许的失败会中止后续 success 步骤，但 always/failure 步骤仍会执行，
// 这样「清理/收集诊断信息」类步骤不会因为前面的失败而漏跑。
func (l *LocalExecutor) RunStepsWithResults(ctx context.Context, spec RunSpec, out io.Writer) ([]model.StepResult, int, error) {
	if len(spec.Steps) == 0 {
		fmt.Fprintln(out, "[local] no steps defined, nothing to run")
		return nil, 0, nil
	}
	if spec.Workspace == "" {
		return nil, -1, errors.New("local exec: workspace is required")
	}
	if err := os.MkdirAll(spec.Workspace, 0o755); err != nil {
		return nil, -1, fmt.Errorf("local exec: prepare workspace: %w", err)
	}

	// Windows 单行命令直接通过 CmdLine 交给 cmd.exe，保留引号与 && / || 语义；
	// 多行命令或 PowerShell / 自定义 shell 才写入临时脚本。
	tmpDir := filepath.Join(spec.Workspace, ".ci-tmp")
	if err := os.RemoveAll(tmpDir); err != nil {
		return nil, -1, fmt.Errorf("local exec: clean temp dir: %w", err)
	}
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		return nil, -1, fmt.Errorf("local exec: create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	results := make([]model.StepResult, 0, len(spec.Steps))
	var state stepState
	for i, step := range spec.Steps {
		if err := ctx.Err(); err != nil {
			return results, -1, err
		}
		name := step.Name
		if name == "" {
			name = fmt.Sprintf("step-%d", i+1)
		}
		if !shouldRunStep(step.When, state) {
			fmt.Fprintf(out, "\n[local] === step %d/%d: %s ===\n[local] skipped (when=%s%s)\n",
				i+1, len(spec.Steps), name, stepWhen(step.When), skipReason(state))
			results = append(results, model.StepResult{Name: name, Status: "skipped"})
			continue
		}

		startedAt := time.Now()
		code, err := l.runStepWithRetry(ctx, spec, tmpDir, i, step, name, out)
		duration := time.Since(startedAt)
		if err != nil {
			// 只有取消/启动失败会返回 error；取消必须立即整体中止。
			results = append(results, model.StepResult{
				Name: name, Status: "cancelled", ExitCode: -1,
				DurationMS: duration.Milliseconds(), AllowFailure: step.AllowFailure,
			})
			return results, -1, err
		}
		status := "success"
		if code != 0 {
			status = "failed"
		}
		results = append(results, model.StepResult{
			Name: name, Status: status, ExitCode: code,
			DurationMS: duration.Milliseconds(), AllowFailure: step.AllowFailure,
		})
		if code == 0 {
			continue
		}
		state.anyFailed = true
		if step.AllowFailure {
			fmt.Fprintf(out, "[local] step %q failed with exit code %d (allow_failure=true，继续后续步骤)\n", name, code)
			continue
		}
		if !state.fatalFailed {
			state.fatalFailed = true
			state.fatalCode = code
		}
		fmt.Fprintf(out, "[local] step %q failed with exit code %d (中止后续步骤，仅执行 when=always/failure)\n", name, code)
	}
	if state.fatalFailed {
		return results, state.fatalCode, nil
	}
	fmt.Fprintln(out, "\n[local] all steps succeeded")
	return results, 0, nil
}

// stepWhen 把空值归一为默认的 success，便于日志展示。
func stepWhen(when string) string {
	when = strings.ToLower(strings.TrimSpace(when))
	if when == "" {
		return "success"
	}
	return when
}

// skipReason 解释跳过原因，避免看日志时误以为步骤被静默丢弃。
func skipReason(s stepState) string {
	if s.fatalFailed {
		return "，前置步骤失败"
	}
	return "，前置步骤未失败"
}

// runStepWithRetry 执行单个步骤（含重试），返回最终退出码。
// 超时与退出码非零都只是「失败」，交给上层决定 allow_failure / 中止；
// 仅当构建被取消或进程无法启动时返回 error。
func (l *LocalExecutor) runStepWithRetry(ctx context.Context, spec RunSpec, tmpDir string, index int, step model.Step, name string, out io.Writer) (int, error) {
	attempts := step.Retry + 1
	if attempts < 1 {
		attempts = 1
	}
	if attempts > model.MaxStepRetry+1 {
		attempts = model.MaxStepRetry + 1
	}
	delay := time.Duration(step.RetryDelaySec) * time.Second
	for attempt := 1; attempt <= attempts; attempt++ {
		if attempt > 1 {
			fmt.Fprintf(out, "[local] 重试 %s（第 %d/%d 次）\n", name, attempt, attempts)
			if delay > 0 {
				// 等待期间也要响应取消，避免取消后还要干等重试间隔。
				select {
				case <-ctx.Done():
					return -1, fmt.Errorf("local exec: cancelled: %w", ctx.Err())
				case <-time.After(delay):
				}
			}
		}
		code, retryable, err := l.runStepOnce(ctx, spec, tmpDir, index, step, name, out, attempt, attempts)
		if err != nil {
			return -1, err
		}
		if code == 0 {
			return 0, nil
		}
		if !retryable {
			return code, nil
		}
		if attempt == attempts {
			return code, nil
		}
	}
	return 0, nil
}

// runStepOnce 执行一次步骤。retryable 表示这次失败值得重试（退出码/超时都可重试，
// 启动失败通常是环境问题，也允许重试一次；取消则直接返回 error）。
func (l *LocalExecutor) runStepOnce(ctx context.Context, spec RunSpec, tmpDir string, index int, step model.Step, name string, out io.Writer, attempt, attempts int) (code int, retryable bool, err error) {
	// Windows 单行命令直接走 cmd.exe，避免脚本文件里调用 npm.cmd / mvn.cmd
	// 时控制流不返回导致 && / || 后续命令不执行。
	_, kind := resolveShell(spec.Shell)
	var cmd *exec.Cmd
	scriptPath := ""
	// 注意：不用 exec.CommandContext（原因见下方 Start 处注释）。
	if kind == "cmd" && !strings.ContainsAny(step.Run, "\r\n") {
		cmd = exec.Command("cmd.exe")
		configureCmd(cmd, step.Run)
	} else {
		shell, args, path, werr := writeStepScript(tmpDir, index+1, spec.Shell, step.Run)
		if werr != nil {
			return -1, false, werr
		}
		scriptPath = path
		cmd = exec.Command(shell, args...)
	}
	cmd.Dir = spec.Workspace
	cmd.Env = mergeEnv(os.Environ(), spec.Env)
	cmd.Stdout = out
	cmd.Stderr = out

	label := fmt.Sprintf("step %d/%d: %s", index+1, len(spec.Steps), name)
	if attempts > 1 {
		fmt.Fprintf(out, "\n[local] === %s (第 %d/%d 次尝试) ===\n$ %s\n", label, attempt, attempts, step.Run)
	} else {
		fmt.Fprintf(out, "\n[local] === %s ===\n$ %s\n", label, step.Run)
	}

	// 步骤级超时：用子 context 控制杀进程，父 context 的取消除外。
	runCtx := ctx
	if step.TimeoutSec > 0 {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(ctx, time.Duration(step.TimeoutSec)*time.Second)
		defer cancel()
	}

	prepareProcessGroup(cmd)
	// 手动 Start：ctx 取消/超时时用 taskkill /T /F 杀整个进程树（cmd.exe
	// 及其全部子进程），彻底释放管道与文件句柄。不能用 exec.CommandContext——
	// 它会在 ctx 取消时抢先杀掉 cmd.exe 本身，导致 taskkill /T 找不到进程树，
	// 而孙进程（sleep/npm）仍持有管道写端，cmd.Wait() 永远阻塞。
	if err := cmd.Start(); err != nil {
		return -1, true, fmt.Errorf("local exec %q: start: %w", name, err)
	}
	killDone := make(chan struct{})
	go func() {
		select {
		case <-runCtx.Done():
			_ = killProcessTree(context.Background(), cmd)
		case <-killDone:
		}
	}()
	waitErr := cmd.Wait()
	close(killDone)
	if scriptPath != "" {
		_ = os.Remove(scriptPath)
	}

	// 构建立即取消（父 context）优先：语义是 aborted 而不是 failed。
	if ctx.Err() != nil {
		fmt.Fprintf(out, "[local] step %q cancelled: %v\n", name, ctx.Err())
		return -1, false, fmt.Errorf("local exec: cancelled: %w", ctx.Err())
	}
	if step.TimeoutSec > 0 && errors.Is(runCtx.Err(), context.DeadlineExceeded) {
		fmt.Fprintf(out, "[local] step %q 超时（%ds），已终止进程树\n", name, step.TimeoutSec)
		// 124 是 GNU timeout 的约定退出码，便于外部脚本识别超时。
		return 124, true, nil
	}
	if waitErr == nil {
		return 0, false, nil
	}
	var exitErr *exec.ExitError
	if errors.As(waitErr, &exitErr) {
		code := exitErr.ExitCode()
		fmt.Fprintf(out, "[local] step %q failed with exit code %d\n", name, code)
		return code, true, nil
	}
	return -1, true, fmt.Errorf("local exec %q: %w", name, waitErr)
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

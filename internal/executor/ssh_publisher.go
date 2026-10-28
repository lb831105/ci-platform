package executor

import (
	"ci-platform/internal/pkg/logger"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"ci-platform/internal/model"
)

// SshPublisher 是项目中唯一负责 SSH / SFTP 交互的执行器。
// 语义对齐 Jenkins Publish Over SSH：上传匹配文件到远程目录，可选在远程执行命令。
// service 层禁止拼接任何 SSH 命令字符串，全部通过本文件完成。
type SshPublisher struct{}

// NewSshPublisher 创建 SSH 发布器。
func NewSshPublisher() *SshPublisher { return &SshPublisher{} }

// Publish 执行完整的发布流程：连接 → 上传文件 → 执行远程命令。
// cfg.Enabled 为 false 时直接返回 nil，不发起任何连接。
// 远程命令的 stdout/stderr 通过 out 实时输出，与构建日志合并到同一 log.txt。
func (p *SshPublisher) Publish(ctx context.Context, cfg *model.SshPublishConfig, workspace string, out io.Writer) error {
	if cfg == nil || !cfg.Enabled {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if cfg.Host == "" || cfg.Username == "" {
		return errors.New("ssh publish: host and username are required")
	}

	client, err := p.dial(ctx, cfg)
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()

	fmt.Fprintf(out, "\n[ssh] === publish to %s@%s:%d ===\n", cfg.Username, cfg.Host, cfg.PortOf())

	files, notes, err := matchFiles(workspace, cfg.SourceFiles)
	if err != nil {
		return err
	}
	for _, note := range notes {
		fmt.Fprintf(out, "[ssh] %s", note)
		fmt.Fprintln(out)
	}
	if len(files) == 0 {
		return fmt.Errorf("SSH 发布：%w", newNoMatchError(workspace, cfg.SourceFiles))
	}

	if err := p.upload(ctx, client, cfg, workspace, files, out); err != nil {
		return err
	}

	if strings.TrimSpace(cfg.ExecCommand) != "" {
		// 远程命令超时（对齐 Jenkins execTimeout）：只约束命令本身，
		// 不影响上传阶段；超时需要杀掉远程会话，否则部署命令可能一直挂着。
		cmdCtx := ctx
		if cfg.ExecTimeoutSec > 0 {
			var cancel context.CancelFunc
			cmdCtx, cancel = context.WithTimeout(ctx, time.Duration(cfg.ExecTimeoutSec)*time.Second)
			defer cancel()
		}
		if err := p.runCommand(cmdCtx, client, cfg.ExecCommand, out); err != nil {
			if cfg.ExecTimeoutSec > 0 && errors.Is(cmdCtx.Err(), context.DeadlineExceeded) {
				fmt.Fprintf(out, "[ssh] 远程命令超时（%ds），已终止会话\n", cfg.ExecTimeoutSec)
				return fmt.Errorf("ssh publish: remote command timed out after %ds", cfg.ExecTimeoutSec)
			}
			return err
		}
	}

	fmt.Fprintln(out, "[ssh] publish finished")
	return nil
}

// PublishArtifacts 把某次构建已归档的产物（buildDir/artifacts/）直接发布到远程，
// 不重新编译。用于「构建成功但发布失败/需要回滚重发」的场景。
func (p *SshPublisher) PublishArtifacts(ctx context.Context, cfg *model.SshPublishConfig, buildDir string, out io.Writer) error {
	if cfg == nil || !cfg.Enabled {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if cfg.Host == "" || cfg.Username == "" {
		return errors.New("ssh publish: host and username are required")
	}
	artifactsDir := filepath.Join(buildDir, model.ArtifactDirName)
	files, _, err := matchFiles(artifactsDir, []string{"**"})
	if err != nil {
		return fmt.Errorf("republish: %w", err)
	}
	if len(files) == 0 {
		return fmt.Errorf("republish: no artifacts under %s", artifactsDir)
	}

	client, err := p.dial(ctx, cfg)
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()

	fmt.Fprintf(out, "\n[ssh] === re-publish %d artifact file(s) from build artifacts/ ===\n", len(files))
	// 产物目录即相对路径根；不剥离前缀，保证与归档时一致的远程目录结构。
	uploadCfg := *cfg
	uploadCfg.RemovePrefix = ""
	if err := p.upload(ctx, client, &uploadCfg, artifactsDir, files, out); err != nil {
		return err
	}
	fmt.Fprintln(out, "[ssh] re-publish finished")
	return nil
}

// dial 建立 SSH 连接，支持密钥与密码两种认证方式。
func (p *SshPublisher) dial(ctx context.Context, cfg *model.SshPublishConfig) (*ssh.Client, error) {
	var auths []ssh.AuthMethod
	switch cfg.AuthType {
	case "password":
		if cfg.Password == "" {
			return nil, errors.New("ssh publish: password auth requires password")
		}
		auths = append(auths, ssh.Password(cfg.Password))
		// 部分服务器只启用 keyboard-interactive（而非 password）认证，用同一密码应答。
		auths = append(auths, ssh.KeyboardInteractive(func(_, _ string, questions []string, _ []bool) ([]string, error) {
			answers := make([]string, len(questions))
			for i := range answers {
				answers[i] = cfg.Password
			}
			return answers, nil
		}))
	default: // key
		if cfg.PrivateKeyPath == "" {
			return nil, errors.New("ssh publish: key auth requires private_key_path")
		}
		keyPath := expandHome(cfg.PrivateKeyPath)
		keyBytes, err := os.ReadFile(keyPath)
		if err != nil {
			return nil, fmt.Errorf("ssh publish: read private key %s: %w", keyPath, err)
		}
		signer, err := ssh.ParsePrivateKey(keyBytes)
		if err != nil {
			return nil, fmt.Errorf("ssh publish: parse private key: %w", err)
		}
		auths = append(auths, ssh.PublicKeys(signer))
	}

	addr := net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.PortOf()))
	hostKeyCallback, err := sshHostKeyCallback(cfg.KnownHostsPath)
	if err != nil {
		return nil, err
	}
	clientCfg := &ssh.ClientConfig{
		User:            cfg.Username,
		Auth:            auths,
		HostKeyCallback: hostKeyCallback,
		Timeout:         15 * time.Second,
	}

	dialer := net.Dialer{Timeout: 15 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("ssh publish: dial %s: %w", addr, err)
	}
	sshConn, chans, reqs, err := ssh.NewClientConn(conn, addr, clientCfg)
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("ssh publish: handshake %s: %w", addr, err)
	}
	return ssh.NewClient(sshConn, chans, reqs), nil
}

// sshHostKeyCallback 构建主机密钥校验回调。
// 未配置 known_hosts 时保持兼容行为并明确告警；配置后未知主机将直接拒绝连接，
// 防止中间人攻击。
func sshHostKeyCallback(knownHostsPath string) (ssh.HostKeyCallback, error) {
	knownHostsPath = strings.TrimSpace(knownHostsPath)
	if knownHostsPath == "" {
		logger.Warnf("ssh publish host key verification disabled; configure known_hosts_path to enable it")
		return ssh.InsecureIgnoreHostKey(), nil
	}
	path := expandHome(knownHostsPath)
	callback, err := knownhosts.New(path)
	if err != nil {
		return nil, fmt.Errorf("ssh publish: load known_hosts %s: %w", path, err)
	}
	return callback, nil
}

// expandHome 展开 ~/ 前缀，方便在界面中填写当前用户的 known_hosts / 私钥路径。
func expandHome(path string) string {
	path = strings.TrimSpace(path)
	if path == "~" || strings.HasPrefix(path, "~/") || strings.HasPrefix(path, `~\`) {
		if home, err := os.UserHomeDir(); err == nil {
			if path == "~" {
				return home
			}
			return filepath.Join(home, path[2:])
		}
	}
	return path
}

// upload 通过 SFTP 上传文件，自动创建远程目录并支持剥离前缀。
func (p *SshPublisher) upload(ctx context.Context, client *ssh.Client, cfg *model.SshPublishConfig, workspace string, files []string, out io.Writer) error {
	sftpCli, err := sftp.NewClient(client)
	if err != nil {
		return fmt.Errorf("ssh publish: init sftp: %w", err)
	}
	defer func() { _ = sftpCli.Close() }()

	base := strings.TrimRight(filepath.ToSlash(cfg.RemoteBaseDir), "/")
	// 拒绝空目录与根目录：误发布到 / 可能覆盖远程服务器上的系统文件。
	if base == "" || base == "/" {
		return fmt.Errorf("ssh publish: remote_base_dir is required (refusing to publish to /)")
	}
	if cfg.CleanRemote {
		// 对齐 Jenkins「Clean remote directory」：上传前清空目标目录，用于整目录替换部署。
		// 递归删除不可逆，日志里明确写出被清空的路径，便于事后排查。
		fmt.Fprintf(out, "[ssh] clean remote directory %s (cleanRemote=true)\n", base)
		if err = sftpCli.RemoveAll(base); err != nil {
			return fmt.Errorf("ssh publish: clean %s: %w", base, err)
		}
	}
	if err = sftpCli.MkdirAll(base); err != nil {
		return fmt.Errorf("ssh publish: mkdir %s: %w", base, err)
	}

	uploaded := 0
	for _, local := range files {
		if err = ctx.Err(); err != nil {
			return err
		}
		info, statErr := os.Lstat(local)
		if statErr != nil {
			return fmt.Errorf("ssh publish: stat local %s: %w", local, statErr)
		}
		// 不跟随符号链接：恶意仓库可能用链接把工作区外的服务器文件上传到目标机。
		if info.Mode()&os.ModeSymlink != 0 {
			fmt.Fprintf(out, "[ssh] WARN: skip symlink %s", local)
			fmt.Fprintln(out)
			continue
		}
		if !info.Mode().IsRegular() {
			continue
		}
		rel := relativeTo(workspace, local)
		rel = applyRemovePrefix(rel, cfg.RemovePrefix)
		rel = applyFlatten(rel, cfg.Flatten)
		remotePath := path.Join(base, rel)

		if err = sftpCli.MkdirAll(path.Dir(remotePath)); err != nil {
			return fmt.Errorf("ssh publish: mkdir %s: %w", path.Dir(remotePath), err)
		}
		if err = copyFile(sftpCli, local, remotePath); err != nil {
			return err
		}
		uploaded++
		fmt.Fprintf(out, "[ssh] uploaded %s -> %s\n", rel, remotePath)
	}
	if uploaded == 0 {
		return fmt.Errorf("ssh publish: no regular files matched %v", cfg.SourceFiles)
	}
	return nil
}

// runCommand 在远程执行部署脚本，实时回传 stdout/stderr。
func (p *SshPublisher) runCommand(ctx context.Context, client *ssh.Client, cmd string, out io.Writer) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	session, err := client.NewSession()
	if err != nil {
		return fmt.Errorf("ssh publish: new session: %w", err)
	}
	defer func() { _ = session.Close() }()

	stdout, err := session.StdoutPipe()
	if err != nil {
		return fmt.Errorf("ssh publish: stdout pipe: %w", err)
	}
	stderr, err := session.StderrPipe()
	if err != nil {
		return fmt.Errorf("ssh publish: stderr pipe: %w", err)
	}

	fmt.Fprintf(out, "\n[ssh] $ %s\n", cmd)
	if err = session.Start(cmd); err != nil {
		return fmt.Errorf("ssh publish: start remote command: %w", err)
	}

	// 构建超时/取消时关闭会话，终止远程命令，避免超时后仍在继续部署。
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = session.Close()
		case <-done:
		}
	}()

	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); _, _ = io.Copy(out, stdout) }()
	go func() { defer wg.Done(); _, _ = io.Copy(out, stderr) }()
	wg.Wait()
	close(done)

	if err = session.Wait(); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("ssh publish: remote command cancelled: %w", ctx.Err())
		}
		return fmt.Errorf("ssh publish: remote command failed: %w", err)
	}
	fmt.Fprintln(out, "[ssh] remote command finished")
	return nil
}

// applyFlatten 对齐 Jenkins「Flatten files」：开启后只保留文件名、丢弃相对目录。
// 单独抽出来是为了能在没有 SSH 服务端的单测里覆盖路径决策。
func applyFlatten(rel string, flatten bool) string {
	if !flatten {
		return rel
	}
	return path.Base(rel)
}

// matchFiles 展开待上传文件，支持 Jenkins 风格的 ** 递归通配与目录模式。
// matchFiles 是 SSH 发布对通用工作区 glob 匹配器的包装。
// notes（自动递归兜底的说明）由调用方打印到构建日志。
func matchFiles(workspace string, patterns []string) ([]string, []string, error) {
	return matchWorkspaceFilesDetailed(workspace, patterns, "ssh publish")
}

// relativeTo 计算文件相对工作区的斜杠路径。
func relativeTo(workspace, file string) string {
	rel, err := filepath.Rel(workspace, file)
	if err != nil || strings.HasPrefix(rel, "..") {
		return filepath.ToSlash(filepath.Base(file))
	}
	return filepath.ToSlash(rel)
}

// applyRemovePrefix 从上传路径中剥离指定前缀（按路径段匹配，避免 target 误伤 targetx）。
func applyRemovePrefix(rel, prefix string) string {
	prefix = strings.Trim(prefix, "/")
	if prefix == "" {
		return rel
	}
	if strings.HasPrefix(rel, prefix+"/") {
		if trimmed := rel[len(prefix)+1:]; trimmed != "" {
			return trimmed
		}
	}
	return rel
}

// withinDir 判断 target 是否位于 base 目录内（含 base 本身）。
func withinDir(base, target string) bool {
	rel, err := filepath.Rel(base, target)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// copyFile 本地文件 → 远程文件。
func copyFile(client *sftp.Client, localPath, remotePath string) error {
	src, err := os.Open(localPath)
	if err != nil {
		return fmt.Errorf("ssh publish: open local %s: %w", localPath, err)
	}
	defer func() { _ = src.Close() }()

	dst, err := client.Create(remotePath)
	if err != nil {
		return fmt.Errorf("ssh publish: create remote %s: %w", remotePath, err)
	}
	if _, err = io.Copy(dst, src); err != nil {
		_ = dst.Close()
		return fmt.Errorf("ssh publish: copy to %s: %w", remotePath, err)
	}
	if err = dst.Close(); err != nil {
		return fmt.Errorf("ssh publish: close remote %s: %w", remotePath, err)
	}
	// 保留可执行位，便于上传部署脚本后可直接执行。
	mode := os.FileMode(0o644)
	if info, statErr := os.Stat(localPath); statErr == nil && info.Mode()&0o111 != 0 {
		mode = 0o755
	}
	_ = client.Chmod(remotePath, mode)
	return nil
}

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

const cliVersion = "0.1.0"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "错误:", err)
		os.Exit(1)
	}
}

type globalOpts struct {
	server  string
	token   string
	json    bool
	timeout time.Duration
}

func (g *globalOpts) bind(fs *flag.FlagSet) {
	fs.StringVar(&g.server, "server", "", "平台地址（默认读 CI_SERVER 或本地配置）")
	fs.StringVar(&g.token, "token", "", "访问令牌（默认读 CI_TOKEN 或本地配置）")
	fs.BoolVar(&g.json, "json", false, "输出原始 JSON")
	fs.DurationVar(&g.timeout, "timeout", 60*time.Second, "单次请求超时")
}

func (g *globalOpts) resolve() (string, string, error) {
	cfg, err := loadConfig()
	if err != nil {
		return "", "", err
	}
	server := strings.TrimSpace(g.server)
	if server == "" {
		server = strings.TrimSpace(os.Getenv("CI_SERVER"))
	}
	if server == "" {
		server = strings.TrimSpace(cfg.Server)
	}
	token := strings.TrimSpace(g.token)
	if token == "" {
		token = strings.TrimSpace(os.Getenv("CI_TOKEN"))
	}
	if token == "" {
		token = strings.TrimSpace(cfg.Token)
	}
	return server, token, nil
}

func (g *globalOpts) client() (*client, error) {
	server, token, err := g.resolve()
	if err != nil {
		return nil, err
	}
	return newClient(server, token, g.timeout)
}

func run(args []string) error {
	if len(args) == 0 {
		usage()
		return nil
	}
	switch args[0] {
	case "-h", "--help", "help":
		usage()
		return nil
	case "version", "--version", "-v":
		fmt.Println("ci", cliVersion)
		return nil
	case "login":
		return runLogin(args[1:])
	case "list", "ls":
		return runList(args[1:])
	case "build", "trigger":
		return runBuild(args[1:])
	case "status":
		return runStatus(args[1:])
	case "logs", "log":
		return runLogs(args[1:])
	case "builds":
		return runBuilds(args[1:])
	case "cancel":
		return runCancel(args[1:])
	case "token":
		return runToken(args[1:])
	case "dashboard":
		return runDashboard(args[1:])
	default:
		usage()
		return fmt.Errorf("未知命令: %s", args[0])
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `ci-platform CLI

用法:
  ci login --server URL [--token TOKEN | --username USER --password PASS]
  ci list
  ci build <任务名或ID> [-param k=v ...] [--wait] [--idempotency-key KEY]
  ci status <任务名或ID> <构建号>
  ci logs <任务名或ID> <构建号> [--tail N] [--grep 关键字]
  ci builds <任务名或ID>
  ci cancel <任务名或ID> <构建号>
  ci token list
  ci token create --name NAME [--days N] [--scope job.read --scope job.build]
  ci token revoke <令牌ID>
  ci dashboard
  ci version

全局参数:
  --server URL       平台地址（也可用 CI_SERVER）
  --token TOKEN      访问令牌（也可用 CI_TOKEN；建议使用 API Token）
  --json             机器可读输出
  --timeout 60s      请求超时

配置文件默认在用户配置目录 ci-platform/config.json（0600）。
`)
}

func runLogin(args []string) error {
	fs := flag.NewFlagSet("login", flag.ContinueOnError)
	var g globalOpts
	g.bind(fs)
	username := fs.String("username", "", "登录用户名")
	password := fs.String("password", "", "登录密码（不传则交互输入）")
	if err := fs.Parse(args); err != nil {
		return err
	}
	server, _, err := g.resolve()
	if err != nil {
		return err
	}
	if server == "" {
		server, err = readLine("平台地址（如 http://127.0.0.1:8080）: ")
		if err != nil {
			return err
		}
	}
	c, err := newClient(server, g.token, g.timeout)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), g.timeout)
	defer cancel()

	tokenValue := strings.TrimSpace(g.token)
	if tokenValue != "" {
		user, err := c.me(ctx)
		if err != nil {
			return err
		}
		if err := saveConfig(cliConfig{Server: c.base, Token: tokenValue}); err != nil {
			return err
		}
		fmt.Printf("已保存 %s 的访问令牌到 %s\n", user, configHint())
		return nil
	}

	if strings.TrimSpace(*username) == "" {
		*username, err = readLine("用户名: ")
		if err != nil {
			return err
		}
	}
	if *password == "" {
		*password, err = readLine("密码: ")
		if err != nil {
			return err
		}
	}
	session, expiresAt, err := c.login(ctx, strings.TrimSpace(*username), *password)
	if err != nil {
		return err
	}
	if err := saveConfig(cliConfig{Server: c.base, Token: session}); err != nil {
		return err
	}
	fmt.Printf("已登录 %s，会话有效期至 %s\n", strings.TrimSpace(*username), expiresAt)
	fmt.Printf("配置已保存到 %s；长期脚本建议改用 API Token\n", configHint())
	return nil
}

func configHint() string {
	p, err := configPath()
	if err != nil {
		return "用户配置目录"
	}
	return p
}

func runList(args []string) error {
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	var g globalOpts
	g.bind(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	c, err := g.client()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), g.timeout)
	defer cancel()
	items, err := c.listPipelines(ctx)
	if err != nil {
		return err
	}
	if g.json {
		return printJSON(items)
	}
	if len(items) == 0 {
		fmt.Println("没有可见任务")
		return nil
	}
	for _, item := range items {
		status := "启用"
		if item.Disabled {
			status = "禁用"
		}
		branch := item.GitBranch
		if branch == "" {
			branch = "-"
		}
		fmt.Printf("%-24s %-10s %-8s %s\n", item.ID, branch, status, item.Name)
	}
	return nil
}

func runBuild(args []string) error {
	fs := flag.NewFlagSet("build", flag.ContinueOnError)
	var g globalOpts
	g.bind(fs)
	wait := fs.Bool("wait", false, "等待构建结束")
	waitTimeout := fs.Duration("wait-timeout", 30*time.Minute, "等待构建结束的超时")
	idem := fs.String("idempotency-key", "", "幂等键：重试不会创建重复构建")
	params := kvFlags{}
	fs.Var(params, "param", "构建参数 k=v，可重复")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("用法: ci build <任务名或ID> [-param k=v ...] [--wait]")
	}
	c, err := g.client()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), g.timeout)
	defer cancel()
	p, err := c.resolvePipeline(ctx, fs.Arg(0))
	if err != nil {
		return err
	}
	b, err := c.trigger(ctx, p.ID, params, strings.TrimSpace(*idem))
	if err != nil {
		return err
	}
	if g.json {
		if err := printJSON(b); err != nil {
			return err
		}
	} else {
		fmt.Printf("已触发 %s #%s（%s）\n", p.Name, b.ID, b.Status)
	}
	if !*wait {
		return nil
	}
	wctx, wcancel := context.WithTimeout(context.Background(), *waitTimeout)
	defer wcancel()
	final, err := c.waitBuild(wctx, p.ID, b.ID, 2*time.Second)
	if err != nil {
		return err
	}
	if g.json {
		return printJSON(final)
	}
	fmt.Printf("构建 #%s 结束: %s (exit=%d)\n", final.ID, final.Status, final.ExitCode)
	return nil
}

func runStatus(args []string) error {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	var g globalOpts
	g.bind(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 2 {
		return errors.New("用法: ci status <任务名或ID> <构建号>")
	}
	c, err := g.client()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), g.timeout)
	defer cancel()
	p, err := c.resolvePipeline(ctx, fs.Arg(0))
	if err != nil {
		return err
	}
	b, err := c.getBuild(ctx, p.ID, fs.Arg(1))
	if err != nil {
		return err
	}
	if g.json {
		return printJSON(b)
	}
	fmt.Printf("%s #%s: %s (exit=%d, branch=%s, started=%s, finished=%s)\n",
		p.Name, b.ID, b.Status, b.ExitCode, b.Branch, b.StartedAt, b.FinishedAt)
	return nil
}

func runLogs(args []string) error {
	fs := flag.NewFlagSet("logs", flag.ContinueOnError)
	var g globalOpts
	g.bind(fs)
	tail := fs.Int("tail", 0, "只取最近 N 行（上限 5000）")
	grep := fs.String("grep", "", "只保留包含关键字的行")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 2 {
		return errors.New("用法: ci logs <任务名或ID> <构建号> [--tail N] [--grep 关键字]")
	}
	c, err := g.client()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), g.timeout)
	defer cancel()
	p, err := c.resolvePipeline(ctx, fs.Arg(0))
	if err != nil {
		return err
	}
	out, err := c.logs(ctx, p.ID, fs.Arg(1), *tail, strings.TrimSpace(*grep))
	if err != nil {
		return err
	}
	if g.json {
		return printJSON(out)
	}
	fmt.Print(out.Log)
	if !strings.HasSuffix(out.Log, "\n") {
		fmt.Println()
	}
	return nil
}

func runBuilds(args []string) error {
	fs := flag.NewFlagSet("builds", flag.ContinueOnError)
	var g globalOpts
	g.bind(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("用法: ci builds <任务名或ID>")
	}
	c, err := g.client()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), g.timeout)
	defer cancel()
	p, err := c.resolvePipeline(ctx, fs.Arg(0))
	if err != nil {
		return err
	}
	items, err := c.listBuilds(ctx, p.ID)
	if err != nil {
		return err
	}
	if g.json {
		return printJSON(items)
	}
	for _, b := range items {
		fmt.Printf("#%-6s %-16s branch=%-16s started=%s\n", b.ID, b.Status, b.Branch, b.StartedAt)
	}
	return nil
}

func runCancel(args []string) error {
	fs := flag.NewFlagSet("cancel", flag.ContinueOnError)
	var g globalOpts
	g.bind(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 2 {
		return errors.New("用法: ci cancel <任务名或ID> <构建号>")
	}
	c, err := g.client()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), g.timeout)
	defer cancel()
	p, err := c.resolvePipeline(ctx, fs.Arg(0))
	if err != nil {
		return err
	}
	if err := c.cancel(ctx, p.ID, fs.Arg(1)); err != nil {
		return err
	}
	fmt.Printf("已请求取消 %s #%s\n", p.Name, fs.Arg(1))
	return nil
}

func runToken(args []string) error {
	if len(args) == 0 {
		return errors.New("用法: ci token list | create | revoke")
	}
	switch args[0] {
	case "list", "ls":
		return runTokenList(args[1:])
	case "create":
		return runTokenCreate(args[1:])
	case "revoke", "delete":
		return runTokenRevoke(args[1:])
	default:
		return fmt.Errorf("未知 token 子命令: %s", args[0])
	}
}

func runTokenList(args []string) error {
	fs := flag.NewFlagSet("token list", flag.ContinueOnError)
	var g globalOpts
	g.bind(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	c, err := g.client()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), g.timeout)
	defer cancel()
	items, err := c.listTokens(ctx)
	if err != nil {
		return err
	}
	if g.json {
		return printJSON(items)
	}
	for _, t := range items {
		scopes := strings.Join(t.Scopes, ",")
		if scopes == "" {
			scopes = "继承全部"
		}
		fmt.Printf("%-24s %-10s %-28s %s\n", t.ID, t.Prefix, scopes, t.Name)
	}
	return nil
}

func runTokenCreate(args []string) error {
	fs := flag.NewFlagSet("token create", flag.ContinueOnError)
	var g globalOpts
	g.bind(fs)
	name := fs.String("name", "", "令牌名称")
	days := fs.Int("days", 90, "有效期天数，0 表示永不过期")
	scopes := stringList{}
	fs.Var(&scopes, "scope", "权限范围，可重复；不传表示全部权限")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*name) == "" {
		return errors.New("必须提供 --name")
	}
	c, err := g.client()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), g.timeout)
	defer cancel()
	out, err := c.createToken(ctx, strings.TrimSpace(*name), *days, scopes)
	if err != nil {
		return err
	}
	if g.json {
		return printJSON(out)
	}
	fmt.Printf("已创建令牌 %s（%s）\n", out.Token.Name, out.Token.ID)
	fmt.Printf("Secret（只显示一次，请立即保存）:\n%s\n", out.Secret)
	return nil
}

func runTokenRevoke(args []string) error {
	fs := flag.NewFlagSet("token revoke", flag.ContinueOnError)
	var g globalOpts
	g.bind(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("用法: ci token revoke <令牌ID>")
	}
	c, err := g.client()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), g.timeout)
	defer cancel()
	if err := c.revokeToken(ctx, fs.Arg(0)); err != nil {
		return err
	}
	fmt.Printf("已吊销令牌 %s\n", fs.Arg(0))
	return nil
}

func runDashboard(args []string) error {
	fs := flag.NewFlagSet("dashboard", flag.ContinueOnError)
	var g globalOpts
	g.bind(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	c, err := g.client()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), g.timeout)
	defer cancel()
	var out any
	if err := c.do(ctx, "GET", "/api/dashboard", nil, &out); err != nil {
		return err
	}
	return printJSON(out)
}

func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

var stdinReader = bufio.NewReader(os.Stdin)

func readLine(prompt string) (string, error) {
	fmt.Fprint(os.Stderr, prompt)
	// 复用同一个 reader：连续读用户名/密码时，第一次 ReadString 可能已经
	// 把后续行缓冲进来；每次 new reader 会丢数据，管道输入也会在 EOF 时失败。
	line, err := stdinReader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, os.ErrClosed) {
		return "", err
	}
	if line == "" && err != nil {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

// kvFlags 支持重复的 -param k=v。
type kvFlags map[string]string

func (k kvFlags) String() string {
	if len(k) == 0 {
		return ""
	}
	parts := make([]string, 0, len(k))
	for key, value := range k {
		parts = append(parts, key+"="+value)
	}
	return strings.Join(parts, ",")
}

func (k kvFlags) Set(v string) error {
	key, value, ok := strings.Cut(v, "=")
	if !ok || strings.TrimSpace(key) == "" {
		return fmt.Errorf("参数必须是 k=v: %s", v)
	}
	k[strings.TrimSpace(key)] = value
	return nil
}

// stringList 支持重复的 -scope。
type stringList []string

func (s *stringList) String() string { return strings.Join(*s, ",") }
func (s *stringList) Set(v string) error {
	v = strings.TrimSpace(v)
	if v == "" {
		return errors.New("scope 不能为空")
	}
	*s = append(*s, v)
	return nil
}

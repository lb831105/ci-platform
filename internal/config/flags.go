package config

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
)

// ErrHelp 表示用户显式请求打印帮助，调用方应正常退出（退出码 0），
// 而不是当成错误处理。
var ErrHelp = errors.New("help requested")

// portFlag 是 -port 的专用载体：它不直接映射到某个字段，
// 而是在解析完成后覆盖 Addr 的端口部分，保留主机名。
type portFlag struct {
	value int // 0 表示未指定
}

func (p *portFlag) String() string {
	if p.value == 0 {
		return ""
	}
	return fmt.Sprintf("%d", p.value)
}

func (p *portFlag) Set(s string) error {
	var n int
	if _, err := fmt.Sscanf(s, "%d", &n); err != nil {
		return fmt.Errorf("invalid port %q: %w", s, err)
	}
	if n < 1 || n > 65535 {
		return fmt.Errorf("port %d out of range 1-65535", n)
	}
	p.value = n
	return nil
}

// ParseArgs 用命令行参数覆盖「环境变量装载 + 内置默认值」得到的配置。
//
// 优先级（高 → 低）：命令行参数 > 环境变量 > 内置默认值。
//
// 常用形式：
//
//	./ci-platform -port 9090                     # 最简单：只换端口
//	./ci-platform -addr 0.0.0.0:9090             # 显式指定监听地址
//	./ci-platform -addr 10.0.0.1:8080 -port 9000 # -port 覆盖 -addr 中的端口
//
// 固定字段上的 -h/--help 只打印帮助；调用方收到 ErrHelp 后应直接返回。
func (c *Config) ParseArgs(args []string) error {
	fs := flag.NewFlagSet("ci-platform", flag.ContinueOnError)
	fs.SetOutput(os.Stdout)

	fs.StringVar(&c.Addr, "addr", c.Addr, "HTTP 监听地址，如 :8080 / 0.0.0.0:9090 / 127.0.0.1:8080")
	fs.StringVar(&c.DataHome, "data-home", c.DataHome, "运行时数据目录（文件即数据库）")
	fs.StringVar(&c.ConfigFile, "config", c.ConfigFile, "引导配置文件路径（记录数据目录，重启生效）")
	fs.StringVar(&c.WebDir, "web-dir", c.WebDir, "前端静态资源的磁盘回退目录（二进制内嵌资源可用时不生效）")
	fs.IntVar(&c.Workers, "workers", c.Workers, "构建 worker 并发数")
	fs.IntVar(&c.QueueSize, "queue", c.QueueSize, "构建队列容量")
	fs.IntVar(&c.LogBufferSize, "log-buffer", c.LogBufferSize, "单条 WebSocket 连接的日志发送缓冲行数")
	fs.StringVar(&c.LogLevel, "log-level", c.LogLevel, "平台日志级别：debug / info / warn / error")
	fs.StringVar(&c.ToolsMirror, "tools-mirror", c.ToolsMirror, "自动安装工具的下载镜像前缀，如 https://mirrors.example.com（空 = 直连官方）")
	fs.IntVar(&c.LogRetainDays, "log-retain-days", c.LogRetainDays, "平台日志保留天数：一天一个文件，超过天数的文件自动清理（0 = 永久保留）")
	corsOrigins := strings.Join(c.CORSOrigins, ",")
	fs.StringVar(&corsOrigins, "cors-origins", corsOrigins, "允许跨域来源，逗号分隔；* 表示开放")
	fs.DurationVar(&c.ShutdownTimeout, "shutdown-timeout", c.ShutdownTimeout, "优雅停机等待运行中构建的最长时间")
	fs.BoolVar(&c.TrustProxy, "trust-proxy", c.TrustProxy, "信任反向代理的 X-Forwarded-For（仅当服务不对公网直连时开启）")
	fs.DurationVar(&c.WorkspaceTTL, "workspace-ttl", c.WorkspaceTTL, "构建工作区保留时长，如 60m")
	fs.DurationVar(&c.CleanInterval, "clean-interval", c.CleanInterval, "工作区过期扫描间隔，如 5m")
	fs.BoolVar(&c.AuthEnabled, "auth", c.AuthEnabled, "是否开启登录校验")
	fs.StringVar(&c.AuthUser, "auth-user", c.AuthUser, "管理员用户名")
	fs.StringVar(&c.AuthPassword, "auth-password", c.AuthPassword, "管理员初始密码（仅首次初始化生效，之后在「个人资料 → 修改密码」修改）")
	fs.DurationVar(&c.AuthTokenTTL, "auth-ttl", c.AuthTokenTTL, "登录会话有效期，如 12h")
	fs.BoolVar(&c.ShowVersion, "version", false, "打印版本信息后退出")

	noAuth := false
	fs.BoolVar(&noAuth, "no-auth", false, "关闭登录校验（等价于 -auth=false，优先于 -auth）")

	port := &portFlag{}
	fs.Var(port, "port", "监听端口（等价于只替换 -addr 的端口部分，优先于 -addr 中的端口）")
	fs.Var(port, "p", "等价于 -port")

	fs.Usage = func() {
		out := fs.Output()
		fmt.Fprintf(out, "ci-platform —— 轻量 CI/CD 平台（Go + 本地工具链 + 纯文件系统存储）\n\n")
		fmt.Fprintf(out, "用法:\n  %s [参数]\n\n", fs.Name())
		fmt.Fprintf(out, "参数:\n")
		fs.PrintDefaults()
		fmt.Fprintf(out, "\n示例:\n")
		fmt.Fprintf(out, "  %s -port 9090\n", fs.Name())
		fmt.Fprintf(out, "  %s -addr 0.0.0.0:9090 -data-home D:\\ci-data\n", fs.Name())
		fmt.Fprintf(out, "\n相同参数也可用环境变量提供，命令行优先级更高：\n")
		fmt.Fprintf(out, "  CI_ADDR / CI_DATA_HOME / CI_WEB_DIR / CI_WORKERS / CI_QUEUE_SIZE\n")
		fmt.Fprintf(out, "  CI_CONFIG_FILE（引导配置文件，默认 ci-platform.json）\n")
		fmt.Fprintf(out, "  CI_WORKSPACE_TTL_MINUTES / CI_CLEAN_INTERVAL_MINUTES / CI_LOG_BUFFER\n")
		fmt.Fprintf(out, "  CI_AUTH_ENABLED / CI_AUTH_USER / CI_AUTH_PASSWORD / CI_AUTH_TOKEN_TTL_MINUTES\n")
	}

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return ErrHelp
		}
		return fmt.Errorf("parse flags: %w", err)
	}

	if rest := fs.Args(); len(rest) > 0 {
		return fmt.Errorf("unexpected positional arguments: %s", strings.Join(rest, " "))
	}

	// 记录 data-home 是否由命令行显式指定：显式指定时优先于引导配置文件。
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "data-home" {
			c.DataHomeSet = true
		}
	})

	if port.value != 0 {
		c.Addr = applyPort(c.Addr, port.value)
	}
	if trimmed := strings.TrimSpace(corsOrigins); trimmed != "" {
		parts := strings.Split(trimmed, ",")
		c.CORSOrigins = c.CORSOrigins[:0]
		for _, part := range parts {
			if part = strings.TrimSpace(part); part != "" {
				c.CORSOrigins = append(c.CORSOrigins, part)
			}
		}
	}
	if noAuth {
		c.AuthEnabled = false
	}
	if strings.TrimSpace(c.Addr) == "" {
		return errors.New("empty listen address")
	}
	return nil
}

// applyPort 用 port 覆盖 addr 的端口部分并保留主机名。
// addr 形如 ":8080"、"0.0.0.0:9090"、"127.0.0.1:8080"。
func applyPort(addr string, port int) string {
	host := ""
	if i := strings.LastIndex(addr, ":"); i >= 0 {
		host = addr[:i]
	}
	return fmt.Sprintf("%s:%d", host, port)
}

// ListenURL 返回便于在终端/浏览器中直接使用的访问地址。
// 监听 0.0.0.0 / :: 时用 localhost 代替，避免给出不可点击的地址。
func (c *Config) ListenURL() string {
	host, port := splitHostPort(c.Addr)
	switch host {
	case "", "0.0.0.0", "::", "[::]":
		host = "localhost"
	}
	if strings.Contains(host, ":") && !strings.HasPrefix(host, "[") {
		host = "[" + host + "]"
	}
	return fmt.Sprintf("http://%s:%s/", host, port)
}

func splitHostPort(addr string) (string, string) {
	i := strings.LastIndex(addr, ":")
	if i < 0 {
		return "", addr
	}
	return addr[:i], addr[i+1:]
}

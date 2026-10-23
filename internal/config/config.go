package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Config 是进程级装配参数：先由环境变量（带默认值）装载，
// 再由命令行参数覆盖（见 flags.go，优先级：命令行 > 环境变量 > 默认值）。
type Config struct {
	Addr            string        // HTTP 监听地址
	DataHome        string        // 运行时数据目录（替代数据库）
	ConfigFile      string        // 引导配置文件路径（记录数据目录，独立于 data）
	DataHomeSet     bool          // 数据目录是否由环境变量/命令行显式指定（优先级高于引导文件）
	WebDir          string        // 前端静态资源「磁盘回退」目录（生产形态优先使用内嵌资源）
	Workers         int           // 构建 worker 数量
	QueueSize       int           // 构建队列容量
	WorkspaceTTL    time.Duration // 工作区保留时长
	CleanInterval   time.Duration // 工作区清理扫描间隔
	LogBufferSize   int           // 单连接日志发送缓冲行数
	CORSOrigins     []string      // 允许跨域来源；空或 * 表示开放（生产应配置精确域名）
	TrustProxy      bool          // 是否信任反向代理写入的 X-Forwarded-For（用于限流取真实 IP）
	ShutdownTimeout time.Duration // 优雅停机等待运行中构建的最长时间

	// 认证配置：基础版单管理员账号 + 内存会话。
	AuthEnabled  bool          // 是否开启登录校验
	AuthUser     string        // 管理员用户名
	AuthPassword string        // 管理员密码
	AuthTokenTTL time.Duration // 登录会话有效期

	// ShowVersion 不是运行配置，而是「-version」这样的启动期一次性动作标记，
	// 由 main 读取后打印版本并立即退出。
	ShowVersion bool
}

// Load 从环境变量装载配置。
func Load() *Config {
	return &Config{
		Addr:            env("CI_ADDR", ":8080"),
		DataHome:        env("CI_DATA_HOME", "./data"),
		ConfigFile:      env("CI_CONFIG_FILE", "ci-platform.json"),
		DataHomeSet:     os.Getenv("CI_DATA_HOME") != "",
		WebDir:          env("CI_WEB_DIR", "./web/dist"),
		Workers:         envInt("CI_WORKERS", 3),
		QueueSize:       envInt("CI_QUEUE_SIZE", 100),
		WorkspaceTTL:    time.Duration(envInt("CI_WORKSPACE_TTL_MINUTES", 60)) * time.Minute,
		CleanInterval:   time.Duration(envInt("CI_CLEAN_INTERVAL_MINUTES", 5)) * time.Minute,
		LogBufferSize:   envInt("CI_LOG_BUFFER", 512),
		CORSOrigins:     envList("CI_CORS_ORIGINS", []string{"*"}),
		TrustProxy:      envBool("CI_TRUST_PROXY", false),
		ShutdownTimeout: time.Duration(envInt("CI_SHUTDOWN_TIMEOUT_SECONDS", 30)) * time.Second,

		AuthEnabled:  envBool("CI_AUTH_ENABLED", true),
		AuthUser:     env("CI_AUTH_USER", "admin"),
		AuthPassword: env("CI_AUTH_PASSWORD", "admin"),
		AuthTokenTTL: time.Duration(envInt("CI_AUTH_TOKEN_TTL_MINUTES", 720)) * time.Minute,
	}
}

// Validate 在启动早期拒绝明显错误的参数，避免服务带病运行。
func (c *Config) Validate() error {
	if strings.TrimSpace(c.DataHome) == "" {
		return fmt.Errorf("数据目录不能为空")
	}
	if c.Workers < 1 {
		return fmt.Errorf("workers 必须大于 0（当前 %d）", c.Workers)
	}
	if c.QueueSize < 1 {
		return fmt.Errorf("queue size 必须大于 0（当前 %d）", c.QueueSize)
	}
	if c.WorkspaceTTL <= 0 {
		return fmt.Errorf("workspace TTL 必须大于 0")
	}
	if c.CleanInterval <= 0 {
		return fmt.Errorf("clean interval 必须大于 0")
	}
	if c.AuthTokenTTL <= 0 {
		return fmt.Errorf("auth token TTL 必须大于 0")
	}
	if c.ShutdownTimeout < 0 {
		return fmt.Errorf("shutdown timeout 不能为负数")
	}
	if c.LogBufferSize < 0 {
		return fmt.Errorf("log buffer 不能为负数")
	}
	return nil
}

// JobsDir 返回 pipeline 存储根目录 data/jobs。
func (c *Config) JobsDir() string {
	return filepath.Join(c.DataHome, "jobs")
}

// WorkspaceDir 返回临时工作区根目录 data/workspace。
func (c *Config) WorkspaceDir() string {
	return filepath.Join(c.DataHome, "workspace")
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// envList 解析逗号分隔的字符串列表。
func envList(key string, def []string) []string {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return def
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	if len(out) == 0 {
		return def
	}
	return out
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

// envBool 解析布尔型环境变量，1/true/yes/on 视为 true，其余回退默认值。
func envBool(key string, def bool) bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	if v == "" {
		return def
	}
	switch v {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	}
	return def
}

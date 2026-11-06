# 环境变量

[← 返回 README](../README.md)

| 变量 | 默认值 | 说明 |
|---|---|---|
| `CI_ADDR` | `:8080` | HTTP 监听地址 |
| `CI_DATA_HOME` | `./data` | 运行时数据目录（替代数据库） |
| `CI_CONFIG_FILE` | `ci-platform.json` | 引导配置文件路径（记录数据目录，重启生效） |
| `CI_WEB_DIR` | `./web/dist` | 前端资源**磁盘回退**目录（仅当二进制未内嵌有效资源时使用） |
| `CI_WORKERS` | `3` | 构建 worker 数量 |
| `CI_QUEUE_SIZE` | `100` | 构建队列容量（满则返回业务错误） |
| `CI_WORKSPACE_TTL_MINUTES` | `60` | 已删除任务的孤儿工作区保留时长（活动任务的工作区不清理） |
| `CI_CLEAN_INTERVAL_MINUTES` | `5` | 孤儿工作区清理扫描间隔 |
| `CI_LOG_BUFFER` | `512` | 单 WebSocket 连接日志缓冲行数 |
| `CI_AUTH_ENABLED` | `true` | 是否开启登录校验（`false` 关闭） |
| `CI_AUTH_USER` | `admin` | 管理员用户名 |
| `CI_AUTH_PASSWORD` | 随机生成 | 管理员初始密码；未设置时首次启动随机生成并在日志输出一次 |
| `CI_AUTH_TOKEN_TTL_MINUTES` | `720` | 登录会话有效期（分钟），会话存内存、重启失效 |
| `CI_CORS_ORIGINS` | `*` | 允许跨域与 WebSocket 的来源，逗号分隔；生产建议配置精确域名 |
| `CI_TRUST_PROXY` | `false` | 是否信任反向代理的 `X-Forwarded-For`（服务不直接暴露公网时开启，登录/Webhook 限流取真实 IP） |
| `CI_SHUTDOWN_TIMEOUT_SECONDS` | `30` | 优雅停机时等待运行中构建的最长时间（也可用 `-shutdown-timeout`） |

---

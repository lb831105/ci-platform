# API 与 CLI

[← 返回 README](../README.md)

统一响应体：`{ "code": 0, "msg": "ok", "data": ... }`（HTTP 状态码恒为 200，业务态由 `code` 表达）。

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/healthz` | 进程存活探针（不返回业务数据） |
| GET | `/readyz` | 数据目录与凭据密钥就绪探针（未就绪返回 503） |
| GET | `/metrics` | Prometheus 聚合指标（构建/成功率/队列/部署；不含任务名与密钥） |
| GET | `/openapi.json` | OpenAPI 3.0 契约（同时提供 `/api/openapi.json`；随二进制内嵌） |
| GET | `/api/auth/status` | 认证开关与当前登录态（公开） |
| POST | `/api/auth/login` | 登录，返回 Bearer token（公开） |
| POST | `/api/auth/logout` | 登出 |
| GET | `/api/auth/me` | 当前用户 |
| POST | `/api/auth/password` | 修改**当前登录账号**的密码（校验旧密码；内置管理员与平台内用户都走这里，改密后其他会话失效） |
| GET | `/api/overview` | 带构建聚合的任务列表（Jenkins 作业列表） |
| GET | `/api/dashboard` | 首页仪表盘聚合：今日构建/成功率/平均耗时/队列/部署/失败 Top（按当前用户可见任务） |
| GET | `/api/builds` | 跨任务构建历史；支持 `q` / `status` / `pipeline` / `limit` / `offset`，总数在 `X-Total-Count` 响应头 |
| GET | `/api/pipelines` | 任务列表 |
| POST | `/api/pipelines` | 创建任务 |
| GET | `/api/pipelines/{pid}` | 任务详情 |
| PUT | `/api/pipelines/{pid}` | 更新任务 |
| DELETE | `/api/pipelines/{pid}` | 删除任务（递归删目录） |
| POST | `/api/pipelines/{pid}/build` | 手动触发；body 可带 `{"params":{"VERSION":"1.2.3"}}`（参数化构建），body 可省略；可带 `Idempotency-Key` 请求头或 body `idempotency_key`，同一用户/任务的重试复用第一次构建 |
| POST | `/api/pipelines/{pid}/rename` | 重命名任务（受工程命名限制约束） |
| POST | `/api/webhook/{pipeline_id}` | Webhook 触发：密钥兼容 `X-Webhook-Secret` / GitLab `X-Gitlab-Token` / GitHub `X-Hub-Signature-256`；按**事件与分支白名单**过滤、`[ci skip]` 跳过、按投递 UUID **幂等去重**、按推送分支构建；查询串可覆盖已定义参数。**该接口返回真实 HTTP 状态码**（401/404/409/429），被策略忽略时返回 200 + `{ignored:true,reason}` |
| GET | `/api/pipelines/{pid}/builds` | 构建历史（遍历目录，编号倒序） |
| GET | `/api/pipelines/{pid}/builds/{bid}` | 构建详情（含 Revision、Changes、备注、保留标记） |
| PUT | `/api/pipelines/{pid}/builds/{bid}` | 更新构建备注 / `keep_forever`（权限 `job.configure`） |
| GET | `/api/pipelines/{pid}/builds/{bid}/log` | 拉取日志；可选 `tail=N`（最近 N 行，上限 5000）与 `grep=关键字` |
| GET | `/api/pipelines/{pid}/builds/{bid}/log/download` | 下载日志纯文本附件 |
| GET | `/api/pipelines/{pid}/builds/{bid}/artifacts` | 构建产物清单 |
| GET | `/api/pipelines/{pid}/builds/{bid}/artifacts/download?file=...` | 下载单个产物（二进制） |
| GET | `/api/pipelines/{pid}/builds/{bid}/artifacts.zip` | 下载全部产物 ZIP |
| POST | `/api/pipelines/{pid}/builds/{bid}/republish` | 把该次构建的产物直接重新发布到 SSH（不重新编译）；body `{"server_id":"...","publish":"目标名"}`，`publish` 为空取第一个启用目标 |
| POST | `/api/pipelines/{pid}/builds/{bid}/rerun` | 用该次构建的参数快照重新触发；密码参数由服务端从加密快照还原，不经过前端 |
| POST | `/api/pipelines/{pid}/builds/{bid}/artifacts/promote` | 跨任务产物晋级：把源构建产物复制到目标构建（`bid=latest` 取最近成功构建），复制时校验源清单 SHA-256；body `{"source_pipeline_id":"...","source_build_id":"...","prefix":"","files":[],"overwrite":false}` |
| POST | `/api/pipelines/{pid}/builds/{bid}/cancel` | 取消运行中/等待中的构建（状态落 `aborted`） |
| GET | `/api/views` | 视图列表（列表视图 / 我的视图） |
| GET | `/api/views/{name}/jobs` | 视图内任务（按当前用户权限过滤） |
| POST/PUT/DELETE | `/api/views` `/api/views/{name}` | 新建 / 编辑 / 删除视图（`data/views.json`） |
| DELETE | `/api/pipelines/{pid}/builds/{bid}` | 删除单次构建（连 `builds/{bid}` 目录：日志 + 产物，权限 `job.delete`） |
| WS | `/ws/pipelines/{pid}/builds/{bid}/logs` | 实时日志流（token 查询参数鉴权） |
| GET/PUT | `/api/system/settings` | 系统配置读取 / 更新（含 `log_max_mb` 日志上限、保留策略定期扫描） |
| GET/PUT | `/api/system/events` | 全局出站事件订阅：列表（URL/Secret 脱敏）/ 全量保存；URL 与 HMAC 密钥加密落盘，留空表示沿用 |
| POST | `/api/system/events/test` | 向指定订阅（body `{"id":"..."}`，为空则全部启用项）发送测试事件 |
| GET | `/api/pipelines/{pid}/config.json` | **导出任务配置**（对齐 Jenkins `job/{name}/config.xml`）：返回裸 JSON 文档（不套 `{code,msg,data}`），**不含任何密钥**，集成 `server_name` / `git_credential_name` 便于跨实例对齐 |
| POST | `/api/system/import/config` | **导入平台配置文档**：body 为导出文档或原生 `config.json`；query `name`/`mode=create\|update\|skip`/`dry_run=true`（管理员） |
| POST | `/api/system/import/jenkins` | **从 Jenkins 导入任务**：body 为 `config.xml`（`application/xml`）或 multipart（`file`）；query `name`（必填，config.xml 不含任务名）、`mode=create\|update\|skip`、`dry_run=true`、`keep_source=false`（管理员） |
| POST | `/api/system/email/test` | 发送测试邮件（管理员） |
| POST | `/api/system/webhook/test` | 发送 Webhook 测试通知（钉钉/企微/Slack，管理员） |
| GET | `/api/notify/contacts` | 平台用户联系方式（任务通知选人用，需任务配置权限） |
| POST | `/api/pipelines/{pid}/notify/test` | 向该任务配置的人员/群发送测试通知 |
| PUT | `/api/security/users/contact` | 设置用户姓名与通知联系方式（姓名仅汉字；邮箱/企微 ID/手机号，管理员） |
| GET | `/api/system/audit?limit=300` | 操作审计日志（最近 N 条，时间倒序，管理员） |
| GET | `/api/system/notify?limit=&pipeline=&recipient=&status=` | 通知投递记录（按收件人粒度，排查漏收） |
| GET/PUT | `/api/profile` `/api/profile/contact` | 个人资料：查看 / 自助维护联系方式（任何登录用户）；**修改密码**在「个人资料」页自助完成 |
| GET/POST/DELETE | `/api/profile/tokens` `/api/profile/tokens/{id}` | API 访问令牌：列表（脱敏）/ 创建（secret 只返回一次）/ 吊销；支持 `scopes` 最小权限，旧 Token 无 scopes 时兼容继承全部权限；Token 不能自助创建/吊销 Token |
| POST | `/api/profile/email/verify` `/api/profile/email/confirm` | 发送邮箱验证码 / 提交验证码确认 |
| GET | `/api/security/users` | 用户管理页数据：内置管理员 + 平台用户 + 联系方式 + 角色摘要 |
| POST | `/api/security/users` | 创建平台内用户（bcrypt；用户名 + 汉字姓名 + 邮箱，管理员） |
| POST | `/api/security/users/delete` | 删除用户及授权（管理员） |
| PUT | `/api/security/users/password` | 管理员重置用户密码（撤销会话） |
| POST | `/api/system/reload` | 从磁盘重新读取设置 |
| POST | `/api/system/quiet` | 静默模式（准备关机） |
| GET | `/api/system/info` `/load` `/log` `/features` | 系统信息 / 负载 / 日志 / 内置组件 |
| GET/POST | `/api/system/old-data` `/old-data/clean` | 扫描 / 清理旧数据 |
| GET/PUT | `/api/system/data-home` | 读取 / 修改数据目录（写入引导配置，重启生效；可迁移现有数据） |
| GET/POST/PUT/DELETE | `/api/credentials` | 凭据管理（密钥不回传） |
| GET | `/api/security/rbac` | 角色与授权数据（Manage and Assign Roles，仅 Administer） |
| GET | `/api/security/rbac/effective` | **生效预览**：每个主体最终可见的任务、每个角色覆盖的任务（后端权威计算，仅 Administer） |
| PUT | `/api/security/rbac/roles` | 替换全部角色（全局/任务角色 + 权限矩阵） |
| PUT | `/api/security/rbac/assignments` | 替换全部授权（用户/组 → 角色） |

**任务配置扩展**：新建任务支持 **Copy from**（从现有任务复制全部配置，对齐 Jenkins New Item）；
`cron_spec` 字段支持 5 字段 cron 定时构建（含 `H` 散列均衡），触发记录显示「定时触发」；
`notify` 支持人员/群/事件、值班轮值、升级、抑制窗口、分支路由与消息模板（详见[流水线与发布](pipeline.md#出站事件订阅)）。

```bash
# 手动触发
curl -X POST http://127.0.0.1:8080/api/pipelines/1703123456/build

# Webhook 触发

# 方式一：平台自定义 header（curl / CI 脚本）
curl -X POST http://[IP]:8080/api/webhook/1703123456 \
     -H "X-Webhook-Secret: abc123"

# 方式二：GitLab Webhook（项目 Settings → Webhooks，Secret token 填 abc123，
# GitLab 会以 X-Gitlab-Token 头发送；内网 HTTP 地址请关闭「SSL verification」）
curl -X POST http://[IP]:8080/api/webhook/1703123456
```

## OpenAPI 与 CLI

OpenAPI 3.0 契约随服务端内嵌：

```text
http://[IP]:8080/openapi.json
http://[IP]:8080/api/openapi.json
```

CLI 不依赖平台内部包，可单独编译分发：

```bash
go build -o bin/ci ./cmd/ci

# 交互登录（会话）或直接保存 API Token（长期脚本推荐）
./bin/ci login --server http://[IP]:8080 --username admin --password '***'
./bin/ci login --server http://[IP]:8080 --token cip_xxx

./bin/ci list
./bin/ci build demo-app --wait -param VERSION=1.2.3
./bin/ci logs demo-app 42 --tail 200 --grep ERROR
./bin/ci builds demo-app
./bin/ci status demo-app 42
./bin/ci cancel demo-app 42

# 令牌管理使用登录会话执行（API Token 不能自助创建新令牌）
./bin/ci token list
./bin/ci token create --name deploy --days 90 --scope job.read --scope job.build
```

连接信息也可用环境变量覆盖：`CI_SERVER`、`CI_TOKEN`；配置文件默认在用户配置目录
`ci-platform/config.json`，权限 `0600`，可用 `CI_CONFIG` 指定其他路径。

---

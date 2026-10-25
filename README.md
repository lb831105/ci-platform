# 轻量 CI/CD 平台（类 Jenkins 基础版）

文件系统存储 + 本地工具链构建 + SSH 发布，**不依赖任何数据库**，**前端内嵌、单文件交付**。

还原 Jenkins 最核心的用户心智：

```
任务(Job) → 触发(手动/Webhook/定时，可带构建参数) → 构建(服务器本地串行执行，步骤级超时/重试/允许失败/when) → 查看控制台输出 → 归档产物 → SSH 发布到一个或多个目标服务器
```

浏览器打开即用的 Web 管理界面（Vue3 + Element Plus）：任务列表、任务编辑（构建参数、步骤级控制、多目标 SSH 发布、产物归档、通知）、构建历史、实时控制台输出。

---

## 一、快速开始

### 方式 A：直接运行构建好的单文件（推荐）

前端已用 `go:embed` 内嵌进二进制，**部署时只需拷贝一个可执行文件**，无需 Node、无需静态资源目录。

```bash
# Linux
./dist/ci-platform-linux-amd64

# Windows
dist\ci-platform-windows-amd64.exe
```

默认监听 `:8080`，数据目录 `./data`。

数据目录（任务、构建记录、凭据、设置）可在**系统配置 → 主目录**中修改，修改后写入引导配置 `ci-platform.json`，**重启服务后生效**；保存时可选择把现有任务/凭据/设置迁移到新目录。也可在启动时通过 `-data-home` 或 `CI_DATA_HOME` 指定（优先级高于引导配置），或用 `-config` / `CI_CONFIG_FILE` 改变引导配置文件位置。
默认开启登录校验，初始账号 **`admin` / `admin`**，首次登录后请通过启动参数或环境变量修改：

```bash
./dist/ci-platform-linux-amd64 -auth-user ops -auth-password 'your-password'
# 等价环境变量：CI_AUTH_USER / CI_AUTH_PASSWORD
# 仅本地调试可关闭登录：-no-auth（或 CI_AUTH_ENABLED=false）
```

> 密码安全：管理员密码以 **bcrypt hash** 落盘（`auth_password_hash`），`data/settings.json` 中不再保存明文；
> 旧版明文配置在首次启动时自动迁移。新设置的密码至少 8 个字符；使用默认密码 `admin` 时启动日志会给出 WARN 提示。

浏览器访问 <http://127.0.0.1:8080>。

> **构建依赖服务器本地工具链**：平台直接在运行服务的机器上执行构建步骤，不使用 Docker。
> 请确保服务器已安装 Go / JDK / Maven / Node.js / Gradle 等所需工具，并在「系统管理 → 全局工具配置」
> 中填写工具主目录（或让工具已在系统 `PATH` 中）。

### 方式 B：一键构建（Windows 上通过 Git Bash 运行）

```bash
./build.sh                     # 前端构建 + 交叉编译 linux/amd64 与 windows/amd64 + UPX 压缩
```

或使用 Make：

```bash
make release                   # 等价于 ./build.sh
make release ARGS="--no-upx"   # 透传参数
```

### 方式 C：本地开发

```bash
# 终端 1：后端（默认 :8080）
go run ./cmd/server

# 终端 2：前端开发服务器，/api 与 /ws 自动代理到 :8080
cd web && npm install && npm run dev     # http://127.0.0.1:5173
```

> ⚠️ `go:embed` 在**编译期**要求 `web/dist/index.html` 存在。因此 clone 后若直接 `go build`，
> 需先执行 `cd web && npm install && npm run build`（仓库中已保留一份前端产物，通常开箱即可编译）。

---

## 二、构建脚本 `build.sh`

在 Windows 上通过 Git Bash 运行，也可直接在 Linux / macOS 运行。完整流程：

| 步骤 | 动作 |
|---|---|
| 1. 环境检查 | 校验 `go` / `node` / `npm` / `upx`，任一缺失给出明确提示（UPX 缺失自动降级跳过） |
| 2. 前端构建 | `node_modules` 缺失时自动 `npm ci`（无 lock 则 `npm install`），再执行 `npm run build` |
| 3. 内嵌校验 | 确认 `web/dist/index.html` 存在，否则中止并给出修复命令 |
| 4. 交叉编译 | `CGO_ENABLED=0 GOOS/GOARCH` + `-trimpath -ldflags "-s -w -X main.buildVersion=..."` |
| 5. UPX 压缩 | `upx --best --lzma` 逐产物压缩，打印压缩前后大小与压缩率 |
| 6. 结果汇总 | 输出产物表格与运行方式 |

### 参数

| 参数 | 说明 |
|---|---|
| `--platforms <列表>` | 目标平台，逗号分隔，默认 `linux/amd64,windows/amd64` |
| `--single` | 仅构建当前平台（本机快速验证） |
| `--skip-web` | 跳过前端构建（要求 `web/dist/index.html` 已存在） |
| `--no-upx` | 跳过 UPX 压缩 |
| `--upx-level <参数>` | UPX 级别，默认 `--best --lzma` |
| `-h, --help` | 帮助 |

```bash
# 三平台全量构建，不压缩
./build.sh --platforms linux/amd64,linux/arm64,windows/amd64 --no-upx

# 仅本机验证
./build.sh --single
```

### 实测构建结果

```
产物                                          压缩前    压缩后 压缩率
----------------------------------------------------------------------------------
ci-platform-linux-amd64                          11.62 MB      3.37 MB    71.0%
ci-platform-windows-amd64.exe                    12.10 MB      3.61 MB    70.2%
```

产物已用文件头校验：Linux 为 `ELF`（`7f454c46`），Windows 为 `MZ`（`4d5a9000`）。
经 UPX 压缩后的 Windows 产物已实测可正常启动并完整提供 Web 界面。

### 版本信息注入

脚本会把 `git describe`、短 commit、UTC 构建时间通过 `-ldflags -X` 注入，启动首行即可见：

```
ci-platform version=v1.0.0 commit=a1b2c3d built=2026-10-22T07:28:54Z
web assets source: embedded
ci-platform listening on :8080 (data_home=./data, workers=3, queue=100)
```

---

## 三、环境变量

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
| `CI_AUTH_PASSWORD` | `admin` | 管理员密码 |
| `CI_AUTH_TOKEN_TTL_MINUTES` | `720` | 登录会话有效期（分钟），会话存内存、重启失效 |
| `CI_CORS_ORIGINS` | `*` | 允许跨域与 WebSocket 的来源，逗号分隔；生产建议配置精确域名 |
| `CI_TRUST_PROXY` | `false` | 是否信任反向代理的 `X-Forwarded-For`（服务不直接暴露公网时开启，登录/Webhook 限流取真实 IP） |
| `CI_SHUTDOWN_TIMEOUT_SECONDS` | `30` | 优雅停机时等待运行中构建的最长时间（也可用 `-shutdown-timeout`） |

---

## 四、技术选型确认单

| 维度 | 最终选择 |
|---|---|
| 后端语言 | Go 1.24（`go.mod` 声明） |
| HTTP 框架 | `github.com/go-chi/chi/v5` |
| 存储 | **纯文件系统**（JSON + log.txt，原子写入）— **零数据库依赖** |
| 构建执行 | 本地 `os/exec`（服务器上的 shell 与工具链） |
| SSH 客户端 | `golang.org/x/crypto/ssh` |
| SCP/SFTP 传输 | `github.com/pkg/sftp` |
| WebSocket | `github.com/gorilla/websocket` |
| Git 操作 | `github.com/go-git/go-git/v5` |
| 前端框架 | Vue3 + TypeScript + Vite |
| UI 组件库 | Element Plus |
| 状态管理 | Pinia（setup store） |
| 前端交付 | `go:embed` 内嵌进二进制 + UPX 压缩 |

借鉴设计思路（未复制代码）：Woodpecker CI、Drone CI、Jenkins（`JENKINS_HOME/jobs/{job}/builds/{id}/` 布局与 Publish Over SSH 语义）。

---

## 五、API 约定

统一响应体：`{ "code": 0, "msg": "ok", "data": ... }`（HTTP 状态码恒为 200，业务态由 `code` 表达）。

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/healthz` | 进程存活探针（不返回业务数据） |
| GET | `/readyz` | 数据目录就绪探针（未就绪返回 503） |
| GET | `/api/auth/status` | 认证开关与当前登录态（公开） |
| POST | `/api/auth/login` | 登录，返回 Bearer token（公开） |
| POST | `/api/auth/logout` | 登出 |
| GET | `/api/auth/me` | 当前用户 |
| POST | `/api/auth/password` | 修改密码（校验旧密码） |
| GET | `/api/overview` | 带构建聚合的任务列表（Jenkins 作业列表） |
| GET | `/api/builds` | 跨任务构建历史 |
| GET | `/api/pipelines` | 任务列表 |
| POST | `/api/pipelines` | 创建任务 |
| GET | `/api/pipelines/{pid}` | 任务详情 |
| PUT | `/api/pipelines/{pid}` | 更新任务 |
| DELETE | `/api/pipelines/{pid}` | 删除任务（递归删目录） |
| POST | `/api/pipelines/{pid}/build` | 手动触发；body 可带 `{"params":{"VERSION":"1.2.3"}}`（参数化构建），body 可省略 |
| POST | `/api/pipelines/{pid}/rename` | 重命名任务（受工程命名限制约束） |
| POST | `/api/webhook/{pipeline_id}` | Webhook 触发（密钥兼容 `X-Webhook-Secret` / GitLab `X-Gitlab-Token` / GitHub `X-Hub-Signature-256`，任一匹配即通过）；查询串可覆盖已定义的构建参数（`?VERSION=1.2.3`） |
| GET | `/api/pipelines/{pid}/builds` | 构建历史（遍历目录，编号倒序） |
| GET | `/api/pipelines/{pid}/builds/{bid}` | 构建详情 |
| GET | `/api/pipelines/{pid}/builds/{bid}/log` | 拉取日志全文 |
| GET | `/api/pipelines/{pid}/builds/{bid}/artifacts` | 构建产物清单 |
| GET | `/api/pipelines/{pid}/builds/{bid}/artifacts/download?file=...` | 下载单个产物（二进制） |
| GET | `/api/pipelines/{pid}/builds/{bid}/artifacts.zip` | 下载全部产物 ZIP |
| POST | `/api/pipelines/{pid}/builds/{bid}/republish` | 把该次构建的产物直接重新发布到 SSH（不重新编译）；body `{"server_id":"...","publish":"目标名"}`，`publish` 为空取第一个启用目标 |
| POST | `/api/pipelines/{pid}/builds/{bid}/cancel` | 取消运行中/等待中的构建（状态落 `aborted`） |
| GET | `/api/views` | 视图列表（列表视图 / 我的视图） |
| GET | `/api/views/{name}/jobs` | 视图内任务（按当前用户权限过滤） |
| POST/PUT/DELETE | `/api/views` `/api/views/{name}` | 新建 / 编辑 / 删除视图（`data/views.json`） |
| DELETE | `/api/pipelines/{pid}/builds/{bid}` | 删除单次构建（连 `builds/{bid}` 目录：日志 + 产物，权限 `job.delete`） |
| WS | `/ws/pipelines/{pid}/builds/{bid}/logs` | 实时日志流（token 查询参数鉴权） |
| GET/PUT | `/api/system/settings` | 系统配置读取 / 更新（含 `log_max_mb` 日志上限、保留策略定期扫描） |
| GET | `/api/pipelines/{pid}/config.json` | **导出任务配置**（对齐 Jenkins `job/{name}/config.xml`）：返回裸 JSON 文档（不套 `{code,msg,data}`），**不含任何密钥**，集成 `server_name` / `git_credential_name` 便于跨实例对齐 |
| POST | `/api/system/import/config` | **导入平台配置文档**：body 为导出文档或原生 `config.json`；query `name`/`mode=create\|update\|skip`/`dry_run=true`（管理员） |
| POST | `/api/system/import/jenkins` | **从 Jenkins 导入任务**：body 为 `config.xml`（`application/xml`）或 multipart（`file`）；query `name`（必填，config.xml 不含任务名）、`mode=create\|update\|skip`、`dry_run=true`、`keep_source=false`（管理员） |
| POST | `/api/system/email/test` | 发送测试邮件（管理员） |
| POST | `/api/system/webhook/test` | 发送 Webhook 测试通知（钉钉/企微/Slack，管理员） |
| GET | `/api/notify/contacts` | 平台用户联系方式（任务通知选人用，需任务配置权限） |
| POST | `/api/pipelines/{pid}/notify/test` | 向该任务配置的人员/群发送测试通知 |
| PUT | `/api/security/users/contact` | 设置用户通知联系方式（邮箱/企微 ID/手机号，管理员） |
| GET | `/api/system/audit?limit=300` | 操作审计日志（最近 N 条，时间倒序，管理员） |
| GET | `/api/system/notify?limit=&pipeline=&recipient=&status=` | 通知投递记录（按收件人粒度，排查漏收） |
| GET/PUT | `/api/profile` `/api/profile/contact` | 个人资料：查看 / 自助维护联系方式（任何登录用户） |
| POST | `/api/profile/email/verify` `/api/profile/email/confirm` | 发送邮箱验证码 / 提交验证码确认 |
| GET | `/api/security/users` | 用户管理页数据：内置管理员 + 平台用户 + 联系方式 + 角色摘要 |
| POST | `/api/security/users` | 创建平台内用户（bcrypt，管理员） |
| POST | `/api/security/users/delete` | 删除用户及授权（管理员） |
| PUT | `/api/security/users/password` | 管理员重置用户密码（撤销会话） |
| POST | `/api/system/reload` | 从磁盘重新读取设置 |
| POST | `/api/system/quiet` | 静默模式（准备关机） |
| GET | `/api/system/info` `/load` `/log` `/features` | 系统信息 / 负载 / 日志 / 内置组件 |
| GET/POST | `/api/system/old-data` `/old-data/clean` | 扫描 / 清理旧数据 |
| GET/PUT | `/api/system/data-home` | 读取 / 修改数据目录（写入引导配置，重启生效；可迁移现有数据） |
| GET/POST/PUT/DELETE | `/api/credentials` | 凭据管理（密钥不回传） |
| GET | `/api/security/rbac` | 角色与授权数据（Manage and Assign Roles，仅 Administer） |
| PUT | `/api/security/rbac/roles` | 替换全部角色（全局/任务角色 + 权限矩阵） |
| PUT | `/api/security/rbac/assignments` | 替换全部授权（用户/组 → 角色） |

**任务配置扩展**：新建任务支持 **Copy from**（从现有任务复制全部配置，对齐 Jenkins New Item）；
`cron_spec` 字段支持 5 字段 cron 定时构建（含 `H` 散列均衡），触发记录显示「定时触发」；
`notify` 支持人员/群/事件、值班轮值、升级、抑制窗口、分支路由与消息模板（详见 §十一）。

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

---

## 六、文件系统存储设计

```
ci-platform.json                      # 引导配置：记录 DATA_HOME 位置（已 gitignore，可用 -config 改路径）
data/                                  # DATA_HOME，运行时创建，已 gitignore
├── settings.json                      # 系统配置（应用名 / 默认参数 / 认证 / 静默模式 / 邮件 / Webhook / SSH）
├── rbac.json                          # 角色、授权与平台内用户（bcrypt、联系方式、邮箱验证状态）
├── views.json                         # 视图（列表视图 / 我的视图）
├── audit.log                          # 操作审计（JSONL；16MB 轮转为 audit.log.1）
├── notify.log                         # 通知投递记录（JSONL；16MB 轮转为 notify.log.1）
├── credentials/
│   └── {credential_id}.json           # 凭据（用户名密码 / 密钥 / SSH 私钥）
├── jobs/
│   └── {pipeline_id}/                 # 目录名即 ID（时间戳字符串），无额外索引
│       ├── config.json                # Pipeline 定义（含 notify：人员/群/轮值/升级/抑制/路由/模板）
│       ├── next_build                 # 下一个构建编号（单调递增，删除构建后不回退）
│       └── builds/
│           └── {build_id}/            # 自增编号，对应 Jenkins #N
│               ├── build.json         # 构建元数据（含分支/提交人，供分支路由与通知）
│               ├── log.txt            # 控制台输出（超 log_max_mb 截断并标注）
│               ├── artifacts.json     # 产物清单（路径 / 大小 / SHA-256）
│               └── artifacts/         # 每次构建独立保留的编译产物
└── workspace/                         # 工作区，按任务名 {workspace_dir} 复用；仅已删除任务的孤儿目录超过 TTL 自动清理
```

- **原子写入**：先写同目录临时文件 → `fsync` → `rename`，避免并发写坏文件。
- **编号分配**：`CreateNext` 在锁内完成「取 `max(目录最大编号, next_build-1) + 1` → 创建目录」，
  既避免并发触发撞号，也保证删除最新构建后编号不回退（对齐 Jenkins `nextBuildNumber`）。
- **删除粒度**：删任务 → 递归删除整个 `data/jobs/{id}/`（构建记录随之清空）；
  删单次构建 → 只删 `data/jobs/{id}/builds/{bid}/`（日志 + 产物），任务与其它构建保留。

---

## 七、构建执行流程

1. 创建构建目录与 `build.json`（`status=pending`），原子分配自增 `build_id`。
2. 入 `jobs` channel（容量 100），worker 竞争取出 → `status=running`、写 `started_at`。
3. 准备工作区 `data/workspace/{workspace_dir}/`（目录名默认取任务名；默认复用；仅当任务显式勾选 `delete_workspace_before_build=true` 时先清空）。
4. `git_url` 非空则拉取：工作区已有 `.git` 时 fetch；远端无新提交则跳过 checkout/reset，有更新时才 hard reset 并保留未跟踪/忽略的构建产物与缓存。
5. 在服务器工作区串行执行 steps：注入 `CI=true` / `CI_BUILD_ID` / `CI_BUILD_NUMBER` / `CI_PIPELINE_NAME`、构建参数与工具主目录；
   每步由 shell 执行 `"<run>"`。默认**退出码非零即失败并终止后续步骤**，步骤可单独配置
   `timeout_sec` / `retry` / `retry_delay_sec` / `allow_failure` / `when`（见第九节）。
6. 命令 stdout/stderr **同时**写 `log.txt` 与广播 WebSocket。
7. 全部成功后若 `artifacts.enabled=true`，按 glob 把产物复制到 `data/jobs/{pid}/builds/{bid}/artifacts/`，并写 `artifacts.json`。
8. 然后按顺序发布到每个启用的 SSH 发布目标（`ssh_publishes`，可多个）；单个目标失败不阻断其余目标，
   但构建最终标记 `failed` 并在错误里列出失败目标名。发布失败不影响已归档的产物。
9. 签出成功后记录 SCM 现场（实际分支、短 SHA、提交人姓名/邮箱）到 `build.json`，供分支路由与「通知提交者」使用。
10. 结束写 `status` / `finished_at` / `exit_code`（用户取消落 `aborted`，与代码失败的 `failed` 区分）；
   工作区持续保留（产物如 `app` / `target/*.jar` / `dist/` 可用于后续 SSH 发布）。
11. 按任务/全局通知配置发送结果通知（值班 @、连败升级、抑制合并、分支路由、消息模板），
   投递结果按收件人写入 `data/notify.log`，并在构建日志写一行 `[notify] ...`。

> 同一任务的构建由调度器**串行执行**（不同任务并行），避免并发写同一工作区；
> **工作区目录使用任务名**：`workspace_dir` 由任务名生成（非法字符替换为 `_`，重名追加 `-{id}`），重命名任务会同步移动目录。
>
> **默认复用工作区**：Git 只做增量 fetch；未跟踪/`.gitignore` 忽略的文件（`node_modules`、`target`、`dist`、Go 编译产物等）在 reset 时会被临时移出再移回，避免每次构建都重新下载依赖或重新 clone。

**执行方式**：平台统一在运行服务的服务器上本地执行，不再使用 Docker。

- 构建步骤直接使用服务器上的 shell 与工具链；
- 「全局工具配置」中每个类别第一个填写主目录的安装项会被注入 `GOROOT` / `JAVA_HOME` / `MAVEN_HOME` /
  `GRADLE_HOME` / `ANT_HOME` / `NODE_HOME` 等变量，并把 `主目录/bin` 与主目录本身前置到 `PATH`（兼容 Windows 版 Node）；
- Windows 默认使用 `cmd.exe`（也可在「系统配置 → 执行 shell」指定 PowerShell / Git Bash）；
- 以 **CI 服务进程的用户权限**运行命令，请只给可信用户任务编辑权限。

**语言版本解法**：不再通过镜像切换版本，而是由服务器安装的工具与「全局工具配置」中的主目录决定；
代码中**不存在任何 `switch language` 分支**。旧任务的 `executor` / `image` 字段仅保留兼容，不再参与调度。

### 7.1 构建产物归档与回滚

每次构建的编译成果独立保存在 `data/jobs/{pipeline_id}/builds/{build_id}/artifacts/`，下次构建不会覆盖；出问题时可在构建页下载历史产物回滚。

配置：**任务 → 构建后操作 → 归档构建产物**，填写相对工作区的 glob：

| 场景 | 常用模式 |
| --- | --- |
| Go | `app`、`app.exe` |
| Maven | `target/*.jar` |
| Java | `*.jar` |
| Node | `dist/**` |
| 整个目录 | `dist`（等价于 `dist/**`） |

- 归档发生在**构建步骤成功之后、SSH 发布之前**；SSH 失败时产物仍保留。
- **一键重新发布**：构建成功且已归档产物时，构建页可把 `artifacts/` 直接重新发布到
  「系统配置 → SSH 服务器」中的目标（`POST /api/pipelines/{pid}/builds/{bid}/republish`，不重新编译）；
  发布日志追加在该构建的 log.txt。用于发布失败重发、回滚到历史版本后重发。
- 升级后首次启动会自动为「步骤里明确写了输出文件」的旧任务启用归档：
  `go build -o <file>` 与 `jar cf <file>`；其他任务在任务配置中手动开启即可（一次性标记 `.artifact-config-v1`）。
- 至少匹配一个文件，否则构建失败并在日志中提示，避免“构建成功但没有可回滚产物”。
- 产物清单写入 `artifacts.json`（含 SHA-256），下载接口只允许清单中的相对路径。
- 产物随「丢弃旧的构建」策略一起删除；需要保留全部构建时，把任务/全局保留数量与天数都设为 `0`。
- 构建页「构建产物」卡片支持单文件下载与 ZIP 全量下载；接口见下表。

---

## 八、参数化构建

任务可定义参数（**任务 → 参数化构建**），触发时浏览器弹出表单；未填的取默认值。
值会以两种环境变量注入构建步骤：原名 `VERSION` 与带前缀的 `CI_PARAM_VERSION`。

| 类型 | 说明 |
| --- | --- |
| `string` | 单行字符串，注入前把换行压成空格（换行会破坏 shell 变量赋值） |
| `bool` | `true` / `false`；触发时也接受 `1/0`、`yes/no`，统一归一为 `true`/`false` |
| `choice` | 固定可选值集合，默认值必须在集合内（留空取第一个） |
| `text` | 多行文本，保留换行（适合 changelog 之类的片段） |

约束（`internal/service/pipeline_service.go` 的 `normalizeParams` / `ResolveBuildParams`）：

- 最多 30 个参数；参数名 `^[A-Za-z_][A-Za-z0-9_]*$`，且**不能以 `CI_` 开头**（平台保留命名空间，
  否则任务可以覆盖 `CI_BUILD_ID` 等内置变量）。
- 参数名大小写不敏感去重（Windows 环境变量不区分大小写，`version` 与 `VERSION` 会互相覆盖）。
- 触发时传入未定义的参数直接拒绝（`40000`），避免拼写错误被静默忽略。
- **参数快照进构建记录**（`build.json` 的 `params`）：后续修改默认值不影响历史构建的复盘，
  构建页会显示「构建参数」卡片。
- 触发来源：手动（body `{"params":{...}}`）、Webhook（查询串 `?VERSION=1.2.3`，只认已定义参数，
  不会因为 `token` 之类的额外查询键报错）、定时（全部取默认值）。
- 任务列表/视图页的「一键构建」对参数化任务会跳到任务页，强制用户填参数（避免悄悄用默认值发错版本）。

## 九、步骤级执行控制

每个构建步骤可配置（**任务 → 构建步骤 → 高级选项**）：

| 字段 | 语义 |
| --- | --- |
| `timeout_sec` | 单次尝试的执行时限，`0` 表示不限（上限 86400 = 24h）。超时杀掉整个进程树，按退出码 `124` 视为失败 |
| `retry` | 失败后的**额外**重试次数（0~5）。退出码非零与超时都会重试 |
| `retry_delay_sec` | 重试间隔（0~60s），等待期间响应取消 |
| `allow_failure` | 失败只记警告，不阻断后续步骤，也不影响构建整体结果 |
| `when` | `success`（默认，前置成功后执行）/ `failure`（有失败才执行）/ `always`（总是执行） |

执行语义（`internal/executor/local_executor.go`）：

- 未被允许的失败会**中止后续 `when=success` 步骤**，但 `when=always` / `when=failure` 步骤仍会执行——
  清理、收集诊断信息这类收尾动作不会因为前面的失败而漏跑；最终构建仍标记 `failed`，退出码取第一个致命失败。
- 跳过会显式写日志（`skipped (when=..., 前置步骤失败)`），避免看日志时误以为步骤被静默丢弃。
- `when=failure` 与 `allow_failure` 互斥（自相矛盾），保存时直接拒绝。
- 上限（`retry<=5`、`timeout<=24h`）在服务端硬性拦截：误填 `retry=1000` 会让构建卡数小时难以发现。

---

## 十、SSH 发布（对齐 Jenkins Publish Over SSH）

`internal/executor/ssh_publisher.go` 是项目中**唯一**的 SSH/SFTP 交互点。

**配置分层（推荐）**：

1. **系统配置 → SSH 服务器**：集中维护主机、端口、用户名、认证方式（密钥/密码）、默认远程目录，保存到 `data/settings.json`；密码不回显（`has_password`）。
2. **任务 → 构建后操作 → SSH 发布**：可添加**一个或多个发布目标**（`ssh_publishes`），
   每个目标有名称（日志与「重新发布」下拉里用它区分）、`server_id`、`source_files`、`remove_prefix`、
   `exec_command` 与可选的远程目录覆盖（留空用服务器默认值）。任务里不保存任何 SSH 密码或私钥。
3. 构建时由 `BuildService.resolveSshConfig` 把服务器配置与任务参数合并后交给 `SshPublisher`；修改服务器后所有引用它的任务自动生效。
4. 旧版任务（单目标 `ssh_publish` 或内联 host/username/password）继续兼容：保存时自动迁移成列表里的
   「默认发布」目标；在任务里改选全局服务器并保存后，内联账号/密码会被清除。

多目标行为：

- 目标按配置顺序依次发布，单个目标失败**不阻断其余目标**，全部尝试完再返回
  `ssh publish failed (N/M targets): ...`，构建标记 `failed`；日志里每个目标一行 `[ssh] target "名称" ...`。
- 上限 10 个目标；名称为空自动命名「发布 N」，重名自动加 `(2)` 后缀（`normalizeSshPublishes`）。
- 禁用的目标（`enabled=false`）保留配置但不执行。
- 构建页「重新发布」可选择要发布到哪个目标（`publish` 字段，按名称匹配；留空取第一个启用目标）。

执行流程（每个目标）：

1. 建立连接（`golang.org/x/crypto/ssh`，密钥或密码认证；私钥路径支持 `~/` 前缀）。
2. 若 `clean_remote=true`，先递归清空 `remote_base_dir`（对齐 Jenkins Clean remote directory，不可逆，日志记录被清空路径）。
3. SFTP 上传 `source_files`（glob，如 `target/*.jar`）到 `remote_base_dir`，`remove_prefix` 剥离路径前缀，
   远程目录自动创建；`flatten=true` 只保留文件名、不保留目录结构（对齐 Jenkins Flatten files）。
4. `exec_command` 非空则在远程执行，stdout/stderr **实时合并进同一份日志**；
   `exec_timeout_sec`（0 表示沿用构建级超时）到点会终止远程会话并让发布失败（对齐 Jenkins execTimeout）。
5. 该目标任一步失败 → 记失败并继续下一个目标，最终构建 `failed`。

**安全提示**：SSH 服务器可配置 `known_hosts_path`（如 `~/.ssh/known_hosts`），配置后会校验服务器主机密钥，
未知主机直接拒绝连接；不填写时跳过校验（存在中间人风险，构建日志会输出 WARN）。

---

## 十一、验证结果

```bash
go build ./...          # ✅
go vet ./...            # ✅
go test ./...           # ✅ 7 个包（store / executor / service / handler / middleware / model / pkg）
bash ./build.sh         # ✅ 双平台产物 + UPX 压缩
```

| 场景 | 结果 |
|---|---|
| 创建带 SSH 配置的任务 | ✅ 生成 `data/jobs/{id}/config.json` |
| 列表 / 详情 / 更新 / 删除（递归清目录） | ✅ |
| 非法入参 → `code=40000`；Webhook 错密钥 → 拒绝 | ✅ |
| 构建状态流转 `pending→running→failed`，元数据与日志落盘 | ✅ |
| 日志 API 内容与 `log.txt` 一致 | ✅ |
| 构建产物按次归档 + 下载/ZIP 回滚 | ✅ |
| WebSocket 握手 | ✅ `101 Switching Protocols` |
| SPA 托管 + history 深链回退 | ✅ |
| **前端内嵌** | ✅ 移走 `web/dist` 且 `CI_WEB_DIR` 指向不存在目录，页面与 1MB JS bundle 仍正常返回 |
| **UPX 压缩后运行** | ✅ 压缩版 Windows 产物实测启动并完整提供 Web 界面 |
| 交叉编译产物格式 | ✅ Linux `ELF` / Windows `MZ` |
| Git 工作区复用 | ✅ 第二次构建不 clone；远端无更新时跳过 reset，有新提交时保留未跟踪产物与依赖缓存 |
| 本地构建阶段 | ✅ Go 编译可执行文件、javac + jar 打包、`npm ci \|\| npm install` + `npm run build`；无需 Docker |
| 构建取消 | ✅ 运行中取消落 `aborted`（与 failed 区分），Windows 进程树被 `taskkill /T` 终止，排队期取消直接 aborted |
| 定时构建 | ✅ `* * * * *` 30s 内触发（`timer`）；清空 cron 后 75s 内不再触发；4 字段非法被拒绝 |
| 通知（项目 × 人员/群/事件）| ✅ 事件过滤、按人员解析联系方式、群 @ 提醒、URL 脱敏与「脱敏回写」保留真实 token |
| 值班 / 升级 / 抑制 / 分支路由 | ✅ 3 次快速失败仅发 1 条（后两条合并）；连败达阈值追加升级（升级优先于抑制）；feature/* 只通知提交者；模板渲染出连败与值班人 |
| 多目标 SSH 发布 | ✅ 一次构建依次发布到 2 个目标（禁用目标跳过）；目标 1 失败仍尝试目标 2，构建 `failed` 且错误里带目标名；密码不回显；旧单目标配置自动迁移为「默认发布」 |
| 参数化构建 | ✅ `VERSION/DEBUG(bool)/ENV(choice)` 注入 `VERSION` 与 `CI_PARAM_VERSION`；未传取默认值；未知参数与越界 choice 被拒绝且不产生构建；Webhook 查询串覆盖参数 |
| 从 Jenkins 导入任务 | ✅ 32 项 e2e：预览不落盘、Git/分支/步骤/cron/参数/产物/保留策略/SSH 自动匹配、源 XML 落盘、重名 create/update/skip、Jenkinsfile 与非法 XML/cron 拒绝、multipart 文件名兜底、导入后能带参数真实构建成功 |
| SSH 发布选项 | ✅ cleanRemote / flatten / execTimeout(毫秒→秒) 从 Jenkins 导入后落盘、导出、再导入一致；`usePty` 等逐条告警；`clean_remote` 未填远程目录被拒 |
| 配置导出 / 导入 | ✅ 36 项 e2e：导出为裸 JSON（无 `code` 包装、带下载文件名）、不含 SSH 密码/Webhook 密钥/群 token、只带服务器与凭据**名称**；导入预览不落盘、按名对齐到另一实例的 `server_id`/凭据、缺失服务器时停用+error 告警、原生 config.json 可导入、update 覆盖、坏文档/版本过高 40000 |
| 步骤级控制 | ✅ `allow_failure` 失败继续；`timeout_sec=1` 杀掉 sleep 并记 124；`when=failure/always` 在失败后执行；致命失败跳过 `when=success`；`retry=2` 共 3 次尝试 |
| 通知记录 | ✅ 按收件人粒度落盘，可过滤 `sent` / `failed` / `suppressed`，失败带错误原因 |
| 个人资料与邮箱验证 | ✅ 迷你 SMTP 实测：发码 → 错误码拒绝 → 正确码标记已验证 → 改邮箱失效 → 60s 限流 `42900` |
| 邮箱必填 | ✅ 无邮箱创建/改资料 `40000`；历史空邮箱启动 WARN + 构建日志警告 |
| 删除单次构建 | ✅ 目录（含产物）一并删除、其它构建保留、运行中 `40900`、重复删除幂等、编号不回退 |
| 操作审计 | ✅ 变更类请求落 `data/audit.log`，重启后 ID 单调；与通知记录共用倒序读取实现 |

---

## 十二、目录结构

```
ci-platform/
├── assets.go                        # go:embed 内嵌 web/dist（必须位于项目根）
├── build.sh                         # 构建脚本：前端 + 交叉编译 + UPX
├── cmd/server/main.go               # 只做装配 + 优雅退出
├── internal/
│   ├── config/  router/  middleware/
│   ├── handler/                     # pipeline / build / webhook / ws / auth / system / credential /
│   │                                #   rbac / view / notify / profile
│   ├── service/                     # pipeline / build / scheduler / cron(+trigger) / auth / settings /
│   │                                #   credential / system / rbac / view / notify / audit / profile
│   ├── store/                       # pipeline / build / settings / credential / rbac / view / audit / notify
│   ├── executor/                    # local_executor / ssh_publisher / git_client / log_streamer /
│   │                                #   artifact_collector / webhook_notifier / checkout_info
│   ├── importer/                    # jenkins.go：Jenkins config.xml → 平台任务（语义翻译 + 告警）
│   ├── model/  pkg/                 # pkg: response / mask / limitedwriter / notifytemplate / ...
├── web/
│   ├── src/                         # api / composables / stores / views / components / router / types / utils
│   └── dist/                        # 前端产物（入库，保证 go build 可用；会被 go:embed 内嵌）
├── dist/                            # 构建脚本输出（gitignore）
├── data/                            # 运行时数据目录（gitignore）
├── go.mod / Makefile / .gitignore / README.md / DESIGN.md
```

---

## 十三、系统管理（对齐 Jenkins Manage Jenkins）

登录后左侧菜单进入「系统管理」，按 Jenkins 的信息架构组织：

> **关于「工具路径」**：构建直接在服务器本地执行，因此「全局工具配置」中的安装项只需填写
> **主目录（服务器绝对路径）**。平台会注入 `JAVA_HOME` / `MAVEN_HOME` / `GRADLE_HOME` / `ANT_HOME` /
> `GOROOT` / `NODE_HOME` 等变量，并把 `主目录/bin` 前置到 `PATH`，步骤里直接写 `go build` / `mvn package` /
> `npm run build` 即可。若工具已经在系统 `PATH` 中，也可以不配置主目录。

| 分类 | 页面 | 能力 |
|---|---|---|
| 系统配置 | 系统配置 | 主目录、系统消息、Jenkins Location、默认分支、工程命名限制、静默期/SCM 重试/Shell/超时、Global Build Discarders（含日志上限）、全局环境变量、Maven 项目配置、Git plugin、SSH 服务器（Publish Over SSH）、邮件通知、Webhook 通知（钉钉/企微/Slack）、准备关机、读取设置 |
| 系统配置 | 全局工具配置 | 按 JDK / Git / Gradle / Ant / Maven / NodeJS 分类维护安装项（工具主目录），以及 Maven settings.xml 提供者 |
| 系统配置 | 插件管理 | 内置能力清单（只读，单二进制交付无插件生态） |
| 系统配置 | 节点和云管理 | 执行器数量、构建队列、工作区 |
| 安全 | 全局安全配置 | 登录开关、管理员账号、修改密码 |
| 安全 | 凭据管理 | 用户名密码 / 密钥文本 / SSH 私钥的增删改查（密钥不回传） |
| 安全 | 用户管理 | 账号本身：新建（**邮箱必填**）/ 删除 / 重置密码（撤销会话）/ 联系方式；列表显示角色摘要与邮箱验证状态；内置管理员只读 |
| 安全 | Manage and Assign Roles | 全局角色 + 任务角色（Pattern 匹配任务名、Template 模板继承）与用户/组授权；**权限真实生效**：未授权用户访问 API 返回 `code=40300`，前端隐藏对应入口；数据存 `data/rbac.json`，内置主体 `anonymous` / `authenticated`，代理/SCM/Metrics 列禁用（平台不支持） |
| 视图 | 新建视图 / 编辑视图 | 列表视图（显式任务 + 任务名正则）与我的视图（自动显示当前用户有权限访问的任务）；数据存 `data/views.json`；展示列与 Widgets（过滤构建队列/执行器）可配置 |
| 状态信息 | 系统信息 / 系统日志 / 负载统计 / 关于 | 运行时信息、内存环形日志、队列与磁盘占用、版本 |
| 状态信息 | 操作日志 | 变更类操作审计（登录/登出/触发/增删改），数据存 `data/audit.log` |
| 状态信息 | 通知记录 | 每次告警按收件人/群的投递结果（已送达/失败/已合并），数据存 `data/notify.log`；按任务、收件人、状态过滤 |
| 系统配置 | 导入任务 | 两个来源：Jenkins `config.xml`（XML → 平台任务）与平台导出的 `config.json`（实例间搬迁）；都是**先预览（含告警分级）再导入**。详见第十六节 |
| 问题排查 | 管理旧数据 | 扫描并清理无主工作区与孤儿任务目录（仍存在任务的工作区保留） |
| 工具和动作 | 读取设置 / 准备关机 / REST API | 重载 settings.json、进入静默模式、接口说明 |

说明：
- 系统配置持久化到 `data/settings.json`；启动参数 / 环境变量仅在文件缺失或字段为空时提供默认值。
- `workers`、`queue_size`、TTL 等属启动期参数，界面只读，修改需重启。
- 认证默认只有系统配置里的单管理员账号（密码 bcrypt 落盘，改后重启仍生效，至少 8 个字符）；
  需要多人协作时在「系统管理 → 用户管理」创建平台用户，并在「Assign Roles」分配角色（邮箱必填）。
- 系统配置中的「单次产物大小上限（MB）」「单次产物文件数上限」会在归档时强制执行，防止异常产物写满磁盘。
- 「准备关机（静默模式）」只拒绝**新的**构建入队，不影响正在运行的构建。
- 以下配置会**直接作用于构建**：静默期（开始前等待）、SCM 重试次数、Shell、Global Time Out、构建保留策略、全局环境变量（含 Maven / Git 相关变量）、邮件通知、Webhook 通知（钉钉/企微/Slack）。
- **构建历史可删除**：任务页构建历史、构建日志页、全局构建历史都支持删除单次构建，
  会连同 `data/jobs/{pid}/builds/{bid}` 目录（日志 + 已归档产物）一起删除，需二次确认；
  运行中/排队中的构建需先停止。删除最新构建后编号不会回退（`next_build` 记录）。
- **用户邮箱必填**：创建用户 / 改联系方式 / 个人资料都要求填写有效邮箱——构建通知按人员发邮件，
  没有邮箱等于收不到告警。历史遗留的空邮箱会在启动日志告警、用户管理页列出，并在构建日志中以
  `[notify] ... 警告：人员 X 未配置邮箱` 提示。
- **通知按项目挂人/挂群**：在任务的「构建后操作 → 构建通知」里选择平台用户（自动解析其邮箱/企微 ID/手机号）、
  填外部邮箱、添加钉钉/企微/Slack 群机器人，并勾选通知事件（失败 / 成功 / **修复**（上次失败→本次成功）/ 取消）。
  任务未配置时沿用「系统配置」的全局默认；勾选「同时发送全局默认」可叠加。用户联系方式在
  「系统管理 → 用户管理」维护（用户也可在「个人资料」页自助维护并验证邮箱）。发送结果会写入构建日志（`[notify] ...`）。
- **值班轮值（on-call）**：任务里配置轮值人员与周期，失败时自动 @ 当周值班人；轮值由日期推算，重启不影响。
- **升级策略（escalation）**：连续失败达到阈值（默认 3 次）追加通知上级/更大群；升级优先于抑制窗口。
- **失败抑制与聚合**：设置抑制窗口（分钟，默认关闭）后，同一任务的重复失败只发首条，后续合并为
  「已连续失败 N 次」，避免刷屏；被合并的记录仍可在「通知记录」里查到。
- **按分支/环境路由**：按分支 glob（`main` / `feature/*` / `release/**`）叠加人员、群，或勾选「提交者」
  只通知本次 git 提交作者（分支与提交人从实际签出的工作区读取）。
- **通知记录（谁漏收了告警）**：每次发送按收件人/群落盘到 `data/notify.log`，在「系统管理 → 通知记录」
  可按任务、收件人、状态（已送达/失败/已合并）查询，失败记录带错误原因。
- **消息模板**：任务级自定义文案，占位符 `{{job}} {{build}} {{event}} {{branch}} {{commit}} {{streak}}
  {{oncall}} {{url}}` 等；邮件与群消息共用模板，@ 提醒始终追加在末尾。

---

## 十四、明确的非目标

不实现：多节点 agent、DAG 并行、插件系统、multibranch / pipeline-as-code、构建触发器链（upstream/downstream）。

已实现（曾列为非目标，后补齐）：定时构建（5 字段 cron + `H` 散列）、视图（列表视图 / 我的视图）、
权限体系（对齐 Jenkins Role Strategy 的精简版，见「系统管理 → Manage and Assign Roles」）、
按项目挂人/挂群的告警（含值班轮值、升级、抑制、分支路由与通知记录）。
构建执行仍为**服务器本地串行步骤**，发布固定走 **SSH/SFTP**。

详细设计（架构分层、时序、日志背压、内嵌方案权衡等）见 [DESIGN.md](./DESIGN.md)。
与 Jenkins / GitLab CI / GitHub Actions / Argo / Tekton 等工具的功能对比、差距清单与优先级路线图见
[COMPARISON.md](./COMPARISON.md)（含每项差距的最小实现与改动模块）。

## 十五、任务详情页（对齐 Jenkins Project 页）

点击左侧 jobs 或作业列表进入任务页，包含：
- 左侧动作：系统管理 / 状态 / 修改记录 / 工作空间 / 立即构建 / 配置 / 删除工程 / 重命名 / 凭据
- 任务描述（支持 `Edit description` 内联编辑）
- Builds 列表：按日期分组，每行显示状态球、`#N`、触发原因（`Started by user X` / `Started by webhook`）与时间；
  运行中的构建可「停止」（落 `aborted`），已结束的构建可「删除」（连带 `builds/{bid}` 目录，需 `job.delete` 权限）
- 相关链接：最近一次 / 稳定 / 成功 / 失败 / 未成功 / 完成的构建

数据层：`Pipeline` 新增 `description`，`Build` 新增 `triggered_by`（手动触发记录登录用户名，Webhook 记为 `webhook`）；新增 `POST /api/pipelines/{pid}/rename`。

### 任务配置页（对齐 Jenkins Configure）

配置页按 Jenkins 分区组织，并提供顶部锚点导航：

| 分区 | 内容 |
|---|---|
| General | 任务名称、描述、丢弃旧的构建（任务级保留策略）、参数化构建（参数个数 + 快捷跳转）、并发构建（说明：平台同任务固定串行）、禁用任务（说明：用清空 cron + 撤销 Webhook 代替） |
| 源码管理 | 无 / Git、Repository URL、Credentials（选择已保存的账号密码 / 令牌，支持现场新增）、指定分支、额外行为（说明） |
| 构建触发器 | 远程触发 Webhook 地址（可复制）+ 密钥；GitLab/GitHub 事件说明（Push 可用，MR/ciSkip 等不区分）；**定时构建**（5 字段 cron，支持 Jenkins `H` 散列）；SCM 轮询/上下游任务链说明 |
| 构建环境 | Delete workspace before build starts；**构建工具版本**：显示全局工具配置里生效的 JDK/NodeJS/Maven/Go 主目录（对应 Jenkins 的 per-job JDK/NodeJS 选择） |
| 构建步骤 | 执行方式（本地服务器）、执行 shell（步骤编辑器，含超时/重试/允许失败/when） |
| 参数化构建 | 定义参数（string / bool / choice / text），触发时弹表单；值注入同名环境变量与 `CI_PARAM_<大写名>`，并快照进构建记录 |
| 构建步骤 | 每步可配置超时、重试次数/间隔、允许失败、执行条件（success / failure / always） |
| 构建后操作 | 归档构建产物（每次构建独立保留，可下载回滚）、SSH 发布（**可配置多个目标**，每个目标独立选择服务器 + 上传文件/前缀/平铺/清空远程目录/远程命令/命令超时）、构建通知（人员 / 群 / 事件 / 值班轮值 / 升级 / 抑制窗口 / 分支路由 / 消息模板，可发测试通知） |

新增任务级字段：`build_retention_max_per_job`、`build_retention_max_age_days`（覆盖全局丢弃策略）、`delete_workspace_before_build`（默认 **false**；升级后旧 Git 任务会一次性迁移为复用，避免每次重新 clone）。

Git 凭据：任务通过 `git_credential_id` 引用「凭据管理」中的账号密码 / 令牌；构建拉取时由 `BuildService` 解析并通过 HTTPS BasicAuth 使用（日志仅记录 `auth=true/false`，不输出密钥）。

---

## 十六、从 Jenkins 迁移任务

Jenkins 没有「迁移任务」的标准 REST API：`config.xml` + `createItem` 只能搬配置，带构建历史的迁移要靠拷目录。
本平台把前者做成了**服务端内建的一键导入**（`POST /api/system/import/jenkins`），
构建历史依旧可以像 Jenkins 官方建议的那样「拷目录」，只是目录格式需要一次翻译。

### 一键导入（配置）

```bash
# 源：http://jenkins/job/order/config.xml   目标：本平台
curl -X POST "http://ci:8080/api/system/import/jenkins?name=order" \
  -H "Authorization: Bearer <token>" \
  -H "Content-Type: application/xml" \
  --data-binary @config.xml
```

返回 `{pipeline, warnings[], action, dry_run, source_kept}`。先加 `dry_run=true` 只看转换结果不落盘。
Web UI 在「系统管理 → 从 Jenkins 导入」：上传或粘贴 XML → 预览（红/黄/蓝告警分级）→ 导入。

**翻译规则**（`internal/importer/jenkins.go`，按语义翻译而不是复制 XML 结构）：

| Jenkins | 本平台 |
|---|---|
| `<description>` | `description` |
| GitSCM：`url` / `branches[0]` | `git_url` / `git_branch`（`*/main`、`refs/heads/x` 会归一） |
| `builders/*`：Shell / BatchFile / PowerShell | `steps[].run`（多行原样保留，步骤名 `shell-1`、`batch-1`…） |
| Maven / Gradle / Ant builder | 转成 `mvn …` / `./gradlew …` / `ant …` 步骤（工具主目录见「全局工具配置」） |
| `TimerTrigger/spec` | `cron_spec`（`H` 直接支持；`TZ=` 前缀丢弃并告警；`@daily` 等别名展开为 `H H * * *`） |
| `parameterDefinitions` | `params[]`（String/Boolean/Choice/Text；非法参数名自动改名并告警；`Password` 降级为字符串并**告警**：平台不做参数脱敏） |
| `ArtifactArchiver/artifacts` | `artifacts = {enabled, files[]}`（逗号分隔 glob） |
| `logRotator`（`numToKeep`/`daysToKeep`） | `build_retention_max_per_job` / `_max_age_days` |
| Publish Over SSH（`configName` + `transfers`） | `ssh_publishes[]`（按服务器名/host 自动匹配 `server_id`；匹配不到→**保存为停用**并 error 告警） |
| transfer 的 `cleanRemote` / `flatten` / `execTimeout`（毫秒） | `clean_remote` / `flatten` / `exec_timeout_sec`（秒）；`usePty`、`keepFilePermissions`、`remoteDirectorySDF`、`makeEmptyDirs` 等**逐条告警不支持** |
| Publish Over SSH 作为**构建步骤**（`BapSshBuilderPlugin`，位于 `<builders>`） | 同样翻译成发布目标，并提示「平台统一在构建成功后发布」 |
| Jenkinsfile（`flow-definition`）/ multibranch / folder | **拒绝导入**并提示原因（平台不支持 pipeline-as-code） |
| 其它插件 builder / wrapper / trigger / SCM | 跳过 + 告警（逐条列出，绝不静默丢弃） |

可迁移的部分之外，这三件事**必须人工补**：

| 项 | 原因 |
|---|---|
| Git / SSH 凭据 | Jenkins 用 master key 加密，无法解密；在「凭据管理」重建后重新选择 |
| SSH 服务器 | 先在「系统配置 → SSH 服务器」建好（名字与 Jenkins `configName` 一致即可自动匹配） |
| 工具链版本 | Jenkins agent 上的 JDK/Maven/Node → 「全局工具配置」 |

**兼容性说明**：

- Jenkins 2.x 导出的声明是 `<?xml version='1.1' encoding='UTF-8'?>`，而 Go 的 `encoding/xml` 只支持 1.0，
  导入前会自动把声明改写为 1.0（Jenkins 写出的内容不依赖 1.1 独有语义）。
- `credentialsId`、GitLab `secretToken`、`jobCredentialId` 等密钥字段**不会**写入任务配置。

**重名策略**：`mode=create`（默认，自动 `-2` 后缀）/ `update`（覆盖同名，迁移中断后重跑）/ `skip`（已存在则跳过）。
`keep_source=true`（默认）会把原始 `config.xml` 存到 `data/jobs/{id}/jenkins-config.xml`，便于审计与导入器升级后重导。

### 实例间搬迁（导出 / 导入 config.json）

与 Jenkins 的 `job/{name}/config.xml` 对应，本平台的导出接口返回**裸 JSON 文档**：

```bash
# 导出（不含任何密钥，可直接转发/入库）
curl -H "Authorization: Bearer <token>" \
  "http://ci-a:8080/api/pipelines/{pid}/config.json" -o order-config.json

# 导入到另一个实例：先预览，再真正导入
curl -X POST "http://ci-b:8080/api/system/import/config?name=order&dry_run=true" \
  -H "Authorization: Bearer <token>" -H "Content-Type: application/json" \
  --data-binary @order-config.json
```

Web UI：任务页 / 配置页的「导出配置」按钮下载 `<任务名>-config.json`；
「系统管理 → 导入任务」的第二个页签上传/粘贴该文件。

**导出文档的内容**（`export_version: 1`，见 `internal/service/portability_service.go`）：

| 包含 | 不包含（密钥一律不落文件） |
| --- | --- |
| 名称、描述、步骤（含超时/重试/允许失败/when） | SSH 密码 / 私钥、内联 SSH 账号的密码 |
| Git 地址与分支、**凭据名称**（`git_credential_name`） | Git 凭据的 secret |
| 定时 cron、参数定义、产物 glob、保留策略 | Webhook 密钥（`webhook_secret`） |
| SSH 发布目标（含 `server_name`） | 群机器人 URL（钉钉/企微 token、Slack 路径段） |
| 通知的人员/邮箱/事件/值班/升级/路由/模板 | 构建历史、审计、通知投递记录 |

导入时按**名称**把资源对齐到目标实例（跨实例 ID 不同）：

- `server_name` → 目标实例同名 SSH 服务器（再退化到按 host 匹配）；找不到则**保存为停用**并给 `error` 告警；
- `git_credential_name` → 目标实例同名凭据；找不到则清空并给 `error` 告警；
- 群机器人 URL 为空的条目直接丢弃并告警（保留空地址只会在发送时失败）；
- 文档里残留的 `***` 脱敏值同样会被清空——把 `***` 当真实值写进配置会让构建静默失败。

导入同样支持 `mode=create|update|skip` 与 `dry_run`；未知字段会以 `warn` 告警列出（跨版本导出时旧实例也能导入）。

### 构建历史（拷目录）

Jenkins 侧的 `jobs/{NAME}/builds/{n}/` 需要翻译成平台目录结构：

```
Jenkins builds/17/{build.xml, log, archive/**}
   ↓
data/jobs/{id}/builds/17/{build.json, log.txt, artifacts/**, artifacts.json}
data/jobs/{id}/next_build        # 写入「最大编号+1」，保证编号不回退
```

状态映射：`SUCCESS→success`、`FAILURE→failed`、`ABORTED→aborted`、`UNSTABLE→failed`（平台无 unstable）。
这一步目前建议离线用脚本完成（先导入配置拿到 `id`，再停 Jenkins 拷目录），
后续若需要可加 `POST /api/pipelines/{pid}/import/builds`（zip 上传）以复用同一套翻译逻辑。

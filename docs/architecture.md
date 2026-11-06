# 架构与实现

[← 返回 README](../README.md)

## 技术选型确认单

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


## 文件系统存储设计

```
ci-platform.json                      # 引导配置：记录 DATA_HOME 位置（已 gitignore，可用 -config 改路径）
data/                                  # DATA_HOME，运行时创建，已 gitignore
├── settings.json                      # 系统配置（SMTP/SSH/Webhook/全局密钥变量等敏感字段 AES-GCM 加密）
├── rbac.json                          # 角色、授权与平台内用户（bcrypt、联系方式、邮箱验证状态）
├── views.json                         # 视图（列表视图 / 我的视图）
├── audit.log                          # 操作审计（JSONL；16MB 轮转为 audit.log.1）
├── notify.log                         # 通知投递记录（JSONL；16MB 轮转为 notify.log.1）
├── credentials/
│   └── {credential_id}.json           # 凭据（Secret 用 AES-GCM 加密，读取时解密）
├── credentials.key                    # 凭据/密码参数加密密钥（0600，必须随 data 一起备份）
├── tokens.json                        # API Token（只存 SHA-256 哈希，不存原始 secret）
├── events.json                        # 出站事件订阅（URL/HMAC Secret 加密落盘）
├── jobs/
│   └── {pipeline_id}/                 # 目录名即 ID（时间戳字符串），无额外索引
│       ├── config.json                # Pipeline 定义（Webhook 密钥/SSH 密码/群机器人 URL 加密落盘）
│       ├── next_build                 # 下一个构建编号（单调递增，删除构建后不回退）
│       ├── cache/                     # 显式缓存（按 cache key 哈希分目录，成功后写入）
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


## 构建执行流程

1. 创建构建目录与 `build.json`（`status=pending`），原子分配自增 `build_id`。
2. 入 `jobs` channel（容量 100），worker 竞争取出 → `status=running`、写 `started_at`。
3. 准备工作区 `data/workspace/{workspace_dir}/`（目录名默认取任务名；默认复用；仅当任务显式勾选 `delete_workspace_before_build=true` 时先清空）。
4. `git_url` 非空则拉取：工作区已有 `.git` 时 fetch；远端无新提交则跳过 checkout/reset，有更新时才 hard reset 并保留未跟踪/忽略的构建产物与缓存。
5. 在服务器工作区串行执行 steps：注入 `CI=true` / `CI_BUILD_ID` / `CI_BUILD_NUMBER` / `CI_PIPELINE_NAME`、构建参数与工具主目录；
   每步由 shell 执行 `"<run>"`。默认**退出码非零即失败并终止后续步骤**，步骤可单独配置
   `timeout_sec` / `retry` / `retry_delay_sec` / `allow_failure` / `when`（见[步骤级执行控制](pipeline.md#步骤级执行控制)）。
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
| Maven | `target/*.jar`（多模块会自动递归到子模块） |
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

### JUnit 测试报告

任务配置 → 构建后操作 → **发布 JUnit 测试报告**：按一行一个的 glob 匹配工作区 XML
（如 `**/TEST-*.xml`、`target/surefire-reports/*.xml`）。构建结束后解析汇总并复制原始报告到
`data/jobs/{pid}/builds/{bid}/test-reports/`；出现 failure/error 时构建标记为失败。
「允许无测试报告」关闭时，匹配不到 XML 也会失败，避免测试根本没跑却显示成功。
- 产物清单写入 `artifacts.json`（含 SHA-256），下载接口只允许清单中的相对路径。
- 产物随「丢弃旧的构建」策略一起删除；需要保留全部构建时，把任务/全局保留数量与天数都设为 `0`。
- 构建页「构建产物」卡片支持单文件下载与 ZIP 全量下载；接口见下表。

---

### 缓存、产物晋级与环境记录

- **显式缓存**（任务 → Advanced）：配置 `cache.key` 与工作区内路径（目录或 glob），构建前恢复到工作区、步骤成功后保存到
  `data/jobs/{pid}/cache/`。key 支持 `${参数名}` 占位符，key 变化使用独立缓存目录；缓存超过 8GB 或 10 万文件会跳过并告警，
  不影响构建结果。未启用时仍沿用工作区复用。
- **跨任务产物晋级**：构建页「晋级产物」把源构建的 `artifacts/` 复制到目标任务（目标 `latest` 取最近成功构建），
  每条文件按源清单 SHA-256 校验，失败不会留下半个文件；目标已存在同路径时默认拒绝，可显式覆盖。
- **环境与部署记录**：任务可设置 `environment`（dev/staging/prod 等），随构建快照；每次 SSH 发布/重新发布在
  `build.json` 的 `deployments` 中记录目标、服务器、环境、结果、时间与操作人，构建页展示「部署记录」卡片。


## 目录结构

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
├── docs/                            # 设计、对比、审计、迁移等专题文档
├── go.mod / Makefile / .gitignore / README.md
```

---

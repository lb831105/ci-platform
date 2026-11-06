# 轻量 CI/CD 平台

文件系统存储 + 本地工具链构建 + SSH 发布，**不依赖数据库**，**前端内嵌、单文件交付**。

面向中小团队还原 Jenkins 最常用的工作流：

```text
任务 → 手动 / Webhook / 定时触发 → 服务器本地执行 → 查看日志
     → 归档产物 → SSH 发布 → 通知 / 事件订阅
```

Web 控制台使用 Vue 3 + Element Plus，提供任务管理、构建历史、实时日志、参数化构建、发布审批、RBAC、凭据管理、仪表盘和系统配置。

---

## 核心能力

| 能力 | 说明 |
|---|---|
| 任务与触发 | 手动、Webhook、cron；支持分支白名单、事件过滤、幂等触发、禁用任务 |
| 构建 | 本地 shell 步骤；超时、重试、允许失败、`when` 条件；步骤耗时与状态落盘 |
| 参数化构建 | `string` / `bool` / `choice` / `text` / `password`；密码参数加密保存、日志遮罩 |
| 产物与缓存 | 产物归档、下载、跨任务晋级；显式缓存恢复/保存 |
| 发布 | 多目标 SSH 发布、重新发布、发布审批、部署记录、环境快照 |
| 测试 | JUnit 报告收集；失败/错误联动构建状态 |
| 可观测性 | 构建仪表盘、Prometheus `/metrics`、`/readyz`、`/openapi.json` |
| 安全 | API Token + scopes、RBAC、AES-GCM 配置加密、审计日志、日志脱敏 |
| 集成 | 邮件 / 钉钉 / 企业微信 / Slack；全局出站事件订阅（HMAC 签名） |
| 迁移 | Jenkins `config.xml` 导入；平台配置导出/导入 |

---

## 快速开始

### 1. 获取可执行文件

从发布目录直接运行：

```bash
# Linux
./dist/ci-platform-linux-amd64

# Windows
dist\ci-platform-windows-amd64.exe
```

也可以自行构建（前端会通过 `go:embed` 内嵌）：

```bash
./build.sh --single          # Linux / macOS / Git Bash
build.bat --single           # Windows cmd
```

构建细节见 [构建与验证](docs/build.md)。

### 2. 启动

默认监听 `:8080`，数据目录 `./data`：

```bash
./dist/ci-platform-linux-amd64 \
  -auth-user admin \
  -auth-password 'change-me-now'
```

部署时请设置管理员密码。未指定时会生成随机密码并只在启动日志输出一次；**不再使用 `admin/admin`**。

仅本地调试可关闭登录：

```bash
./dist/ci-platform-linux-amd64 -no-auth
```

数据目录、HTTPS、反向代理、环境变量等见 [环境变量](docs/configuration.md) 与 [快速开始](docs/getting-started.md)。

### 3. 打开控制台

浏览器访问：

```text
http://[IP]:8080
```

首次使用建议：

1. 在「系统管理 → 全局工具配置」确认 JDK / Node / Maven 等工具路径。
2. 在「凭据管理」添加 Git 凭据。
3. 新建任务，或使用「快速创建并立即构建」。
4. 需要 SSH 发布时，先在「系统配置 → SSH 服务器」维护目标。

### 4. 第一次构建

在任务页点击「立即构建」，或使用 CLI：

```bash
go build -o bin/ci ./cmd/ci
./bin/ci login --server http://127.0.0.1:8080 --token cip_xxx
./bin/ci build demo-app --wait -param VERSION=1.2.3
./bin/ci logs demo-app 1 --tail 200
```

CLI 与全部接口见 [API 与 CLI](docs/api.md)。

---

## 本地开发

环境要求：

- Go 1.24+
- Node.js 18+
- Git

```bash
# 后端
go run ./cmd/server

# 前端开发服务器（/api 与 /ws 代理到 :8080）
cd web
npm install
npm run dev
```

常用检查：

```bash
go test ./...
go vet ./...
cd web && npm run typecheck && npm test && npm run build
```

完整构建、交叉编译、版本注入和 UPX 说明见 [构建与验证](docs/build.md)。

---

## 文档

| 文档 | 内容 |
|---|---|
| [快速开始](docs/getting-started.md) | 单文件运行、工具配置、日志、产物、本地开发 |
| [环境变量](docs/configuration.md) | 启动参数与环境变量 |
| [构建与验证](docs/build.md) | `build.sh` / `build.bat`、交叉编译、版本注入、运行产物 |
| [架构与实现](docs/architecture.md) | 技术选型、文件系统存储、执行流程、目录结构 |
| [API 与 CLI](docs/api.md) | 统一响应体、接口清单、OpenAPI、CLI 用法 |
| [流水线与发布](docs/pipeline.md) | 参数、步骤控制、SSH 发布、部署记录、出站事件 |
| [Jenkins 对齐与边界](docs/jenkins.md) | 系统管理、任务页、配置页、明确不做的能力 |
| [从 Jenkins 迁移](docs/migration.md) | `config.xml` 导入、配置导出/导入、构建历史迁移 |
| [验证结果](docs/validation.md) | 功能场景与 e2e 覆盖 |

补充设计与审计文档：

| 文档 | 内容 |
|---|---|
| [DESIGN.md](docs/DESIGN.md) | 架构分层、关键机制、时序与实现权衡 |
| [COMPARISON.md](docs/COMPARISON.md) | 与 Jenkins / GitLab CI / GitHub Actions / Drone 的能力对比 |
| [FEATURE-GAPS.md](docs/FEATURE-GAPS.md) | 功能现状、缺口与优先级 |
| [PERF-AUDIT.md](docs/PERF-AUDIT.md) | Vue 3 前端性能审计 |
| [DOCKER-IMAGE-DESIGN.md](docs/DOCKER-IMAGE-DESIGN.md) | Docker 镜像打包方案（未实现） |

---

## 安全与运维要点

- 数据目录必须整体备份，**尤其要包含 `data/credentials.key`**；丢失后凭据与密码参数无法解密。
- 管理员密码以 bcrypt 存储；API Token 只保存 SHA-256 哈希。
- 生产环境建议开启 HTTPS，并通过反向代理限制 `/metrics` 与 `/openapi.json` 的来源。
- 构建日志中的平台已知密钥会自动遮罩，但步骤临时生成/外部注入的密钥无法识别。
- 明确不做插件系统、内置制品仓库、Groovy 沙盒、完整 GitOps/蓝绿/金丝雀等，详见 [Jenkins 对齐与边界](docs/jenkins.md)。

---

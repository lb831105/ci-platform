# 快速开始

[← 返回 README](../README.md)

## 方式 A：直接运行构建好的单文件（推荐）

前端已用 `go:embed` 内嵌进二进制，**部署时只需拷贝一个可执行文件**，无需 Node、无需静态资源目录。

```bash
# Linux
./dist/ci-platform-linux-amd64

# Windows
dist\ci-platform-windows-amd64.exe
```

默认监听 `:8080`，数据目录 `./data`。

数据目录（任务、构建记录、凭据、设置）可在**系统配置 → 主目录**中修改，修改后写入引导配置 `ci-platform.json`，**重启服务后生效**；保存时可选择把现有任务/凭据/设置迁移到新目录。也可在启动时通过 `-data-home` 或 `CI_DATA_HOME` 指定（优先级高于引导配置），或用 `-config` / `CI_CONFIG_FILE` 改变引导配置文件位置。
默认开启登录校验，初始账号 `admin`。**部署时请通过启动参数或环境变量指定初始密码**（仅首次初始化生效）；未指定时首次启动会生成随机密码并在启动日志中输出一次，不再使用 `admin/admin`：

```bash
./dist/ci-platform-linux-amd64 -auth-user ops -auth-password 'your-password'
# 等价环境变量：CI_AUTH_USER / CI_AUTH_PASSWORD
# 仅本地调试可关闭登录：-no-auth（或 CI_AUTH_ENABLED=false）
```

> 密码安全：管理员密码以 **bcrypt hash** 落盘（`auth_password_hash`），`data/settings.json` 中不保存明文；
> 旧版明文配置在首次启动时自动迁移。新密码至少 8 个字符。
>
> **改密码有两条路**：① 任何登录用户在「个人资料 → 修改密码」自助改（需填当前密码，改完其他会话立即失效）；
> ② 管理员在「系统管理 → 全局安全配置」直接设置。**初始密码只影响首次初始化**——改过之后，即使再次带着
> `-auth-password` 启动也不会被覆盖（以 `data/settings.json` 里的 hash 为准）。
> 登录页不展示任何初始凭据提示；若仍在使用初始密码，启动日志会提醒（不写出具体口令）。

浏览器访问 <http://127.0.0.1:8080>。

## 构建工具（全局工具配置）

「系统管理 → 全局工具配置」里每个工具（JDK / Maven / Gradle / Ant / Go / NodeJS / Git）有两种用法：

| 方式 | 说明 |
|---|---|
| **手动路径** | 填服务器上已装好的主目录（如 `/usr/lib/jvm/jdk-17`、`C:/Program Files/nodejs`）。构建时注入 `JAVA_HOME`/`MAVEN_HOME`/`GRADLE_HOME`/`ANT_HOME`/`GOROOT`/`NODE_HOME`，并把「主目录/bin」与主目录本身加入 `PATH`（Windows 版 node 的 `npm.cmd` 靠这条才能找到） |
| **自动安装** | 选版本由平台下载解压到 `data/tools/<工具>/<版本>/`，首次构建时按需下载（页面也有「立即下载」）。Git 需手动指定（系统依赖，不托管下载） |

预置版本（可在下拉里选，也可手填）：

| 工具 | 预置版本 |
|---|---|
| JDK（Temurin） | **1.8 = 8u422-b05**、11.0.25+9、17.0.12+7、21.0.4+7 |
| Node.js | 12.22.12、14.21.3、16.20.2、18.20.5、20.18.0、22.11.0、24.1.0（**支持手填任意补丁版本**，如 `18.20.4`、`v16.20.2`） |
| Maven | 3.9.9、3.9.6、3.8.8、3.6.3、3.5.4（老版本自动回退 Apache archive） |
| Gradle | 8.10.2、8.5、7.6.4、6.9.4、5.6.4 |
| Go | 1.23.2、1.22.8、1.21.13、1.20.14、1.19.13 |
| Ant | 1.10.14、1.10.13、1.9.16（老版本回退 archive） |

Node 12~24 的其它补丁版本不需要预置：下载地址是 `https://nodejs.org/dist/v{版本}/…` 的固定模板，
在版本框里直接输入 `18.20.4` 即可（带不带 `v` 都行）。

**每个任务可选自己的版本**（构建环境 → 构建工具版本）：例如 A 任务用 JDK 8、B 任务用 JDK 17，
不选则用全局配置里第一个可用安装项。构建时只会准备该任务真正用到的工具（选了该类别，或步骤里调用了
`java`/`mvn`/`npm`/`go` 等命令），不会因为预置了 5 个工具就全下载一遍。

新装实例的「全局工具配置」默认就填好了**自动安装**（JDK 17、JDK 8、Maven 3.9.9、Gradle 8.10.2、
Go 1.23.2、Ant 1.10.14、Node 20），首次构建按需下载，不需要先去装环境；想用服务器已有的工具，
把对应安装项改成「手动路径」即可。

自动安装是**后台任务**：点「立即下载」后接口立刻返回，下载/解压跑在服务端（不挂在浏览器请求上），
关掉页面、切换菜单、重新登录都不影响；页面会轮询 `GET /api/system/tools/tasks` 显示百分比，
重新打开「全局工具配置」还能看到进度并自动回填主目录。选定了版本就等于选定，构建时会等待（或复用）该安装任务后直接使用。

**Windows / Linux 兼容**（已按两边差异分别处理）：

| 场景 | Linux | Windows |
|---|---|---|
| `PATH` 变量名 | `PATH` | `Path`（不再同时写两个键，避免子进程读到空值） |
| Node 可执行文件 | `主目录/bin/npm` | `主目录/npm.cmd`（主目录优先加入 PATH） |
| Git 可执行文件 | `主目录/bin/git` | `主目录/cmd/git.exe`（PATH 同时含 `cmd/`、`bin/`） |
| 下载包格式 | `*.tar.gz` | `*.zip`（按文件魔数判断，不依赖扩展名；tar 符号链接在 Windows 上失败时跳过） |
| 安装目录名 | `17.0.12+7` → `17.0.12-7`（避开 Windows 非法字符） | 同左 |
| 路径校验 | 绝对路径 `/opt/...`、UNC `//host/share` | `C:/...`、`C:\...`、UNC |

排查 `npm: command not found` 这类问题：

- 构建日志开头会列出已解析的工具：`[tools] NodeJS 20.18.0 → /data/ci/tools/nodejs/20.18.0/node-v20.18.0-linux-x64`
- 手动填的路径不存在时提示：`[tools] NodeJS 的主目录不存在：…`
- 步骤里用到 `npm` 但 NodeJS 没有可用主目录时提前提示：`[tools] 步骤里用到 "npm"，但「全局工具配置 → NodeJS」没有可用的主目录…`
- 内网无法直连官方下载地址时：`-tools-mirror https://mirrors.example.com`（只替换 scheme+host，路径不变），或改用手动路径
- 下载失败会在页面上显示原因（HTTP 状态码/超时/解压失败），构建日志里同样有一行 `[tools] …`

## 平台日志（分级 + 按月轮转）

运行日志同时写到三处，级别分 `DEBUG / INFO / WARN / ERROR` 四档：

| 位置 | 内容 | 保留 |
|---|---|---|
| `data/logs/YYYY-MM/app-YYYY-MM-DD.log` | 全量平台日志（**一天一个文件**，一行一条；按月目录分层） | 默认 **30 天**，可用 `-log-retain-days` 调整（`0` = 永久保留）；过期文件逐日清理，空月份目录一并删除 |
| 控制台（stderr） | 同上，便于 systemd / journald 收集 | 由 journald 决定 |
| 内存环形缓冲（最近 2000 条） | 「系统管理 → 系统日志」页面，可按级别过滤 / 搜索 | 进程内存，重启清空 |

```bash
-log-level debug|info|warn|error     # 默认 info；debug 额外输出 Webhook 解析、RBAC 拒绝、调度入队、SSH 逐文件上传等
-tools-mirror https://mirrors.example.com   # 自动安装工具的下载镜像前缀
-log-retain-days 30                  # 默认 30 天；0 表示永久保留（一天一个文件，逐日清理）
# 等价环境变量：CI_LOG_LEVEL / CI_LOG_RETAIN_DAYS
```

目录结构（按天记录、按月轮换）：

```
data/logs/
├── 2026-10/                      ← 当月目录
│   ├── app-2026-10-24.log
│   └── app-2026-10-25.log        ← 今天，tail -f 这个
└── 2026-09/                      ← 上月目录（仍按天分文件）
    └── app-2026-09-30.log        ← 超过 30 天的会自动删除
```

日志行格式：`2026-10-25 20:30:01.123 INFO  消息`（多行消息自动缩进续行，保证仍能按行 grep）。
「系统日志」页面顶部会显示当前落盘文件与级别，方便直接去服务器 `tail -f`。

> 排查构建问题看**构建日志**（`data/jobs/{id}/builds/{n}/log.txt`）；平台日志记录的是"平台自己发生了什么"

## 产物归档模式（Archive the artifacts）

匹配语义以 Jenkins 为基准：`*` 不跨目录、`**` 跨任意层级，路径相对**工作区根目录**。

**但根目录没匹配到时会自动按 `**/<模式>` 再匹配一次**（日志注明「已按递归匹配处理」）：
多模块 Maven 工程把 jar 产在 `ruoyi-admin/target/` 下，写 `target/*.jar` 也能归档 ——
用户没有义务先学会 `**` 语法，因为目录层级导致构建失败属于产品缺陷。

| 工程结构 | 写法 | 结果 |
|---|---|---|
| 单模块 Maven（jar 在根 `target/`） | `target/*.jar` | 归档根 `target/` 下的 jar |
| **多模块 Maven**（jar 在子模块，如 `ruoyi-admin/target/`） | `target/*.jar` | 根目录没有 → 自动递归，归档**全部子模块**的 jar |
| 多模块但只想根目录那一份 | `target/*.jar`（根目录已有匹配） | 根目录优先，**不会**混入子模块 |
| 只要某个子模块 | `ruoyi-admin/target/*.jar` | 精确到模块 |
| 任意层级同名目录 | `**/target/*.jar` | 显式递归（不再触发回退） |
| Node 前端 | `dist/**` | 递归整目录 |

没匹配到文件时会**直接列出工作区里找到的同名文件并给出建议模式**：

```
[artifact] 没有匹配到任何文件：target/*.jar
工作区里找到同名文件（模式是相对工作区根目录匹配的）：
  - ruoyi-admin/target/ruoyi-admin.jar
如果它们就是想要的产物，把模式改成：**/target/*.jar（** 表示任意层级目录）
```

如果确实允许"这次没有产物"（多模块里某个模块没构建），可在产物配置里打开
**「没匹配到产物时不算失败」**（`allow_empty`），此时只打 WARN，构建保持成功。

同样的信息也会写进构建记录（`advice` 字段），任务页顶部出现一张修复卡片：
列出工作区里找到的 jar，并给出 **「一键改为 `**/target/*.jar`」** 按钮 —— 点一下改配置，再点构建即可。
> （启动、调度、Webhook、通知、状态回写等），两者不要混用。

> **构建依赖服务器本地工具链**：平台直接在运行服务的机器上执行构建步骤，不使用 Docker。
> 请确保服务器已安装 Go / JDK / Maven / Node.js / Gradle 等所需工具，并在「系统管理 → 全局工具配置」
> 中填写工具主目录（或让工具已在系统 `PATH` 中）。

## 方式 B：一键构建（Windows 上通过 Git Bash 运行）

```bash
./build.sh                     # 前端构建 + 交叉编译 linux/amd64 与 windows/amd64 + UPX 压缩
```

或使用 Make：

```bash
make release                   # 等价于 ./build.sh
make release ARGS="--no-upx"   # 透传参数
```

## 方式 C：本地开发

```bash
# 终端 1：后端（默认 :8080）
go run ./cmd/server

# 终端 2：前端开发服务器，/api 与 /ws 自动代理到 :8080
cd web && npm install && npm run dev     # http://127.0.0.1:5173
```

> ⚠️ `go:embed` 在**编译期**要求 `web/dist/index.html` 存在。因此 clone 后若直接 `go build`，
> 需先执行 `cd web && npm install && npm run build`（仓库中已保留一份前端产物，通常开箱即可编译）。

---

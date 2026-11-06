# Docker 镜像打包 — 设计方案（只做设计，不含实现）

> 目标读者：维护本平台的开发 / 需要评审此需求的运维。
> 本文档只描述**怎么开发合适**，不包含代码改动。落地时按「第 13 节 分期」推进。

---

## 1. 目标与非目标

### 1.1 目标

| 编号 | 目标 |
|---|---|
| G1 | 构建成功后，按工作区里的 `Dockerfile` 打出镜像，按规范打 tag |
| G2 | 可推送到私有 registry，**凭据不落命令行、不落日志、不残留** |
| G3 | 把**镜像 digest** 记录进 `build.json`，作为「测的就是发的」唯一凭据 |
| G4 | 镜像信息能在构建详情页看到（仓库、tag、短 digest、`docker pull` 命令） |
| G5 | 后续 SSH 发布能直接引用本次镜像（注入 `CI_IMAGE` 等变量，供 `docker compose pull` / `kubectl set image` 使用） |
| G6 | 与现有能力不冲突：产物归档、多目标 SSH 发布、通知、审批门、导入导出、审计 |

### 1.2 非目标（明确不做，避免过度设计）

| 不做 | 原因 |
|---|---|
| 把构建执行改成「跑在容器里」 | 平台定位是本地执行（工具链装在服务器上）。镜像打包是**产物形态**，不是执行环境 |
| 内置镜像仓库 | registry 是基础设施，平台只做 client |
| 镜像漏洞扫描 / 签名 / SBOM | 一期不做，见第 13 节三期 |
| 通过 Docker Engine API 直连 daemon | 见 3.2 选型：CLI 更简单、日志更自然、无重量级依赖 |
| 解析用户的 Dockerfile 做依赖分析 | 越界，交给 docker 自己 |

---

## 2. 定位：镜像打包是「打包阶段」，不是「执行阶段」

现有构建流程（`internal/service/build_service.go` 的 `executeStages`）已经是一条固定顺序：

```
Git 签出 → 本地步骤(Steps) → 归档产物(Artifacts) → SSH 发布(多目标) → 通知
```

镜像打包应插入为**步骤之后、归档之前**的新阶段：

```
Git 签出 → 本地步骤 → 【Docker 镜像打包/推送】 → 归档产物 → SSH 发布 → 通知
```

**为什么在归档之前**：镜像阶段可能在工作区写出部署用文件（如渲染后的 `docker-compose.yml`、`kustomization.yaml`、`image-digest.txt`），归档要能收走它们。
**为什么在 SSH 发布之前**：发布要引用本次镜像（`CI_IMAGE`），顺序不能反。
**为什么不做成"步骤"**：见 3.1。

---

## 3. 三个关键选型

### 3.1 形态：结构化的「构建后操作」而不是「内置步骤」

| 方案 | 优点 | 缺点 | 结论 |
|---|---|---|---|
| A. 内置步骤类型（`step.type = docker_build`） | 能和其他步骤混排 | 步骤模型是「一次性命令 + 退出码」，塞入结构化字段会把步骤编辑器变成表单怪物；步骤级 `when/retry` 对"打包"没有意义 | ❌ |
| **B. 构建后操作（与「归档产物」「SSH 发布」并列）** | 结构化字段可校验、可导入导出、可注入变量、可在 UI 呈现 digest | 位置固定（不能插在步骤中间） | ✅ **推荐** |
| C. 只让用户自己在 shell 步骤写 `docker build` | 零开发 | 平台拿不到 digest/tag，无法参与发布与审批；日志里容易出现明文密码 | 保留为「逃逸通道」，但不是功能形态 |

**结论**：新增构建后操作 `DockerImage`，同时**不禁止**用户在 shell 步骤里自己 `docker build`（只是平台不会记录它的镜像信息）。

### 3.2 构建器：默认 `docker` CLI，抽象成接口

| 方案 | 依赖 | 日志体验 | 多架构 | 结论 |
|---|---|---|---|---|
| **① `docker` CLI（默认）** | 宿主机装 docker | 子进程输出直接进现有日志管道 | 需要 buildx | ✅ 一期 |
| ② Docker Engine API（unix socket） | 无 CLI，但需 HTTP 客户端 + 流式解析 | 需自研进度解析 | 复杂 | ❌ 收益不抵成本 |
| ③ Kaniko / Buildah | 无需 daemon、无需特权 | 输出可读 | 支持 | ✅ 三期可选（平台自身跑在容器里时的推荐路径） |

实现时定义接口（示例，仅示意分层）：

```
type ImageBuilder interface {
    Build(ctx, spec ImageSpec, out io.Writer) (ImageResult, error)
    Push(ctx, spec ImageSpec, out io.Writer) (ImageResult, error)
}
```

一期只给 `DockerCLIBuilder` 一个实现；`docker` 可执行文件路径放到系统配置（默认 `docker`，测试时可指向假脚本，见第 12 节）。

**必须在执行前检测而不是保存时检测**：保存任务时服务器上有没有 docker 不代表执行时有（可能换机器/换 PATH）。执行时缺失要给中文明确提示（见第 11 节）。

### 3.3 触发位置：构建成功才执行，失败即短路

- 仅在**所有步骤 exit code = 0** 之后执行（与归档、发布一致）。
- 镜像阶段失败 → 本次构建 `failed`，**后续阶段（归档、SSH 发布）不执行**。

> 注意与「多目标 SSH 发布」的语义区别：多目标是**同一阶段内的尽力而为**（发完所有目标再汇总），而跨阶段必须**快速失败**——镜像没打出来还去发布，就是把上次的旧镜像又发一遍。

---

## 4. 数据模型

### 4.1 任务配置（`data/jobs/{id}/config.json` 新增字段）

```jsonc
{
  "docker_image": {
    "enabled": true,
    "dockerfile": "Dockerfile",          // 相对工作区
    "context": ".",                      // 相对工作区；禁止逃逸
    "repository": "registry.example.com/${PROJECT_NAME}",  // 模板
    "tags": ["build-${BUILD_NUMBER}", "${BRANCH}"],        // 模板，至少一个不可变 tag
    "build_args": [{ "key": "VERSION", "value": "${VERSION}" }],
    "target": "",                        // multi-stage 目标阶段（可空）
    "platforms": [],                     // 空 = 宿主架构；多值需 buildx
    "no_cache": false,
    "pull": true,                        // --pull，保证基础镜像最新
    "push": true,
    "credential_id": "cred-xxxx",        // 复用凭据管理（username_password / secret_text）
    "clean_local": true,                 // 构建后删除本地镜像
    "timeout_sec": 1800                  // 0 = 沿用构建级超时
  }
}
```

### 4.2 构建记录（`build.json` 新增字段）

```jsonc
{
  "image": {
    "repository": "registry.example.com/order-api",
    "tags": ["build-128", "main"],
    "reference": "registry.example.com/order-api:build-128",
    "digest": "sha256:9f2c…",            // 推送后才有；未推送时为空
    "push": true,
    "pushed": true,
    "platforms": ["linux/amd64"],
    "size_bytes": 187654321,
    "built_at": "2024-10-24T12:00:00Z",
    "duration_ms": 42310
  }
}
```

**digest 是这一期的核心产出**：没有 digest，"测试通过就能上生产"就退化成"测的是 build-128，发的也是 build-128，但没人知道镜像层有没有被覆盖"。

### 4.3 系统配置（`settings.json` 新增）

```jsonc
{
  "docker": {
    "enabled": true,                     // 全局开关：关闭后所有任务的镜像阶段直接跳过并告警
    "binary": "docker",                  // 可执行文件（或绝对路径，测试可用假脚本）
    "default_registry": "registry.example.com",
    "insecure_registries": [],           // 仅自签/HTTP 环境使用，默认空
    "login_timeout_sec": 60
  }
}
```

沿用现有 `data/settings.json`，不引入新的存储文件、不引入 DB。

---

## 5. 执行流程

```
① 渲染配置：repository / tags / build_args 用模板变量渲染（见第 6 节）
② 校验：渲染后的仓库名、tag 合法性；Dockerfile 存在；context 在工作区内
③ 认证：若 push=true 且配置了凭据 → 独立 DOCKER_CONFIG 目录 + docker login --password-stdin
④ 构建：docker build --file … --tag … [--build-arg …] [--target …] [--platform …] --progress=plain
⑤ 取 digest
   - push=false：记录 image ID，digest 置空，日志提示「未推送，无法获得 digest」
   - push=true ：docker push 后 docker inspect --format '{{json .RepoDigests}}' 取 sha256
                 （buildx 场景也可用 --metadata-file 直接拿）
⑥ 清理：clean_local=true → docker rmi 本次所有 tag
⑦ 登出/清理：删除临时 DOCKER_CONFIG 目录（关键：不残留登录态）
⑧ 记录：写 build.json，注入后续阶段变量 CI_IMAGE / CI_IMAGE_DIGEST / CI_IMAGE_TAGS
```

日志分段（便于 grep，与现有 `[git]` `[ssh]` 风格一致）：

```
[docker] build context=. dockerfile=Dockerfile
[docker] repo=registry.example.com/order-api tags=build-128,main
[docker] login registry.example.com as deploy (密码已隐藏)
[docker] Step 1/12 : FROM eclipse-temurin:17-jre ...
[docker] built image sha256:1a2b… in 41.3s
[docker] pushed registry.example.com/order-api:build-128
[docker] digest sha256:9f2c…
```

---

## 6. 命名与标签规范

### 6.1 模板变量（白名单，未知变量直接报错）

| 变量 | 值 |
|---|---|
| `${PROJECT_NAME}` | 任务名（建议只取安全字符，见 6.2） |
| `${BUILD_NUMBER}` | 构建号（`build.ID`） |
| `${BRANCH}` | 实际签出分支（`build.Branch`，非 Git 任务为空） |
| `${COMMIT}` / `${COMMIT_SHORT}` | 提交全/短 SHA |
| `${PARAM_NAME}` | 已定义构建参数（如 `${VERSION}`） |
| `${DATE}` / `${TIMESTAMP}` | `20241024` / `20241024-1200` |

**未知变量必须报错**，不能静默留空：`order-api:${BUILD_NUMBR}` 静默留空会变成 `order-api:`，docker 会直接失败或产生奇怪 tag。

> 现有通知模板用的是 `$PROJECT_NAME` 风格（`build_service.go` 的 `renderSubject`）。建议抽一个统一的 `renderTemplate(tpl, vars)` 供两边使用，**但注意两者的错误策略不同**：通知里未知占位符保留原样（便于发现拼写），镜像 tag 里必须拒绝。

### 6.2 校验规则（业务规则只允许放在 service 层）

| 规则 | 说明 |
|---|---|
| repository 段必须小写 | 自动 `strings.ToLower`，日志提示「已转为小写」 |
| repository 长度 ≤ 255 | 超长报错 |
| tag ≤ 128 且匹配 `^[A-Za-z0-9_][A-Za-z0-9._-]*$` | 渲染后校验，报错指出是哪个变量产生的非法值 |
| **至少一个不可变 tag** | 未包含 `${BUILD_NUMBER}` / `${COMMIT}` 时自动追加 `build-${BUILD_NUMBER}` 并提示；不硬性拒绝 |
| tag 数量 ≤ 5 | 防止误配导致推送风暴 |
| `dockerfile` / `context` 不得逃逸工作区 | 与产物归档同样校验 `..` 与绝对路径 |
| `platforms` 非空时必须是 `os/arch` 形式 | 且最多 3 个；无 buildx 时执行期报错 |

---

## 7. 凭据与安全

| 项 | 做法 | 理由 |
|---|---|---|
| 登录方式 | `docker login -u <用户> --password-stdin <registry>`，密码通过 stdin 传入 | 命令行参数会出现在 `ps` 与日志中 |
| secret_text 凭据 | 用户名固定 `oauth2`（与 Git token 一致） | 各 registry（Harbor、GHCR、ACR）都接受 |
| 登录态隔离 | 每次构建 `DOCKER_CONFIG=$(mktemp -d)`，结束删除整个目录 | 不会污染 `~/.docker/config.json`，也不会让下一个任务继承登录态 |
| 日志打码 | 复用 `internal/pkg/mask.go`；`login` 只打印 registry 与用户名 | 凭证绝不进日志 |
| build-arg 中的敏感值 | 文档明确：**不要用它传密钥**（会进 `ps`、进镜像 history）。敏感值用 `--mount=type=secret`（buildx）或运行时注入 | 这是 Dockerfile 层面的既有事实，文档要写清 |
| 平台跑在容器里 | **不要挂 `docker.sock`**（等于给容器 root）；改用 ③ kaniko/buildkit 或独立 runner 主机 | 安全红线，写进文档与 README 警告 |
| 审计 | 镜像推送属于「发布类动作」，记入 `audit.log`（任务、构建、image、digest、操作人） | 与 SSH 发布同等对待 |

---

## 8. 缓存与磁盘治理

| 项 | 一期 | 二期 |
|---|---|---|
| 层缓存 | 默认使用 docker 本地层缓存；`no_cache` 可关 | `--cache-from` 上一次的不可变 tag |
| 远端缓存 | 不做 | buildx `--cache-to type=registry`（要求 registry 支持） |
| 本地镜像清理 | `clean_local=true` → `docker rmi` 本次 tag | 「保留最近 N 个本地镜像」全局策略（后台定时清理，复用现有 janitor 思路） |
| 磁盘可见性 | 不做 | 系统信息/负载统计展示 `docker system df` 摘要 |

> 一期必须做 `clean_local`：单机 CI 的镜像层会以每周数 GB 的速度吃掉磁盘，这是最容易被忽略的运维事故。

---

## 9. UI 设计

### 9.1 任务配置页（`PipelineEditView` → 构建后操作）

新增卡片「Docker 镜像打包」：

- 启用开关
- Dockerfile 路径 / 构建上下文（并排两个输入）
- 镜像仓库（模板），占位示例 `registry.example.com/order-api`，下面实时预览渲染结果
- 标签（可增删的多行），右侧显示「至少一个不可变标签」的提示或已自动补的 `build-N`
- 构建参数（键值表，值支持模板）
- 目标阶段 / 平台架构（折叠在「高级」里）
- 推送开关 + 凭据下拉（只列 `username_password` / `secret_text`）+ 「不校验 TLS」开关（默认关，需二次确认）
- 构建后清理本地镜像（默认开）
- 超时（秒，0 = 沿用构建级，上限与步骤级一致）

### 9.2 构建详情页（`BuildLogView`）

新增「镜像」卡片：

```
registry.example.com/order-api:build-128   [已推送]
digest  sha256:9f2c1a…（点击复制）
大小    179 MB · 平台 linux/amd64 · 耗时 42s
docker pull registry.example.com/order-api@sha256:9f2c1a…
```

### 9.3 构建历史列表

状态列旁加一个小图标/标签「含镜像」，悬停显示短 digest。

---

## 10. 与既有能力的关系

| 能力 | 关系 |
|---|---|
| **产物归档** | 镜像不进 `artifacts/`（在 registry 里）；只把 `image-digest.txt` 之类小文件归档可选 |
| **多目标 SSH 发布** | 环境变量注入 `CI_IMAGE`（`repo:build-128`）、`CI_IMAGE_DIGEST`、`CI_IMAGE_TAGS`；远程命令可直接 `docker compose pull && up -d` 或 `kubectl set image deploy/x x=$CI_IMAGE_DIGEST` |
| **发布审批门（上一轮设计）** | 审批记录里同时展示 `image.reference + digest`，「批的就是这一份」才成立 |
| **参数化构建** | `tags` / `build_args` 可用 `${VERSION}` 等参数；参数仍在触发时快照 |
| **步骤级控制** | 不适用于镜像阶段（它是构建后操作）；但给出整体 `timeout_sec` |
| **通知** | 通知模板新增占位变量 `${IMAGE}` / `${IMAGE_DIGEST}`（未知占位符在通知里保留原样，见 6.1 注） |
| **导入 / 导出** | `config.json` 导出包含 `docker_image` 配置但**不含密码**（只带 `credential_name`），与现有 SSH/凭据策略一致 |
| **RBAC** | 一期沿用 `job.configure`（能改配置就能改推送目标）；若需「谁能推生产 registry」独立控制，二期加 `job.publish` |

---

## 11. 失败语义与错误提示（中文文案清单）

| 场景 | 提示 |
|---|---|
| 服务器上无 docker | `未找到 docker 命令（当前查找路径：PATH）。请在构建服务器安装 Docker，或在「系统配置 → Docker」中填写可执行文件绝对路径` |
| Dockerfile 不存在 | `构建上下文 . 中未找到 Dockerfile（配置值：Dockerfile）` |
| 模板变量未知 | `镜像标签 "${BUILD_NUMBR}" 中的变量未定义，可用变量见任务配置页说明` |
| 渲染后 tag 非法 | `镜像标签 "release/1.0" 不合法：tag 不能包含 /（来自模板 "${BRANCH}"）` |
| 未配置凭据却要推送 | `推送镜像需要 registry 凭据，请在任务配置里选择凭据（系统管理 → 凭据管理）` |
| 登录失败 | `registry 认证失败：unauthorized（凭据 cred-x 的用户名或密码可能已失效）` |
| 推送被拒（无权限/仓库不存在） | `推送失败：denied: requested access to the resource is denied（请确认 registry 已创建该项目且账号有 push 权限）` |
| 磁盘不足 | `构建失败：no space left on device（可用「系统管理 → 管理旧数据」或清理本地镜像）` |
| 多架构但无 buildx | `配置了多个平台但当前 docker 未安装 buildx 插件` |
| 超时 | `镜像构建超时（1800 秒），已终止` （退出码沿用步骤级约定 124） |

**关键原则**：把 docker 原始错误**原文贴进日志**（便于搜索），同时给一行中文「人话摘要」。

---

## 12. 测试策略

| 层级 | 内容 |
|---|---|
| 纯函数单测 | 模板渲染（含未知变量报错）、repository/tag 规范化与校验、`docker build` argv 构造（返回 `[]string` 便于断言，不实际执行）、`RepoDigests` 解析、日志打码 |
| 假 CLI 集成测试 | 在临时目录写一个假的 `docker` 脚本（把 argv 逐行打印并生成 `iidfile` / metadata 文件），把 `settings.docker.binary` 指向它 → **无需真实 docker 即可验证命令拼装、超时、失败短路、digest 解析** |
| 真实 E2E（可选，环境无 docker 则 skip） | `registry:2` 起本地仓库：构建 tiny Dockerfile → push → 断言 `build.json` 里的 digest 与 registry 返回一致；再断言错误路径（错密码、无权限仓库） |
| 回归 | 现有 `go test ./...`、`vue-tsc`、`build.sh` 全绿；`config.json` 导出→导入往返保留 `docker_image` |

> 假 CLI 是关键：CI 机器上一般没有 docker，没有这层测试，镜像功能就只能靠手工验证。

---

## 13. 分期与工作量

| 期 | 内容 | 估工 |
|---|---|---|
| **一期（MVP）** | 数据模型 + 校验 + docker build/tag/push + digest 记录 + 日志分段 + 环境变量注入 + 配置 UI + 构建详情卡片 + 单测/假 CLI 测试 | 6–8 人日 |
| **二期** | buildx 多架构 + registry 缓存 + 本地镜像保留策略 + `docker system df` 展示 + Jenkins `DockerBuilder` 导入映射 + 导出字段兼容 + 通知模板变量 | 4–6 人日 |
| **三期（按需）** | kaniko/buildkit 无特权构建（平台自身容器化时）+ 漏洞扫描(trivy) + 签名/ SBOM(cosign) + 推送后回调中台发布单 | 8–12 人日 |

一期就能支撑完整链路：
`步骤(编译测试) → 镜像打包推送 → SSH 发布(compose pull) → 审批门放行 → 生产用同一 digest 发布`

---

## 14. 风险与开放问题

### 14.1 风险

| 风险 | 影响 | 缓解 |
|---|---|---|
| 宿主机 docker 权限 ≈ root | 平台进程被攻破即宿主被控 | 平台与构建机同机部署时限制操作人数；容器部署时**禁止挂 socket**，走三期 kaniko |
| 镜像层吃磁盘 | 磁盘打满导致全部构建失败 | 一期 `clean_local` + 错误提示指向「管理旧数据」；二期保留策略 |
| registry 不可用 | 推送失败 → 构建 failed | 明确「已构建未推送」状态，允许「重新推送」（复用现有 republish 思路） |
| tag 覆盖 | `latest` 被覆盖后无法复现 | 强制不可变 tag + 记录 digest + 详情页只推荐用 digest 拉取 |
| 与「无 Docker 依赖」的旧定位冲突 | 文档/用户预期混乱 | README 明确：**构建用本地工具链，镜像打包是可选产物** |

### 14.2 待确认（开发前需产品/运维拍板）

1. 私有 registry 是哪个（Harbor / ACR / GHCR / 自建）？是否需要先建项目？
2. 是否需要多架构（arm64）？若要，服务器是否已装 buildx + QEMU？
3. 生产部署方式：`docker compose pull` 还是 K8s `kubectl set image`？（决定 `CI_IMAGE_DIGEST` 的用法示例）
4. tag 规范：以 `build-N` 为主还是 `commit sha` 为主？是否允许 `latest`？
5. 平台自身是否跑在容器里（决定一期能否依赖宿主机 docker，还是直接上三期 kaniko）
6. 是否需要把 digest 回写中台/OA 的发布单（决定要不要在事件里带 image 信息）

---

## 15. 验收清单（一期）

- [ ] 任务配置可保存/编辑镜像配置，非法模板与非法 tag 在保存时被拒绝
- [ ] 构建成功后日志有 `[docker]` 分段，末尾有 digest 行
- [ ] `build.json` 含 `image.digest`，详情页可复制 `docker pull …@sha256:…`
- [ ] 推送凭据不出现在日志、`ps`、`~/.docker/config.json` 中
- [ ] 镜像阶段失败 → 构建 failed 且**不执行**归档与 SSH 发布
- [ ] SSH 发布远程命令能用 `$CI_IMAGE` / `$CI_IMAGE_DIGEST`
- [ ] `config.json` 导出→导入往返保留镜像配置且不含密码
- [ ] 无 docker 环境下有假 CLI 测试覆盖；`go test ./...` / `vue-tsc` / `build.sh` 全绿
- [ ] 镜像阶段超时可控（默认沿用构建级超时，可单独配置）

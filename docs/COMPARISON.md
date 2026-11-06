# 功能对比与差距规划（对标 Jenkins / GitLab CI / GitHub Actions / Argo / Tekton 等）

> 目的：把「我们现在有什么」与「主流开源 / 大厂工具做到什么」放在同一张表上，
> 让每一条差距都有明确的**为什么做、最小实现、改动模块、优先级**，方便排期开发。
>
> 对比口径：Jenkins 2.4xx + Blue Ocean、GitLab CI 17.x、GitHub Actions（2024）、
> Argo Workflows 3.5 / Argo CD 2.11、Tekton Pipelines 0.5x、Drone 2.x（Harness）、
> CircleCI、TeamCity 2024、Buildkite、Harness。开源工具按**原生能力**计，不含第三方插件市场。
>
> 图例：`✅` 原生支持 · `◐` 部分/受限 · `◻` 依赖插件或外部系统 · `✗` 不支持 · `—` 不适用

---

## 一、总览矩阵

| 能力域 | 本平台 | Jenkins | GitLab CI | GitHub Actions | Argo WF/CD | Tekton | TeamCity | Buildkite |
|---|---|---|---|---|---|---|---|---|
| 流水线定义 | ◐ 纯 UI（步骤列表） | ◻ Jenkinsfile + 插件 | ✅ `.gitlab-ci.yml` | ✅ workflow YAML | ✅ YAML（CRD） | ✅ CRD YAML | ✅ Kotlin DSL | ✅ YAML + 动态生成 |
| 参数化构建 | ✅ string/bool/choice/text + 快照 | ✅ | ✅ variables | ✅ inputs | ✅ parameters | ✅ params | ✅ | ✅ env |
| 并行 / DAG | ✗（任务级并行，步骤串行） | ✅ parallel/stage | ✅ needs/DAG | ✅ needs | ✅ DAG 为一等公民 | ✅ DAG | ✅ 依赖图 | ✅ |
| 矩阵构建 | ✗ | ◻ matrix 插件 | ✅ `parallel:matrix` | ✅ `matrix` | ✅ withItems | ✅ matrix | ✅ | ◻ 动态流水线 |
| 步骤级控制（超时/重试/允许失败/when） | ✅ timeout/retry/allow_failure/when | ✅ timeout/retry/catchError | ✅ timeout/retry/allow_failure | ✅ `continue-on-error`/`timeout-minutes` | ✅ retryStrategy | ✅ retries/onError | ✅ | ✅ |
| 执行器 | ◐ 单机本地进程 | ✅ agent（SSH/K8s/Docker） | ✅ shared/self-managed runner + K8s | ✅ hosted/self-hosted runner | ✅ K8s Pod | ✅ K8s Pod | ✅ agent pool | ✅ 自建 agent 集群 |
| 容器化构建 | ✗ | ◻ Docker 插件 | ✅ image/services | ✅ container | ✅ 原生 | ✅ 原生 | ◻ | ✅ 插件 |
| 工作区缓存 | ◐ 复用工作区（等价本地缓存） | ◻ 插件 | ✅ cache | ✅ cache | ✅ artifact/volume | ✅ workspace | ✅ | ✅ 插件 |
| 构建触发 | ✅ 手动 / Webhook / cron(`H`) | ✅ 全类型 | ✅ 全类型 + MR/merge train | ✅ 全类型 + `schedule` | ✅ cron/sensor | ✅ Trigger/EventListener | ✅ VCS trigger | ✅ |
| SCM 回写（Commit Status / PR 检查） | ✗ | ◻ 插件 | ✅ 原生 | ✅ 原生（Checks API） | ◐ Argo CD 回写 | ◻ | ✅ | ✅ |
| 多分支 / PR 流水线 | ✗（一分支一任务） | ✅ multibranch | ✅ MR pipeline | ✅ PR workflow | —（GitOps 侧） | ✅ | ✅ | ✅ |
| 制品归档 | ✅ 按构建独立 + SHA256 + ZIP + 模式自动递归兜底 | ✅ | ✅ artifacts + reports | ✅ artifacts | ✅ artifact repo | ✅ results | ✅ | ✅ |
| 外部制品库 / 镜像仓库 | ✗ | ◻ | ✅ 内置 Registry | ✅ GHCR | ✅ S3/GCS/OSS | ✅ | ◻ | ◻ |
| 测试报告 / 覆盖率 | ✗ | ◻ JUnit/coverage 插件 | ✅ 原生 reports | ◻ 需第三方 action | ◻ | ◻ | ✅ 原生 + flaky 检测 | ✅ Test Analytics |
| 代码扫描 / 供应链安全（SBOM、签名、漏洞） | ✗ | ◻ 插件 | ✅ SAST/DAST/Dependency/SBOM | ✅ CodeQL/Dependabot/Attestation | ◻ Chains 项目 | ✅ Tekton Chains | ◻ | ◻ |
| 部署环境 / 发布编排 | ◐ 多目标 SSH 发布 + 远程命令 | ◻ 插件 | ✅ environments/deployments | ✅ environments + protection | ✅ Argo CD（GitOps） | ◻ | ◻ | ◻ |
| 人工审批门 | ✗ | ◻ input 步骤 | ✅ manual job / approval rules | ✅ required reviewers | ✅ sync 审批（PR/UI） | ◻ | ✅ | ✅ block step |
| 回滚 | ◐ 历史产物重新发布 | ◻ | ✅ 重新部署旧 job | ◐ 重跑历史 | ✅ rollback 到 revision | ◻ | ◻ | ◻ |
| 指标 / 可观测（Prometheus、DORA） | ✗ | ◻ 插件 | ✅ 内置 | ◻ Insights（部分） | ✅ | ◻ | ✅ | ✅ Insights |
| 认证与 SSO | ◐ 本地账号（bcrypt） | ◻ LDAP/OIDC 插件 | ✅ LDAP/SAML/OIDC | ✅ SAML/OIDC/SCIM | ✅ Dex/OIDC | ◻ | ✅ LDAP/SAML | ✅ SAML/SCIM |
| 细粒度权限 | ✅ 全局 + 任务角色（Pattern/模板） | ✅ matrix authz | ✅ 角色 + 保护分支 | ✅ 环境/仓库级 | ✅ AppProject RBAC | ◻ | ✅ | ✅ Teams |
| 密钥管理 | ◐ 凭据库（HTTP 回显脱敏） | ✅ Credentials + Binding | ✅ masked/protected vars + Vault | ✅ Secrets + OIDC 联邦 | ✅ Secrets/Sealed | ✅ | ✅ | ✅ Secrets |
| 密钥日志遮罩 | ✗（步骤打印的内容原样落盘） | ✅ `Mask Passwords` | ✅ masked variables | ✅ `::add-mask::` | ◐ | ◻ | ✅ | ✅ |
| 操作审计 | ✅ JSONL + 轮转 + UI | ◻ 插件 | ✅ Audit Events | ✅ Audit log | ◐ | ◻ | ✅ | ✅ |
| 通知（多渠道 + 值班/升级/抑制/路由） | ✅ 邮件/钉钉/企微/Slack + 值班轮值 + 连败升级 + 抑制窗口 + 分支路由 + 投递记录 | ◻ 插件（各自为政） | ◐ 邮件/Slack/Webhook | ◐ 通知 + 第三方 action | ◐ | ◻ | ◐ | ◐ |
| 高可用 / 水平扩展 | ✗（单进程 + 内存会话） | ✅ controller/agents | ✅ | ✅（托管） | ✅ | ✅ | ✅ | ✅ |
| 部署形态 | ✅ **单文件 + 内嵌 UI + 零依赖** | ◻ WAR + JVM + 插件 | ◻ 重量级套件 | —（SaaS） | ◻ 需 K8s | ◻ 需 K8s | ◻ 服务端 + agent | ◻ 服务端 + agent |

**一句话结论**：本平台在「**单机、零依赖、单文件交付**」这个约束下，已经把 **流水线执行 + 触发 + 权限 + 审计 + 制品 + 告警运营** 做成了闭环，
并且**通知运营能力（值班/升级/抑制/路由/投递记录）明显强于 Jenkins/GitLab 的原生能力**；
参数化构建、步骤级控制（超时/重试/允许失败/when）与多目标 SSH 发布已补齐，
主要差距集中在 **流水线表达力（pipeline-as-code / 并行 / DAG / 矩阵）**、**SCM 双向集成（PR 校验与状态回写）**、
**质量与安全数据（测试报告/扫描/制品仓库/密钥遮罩）**、**执行器扩展（agent/容器）** 这四块。

---

## 二、分维度详细对比

### 2.1 流水线定义与表达力

| 能力 | 本平台 | Jenkins | GitLab CI | GitHub Actions | 说明 / 差距定位 |
|---|---|---|---|---|---|
| 定义方式 | UI 表单（step = name + shell） | Jenkinsfile（Groovy） | YAML | YAML | 我们零学习成本，但不可版本化、不可复用 |
| Pipeline-as-code | ✗ | ✅ | ✅ | ✅ | **P0**：YAML 导入/导出 + `config.json` 双读，UI 仍是主入口 |
| 参数化构建 | ✅ string/bool/choice/text，值注入 env + `CI_PARAM_*`，快照进构建记录 | ✅ | ✅ | ✅ | 已实现（`normalizeParams` / `ResolveBuildParams`） |
| 并行 / DAG | ✗ | ✅ | ✅ needs | ✅ needs | **P0**：前后端同时构建、测试分片 |
| 矩阵 | ✗ | ◻ | ✅ | ✅ | **P1**：多 JDK/多平台组合 |
| 步骤级 timeout / retry / allow_failure / when | ✅ 单次超时（124）、额外重试≤5、允许失败、success/failure/always | ✅ | ✅ | ✅ | 已实现（`local_executor.go` + 高级选项） |
| 配置迁移 | ✅ Jenkins config.xml 一键导入 + 平台 config.json 导出/导入（脱敏、按名对齐、告警分级、dry-run） | ◐ config.xml 手工 createItem，无历史 | ◐ 需脚本/API 组合 | ◐ 需脚本 | Jenkins 无迁移 API；我们把「导出→导入」做成内建能力，并保证导出文件不含密钥 |
| 复用与模板 | ◐ Copy from（前端复制整份配置） | ◻ shared library | ✅ include/extends | ✅ reusable workflow | **P1**：任务模板库 + 变量继承 |
| 手动审批 | ✗ | ◻ input | ✅ | ✅ reviewers | **P0/P1**：生产发布必须有人点确认 |
| 上游/下游触发（build chain） | ✗ | ✅ | ✅ trigger | ✅ workflow_run | **P2**：编译 → 部署 → 回归 的链式触发 |

### 2.2 执行器、资源与缓存

| 能力 | 本平台 | Jenkins | GitLab CI | GitHub Actions | 说明 |
|---|---|---|---|---|---|
| 执行位置 | 服务进程本地 shell | agent / K8s / Docker | runner（含 K8s） | runner（托管/自建） | 单机是产品定位，也是最大扩展瓶颈 |
| 并发模型 | 全局 worker（默认 3）+ 任务内串行 | 执行器数 + 标签 | concurrent + resource_group | concurrency + matrix | 我们已有 per-job 串行（避免工作区竞争），缺 **per-job 并发上限与 cancel-in-progress** |
| 资源限制（CPU/内存） | ✗ | ◻ | ✅ K8s resources | ✅ | P2（需容器/远程执行器） |
| 队列与优先级 | ◐ FIFO + 容量上限（满了 40900） | ✅ 标签/优先级 | ✅ resource_group | ✅ concurrency | P1：紧急发布插队 |
| 缓存 | ◐ 工作区复用（依赖目录留在原地） | ◻ | ✅ cache key | ✅ cache | P1：显式缓存（key + 路径 + 跨工作区） |
| 日志上限 | ✅ `log_max_mb` 截断（不失败） | ◻ | ✅ 限制/归档 | ✅ | 我们的做法比多数工具更「不伤构建」 |
| 取消构建 | ✅ 进程树级终止 + `aborted` 语义 | ✅ | ✅ | ✅ | 已对齐 Jenkins 语义（aborted ≠ failed） |
| 构建删除 | ✅ 单次构建 + 目录（日志/产物） | ✅ | ✅ | ✅（保留策略） | 已对齐，且编号不回退 |

### 2.3 SCM 与触发

| 能力 | 本平台 | Jenkins | GitLab CI | GitHub Actions | 说明 |
|---|---|---|---|---|---|
| 入站 Webhook | ✅ 三头兼容（自定义 / GitLab Token / GitHub HMAC） | ◻ | ✅ | ✅ | 兼容性做得好，但只有「触发」 |
| 分支/路径过滤 | ✗（固定分支，一分支一任务） | ✅ | ✅ rules:changes | ✅ paths/branches | **P0**：至少支持「分支正则 + 路径过滤」 |
| MR/PR 流水线 | ✗ | ✅ | ✅ | ✅ | **P0/P1**：合并前校验是 CI 的核心价值 |
| Commit Status / Check 回写 | ✗ | ◻ | ✅ | ✅ | **P0**：结果必须回到代码平台，否则等于没接入 |
| 多分支发现 | ✗ | ✅ | ✅ | ✅ | **P1**：自动为分支建子任务（或按分支参数化） |
| Tag / Release 触发 | ✗ | ✅ | ✅ | ✅ | P1：发版打 tag 即构建 |
| 浅克隆 / 增量 | ✅ 浅克隆 + 复用 fetch | ◻ | ✅ | ✅ | 已优化 |
| 凭据引用 | ✅ 凭据库（账号密码/令牌/SSH） | ✅ | ✅ | ✅ | 缺「按任务限制凭据可见性」与静态加密（见 2.6） |

### 2.4 制品与供应链

| 能力 | 本平台 | Jenkins | GitLab CI | GitHub Actions | 说明 |
|---|---|---|---|---|---|
| 按构建归档 | ✅ 独立目录 + `artifacts.json` + SHA256 + ZIP | ✅ | ✅ | ✅ | 已足够日常回滚 |
| 外部制品库 | ✗ | ◻ Nexus/Artifactory 插件 | ✅ 内置 Registry + Package | ✅ GHCR + Packages | P1：至少要能 push 到外部仓库（S3/Nexus/Registry） |
| 跨任务制品依赖 | ✗ | ✅ copy artifact | ✅ needs:artifacts | ✅ download-artifact | P1：编译任务产物给部署任务用 |
| 保留策略 | ✅ 数量 + 天数（任务级覆盖全局） | ✅ | ✅ | ✅ | 缺「按总大小」与「按 tag/branch 保留」 |
| SBOM / 签名 / 溯源 | ✗ | ◻ | ✅ | ✅ Attestation | P2：合规场景硬需求 |
| 漏洞扫描 | ✗ | ◻ | ✅ | ✅ CodeQL/Dependabot | P2：可先用「步骤里跑 trivy」的轻量方式兜底 |

### 2.5 部署、环境与发布

| 能力 | 本平台 | Jenkins | GitLab CI | GitHub Actions | Argo CD | 说明 |
|---|---|---|---|---|---|---|
| 部署动作 | ✅ 多目标 SSH 上传 + 远程命令（Publish Over SSH 语义），可按参数选目标 | ◻ | ✅ | ✅ | ✅ GitOps | 单机 / 少量机器场景够用 |
| 环境（dev/staging/prod） | ✗ | ◻ | ✅ environments | ✅ environments | ✅ | **P1**：环境维度 + 部署历史 + 权限隔离 |
| 审批与保护规则 | ✗ | ◻ input | ✅ | ✅ required reviewers | ✅ | **P0**：生产必须审批 |
| 蓝绿 / 金丝雀 | ✗ | ◻ | ◐ | ◐ | ✅ | P2：需流量层配合，超出本平台范围 |
| 回滚 | ◐ 历史产物重新发布（Republish） | ◻ | ✅ | ◐ | ✅ | P1：升级为「部署记录一键回滚」 |
| GitOps / 漂移检测 | ✗ | ✗ | ◐ | ✗ | ✅ | 不做（定位不同），可对接 Argo CD |

### 2.6 安全、权限与合规

| 能力 | 本平台 | Jenkins | GitLab CI | GitHub Actions | 说明 |
|---|---|---|---|---|---|
| 账号体系 | ✅ 本地用户（bcrypt）+ 内置管理员 | ◻ 安全域 | ✅ | ✅ | |
| SSO / OIDC / LDAP / SAML | ✗ | ◻ 插件 | ✅ | ✅ | **P1**：企业落地第一道门槛 |
| MFA（TOTP） | ✗ | ◻ | ✅ | ✅ | P1：管理员强制 MFA |
| 细粒度授权 | ✅ 全局角色 + 任务角色（Pattern/模板） | ✅ | ✅ | ✅ | 已覆盖 Jenkins Role Strategy 主要能力 |
| 密钥存储 | ◐ 凭据 JSON 落盘（接口不回显） | ✅ | ✅ 加密/Vault | ✅ | **P0**：静态加密（AES-GCM + 主密钥），至少防止「拷走目录即拿密钥」 |
| 密钥日志遮罩 | ✗ | ✅ | ✅ | ✅ | **P0**：步骤里 `echo $TOKEN` 会原样落盘，风险最高、实现成本最低 |
| 审计 | ✅ 变更类全量 + JSONL 轮转 + UI | ◻ | ✅ | ✅ | 缺「导出 / 长期归档 / 保留策略」 |
| 构建隔离 | ✗（以服务用户权限跑 shell） | ◐ 视 agent | ✅ 容器 | ✅ 容器/VM | P2：需容器或远程执行器；当前靠「只给可信用户配置权限」 |
| 策略即代码 | ✗ | ◻ | ◐ | ◐ | P2：OPA/Conftest 步骤级兜底 |

### 2.7 质量与可观测

| 能力 | 本平台 | Jenkins | GitLab CI | GitHub Actions | 说明 |
|---|---|---|---|---|---|
| 构建历史/日志 | ✅ 任务内历史 + 实时 WS + 全文；**跨任务历史一次性返回全部**（无服务端分页） | ✅ | ✅ | ✅ | P1：构建量上千后需要分页/游标 |
| 日志检索 | ✗ 只能拉全文/尾部（无搜索框、无索引） | ◻ | ✅ | ✅ | P1：构建后生成失败行摘要 + 关键字过滤 |
| 测试报告 | ✗ | ◻ | ✅ | ◻ | **P0**：JUnit XML 解析 + 失败用例 + 趋势 |
| 覆盖率 | ✗ | ◻ | ✅ | ◻ | P1 |
| Flaky 检测 | ✗ | ◻ | ◐ | ◻ | P2（TeamCity 强项）|
| 指标（Prometheus） | ✗ | ◻ | ✅ | ◻ | **P0**：`/metrics` 暴露队列/时长/成功率，接入现有监控 |
| DORA 面板 | ✗ | ✗ | ✅ | ◻ | P2 |
| 通知与值班运营 | ✅ 强于多数开源默认 | ◻ | ◐ | ◐ | 这是我们的**相对优势**（见第三节） |

### 2.8 谁做什么：能力归属与「该不该做」

| 能力 | 更适合「我们做」 | 更适合「对接外部」 | 建议 |
|---|---|---|---|
| 流水线表达力（参数/并行/条件） | ✅ | | 自己做，成本可控且是核心体验 |
| 测试报告/覆盖率展示 | ✅ | | 自己做（解析 JUnit XML 即可，无外部依赖） |
| 制品仓库 / 镜像仓库 | | ✅ Nexus/Harbor/S3 | 提供 push/get 配置项，不做内置仓库 |
| 代码扫描 / SBOM / 签名 | ◐ 触发即可 | ✅ Trivy/Syft/Cosign/CodeQL | 在步骤里跑，平台只负责展示结果摘要 |
| SSO / MFA | ✅ | | 自己做（OIDC + TOTP 代码量不大） |
| 蓝绿/金丝雀/流量治理 | | ✅ Argo Rollouts/Istio | 不做，避免变成发布编排平台 |
| 多节点调度 | ✅（最小远程执行器协议） | | 做「远程 executor」而非完整 agent 生态 |

---

## 三、我们当前的相对优势（别在改造中弄丢）

| 优势 | 代码位置 | 为什么值钱 |
|---|---|---|
| 单文件零依赖交付（前端内嵌 + UPX ~3.4MB） | `assets.go` / `build.sh` | 内网/离线/边缘环境部署成本近乎为零 |
| 文件系统存储，目录即真相 | `internal/store/*` | 备份=拷目录，排障=看文件，无 DB 运维 |
| 告警运营闭环（值班/升级/抑制/分支路由/投递记录） | `service/notify_service.go`、`model/notify.go` | Jenkins/GitLab 要装插件/写脚本才能凑齐 |
| 通知「谁漏收了」可审计（按收件人粒度落盘） | `store/notify_store.go` | 大厂也常缺这一层，故障复盘直接受益 |
| 取消语义对齐 Jenkins（aborted ≠ failed）+ 进程树终止 | `executor/local_executor.go` | 少一类「假的失败」噪音 |
| 密钥脱敏与「脱敏回写」回填 | `pkg/mask.go` | 前端永不拿到 token，同时不丢配置 |
| 日志上限「截断而不失败」 | `pkg/limitedwriter.go` | 避免日志把构建搞挂（比多数工具温和） |
| 构建删除 + 编号不回退 | `build_service.go`、`build_store.go` | 与 Jenkins 同语义，避免 #5 重号 |
| RBAC 任务角色 Pattern/模板 | `service/rbac_service.go` | 小团队也能做到「谁只能动哪些任务」 |
| 参数化构建带「参数快照」 | `service/pipeline_service.go`、`model/build.go` | 改默认值后历史构建仍可复盘「当时用的什么版本/环境」 |
| 步骤级控制「失败后仍跑收尾」 | `executor/local_executor.go` | `when=always/failure` 让清理与诊断收集不被前置失败吞掉 |
| 多目标发布「失败不互相拖累」 | `service/build_service.go` | 生产 + 备份场景要的是「尽力发完并报告失败」，而不是首个失败即停 |
| Jenkins 一键导入（告警分级 + dry-run） | `internal/importer/jenkins.go` | Jenkins 无迁移 API；我们把「XML → 任务」做成内建能力，迁移期最省事 |
| 可移植的配置导出（默认不含密钥） | `internal/service/portability_service.go` | 导出文件会被转发/入库；只带服务器与凭据「名称」，跨实例按名对齐即可用 |
| 中文 UI 与 Jenkins 信息架构 | `web/src/views/**` | 迁移成本低，认知负担小 |

---

## 四、差距清单（可直接排期）

> 工作量为人日粗估（1 人，熟悉本代码库），含单测与 e2e；不含前端视觉打磨。

### P0 —— 让「流水线」具备基本表达力与可信度

> 已完成（本清单移出）：**参数化构建**、**步骤级 timeout/retry/allow_failure/when**、**多目标 SSH 发布**。
> 下面是最早一批 P0 中仍未交付的部分。

| # | 差距 | 大厂做法参考 | 最小可行实现 | 改动模块 | 估算 |
|---|---|---|---|---|---|
| 1 | 无并行步骤 / 阶段 | GHA `needs`、GitLab `parallel` | 引入 `stage`（同 stage 内并行、跨 stage 串行）+ 日志前缀 `[step]`；先做「同阶段并行」不做 DAG | `model/pipeline.go`、`build_service.go`、`LogTerminal` | 4 |
| 2 | SCM 不回写状态 | GitLab/GitHub Commit Status、Checks | 任务配置 `report_status`；构建开始/结束调 GitLab `statuses` / GitHub `check-runs`（用现有凭据）；失败仅记日志不影响构建 | `executor/scm_status.go`（新）、`build_service.go`、`pipeline_service.go` | 4 |
| 3 | 无 PR/MR 语义与分支过滤 | GitLab MR pipeline、GHA `on: pull_request` | Webhook 解析 `ref`/`object_attributes`；任务加 `branch_filter`（glob/regex）与 `on_mr`；分支不匹配直接忽略 | `handler/webhook_handler.go`、`model/pipeline.go` | 3 |
| 4 | 密钥会原样写进构建日志 | Jenkins Mask Passwords、GHA `::add-mask::` | 构建时把任务/全局用到的凭据值注册进「遮罩表」，`LimitedWriter` 前置一个替换 Writer（多模式匹配 `***`） | `pkg/maskwriter.go`（新）、`build_service.go` | 2 |
| 5 | 凭据静态明文 | GitLab 加密变量、HashiCorp Vault | 主密钥（`CI_MASTER_KEY` 或 `data/.master_key` 0600）AES-GCM 加密 `secret` 字段；启动校验；旧数据自动迁移 | `store/credential_store.go`、`pkg/crypto.go`（新）、`cmd/server/main.go` | 2 |
| 6 | 无测试报告 | GitLab `reports:junit`、TeamCity 内置 | 任务配置 `junit_paths[]`；构建后解析 JUnit XML（标准库 `encoding/xml`），展示总数/失败/耗时/失败用例；存 `build.json` | `executor/junit.go`（新）、`build_service.go`、`BuildLogView` 新增「测试」页签 | 3 |
| 7 | 无指标端点 | 所有大厂都有 `/metrics` | 暴露 `ci_builds_total{status}`、`ci_build_duration_seconds`、`ci_queue_depth`、`ci_workers_busy`；纯标准库手写文本格式（避免引入 prometheus 依赖） | `handler/metrics_handler.go`（新）、`scheduler`/`build_service` 计数 | 2 |
| 8 | 无人工审批门 | GitLab manual job、GHA required reviewers | `Step` 增加 `type: approval`（审批人角色/用户、超时自动取消）；审批动作走 API + 通知（复用现有通知）；构建状态新增 `waiting_approval` | `model/build.go`、`build_service.go`、`handler/build_handler.go`、通知复用 | 3 |

### P1 —— 团队规模化与生态对接

| # | 差距 | 最小可行实现 | 估算 |
|---|---|---|---|
| 9 | 多分支/标签自动构建 | 「分支模板」：一个任务可按 `branch_filter` 匹配多个分支，构建记录带分支；或为每个分支自动派生只读子任务（Jenkins multibranch 简化版） | 5 |
| 10 | 无环境与部署记录 | `environment` 维度（名称/审批人/变量）+ 部署历史表（JSONL）+ 一键回滚（复用 Republish） | 5 |
| 11 | 无跨任务制品依赖 | 任务配置 `copy_artifacts_from`（上游任务 + 构建选择器）；构建前下载到工作区 | 4 |
| 12 | 无外部制品库 | 任务后置动作 `push_artifacts`：S3（签名 v4）/ Nexus raw 二选一，凭据复用凭据库 | 4 |
| 13 | 无显式缓存 | 任务级 `cache{key,paths[]}`，落在 `data/cache/{pipeline}/{key}/`，构建前后 restore/save（本地目录，不做远端） | 3 |
| 14 | 无 SSO/OIDC | 授权码流程 + 用户自动落地 + 组 → 角色映射（`groups_claim`）；保留本地账号兜底 | 5 |
| 15 | 无 MFA | TOTP（标准库 HMAC-SHA1）+ 恢复码；管理员可强制 | 3 |
| 16 | 通知渠道偏少 | 通用 Webhook（自定义 JSON 模板）+ Microsoft Teams + PagerDuty/Opsgenie（事件 + 关闭事件） | 3 |
| 17 | 审计不可导出/无保留策略 | `GET /api/system/audit/export?from=&to=`（CSV/JSONL）+ 保留天数配置 | 2 |
| 18 | 无队列优先级/并发策略 | `priority` + `max_concurrent_per_job` + `cancel_in_progress`（新构建取消同任务运行中构建） | 3 |
| 19 | 日志不可检索 | 构建结束后把「失败行 + 关键字命中」写 `build.json.summary`；前端按关键字过滤历史日志（服务端 grep 单个 log.txt） | 2 |
| 20 | 无 DORA/趋势 | 基于 `build.json` 聚合：部署频率、变更前置时间、失败率、MTTR；新增「统计」页 | 4 |
| 21 | 跨任务构建历史无分页 | `GET /api/builds?limit=&cursor=`（按 `started_at` 倒序游标）+ 前端「加载更多」；同时给总览做任务级汇总缓存，避免每次全量扫描 | 3 |

### P2 —— 大型组织能力（与当前定位有明显张力）

| # | 差距 | 备注 |
|---|---|---|
| 22 | 远程 executor / agent | 建议做「最小协议」：HTTP 长轮询领任务 + 日志回传 + 工作区在 agent 本地；不做插件生态。约 10-15 人日 |
| 23 | 容器化/K8s executor | 依赖 #22；或者允许步骤里自行 `docker run`（现状即可） |
| 24 | Pipeline-as-code（YAML） | 与 UI 双写有冲突，建议「YAML 只读导入 + 导出」，避免两套真相；Jenkins 侧迁移已由 config.xml 导入覆盖 |
| 25 | SBOM/签名/溯源 | 建议步骤内跑 Syft/Cosign，平台只展示摘要与链接 |
| 26 | 策略即代码（OPA） | 可用「前置步骤 + conftest」兜底 |
| 27 | 高可用 | 与「文件存储 + 内存会话」冲突；真要做需外置会话/共享存储锁，等于换架构 |
| 28 | 插件系统 | 明确不做（单文件交付的核心约束） |

---

## 五、建议路线图

### 阶段 A（已完成）：把「能跑」变成「敢用」
~~`#1 参数化` → `#2 步骤控制`~~（已交付）→ `#4 日志遮罩` → `#5 凭据加密` → `#7 指标`
> 已获得：生产发布可以传版本号；网络步骤失败能自动重试、可设置超时与允许失败。
> 剩余判据：日志里搜不到 token；凭据文件被拷走也解不开；监控能看到队列与失败率。

### 阶段 B（2~3 周）：把「CI」接进研发流程
`#2 状态回写` → `#3 PR/分支过滤` → `#6 测试报告` → `#8 审批门`
> 交付判据：MR 上有 ✅/❌；测试失败能在平台看到具体用例；生产部署有人点确认。

### 阶段 C（3~4 周）：团队规模化
`#10 环境与部署记录` → `#11 制品依赖` → `#13 缓存` → `#14 SSO` → `#16 通知渠道` → `#18 队列策略`
> 交付判据：多环境发布可追溯可回滚；依赖不再重复下载；员工用公司账号登录。

### 暂缓（需要重新评估架构）
多节点 agent、HA、容器执行器、Pipeline-as-code 双向同步。

---

## 六、维护约定

- 每完成一项，把对应表格里的 `✗/◐` 改成 `✅` 并补一行「验证方式」（对应 `*_test.go` 或 e2e 场景）。
- 新增能力前先看「2.8 能力归属」：能通过外部系统解决的，不要在平台里重造。
- 本文件与 `README.md`（用户视角）、`DESIGN.md`（实现视角）互补：这里只回答「差什么、为什么、先做哪个」。

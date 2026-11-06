# 验证结果

[← 返回 README](../README.md)

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
| 配置页编辑兼容 | ✅ `npm run test`（`web/tests/pipelineConfig.test.mts`）：复现并修复「绑定 SSH 服务器的任务保存时报 `reading 'trim'`」——服务端 `omitempty` 字段缺失时前端统一归一化 |
| 配置导出 / 导入 | ✅ 36 项 e2e：导出为裸 JSON（无 `code` 包装、带下载文件名）、不含 SSH 密码/Webhook 密钥/群 token、只带服务器与凭据**名称**；导入预览不落盘、按名对齐到另一实例的 `server_id`/凭据、缺失服务器时停用+error 告警、原生 config.json 可导入、update 覆盖、坏文档/版本过高 40000 |
| 步骤级控制 | ✅ `allow_failure` 失败继续；`timeout_sec=1` 杀掉 sleep 并记 124；`when=failure/always` 在失败后执行；致命失败跳过 `when=success`；`retry=2` 共 3 次尝试 |
| 通知记录 | ✅ 按收件人粒度落盘，可过滤 `sent` / `failed` / `suppressed`，失败带错误原因 |
| 平台日志分级记录 + 30 天保留 | ✅ e2e + 7 例单测：默认 info 下无 DEBUG、`-log-level debug` 后出现 DEBUG、一天一个文件 `YYYY-MM/app-YYYY-MM-DD.log`、同一天复用同一文件 / 跨天换文件 / 跨月换目录、级别列落盘、API 返回结构化条目与「保留 30 天」信息、超 30 天的天文件被删除（空月目录一并清理，用户自建目录不碰，旧版根目录月文件也会清） |
| JUnit 测试报告 | ✅ 解析 `<testsuites>` / 单 `<testsuite>`、汇总 passed/failed/error/skipped/耗时、失败用例与原始报告落盘；failure/error 让构建失败；`allow_empty` 可放行无报告 |
| 禁用任务 | ✅ 手动/Webhook/定时触发统一拒绝（`Trigger` 入口拦截），配置与历史保留；重新启用即可继续 |
| API Token / 幂等触发 | ✅ 令牌只存哈希、可吊销/过期、scopes 最小权限；`Idempotency-Key` 按任务+用户复用首次构建；Token 不能自助提权 |
| CLI / OpenAPI | ✅ `cmd/ci` 独立 CLI（login/list/build/logs/status/cancel/token/dashboard）+ `/openapi.json` 内嵌 OpenAPI 3.0 |
| 出站事件订阅 | ✅ `build.started` / `build.finished` / `deploy.finished` / `approval.requested`；HMAC-SHA256 签名、失败重试、URL/Secret 加密；投递失败不影响构建 |
| 首次创建向导 | ✅ 模板库（Go/Node/Maven/流水线/自由风格）+ Git/分支/环境最小输入，一键创建并触发首次构建；模板 payload 单测覆盖 |
| 密钥治理 | ✅ SMTP/SSH/Webhook/任务 Webhook/SSH 发布/群机器人 URL/全局 secret 变量统一 AES-GCM 落盘；API 不回传原文；`/readyz` 检查 credentials.key |
| 仪表盘 / Metrics | ✅ 首页 KPI（今日构建/成功率/耗时/队列/部署/失败 Top）；`/metrics` 提供 Prometheus 聚合指标 |
| 密码参数 | ✅ 触发时填写、`build.json` 只落 AES-GCM 密文、日志遮罩、API 显示 `***`；Re-run 服务端解密还原 |
| 缓存 / 产物晋级 | ✅ 显式 cache key/paths 恢复与保存；跨任务复制产物并校验 SHA-256 |
| 环境与部署记录 | ✅ 任务 environment 快照；SSH 发布/重新发布记录目标、状态、时间、操作人 |
| 产物归档模式诊断 | ✅ e2e 11 + 11 + 7 项：`target/*.jar` 在多模块工程下（jar 只在 `ruoyi-admin/target/`）**直接归档成功**且日志注明自动递归、根目录有 jar 时优先根目录、真无产物仍失败并说明原因；构建记录带结构化 `advice`（当前模式/建议模式/找到的文件），按建议 PUT 后重建成功且产物归档；构建记录带结构化 `advice`（当前模式/建议模式/找到的文件）、按建议改模式后重建成功、`allow_empty` 无产物仍成功且配置持久化；单测覆盖 glob 语义、建议逻辑、allow_empty |
| 任务级工具版本 | ✅ e2e 15 项：新装实例预置「自动安装」、任务选 JDK 8 / JDK 17 各自生效（构建日志可验证）、两个 Java 任务不会下载 NodeJS、选不存在的安装项保存被拒、未选择时用默认自动安装 |
| 自动安装后台任务 | ✅ e2e 11 项：接口立刻返回、任务进度可查、重复点击单飞、客户端断开安装继续、完成后构建直接使用 |
| Webhook × GitLab | ✅ 22 项 e2e：按推送分支构建（真实 git 签出 `release/1.2`）、MR 事件忽略、白名单外分支忽略、`[ci skip]` 忽略、tag 事件需勾选、重复投递去重、错误密钥 401、任务不存在 404、`fixed` 模式仍构建指定分支 |
| 修改密码（自助） | ✅ 15 项 e2e：管理员与平台内用户各自改密、旧密码失效、新密码可登录、当前会话保留而其他会话失效、管理员重置密码仍可用 |
| 个人资料与邮箱验证 | ✅ 迷你 SMTP 实测：发码 → 错误码拒绝 → 正确码标记已验证 → 改邮箱失效 → 60s 限流 `42900` |
| 邮箱必填 | ✅ 无邮箱创建/改资料 `40000`；历史空邮箱启动 WARN + 构建日志警告 |
| 删除单次构建 | ✅ 目录（含产物）一并删除、其它构建保留、运行中 `40900`、重复删除幂等、编号不回退 |
| 操作审计 | ✅ 变更类请求落 `data/audit.log`，重启后 ID 单调；与通知记录共用倒序读取实现 |

---

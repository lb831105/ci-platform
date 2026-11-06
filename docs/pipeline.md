# 流水线与发布

[← 返回 README](../README.md)

## 参数化构建

任务可定义参数（**任务 → 参数化构建**），触发时浏览器弹出表单；未填的取默认值。
值会以两种环境变量注入构建步骤：原名 `VERSION` 与带前缀的 `CI_PARAM_VERSION`。

| 类型 | 说明 |
| --- | --- |
| `string` | 单行字符串，注入前把换行压成空格（换行会破坏 shell 变量赋值） |
| `bool` | `true` / `false`；触发时也接受 `1/0`、`yes/no`，统一归一为 `true`/`false` |
| `choice` | 固定可选值集合，默认值必须在集合内（留空取第一个） |
| `text` | 多行文本，保留换行（适合 changelog 之类的片段） |
| `password` | 密码类参数：触发时填写，不设默认值；`build.json` 只保存加密值，API/构建页显示 `***`，重跑时由服务端解密还原 |

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

## 步骤级执行控制

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


## SSH 发布（对齐 Jenkins Publish Over SSH）

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

### 出站事件订阅

系统配置 → **出站事件订阅**把平台状态推给外部系统（工单、发布平台、IM Bot）。与任务级
IM 通知不同，订阅是全局的，payload 是稳定的机器可读 JSON：

```http
POST https://example.com/hooks/ci
Content-Type: application/json
X-CI-Event: build.finished
X-CI-Delivery: 3f6c...
X-CI-Signature: sha256=<HMAC-SHA256(secret, raw_body)>
```

```json
{
  "id": "3f6c...",
  "type": "build.finished",
  "time": "2026-01-01T12:00:00Z",
  "pipeline_id": "1703123456",
  "job_name": "demo-app",
  "build_id": "42",
  "status": "success",
  "trigger_type": "manual",
  "triggered_by": "alice",
  "branch": "main",
  "commit": "a1b2c3d",
  "environment": "prod",
  "exit_code": 0,
  "duration_ms": 12345
}
```

事件类型：`build.started`、`build.finished`、`deploy.finished`（带 `deployment`）、
`approval.requested`（带 `approval`）。行为：

- URL 与 HMAC Secret 使用 `credentials.key` 加密落盘（`data/events.json`），API 只返回
  `has_url` / `has_secret`；留空保存表示沿用旧值，`clear_secret=true` 才清除。
- 仅允许 `http(s)` 外网地址；loopback / 私网 / 云元数据地址在保存时直接拒绝。
- 失败重试 3 次（网络错误 / 5xx / 429），其它 4xx 不重试；投递失败只写平台日志，
  **绝不影响构建结果**。
- 单实例最多 20 个订阅；测试事件通过「测试」按钮或 `POST /api/system/events/test` 发送。

---

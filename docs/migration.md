# 从 Jenkins 迁移

[← 返回 README](../README.md)

Jenkins 没有「迁移任务」的标准 REST API：`config.xml` + `createItem` 只能搬配置，带构建历史的迁移要靠拷目录。
本平台把前者做成了**服务端内建的一键导入**（`POST /api/system/import/jenkins`），
构建历史依旧可以像 Jenkins 官方建议的那样「拷目录」，只是目录格式需要一次翻译。

## 一键导入（配置）

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

## 实例间搬迁（导出 / 导入 config.json）

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

## 构建历史（拷目录）

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

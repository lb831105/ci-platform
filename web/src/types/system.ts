// 与 Go internal/model/settings.go / system.go / credential.go 的 json tag 对齐。

// 平台日志条目（系统管理 → 系统日志）：级别由后端分级记录。
export interface LogEntry {
  at: string
  level: 'DEBUG' | 'INFO' | 'WARN' | 'ERROR' | string
  message: string
}

export interface SystemLogResponse {
  lines: string[]
  entries: LogEntry[]
  // 当前落盘文件与保留策略（运维可据此直接去服务器上 grep）
  file?: { file: string; level: string; retain_days: number }
}

export interface ToolInstallation {
  name: string
  home?: string
  note?: string
  // manual（默认，兼容旧数据）：用服务器上已装好的工具，只认 home
  // auto：由平台按 version 下载安装到 data/tools，home 由平台回填
  install_mode?: 'manual' | 'auto'
  version?: string
}

// 自动安装候选版本（GET /system/tools/catalog）
// 后台安装任务：离开页面也在服务端继续跑，重新打开还能看到进度。
export interface ToolInstallTask {
  type: string
  version: string
  state: 'queued' | 'downloading' | 'extracting' | 'done' | 'failed'
  downloaded: number
  total: number
  home?: string
  error?: string
  started_at: string
  updated_at: string
}

export interface ToolCatalogVersion {
  version: string
  note?: string
  /** 当前服务器平台是否有预置下载包 */
  supported: boolean
  installed: boolean
  home?: string
  /** 用户手填的版本（按下载地址模板推导，如 Node 18.20.4） */
  custom?: boolean
  /** 正在进行的安装任务（含进度） */
  task?: ToolInstallTask
}

export interface ToolCatalogTool {
  type: string
  title: string
  home_env?: string
  hint?: string
  auto_install: boolean
  versions: ToolCatalogVersion[]
  /** 手填版本的格式说明，如「任意补丁版本，如 18.20.4」 */
  version_hint?: string
}

export interface ToolCategory {
  id: string
  title: string
  hint?: string
  installations: ToolInstallation[]
}

// 对应 Jenkins「Maven 项目配置」中真正会影响构建的两项。
// settings.xml 内容不在此维护：平台不会写入磁盘，放在这里只会让人以为已经生效。
export interface MavenSettings {
  maven_opts: string
  local_repo: string
}

// 系统配置中集中维护的 SSH 发布目标。
export interface SshServer {
  id: string
  name: string
  host: string
  port: number
  username: string
  auth_type: 'key' | 'password'
  private_key_path?: string
  password?: string
  known_hosts_path?: string
  remote_base_dir?: string
  note?: string
  has_password?: boolean
}

export interface EnvVar {
  key: string
  value: string
}

export interface WebhookNotifySettings {
  enabled: boolean
  channel: string // dingtalk | wecom | slack
  url: string
  notify_on_failure: boolean
  notify_on_success: boolean
}

export interface EmailSettings {
  enabled: boolean
  smtp_host: string
  smtp_port: number
  use_tls: boolean
  username: string
  password?: string
  from: string
  default_suffix: string
  recipients: string
  notify_on_failure: boolean
  notify_on_success: boolean
  subject_template: string
}

export interface Settings {
  display_name: string
  system_message: string
  jenkins_url: string
  // GitLab 站点地址（如 https://gitlab.example.com），用于「回写构建状态」
  gitlab_base_url?: string
  admin_email: string
  default_branch: string
  job_name_regex: string
  quiet_period_seconds: number
  scm_retry_count: number
  shell: string
  global_build_timeout_minutes: number
  build_retention_max_per_job: number
  build_retention_max_age_days: number
  artifact_max_total_mb: number
  artifact_max_files: number
  log_max_mb: number
  global_env: EnvVar[]
  git_user_name: string
  git_user_email: string
  tool_categories: ToolCategory[]
  ssh_servers: SshServer[]
  maven: MavenSettings
  email: EmailSettings
  webhook_notify: WebhookNotifySettings
  quiet_mode: boolean
  auth_enabled: boolean
  auth_user: string
  auth_password?: string
  workers: number
  queue_size: number
  workspace_ttl_minutes: number
  clean_interval_minutes: number
  updated_at: string
  has_password: boolean
}

export interface SystemInfo {
  app_name: string
  version: string
  commit: string
  build_date: string
  go_version: string
  os: string
  arch: string
  cpus: number
  goroutines: number
  memory_alloc_mb: number
  memory_sys_mb: number
  uptime_seconds: number
  data_home: string
  jobs_dir: string
  workspace_dir: string
  workers: number
  queue_capacity: number
  queue_length: number
  quiet_mode: boolean
  auth_enabled: boolean
  pipeline_count: number
  build_count: number
  running_builds: number
  // 启动参数的实际生效值（来自命令行参数，不是 settings.json）
  workspace_ttl_minutes: number
  clean_interval_minutes: number
  server_time: string
}

export interface DataHomeInfo {
  data_home: string
  configured_data_home: string
  config_file: string
  jobs_dir: string
  workspace_dir: string
  explicit: boolean
  restart_required: boolean
}

export interface LoadStats {
  pipeline_count: number
  build_count: number
  success_builds: number
  failed_builds: number
  running_builds: number
  pending_builds: number
  queue_length: number
  queue_capacity: number
  average_duration_ms: number
  workspace_count: number
  workspace_bytes: number
  jobs_bytes: number
}

export interface Feature {
  name: string
  version: string
  status: string
  description: string
}

export interface OldDataItem {
  kind: 'workspace' | 'orphan_job' | string
  path: string
  bytes: number
  detail: string
}

export interface OldDataReport {
  items: OldDataItem[]
  total_bytes: number
  scanned_at: string
}

// ssh_key 暂不支持：平台没有「凭据 → 私钥文件」的使用方，后端会明确拒绝。
export type CredentialType = 'username_password' | 'secret_text'

export interface Credential {
  id: string
  name: string
  type: CredentialType
  username?: string
  description: string
  created_at: string
  updated_at: string
}

export interface CredentialPayload {
  name: string
  type: CredentialType
  username: string
  description: string
  secret: string
}

// 操作审计日志条目（与 Go model.AuditEntry 对齐）。
export interface AuditEntry {
  id: number
  time: string
  user: string
  action: string
  target: string
  status: number
  ip: string
}

// 通知投递记录（与 Go model.NotifyRecord 对齐）：按收件人/群粒度落盘。
export interface NotifyRecord {
  id: number
  time: string
  pipeline_id: string
  pipeline_name: string
  build_id: string
  event: string
  channel: string
  recipient: string
  status: 'sent' | 'failed' | 'suppressed'
  error?: string
  streak?: number
  escalated?: boolean
  on_call?: string
  branch?: string
}

export interface NotifyHistoryQuery {
  limit?: number
  pipeline?: string
  recipient?: string
  status?: string
}

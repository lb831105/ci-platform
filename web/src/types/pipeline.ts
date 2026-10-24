// 与 Go internal/model/pipeline.go 的 json tag 严格对齐。

export type SshAuthType = 'key' | 'password'

export interface SshPublishConfig {
  enabled: boolean
  // 引用系统配置中的 SSH 服务器；为空时使用下方的旧版内联配置。
  server_id: string
  host: string
  port: number
  username: string
  auth_type: SshAuthType
  private_key_path: string
  password: string
  remote_base_dir: string
  source_files: string[]
  remove_prefix: string
  exec_command: string
}

export type PipelineExecutor = 'docker' | 'local'

// 构建产物归档规则：构建成功后按 glob 复制到 jobs/{pid}/builds/{bid}/artifacts/。
export interface ArtifactConfig {
  enabled: boolean
  files: string[]
}

export interface BuildStep {
  name: string
  run: string
}

// ---- 通知（项目 × 人员/群 × 事件）----
export type NotifyChannel = 'dingtalk' | 'wecom' | 'slack'

export interface NotifyEvents {
  on_failure: boolean
  on_success: boolean
  // 上次失败/取消 → 本次成功（修复通知）
  on_fixed: boolean
  on_aborted: boolean
}

export interface NotifyTarget {
  channel: NotifyChannel
  url: string
  // 为空表示继承任务级事件
  events: NotifyEvents
}

// 值班轮值：失败时 @ 当周值班人（而不是固定名单）。
export interface OnCallConfig {
  enabled: boolean
  // 轮值人员（用户名或邮箱），按顺序轮转
  rotation: string[]
  // 轮值起点 YYYY-MM-DD（空则用固定锚点，轮值仍随时间推进）
  start_date: string
  // 每人连续值班天数（默认 7）
  period_days: number
}

// 升级策略：连续失败达到阈值后追加上级/更大群。
export interface EscalationConfig {
  enabled: boolean
  // 连续失败次数阈值（默认 3，最小 2）
  after: number
  people: string[]
  emails: string[]
  targets: NotifyTarget[]
}

// 分支路由：分支匹配时把人员/群/提交者叠加到通知目标。
export interface NotifyRoute {
  match: string // main / feature/* / release/**
  people: string[]
  emails: string[]
  targets: NotifyTarget[]
  committer: boolean // 追加通知本次提交作者
  on_call: boolean   // 追加通知当周值班人
}

export interface JobNotify {
  enabled: boolean
  // 同时发送全局默认收件人/群（叠加而非替换）
  use_global: boolean
  // 引用平台用户，发送时解析其邮箱 / 企微 ID / 手机号
  people: string[]
  // 额外原始邮箱（外部人员、邮件组）
  emails: string[]
  targets: NotifyTarget[]
  events: NotifyEvents

  // 运维增强：值班轮值 / 升级 / 抑制 / 分支路由 / 消息模板。
  on_call?: OnCallConfig
  escalation?: EscalationConfig
  routes?: NotifyRoute[]
  // 失败抑制窗口（分钟）：窗口内连续失败合并为一条；0 表示关闭
  suppress_window_min?: number
  // 任务级消息模板（占位符 {{job}} {{build}} {{streak}} ...）
  template?: string
}

export function emptyOnCall(): OnCallConfig {
  return { enabled: false, rotation: [], start_date: '', period_days: 7 }
}

export function emptyEscalation(): EscalationConfig {
  return { enabled: false, after: 3, people: [], emails: [], targets: [] }
}

export function emptyRoute(): NotifyRoute {
  return { match: '', people: [], emails: [], targets: [], committer: false, on_call: false }
}

// 消息模板占位符（与后端 pkg.NotifyTemplateVars 保持一致）。
export const NOTIFY_TEMPLATE_VARS = [
  'job', 'build', 'event', 'status', 'branch', 'commit',
  'author', 'duration', 'url', 'oncall', 'streak', 'message',
] as const

export function emptyNotifyEvents(): NotifyEvents {
  return { on_failure: true, on_success: false, on_fixed: true, on_aborted: false }
}

// noNotifyEvents 全 false：对「群目标」表示「继承任务级事件」（后端零值语义）。
// 注意：不能用 emptyNotifyEvents() 兑底目标事件——它会把「继承」静默变成
// 「只发失败+修复」，前端一保存就会改变原有通知行为。
export function noNotifyEvents(): NotifyEvents {
  return { on_failure: false, on_success: false, on_fixed: false, on_aborted: false }
}

// 平台用户联系方式（通知选人下拉用）。
export interface NotifyContact {
  name: string
  email?: string
  wecom_id?: string
  phone?: string
}

// 测试通知结果。
export interface NotifyTestResult {
  emails: string[]
  targets: string[]
  errors: string[]
  summary: string
}

export function emptyJobNotify(): JobNotify {
  return {
    enabled: false,
    use_global: false,
    people: [],
    emails: [],
    targets: [],
    events: emptyNotifyEvents(),
    on_call: emptyOnCall(),
    escalation: emptyEscalation(),
    routes: [],
    suppress_window_min: 0,
    template: '',
  }
}

export interface Pipeline {
  id: string
  name: string
  description: string
  image: string // 已废弃：仅兼容旧数据，本地构建不再使用
  steps: BuildStep[]
  git_url: string
  git_branch: string
  git_credential_id: string
  webhook_secret: string
  // 定时构建（对齐 Jenkins Build periodically）：5 字段 cron 规格，空表示不启用
  cron_spec: string
  ssh_publish: SshPublishConfig | null
  artifacts: ArtifactConfig
  // 任务级通知配置；null 表示沿用全局默认（系统配置 → 邮件 / Webhook 通知）
  notify: JobNotify | null
  build_retention_max_per_job: number
  build_retention_max_age_days: number
  delete_workspace_before_build: boolean
  // 平台已统一为本地执行；字段保留兼容旧数据。
  executor?: PipelineExecutor
  // data/workspace 下的目录名，由后端根据任务名生成；前端只读。
  workspace_dir?: string
  created_at: string
  updated_at: string
}

// 提交给后端的负载：id 与时间戳由服务端生成/维护。
export type PipelinePayload = Omit<Pipeline, 'id' | 'created_at' | 'updated_at'>

// 创建空表单，避免各处重复写默认值。
export function emptySshPublish(): SshPublishConfig {
  return {
    enabled: false,
    server_id: '',
    host: '',
    port: 22,
    username: '',
    auth_type: 'key',
    private_key_path: '',
    password: '',
    remote_base_dir: '',
    source_files: [],
    remove_prefix: '',
    exec_command: '',
  }
}

export function emptyArtifacts(): ArtifactConfig {
  return { enabled: false, files: [] }
}

export function emptyPipeline(): PipelinePayload {
  return {
    name: '',
    description: '',
    image: '',
    steps: [{ name: 'build', run: '' }],
    git_url: '',
    git_branch: 'main',
    git_credential_id: '',
    webhook_secret: '',
    cron_spec: '',
    ssh_publish: emptySshPublish(),
    artifacts: emptyArtifacts(),
    notify: emptyJobNotify(),
    build_retention_max_per_job: 0,
    build_retention_max_age_days: 0,
    delete_workspace_before_build: false,
    executor: 'local',
  }
}

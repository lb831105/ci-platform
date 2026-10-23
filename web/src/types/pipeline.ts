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
  ssh_publish: SshPublishConfig | null
  artifacts: ArtifactConfig
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
    ssh_publish: emptySshPublish(),
    artifacts: emptyArtifacts(),
    build_retention_max_per_job: 0,
    build_retention_max_age_days: 0,
    delete_workspace_before_build: false,
    executor: 'local',
  }
}

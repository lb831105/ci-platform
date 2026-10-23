// 与 Go internal/model/build.go 的 json tag 严格对齐。

export type BuildStatus = 'pending' | 'running' | 'success' | 'failed'

export type TriggerType = 'manual' | 'webhook'

export interface Build {
  id: string
  pipeline_id: string
  status: BuildStatus
  trigger_type: TriggerType
  triggered_by: string
  started_at: string | null
  finished_at: string | null
  exit_code: number
}

export interface BuildLogPayload {
  log: string
}

// 与 Go internal/model/build.go 的 ArtifactFile / ArtifactManifest 对齐。
export interface ArtifactFile {
  path: string
  size: number
  sha256?: string
}

export interface ArtifactManifest {
  created_at: string
  total_size: number
  files: ArtifactFile[]
}

// 终态判断，供日志页决定是否建立 WebSocket。
export function isFinished(status: BuildStatus): boolean {
  return status === 'success' || status === 'failed'
}

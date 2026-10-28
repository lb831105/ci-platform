// 与 Go internal/model/build.go 的 json tag 严格对齐。

export type BuildStatus = 'pending' | 'running' | 'success' | 'failed' | 'aborted'

export type TriggerType = 'manual' | 'webhook' | 'timer'

export interface Build {
  id: string
  pipeline_id: string
  status: BuildStatus
  trigger_type: TriggerType
  triggered_by: string
  started_at: string | null
  finished_at: string | null
  exit_code: number
  // 本次构建实际使用的参数值（触发时快照）；非参数化任务为空。
  params?: Record<string, string>
  // 构建失败时后端给出的可执行修复建议（目前用于产物模式没匹配到文件）
  advice?: BuildAdvice
}

export interface BuildAdvice {
  // 目前只有 'artifact_pattern'，后续新增种类时前端按字符串匹配即可
  kind: string
  message: string
  patterns?: string[]
  suggested?: string[]
  files?: string[]
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
  return status === 'success' || status === 'failed' || status === 'aborted'
}

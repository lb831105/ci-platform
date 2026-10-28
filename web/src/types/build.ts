// 与 Go internal/model/build.go 的 json tag 严格对齐。

export type BuildStatus = 'pending' | 'pending_approval' | 'running' | 'success' | 'failed' | 'aborted'

export type TriggerType = 'manual' | 'webhook' | 'timer'

export interface Build {
  id: string
  pipeline_id: string
  status: BuildStatus
  trigger_type: TriggerType
  triggered_by: string
  // created_at 是入队时间（旧构建可能没有）；started_at 是真正开始执行。
  created_at?: string | null
  started_at: string | null
  finished_at: string | null
  exit_code: number
  // 构建页备注 / 永久保留（Jenkins Add description / Keep forever）。
  description?: string
  keep_forever?: boolean
  // SCM 现场：构建页展示 Revision / Repository / refs / Changes。
  branch?: string
  commit?: string
  commit_full?: string
  commit_message?: string
  changed_files?: string[]
  commit_author?: string
  commit_email?: string
  // 本次构建实际使用的参数值（触发时快照）；非参数化任务为空。
  params?: Record<string, string>
  // 构建失败时后端给出的可执行修复建议（目前用于产物模式没匹配到文件）
  advice?: BuildAdvice
  // 发布审批信息（仅 pending_approval / 已审批构建有值）
  approval?: BuildApproval
}

export interface BuildApproval {
  requested_at: string
  requested_by?: string
  decided_at?: string | null
  decided_by?: string
  approved: boolean
  comment?: string
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

// 与 Go internal/model/summary.go 的 json tag 严格对齐。

import type { Build, BuildStatus } from './build'
import type { Pipeline } from './pipeline'

export interface BuildRef {
  id: string
  status: BuildStatus
  started_at: string | null
  finished_at: string | null
  duration_ms: number
}

// 任务列表行：Pipeline 字段平铺 + 构建聚合。
export interface PipelineSummary extends Pipeline {
  status: BuildStatus | 'none'
  running: boolean
  last_build: BuildRef | null
  last_success: BuildRef | null
  last_failure: BuildRef | null
  recent_statuses: BuildStatus[]
}

export interface BuildListItem extends Build {
  pipeline_name: string
}

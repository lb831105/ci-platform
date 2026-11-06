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
  // 服务端按任务内序列计算的上下文：分页后仍能显示 Jenkins 的断开语义。
  previous_status?: BuildStatus
  failure_streak?: number
  failing_since_build?: string
  failing_since_at?: string | null
}

// 首页仪表盘聚合数据（与 Go model.Dashboard 对齐）。
export interface DashboardDeployment {
  pipeline_id: string
  pipeline_name: string
  build_id: string
  target?: string
  environment?: string
  status: 'success' | 'failed' | string
  deployed_at: string
  deployed_by?: string
}

export interface DashboardFailure {
  pipeline_id: string
  pipeline_name: string
  failures: number
  last_failed_at?: string | null
  last_build_id?: string
}

export interface DashboardData {
  updated_at: string
  jobs_total: number
  jobs_disabled: number
  jobs_running: number
  builds_today: number
  success_today: number
  failed_today: number
  aborted_today: number
  running_builds: number
  pending_builds: number
  success_rate: number
  avg_duration_ms: number
  queue_length: number
  queue_capacity: number
  workers: number
  quiet: boolean
  deployments_today: number
  deployment_failures_today: number
  recent_deployments: DashboardDeployment[]
  top_failures: DashboardFailure[]
}

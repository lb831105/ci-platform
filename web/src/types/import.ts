// 与 Go internal/importer/jenkins.go 的 Warning / handler 的响应严格对齐。

import type { Pipeline } from './pipeline'

export type ImportWarningLevel = 'error' | 'warn' | 'info'

export interface ImportWarning {
  field: string
  level: ImportWarningLevel
  message: string
}

export type ImportAction = 'preview' | 'created' | 'updated' | 'skipped'

export interface JenkinsImportResult {
  pipeline: Pipeline
  warnings: ImportWarning[]
  action: ImportAction
  dry_run: boolean
  source_kept: boolean
}

// 导入模式：重名时的处理策略。
export type ImportMode = 'create' | 'update' | 'skip'

export interface JenkinsImportOptions {
  name: string
  mode: ImportMode
  dryRun: boolean
  keepSource: boolean
}

// 平台自有 JSON 配置导入：与 Jenkins 导入共用告警与动作语义，
// 但配置来源是平台导出文档（config.json / export 文档），没有 SSH 服务器名提示之外的差异。
export interface PlatformImportOptions {
  name: string
  mode: ImportMode
  dryRun: boolean
}

export interface PlatformImportResult {
  pipeline: Pipeline
  warnings: ImportWarning[]
  action: ImportAction
  dry_run: boolean
}

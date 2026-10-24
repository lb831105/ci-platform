// 与 Go internal/model/view.go 的 json tag 严格对齐。

export type ViewType = 'list' | 'my'

export interface View {
  name: string
  description?: string
  type: ViewType
  jobs?: string[]
  job_regex?: string
  columns?: string[]
  filter_queue?: boolean
  filter_executors?: boolean
  created_at?: string
  updated_at?: string
}

// 视图列（与后端 ViewColumns 对齐，供编辑页勾选）。
export interface ViewColumnOption {
  id: string
  title: string
}

export const VIEW_COLUMNS: ViewColumnOption[] = [
  { id: 'status', title: '状态' },
  { id: 'weather', title: '气象' },
  { id: 'name', title: '名称' },
  { id: 'lastSuccess', title: '上次成功' },
  { id: 'lastFailure', title: '上次失败' },
  { id: 'lastDuration', title: '上次花费时间' },
  { id: 'buildButton', title: '构建按钮' },
]

// 视图类型选项（对齐 Jenkins New View）。
export const VIEW_TYPES: { value: ViewType; title: string; desc: string }[] = [
  { value: 'list', title: '列表视图', desc: '显示简单列表。你可以从中选择任务来显示在某个视图中' },
  { value: 'my', title: '我的视图', desc: '该视图自动显示当前用户有权限访问的任务' },
]

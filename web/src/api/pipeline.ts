import { http } from './request'
import type { Build } from '@/types/build'
import type { NotifyContact, NotifyTestResult, Pipeline, PipelinePayload } from '@/types/pipeline'
import type { BuildListItem, DashboardData, PipelineSummary } from '@/types/summary'

export const pipelineApi = {
  list(): Promise<Pipeline[]> {
    return http.get<Pipeline[]>('/pipelines')
  },

  // 带构建聚合的任务列表（Jenkins 风格作业列表）。
  overview(): Promise<PipelineSummary[]> {
    return http.get<PipelineSummary[]>('/overview')
  },

  // 首页仪表盘（只统计当前用户可见任务）。
  dashboard(): Promise<DashboardData> {
    return http.get<DashboardData>('/dashboard')
  },

  // 跨任务的构建历史。
  allBuilds(): Promise<BuildListItem[]> {
    return http.get<BuildListItem[]>('/builds')
  },

  // 分页/筛选版构建历史：返回当前页与总条数（总数来自 X-Total-Count）。
  async allBuildsPage(params: {
    page?: number
    pageSize?: number
    q?: string
    status?: string
    pipeline?: string
  }): Promise<{ items: BuildListItem[]; total: number }> {
    const query = new URLSearchParams()
    query.set('limit', String(params.pageSize ?? 20))
    query.set('offset', String(((params.page ?? 1) - 1) * (params.pageSize ?? 20)))
    if (params.q) query.set('q', params.q)
    if (params.status) query.set('status', params.status)
    if (params.pipeline) query.set('pipeline', params.pipeline)
    const result = await http.getPage<BuildListItem[]>(`/builds?${query.toString()}`)
    return { items: result.data, total: result.total }
  },

  get(id: string): Promise<Pipeline> {
    return http.get<Pipeline>(`/pipelines/${id}`)
  },

  create(payload: PipelinePayload): Promise<Pipeline> {
    return http.post<Pipeline>('/pipelines', payload)
  },

  update(id: string, payload: PipelinePayload): Promise<Pipeline> {
    return http.put<Pipeline>(`/pipelines/${id}`, payload)
  },

  remove(id: string): Promise<null> {
    return http.del<null>(`/pipelines/${id}`)
  },

  // 导出任务配置：与 Jenkins 的 job/{name}/config.xml 对应，返回裸 JSON 文档。
  exportConfig(id: string): Promise<string> {
    return http.getText(`/pipelines/${id}/config.json`)
  },

  // 触发构建：params 仅对「参数化构建」任务有效，未传的参数由后端取默认值。
  build(id: string, params?: Record<string, string>): Promise<Build> {
    return http.post<Build>(`/pipelines/${id}/build`, params ? { params } : {})
  },

  // 用历史构建的参数重新触发；密码参数由后端解密源构建的快照，前端不接触明文。
  rerun(id: string, buildId: string): Promise<Build> {
    return http.post<Build>(`/pipelines/${id}/builds/${buildId}/rerun`)
  },

  rename(id: string, name: string): Promise<Pipeline> {
    return http.post<Pipeline>(`/pipelines/${id}/rename`, { name })
  },

  // 通知选人：平台用户及其联系方式（需任务配置权限）。
  notifyContacts(): Promise<NotifyContact[]> {
    return http.get<NotifyContact[]>('/notify/contacts')
  },

  // 向该任务当前配置的收件人/群发送测试通知。
  testNotify(id: string): Promise<NotifyTestResult> {
    return http.post<NotifyTestResult>(`/pipelines/${id}/notify/test`)
  },
}

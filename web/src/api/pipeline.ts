import { http } from './request'
import type { Build } from '@/types/build'
import type { Pipeline, PipelinePayload } from '@/types/pipeline'
import type { BuildListItem, PipelineSummary } from '@/types/summary'

export const pipelineApi = {
  list(): Promise<Pipeline[]> {
    return http.get<Pipeline[]>('/pipelines')
  },

  // 带构建聚合的任务列表（Jenkins 风格作业列表）。
  overview(): Promise<PipelineSummary[]> {
    return http.get<PipelineSummary[]>('/overview')
  },

  // 跨任务的构建历史。
  allBuilds(): Promise<BuildListItem[]> {
    return http.get<BuildListItem[]>('/builds')
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

  build(id: string): Promise<Build> {
    return http.post<Build>(`/pipelines/${id}/build`)
  },

  rename(id: string, name: string): Promise<Pipeline> {
    return http.post<Pipeline>(`/pipelines/${id}/rename`, { name })
  },
}

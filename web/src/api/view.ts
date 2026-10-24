import { http } from './request'
import type { View } from '@/types/view'
import type { PipelineSummary } from '@/types/summary'

// 视图接口：给任务归类（列表视图 + 我的视图）。
export const viewApi = {
  list(): Promise<View[]> {
    return http.get<View[]>('/views')
  },

  // 视图内任务（含构建聚合；后端按视图类型与当前用户权限解析）。
  jobs(name: string): Promise<PipelineSummary[]> {
    return http.get<PipelineSummary[]>(`/views/${encodeURIComponent(name)}/jobs`)
  },

  create(payload: Partial<View>): Promise<View> {
    return http.post<View>('/views', payload)
  },

  // 编辑视图：名称不可改（对齐 Jenkins），后端忽略请求体中的 name。
  update(name: string, payload: Partial<View>): Promise<View> {
    return http.put<View>(`/views/${encodeURIComponent(name)}`, payload)
  },

  remove(name: string): Promise<null> {
    return http.del<null>(`/views/${encodeURIComponent(name)}`)
  },
}

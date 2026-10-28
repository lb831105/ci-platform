import { http } from './request'
import type { ArtifactManifest, Build, BuildLogPayload } from '@/types/build'

export const buildApi = {
  list(pipelineId: string): Promise<Build[]> {
    return http.get<Build[]>(`/pipelines/${pipelineId}/builds`)
  },

  get(pipelineId: string, buildId: string): Promise<Build> {
    return http.get<Build>(`/pipelines/${pipelineId}/builds/${buildId}`)
  },

  // 构建页备注 / 永久保留：指针语义由后端处理，未传字段保持不变。
  updateMetadata(
    pipelineId: string,
    buildId: string,
    payload: { description?: string; keep_forever?: boolean },
  ): Promise<Build> {
    return http.put<Build>(`/pipelines/${pipelineId}/builds/${buildId}`, payload)
  },

  log(
    pipelineId: string,
    buildId: string,
    options: { tail?: number; grep?: string } = {},
  ): Promise<BuildLogPayload> {
    const query = new URLSearchParams()
    if (options.tail && options.tail > 0) query.set('tail', String(options.tail))
    if (options.grep) query.set('grep', options.grep)
    const suffix = query.size > 0 ? `?${query.toString()}` : ''
    return http.get<BuildLogPayload>(`/pipelines/${pipelineId}/builds/${buildId}/log${suffix}`)
  },

  downloadLog(pipelineId: string, buildId: string): Promise<Blob> {
    return http.download(`/pipelines/${pipelineId}/builds/${buildId}/log/download`)
  },

  artifacts(pipelineId: string, buildId: string): Promise<ArtifactManifest> {
    return http.get<ArtifactManifest>(`/pipelines/${pipelineId}/builds/${buildId}/artifacts`)
  },

  downloadArtifact(pipelineId: string, buildId: string, file: string): Promise<Blob> {
    return http.download(
      `/pipelines/${pipelineId}/builds/${buildId}/artifacts/download?file=${encodeURIComponent(file)}`,
    )
  },

  downloadArtifactsZip(pipelineId: string, buildId: string): Promise<Blob> {
    return http.download(`/pipelines/${pipelineId}/builds/${buildId}/artifacts.zip`)
  },

  // 跨任务产物晋级：把源构建产物复制到目标任务（targetBuildId 为空/ latest 时取最近成功构建）。
  promoteArtifacts(
    targetPipelineId: string,
    targetBuildId: string,
    payload: {
      source_pipeline_id: string
      source_build_id: string
      prefix?: string
      files?: string[]
      overwrite?: boolean
    },
  ): Promise<ArtifactManifest> {
    const bid = targetBuildId.trim() || 'latest'
    return http.post<ArtifactManifest>(
      `/pipelines/${targetPipelineId}/builds/${bid}/artifacts/promote`,
      payload,
    )
  },

  // 把该次构建已归档的产物直接发布到 SSH 服务器（不重新编译）。
  // publish 指定任务里的哪个发布目标（名称）；为空时用第一个启用的目标。
  republish(
    pipelineId: string,
    buildId: string,
    serverId?: string,
    publish?: string,
  ): Promise<void> {
    return http.post(`/pipelines/${pipelineId}/builds/${buildId}/republish`, {
      server_id: serverId ?? '',
      publish: publish ?? '',
    })
  },

  // 取消运行中/等待中的构建（对齐 Jenkins Stop Build）。
  cancel(pipelineId: string, buildId: string): Promise<null> {
    return http.post(`/pipelines/${pipelineId}/builds/${buildId}/cancel`)
  },

  // 发布审批：放行 / 拒绝 pending_approval 构建。
  approve(pipelineId: string, buildId: string, comment = ''): Promise<Build> {
    return http.post<Build>(`/pipelines/${pipelineId}/builds/${buildId}/approve`, { comment })
  },

  reject(pipelineId: string, buildId: string, comment: string): Promise<Build> {
    return http.post<Build>(`/pipelines/${pipelineId}/builds/${buildId}/reject`, { comment })
  },

  // 删除构建：连同 data/jobs/{pid}/builds/{bid} 目录（日志 + 产物）一起删除。
  remove(pipelineId: string, buildId: string): Promise<null> {
    return http.del<null>(`/pipelines/${pipelineId}/builds/${buildId}`)
  },
}

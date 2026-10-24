import { http } from './request'
import type { ArtifactManifest, Build, BuildLogPayload } from '@/types/build'

export const buildApi = {
  list(pipelineId: string): Promise<Build[]> {
    return http.get<Build[]>(`/pipelines/${pipelineId}/builds`)
  },

  get(pipelineId: string, buildId: string): Promise<Build> {
    return http.get<Build>(`/pipelines/${pipelineId}/builds/${buildId}`)
  },

  log(pipelineId: string, buildId: string): Promise<BuildLogPayload> {
    return http.get<BuildLogPayload>(`/pipelines/${pipelineId}/builds/${buildId}/log`)
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

  // 把该次构建已归档的产物直接发布到 SSH 服务器（不重新编译）。
  republish(pipelineId: string, buildId: string, serverId?: string): Promise<void> {
    return http.post(`/pipelines/${pipelineId}/builds/${buildId}/republish`, { server_id: serverId ?? '' })
  },

  // 取消运行中/等待中的构建（对齐 Jenkins Stop Build）。
  cancel(pipelineId: string, buildId: string): Promise<null> {
    return http.post(`/pipelines/${pipelineId}/builds/${buildId}/cancel`)
  },

  // 删除构建：连同 data/jobs/{pid}/builds/{bid} 目录（日志 + 产物）一起删除。
  remove(pipelineId: string, buildId: string): Promise<null> {
    return http.del<null>(`/pipelines/${pipelineId}/builds/${buildId}`)
  },
}

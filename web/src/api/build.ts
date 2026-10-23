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
}

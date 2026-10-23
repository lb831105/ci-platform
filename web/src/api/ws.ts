// WebSocket 地址从 window.location 推导：
// 开发期走 Vite proxy（/ws 转发到 Go 服务），生产期同源直连。
// 浏览器无法为 WS 握手设置自定义头，因此 token 通过查询参数携带。

import { getToken } from './token'

export function buildLogSocketUrl(pipelineId: string, buildId: string): string {
  const scheme = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
  const path = `/ws/pipelines/${encodeURIComponent(pipelineId)}/builds/${encodeURIComponent(buildId)}/logs`
  const token = getToken()
  const query = token ? `?token=${encodeURIComponent(token)}` : ''
  return `${scheme}//${window.location.host}${path}${query}`
}

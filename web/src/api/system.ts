import { http } from './request'
import type {
  AuditEntry,
  Credential,
  CredentialPayload,
  DataHomeInfo,
  Feature,
  LoadStats,
  SystemLogResponse,
  NotifyHistoryQuery,
  NotifyRecord,
  OldDataReport,
  Settings,
  SystemInfo,
} from '@/types/system'

export const systemApi = {
  info(): Promise<SystemInfo> {
    return http.get<SystemInfo>('/system/info')
  },

  load(): Promise<LoadStats> {
    return http.get<LoadStats>('/system/load')
  },

  log(lines = 500): Promise<SystemLogResponse> {
    return http.get<SystemLogResponse>(`/system/log?lines=${lines}`)
  },

  clearLog(): Promise<null> {
    return http.del<null>('/system/log')
  },

  features(): Promise<Feature[]> {
    return http.get<Feature[]>('/system/features')
  },

  getSettings(): Promise<Settings> {
    return http.get<Settings>('/system/settings')
  },

  updateSettings(payload: Settings): Promise<Settings> {
    return http.put<Settings>('/system/settings', payload)
  },

  reload(): Promise<Settings> {
    return http.post<Settings>('/system/reload')
  },

  setQuiet(quiet: boolean): Promise<Settings> {
    return http.post<Settings>('/system/quiet', { quiet })
  },

  testEmail(to: string): Promise<null> {
    return http.post<null>('/system/email/test', { to })
  },

  // 发送 Webhook 测试通知（钉钉/企微/Slack）。
  testWebhookNotify(): Promise<null> {
    return http.post<null>('/system/webhook/test')
  },

  // 操作审计日志（最近 limit 条，时间倒序）。
  audit(limit = 300): Promise<AuditEntry[]> {
    return http.get<AuditEntry[]>(`/system/audit?limit=${limit}`)
  },

  oldData(): Promise<OldDataReport> {
    return http.get<OldDataReport>('/system/old-data')
  },

  getDataHome(): Promise<DataHomeInfo> {
    return http.get<DataHomeInfo>('/system/data-home')
  },

  updateDataHome(payload: { data_home: string; migrate: boolean }): Promise<DataHomeInfo> {
    return http.put<DataHomeInfo>('/system/data-home', payload)
  },

  cleanOldData(): Promise<OldDataReport> {
    return http.post<OldDataReport>('/system/old-data/clean')
  },

  // 通知投递记录：按任务/收件人/状态过滤，用于排查「谁漏收了告警」。
  notifyHistory(query: NotifyHistoryQuery = {}): Promise<NotifyRecord[]> {
    const params = new URLSearchParams()
    if (query.limit) params.set('limit', String(query.limit))
    if (query.pipeline) params.set('pipeline', query.pipeline)
    if (query.recipient) params.set('recipient', query.recipient)
    if (query.status) params.set('status', query.status)
    const qs = params.toString()
    return http.get<NotifyRecord[]>(`/system/notify${qs ? '?' + qs : ''}`)
  },
}

export const credentialApi = {
  list(): Promise<Credential[]> {
    return http.get<Credential[]>('/credentials')
  },

  create(payload: CredentialPayload): Promise<Credential> {
    return http.post<Credential>('/credentials', payload)
  },

  update(id: string, payload: CredentialPayload): Promise<Credential> {
    return http.put<Credential>(`/credentials/${id}`, payload)
  },

  remove(id: string): Promise<null> {
    return http.del<null>(`/credentials/${id}`)
  },
}

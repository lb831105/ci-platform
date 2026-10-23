import { http } from './request'
import type {
  Credential,
  CredentialPayload,
  DataHomeInfo,
  Feature,
  LoadStats,
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

  log(lines = 500): Promise<{ lines: string[] }> {
    return http.get<{ lines: string[] }>(`/system/log?lines=${lines}`)
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

import { http } from './request'
import type { APIToken, CreateAPITokenResult, Profile } from '@/types/profile'

// 个人资料接口：任何已登录用户都可用（不需要 Administer）。
export const profileApi = {
  get(): Promise<Profile> {
    return http.get<Profile>('/profile')
  },

  updateContact(payload: {
    email: string
    wecom_id: string
    phone: string
  }): Promise<Profile> {
    return http.put<Profile>('/profile/contact', payload)
  },

  // 发送邮箱验证码（SMTP 未启用时后端返回可读错误）。
  requestEmailVerify(): Promise<{ email: string; expires_in_sec: number }> {
    return http.post<{ email: string; expires_in_sec: number }>('/profile/email/verify')
  },

  confirmEmail(code: string): Promise<Profile> {
    return http.post<Profile>('/profile/email/confirm', { code })
  },
}

// API Token 管理：secret 只在创建响应里返回一次，列表永远看不到原文。
export const tokenApi = {
  list(): Promise<APIToken[]> {
    return http.get<APIToken[]>('/profile/tokens')
  },

  create(name: string, expiresInDays: number, scopes: string[]): Promise<CreateAPITokenResult> {
    return http.post<CreateAPITokenResult>('/profile/tokens', {
      name,
      expires_in_days: expiresInDays,
      scopes,
    })
  },

  remove(id: string): Promise<void> {
    return http.del<void>(`/profile/tokens/${encodeURIComponent(id)}`)
  },
}

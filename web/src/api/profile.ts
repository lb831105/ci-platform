import { http } from './request'
import type { Profile } from '@/types/profile'

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

import { http } from './request'

export interface AuthStatus {
  enabled: boolean
  authenticated: boolean
  username: string
}

export interface LoginResult {
  token: string
  username: string
  expires_at: string
}

export const authApi = {
  // 公开：探测认证是否开启、当前请求是否已登录。
  status(): Promise<AuthStatus> {
    return http.get<AuthStatus>('/auth/status')
  },

  login(username: string, password: string): Promise<LoginResult> {
    return http.post<LoginResult>('/auth/login', { username, password })
  },

  me(): Promise<AuthStatus> {
    return http.get<AuthStatus>('/auth/me')
  },

  logout(): Promise<null> {
    return http.post<null>('/auth/logout')
  },

  changePassword(oldPassword: string, newPassword: string): Promise<null> {
    return http.post<null>('/auth/password', { old_password: oldPassword, new_password: newPassword })
  },
}

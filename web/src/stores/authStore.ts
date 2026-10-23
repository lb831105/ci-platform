import { ref } from 'vue'
import { defineStore } from 'pinia'
import { authApi } from '@/api/auth'
import { clearToken, getStoredUsername, getToken, setToken } from '@/api/token'
import { describeError } from '@/api/request'

// 认证状态：判断是否需要登录、当前用户名，以及登录/登出动作。
export const useAuthStore = defineStore('auth', () => {
  const enabled = ref(true)
  const authenticated = ref(false)
  const username = ref(getStoredUsername())
  const initialized = ref(false)

  // bootstrap 在应用启动（路由首次导航）时调用一次：
  // 探测认证开关，并用手上已有的 token 换取登录态。
  async function bootstrap(): Promise<void> {
    if (initialized.value) {
      return
    }
    try {
      const status = await authApi.status()
      enabled.value = status.enabled
      authenticated.value = status.authenticated
      username.value = status.username || getStoredUsername()
      if (status.enabled && !status.authenticated) {
        clearToken()
      }
    } catch {
      // 探测失败时按未登录处理，避免把用户永久挡在加载页。
      authenticated.value = false
    } finally {
      initialized.value = true
    }
  }

  async function login(user: string, password: string): Promise<void> {
    const result = await authApi.login(user, password)
    setToken(result.token, result.username)
    enabled.value = true
    authenticated.value = true
    username.value = result.username
    initialized.value = true
  }

  async function logout(): Promise<void> {
    try {
      await authApi.logout()
    } catch (e) {
      // 登出失败不阻塞本地清理，仅提示。
      console.warn('logout failed:', describeError(e))
    } finally {
      clearToken()
      authenticated.value = false
      username.value = ''
    }
  }

  return { enabled, authenticated, username, initialized, bootstrap, login, logout, hasToken: () => getToken() !== '' }
})

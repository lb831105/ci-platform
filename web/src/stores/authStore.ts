import { computed, ref } from 'vue'
import { defineStore } from 'pinia'
import { authApi } from '@/api/auth'
import { clearToken, getStoredUsername, getToken, setToken } from '@/api/token'
import { describeError } from '@/api/request'
import type { JobRoleRule } from '@/api/auth'

// 认证状态：判断是否需要登录、当前用户名，以及登录/登出动作。
export const useAuthStore = defineStore('auth', () => {
  const enabled = ref(true)
  const authenticated = ref(false)
  const username = ref(getStoredUsername())
  const initialized = ref(false)
  // 有效权限列表（/auth/me 或 /auth/status 返回）；前端据此隐藏入口/按钮。
  const permissions = ref<string[]>([])
  // 任务角色规则：只被授予任务角色的用户，权限按任务名 Pattern 生效。
  const jobRoles = ref<JobRoleRule[]>([])

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
      if (status.enabled && status.authenticated) {
        // 已认证时一并恢复有效权限：否则页面刷新（F5）后 permissions 仍为空，
        // 前端所有入口/按钮会错误地消失（后端权限其实还在）。
        try {
          const me = await authApi.me()
          permissions.value = me.permissions ?? []
          jobRoles.value = me.job_roles ?? []
        } catch {
          permissions.value = []
          jobRoles.value = []
        }
      }
      if (status.enabled && !status.authenticated) {
        // 未认证时清理残留 token，避免后续请求继续携带失效凭据。
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
    // 登录后拉取有效权限（含 RBAC 角色授予的部分）。
    try {
      const me = await authApi.me()
      permissions.value = me.permissions ?? []
      jobRoles.value = me.job_roles ?? []
    } catch {
      permissions.value = []
      jobRoles.value = []
    }
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
      permissions.value = []
    }
  }

  // hasPerm 判断当前用户是否拥有权限。后端是最终裁决，这里只做入口隐藏。
  function hasPerm(perm: string): boolean {
    return permissions.value.includes(perm)
  }

  // canJob 判断当前用户对「某个具体任务」是否拥有权限：
  // 全局权限直接true；否则看任务角色 Pattern 是否匹配该任务名。
  // 这样只被授予任务角色的用户，也能看到自己任务上的「构建/配置」按钮。
  function canJob(perm: string, jobName: string): boolean {
    if (hasPerm(perm)) {
      return true
    }
    for (const rule of jobRoles.value) {
      if (!rule.permissions.includes(perm)) {
        continue
      }
      try {
        // 与后端一致：忽略大小写、完全匹配
        if (new RegExp(`^(?:${rule.pattern})$`, 'i').test(jobName)) {
          return true
        }
      } catch {
        // Pattern 非法时不授予（后端保存时已校验）
      }
    }
    return false
  }

  return {
    enabled,
    authenticated,
    username,
    initialized,
    permissions,
    jobRoles,
    hasPerm,
    canJob,
    isAdmin: computed(() => hasPerm('overall.administer')),
    bootstrap,
    login,
    logout,
    hasToken: () => getToken() !== '',
  }
})

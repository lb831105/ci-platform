// 会话 token 的本地持久化。
// 单独成模块是为了让 request 层（非 Vue 上下文）也能读写 token，
// 同时避免 request ↔ store 之间产生循环依赖。

const TOKEN_KEY = 'ci_token'
const USER_KEY = 'ci_username'

export function getToken(): string {
  return localStorage.getItem(TOKEN_KEY) ?? ''
}

export function getStoredUsername(): string {
  return localStorage.getItem(USER_KEY) ?? ''
}

export function setToken(token: string, username = ''): void {
  localStorage.setItem(TOKEN_KEY, token)
  if (username) {
    localStorage.setItem(USER_KEY, username)
  }
}

export function clearToken(): void {
  localStorage.removeItem(TOKEN_KEY)
  localStorage.removeItem(USER_KEY)
}

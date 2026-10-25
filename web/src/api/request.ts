import { clearToken, getToken } from './token'

// 统一响应包装（与 Go internal/pkg/response.go 对齐）。
export interface ApiResponse<T> {
  code: number
  msg: string
  data: T
}

// 业务错误，携带服务端 code。
export class ApiError extends Error {
  readonly code: number

  constructor(code: number, message: string) {
    super(message)
    this.name = 'ApiError'
    this.code = code
  }
}

const BASE_URL = '/api'

// 与 Go internal/pkg/errcode.go 的 CodeUnauthorized 对齐。
const CODE_UNAUTHORIZED = 40100

const JSON_HEADERS: Record<string, string> = {
  'Content-Type': 'application/json',
}

// redirectToLogin 清理本地会话并跳转登录页，携带回跳地址。
// 同时防止在登录页上重复触发跳转。
function redirectToLogin(): void {
  clearToken()
  if (window.location.pathname === '/login') {
    return
  }
  const redirect = encodeURIComponent(window.location.pathname + window.location.search)
  window.location.assign(`/login?redirect=${redirect}`)
}

async function requestBlob(path: string): Promise<Blob> {
  const headers = new Headers()
  const token = getToken()
  if (token) {
    headers.set('Authorization', `Bearer ${token}`)
  }
  const response = await fetch(`${BASE_URL}${path}`, { headers })
  if (!response.ok) {
    throw new ApiError(response.status, `HTTP ${response.status}`)
  }
  const contentType = response.headers.get('Content-Type') ?? ''
  // 后端错误仍返回统一 JSON（HTTP 200），下载接口成功时才是二进制。
  if (contentType.includes('application/json')) {
    const body = (await response.json()) as ApiResponse<unknown>
    if (body.code === CODE_UNAUTHORIZED) {
      redirectToLogin()
      throw new ApiError(body.code, body.msg)
    }
    if (body.code !== 0) {
      throw new ApiError(body.code, body.msg)
    }
  }
  return response.blob()
}

async function request<T>(path: string, init: RequestInit = {}): Promise<T> {
  const headers = new Headers(init.headers)
  const token = getToken()
  if (token) {
    headers.set('Authorization', `Bearer ${token}`)
  }
  const response = await fetch(`${BASE_URL}${path}`, { ...init, headers })
  if (!response.ok) {
    throw new ApiError(response.status, `HTTP ${response.status}`)
  }
  const body = (await response.json()) as ApiResponse<T>
  if (body.code === CODE_UNAUTHORIZED) {
    redirectToLogin()
    throw new ApiError(body.code, body.msg)
  }
  if (body.code !== 0) {
    throw new ApiError(body.code, body.msg)
  }
  return body.data
}

// 只做请求封装与类型标注，不含任何业务判断。
export const http = {
  get<T>(path: string): Promise<T> {
    return request<T>(path)
  },
  post<T>(path: string, body?: unknown): Promise<T> {
    return request<T>(path, {
      method: 'POST',
      headers: JSON_HEADERS,
      body: body === undefined ? undefined : JSON.stringify(body),
    })
  },
  put<T>(path: string, body: unknown): Promise<T> {
    return request<T>(path, {
      method: 'PUT',
      headers: JSON_HEADERS,
      body: JSON.stringify(body),
    })
  },
  del<T>(path: string): Promise<T> {
    return request<T>(path, { method: 'DELETE' })
  },
  // 取纯文本响应（如导出的 config.json 文档：不是统一包装，需要原样保存）。
  async getText(path: string): Promise<string> {
    const headers = new Headers()
    const token = getToken()
    if (token) {
      headers.set('Authorization', `Bearer ${token}`)
    }
    const response = await fetch(`${BASE_URL}${path}`, { headers })
    if (!response.ok) {
      throw new ApiError(response.status, `HTTP ${response.status}`)
    }
    const text = await response.text()
    // 出错时后端仍返回统一 JSON 包装，需要按业务码提示，而不是把错误 JSON 存成配置文件。
    if (text.trimStart().startsWith('{')) {
      try {
        const body = JSON.parse(text) as ApiResponse<unknown>
        if (typeof body.code === 'number' && body.code !== 0) {
          if (body.code === CODE_UNAUTHORIZED) {
            redirectToLogin()
          }
          throw new ApiError(body.code, body.msg)
        }
      } catch (error) {
        if (error instanceof ApiError) {
          throw error
        }
      }
    }
    return text
  },
  // 原样发送文本请求体（如 Jenkins config.xml），不走 JSON 序列化。
  postRaw<T>(path: string, body: string, contentType: string): Promise<T> {
    return request<T>(path, {
      method: 'POST',
      headers: { 'Content-Type': contentType },
      body,
    })
  },
  download(path: string): Promise<Blob> {
    return requestBlob(path)
  },
}

// 把未知异常转换为可展示文案。
export function describeError(error: unknown): string {
  if (error instanceof ApiError) {
    return error.message
  }
  if (error instanceof Error) {
    return error.message
  }
  return String(error)
}

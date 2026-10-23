/**
 * 将时间字符串格式化为本地可读时间；空值返回占位符。
 */
export function formatTime(value: string | null | undefined, fallback = '-'): string {
  if (!value) {
    return fallback
  }
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) {
    return fallback
  }
  const pad = (n: number): string => String(n).padStart(2, '0')
  return (
    `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())} ` +
    `${pad(date.getHours())}:${pad(date.getMinutes())}:${pad(date.getSeconds())}`
  )
}

/**
 * 根据开始/结束时间计算耗时可读文本。
 */
export function formatDuration(startedAt: string | null, finishedAt: string | null): string {
  if (!startedAt) {
    return '-'
  }
  const start = new Date(startedAt).getTime()
  const end = finishedAt ? new Date(finishedAt).getTime() : Date.now()
  if (Number.isNaN(start) || Number.isNaN(end) || end < start) {
    return '-'
  }
  const ms = end - start
  if (ms < 1000) {
    return `${ms}ms`
  }
  const seconds = ms / 1000
  if (seconds < 60) {
    return `${seconds.toFixed(1)}s`
  }
  const minutes = Math.floor(seconds / 60)
  return `${minutes}m${Math.round(seconds % 60)}s`
}

/**
 * 把任意文本按行切分，保留空行以维持终端观感。
 */
export function splitLines(text: string): string[] {
  if (text === '') {
    return []
  }
  return text.replace(/\r\n/g, '\n').split('\n')
}

/**
 * 把字节数格式化为可读大小（B / KB / MB / GB）。
 */
export function formatBytes(bytes: number | null | undefined): string {
  if (!bytes || bytes <= 0) {
    return '0 B'
  }
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let value = bytes
  let i = 0
  while (value >= 1024 && i < units.length - 1) {
    value /= 1024
    i++
  }
  return `${value.toFixed(i === 0 ? 0 : 2)} ${units[i]}`
}

const RELATIVE_UNITS: Array<[string, number]> = [
  ['年', 365 * 24 * 3600],
  ['月', 30 * 24 * 3600],
  ['天', 24 * 3600],
  ['小时', 3600],
  ['分', 60],
  ['秒', 1],
]

/**
 * 按 Jenkins 风格输出「距今多久」：如「3 天 4 小时」「11 月」「41 秒」。
 * 取最高的两个非零单位；无值返回 fallback（Jenkins 用「没有」）。
 */
export function formatRelativeTime(value: string | null | undefined, fallback = '没有'): string {
  if (!value) {
    return fallback
  }
  const then = new Date(value).getTime()
  if (Number.isNaN(then)) {
    return fallback
  }
  const seconds = Math.max(0, Math.floor((Date.now() - then) / 1000))
  if (seconds < 5) {
    return '刚刚'
  }
  const parts: string[] = []
  let remaining = seconds
  for (const [label, size] of RELATIVE_UNITS) {
    const n = Math.floor(remaining / size)
    if (n > 0) {
      parts.push(`${n} ${label}`)
      remaining -= n * size
      if (parts.length >= 2) {
        break
      }
    }
  }
  return parts.join(' ')
}

/**
 * 把毫秒耗时格式化为 Jenkins 风格的中文时长：如「4 分 5 秒」「18 分」。
 * 无有效时长返回 fallback。
 */
export function formatDurationCN(ms: number | null | undefined, fallback = '-'): string {
  if (ms === null || ms === undefined || ms <= 0) {
    return fallback
  }
  if (ms < 1000) {
    return `${ms} 毫秒`
  }
  const seconds = Math.floor(ms / 1000)
  const parts: string[] = []
  let remaining = seconds
  for (const [label, size] of RELATIVE_UNITS) {
    if (size < 60) {
      continue // 时长不需要「秒」以下之外的更小单位，秒最后单独处理
    }
    const n = Math.floor(remaining / size)
    if (n > 0) {
      parts.push(`${n} ${label}`)
      remaining -= n * size
      if (parts.length >= 2) {
        break
      }
    }
  }
  if (parts.length < 2 && remaining > 0) {
    parts.push(`${remaining} 秒`)
  }
  return parts.length > 0 ? parts.join(' ') : fallback
}

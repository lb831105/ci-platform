import { onUnmounted, ref, type Ref } from 'vue'
import { buildApi } from '@/api/build'
import { describeError } from '@/api/request'
import { buildLogSocketUrl } from '@/api/ws'
import { isFinished, type Build, type BuildStatus } from '@/types/build'
import { splitLines } from '@/utils/format'

const MAX_RECONNECT_ATTEMPTS = 5
const BASE_RECONNECT_DELAY_MS = 1000
// 控制台仅保留最近 N 行，避免长时间构建导致内存与渲染开销过大。
const MAX_LINES = 5000

export interface BuildLogController {
  lines: Ref<string[]>
  status: Ref<BuildStatus>
  connected: Ref<boolean>
  retrying: Ref<boolean>
  error: Ref<string>
  finished: Ref<boolean>
  start: () => Promise<void>
  stop: () => void
  loadInitial: () => Promise<void>
  refreshStatus: () => Promise<Build | null>
}

/**
 * 构建日志订阅：
 * 1. 首次进入先 HTTP 拉取 log.txt 全文；
 * 2. 构建未结束时再建立 WebSocket 接收增量；
 * 3. 断线按指数退避重连（1s→2s→4s→8s→16s，最多 5 次）；
 * 4. 组件卸载时关闭连接。
 */
export function useBuildLog(pipelineId: string, buildId: string): BuildLogController {
  const lines = ref<string[]>([])
  const status = ref<BuildStatus>('pending')
  const connected = ref(false)
  const retrying = ref(false)
  const error = ref('')
  const finished = ref(false)

  let socket: WebSocket | null = null
  let attempt = 0
  let stopped = false
  let partial = ''

  function pushLines(incoming: string[]): void {
    if (incoming.length === 0) {
      return
    }
    lines.value.push(...incoming)
    const overflow = lines.value.length - MAX_LINES
    if (overflow > 0) {
      lines.value.splice(0, overflow)
    }
  }

  function appendChunk(chunk: string): void {
    if (chunk === '') {
      return
    }
    const text = partial + chunk
    const parts = text.replace(/\r\n/g, '\n').split('\n')
    partial = parts.pop() ?? ''
    pushLines(parts)
  }

  async function loadInitial(): Promise<void> {
    try {
      const payload = await buildApi.log(pipelineId, buildId)
      lines.value = []
      partial = ''
      pushLines(splitLines(payload.log))
    } catch (e) {
      error.value = describeError(e)
    }
  }

  async function refreshStatus(): Promise<Build | null> {
    try {
      const build = await buildApi.get(pipelineId, buildId)
      status.value = build.status
      finished.value = isFinished(build.status)
      return build
    } catch (e) {
      error.value = describeError(e)
      return null
    }
  }

  function connect(): void {
    if (stopped || finished.value) {
      return
    }
    socket = new WebSocket(buildLogSocketUrl(pipelineId, buildId))

    socket.onopen = () => {
      connected.value = true
      retrying.value = false
      attempt = 0
      error.value = ''
    }

    socket.onmessage = (event: MessageEvent<string | Blob>) => {
      if (typeof event.data === 'string') {
        appendChunk(event.data)
      }
    }

    socket.onclose = () => {
      connected.value = false
      socket = null
      if (stopped || finished.value) {
        return
      }
      scheduleReconnect()
    }
  }

  function scheduleReconnect(): void {
    if (attempt >= MAX_RECONNECT_ATTEMPTS) {
      retrying.value = false
      error.value = `日志连接已断开，重连 ${MAX_RECONNECT_ATTEMPTS} 次仍失败`
      return
    }
    const delay = BASE_RECONNECT_DELAY_MS * 2 ** attempt
    attempt += 1
    retrying.value = true

    window.setTimeout(() => {
      void (async () => {
        if (stopped) {
          return
        }
        // 重连前先补齐状态并重载全文日志，避免丢失断线期间的输出。
        await refreshStatus()
        if (finished.value) {
          await loadInitial()
          retrying.value = false
          return
        }
        await loadInitial()
        connect()
      })()
    }, delay)
  }

  async function start(): Promise<void> {
    stopped = false
    await refreshStatus()
    await loadInitial()
    if (!finished.value) {
      connect()
    }
  }

  function stop(): void {
    stopped = true
    retrying.value = false
    if (socket !== null) {
      socket.close()
      socket = null
    }
    connected.value = false
  }

  onUnmounted(stop)

  return { lines, status, connected, retrying, error, finished, start, stop, loadInitial, refreshStatus }
}

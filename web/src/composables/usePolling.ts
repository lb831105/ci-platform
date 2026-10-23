import { onUnmounted, ref, type Ref } from 'vue'

export interface PollingController {
  active: Ref<boolean>
  start: () => void
  stop: () => void
}

/**
 * 周期性执行任务的组合式函数。
 * 使用 setTimeout 串行调度而非 setInterval，避免上一次请求未返回时叠加。
 */
export function usePolling(task: () => void | Promise<void>, intervalMs = 3000): PollingController {
  const active = ref(false)
  let timer: number | null = null

  const tick = async (): Promise<void> => {
    try {
      await task()
    } finally {
      if (active.value) {
        timer = window.setTimeout(() => {
          void tick()
        }, intervalMs)
      }
    }
  }

  function start(): void {
    if (active.value) {
      return
    }
    active.value = true
    void tick()
  }

  function stop(): void {
    active.value = false
    if (timer !== null) {
      window.clearTimeout(timer)
      timer = null
    }
  }

  onUnmounted(stop)

  return { active, start, stop }
}

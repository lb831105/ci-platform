import { ref } from 'vue'
import { defineStore } from 'pinia'
import { pipelineApi } from '@/api/pipeline'
import { describeError } from '@/api/request'
import type { PipelineSummary } from '@/types/summary'

// 任务概览数据由左侧边栏 jobs 与右侧作业列表共享，集中在这里缓存，避免重复请求。
export const useOverviewStore = defineStore('overview', () => {
  const items = ref<PipelineSummary[]>([])
  const loading = ref(false)
  const loaded = ref(false)
  const error = ref('')

  async function fetch(force = false): Promise<void> {
    if (loading.value) {
      return
    }
    if (loaded.value && !force) {
      return
    }
    // 首次加载才显示全屏 loading；后台轮询 / 刷新不遮挡表格。
    const first = !loaded.value
    if (first) {
      loading.value = true
    }
    try {
      items.value = await pipelineApi.overview()
      error.value = ''
      loaded.value = true
    } catch (e) {
      error.value = describeError(e)
    } finally {
      loading.value = false
    }
  }

  function clear(): void {
    items.value = []
    loaded.value = false
    error.value = ''
  }

  return { items, loading, loaded, error, fetch, clear }
})

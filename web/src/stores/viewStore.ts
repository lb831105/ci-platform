import { ref } from 'vue'
import { defineStore } from 'pinia'
import { viewApi } from '@/api/view'
import { describeError } from '@/api/request'
import type { View } from '@/types/view'

// 视图列表由左侧边栏与视图页共享，集中缓存避免重复请求。
export const useViewStore = defineStore('view', () => {
  const items = ref<View[]>([])
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
    loading.value = true
    try {
      items.value = await viewApi.list()
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

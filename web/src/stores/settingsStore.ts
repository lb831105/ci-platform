import { computed, ref } from 'vue'
import { defineStore } from 'pinia'
import { systemApi } from '@/api/system'
import { describeError } from '@/api/request'
import type { Settings } from '@/types/system'

// 系统配置在顶栏（应用名称）与新建任务表单（默认分支）之间共享。
export const useSettingsStore = defineStore('settings', () => {
  const data = ref<Settings | null>(null)
  const loading = ref(false)
  const error = ref('')

  const displayName = computed(() => data.value?.display_name || 'CI Platform')
  const systemMessage = computed(() => data.value?.system_message || '')
  const defaultBranch = computed(() => data.value?.default_branch || 'main')
  const sshServers = computed(() => data.value?.ssh_servers ?? [])

  async function fetch(force = false): Promise<void> {
    if (loading.value) {
      return
    }
    if (data.value && !force) {
      return
    }
    loading.value = true
    try {
      data.value = await systemApi.getSettings()
      error.value = ''
    } catch (e) {
      error.value = describeError(e)
    } finally {
      loading.value = false
    }
  }

  function set(settings: Settings): void {
    data.value = settings
  }

  return { data, loading, error, displayName, systemMessage, defaultBranch, sshServers, fetch, set }
})

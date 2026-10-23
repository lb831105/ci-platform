import { ref } from 'vue'
import { pipelineApi } from '@/api/pipeline'
import { describeError } from '@/api/request'
import { usePipelineStore } from '@/stores/pipelineStore'
import type { Build } from '@/types/build'
import type { Pipeline, PipelinePayload } from '@/types/pipeline'

/**
 * Pipeline 数据获取与变更的封装：view 只能通过它访问 api 与 store。
 */
export function usePipeline() {
  const store = usePipelineStore()
  const loading = ref(false)
  const error = ref('')

  async function fetchList(): Promise<void> {
    loading.value = true
    error.value = ''
    try {
      store.setAll(await pipelineApi.list())
    } catch (e) {
      error.value = describeError(e)
    } finally {
      loading.value = false
    }
  }

  async function fetchOne(id: string): Promise<Pipeline | null> {
    loading.value = true
    error.value = ''
    try {
      const pipeline = await pipelineApi.get(id)
      store.setCurrent(pipeline)
      return pipeline
    } catch (e) {
      error.value = describeError(e)
      return null
    } finally {
      loading.value = false
    }
  }

  async function save(id: string | null, payload: PipelinePayload): Promise<Pipeline> {
    const pipeline = id === null ? await pipelineApi.create(payload) : await pipelineApi.update(id, payload)
    store.upsert(pipeline)
    return pipeline
  }

  async function remove(id: string): Promise<void> {
    await pipelineApi.remove(id)
    store.removeById(id)
  }

  async function triggerBuild(id: string): Promise<Build> {
    return pipelineApi.build(id)
  }

  return { store, loading, error, fetchList, fetchOne, save, remove, triggerBuild }
}

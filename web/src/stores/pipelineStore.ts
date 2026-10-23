import { ref } from 'vue'
import { defineStore } from 'pinia'
import type { Pipeline } from '@/types/pipeline'

// 仅存放跨组件共享的 pipeline 数据。
export const usePipelineStore = defineStore('pipeline', () => {
  const items = ref<Pipeline[]>([])
  const current = ref<Pipeline | null>(null)

  function setAll(list: Pipeline[]): void {
    items.value = list
  }

  function upsert(pipeline: Pipeline): void {
    const index = items.value.findIndex((item) => item.id === pipeline.id)
    if (index >= 0) {
      items.value[index] = pipeline
    } else {
      items.value.unshift(pipeline)
    }
  }

  function removeById(id: string): void {
    items.value = items.value.filter((item) => item.id !== id)
    if (current.value?.id === id) {
      current.value = null
    }
  }

  function setCurrent(pipeline: Pipeline | null): void {
    current.value = pipeline
  }

  return { items, current, setAll, upsert, removeById, setCurrent }
})

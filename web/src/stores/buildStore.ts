import { ref } from 'vue'
import { defineStore } from 'pinia'
import type { Build } from '@/types/build'

// 构建历史按 pipeline_id 分组缓存；当前构建用于日志页展示。
export const useBuildStore = defineStore('build', () => {
  const historyByPipeline = ref<Record<string, Build[]>>({})
  const current = ref<Build | null>(null)

  function setHistory(pipelineId: string, list: Build[]): void {
    historyByPipeline.value = { ...historyByPipeline.value, [pipelineId]: list }
  }

  function getHistory(pipelineId: string): Build[] {
    return historyByPipeline.value[pipelineId] ?? []
  }

  function setCurrent(build: Build | null): void {
    current.value = build
  }

  return { historyByPipeline, current, setHistory, getHistory, setCurrent }
})

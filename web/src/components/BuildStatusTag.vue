<script setup lang="ts">
import { computed } from 'vue'
import type { BuildStatus } from '@/types/build'

const props = defineProps<{ status: BuildStatus }>()

type TagType = 'success' | 'danger' | 'warning' | 'info' | 'primary'

const LABELS: Record<BuildStatus, string> = {
  pending: '等待中',
  pending_approval: '待审批',
  running: '构建中',
  success: '成功',
  failed: '失败',
  aborted: '已取消',
}

const TYPES: Record<BuildStatus, TagType> = {
  pending: 'info',
  pending_approval: 'warning',
  running: 'warning',
  success: 'success',
  failed: 'danger',
  aborted: 'info',
}

const label = computed(() => LABELS[props.status] ?? props.status)
const type = computed<TagType>(() => TYPES[props.status] ?? 'info')
</script>

<template>
  <el-tag :type="type" disable-transitions>{{ label }}</el-tag>
</template>

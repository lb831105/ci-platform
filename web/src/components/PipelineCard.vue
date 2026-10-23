<script setup lang="ts">
import { computed } from 'vue'
import type { Pipeline } from '@/types/pipeline'

const props = defineProps<{ pipeline: Pipeline }>()

const emit = defineEmits<{
  (event: 'build'): void
  (event: 'history'): void
  (event: 'edit'): void
  (event: 'remove'): void
}>()

const sshEnabled = computed(() => props.pipeline.ssh_publish?.enabled === true)
</script>

<template>
  <el-card shadow="hover" class="pipeline-card">
    <div class="pipeline-card__head">
      <div>
        <div class="pipeline-card__name">{{ pipeline.name }}</div>
        <div class="muted mono">{{ pipeline.id }}</div>
      </div>
      <el-tag v-if="sshEnabled" type="warning" effect="plain" disable-transitions>SSH 发布</el-tag>
    </div>

    <el-descriptions :column="1" size="small" class="pipeline-card__meta">
      <el-descriptions-item label="执行方式">本地服务器</el-descriptions-item>
      <el-descriptions-item label="步骤">{{ pipeline.steps.length }} 个</el-descriptions-item>
      <el-descriptions-item label="仓库">
        <span class="mono">{{ pipeline.git_url || '（未配置，直接使用工作区）' }}</span>
        <span v-if="pipeline.git_url" class="muted"> @ {{ pipeline.git_branch }}</span>
      </el-descriptions-item>
    </el-descriptions>

    <template #footer>
      <div class="pipeline-card__actions">
        <el-button type="primary" size="small" @click="emit('build')">立即构建</el-button>
        <el-button size="small" @click="emit('history')">构建历史</el-button>
        <el-button size="small" @click="emit('edit')">编辑</el-button>
        <el-button type="danger" size="small" plain @click="emit('remove')">删除</el-button>
      </div>
    </template>
  </el-card>
</template>

<style scoped>
.pipeline-card__head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;
}
.pipeline-card__name {
  font-size: 16px;
  font-weight: 600;
}
.pipeline-card__meta {
  margin-top: 12px;
}
.pipeline-card__actions {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
}
</style>

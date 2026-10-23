<script setup lang="ts">
import { RouterLink } from 'vue-router'

defineProps<{
  title: string
  description?: string
}>()
</script>

<template>
  <div class="page-header">
    <div>
      <h1 class="page-title">{{ title }}</h1>
      <div v-if="description" class="muted">{{ description }}</div>
    </div>
    <div class="manage-header__actions">
      <slot name="actions" />
      <RouterLink :to="{ name: 'system-config' }">
        <el-button>返回系统管理</el-button>
      </RouterLink>
    </div>
  </div>
</template>

<style scoped>
.manage-header__actions {
  display: flex;
  align-items: center;
  gap: 8px;
}
</style>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { ElMessage } from 'element-plus'
import ManagePageHeader from '@/components/ManagePageHeader.vue'
import { systemApi } from '@/api/system'
import { describeError } from '@/api/request'
import type { SystemInfo } from '@/types/system'

const loading = ref(false)
const info = ref<SystemInfo | null>(null)

const uptimeText = computed(() => {
  const total = info.value?.uptime_seconds ?? 0
  const days = Math.floor(total / 86400)
  const hours = Math.floor((total % 86400) / 3600)
  const minutes = Math.floor((total % 3600) / 60)
  const seconds = total % 60
  const parts: string[] = []
  if (days) parts.push(`${days} 天`)
  if (hours) parts.push(`${hours} 小时`)
  if (minutes) parts.push(`${minutes} 分`)
  parts.push(`${seconds} 秒`)
  return parts.join(' ')
})

onMounted(async () => {
  loading.value = true
  try {
    info.value = await systemApi.info()
  } catch (e) {
    ElMessage.error(describeError(e))
  } finally {
    loading.value = false
  }
})
</script>

<template>
  <div class="page">
    <ManagePageHeader title="系统信息" description="显示系统环境信息以帮助解决问题" />

    <el-card v-loading="loading" shadow="never" class="info-card">
      <el-descriptions v-if="info" :column="2" border>
        <el-descriptions-item label="应用名称">{{ info.app_name }}</el-descriptions-item>
        <el-descriptions-item label="已运行">{{ uptimeText }}</el-descriptions-item>
        <el-descriptions-item label="版本">{{ info.version }}</el-descriptions-item>
        <el-descriptions-item label="Commit">
          <span class="mono">{{ info.commit }}</span>
        </el-descriptions-item>
        <el-descriptions-item label="构建时间">{{ info.build_date }}</el-descriptions-item>
        <el-descriptions-item label="服务器时间">{{ info.server_time }}</el-descriptions-item>
        <el-descriptions-item label="Go 版本">{{ info.go_version }}</el-descriptions-item>
        <el-descriptions-item label="操作系统">{{ info.os }} / {{ info.arch }}</el-descriptions-item>
        <el-descriptions-item label="CPU 核心">{{ info.cpus }}</el-descriptions-item>
        <el-descriptions-item label="Goroutine">{{ info.goroutines }}</el-descriptions-item>
        <el-descriptions-item label="内存（已分配）">{{ info.memory_alloc_mb.toFixed(2) }} MB</el-descriptions-item>
        <el-descriptions-item label="内存（系统）">{{ info.memory_sys_mb.toFixed(2) }} MB</el-descriptions-item>
        <el-descriptions-item label="数据目录">
          <span class="mono">{{ info.data_home }}</span>
        </el-descriptions-item>
        <el-descriptions-item label="任务目录">
          <span class="mono">{{ info.jobs_dir }}</span>
        </el-descriptions-item>
        <el-descriptions-item label="工作区目录">
          <span class="mono">{{ info.workspace_dir }}</span>
        </el-descriptions-item>
        <el-descriptions-item label="执行器 / 队列">
          {{ info.workers }} / {{ info.queue_length }}（{{ info.queue_capacity }}）
        </el-descriptions-item>
        <el-descriptions-item label="任务 / 构建">{{ info.pipeline_count }} / {{ info.build_count }}</el-descriptions-item>
        <el-descriptions-item label="运行中构建">{{ info.running_builds }}</el-descriptions-item>
        <el-descriptions-item label="登录校验">{{ info.auth_enabled ? '已开启' : '已关闭' }}</el-descriptions-item>
        <el-descriptions-item label="静默模式">{{ info.quiet_mode ? '已开启' : '未开启' }}</el-descriptions-item>
      </el-descriptions>
    </el-card>
  </div>
</template>

<style scoped>
.info-card {
  max-width: 1080px;
}
</style>

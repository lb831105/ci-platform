<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { ElMessage } from 'element-plus'
import ManagePageHeader from '@/components/ManagePageHeader.vue'
import { systemApi } from '@/api/system'
import { describeError } from '@/api/request'
import { formatBytes, formatDurationCN } from '@/utils/format'
import type { LoadStats } from '@/types/system'

const loading = ref(false)
const stats = ref<LoadStats | null>(null)

const queuePercent = computed(() => {
  if (!stats.value || stats.value.queue_capacity === 0) {
    return 0
  }
  return Math.round((stats.value.queue_length / stats.value.queue_capacity) * 100)
})

onMounted(async () => {
  loading.value = true
  try {
    stats.value = await systemApi.load()
  } catch (e) {
    ElMessage.error(describeError(e))
  } finally {
    loading.value = false
  }
})
</script>

<template>
  <div class="page">
    <ManagePageHeader title="负载统计" description="检查资源利用情况，评估是否需要更多执行器" />

    <div v-loading="loading" class="load-grid">
      <div class="stat-card">
        <div class="stat-card__label">任务总数</div>
        <div class="stat-card__value">{{ stats?.pipeline_count ?? '-' }}</div>
      </div>
      <div class="stat-card">
        <div class="stat-card__label">构建总数</div>
        <div class="stat-card__value">{{ stats?.build_count ?? '-' }}</div>
      </div>
      <div class="stat-card">
        <div class="stat-card__label">成功 / 失败</div>
        <div class="stat-card__value">
          <span class="is-ok">{{ stats?.success_builds ?? 0 }}</span>
          /
          <span class="is-bad">{{ stats?.failed_builds ?? 0 }}</span>
        </div>
      </div>
      <div class="stat-card">
        <div class="stat-card__label">运行中 / 等待中</div>
        <div class="stat-card__value">{{ stats?.running_builds ?? 0 }} / {{ stats?.pending_builds ?? 0 }}</div>
      </div>
      <div class="stat-card">
        <div class="stat-card__label">平均构建耗时</div>
        <div class="stat-card__value">{{ formatDurationCN(stats?.average_duration_ms, '暂无') }}</div>
      </div>
      <div class="stat-card">
        <div class="stat-card__label">工作区占用</div>
        <div class="stat-card__value">{{ formatBytes(stats?.workspace_bytes ?? 0) }}</div>
        <div class="stat-card__sub">{{ stats?.workspace_count ?? 0 }} 个目录</div>
      </div>
      <div class="stat-card">
        <div class="stat-card__label">任务目录占用</div>
        <div class="stat-card__value">{{ formatBytes(stats?.jobs_bytes ?? 0) }}</div>
      </div>
    </div>

    <el-card shadow="never" class="load-card">
      <template #header>构建队列</template>
      <div class="queue">
        <el-progress
          :percentage="queuePercent"
          :stroke-width="18"
          :status="queuePercent > 80 ? 'exception' : undefined"
        />
        <div class="queue__text">
          排队中 {{ stats?.queue_length ?? 0 }} / 容量 {{ stats?.queue_capacity ?? '-' }}
        </div>
      </div>
    </el-card>
  </div>
</template>

<style scoped>
.load-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(220px, 1fr));
  gap: 14px;
  margin-bottom: 18px;
}
.stat-card {
  background: #fff;
  border: 1px solid #e4e7ed;
  border-radius: 8px;
  padding: 16px 18px;
}
.stat-card__label {
  font-size: 12px;
  color: #909399;
  margin-bottom: 8px;
}
.stat-card__value {
  font-size: 22px;
  font-weight: 600;
  color: #303133;
}
.stat-card__sub {
  font-size: 12px;
  color: #909399;
  margin-top: 4px;
}
.is-ok {
  color: #67c23a;
}
.is-bad {
  color: #f56c6c;
}
.load-card {
  max-width: 760px;
}
.queue__text {
  margin-top: 8px;
  font-size: 13px;
  color: #606266;
}
</style>

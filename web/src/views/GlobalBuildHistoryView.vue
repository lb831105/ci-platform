<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import BuildStatusTag from '@/components/BuildStatusTag.vue'
import { pipelineApi } from '@/api/pipeline'
import { describeError } from '@/api/request'
import { formatDuration, formatTime } from '@/utils/format'
import type { BuildListItem } from '@/types/summary'

const router = useRouter()
const loading = ref(false)
const error = ref('')
const items = ref<BuildListItem[]>([])

async function load(): Promise<void> {
  loading.value = true
  try {
    items.value = await pipelineApi.allBuilds()
    error.value = ''
  } catch (e) {
    error.value = describeError(e)
  } finally {
    loading.value = false
  }
}

onMounted(load)

function goLog(row: BuildListItem): void {
  void router.push({
    name: 'build-log',
    params: { pipelineId: row.pipeline_id, buildId: row.id },
  })
}
</script>

<template>
  <div class="page">
    <div class="page-header">
      <div>
        <h1 class="page-title">构建历史</h1>
        <div class="muted">全部任务的构建记录，按开始时间倒序</div>
      </div>
      <el-button :loading="loading" @click="load">刷新</el-button>
    </div>

    <el-alert v-if="error" type="error" :closable="false" show-icon :title="error" style="margin-bottom: 16px" />

    <el-card shadow="never">
      <el-table v-loading="loading" :data="items" style="width: 100%" @row-click="goLog">
        <el-table-column label="任务" min-width="200">
          <template #default="{ row }">{{ row.pipeline_name }}</template>
        </el-table-column>
        <el-table-column label="编号" width="90">
          <template #default="{ row }">
            <span class="mono">#{{ row.id }}</span>
          </template>
        </el-table-column>
        <el-table-column label="状态" width="110">
          <template #default="{ row }">
            <BuildStatusTag :status="row.status" />
          </template>
        </el-table-column>
        <el-table-column label="触发方式" width="110">
          <template #default="{ row }">
            {{ row.trigger_type === 'manual' ? '手动' : 'Webhook' }}
          </template>
        </el-table-column>
        <el-table-column label="耗时" width="140">
          <template #default="{ row }">{{ formatDuration(row.started_at, row.finished_at) }}</template>
        </el-table-column>
        <el-table-column label="开始时间" min-width="180">
          <template #default="{ row }">{{ formatTime(row.started_at) }}</template>
        </el-table-column>
        <el-table-column label="操作" width="130">
          <template #default="{ row }">
            <el-button size="small" text type="primary" @click.stop="goLog(row)">控制台输出</el-button>
          </template>
        </el-table-column>
        <template #empty>
          <el-empty description="还没有构建记录" />
        </template>
      </el-table>
    </el-card>
  </div>
</template>

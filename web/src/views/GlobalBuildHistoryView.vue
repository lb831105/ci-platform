<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import BuildStatusTag from '@/components/BuildStatusTag.vue'
import { pipelineApi } from '@/api/pipeline'
import { buildApi } from '@/api/build'
import { useAuthStore } from '@/stores/authStore'
import { describeError } from '@/api/request'
import { formatDuration, formatTime } from '@/utils/format'
import type { BuildListItem } from '@/types/summary'

const router = useRouter()
const auth = useAuthStore()
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

// 删除构建（含该构建目录：日志与产物）。跨任务列表也支持，便于就地清理。
const deletingId = ref('')
async function onDelete(row: BuildListItem): Promise<void> {
  try {
    await ElMessageBox.confirm(
      `删除 ${row.pipeline_name ?? row.pipeline_id} 的构建 #${row.id}，会同时删除其日志与已归档产物，且不可恢复。确认删除？`,
      '删除构建',
      { type: 'warning', confirmButtonText: '确认删除', cancelButtonText: '取消' },
    )
  } catch {
    return
  }
  deletingId.value = row.id + row.pipeline_id
  try {
    await buildApi.remove(row.pipeline_id, row.id)
    ElMessage.success(`构建 #${row.id} 已删除`)
    await load()
  } catch (e) {
    ElMessage.error(describeError(e))
  } finally {
    deletingId.value = ''
  }
}

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
            {{ row.trigger_type === 'manual' ? '手动' : row.trigger_type === 'timer' ? '定时' : 'Webhook' }}
          </template>
        </el-table-column>
        <el-table-column label="耗时" width="140">
          <template #default="{ row }">{{ formatDuration(row.started_at, row.finished_at) }}</template>
        </el-table-column>
        <el-table-column label="开始时间" min-width="180">
          <template #default="{ row }">{{ formatTime(row.started_at) }}</template>
        </el-table-column>
        <el-table-column label="操作" width="200">
          <template #default="{ row }">
            <el-button size="small" text type="primary" @click.stop="goLog(row)">控制台输出</el-button>
            <el-button
              v-if="row.status !== 'running' && row.status !== 'pending' && auth.hasPerm('job.delete')"
              size="small"
              text
              type="danger"
              :loading="deletingId === row.id + row.pipeline_id"
              @click.stop="onDelete(row)"
            >删除</el-button>
          </template>
        </el-table-column>
        <template #empty>
          <el-empty description="还没有构建记录" />
        </template>
      </el-table>
    </el-card>
  </div>
</template>

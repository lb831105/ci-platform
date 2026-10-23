<script setup lang="ts">
import { computed, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import JobStatusIcon from '@/components/JobStatusIcon.vue'
import JobWeatherIcon from '@/components/JobWeatherIcon.vue'
import { describeError } from '@/api/request'
import { useOverviewStore } from '@/stores/overviewStore'
import { usePolling } from '@/composables/usePolling'
import { usePipeline } from '@/composables/usePipeline'
import { formatDurationCN, formatRelativeTime } from '@/utils/format'
import type { BuildRef, PipelineSummary } from '@/types/summary'

const router = useRouter()
const overview = useOverviewStore()
const { remove, triggerBuild } = usePipeline()

const jobs = computed(() => overview.items)

// 有构建在跑时轮询刷新，全部结束后停止。
const polling = usePolling(async () => {
  await overview.fetch(true)
  if (!jobs.value.some((job) => job.running)) {
    polling.stop()
  }
}, 3000)

onMounted(async () => {
  await overview.fetch(true)
  if (jobs.value.some((job) => job.running)) {
    polling.start()
  }
})

function goCreate(): void {
  void router.push({ name: 'pipeline-create' })
}

function goHistory(job: PipelineSummary): void {
  void router.push({ name: 'build-history', params: { pipelineId: job.id } })
}

function goEdit(job: PipelineSummary): void {
  void router.push({ name: 'pipeline-edit', params: { pipelineId: job.id } })
}

function goLog(job: PipelineSummary, ref: BuildRef | null): void {
  if (!ref) {
    return
  }
  void router.push({ name: 'build-log', params: { pipelineId: job.id, buildId: ref.id } })
}

async function onBuild(job: PipelineSummary): Promise<void> {
  try {
    const build = await triggerBuild(job.id)
    ElMessage.success(`已触发构建 #${build.id}`)
    await overview.fetch(true)
    polling.start()
  } catch (e) {
    ElMessage.error(describeError(e))
  }
}

async function onRemove(job: PipelineSummary): Promise<void> {
  try {
    await ElMessageBox.confirm(
      `删除任务「${job.name}」会同时删除其全部构建记录与日志，且不可恢复。确认删除？`,
      '危险操作',
      { type: 'warning', confirmButtonText: '确认删除', cancelButtonText: '取消' },
    )
  } catch {
    return
  }
  try {
    await remove(job.id)
    ElMessage.success('已删除')
    await overview.fetch(true)
  } catch (e) {
    ElMessage.error(describeError(e))
  }
}
</script>

<template>
  <div class="page">
    <div class="page-header">
      <div>
        <h1 class="page-title">所有任务</h1>
        <div class="muted">对应 Jenkins 作业列表：状态 · 健康度 · 上次成功 / 失败 / 耗时</div>
      </div>
      <div>
        <el-button :loading="overview.loading" @click="overview.fetch(true)">刷新</el-button>
        <el-button type="primary" @click="goCreate">新建任务</el-button>
      </div>
    </div>

    <el-alert
      v-if="overview.error"
      type="error"
      :closable="false"
      show-icon
      :title="overview.error"
      style="margin-bottom: 16px"
    />

    <el-card shadow="never" class="jobs-card">
      <el-table
        v-loading="overview.loading"
        :data="jobs"
        style="width: 100%"
        @row-click="goHistory"
      >
        <el-table-column label="S" width="56" align="center">
          <template #default="{ row }">
            <JobStatusIcon :status="row.status" />
          </template>
        </el-table-column>

        <el-table-column label="W" width="56" align="center">
          <template #default="{ row }">
            <JobWeatherIcon :recent="row.recent_statuses" />
          </template>
        </el-table-column>

        <el-table-column label="名称" min-width="220" sortable :sort-by="'name'">
          <template #default="{ row }">
            <div class="job-name">
              <RouterLink
                class="job-name__link"
                :to="{ name: 'build-history', params: { pipelineId: row.id } }"
                @click.stop
              >
                {{ row.name }}
              </RouterLink>
              <span class="muted mono">{{ row.id }}</span>
            </div>
          </template>
        </el-table-column>

        <el-table-column label="上次成功" min-width="170">
          <template #default="{ row }">
            <span v-if="row.last_success" class="job-ref" @click.stop="goLog(row, row.last_success)">
              {{ formatRelativeTime(row.last_success.started_at) }}
              <span class="mono">#{{ row.last_success.id }}</span>
            </span>
            <span v-else class="muted">没有</span>
          </template>
        </el-table-column>

        <el-table-column label="上次失败" min-width="170">
          <template #default="{ row }">
            <span v-if="row.last_failure" class="job-ref" @click.stop="goLog(row, row.last_failure)">
              {{ formatRelativeTime(row.last_failure.started_at) }}
              <span class="mono">#{{ row.last_failure.id }}</span>
            </span>
            <span v-else class="muted">无</span>
          </template>
        </el-table-column>

        <el-table-column label="上次持续时间" min-width="140">
          <template #default="{ row }">
            {{ formatDurationCN(row.last_build?.duration_ms, '无') }}
          </template>
        </el-table-column>

        <el-table-column label="操作" width="220" align="right">
          <template #default="{ row }">
            <el-button size="small" type="primary" @click.stop="onBuild(row)">构建</el-button>
            <el-button size="small" @click.stop="goEdit(row)">配置</el-button>
            <el-button size="small" type="danger" plain @click.stop="onRemove(row)">删除</el-button>
          </template>
        </el-table-column>

        <template #empty>
          <el-empty description="还没有任务，先创建一个吧" />
        </template>
      </el-table>
    </el-card>
  </div>
</template>

<style scoped>
.jobs-card :deep(.el-table__row) {
  cursor: pointer;
}
.job-name {
  display: flex;
  flex-direction: column;
  gap: 2px;
}
.job-name__link {
  color: var(--brand-600);
  text-decoration: none;
  font-weight: 600;
}
.job-name__link:hover {
  text-decoration: underline;
}
.job-ref {
  cursor: pointer;
  color: var(--text-1);
}
.job-ref:hover {
  color: var(--brand-600);
  text-decoration: underline;
}
</style>

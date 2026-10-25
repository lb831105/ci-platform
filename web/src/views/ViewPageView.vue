<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { RouterLink, useRouter } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import JobStatusIcon from '@/components/JobStatusIcon.vue'
import JobWeatherIcon from '@/components/JobWeatherIcon.vue'
import { describeError } from '@/api/request'
import { viewApi } from '@/api/view'
import { useViewStore } from '@/stores/viewStore'
import { useAuthStore } from '@/stores/authStore'
import { usePolling } from '@/composables/usePolling'
import { usePipeline } from '@/composables/usePipeline'
import { formatDurationCN, formatRelativeTime } from '@/utils/format'
import type { View } from '@/types/view'
import type { PipelineSummary } from '@/types/summary'

const props = defineProps<{ viewName: string }>()

const router = useRouter()
const viewStore = useViewStore()
const auth = useAuthStore()
const { triggerBuild } = usePipeline()

const view = ref<View | null>(null)
const jobs = ref<PipelineSummary[]>([])
const loading = ref(false)
const error = ref('')

async function load(): Promise<void> {
  loading.value = true
  try {
    const [v, list] = await Promise.all([
      viewApi.list().then((views) => views.find((item) => item.name === props.viewName) ?? null),
      viewApi.jobs(props.viewName),
    ])
    if (!v) {
      error.value = `视图「${props.viewName}」不存在`
      return
    }
    view.value = v
    jobs.value = list
    error.value = ''
  } catch (e) {
    error.value = describeError(e)
  } finally {
    loading.value = false
  }
}

// 有构建在跑时轮询刷新，全部结束后停止。
const polling = usePolling(async () => {
  await load()
  if (!jobs.value.some((job) => job.running)) {
    polling.stop()
  }
}, 3000)

onMounted(async () => {
  await load()
  if (jobs.value.some((job) => job.running)) {
    polling.start()
  }
})

function hasColumn(id: string): boolean {
  const cols = view.value?.columns
  // 列为空时后端返回全部默认列；这里仅做兜底。
  return !cols || cols.length === 0 || cols.includes(id)
}

function goHistory(job: PipelineSummary): void {
  void router.push({ name: 'build-history', params: { pipelineId: job.id } })
}

function goLog(job: PipelineSummary, ref: { id: string } | null): void {
  if (!ref) {
    return
  }
  void router.push({ name: 'build-log', params: { pipelineId: job.id, buildId: ref.id } })
}

async function onBuild(job: PipelineSummary): Promise<void> {
  // 参数化任务必须由用户填参数：直接一键构建会悄悄用默认值，容易发错版本。
  if ((job.params ?? []).length > 0) {
    ElMessage.info('该任务是参数化构建，请在任务页填写参数后构建')
    void router.push({ name: 'build-history', params: { pipelineId: job.id } })
    return
  }
  try {
    const build = await triggerBuild(job.id)
    ElMessage.success(`已触发构建 #${build.id}`)
    await load()
    polling.start()
  } catch (e) {
    ElMessage.error(describeError(e))
  }
}

async function onDelete(): Promise<void> {
  try {
    await ElMessageBox.confirm(`删除视图「${props.viewName}」不会影响任务本身。确认删除？`, '删除视图', {
      type: 'warning',
      confirmButtonText: '确认删除',
      cancelButtonText: '取消',
    })
  } catch {
    return
  }
  try {
    await viewApi.remove(props.viewName)
    ElMessage.success('视图已删除')
    await viewStore.fetch(true)
    void router.push({ name: 'pipeline-list' })
  } catch (e) {
    ElMessage.error(describeError(e))
  }
}
</script>

<template>
  <div class="page">
    <div class="page-header">
      <div>
        <h1 class="page-title">{{ view?.name || viewName }}</h1>
        <div class="muted">{{ view?.description || (view?.type === 'my' ? '我的视图：自动显示当前用户有权限访问的任务' : '列表视图') }}</div>
      </div>
      <div>
        <el-button :loading="loading" @click="load">刷新</el-button>
        <el-button v-if="auth.hasPerm('view.update')" @click="router.push({ name: 'view-edit', params: { viewName } })">编辑视图</el-button>
        <el-button v-if="auth.hasPerm('view.delete')" type="danger" plain @click="onDelete">删除视图</el-button>
      </div>
    </div>

    <el-alert v-if="error" type="error" :closable="false" show-icon :title="error" style="margin-bottom: 16px" />

    <el-card shadow="never" class="jobs-card">
      <el-table v-loading="loading" :data="jobs" style="width: 100%" @row-click="goHistory">
        <el-table-column v-if="hasColumn('status')" label="S" width="56" align="center">
          <template #default="{ row }">
            <JobStatusIcon :status="row.status" />
          </template>
        </el-table-column>

        <el-table-column v-if="hasColumn('weather')" label="W" width="56" align="center">
          <template #default="{ row }">
            <JobWeatherIcon :recent="row.recent_statuses" />
          </template>
        </el-table-column>

        <el-table-column v-if="hasColumn('name')" label="名称" min-width="220">
          <template #default="{ row }">
            <RouterLink
              class="job-name__link"
              :to="{ name: 'build-history', params: { pipelineId: row.id } }"
              @click.stop
            >
              {{ row.name }}
            </RouterLink>
          </template>
        </el-table-column>

        <el-table-column v-if="hasColumn('lastSuccess')" label="上次成功" min-width="170">
          <template #default="{ row }">
            <span v-if="row.last_success" class="job-ref" @click.stop="goLog(row, row.last_success)">
              {{ formatRelativeTime(row.last_success.started_at) }}
              <span class="mono">#{{ row.last_success.id }}</span>
            </span>
            <span v-else class="muted">没有</span>
          </template>
        </el-table-column>

        <el-table-column v-if="hasColumn('lastFailure')" label="上次失败" min-width="170">
          <template #default="{ row }">
            <span v-if="row.last_failure" class="job-ref" @click.stop="goLog(row, row.last_failure)">
              {{ formatRelativeTime(row.last_failure.started_at) }}
              <span class="mono">#{{ row.last_failure.id }}</span>
            </span>
            <span v-else class="muted">无</span>
          </template>
        </el-table-column>

        <el-table-column v-if="hasColumn('lastDuration')" label="上次花费时间" min-width="140">
          <template #default="{ row }">
            {{ formatDurationCN(row.last_build?.duration_ms, '无') }}
          </template>
        </el-table-column>

        <el-table-column v-if="hasColumn('buildButton')" label="操作" width="120" align="right">
          <template #default="{ row }">
            <el-button v-if="auth.hasPerm('job.build')" size="small" type="primary" @click.stop="onBuild(row)">构建</el-button>
          </template>
        </el-table-column>

        <template #empty>
          <el-empty description="视图中还没有任务（可在编辑视图中选择）" />
        </template>
      </el-table>
    </el-card>
  </div>
</template>

<style scoped>
.jobs-card :deep(.el-table__row) {
  cursor: pointer;
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
}
.job-ref:hover {
  color: var(--brand-600);
  text-decoration: underline;
}
</style>

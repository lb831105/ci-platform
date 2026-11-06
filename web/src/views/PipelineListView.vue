<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import JobStatusIcon from '@/components/JobStatusIcon.vue'
import JobWeatherIcon from '@/components/JobWeatherIcon.vue'
import { describeError } from '@/api/request'
import { pipelineApi } from '@/api/pipeline'
import { useOverviewStore } from '@/stores/overviewStore'
import { useAuthStore } from '@/stores/authStore'
import { usePolling } from '@/composables/usePolling'
import { usePipeline } from '@/composables/usePipeline'
import { formatDurationCN, formatRelativeTime, formatTime } from '@/utils/format'
import type { BuildRef, DashboardData, DashboardDeployment, DashboardFailure, PipelineSummary } from '@/types/summary'

const router = useRouter()
const overview = useOverviewStore()
const auth = useAuthStore()
const { remove, triggerBuild } = usePipeline()

const jobs = computed(() => overview.items)

// 仪表盘数据与任务列表一起刷新；失败不遮挡列表（列表仍可用）。
const dashboard = ref<DashboardData | null>(null)
const dashboardLoading = ref(false)
async function loadDashboard(): Promise<void> {
  dashboardLoading.value = true
  try {
    dashboard.value = await pipelineApi.dashboard()
  } catch {
    // 仪表盘是增强信息，接口失败时保持上一次数据，不打断任务列表。
  } finally {
    dashboardLoading.value = false
  }
}

// 有构建在跑时只轮询任务列表；仪表盘是聚合扫描，避免每 3 秒重复扫全部构建。
const polling = usePolling(async () => {
  await overview.fetch(true)
  if (!jobs.value.some((job) => job.running)) {
    polling.stop()
  }
}, 3000)

async function refreshAll(): Promise<void> {
  await Promise.all([overview.fetch(true), loadDashboard()])
}

onMounted(async () => {
  await refreshAll()
  if (jobs.value.some((job) => job.running)) {
    polling.start()
  }
})

function goDeployment(row: DashboardDeployment): void {
  void router.push({ name: 'build-log', params: { pipelineId: row.pipeline_id, buildId: row.build_id } })
}

function goFailure(row: DashboardFailure): void {
  if (row.last_build_id) {
    void router.push({ name: 'build-log', params: { pipelineId: row.pipeline_id, buildId: row.last_build_id } })
  } else {
    void router.push({ name: 'build-history', params: { pipelineId: row.pipeline_id } })
  }
}

function percent(value: number): string {
  return `${(value * 100).toFixed(1)}%`
}

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
  // 参数化任务必须由用户填参数：直接一键构建会悄悄用默认值，容易发错版本。
  if ((job.params ?? []).length > 0) {
    ElMessage.info('该任务是参数化构建，请在任务页填写参数后构建')
    void router.push({ name: 'build-history', params: { pipelineId: job.id } })
    return
  }
  try {
    const build = await triggerBuild(job.id)
    ElMessage.success(`已触发构建 #${build.id}`)
    await overview.fetch(true)
    void loadDashboard()
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
        <el-button :loading="overview.loading || dashboardLoading" @click="refreshAll">刷新</el-button>
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

    <div v-loading="dashboardLoading" class="dashboard">
      <div class="dashboard-grid">
        <div class="metric-card">
          <div class="metric-card__label">今日构建</div>
          <div class="metric-card__value">{{ dashboard?.builds_today ?? 0 }}</div>
          <div class="metric-card__sub">
            成功 {{ dashboard?.success_today ?? 0 }} · 失败 {{ dashboard?.failed_today ?? 0 }} · 中断 {{ dashboard?.aborted_today ?? 0 }}
          </div>
        </div>
        <div class="metric-card">
          <div class="metric-card__label">今日成功率</div>
          <div class="metric-card__value" :class="{ 'is-bad': (dashboard?.success_rate ?? 0) < 0.8 }">
            {{ dashboard ? percent(dashboard.success_rate) : '—' }}
          </div>
          <div class="metric-card__sub">按今日已完成构建计算</div>
        </div>
        <div class="metric-card">
          <div class="metric-card__label">平均构建耗时</div>
          <div class="metric-card__value">{{ formatDurationCN(dashboard?.avg_duration_ms, '—') }}</div>
          <div class="metric-card__sub">今日成功 / 失败构建</div>
        </div>
        <div class="metric-card">
          <div class="metric-card__label">队列 / 运行</div>
          <div class="metric-card__value">
            {{ dashboard?.queue_length ?? 0 }}<span class="metric-card__unit">/ {{ dashboard?.queue_capacity ?? 0 }}</span>
          </div>
          <div class="metric-card__sub">
            运行 {{ dashboard?.running_builds ?? 0 }} · 等待 {{ dashboard?.pending_builds ?? 0 }} · Worker {{ dashboard?.workers ?? 0 }}
          </div>
        </div>
        <div class="metric-card">
          <div class="metric-card__label">今日部署</div>
          <div class="metric-card__value" :class="{ 'is-bad': (dashboard?.deployment_failures_today ?? 0) > 0 }">
            {{ dashboard?.deployments_today ?? 0 }}
          </div>
          <div class="metric-card__sub">
            失败 {{ dashboard?.deployment_failures_today ?? 0 }}
            <el-tag v-if="dashboard?.quiet" size="small" type="warning" effect="plain" disable-transitions>静默模式</el-tag>
          </div>
        </div>
      </div>

      <div class="dashboard-panels">
        <el-card shadow="never" class="dashboard-panel">
          <template #header>
            <div class="dashboard-panel__head">
              <span>最近部署</span>
              <span class="muted">环境 · 结果 · 时间</span>
            </div>
          </template>
          <el-table v-if="dashboard?.recent_deployments?.length" :data="dashboard.recent_deployments" size="small">
            <el-table-column label="任务" min-width="150" show-overflow-tooltip>
              <template #default="{ row }">
                <a class="dashboard-link" @click="goDeployment(row)">{{ row.pipeline_name }} #{{ row.build_id }}</a>
              </template>
            </el-table-column>
            <el-table-column label="环境" width="110">
              <template #default="{ row }">{{ row.environment || '—' }}</template>
            </el-table-column>
            <el-table-column label="结果" width="80">
              <template #default="{ row }">
                <el-tag :type="row.status === 'success' ? 'success' : 'danger'" size="small" disable-transitions>
                  {{ row.status === 'success' ? '成功' : '失败' }}
                </el-tag>
              </template>
            </el-table-column>
            <el-table-column label="时间" width="160">
              <template #default="{ row }">{{ formatTime(row.deployed_at) }}</template>
            </el-table-column>
          </el-table>
          <el-empty v-else description="今天还没有部署记录" :image-size="52" />
        </el-card>

        <el-card shadow="never" class="dashboard-panel">
          <template #header>
            <div class="dashboard-panel__head">
              <span>近 7 天失败最多</span>
              <span class="muted">按失败次数排序</span>
            </div>
          </template>
          <el-table v-if="dashboard?.top_failures?.length" :data="dashboard.top_failures" size="small">
            <el-table-column label="任务" min-width="150" show-overflow-tooltip>
              <template #default="{ row }">
                <a class="dashboard-link" @click="goFailure(row)">{{ row.pipeline_name }}</a>
              </template>
            </el-table-column>
            <el-table-column prop="failures" label="失败次数" width="90" />
            <el-table-column label="最近失败" width="150">
              <template #default="{ row }">{{ formatRelativeTime(row.last_failed_at) }}</template>
            </el-table-column>
          </el-table>
          <el-empty v-else description="近 7 天没有失败构建" :image-size="52" />
        </el-card>
      </div>
    </div>

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
            <el-button v-if="auth.canJob('job.build', row.name)" size="small" type="primary" @click.stop="onBuild(row)">构建</el-button>
            <el-button v-if="auth.canJob('job.configure', row.name)" size="small" @click.stop="goEdit(row)">配置</el-button>
            <el-button v-if="auth.canJob('job.delete', row.name)" size="small" type="danger" plain @click.stop="onRemove(row)">删除</el-button>
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
.dashboard {
  margin-bottom: 16px;
}
.dashboard-grid {
  display: grid;
  grid-template-columns: repeat(5, minmax(0, 1fr));
  gap: 12px;
}
.metric-card {
  padding: 14px 16px;
  border: 1px solid var(--border-1, #ebeef5);
  border-radius: 10px;
  background: var(--bg-1, #fff);
}
.metric-card__label {
  color: var(--text-3, #909399);
  font-size: 12px;
}
.metric-card__value {
  margin-top: 8px;
  font-size: 26px;
  font-weight: 700;
  line-height: 1.15;
  color: var(--text-1, #303133);
}
.metric-card__value.is-bad {
  color: var(--danger-600, #d03050);
}
.metric-card__unit {
  font-size: 13px;
  font-weight: 500;
  color: var(--text-3, #909399);
}
.metric-card__sub {
  margin-top: 6px;
  min-height: 18px;
  color: var(--text-3, #909399);
  font-size: 12px;
  line-height: 1.5;
}
.dashboard-panels {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 12px;
  margin-top: 12px;
}
.dashboard-panel__head {
  display: flex;
  align-items: baseline;
  justify-content: space-between;
  gap: 8px;
}
.dashboard-link {
  color: var(--brand-600);
  cursor: pointer;
}
.dashboard-link:hover {
  text-decoration: underline;
}
@media (max-width: 1100px) {
  .dashboard-grid {
    grid-template-columns: repeat(3, minmax(0, 1fr));
  }
}
@media (max-width: 760px) {
  .dashboard-grid,
  .dashboard-panels {
    grid-template-columns: 1fr;
  }
}
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

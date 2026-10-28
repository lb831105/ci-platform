<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { RouterLink, useRouter } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import { pipelineApi } from '@/api/pipeline'
import { buildApi } from '@/api/build'
import { useAuthStore } from '@/stores/authStore'
import { describeError } from '@/api/request'
import { formatRelativeTime, formatTime } from '@/utils/format'
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

// Jenkins Builds 页的状态不是单纯的 success/failed，而是结合「上一次构建」判断：
//   - 成功且上次失败：返回到正常
//   - 失败且连续失败：自从构建 #N 后断开
//   - 连续失败很久：断开很长时间
// 这里在前端按任务的构建序列计算，既不需要改历史数据，也不会因分页改变语义。
type StatusTone = 'stable' | 'normal' | 'failure' | 'long' | 'aborted' | 'active' | 'unknown'

interface JenkinsStatus {
  label: string
  tone: StatusTone
}

// Jenkins 的「broken for a long time」没有唯一阈值；平台取连续 3 次失败或
// 同一失败 streak 超过 7 天，避免把刚失败一次的任务误标成长期断开。
const LONG_BROKEN_MS = 7 * 24 * 60 * 60 * 1000
const LONG_BROKEN_COUNT = 3

function buildKey(build: BuildListItem): string {
  return `${build.pipeline_id}\u0000${build.id}`
}

function startedAtKey(build: BuildListItem): number {
  if (!build.started_at) {
    return 0
  }
  const value = new Date(build.started_at).getTime()
  return Number.isNaN(value) ? 0 : value
}

function previousFinished(builds: BuildListItem[], index: number): BuildListItem | null {
  for (let i = index + 1; i < builds.length; i++) {
    const status = builds[i].status
    if (status === 'success' || status === 'failed' || status === 'aborted') {
      return builds[i]
    }
  }
  return null
}

function statusOf(build: BuildListItem, builds: BuildListItem[], index: number): JenkinsStatus {
  switch (build.status) {
    case 'running':
      return { label: '构建中', tone: 'active' }
    case 'pending':
      return { label: '等待中', tone: 'active' }
    case 'pending_approval':
      return { label: '待审批', tone: 'active' }
    case 'aborted':
      return { label: '中断', tone: 'aborted' }
    case 'success': {
      const prev = previousFinished(builds, index)
      if (prev?.status === 'failed') {
        return { label: '返回到正常', tone: 'normal' }
      }
      return { label: '稳定', tone: 'stable' }
    }
    case 'failed': {
      // 当前失败 streak 的起点（越靠后越旧）。
      let oldestIndex = index
      while (oldestIndex + 1 < builds.length && builds[oldestIndex + 1].status === 'failed') {
        oldestIndex++
      }
      const oldest = builds[oldestIndex]
      const streakCount = oldestIndex - index + 1
      const age = Date.now() - startedAtKey(oldest)
      if (streakCount >= LONG_BROKEN_COUNT || age >= LONG_BROKEN_MS) {
        return { label: '断开很长时间', tone: 'long' }
      }
      if (streakCount > 1) {
        return { label: `自从构建 #${oldest.id} 后断开`, tone: 'failure' }
      }
      return { label: '自从这次构建后断开', tone: 'failure' }
    }
    default:
      return { label: String(build.status), tone: 'unknown' }
  }
}

const statusByBuild = computed<Map<string, JenkinsStatus>>(() => {
  const byPipeline = new Map<string, BuildListItem[]>()
  for (const item of items.value) {
    const list = byPipeline.get(item.pipeline_id)
    if (list) {
      list.push(item)
    } else {
      byPipeline.set(item.pipeline_id, [item])
    }
  }

  const result = new Map<string, JenkinsStatus>()
  for (const builds of byPipeline.values()) {
    // API 已按全局开始时间倒序，但同一任务内仍需显式排序，才发现「上一次构建」。
    builds.sort((a, b) => startedAtKey(b) - startedAtKey(a))
    builds.forEach((build, index) => {
      result.set(buildKey(build), statusOf(build, builds, index))
    })
  }
  return result
})

type BuildRow = BuildListItem & { jenkins: JenkinsStatus; canDelete: boolean }

const rows = computed<BuildRow[]>(() =>
  items.value.map((item) => ({
    ...item,
    jenkins: statusByBuild.value.get(buildKey(item)) ?? { label: item.status, tone: 'unknown' },
    canDelete: canDelete(item),
  })),
)

function goLog(row: BuildListItem): void {
  void router.push({
    name: 'build-log',
    params: { pipelineId: row.pipeline_id, buildId: row.id },
  })
}

function canDelete(row: BuildListItem): boolean {
  if (row.status === 'running' || row.status === 'pending' || row.status === 'pending_approval') {
    return false
  }
  return auth.canJob('job.delete', row.pipeline_name ?? '')
}

// 删除构建（含该构建目录：日志与产物）。跨任务列表也支持，便于就地清理。
const deletingId = ref('')
async function onDelete(row: BuildRow): Promise<void> {
  try {
    await ElMessageBox.confirm(
      `删除 ${row.pipeline_name ?? row.pipeline_id} 的构建 #${row.id}，会同时删除其日志与已归档产物，且不可恢复。确认删除？`,
      '删除构建',
      { type: 'warning', confirmButtonText: '确认删除', cancelButtonText: '取消' },
    )
  } catch {
    return
  }
  deletingId.value = buildKey(row)
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

</script>

<template>
  <div class="page builds">
    <div class="page-header">
      <div>
        <h1 class="page-title">构建</h1>
        <div class="muted">全部任务的构建记录，按 Jenkins Builds 视图展示</div>
      </div>
      <el-button :loading="loading" @click="load">刷新</el-button>
    </div>

    <el-alert v-if="error" type="error" :closable="false" show-icon :title="error" style="margin-bottom: 16px" />

    <el-card shadow="never">
      <el-table
        v-loading="loading"
        :data="rows"
        style="width: 100%"
        row-class-name="build-row"
        :default-sort="{ prop: 'started_at', order: 'descending' }"
        @row-click="goLog"
      >
        <el-table-column label="S" width="44" align="center">
          <template #default="{ row }">
            <span
              class="status-dot"
              :class="`status-dot--${row.jenkins.tone}`"
              :title="row.jenkins.label"
              :aria-label="row.jenkins.label"
            />
          </template>
        </el-table-column>

        <el-table-column label="构建" min-width="360">
          <template #default="{ row }">
            <div class="build-cell">
              <RouterLink
                class="build-link"
                :to="{ name: 'build-log', params: { pipelineId: row.pipeline_id, buildId: row.id } }"
                @click.stop
              >
                {{ row.pipeline_name }}<span class="mono build-number">#{{ row.id }}</span>
              </RouterLink>
              <button class="console-link" type="button" @click.stop="goLog(row)">命令行输出</button>
            </div>
          </template>
        </el-table-column>

        <el-table-column prop="started_at" label="日期" width="150" sortable>
          <template #default="{ row }">
            <span :title="formatTime(row.started_at)">{{ formatRelativeTime(row.started_at) }}</span>
          </template>
        </el-table-column>

        <el-table-column label="状态" width="260">
          <template #default="{ row }">
            <span class="build-status" :class="`build-status--${row.jenkins.tone}`">
              {{ row.jenkins.label }}
            </span>
          </template>
        </el-table-column>

        <el-table-column label="操作" width="80" align="right">
          <template #default="{ row }">
            <el-button
              v-if="row.canDelete"
              size="small"
              text
              type="danger"
              :loading="deletingId === buildKey(row)"
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

<style scoped>
.build-cell {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 2px;
  line-height: 1.45;
}
.build-link {
  color: var(--brand-600, #1677ff);
  text-decoration: none;
  font-weight: 500;
}
.build-link:hover {
  text-decoration: underline;
}
.build-number {
  margin-left: 2px;
  font-weight: 600;
}
.console-link {
  padding: 0;
  border: 0;
  background: transparent;
  color: var(--text-3, #909399);
  font-size: 12.5px;
  line-height: 1.4;
  cursor: pointer;
}
.console-link:hover {
  color: var(--brand-600, #1677ff);
  text-decoration: underline;
}
.status-dot {
  display: inline-block;
  width: 14px;
  height: 14px;
  border: 1px solid rgba(0, 0, 0, 0.12);
  border-radius: 50%;
  background: #c0c4cc;
  vertical-align: middle;
}
.status-dot--stable {
  background: #4caf50;
}
.status-dot--normal {
  background: #2196f3;
}
.status-dot--failure {
  background: #f44336;
}
.status-dot--long {
  background: #b71c1c;
}
.status-dot--aborted {
  background: #9e9e9e;
}
.status-dot--active {
  background: #ffb300;
}
.status-dot--unknown {
  background: #c0c4cc;
}
.build-status {
  font-size: 13px;
}
.build-status--stable {
  color: #2e7d32;
}
.build-status--normal {
  color: #1565c0;
}
.build-status--failure {
  color: #c62828;
}
.build-status--long {
  color: #b71c1c;
  font-weight: 600;
}
.build-status--aborted {
  color: #757575;
}
.build-status--active {
  color: #b26a00;
}
.build-status--unknown {
  color: #909399;
}
:deep(.build-row) {
  cursor: pointer;
}
</style>

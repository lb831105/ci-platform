<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { RouterLink, useRouter } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import JobStatusIcon from '@/components/JobStatusIcon.vue'
import { buildApi } from '@/api/build'
import { pipelineApi } from '@/api/pipeline'
import { describeError } from '@/api/request'
import { usePipeline } from '@/composables/usePipeline'
import { downloadText } from '@/utils/download'
import { usePolling } from '@/composables/usePolling'
import { useBuildStore } from '@/stores/buildStore'
import { useOverviewStore } from '@/stores/overviewStore'
import { useAuthStore } from '@/stores/authStore'
import type { Build } from '@/types/build'
import type { Pipeline, PipelinePayload } from '@/types/pipeline'
import { formatDuration, formatRelativeTime, formatTime } from '@/utils/format'

const props = defineProps<{ pipelineId: string }>()

const router = useRouter()
const { fetchOne, save, remove, triggerBuild } = usePipeline()
const auth = useAuthStore()
const buildStore = useBuildStore()
const overview = useOverviewStore()

const loading = ref(false)
const error = ref('')
const job = ref<Pipeline | null>(null)
// 任务级权限判断：普通用户可能只被授予某几个任务的角色，
// 因此这里的按钮一律用 canJob(perm, 任务名) 而不是全局 hasPerm。
const jobName = computed(() => job.value?.name ?? '')
function can(perm: string): boolean {
  return jobName.value !== '' && auth.canJob(perm, jobName.value)
}
const triggering = ref(false)
const liveBuildId = ref('')

const history = computed(() => buildStore.getHistory(props.pipelineId))

function hasActive(list: Build[]): boolean {
  return list.some((item) => item.status === 'pending' || item.status === 'running')
}

// 存在未结束的构建时持续刷新历史，全部结束即停止。
const polling = usePolling(async () => {
  const list = await loadHistory(false)
  if (!hasActive(list)) {
    polling.stop()
  }
}, 2000)

async function loadHistory(showLoading = true): Promise<Build[]> {
  if (showLoading) {
    loading.value = true
  }
  try {
    const list = await buildApi.list(props.pipelineId)
    buildStore.setHistory(props.pipelineId, list)
    error.value = ''
    return list
  } catch (e) {
    error.value = describeError(e)
    return []
  } finally {
    if (showLoading) {
      loading.value = false
    }
  }
}

onMounted(async () => {
  job.value = await fetchOne(props.pipelineId)
  const list = await loadHistory()
  if (hasActive(list)) {
    polling.start()
  }
})

// ---- 构建列表按日期分组（对齐 Jenkins 的 Builds 时间线） ----

interface BuildGroup {
  date: string
  builds: Build[]
}

function pad(n: number): string {
  return String(n).padStart(2, '0')
}

function dateLabel(value: string | null): string {
  if (!value) {
    return '未知时间'
  }
  const d = new Date(value)
  if (Number.isNaN(d.getTime())) {
    return '未知时间'
  }
  return `${d.getFullYear()}年${d.getMonth() + 1}月${d.getDate()}日`
}

function clockLabel(value: string | null): string {
  if (!value) {
    return ''
  }
  const d = new Date(value)
  if (Number.isNaN(d.getTime())) {
    return ''
  }
  return `${pad(d.getHours())}:${pad(d.getMinutes())}`
}

const groupedBuilds = computed<BuildGroup[]>(() => {
  const groups: BuildGroup[] = []
  const map = new Map<string, BuildGroup>()
  for (const build of history.value) {
    const key = dateLabel(build.started_at)
    let group = map.get(key)
    if (!group) {
      group = { date: key, builds: [] }
      map.set(key, group)
      groups.push(group)
    }
    group.builds.push(build)
  }
  return groups
})

function causeText(build: Build): string {
  if (build.trigger_type === 'webhook') {
    return build.triggered_by && build.triggered_by !== 'webhook'
      ? `Started by ${build.triggered_by}`
      : 'Started by webhook'
  }
  if (build.trigger_type === 'timer') {
    return '定时触发'
  }
  return `Started by user ${build.triggered_by || 'anonymous'}`
}

// ---- 相关链接（最近一次 / 稳定 / 成功 / 失败 / 未成功 / 完成） ----

function firstBy(predicate: (b: Build) => boolean): Build | null {
  return history.value.find(predicate) ?? null
}

const relatedLinks = computed(() => {
  const last = history.value[0] ?? null
  const lastSuccess = firstBy((b) => b.status === 'success')
  const lastFailed = firstBy((b) => b.status === 'failed')
  const lastCompleted = firstBy((b) => b.status === 'success' || b.status === 'failed')
  return [
    { label: '最近一次构建', build: last },
    { label: '最近稳定构建', build: lastSuccess },
    { label: '最近成功的构建', build: lastSuccess },
    { label: '最近失败的构建', build: lastFailed },
    { label: '最近未成功的构建', build: lastFailed },
    { label: '最近完成的构建', build: lastCompleted },
  ]
})

// ---- 交互 ----

function goLog(build: Build): void {
  void router.push({ name: 'build-log', params: { pipelineId: props.pipelineId, buildId: build.id } })
}

function goEdit(): void {
  void router.push({ name: 'pipeline-edit', params: { pipelineId: props.pipelineId } })
}

// 导出配置：与 Jenkins 的 config.xml 对应，但用 JSON。
// 导出文档不含任何密钥（SSH 密码/Webhook secret/群机器人 token），可直接转发或提交到仓库。
const exporting = ref(false)
async function onExport(): Promise<void> {
  exporting.value = true
  try {
    const text = await pipelineApi.exportConfig(props.pipelineId)
    const base = job.value?.name || props.pipelineId
    downloadText(`${base}-config.json`, text)
    ElMessage.success('配置已导出（不含密钥）')
  } catch (e) {
    ElMessage.error(describeError(e))
  } finally {
    exporting.value = false
  }
}

function goList(): void {
  void router.push({ name: 'pipeline-list' })
}

// 手动取消运行中/等待中的构建。
const cancellingId = ref('')
async function onCancel(build: Build): Promise<void> {
  cancellingId.value = build.id
  try {
    await buildApi.cancel(props.pipelineId, build.id)
    ElMessage.success(`已发送取消请求（#${build.id}）`)
    await loadHistory(false)
  } catch (e) {
    ElMessage.error(describeError(e))
  } finally {
    cancellingId.value = ''
  }
}

// 删除单次构建（含该构建目录：日志与已归档产物）。
const deletingId = ref('')
async function onDeleteBuild(build: Build): Promise<void> {
  try {
    await ElMessageBox.confirm(
      `删除构建 #${build.id} 会同时删除 ${`data/jobs/${props.pipelineId}/builds/${build.id}`} ` +
        '目录（日志与已归档产物），且不可恢复。确认删除？',
      '删除构建',
      { type: 'warning', confirmButtonText: '确认删除', cancelButtonText: '取消' },
    )
  } catch {
    return
  }
  deletingId.value = build.id
  try {
    await buildApi.remove(props.pipelineId, build.id)
    ElMessage.success(`构建 #${build.id} 已删除`)
    if (liveBuildId.value === build.id) {
      liveBuildId.value = ''
    }
    await loadHistory(false)
    await overview.fetch(true)
  } catch (e) {
    ElMessage.error(describeError(e))
  } finally {
    deletingId.value = ''
  }
}

// 参数化构建：有参数定义时先弹表单，无定义时保持「立即构建」的一键体验。
const paramDialog = ref(false)
const paramValues = ref<Record<string, string>>({})
const paramDefs = computed(() => job.value?.params ?? [])
const hasParams = computed(() => paramDefs.value.length > 0)

function onBuild(): void {
  if (!hasParams.value) {
    void startBuild()
    return
  }
  const values: Record<string, string> = {}
  for (const def of paramDefs.value) {
    // bool 用 'true'/'false' 字符串与后端契约保持一致（统一走环境变量）。
    values[def.name] = def.type === 'bool' ? (def.default === 'true' ? 'true' : 'false') : def.default
  }
  paramValues.value = values
  paramDialog.value = true
}

async function submitParams(): Promise<void> {
  for (const def of paramDefs.value) {
    if (def.required && (paramValues.value[def.name] ?? '').trim() === '') {
      ElMessage.warning(`参数「${def.name}」为必填项`)
      return
    }
  }
  const ok = await startBuild({ ...paramValues.value })
  if (ok) {
    paramDialog.value = false
  }
}

async function startBuild(params?: Record<string, string>): Promise<boolean> {
  triggering.value = true
  try {
    const build = await triggerBuild(props.pipelineId, params)
    liveBuildId.value = build.id
    ElMessage.success(`已触发构建 #${build.id}`)
    await Promise.all([loadHistory(false), overview.fetch(true)])
    polling.start()
    goLog(build)
    return true
  } catch (e) {
    ElMessage.error(describeError(e))
    return false
  } finally {
    triggering.value = false
  }
}

async function onRename(): Promise<void> {
  if (!job.value) {
    return
  }
  let value = ''
  try {
    const result = await ElMessageBox.prompt('输入新的任务名称', '重命名', {
      inputValue: job.value.name,
      confirmButtonText: '确定',
      cancelButtonText: '取消',
      inputValidator: (v: string) => (v && v.trim() ? true : '名称不能为空'),
    })
    value = result.value.trim()
  } catch {
    return
  }
  try {
    const updated = await pipelineApi.rename(props.pipelineId, value)
    job.value = updated
    await overview.fetch(true)
    ElMessage.success('已重命名')
  } catch (e) {
    ElMessage.error(describeError(e))
  }
}

async function onEditDescription(): Promise<void> {
  if (!job.value) {
    return
  }
  let value = ''
  try {
    const result = await ElMessageBox.prompt('任务描述', 'Edit description', {
      inputValue: job.value.description,
      inputType: 'textarea',
      confirmButtonText: '保存',
      cancelButtonText: '取消',
    })
    value = result.value
  } catch {
    return
  }
  const p = job.value
  const payload: PipelinePayload = {
    name: p.name,
    description: value,
    image: '',
    steps: p.steps.map((step) => ({ ...step })),
    git_url: p.git_url,
    git_branch: p.git_branch,
    git_credential_id: p.git_credential_id ?? '',
    webhook_secret: p.webhook_secret,
    // 触发器策略与状态回写：编辑/只改描述时都必须原样带回，否则保存会清空配置。
    webhook_branch_mode: p.webhook_branch_mode ?? '',
    webhook_branches: [...(p.webhook_branches ?? [])],
    webhook_events: [...(p.webhook_events ?? [])],
    commit_status: {
      enabled: p.commit_status?.enabled ?? false,
      credential_id: p.commit_status?.credential_id ?? '',
      project: p.commit_status?.project ?? '',
    },
    build_retention_max_per_job: p.build_retention_max_per_job ?? 0,
    build_retention_max_age_days: p.build_retention_max_age_days ?? 0,
    cron_spec: p.cron_spec ?? '',
    notify: p.notify ?? null,
    delete_workspace_before_build: p.delete_workspace_before_build ?? false,
    artifacts: {
      enabled: p.artifacts?.enabled ?? false,
      files: [...(p.artifacts?.files ?? [])],
    },
    ssh_publish: p.ssh_publish
      ? { ...p.ssh_publish, password: '', source_files: [...p.ssh_publish.source_files] }
      : null,
    // 只改描述也要带回参数定义，否则后端会把参数化构建「清空」。
    params: (p.params ?? []).map((def) => ({ ...def, options: [...(def.options ?? [])] })),
    // 只改描述也要带回发布配置，否则后端会把多目标当成「全部删除」。
    ssh_publishes: (p.ssh_publishes ?? []).map((item) => ({
      ...item,
      password: '',
      source_files: [...(item.source_files ?? [])],
    })),
  }
  try {
    job.value = await save(props.pipelineId, payload)
    await overview.fetch(true)
    ElMessage.success('描述已更新')
  } catch (e) {
    ElMessage.error(describeError(e))
  }
}

async function onRemove(): Promise<void> {
  if (!job.value) {
    return
  }
  try {
    await ElMessageBox.confirm(
      `删除任务「${job.value.name}」会同时删除其全部构建记录与日志，且不可恢复。确认删除？`,
      '危险操作',
      { type: 'warning', confirmButtonText: '确认删除', cancelButtonText: '取消' },
    )
  } catch {
    return
  }
  try {
    await remove(props.pipelineId)
    await overview.fetch(true)
    ElMessage.success('已删除')
    goList()
  } catch (e) {
    ElMessage.error(describeError(e))
  }
}

function showWorkspace(): void {
  const dir = job.value?.workspace_dir || props.pipelineId
  void ElMessageBox.alert(
    `工作区目录：data/workspace/${dir}\n` +
      '工作区按任务复用；默认保留构建产物与依赖缓存，仅已删除任务的孤儿目录会按 TTL 清理。',
    '工作空间',
    { confirmButtonText: '知道了' },
  )
}

function showChanges(): void {
  if (!job.value) {
    return
  }
  const lines = [
    `创建时间：${formatTime(job.value.created_at)}`,
    `最近修改：${formatTime(job.value.updated_at)}`,
    `构建次数：${history.value.length}`,
  ]
  if (history.value[0]?.started_at) {
    lines.push(`最近构建：${formatTime(history.value[0].started_at)}`)
  }
  void ElMessageBox.alert(lines.join('\n'), '修改记录', { confirmButtonText: '知道了' })
}
</script>

<template>
  <div class="page job">
    <div class="page-header">
      <div>
        <h1 class="page-title">{{ job?.name || pipelineId }}</h1>
        <div class="muted">
          任务 ID <span class="mono">{{ pipelineId }}</span>
          <span v-if="job?.git_url"> · <span class="mono">{{ job.git_url }}@{{ job.git_branch }}</span></span>
        </div>
      </div>
      <div>
        <el-button @click="goList">返回列表</el-button>
        <el-button v-if="can('job.configure')" @click="goEdit">配置</el-button>
        <el-button v-if="can('job.read')" :loading="exporting" @click="onExport">导出配置</el-button>
        <el-button v-if="can('job.build')" type="primary" :loading="triggering" @click="onBuild">
          {{ hasParams ? '参数化构建' : '立即构建' }}
        </el-button>
      </div>
    </div>

    <el-alert v-if="error" type="error" :closable="false" show-icon :title="error" style="margin-bottom: 16px" />

    <div class="job__body">
      <aside class="job__side">
        <ul class="job__actions">
          <li v-if="auth.hasPerm('system.read')">
            <RouterLink :to="{ name: 'system-config' }">系统管理</RouterLink>
          </li>
          <li><a @click="showChanges">状态</a></li>
          <li><a @click="showChanges">修改记录</a></li>
          <li><a @click="showWorkspace">工作空间</a></li>
          <li v-if="can('job.build')">
            <a @click="onBuild">{{ hasParams ? '参数化构建' : '立即构建' }}</a>
          </li>
          <li v-if="can('job.configure')"><a @click="goEdit">配置</a></li>
          <li v-if="can('job.read')"><a @click="onExport">导出配置</a></li>
          <li v-if="can('job.delete')"><a @click="onRemove">删除工程</a></li>
          <li v-if="can('job.configure')"><a @click="onRename">重命名</a></li>
          <li v-if="auth.hasPerm('credential.view')">
            <RouterLink :to="{ name: 'system-credentials' }">凭据</RouterLink>
          </li>
        </ul>
      </aside>

      <main class="job__main">
        <el-card shadow="never" class="job__card">
          <div class="job__desc-head">
            <span class="job__desc-title">任务描述</span>
            <el-button v-if="can('job.configure')" text type="primary" @click="onEditDescription">编辑描述</el-button>
          </div>
          <div v-if="job?.description" class="job__desc">{{ job.description }}</div>
          <div v-else class="muted">暂无描述</div>
        </el-card>

        <el-card shadow="never" class="job__card" v-loading="loading">
          <template #header>
            <div class="job__card-head">
              <span>Builds</span>
              <span class="muted">共 {{ history.length }} 次</span>
            </div>
          </template>

          <div v-if="history.length === 0" class="muted">还没有构建记录。</div>
          <template v-else>
            <template v-for="group in groupedBuilds" :key="group.date">
              <div class="build-date">{{ group.date }}</div>
              <div
                v-for="build in group.builds"
                :key="build.id"
                class="build-row"
                :class="{ 'build-row--live': build.id === liveBuildId }"
                @click="goLog(build)"
              >
                <JobStatusIcon :status="build.status" />
                <span class="build-row__num mono">#{{ build.id }}</span>
                <span class="build-row__cause">{{ causeText(build) }}</span>
                <span class="build-row__time mono">{{ clockLabel(build.started_at) }}</span>
                <span
                  v-if="(build.status === 'running' || build.status === 'pending') && can('job.build')"
                  class="build-row__stop"
                  @click.stop="onCancel(build)"
                >{{ cancellingId === build.id ? '停止中…' : '停止' }}</span>
                <span
                  v-if="build.status !== 'running' && build.status !== 'pending' && can('job.delete')"
                  class="build-row__stop"
                  @click.stop="onDeleteBuild(build)"
                >{{ deletingId === build.id ? '删除中…' : '删除' }}</span>
              </div>
            </template>
          </template>
        </el-card>

        <el-card shadow="never" class="job__card">
          <template #header>相关链接</template>
          <ul class="related">
            <li v-for="link in relatedLinks" :key="link.label">
              <template v-if="link.build">
                <a @click="goLog(link.build)">
                  {{ link.label }}(#{{ link.build.id }}), {{ formatRelativeTime(link.build.started_at) }}前
                </a>
                <span class="muted"> · 耗时 {{ formatDuration(link.build.started_at, link.build.finished_at) }}</span>
              </template>
              <template v-else>
                <span>{{ link.label }}</span>
                <span class="muted"> （无）</span>
              </template>
            </li>
          </ul>
        </el-card>
      </main>
    </div>
  </div>

    <el-dialog v-model="paramDialog" title="构建参数" width="560px">
      <el-form label-width="150px" label-position="right">
        <el-form-item v-for="def in paramDefs" :key="def.name" :required="def.required">
          <template #label>
            <span class="mono">{{ def.name }}</span>
            <el-tooltip v-if="def.description" :content="def.description" placement="top">
              <span class="param-help">?</span>
            </el-tooltip>
          </template>
          <el-switch
            v-if="def.type === 'bool'"
            :model-value="paramValues[def.name] === 'true'"
            @update:model-value="(value: boolean) => (paramValues[def.name] = value ? 'true' : 'false')"
          />
          <el-select
            v-else-if="def.type === 'choice'"
            v-model="paramValues[def.name]"
            style="width: 100%"
          >
            <el-option v-for="opt in def.options ?? []" :key="opt" :label="opt" :value="opt" />
          </el-select>
          <el-input
            v-else-if="def.type === 'text'"
            v-model="paramValues[def.name]"
            type="textarea"
            :rows="3"
          />
          <el-input v-else v-model="paramValues[def.name]" />
        </el-form-item>
        <el-empty v-if="paramDefs.length === 0" description="该任务没有定义构建参数" :image-size="60" />
      </el-form>
      <template #footer>
        <el-button @click="paramDialog = false">取消</el-button>
        <el-button type="primary" :loading="triggering" @click="submitParams">开始构建</el-button>
      </template>
    </el-dialog>
</template>

<style scoped>
.param-help {
  margin-left: 4px;
  color: var(--text-3, #909399);
  cursor: help;
}

.job__body {
  display: flex;
  gap: 20px;
  align-items: flex-start;
}
.job__side {
  flex: 0 0 180px;
}
.job__actions {
  list-style: none;
  margin: 0;
  padding: 0;
}
.job__actions li {
  margin-bottom: 8px;
}
.job__actions a {
  color: var(--brand-600);
  text-decoration: none;
  font-size: 14px;
  cursor: pointer;
}
.job__actions a:hover {
  text-decoration: underline;
}
.job__main {
  flex: 1;
  min-width: 0;
}
.job__card {
  margin-bottom: 16px;
}
.job__desc-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 8px;
}
.job__desc-title {
  font-weight: 600;
  color: var(--text-1);
}
.job__desc {
  color: var(--text-1);
  font-size: 14px;
  line-height: 1.7;
  white-space: pre-wrap;
}
.job__card-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
}
.build-date {
  font-weight: 600;
  color: var(--text-1);
  margin: 14px 0 6px;
  font-size: 14px;
}
.build-row {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 6px 8px;
  border-radius: 6px;
  cursor: pointer;
}
.build-row:hover {
  background: var(--bg-hover);
}
.build-row--live {
  background: var(--brand-50);
}
.build-row__num {
  color: var(--brand-600);
  font-weight: 600;
  min-width: 54px;
}
.build-row__cause {
  flex: 1;
  color: var(--text-2);
  font-size: 13px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.build-row__time {
  color: var(--text-3);
  font-size: 13px;
}
.build-row__stop {
  margin-left: auto;
  color: var(--danger, #dc2626);
  font-size: 12.5px;
  cursor: pointer;
  flex-shrink: 0;
}
.build-row__stop:hover {
  text-decoration: underline;
}
.related {
  list-style: none;
  margin: 0;
  padding: 0;
}
.related li {
  padding: 5px 0;
  font-size: 14px;
}
.related a {
  color: var(--brand-600);
  text-decoration: none;
  cursor: pointer;
}
.related a:hover {
  text-decoration: underline;
}
</style>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { RouterLink, useRouter } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import BuildStatusTag from '@/components/BuildStatusTag.vue'
import LogTerminal from '@/components/LogTerminal.vue'
import { buildApi } from '@/api/build'
import { pipelineApi } from '@/api/pipeline'
import { describeError } from '@/api/request'
import { useBuildLog } from '@/composables/useBuildLog'
import { usePolling } from '@/composables/usePolling'
import { useAuthStore } from '@/stores/authStore'
import { useSettingsStore } from '@/stores/settingsStore'
import type { ArtifactFile, Build } from '@/types/build'
import type { Pipeline } from '@/types/pipeline'
import { formatBytes, formatDurationCN, formatRelativeTime, formatTime } from '@/utils/format'

const props = defineProps<{ pipelineId: string; buildId: string }>()

const router = useRouter()
const log = useBuildLog(props.pipelineId, props.buildId)
const settingsStore = useSettingsStore()
const authStore = useAuthStore()
// 构建详情：Jenkins Build 页的摘要、Revision/Changes 与参数都来自同一条记录。
const build = ref<Build | null>(null)
const buildLoading = ref(false)
const buildParams = computed(() => build.value?.params ?? {})
const paramNames = computed(() => Object.keys(buildParams.value).sort())

// 手动取消运行中/等待中的构建（对齐 Jenkins Stop Build）。
const cancelling = ref(false)
async function onCancel(): Promise<void> {
  cancelling.value = true
  try {
    await buildApi.cancel(props.pipelineId, props.buildId)
    ElMessage.success('已发送取消请求')
    await log.refreshStatus()
    await loadBuild()
  } catch (e) {
    ElMessage.error(describeError(e))
  } finally {
    cancelling.value = false
  }
}

// 发布审批：pending_approval 构建需要 job.approve 才能进入执行队列。
const approving = ref(false)
const rejecting = ref(false)

async function onApprove(): Promise<void> {
  approving.value = true
  try {
    const { value } = await ElMessageBox.prompt('审批意见（可选）', '放行发布', {
      confirmButtonText: '放行',
      cancelButtonText: '取消',
      inputType: 'textarea',
      inputPlaceholder: '例如：变更单 CHG-123 已审批',
    }).catch(() => ({ value: null as string | null }))
    if (value === null) {
      return
    }
    await buildApi.approve(props.pipelineId, props.buildId, value)
    ElMessage.success('已放行，构建将进入执行队列')
    await log.refreshStatus()
    await loadBuild()
    statusPolling.start()
  } catch (e) {
    ElMessage.error(describeError(e))
  } finally {
    approving.value = false
  }
}

async function onReject(): Promise<void> {
  let value = ''
  try {
    const result = await ElMessageBox.prompt('拒绝理由（必填）', '拒绝发布', {
      confirmButtonText: '拒绝',
      cancelButtonText: '取消',
      inputType: 'textarea',
      inputValidator: (v: string) => (v.trim() !== '' ? true : '请填写拒绝理由'),
    })
    value = result.value ?? ''
  } catch {
    return
  }
  rejecting.value = true
  try {
    await buildApi.reject(props.pipelineId, props.buildId, value)
    ElMessage.success('已拒绝，构建已取消')
    await log.refreshStatus()
    await loadBuild()
  } catch (e) {
    ElMessage.error(describeError(e))
  } finally {
    rejecting.value = false
  }
}

// 删除本次构建（含 data/jobs/{pid}/builds/{bid} 目录），删除后回到构建历史。
const deleting = ref(false)
async function onDeleteBuild(): Promise<void> {
  try {
    await ElMessageBox.confirm(
      `删除构建 #${props.buildId} 会连同日志与已归档产物一起删除，且不可恢复。确认删除？`,
      '删除构建',
      { type: 'warning', confirmButtonText: '确认删除', cancelButtonText: '取消' },
    )
  } catch {
    return
  }
  deleting.value = true
  try {
    await buildApi.remove(props.pipelineId, props.buildId)
    ElMessage.success(`构建 #${props.buildId} 已删除`)
    goHistory()
  } catch (e) {
    ElMessage.error(describeError(e))
  } finally {
    deleting.value = false
  }
}

// 构建成功后归档的产物（data/jobs/{pid}/builds/{bid}/artifacts/）。
const artifacts = ref<ArtifactFile[]>([])
const artifactTotal = ref(0)
const artifactsLoading = ref(false)
let artifactsLoaded = false

// loadBuild 读取构建记录（参数、SCM、备注、保留标记）；失败不影响日志查看。
async function loadBuild(): Promise<void> {
  buildLoading.value = true
  try {
    build.value = await buildApi.get(props.pipelineId, props.buildId)
  } catch {
    // 日志接口仍可单独工作，摘要区会显示占位。
  } finally {
    buildLoading.value = false
  }
}

async function loadArtifacts(): Promise<void> {
  if (artifactsLoaded || !log.finished.value) {
    return
  }
  artifactsLoading.value = true
  try {
    const manifest = await buildApi.artifacts(props.pipelineId, props.buildId)
    artifacts.value = manifest.files ?? []
    artifactTotal.value =
      manifest.total_size ?? artifacts.value.reduce((sum, file) => sum + file.size, 0)
    artifactsLoaded = true
  } catch {
    // 没有归档产物的普通构建会返回空清单；网络抖动也不应影响日志查看。
  } finally {
    artifactsLoading.value = false
  }
}

// 构建未结束时轮询状态：一旦结束就停掉轮询、关闭 WS 并补齐最终日志。
const statusPolling = usePolling(async () => {
  const latest = await log.refreshStatus()
  if (latest) {
    build.value = latest
  }
  if (log.finished.value) {
    statusPolling.stop()
    log.stop()
    await log.loadInitial()
    await loadBuild()
    await loadArtifacts()
  }
}, 2000)

async function loadPipelineConfig(): Promise<void> {
  try {
    pipelineConfig.value = await pipelineApi.get(props.pipelineId)
  } catch {
    // 没权限/网络失败时按钮保持隐藏；日志仍可查看。
  }
}

onMounted(async () => {
  await log.start()
  void loadPipelineConfig()
  void loadBuild()
  if (log.finished.value) {
    await loadArtifacts()
  } else {
    statusPolling.start()
  }
})

const connectionText = computed(() => {
  if (log.finished.value) {
    return '构建已结束'
  }
  if (log.retrying.value) {
    return '连接断开，正在重连…'
  }
  return log.connected.value ? '实时连接中' : '连接中…'
})

const connectionType = computed(() => {
  if (log.finished.value) {
    return 'info'
  }
  if (log.retrying.value) {
    return 'warning'
  }
  return log.connected.value ? 'success' : 'info'
})

function goHistory(): void {
  void router.push({ name: 'build-history', params: { pipelineId: props.pipelineId } })
}

function goList(): void {
  void router.push({ name: 'pipeline-list' })
}

function saveBlob(blob: Blob, filename: string): void {
  const url = URL.createObjectURL(blob)
  const anchor = document.createElement('a')
  anchor.href = url
  anchor.download = filename
  document.body.appendChild(anchor)
  anchor.click()
  anchor.remove()
  setTimeout(() => URL.revokeObjectURL(url), 1000)
}

async function downloadArtifact(file: ArtifactFile): Promise<void> {
  try {
    const blob = await buildApi.downloadArtifact(props.pipelineId, props.buildId, file.path)
    saveBlob(blob, file.path.split('/').pop() || 'artifact')
  } catch (e) {
    ElMessage.error(describeError(e))
  }
}

async function downloadAll(): Promise<void> {
  try {
    const blob = await buildApi.downloadArtifactsZip(props.pipelineId, props.buildId)
    saveBlob(blob, `build-${props.buildId}-artifacts.zip`)
  } catch (e) {
    ElMessage.error(describeError(e))
  }
}

// ===== 重新发布（把本次构建产物直接发到 SSH，不重新编译）=====
const republishVisible = ref(false)
const republishServerId = ref('')
// 多目标任务：选择要重新发布到哪个目标（名称）。
const republishTarget = ref('')
const republishing = ref(false)
const pipelineConfig = ref<Pipeline | null>(null)
// 任务级权限：普通用户可能只被授权某几个任务，按钮按「当前任务名」判断。
const jobName = computed(() => pipelineConfig.value?.name ?? '')
function can(perm: string): boolean {
  return jobName.value !== '' && authStore.canJob(perm, jobName.value)
}

// ---- Jenkins Build 页摘要：时间、Revision、Repository、refs、Changes ----

function msBetween(start?: string | null, end?: string | null): number | null {
  if (!start || !end) {
    return null
  }
  const from = new Date(start).getTime()
  const to = new Date(end).getTime()
  if (Number.isNaN(from) || Number.isNaN(to) || to < from) {
    return null
  }
  return to - from
}

function formatSpent(ms: number | null): string {
  if (ms === null) {
    return '-'
  }
  return ms <= 0 ? '0 毫秒' : formatDurationCN(ms)
}

const waitingMs = computed(() => msBetween(build.value?.created_at, build.value?.started_at))
// 运行中的构建没有 finished_at，Jenkins 也会显示到当前时刻的累计时间。
const runMs = computed(() => {
  if (build.value?.started_at && !build.value.finished_at && !log.finished.value) {
    return msBetween(build.value.started_at, new Date().toISOString())
  }
  return msBetween(build.value?.started_at, build.value?.finished_at)
})
const totalMs = computed(() => {
  if (build.value?.created_at) {
    const end = build.value.finished_at ?? (log.finished.value ? null : new Date().toISOString())
    return msBetween(build.value.created_at, end)
  }
  return runMs.value
})
const startedAgo = computed(() =>
  build.value?.started_at ? formatRelativeTime(build.value.started_at) : '-',
)
const runDurationText = computed(() => formatSpent(runMs.value))
const waitingText = computed(() => formatSpent(waitingMs.value))
const totalText = computed(() => formatSpent(totalMs.value))
const revision = computed(() => build.value?.commit_full || build.value?.commit || '')
const repository = computed(() => pipelineConfig.value?.git_url ?? '')
const gitRefs = computed(() =>
  build.value?.branch ? `refs/remotes/origin/${build.value.branch}` : '',
)
const changedFiles = computed(() => build.value?.changed_files ?? [])
const stepRows = computed(() => pipelineConfig.value?.steps ?? [])

const descriptionVisible = ref(false)
const descriptionDraft = ref('')
const metaSaving = ref(false)
const keepSaving = ref(false)

function openDescription(): void {
  if (!can('job.configure')) {
    return
  }
  descriptionDraft.value = build.value?.description ?? ''
  descriptionVisible.value = true
}

async function saveDescription(): Promise<void> {
  metaSaving.value = true
  try {
    const updated = await buildApi.updateMetadata(props.pipelineId, props.buildId, {
      description: descriptionDraft.value,
    })
    build.value = updated
    descriptionVisible.value = false
    ElMessage.success('构建描述已保存')
  } catch (e) {
    ElMessage.error(describeError(e))
  } finally {
    metaSaving.value = false
  }
}

async function onKeepForever(value: string | number | boolean): Promise<void> {
  const wanted = Boolean(value)
  const previous = build.value?.keep_forever ?? false
  keepSaving.value = true
  try {
    const updated = await buildApi.updateMetadata(props.pipelineId, props.buildId, {
      keep_forever: wanted,
    })
    build.value = updated
  } catch (e) {
    // 失败时恢复开关，避免界面显示成功但服务端没保存。
    if (build.value) {
      build.value.keep_forever = previous
    }
    ElMessage.error(describeError(e))
  } finally {
    keepSaving.value = false
  }
}

const canRepublish = computed(
  () => log.status.value === 'success' && artifacts.value.length > 0,
)

// publishTargets 返回任务里启用的发布目标（兼容旧版单目标配置）。
const publishTargets = computed(() => {
  const p = pipelineConfig.value
  if (!p) return []
  const list =
    p.ssh_publishes && p.ssh_publishes.length > 0
      ? p.ssh_publishes
      : p.ssh_publish
        ? [p.ssh_publish]
        : []
  return list
    .filter((item) => item.enabled)
    .map((item, index) => ({ name: item.name || `发布 ${index + 1}`, serverId: item.server_id }))
})

// 服务器覆盖只允许任务已配置过的目标：后端同样会拒绝跨任务服务器，
// 前端提前过滤避免用户选中后才发现 403。
const republishServers = computed(() => {
  const allowed = new Set(publishTargets.value.map((t) => t.serverId).filter(Boolean))
  return settingsStore.sshServers.filter((s) => allowed.has(s.id))
})

function syncRepublishServer(): void {
  const target = publishTargets.value.find((t) => t.name === republishTarget.value)
  republishServerId.value = target?.serverId ?? ''
}

async function openRepublish(): Promise<void> {
  republishServerId.value = ''
  republishTarget.value = ''
  republishVisible.value = true
  void settingsStore.fetch()
  if (!pipelineConfig.value) {
    try {
      pipelineConfig.value = await pipelineApi.get(props.pipelineId)
    } catch {
      // 拿不到任务配置时让用户手动选服务器
    }
  }
  const first = publishTargets.value[0]
  if (first) {
    republishTarget.value = first.name
    republishServerId.value = first.serverId ?? ''
  }
}

async function submitRepublish(): Promise<void> {
  if (!republishServerId.value) {
    ElMessage.error('请选择 SSH 服务器')
    return
  }
  republishing.value = true
  try {
    await buildApi.republish(
      props.pipelineId,
      props.buildId,
      republishServerId.value,
      republishTarget.value,
    )
    ElMessage.success('重新发布完成')
    republishVisible.value = false
    // 发布日志追加在本次构建的 log.txt 中，重新拉取一次展示
    await log.loadInitial()
  } catch (e) {
    ElMessage.error(describeError(e))
  } finally {
    republishing.value = false
  }
}
</script>

<template>
  <div class="page">
    <nav class="build-breadcrumb" aria-label="breadcrumb">
      <RouterLink :to="{ name: 'pipeline-list' }">任务</RouterLink>
      <span class="build-breadcrumb__sep">/</span>
      <RouterLink :to="{ name: 'build-history', params: { pipelineId } }">
        {{ jobName || '任务' }}
      </RouterLink>
      <span class="build-breadcrumb__sep">/</span>
      <span class="mono">#{{ buildId }}</span>
    </nav>

    <div class="page-header">
      <div>
        <h1 class="page-title">
          #{{ buildId }} <BuildStatusTag :status="log.status.value" />
          <span v-if="build?.started_at" class="build-title__date">({{ formatTime(build.started_at) }})</span>
        </h1>
        <div class="muted">{{ jobName || '任务' }}</div>
      </div>
      <div class="build-log__actions">
        <el-tag :type="connectionType" effect="plain" disable-transitions>{{ connectionText }}</el-tag>
        <template v-if="log.status.value === 'pending_approval' && can('job.approve')">
          <el-button type="success" :loading="approving" @click="onApprove">放行发布</el-button>
          <el-button type="danger" plain :loading="rejecting" @click="onReject">拒绝</el-button>
        </template>
        <el-button
          v-if="!log.finished.value && can('job.build')"
          type="danger"
          plain
          :loading="cancelling"
          @click="onCancel"
        >停止构建</el-button>
        <el-button
          v-if="log.finished.value && can('job.delete')"
          type="danger"
          plain
          :loading="deleting"
          @click="onDeleteBuild"
        >删除构建</el-button>
        <el-button @click="goHistory">构建历史</el-button>
        <el-button @click="goList">返回列表</el-button>
      </div>
    </div>

    <el-alert
      v-if="log.error.value"
      type="warning"
      :closable="false"
      show-icon
      :title="log.error.value"
      style="margin-bottom: 12px"
    />

    <el-card v-loading="buildLoading" shadow="never" class="build-summary">
      <div class="build-summary__top">
        <div class="build-summary__main">
          <div class="build-summary__line">
            Started <strong>{{ startedAgo }}</strong> ago
            <span class="build-summary__sep">·</span>
            Took <strong>{{ runDurationText }}</strong>
          </div>
          <div class="build-summary__line">
            启动用户 <span class="mono">{{ build?.triggered_by || 'anonymous' }}</span>
          </div>
          <div v-if="build?.description" class="build-summary__description">
            {{ build.description }}
          </div>
        </div>
        <div class="build-summary__side">
          <el-button
            v-if="can('job.configure')"
            link
            type="primary"
            @click="openDescription"
          >{{ build?.description ? '编辑描述' : 'Add description' }}</el-button>
          <el-checkbox
            :model-value="build?.keep_forever ?? false"
            :disabled="!can('job.configure') || keepSaving"
            @change="onKeepForever"
          >永久保留这次编译</el-checkbox>
        </div>
      </div>

      <el-descriptions :column="1" size="small" border class="build-summary__details">
        <el-descriptions-item label="This run spent">
          <div>{{ waitingText }} waiting;</div>
          <div>{{ runDurationText }} build duration;</div>
          <div>{{ totalText }} total from scheduled to completion.</div>
        </el-descriptions-item>
        <el-descriptions-item v-if="revision" label="Revision">
          <span class="mono">{{ revision }}</span>
        </el-descriptions-item>
        <el-descriptions-item v-if="repository" label="Repository">
          <a :href="repository" target="_blank" rel="noreferrer" class="mono">{{ repository }}</a>
        </el-descriptions-item>
        <el-descriptions-item v-if="gitRefs" label="refs">
          <span class="mono">{{ gitRefs }}</span>
        </el-descriptions-item>
      </el-descriptions>
    </el-card>

    <el-card v-if="build?.commit || build?.commit_message" shadow="never" class="build-changes">
      <template #header>
        <span class="build-section__title">变更记录</span>
        <span class="muted">Changes</span>
      </template>
      <div v-if="build?.commit_message" class="build-changes__summary">
        <div class="build-changes__label">Summary</div>
        <pre class="build-changes__message">{{ build.commit_message }}</pre>
      </div>
      <div class="build-changes__meta">
        Commit <span class="mono">{{ build?.commit_full || build?.commit }}</span>
        by {{ build?.commit_author || '未知' }}
      </div>
      <ul v-if="changedFiles.length" class="build-changes__files">
        <li v-for="file in changedFiles" :key="file">
          The file was modified <span class="mono">{{ file }}</span>
        </li>
      </ul>
      <div v-else class="muted build-changes__empty">本次提交没有可读取的文件变更（merge/root 提交或读取失败）。</div>
    </el-card>

    <el-card v-if="stepRows.length" shadow="never" class="build-steps">
      <template #header>
        <span class="build-section__title">构建步骤</span>
        <span class="muted">平台按步骤串行执行（Jenkins Module Builds 的对应视图）</span>
      </template>
      <el-table :data="stepRows" size="small">
        <el-table-column label="步骤" min-width="180">
          <template #default="{ row, $index }">{{ row.name || `步骤 ${$index + 1}` }}</template>
        </el-table-column>
        <el-table-column label="失败处理" width="120">
          <template #default="{ row }">{{ row.allow_failure ? '允许失败' : '阻断后续' }}</template>
        </el-table-column>
        <el-table-column label="超时" width="110">
          <template #default="{ row }">{{ row.timeout_sec > 0 ? `${row.timeout_sec}s` : '不限' }}</template>
        </el-table-column>
        <el-table-column label="重试" width="90">
          <template #default="{ row }">{{ row.retry }}</template>
        </el-table-column>
      </el-table>
      <div class="build-steps__tip">每步的实际耗时与输出见下方控制台日志。</div>
    </el-card>

    <el-card v-if="paramNames.length" shadow="never" class="param-card">
      <template #header>
        <span class="param-card__title">构建参数</span>
      </template>
      <el-descriptions :column="2" size="small" border>
        <el-descriptions-item v-for="name in paramNames" :key="name" :label="name">
          <span class="mono">{{ buildParams[name] }}</span>
        </el-descriptions-item>
      </el-descriptions>
    </el-card>

    <LogTerminal :lines="log.lines.value" />

    <el-card v-if="artifacts.length > 0" v-loading="artifactsLoading" shadow="never" class="artifact-card">
      <template #header>
        <div class="artifact-card__header">
          <div>
            <span class="artifact-card__title">构建产物</span>
            <span class="muted">
              {{ artifacts.length }} 个文件 · {{ formatBytes(artifactTotal) }}
            </span>
          </div>
          <div class="artifact-card__buttons">
            <el-button size="small" @click="downloadAll">下载全部（ZIP）</el-button>
            <el-button v-if="canRepublish && can('job.replay')" size="small" type="primary" plain @click="openRepublish">
              重新发布
            </el-button>
          </div>
        </div>
      </template>
      <div class="artifact-card__path mono">
        data/jobs/{{ pipelineId }}/builds/{{ buildId }}/artifacts/
      </div>
      <el-table :data="artifacts" size="small" class="artifact-card__table">
        <el-table-column prop="path" label="文件" min-width="280" show-overflow-tooltip />
        <el-table-column label="大小" width="120">
          <template #default="{ row }">{{ formatBytes(row.size) }}</template>
        </el-table-column>
        <el-table-column label="操作" width="100">
          <template #default="{ row }">
            <el-button link type="primary" @click="downloadArtifact(row)">下载</el-button>
          </template>
        </el-table-column>
      </el-table>
      <div class="artifact-card__tip">
        需要回滚时，下载对应成功构建的产物覆盖线上版本，或直接「重新发布」到 SSH 服务器（不重新编译）；
        产物随「丢弃旧的构建」策略一起清理。
      </div>
    </el-card>

    <el-dialog v-model="republishVisible" title="重新发布产物" width="480px">
      <el-form label-width="120px">
        <el-form-item v-if="publishTargets.length > 0" label="发布目标">
          <el-select
            v-model="republishTarget"
            placeholder="选择任务里配置的目标"
            style="width: 100%"
            @change="syncRepublishServer"
          >
            <el-option
              v-for="target in publishTargets"
              :key="target.name"
              :label="target.name"
              :value="target.name"
            />
          </el-select>
          <div class="form-tip">
            来自任务的「构建后操作 → SSH 发布」；选目标时服务器会自动带出，也可在下方临时覆盖。
          </div>
        </el-form-item>
        <el-form-item label="SSH 服务器">
          <el-select v-model="republishServerId" placeholder="选择发布目标" style="width: 100%">
            <el-option
              v-for="server in republishServers"
              :key="server.id"
              :label="`${server.name} (${server.host})`"
              :value="server.id"
            />
          </el-select>
          <div class="form-tip">
            只能使用任务已配置的服务器，避免低权限用户把产物发布到任意主机。
          </div>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="republishVisible = false">取消</el-button>
        <el-button type="primary" :loading="republishing" @click="submitRepublish">发布</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="descriptionVisible" title="构建描述" width="560px">
      <el-input
        v-model="descriptionDraft"
        type="textarea"
        :rows="5"
        maxlength="2000"
        show-word-limit
        placeholder="例如：本次发布包含的变更单、回滚说明或验证结果"
      />
      <template #footer>
        <el-button @click="descriptionVisible = false">取消</el-button>
        <el-button type="primary" :loading="metaSaving" @click="saveDescription">保存</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<style scoped>
.param-card {
  margin-bottom: 12px;
}
.param-card__title {
  font-weight: 600;
}

.build-log__actions {
  display: flex;
  align-items: center;
  gap: 8px;
}

.artifact-card {
  margin-top: 16px;
}

.artifact-card__header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}

.artifact-card__buttons {
  display: flex;
  gap: 8px;
}

.artifact-card__title {
  margin-right: 10px;
  font-weight: 600;
}

.artifact-card__path {
  margin-bottom: 10px;
  color: var(--el-text-color-secondary);
  font-size: 12px;
}

.artifact-card__table {
  width: 100%;
}

.artifact-card__tip {
  margin-top: 10px;
  color: var(--el-text-color-secondary);
  font-size: 12px;
}

.build-title__date {
  margin-left: 8px;
  color: var(--el-text-color-secondary);
  font-size: 13px;
  font-weight: 400;
}
.build-breadcrumb {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 12px;
  color: var(--el-text-color-secondary);
  font-size: 13px;
}
.build-breadcrumb a {
  color: var(--brand-600, #1677ff);
  text-decoration: none;
}
.build-breadcrumb a:hover {
  text-decoration: underline;
}
.build-breadcrumb__sep {
  color: var(--el-text-color-placeholder);
}
.build-summary {
  margin-bottom: 12px;
}
.build-summary__top {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 16px;
  margin-bottom: 12px;
}
.build-summary__line {
  margin-bottom: 5px;
  color: var(--el-text-color-regular);
}
.build-summary__sep {
  margin: 0 8px;
  color: var(--el-text-color-placeholder);
}
.build-summary__description {
  margin-top: 8px;
  padding: 8px 10px;
  border-left: 3px solid var(--el-color-primary);
  background: var(--el-fill-color-lighter);
  color: var(--el-text-color-regular);
  white-space: pre-wrap;
  line-height: 1.6;
}
.build-summary__side {
  display: flex;
  flex-direction: column;
  align-items: flex-end;
  gap: 8px;
  flex: none;
}
.build-summary__details {
  margin-top: 4px;
}
.build-changes,
.build-steps {
  margin-bottom: 12px;
}
.build-section__title {
  margin-right: 8px;
  font-weight: 600;
}
.build-changes__label {
  margin-bottom: 4px;
  color: var(--el-text-color-secondary);
  font-size: 12px;
}
.build-changes__message {
  margin: 0;
  white-space: pre-wrap;
  word-break: break-word;
  font-family: inherit;
  font-size: 13px;
  line-height: 1.6;
}
.build-changes__meta {
  margin-top: 10px;
  color: var(--el-text-color-secondary);
  font-size: 12.5px;
}
.build-changes__files {
  max-height: 280px;
  margin: 10px 0 0;
  padding-left: 20px;
  overflow: auto;
  color: var(--el-text-color-regular);
  font-size: 12.5px;
  line-height: 1.7;
}
.build-changes__empty {
  margin-top: 10px;
  font-size: 12.5px;
}
.build-steps__tip {
  margin-top: 8px;
  color: var(--el-text-color-secondary);
  font-size: 12px;
}
</style>

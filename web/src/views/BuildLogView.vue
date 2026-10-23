<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import BuildStatusTag from '@/components/BuildStatusTag.vue'
import LogTerminal from '@/components/LogTerminal.vue'
import { buildApi } from '@/api/build'
import { pipelineApi } from '@/api/pipeline'
import { describeError } from '@/api/request'
import { useBuildLog } from '@/composables/useBuildLog'
import { usePolling } from '@/composables/usePolling'
import { useSettingsStore } from '@/stores/settingsStore'
import type { ArtifactFile } from '@/types/build'
import type { Pipeline } from '@/types/pipeline'
import { formatBytes } from '@/utils/format'

const props = defineProps<{ pipelineId: string; buildId: string }>()

const router = useRouter()
const log = useBuildLog(props.pipelineId, props.buildId)
const settingsStore = useSettingsStore()

// 构建成功后归档的产物（data/jobs/{pid}/builds/{bid}/artifacts/）。
const artifacts = ref<ArtifactFile[]>([])
const artifactTotal = ref(0)
const artifactsLoading = ref(false)
let artifactsLoaded = false

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
  await log.refreshStatus()
  if (log.finished.value) {
    statusPolling.stop()
    log.stop()
    await log.loadInitial()
    await loadArtifacts()
  }
}, 2000)

onMounted(async () => {
  await log.start()
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
const republishing = ref(false)
const pipelineConfig = ref<Pipeline | null>(null)

const canRepublish = computed(
  () => log.status.value === 'success' && artifacts.value.length > 0,
)

async function openRepublish(): Promise<void> {
  republishServerId.value = ''
  republishVisible.value = true
  void settingsStore.fetch()
  if (!pipelineConfig.value) {
    try {
      pipelineConfig.value = await pipelineApi.get(props.pipelineId)
    } catch {
      // 拿不到任务配置时让用户手动选服务器
    }
  }
  republishServerId.value = pipelineConfig.value?.ssh_publish?.server_id ?? ''
}

async function submitRepublish(): Promise<void> {
  if (!republishServerId.value) {
    ElMessage.error('请选择 SSH 服务器')
    return
  }
  republishing.value = true
  try {
    await buildApi.republish(props.pipelineId, props.buildId, republishServerId.value)
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
    <div class="page-header">
      <div>
        <h1 class="page-title">
          控制台输出 <span class="mono">#{{ buildId }}</span>
        </h1>
        <div class="muted mono">data/jobs/{{ pipelineId }}/builds/{{ buildId }}/log.txt</div>
      </div>
      <div class="build-log__actions">
        <BuildStatusTag :status="log.status.value" />
        <el-tag :type="connectionType" effect="plain" disable-transitions>{{ connectionText }}</el-tag>
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
            <el-button v-if="canRepublish" size="small" type="primary" plain @click="openRepublish">
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
        <el-form-item label="SSH 服务器">
          <el-select v-model="republishServerId" placeholder="选择发布目标" style="width: 100%">
            <el-option
              v-for="server in settingsStore.sshServers"
              :key="server.id"
              :label="`${server.name} (${server.host})`"
              :value="server.id"
            />
          </el-select>
          <div class="form-tip">
            默认使用任务配置的服务器；列表来自「系统配置 → SSH 服务器」。
          </div>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="republishVisible = false">取消</el-button>
        <el-button type="primary" :loading="republishing" @click="submitRepublish">发布</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<style scoped>
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
</style>

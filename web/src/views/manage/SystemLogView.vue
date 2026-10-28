<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import ManagePageHeader from '@/components/ManagePageHeader.vue'
import { systemApi } from '@/api/system'
import { describeError } from '@/api/request'
import { usePolling } from '@/composables/usePolling'
import type { LogEntry } from '@/types/system'

const loading = ref(false)
const entries = ref<LogEntry[]>([])
const autoRefresh = ref(false)
// 级别过滤：默认显示 info 以上（debug 只在排障时打开）。
const levels = ref<string[]>(['INFO', 'WARN', 'ERROR'])
const keyword = ref('')
const fileInfo = ref<{ file: string; level: string; retain_days: number } | null>(null)

const LEVEL_OPTIONS = ['DEBUG', 'INFO', 'WARN', 'ERROR']

const polling = usePolling(async () => {
  await load(false)
}, 3000)

// visible 先按级别过滤再按关键字过滤，避免把"过滤"做成两次遍历。
const visible = computed(() => {
  const kw = keyword.value.trim().toLowerCase()
  return entries.value.filter((e) => {
    if (!levels.value.includes(normalizeLevel(e.level))) {
      return false
    }
    return kw === '' || e.message.toLowerCase().includes(kw)
  })
})

function normalizeLevel(level: string): string {
  return (level || 'INFO').trim().toUpperCase()
}

function levelClass(level: string): string {
  switch (normalizeLevel(level)) {
    case 'DEBUG':
      return 'lv-debug'
    case 'WARN':
      return 'lv-warn'
    case 'ERROR':
      return 'lv-error'
    default:
      return 'lv-info'
  }
}

function timeText(at: string): string {
  if (!at) return ''
  const d = new Date(at)
  if (Number.isNaN(d.getTime())) return at
  const pad = (n: number, w = 2): string => String(n).padStart(w, '0')
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}.${pad(d.getMilliseconds(), 3)}`
}

async function load(showLoading = true): Promise<void> {
  if (showLoading) {
    loading.value = true
  }
  try {
    const res = await systemApi.log(1000)
    entries.value = res.entries ?? []
    fileInfo.value = res.file ?? null
  } catch (e) {
    if (showLoading) {
      ElMessage.error(describeError(e))
    }
  } finally {
    if (showLoading) {
      loading.value = false
    }
  }
}

onMounted(() => {
  void load()
})

function toggleAuto(value: boolean): void {
  autoRefresh.value = value
  if (value) {
    polling.start()
  } else {
    polling.stop()
  }
}

async function onClear(): Promise<void> {
  try {
    await ElMessageBox.confirm(
      '确认清空内存中的日志缓冲？（已落盘的 data/logs/YYYY-MM/app-YYYY-MM-DD.log 不受影响）',
      '确认操作',
      { type: 'warning', confirmButtonText: '清空', cancelButtonText: '取消' },
    )
  } catch {
    return
  }
  try {
    await systemApi.clearLog()
    entries.value = []
    ElMessage.success('已清空')
  } catch (e) {
    ElMessage.error(describeError(e))
  }
}
</script>

<template>
  <div class="page">
    <ManagePageHeader
      title="系统日志"
      description="平台运行日志（分级记录，一天一个文件，默认保留 30 天）；页面展示内存缓冲最近 2000 条"
    >
      <template #actions>
        <el-select
          v-model="levels"
          multiple
          collapse-tags
          size="default"
          style="width: 220px"
          placeholder="级别"
        >
          <el-option v-for="lv in LEVEL_OPTIONS" :key="lv" :label="lv" :value="lv" />
        </el-select>
        <el-input v-model="keyword" placeholder="搜索内容" clearable style="width: 180px" />
        <el-switch
          :model-value="autoRefresh"
          active-text="自动刷新"
          @update:model-value="(v: boolean) => toggleAuto(v)"
        />
        <el-button :loading="loading" @click="load()">刷新</el-button>
        <el-button type="danger" plain @click="onClear">清空缓冲</el-button>
      </template>
    </ManagePageHeader>

    <div v-if="fileInfo?.file" class="log-meta">
      落盘文件 <span class="mono">{{ fileInfo.file }}</span>
      · 当前级别 <span class="mono">{{ fileInfo.level }}</span>
      · 保留
      <span class="mono">{{ fileInfo.retain_days === 0 ? '永久' : fileInfo.retain_days + ' 天' }}</span>
      （一天一个文件 <span class="mono">app-YYYY-MM-DD.log</span>，按月份目录分层
      <span class="mono">YYYY-MM/</span>；过期文件逐日清理。可用
      <span class="mono">-log-level</span> / <span class="mono">-log-retain-days</span> 调整）
    </div>

    <div v-loading="loading" class="log-view">
      <div v-if="visible.length" class="log-view__body">
        <div v-for="(e, i) in visible" :key="i" class="log-line">
          <span class="log-line__time">{{ timeText(e.at) }}</span>
          <span class="log-line__level" :class="levelClass(e.level)">{{ normalizeLevel(e.level) }}</span>
          <span class="log-line__msg">{{ e.message }}</span>
        </div>
      </div>
      <el-empty v-else description="没有符合筛选条件的日志" />
    </div>
  </div>
</template>

<style scoped>
.log-meta {
  margin-bottom: 10px;
  font-size: 12px;
  color: var(--text-3, #909399);
  line-height: 1.8;
}
.log-view {
  background: #0f1720;
  border-radius: 8px;
  min-height: 360px;
  max-height: calc(100vh - 220px);
  overflow: auto;
  padding: 12px 14px;
}
.log-view__body {
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  font-size: 12px;
  line-height: 1.65;
}
.log-line {
  display: flex;
  gap: 10px;
  align-items: baseline;
  white-space: pre-wrap;
  word-break: break-all;
}
.log-line__time {
  flex: 0 0 auto;
  color: #64748b;
}
.log-line__level {
  flex: 0 0 auto;
  font-weight: 600;
  width: 44px;
}
.log-line__msg {
  color: #cbd5e1;
}
.lv-debug {
  color: #94a3b8;
}
.lv-info {
  color: #60a5fa;
}
.lv-warn {
  color: #fbbf24;
}
.lv-error {
  color: #f87171;
}
</style>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import ManagePageHeader from '@/components/ManagePageHeader.vue'
import { systemApi } from '@/api/system'
import { describeError } from '@/api/request'
import { usePolling } from '@/composables/usePolling'

const loading = ref(false)
const lines = ref<string[]>([])
const autoRefresh = ref(false)

const polling = usePolling(async () => {
  await load(false)
}, 3000)

async function load(showLoading = true): Promise<void> {
  if (showLoading) {
    loading.value = true
  }
  try {
    const res = await systemApi.log(800)
    lines.value = res.lines
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
    await ElMessageBox.confirm('确认清空内存中的系统日志缓冲？', '确认操作', {
      type: 'warning',
      confirmButtonText: '清空',
      cancelButtonText: '取消',
    })
  } catch {
    return
  }
  try {
    await systemApi.clearLog()
    lines.value = []
    ElMessage.success('已清空')
  } catch (e) {
    ElMessage.error(describeError(e))
  }
}
</script>

<template>
  <div class="page">
    <ManagePageHeader title="系统日志" description="平台运行日志（内存环形缓冲，最近 2000 行）">
      <template #actions>
        <el-switch
          :model-value="autoRefresh"
          active-text="自动刷新"
          @update:model-value="(v: boolean) => toggleAuto(v)"
        />
        <el-button :loading="loading" @click="load()">刷新</el-button>
        <el-button type="danger" plain @click="onClear">清空</el-button>
      </template>
    </ManagePageHeader>

    <div v-loading="loading" class="log-view">
      <pre v-if="lines.length" class="log-view__pre">{{ lines.join('\n') }}</pre>
      <el-empty v-else description="暂无日志" />
    </div>
  </div>
</template>

<style scoped>
.log-view {
  background: #0f1720;
  border-radius: 8px;
  min-height: 360px;
  max-height: calc(100vh - 200px);
  overflow: auto;
  padding: 14px 16px;
}
.log-view__pre {
  margin: 0;
  color: #cbd5e1;
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  font-size: 12px;
  line-height: 1.6;
  white-space: pre-wrap;
  word-break: break-all;
}
</style>

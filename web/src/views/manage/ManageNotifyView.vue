<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { ElMessage } from 'element-plus'
import ManagePageHeader from '@/components/ManagePageHeader.vue'
import { systemApi } from '@/api/system'
import { describeError } from '@/api/request'
import { formatTime } from '@/utils/format'
import type { NotifyRecord } from '@/types/system'

// 通知历史：把 [notify] 的投递结果结构化落盘后按任务 / 收件人 / 状态查询，
// 用来回答「这条告警谁漏收了」——按收件人粒度记录，收发双方都能对上账。
const loading = ref(false)
const records = ref<NotifyRecord[]>([])
const filters = reactive({ pipeline: '', recipient: '', status: '' })

const STATUS_LABELS: Record<string, string> = {
  sent: '已送达',
  failed: '失败',
  suppressed: '已合并',
}

async function load(): Promise<void> {
  loading.value = true
  try {
    records.value = await systemApi.notifyHistory({
      limit: 300,
      pipeline: filters.pipeline.trim(),
      recipient: filters.recipient.trim(),
      status: filters.status,
    })
  } catch (e) {
    ElMessage.error(describeError(e))
  } finally {
    loading.value = false
  }
}

function reset(): void {
  filters.pipeline = ''
  filters.recipient = ''
  filters.status = ''
  void load()
}

onMounted(load)

function eventLabel(event: string): string {
  switch (event) {
    case 'failure':
      return '失败'
    case 'success':
      return '成功'
    case 'fixed':
      return '已修复'
    case 'aborted':
      return '已取消'
  }
  return event || '-'
}

function eventType(event: string): 'success' | 'danger' | 'warning' | 'info' {
  switch (event) {
    case 'failure':
      return 'danger'
    case 'success':
      return 'success'
    case 'fixed':
      return 'info'
    case 'aborted':
      return 'warning'
  }
  return 'info'
}

function statusType(status: string): 'success' | 'danger' | 'info' {
  if (status === 'sent') return 'success'
  if (status === 'failed') return 'danger'
  return 'info'
}
</script>

<template>
  <div class="page manage">
    <ManagePageHeader
      title="通知记录"
      description="每次构建通知的投递结果（按收件人/群粒度，保存在 data/notify.log）"
    >
      <template #actions>
        <el-button :loading="loading" @click="load()">刷新</el-button>
      </template>
    </ManagePageHeader>

    <el-card shadow="never" class="filter-card">
      <div class="filters">
        <el-input
          v-model="filters.pipeline"
          placeholder="任务 ID 或名称"
          clearable
          style="width: 220px"
          @keyup.enter="load"
        />
        <el-input
          v-model="filters.recipient"
          placeholder="收件人邮箱 / 群标签"
          clearable
          style="width: 240px"
          @keyup.enter="load"
        />
        <el-select v-model="filters.status" placeholder="投递状态" clearable style="width: 150px">
          <el-option label="已送达" value="sent" />
          <el-option label="失败" value="failed" />
          <el-option label="已合并（抑制）" value="suppressed" />
        </el-select>
        <el-button type="primary" @click="load">查询</el-button>
        <el-button @click="reset">重置</el-button>
      </div>
      <div class="form-tip">
        失败记录会带错误原因（SMTP 拒绝、机器人 token 失效等）；「已合并」表示命中了失败抑制窗口，
        只发首条、后续不再刷屏。按收件人过滤可直接筛出「谁没收到」。
      </div>
    </el-card>

    <el-card shadow="never" v-loading="loading">
      <el-table :data="records" stripe size="small" empty-text="暂无通知记录">
        <el-table-column label="时间" width="170">
          <template #default="{ row }">
            <span class="mono muted">{{ formatTime(row.time) }}</span>
          </template>
        </el-table-column>
        <el-table-column label="任务" min-width="160">
          <template #default="{ row }">
            <span class="mono">{{ row.pipeline_name || row.pipeline_id }}</span>
            <span class="muted"> #{{ row.build_id }}</span>
          </template>
        </el-table-column>
        <el-table-column label="事件" width="90">
          <template #default="{ row }">
            <el-tag :type="eventType(row.event)" size="small" disable-transitions>
              {{ eventLabel(row.event) }}
            </el-tag>
            <el-tag v-if="row.escalated" type="danger" size="small" effect="plain" disable-transitions>
              升级
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="渠道" width="100">
          <template #default="{ row }">
            <span class="mono muted">{{ row.channel || '-' }}</span>
          </template>
        </el-table-column>
        <el-table-column label="收件人" min-width="200">
          <template #default="{ row }">
            <span class="mono">{{ row.recipient || '-' }}</span>
          </template>
        </el-table-column>
        <el-table-column label="状态" width="100">
          <template #default="{ row }">
            <el-tag :type="statusType(row.status)" size="small" disable-transitions>
              {{ STATUS_LABELS[row.status] ?? row.status }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="上下文" width="220">
          <template #default="{ row }">
            <span class="muted">
              <template v-if="row.branch">分支 {{ row.branch }}</template>
              <template v-if="row.streak && row.streak > 1"> · 连败 {{ row.streak }} 次</template>
              <template v-if="row.on_call"> · 值班 {{ row.on_call }}</template>
            </span>
          </template>
        </el-table-column>
        <el-table-column label="错误" min-width="220">
          <template #default="{ row }">
            <span class="mono danger-text">{{ row.error || '-' }}</span>
          </template>
        </el-table-column>
      </el-table>
    </el-card>
  </div>
</template>

<style scoped>
.filter-card {
  margin-bottom: 12px;
}

.filters {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}

.danger-text {
  color: var(--el-color-danger);
  font-size: 12px;
  word-break: break-all;
}
</style>

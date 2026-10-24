<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { ElMessage } from 'element-plus'
import ManagePageHeader from '@/components/ManagePageHeader.vue'
import { systemApi } from '@/api/system'
import { describeError } from '@/api/request'
import { formatTime } from '@/utils/format'
import type { AuditEntry } from '@/types/system'

const loading = ref(false)
const entries = ref<AuditEntry[]>([])

// 动作文案与颜色。
const ACTION_LABELS: Record<string, string> = {
  login: '登录',
  logout: '登出',
  create: '创建',
  update: '修改',
  delete: '删除',
}
function actionLabel(action: string): string {
  return ACTION_LABELS[action] ?? action
}
function actionType(action: string): 'success' | 'warning' | 'danger' | 'info' {
  if (action === 'login' || action === 'logout') return 'info'
  if (action === 'delete') return 'danger'
  if (action === 'create') return 'success'
  return 'warning'
}

async function load(showLoading = true): Promise<void> {
  if (showLoading) {
    loading.value = true
  }
  try {
    entries.value = await systemApi.audit(300)
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
</script>

<template>
  <div class="page">
    <ManagePageHeader title="操作日志" description="变更类操作审计（登录/登出/构建触发/任务与配置增删改，保留在 data/audit.log）">
      <template #actions>
        <el-button :loading="loading" @click="load()">刷新</el-button>
      </template>
    </ManagePageHeader>

    <el-card shadow="never" v-loading="loading">
      <el-table :data="entries" stripe size="small" empty-text="暂无操作记录">
        <el-table-column label="时间" width="170">
          <template #default="{ row }">
            <span class="mono muted">{{ formatTime(row.time) }}</span>
          </template>
        </el-table-column>
        <el-table-column label="用户" width="130">
          <template #default="{ row }">
            <span class="mono">{{ row.user || 'anonymous' }}</span>
          </template>
        </el-table-column>
        <el-table-column label="动作" width="90">
          <template #default="{ row }">
            <el-tag :type="actionType(row.action)" size="small" disable-transitions>
              {{ actionLabel(row.action) }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="目标" min-width="280">
          <template #default="{ row }">
            <span class="mono">{{ row.target }}</span>
          </template>
        </el-table-column>
        <el-table-column label="结果" width="90">
          <template #default="{ row }">
            <el-tag :type="row.status === 0 ? 'success' : 'danger'" size="small" disable-transitions>
              {{ row.status === 0 ? '成功' : `失败 ${row.status}` }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="来源 IP" width="140">
          <template #default="{ row }">
            <span class="mono muted">{{ row.ip || '-' }}</span>
          </template>
        </el-table-column>
      </el-table>
    </el-card>
  </div>
</template>

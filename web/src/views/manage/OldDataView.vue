<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import ManagePageHeader from '@/components/ManagePageHeader.vue'
import { systemApi } from '@/api/system'
import { describeError } from '@/api/request'
import { formatBytes, formatTime } from '@/utils/format'
import type { OldDataReport } from '@/types/system'

const loading = ref(false)
const cleaning = ref(false)
const report = ref<OldDataReport | null>(null)

async function scan(): Promise<void> {
  loading.value = true
  try {
    report.value = await systemApi.oldData()
  } catch (e) {
    ElMessage.error(describeError(e))
  } finally {
    loading.value = false
  }
}

onMounted(scan)

async function clean(): Promise<void> {
  try {
    await ElMessageBox.confirm(
      '将删除无对应任务的孤儿工作区与孤儿任务目录；仍存在任务的工作区会保留，构建记录本身不受影响。确认清理？',
      '清理旧数据',
      { type: 'warning', confirmButtonText: '确认清理', cancelButtonText: '取消' },
    )
  } catch {
    return
  }
  cleaning.value = true
  try {
    report.value = await systemApi.cleanOldData()
    ElMessage.success('清理完成')
  } catch (e) {
    ElMessage.error(describeError(e))
  } finally {
    cleaning.value = false
  }
}
</script>

<template>
  <div class="page">
    <ManagePageHeader title="管理旧数据" description="清理无主工作区与孤儿任务目录，释放磁盘空间">
      <template #actions>
        <el-button :loading="loading" @click="scan">重新扫描</el-button>
        <el-button type="danger" :loading="cleaning" @click="clean">清理旧数据</el-button>
      </template>
    </ManagePageHeader>

    <div class="old-data__summary">
      <div class="summary-item">
        <span class="summary-item__label">可清理项</span>
        <span class="summary-item__value">{{ report?.items.length ?? 0 }}</span>
      </div>
      <div class="summary-item">
        <span class="summary-item__label">可释放空间</span>
        <span class="summary-item__value">{{ formatBytes(report?.total_bytes ?? 0) }}</span>
      </div>
      <div class="summary-item">
        <span class="summary-item__label">扫描时间</span>
        <span class="summary-item__value summary-item__value--sm">{{ formatTime(report?.scanned_at) }}</span>
      </div>
    </div>

    <el-card v-loading="loading" shadow="never">
      <el-table :data="report?.items ?? []">
        <el-table-column label="类型" width="130">
          <template #default="{ row }">
            <el-tag effect="plain" disable-transitions>
              {{ row.kind === 'workspace' ? '孤儿工作区' : '孤儿目录' }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="路径" min-width="320">
          <template #default="{ row }">
            <span class="mono">{{ row.path }}</span>
          </template>
        </el-table-column>
        <el-table-column label="大小" width="120">
          <template #default="{ row }">{{ formatBytes(row.bytes) }}</template>
        </el-table-column>
        <el-table-column label="说明" prop="detail" min-width="200" />
        <template #empty>
          <el-empty description="没有需要清理的旧数据" />
        </template>
      </el-table>
    </el-card>
  </div>
</template>

<style scoped>
.old-data__summary {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(180px, 1fr));
  gap: 12px;
  margin-bottom: 16px;
}
.summary-item {
  display: flex;
  flex-direction: column;
  gap: 4px;
  padding: 12px 16px;
  background: #fff;
  border: 1px solid #e4e7ed;
  border-radius: 8px;
}
.summary-item__label {
  font-size: 12px;
  color: #909399;
}
.summary-item__value {
  font-size: 16px;
  font-weight: 600;
  color: #303133;
}
.summary-item__value--sm {
  font-size: 13px;
  font-weight: 500;
}
</style>

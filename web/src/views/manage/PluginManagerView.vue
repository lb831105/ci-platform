<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { ElMessage } from 'element-plus'
import ManagePageHeader from '@/components/ManagePageHeader.vue'
import { systemApi } from '@/api/system'
import { describeError } from '@/api/request'
import type { Feature } from '@/types/system'

const loading = ref(false)
const features = ref<Feature[]>([])

onMounted(async () => {
  loading.value = true
  try {
    features.value = await systemApi.features()
  } catch (e) {
    ElMessage.error(describeError(e))
  } finally {
    loading.value = false
  }
})
</script>

<template>
  <div class="page">
    <ManagePageHeader title="插件管理" description="平台以单二进制交付，内置能力清单（只读）" />

    <el-alert
      type="info"
      :closable="false"
      show-icon
      title="基础版不支持动态安装插件：Git / SSH / Webhook / WebSocket 等能力已内置于二进制。"
      style="margin-bottom: 16px; max-width: 960px"
    />

    <el-card v-loading="loading" shadow="never" class="plugins-card">
      <el-table :data="features">
        <el-table-column label="组件" prop="name" min-width="200">
          <template #default="{ row }">
            <span class="mono">{{ row.name }}</span>
          </template>
        </el-table-column>
        <el-table-column label="状态" width="120">
          <template #default="{ row }">
            <el-tag :type="row.status === 'active' ? 'success' : 'info'" effect="plain" disable-transitions>
              {{ row.status === 'active' ? '已启用' : '内置' }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="版本" prop="version" width="120" />
        <el-table-column label="说明" prop="description" min-width="280" />
      </el-table>
    </el-card>
  </div>
</template>

<style scoped>
.plugins-card {
  max-width: 960px;
}
</style>

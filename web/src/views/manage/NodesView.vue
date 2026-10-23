<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { ElMessage } from 'element-plus'
import ManagePageHeader from '@/components/ManagePageHeader.vue'
import { systemApi } from '@/api/system'
import { describeError } from '@/api/request'
import type { LoadStats, SystemInfo } from '@/types/system'

const loading = ref(false)
const info = ref<SystemInfo | null>(null)
const stats = ref<LoadStats | null>(null)

onMounted(async () => {
  loading.value = true
  try {
    const [i, l] = await Promise.all([systemApi.info(), systemApi.load()])
    info.value = i
    stats.value = l
  } catch (e) {
    ElMessage.error(describeError(e))
  } finally {
    loading.value = false
  }
})
</script>

<template>
  <div class="page">
    <ManagePageHeader title="节点和云管理" description="控制系统运行任务的节点与执行器" />

    <el-card v-loading="loading" shadow="never" class="node-card">
      <template #header>
        <div class="node-card__head">
          <span>内置节点（Master）</span>
          <el-tag type="success" effect="plain" disable-transitions>在线</el-tag>
        </div>
      </template>
      <el-descriptions v-if="info" :column="2" border>
        <el-descriptions-item label="执行器数量">{{ info.workers }}</el-descriptions-item>
        <el-descriptions-item label="构建队列">
          {{ stats?.queue_length ?? 0 }} / {{ stats?.queue_capacity ?? info.queue_capacity }}（排队中 / 容量）
        </el-descriptions-item>
        <el-descriptions-item label="运行中构建">{{ stats?.running_builds ?? 0 }}</el-descriptions-item>
        <el-descriptions-item label="操作系统">{{ info.os }} / {{ info.arch }}</el-descriptions-item>
        <el-descriptions-item label="CPU 核心">{{ info.cpus }}</el-descriptions-item>
        <el-descriptions-item label="工作目录">
          <span class="mono">{{ info.workspace_dir }}</span>
        </el-descriptions-item>
      </el-descriptions>
    </el-card>

    <el-card shadow="never" class="node-card">
      <template #header>云 / 分布式节点</template>
      <el-alert
        type="info"
        :closable="false"
        show-icon
        title="基础版不提供云实例与远程 agent 的动态编排；构建在运行服务的宿主机上执行，同一任务串行、不同任务按 worker 并行。"
      />
    </el-card>
  </div>
</template>

<style scoped>
.node-card {
  max-width: 960px;
  margin-bottom: 16px;
}
.node-card__head {
  display: flex;
  align-items: center;
  justify-content: space-between;
}
</style>

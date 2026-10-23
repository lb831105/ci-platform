<script setup lang="ts">
import { onMounted, ref } from 'vue'
import ManagePageHeader from '@/components/ManagePageHeader.vue'
import { systemApi } from '@/api/system'
import type { SystemInfo } from '@/types/system'

const info = ref<SystemInfo | null>(null)

onMounted(async () => {
  try {
    info.value = await systemApi.info()
  } catch {
    // 关于页允许静默失败
  }
})
</script>

<template>
  <div class="page">
    <ManagePageHeader title="关于" description="版本、构建信息与接口说明" />

    <el-card shadow="never" class="about-card">
      <el-descriptions :column="1" border>
        <el-descriptions-item label="应用">{{ info?.app_name ?? 'CI Platform' }}</el-descriptions-item>
        <el-descriptions-item label="版本">{{ info?.version ?? '-' }}</el-descriptions-item>
        <el-descriptions-item label="Commit"><span class="mono">{{ info?.commit ?? '-' }}</span></el-descriptions-item>
        <el-descriptions-item label="构建时间">{{ info?.build_date ?? '-' }}</el-descriptions-item>
        <el-descriptions-item label="Go 版本">{{ info?.go_version ?? '-' }}</el-descriptions-item>
      </el-descriptions>
    </el-card>

    <el-card shadow="never" class="about-card">
      <template #header>Jenkins 命令行接口 / 脚本命令行</template>
      <p class="about-text">基础版不提供 Groovy 脚本控制台；所有能力通过 REST API 暴露，可用命令行或脚本调用：</p>
      <ul class="about-list">
        <li><code>POST /api/auth/login</code> 获取 Bearer token</li>
        <li><code>GET /api/overview</code> 任务与构建概览</li>
        <li><code>POST /api/pipelines/{id}/build</code> 触发构建</li>
        <li><code>POST /api/webhook/{id}</code> Webhook 触发（<code>X-Webhook-Secret</code>）</li>
        <li><code>GET /api/system/info</code> 系统信息</li>
      </ul>
      <div class="muted">所有接口统一返回 <code>{ code, msg, data }</code>，HTTP 状态恒为 200，业务态由 <code>code</code> 表达。</div>
    </el-card>

    <el-card shadow="never" class="about-card">
      <template #header>许可</template>
      <div class="muted">内部使用的轻量 CI/CD 平台，未附带任何第三方插件生态。</div>
    </el-card>
  </div>
</template>

<style scoped>
.about-card {
  max-width: 900px;
  margin-bottom: 16px;
}
.about-text {
  margin: 0 0 10px;
  color: #606266;
  font-size: 13px;
}
.about-list {
  margin: 0 0 12px;
  padding-left: 18px;
  color: #606266;
  font-size: 13px;
  line-height: 1.9;
}
code {
  background: #f3f4f6;
  border-radius: 4px;
  padding: 1px 5px;
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
}
</style>

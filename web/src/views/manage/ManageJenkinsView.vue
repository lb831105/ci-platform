<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { RouterLink } from 'vue-router'
import { systemApi } from '@/api/system'
import type { LoadStats, Settings, SystemInfo } from '@/types/system'

const info = ref<SystemInfo | null>(null)
const stats = ref<LoadStats | null>(null)
const settings = ref<Settings | null>(null)

interface ManageEntry {
  title: string
  desc: string
  route: string
}

interface ManageSection {
  title: string
  entries: ManageEntry[]
}

const sections: ManageSection[] = [
  {
    title: '系统配置',
    entries: [
      { title: '系统配置', desc: '配置全局设置和路径，包括应用名称与默认构建参数', route: 'system-configure' },
      { title: '全局工具配置', desc: '维护服务器上的工具主目录（JAVA_HOME / GOROOT 等）', route: 'system-tools' },
      { title: '插件管理', desc: '查看平台内置能力与运行状态', route: 'system-plugins' },
      { title: '节点和云管理', desc: '查看执行器、构建队列与工作区', route: 'system-nodes' },
      { title: '导入任务', desc: '从 Jenkins config.xml 或平台导出的 config.json 导入任务配置', route: 'system-import' },
    ],
  },
  {
    title: '安全',
    entries: [
      { title: '全局安全配置', desc: '开启或关闭登录校验，定义谁可以访问系统', route: 'system-security' },
      { title: '凭据管理', desc: '配置用户名密码、密钥与 SSH 私钥', route: 'system-credentials' },
      { title: '用户管理', desc: '新建/删除用户、重置密码、维护通知联系方式', route: 'system-users' },
      { title: 'Manage and Assign Roles', desc: '定义全局/任务角色并授权给用户（角色决定每个任务可用的功能）', route: 'system-roles-manage' },
      { title: 'Assign Roles', desc: '把用户/组授权到全局角色与任务角色', route: 'system-roles-assign' },
    ],
  },
  {
    title: '状态信息',
    entries: [
      { title: '系统信息', desc: '显示系统环境与运行时信息以帮助解决问题', route: 'system-info' },
      { title: '系统日志', desc: '查看平台最近输出的运行日志', route: 'system-log' },
      { title: '操作日志', desc: '谁在何时做了什么：登录、构建触发、任务与配置变更', route: 'system-audit' },
      { title: '通知记录', desc: '每次告警的投递结果：谁收到了、谁失败了、哪些被合并', route: 'system-notify-history' },
      { title: '负载统计', desc: '检查构建、队列与磁盘资源利用情况', route: 'system-load' },
      { title: '关于', desc: '查看版本、构建信息与证书说明', route: 'system-about' },
    ],
  },
  {
    title: '问题排查',
    entries: [{ title: '管理旧数据', desc: '清理构建工作区与无主任务目录，释放磁盘', route: 'system-old-data' }],
  },
  {
    title: '工具和动作',
    entries: [
      { title: '读取设置', desc: '从磁盘重新读取 settings.json（手动修改配置后使用）', route: 'system-configure' },
      { title: '准备关机', desc: '进入静默模式，停止接受新的构建任务', route: 'system-configure' },
      { title: '命令行接口 / 脚本命令行', desc: '通过 REST API 或外部脚本管理平台', route: 'system-about' },
    ],
  },
]

onMounted(async () => {
  try {
    const [i, l, s] = await Promise.all([systemApi.info(), systemApi.load(), systemApi.getSettings()])
    info.value = i
    stats.value = l
    settings.value = s
  } catch {
    // 概览加载失败不阻塞页面：各子页会给出各自错误。
  }
})
</script>

<template>
  <div class="page manage">
    <div class="page-header">
      <div>
        <h1 class="page-title">系统管理</h1>
        <div class="muted">Manage Jenkins · 配置、安全、状态与运维动作</div>
      </div>
    </div>

    <div class="manage__summary">
      <div class="summary-item">
        <span class="summary-item__label">应用名称</span>
        <span class="summary-item__value">{{ settings?.display_name || 'CI Platform' }}</span>
      </div>
      <div class="summary-item">
        <span class="summary-item__label">任务 / 构建</span>
        <span class="summary-item__value">{{ info?.pipeline_count ?? '-' }} / {{ info?.build_count ?? '-' }}</span>
      </div>
      <div class="summary-item">
        <span class="summary-item__label">执行器</span>
        <span class="summary-item__value">{{ info?.workers ?? '-' }}</span>
      </div>
      <div class="summary-item">
        <span class="summary-item__label">队列</span>
        <span class="summary-item__value">{{ stats?.queue_length ?? 0 }} / {{ stats?.queue_capacity ?? '-' }}</span>
      </div>
      <div class="summary-item">
        <span class="summary-item__label">登录校验</span>
        <span class="summary-item__value" :class="settings?.auth_enabled ? 'is-on' : 'is-off'">
          {{ settings?.auth_enabled ? '已开启' : '已关闭' }}
        </span>
      </div>
      <div class="summary-item">
        <span class="summary-item__label">静默模式</span>
        <span class="summary-item__value" :class="settings?.quiet_mode ? 'is-off' : 'is-on'">
          {{ settings?.quiet_mode ? '已开启' : '未开启' }}
        </span>
      </div>
    </div>

    <section v-for="section in sections" :key="section.title" class="manage__section">
      <h2 class="manage__section-title">{{ section.title }}</h2>
      <div class="manage__grid">
        <RouterLink
          v-for="entry in section.entries"
          :key="section.title + entry.title"
          class="manage-card"
          :to="{ name: entry.route }"
        >
          <div class="manage-card__title">{{ entry.title }}</div>
          <div class="manage-card__desc">{{ entry.desc }}</div>
        </RouterLink>
      </div>
    </section>
  </div>
</template>

<style scoped>
.manage__summary {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(160px, 1fr));
  gap: 12px;
  margin-bottom: 22px;
}
.summary-item {
  display: flex;
  flex-direction: column;
  gap: 4px;
  padding: 12px 16px;
  background: #fff;
  border: 1px solid var(--border-1);
  border-radius: 8px;
}
.summary-item__label {
  font-size: 12px;
  color: var(--text-3);
}
.summary-item__value {
  font-size: 16px;
  font-weight: 600;
  color: var(--text-1);
}
.summary-item__value.is-on {
  color: var(--success);
}
.summary-item__value.is-off {
  color: var(--warning);
}
.manage__section {
  margin-bottom: 22px;
}
.manage__section-title {
  font-size: 15px;
  font-weight: 600;
  color: var(--text-1);
  margin: 0 0 10px;
}
.manage__grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(280px, 1fr));
  gap: 12px;
}
.manage-card {
  display: block;
  padding: 14px 16px;
  background: #fff;
  border: 1px solid var(--border-1);
  border-radius: 8px;
  text-decoration: none;
  transition: box-shadow 0.15s, border-color 0.15s, transform 0.15s;
}
.manage-card:hover {
  border-color: var(--brand-200);
  box-shadow: 0 6px 18px rgba(31, 111, 208, 0.1);
  transform: translateY(-1px);
}
.manage-card__title {
  font-size: 14px;
  font-weight: 600;
  color: var(--brand-600);
  margin-bottom: 4px;
}
.manage-card__desc {
  font-size: 12px;
  color: var(--text-3);
  line-height: 1.6;
}
</style>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { RouterLink, RouterView, useRoute, useRouter } from 'vue-router'
import AppIcon from '@/components/AppIcon.vue'
import JobStatusIcon from '@/components/JobStatusIcon.vue'
import { useAuthStore } from '@/stores/authStore'
import { useOverviewStore } from '@/stores/overviewStore'
import { usePipelineStore } from '@/stores/pipelineStore'
import { useSettingsStore } from '@/stores/settingsStore'

const route = useRoute()
const router = useRouter()
const auth = useAuthStore()
const overview = useOverviewStore()
const pipelineStore = usePipelineStore()
const settings = useSettingsStore()

const keyword = ref('')
const collapsed = ref(false)

const jobs = computed(() => {
  const q = keyword.value.trim().toLowerCase()
  if (!q) {
    return overview.items
  }
  return overview.items.filter((job) => job.name.toLowerCase().includes(q) || job.id.includes(q))
})

const runningCount = computed(() => overview.items.filter((job) => job.running).length)
const activePipelineId = computed(() => (typeof route.params.pipelineId === 'string' ? route.params.pipelineId : ''))
const initials = computed(() => (auth.username || 'admin').slice(0, 1).toUpperCase())

const isSystemActive = computed(() => route.path.startsWith('/system'))
const isCreateActive = computed(() => route.path.startsWith('/pipelines/new'))
const isBuildsActive = computed(() => route.name === 'global-build-history')

onMounted(() => {
  void overview.fetch()
  void settings.fetch()
})

async function onUserCommand(command: string): Promise<void> {
  if (command !== 'logout') {
    return
  }
  await auth.logout()
  overview.clear()
  pipelineStore.setAll([])
  void router.push({ name: 'login' })
}
</script>

<template>
  <div class="layout">
    <header class="topbar">
      <button class="topbar__toggle" type="button" aria-label="切换侧边栏" @click="collapsed = !collapsed">
        <AppIcon name="menu" />
      </button>

      <RouterLink class="brand" :to="{ name: 'pipeline-list' }">
        <span class="brand__logo"><AppIcon name="cube" :size="17" /></span>
        <span class="brand__name">{{ settings.displayName }}</span>
      </RouterLink>

      <div class="topbar__search">
        <AppIcon name="search" :size="16" />
        <input v-model="keyword" type="search" placeholder="搜索任务…" />
      </div>

      <div class="topbar__right">
        <span class="status-pill" :class="{ 'status-pill--busy': runningCount > 0 }">
          <span class="status-pill__dot" />
          {{ runningCount > 0 ? `构建中 ${runningCount}` : '空闲' }}
          <span class="status-pill__total">/ 共 {{ overview.items.length }}</span>
        </span>

        <el-dropdown trigger="click" @command="onUserCommand">
          <div class="user">
            <span class="user__avatar">{{ initials }}</span>
            <span class="user__name">{{ auth.username || 'admin' }}</span>
          </div>
          <template #dropdown>
            <el-dropdown-menu>
              <el-dropdown-item command="logout">
                <AppIcon name="logout" :size="15" />
                <span style="margin-left: 8px">退出登录</span>
              </el-dropdown-item>
            </el-dropdown-menu>
          </template>
        </el-dropdown>
      </div>
    </header>

    <div class="layout__body">
      <aside v-show="!collapsed" class="sidebar">
        <nav class="sidebar__menu">
          <RouterLink class="menu-item" :class="{ 'menu-item--active': isCreateActive }" :to="{ name: 'pipeline-create' }">
            <AppIcon name="plus" />
            <span>新建任务</span>
          </RouterLink>
          <RouterLink class="menu-item" :class="{ 'menu-item--active': isSystemActive }" :to="{ name: 'system-config' }">
            <AppIcon name="settings" />
            <span>系统管理</span>
          </RouterLink>
          <RouterLink class="menu-item" :class="{ 'menu-item--active': isBuildsActive }" :to="{ name: 'global-build-history' }">
            <AppIcon name="history" />
            <span>构建历史</span>
          </RouterLink>
        </nav>

        <div class="sidebar__jobs">
          <div class="sidebar__jobs-head">
            <span>任务列表</span>
            <span class="sidebar__jobs-count">{{ overview.items.length }}</span>
          </div>
          <div v-if="overview.loading && overview.items.length === 0" class="sidebar__empty">加载中…</div>
          <ul v-else class="job-list">
            <li v-for="job in jobs" :key="job.id">
              <RouterLink
                class="job-item"
                :class="{ 'job-item--active': job.id === activePipelineId }"
                :to="{ name: 'build-history', params: { pipelineId: job.id } }"
              >
                <JobStatusIcon :status="job.status" />
                <span class="job-item__name">{{ job.name }}</span>
              </RouterLink>
            </li>
            <li v-if="jobs.length === 0" class="sidebar__empty">
              {{ overview.items.length === 0 ? '还没有任务' : '没有匹配的任务' }}
            </li>
          </ul>
        </div>
      </aside>

      <main class="content">
        <div v-if="settings.systemMessage" class="system-message">{{ settings.systemMessage }}</div>
        <RouterView />
      </main>
    </div>
  </div>
</template>

<style scoped>
.layout {
  display: flex;
  flex-direction: column;
  height: 100vh;
  background: var(--bg-app);
}

/* ---------------- Topbar ---------------- */
.topbar {
  flex: 0 0 56px;
  display: flex;
  align-items: center;
  gap: 16px;
  padding: 0 18px;
  background: #fff;
  border-bottom: 1px solid var(--border-1);
  box-shadow: var(--shadow-xs);
  z-index: 10;
}
.topbar__toggle {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 34px;
  height: 34px;
  border: 1px solid var(--border-1);
  border-radius: var(--radius-sm);
  background: #fff;
  color: var(--text-2);
  cursor: pointer;
}
.topbar__toggle:hover {
  background: var(--bg-hover);
  color: var(--brand-600);
}
.brand {
  display: flex;
  align-items: center;
  gap: 10px;
  text-decoration: none;
  color: var(--text-1);
}
.brand__logo {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 32px;
  height: 32px;
  border-radius: 9px;
  background: linear-gradient(135deg, #3b82f6, #2563eb);
  color: #fff;
  box-shadow: 0 4px 10px rgba(37, 99, 235, 0.35);
}
.brand__name {
  font-size: 16px;
  font-weight: 650;
  letter-spacing: -0.01em;
}
.topbar__search {
  display: flex;
  align-items: center;
  gap: 8px;
  flex: 0 1 420px;
  height: 36px;
  padding: 0 12px;
  border: 1px solid var(--border-1);
  border-radius: 999px;
  background: #f8fafc;
  color: var(--text-4);
  transition: border-color 0.15s, box-shadow 0.15s, background 0.15s;
}
.topbar__search:focus-within {
  border-color: var(--brand-500);
  background: #fff;
  box-shadow: 0 0 0 3px var(--brand-100);
}
.topbar__search input {
  flex: 1;
  border: none;
  outline: none;
  background: transparent;
  font-size: 13.5px;
  color: var(--text-1);
}
.topbar__right {
  margin-left: auto;
  display: flex;
  align-items: center;
  gap: 14px;
}
.status-pill {
  display: inline-flex;
  align-items: center;
  gap: 7px;
  height: 30px;
  padding: 0 12px;
  border-radius: 999px;
  background: #f1f5f9;
  color: var(--text-2);
  font-size: 12.5px;
}
.status-pill__dot {
  width: 7px;
  height: 7px;
  border-radius: 50%;
  background: var(--success);
}
.status-pill--busy .status-pill__dot {
  background: var(--brand-500);
  box-shadow: 0 0 0 3px var(--brand-100);
  animation: pulse 1.3s ease-in-out infinite;
}
.status-pill__total {
  color: var(--text-4);
}
.user {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 4px 10px 4px 4px;
  border-radius: 999px;
  cursor: pointer;
  transition: background 0.15s;
}
.user:hover {
  background: var(--bg-hover);
}
.user__avatar {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 28px;
  height: 28px;
  border-radius: 50%;
  background: linear-gradient(135deg, #64748b, #334155);
  color: #fff;
  font-size: 13px;
  font-weight: 600;
}
.user__name {
  font-size: 13.5px;
  color: var(--text-2);
  font-weight: 500;
}

/* ---------------- Body ---------------- */
.layout__body {
  flex: 1;
  display: flex;
  min-height: 0;
}
.sidebar {
  flex: 0 0 268px;
  display: flex;
  flex-direction: column;
  background: #fff;
  border-right: 1px solid var(--border-1);
  min-height: 0;
}
.sidebar__menu {
  display: flex;
  flex-direction: column;
  gap: 3px;
  padding: 14px 12px;
  border-bottom: 1px solid var(--border-2);
}
.menu-item {
  display: flex;
  align-items: center;
  gap: 11px;
  padding: 9px 12px;
  border-radius: var(--radius-sm);
  color: var(--text-2);
  text-decoration: none;
  font-size: 14px;
  font-weight: 500;
  transition: background 0.15s, color 0.15s;
}
.menu-item:hover {
  background: var(--bg-hover);
  color: var(--text-1);
}
.menu-item--active {
  background: var(--brand-50);
  color: var(--brand-700);
}
.sidebar__jobs {
  flex: 1;
  display: flex;
  flex-direction: column;
  min-height: 0;
}
.sidebar__jobs-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 16px 18px 8px;
  font-size: 11.5px;
  font-weight: 600;
  letter-spacing: 0.06em;
  text-transform: uppercase;
  color: var(--text-4);
}
.sidebar__jobs-count {
  background: #f1f5f9;
  border-radius: 999px;
  padding: 1px 9px;
  color: var(--text-3);
  font-size: 11px;
}
.job-list {
  flex: 1;
  overflow-y: auto;
  list-style: none;
  margin: 0;
  padding: 0 10px 14px;
}
.job-item {
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 8px 10px;
  border-radius: var(--radius-sm);
  color: var(--text-2);
  text-decoration: none;
  font-size: 13.5px;
  transition: background 0.15s;
}
.job-item:hover {
  background: var(--bg-hover);
}
.job-item--active {
  background: var(--brand-50);
  color: var(--brand-700);
  font-weight: 600;
}
.job-item__name {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.sidebar__empty {
  padding: 8px 18px;
  color: var(--text-4);
  font-size: 12.5px;
}

.content {
  flex: 1;
  min-width: 0;
  min-height: 0;
  overflow-y: auto;
}
.system-message {
  margin: 16px 28px -6px;
  padding: 11px 16px;
  background: #fffbeb;
  border: 1px solid #fde68a;
  border-radius: var(--radius-sm);
  color: #92400e;
  font-size: 13px;
  line-height: 1.65;
  white-space: pre-wrap;
}
@keyframes pulse {
  0%,
  100% {
    opacity: 1;
  }
  50% {
    opacity: 0.4;
  }
}

@media (max-width: 860px) {
  .topbar__search {
    display: none;
  }
  .brand__name {
    display: none;
  }
  .sidebar {
    position: absolute;
    z-index: 20;
    height: calc(100vh - 56px);
    box-shadow: var(--shadow-lg);
  }
}
</style>

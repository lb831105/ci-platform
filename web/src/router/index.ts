import { createRouter, createWebHistory, type RouteRecordRaw } from 'vue-router'
import { useAuthStore } from '@/stores/authStore'

const routes: RouteRecordRaw[] = [
  {
    path: '/login',
    name: 'login',
    component: () => import('@/views/LoginView.vue'),
    meta: { public: true },
  },
  {
    path: '/',
    component: () => import('@/layout/MainLayout.vue'),
    children: [
      { path: '', redirect: '/pipelines' },
      {
        path: 'pipelines',
        name: 'pipeline-list',
        component: () => import('@/views/PipelineListView.vue'),
      },
      {
        path: 'pipelines/new',
        name: 'pipeline-create',
        component: () => import('@/views/JobCreateView.vue'),
      },
      {
        path: 'pipelines/new/config',
        name: 'pipeline-create-config',
        component: () => import('@/views/PipelineEditView.vue'),
      },
      {
        path: 'pipelines/:pipelineId/edit',
        name: 'pipeline-edit',
        component: () => import('@/views/PipelineEditView.vue'),
        props: true,
      },
      {
        path: 'pipelines/:pipelineId/builds',
        name: 'build-history',
        component: () => import('@/views/BuildHistoryView.vue'),
        props: true,
      },
      {
        path: 'pipelines/:pipelineId/builds/:buildId',
        name: 'build-log',
        component: () => import('@/views/BuildLogView.vue'),
        props: true,
      },
      {
        path: 'builds',
        name: 'global-build-history',
        component: () => import('@/views/GlobalBuildHistoryView.vue'),
      },
      {
        path: 'views/new',
        name: 'view-create',
        component: () => import('@/views/ViewCreateView.vue'),
      },
      {
        path: 'views/:viewName',
        name: 'view-page',
        component: () => import('@/views/ViewPageView.vue'),
        props: true,
      },
      {
        path: 'views/:viewName/edit',
        name: 'view-edit',
        component: () => import('@/views/ViewEditView.vue'),
        props: true,
      },
      {
        path: 'system',
        name: 'system-config',
        component: () => import('@/views/manage/ManageJenkinsView.vue'),
      },
      {
        path: 'system/configure',
        name: 'system-configure',
        component: () => import('@/views/manage/ConfigureSystemView.vue'),
      },
      {
        path: 'system/tools',
        name: 'system-tools',
        component: () => import('@/views/manage/GlobalToolsView.vue'),
      },
      {
        path: 'system/plugins',
        name: 'system-plugins',
        component: () => import('@/views/manage/PluginManagerView.vue'),
      },
      {
        path: 'system/nodes',
        name: 'system-nodes',
        component: () => import('@/views/manage/NodesView.vue'),
      },
      {
        path: 'system/security',
        name: 'system-security',
        component: () => import('@/views/manage/SecurityView.vue'),
      },
      {
        path: 'profile',
        name: 'profile',
        component: () => import('@/views/ProfileView.vue'),
      },
      {
        path: 'system/notify-history',
        name: 'system-notify-history',
        component: () => import('@/views/manage/ManageNotifyView.vue'),
      },
      {
        path: 'system/users',
        name: 'system-users',
        component: () => import('@/views/manage/ManageUsersView.vue'),
      },
      {
        path: 'system/roles-manage',
        name: 'system-roles-manage',
        component: () => import('@/views/manage/ManageRolesView.vue'),
      },
      {
        path: 'system/roles-assign',
        name: 'system-roles-assign',
        component: () => import('@/views/manage/AssignRolesView.vue'),
      },
      {
        path: 'system/credentials',
        name: 'system-credentials',
        component: () => import('@/views/manage/CredentialsView.vue'),
      },
      {
        path: 'system/info',
        name: 'system-info',
        component: () => import('@/views/manage/SystemInfoView.vue'),
      },
      {
        path: 'system/log',
        name: 'system-log',
        component: () => import('@/views/manage/SystemLogView.vue'),
      },
      {
        path: 'system/audit',
        name: 'system-audit',
        component: () => import('@/views/manage/ManageAuditView.vue'),
      },
      {
        path: 'system/load',
        name: 'system-load',
        component: () => import('@/views/manage/LoadStatsView.vue'),
      },
      {
        path: 'system/about',
        name: 'system-about',
        component: () => import('@/views/manage/AboutView.vue'),
      },
      {
        path: 'system/old-data',
        name: 'system-old-data',
        component: () => import('@/views/manage/OldDataView.vue'),
      },
    ],
  },
  { path: '/:pathMatch(.*)*', redirect: '/pipelines' },
]

export const router = createRouter({
  history: createWebHistory(),
  routes,
})

// 全局登录守卫：首次导航先探测登录态；未登录一律跳登录页并记录回跳地址。
router.beforeEach(async (to) => {
  const auth = useAuthStore()
  if (!auth.initialized) {
    await auth.bootstrap()
  }

  if (to.meta.public) {
    if (auth.authenticated && to.name === 'login') {
      return { name: 'pipeline-list' }
    }
    return true
  }

  if (!auth.authenticated) {
    return { name: 'login', query: { redirect: to.fullPath } }
  }
  return true
})

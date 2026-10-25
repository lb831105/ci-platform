<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { ElMessage } from 'element-plus'
import ManagePageHeader from '@/components/ManagePageHeader.vue'
import { rbacApi } from '@/api/rbac'
import { describeError } from '@/api/request'
import type { RbacAssignment, RbacData, RbacEffective, RbacRoleKind } from '@/types/rbac'

const loading = ref(false)
const saving = ref(false)
// 本页只做「授权」：谁 -> 哪个角色。账号自身的增删改密在「用户管理」页。
const data = ref<RbacData | null>(null)
// 生效预览（后端算）：主体能看到哪些任务 / 角色覆盖哪些任务。
const effective = ref<RbacEffective | null>(null)

// 内置主体（对齐 Jenkins）：anonymous 未登录、authenticated 所有已登录用户。
const builtinSubjects = ['anonymous', 'authenticated']

// Jenkins Role Strategy 的两个过滤框：按 User/Group 筛人、按 Role 筛角色。
// 角色多起来（生产上几十个任务角色）不做过滤，一行里根本找不到目标。
const filterSubject = ref('')
const filterRole = ref('')

const globalRoles = computed(() => (data.value?.roles ?? []).filter((r) => r.kind === 'global'))
const allItemRoles = computed(() => (data.value?.roles ?? []).filter((r) => r.kind === 'item'))

function roleVisible(roleName: string): boolean {
  const kw = filterRole.value.trim().toLowerCase()
  return kw === '' || roleName.toLowerCase().includes(kw)
}

const itemRoles = computed(() => allItemRoles.value.filter((r) => roleVisible(r.name)))

// 主体行：内置主体 + 平台内用户 + 授权里出现过的主体（历史数据里可能有已删除账号）。
function subjectsOf(kind: RbacRoleKind): string[] {
  const set = new Set<string>(builtinSubjects)
  ;(data.value?.users ?? []).forEach((u) => set.add(u.name))
  ;(data.value?.assignments ?? [])
    .filter((a) => a.kind === kind)
    .forEach((a) => set.add(a.subject))
  const kw = filterSubject.value.trim().toLowerCase()
  return [...set]
    .filter((s) => kw === '' || s.toLowerCase().includes(kw))
    .sort(subjectOrder)
}

// 内置主体排最前，其余按名称排序（Jenkins 顺序一致，便于扫读）。
function subjectOrder(a: string, b: string): number {
  const ia = builtinSubjects.indexOf(a)
  const ib = builtinSubjects.indexOf(b)
  if (ia >= 0 || ib >= 0) {
    return (ia < 0 ? 99 : ia) - (ib < 0 ? 99 : ib)
  }
  return a.localeCompare(b)
}

const globalSubjects = computed(() => subjectsOf('global'))
const itemSubjects = computed(() => subjectsOf('item'))

async function load(): Promise<void> {
  loading.value = true
  try {
    data.value = await rbacApi.get()
    await reloadEffective()
  } catch (e) {
    ElMessage.error(describeError(e))
  } finally {
    loading.value = false
  }
}

async function reloadEffective(): Promise<void> {
  try {
    effective.value = await rbacApi.effective()
  } catch {
    // 预览失败不影响授权本身（保存后仍会生效）
    effective.value = null
  }
}

// 数据未加载（如无管理权限）时给出一致提示，避免 data.value! 触发运行时异常。
function requireData(): RbacData | null {
  if (!data.value) {
    ElMessage.error('授权数据未加载，请确认权限后刷新页面')
    return null
  }
  return data.value
}

// 平台内用户（users 列表）的行来自账号本身：删掉授权也仍会显示，
// 所以只给他们取消勾选，不提供「移除」（真正的删除在「用户管理」页）。
function isPlatformUser(name: string): boolean {
  return (data.value?.users ?? []).some((u) => u.name === name)
}

function isChecked(kind: RbacRoleKind, subject: string, roleName: string): boolean {
  return (data.value?.assignments ?? []).some(
    (a) => a.kind === kind && a.subject === subject && a.role === roleName,
  )
}

function setChecked(kind: RbacRoleKind, subject: string, roleName: string, value: boolean): void {
  const state = requireData()
  if (!state) {
    return
  }
  const list = state.assignments
  const idx = list.findIndex((a) => a.kind === kind && a.subject === subject && a.role === roleName)
  if (value && idx < 0) {
    list.push({ role: roleName, kind, subject })
  } else if (!value && idx >= 0) {
    list.splice(idx, 1)
  }
}

// ---- 生效预览：全部来自后端（/security/rbac/effective） ----
// 前端不再自己拼：administer 隐含全部权限、模板继承、Pattern 为空不生效
// 这些规则都在服务端，自己算就会显示成「有角色但 0 个任务」。
function subjectInfo(subject: string) {
  return effective.value?.subjects.find((s) => s.subject === subject)
}

function visibleJobs(subject: string): { known: boolean; all: boolean; names: string[] } {
  const info = subjectInfo(subject)
  if (!info) {
    return { known: false, all: false, names: [] }
  }
  return { known: true, all: info.all_jobs, names: info.visible_jobs ?? [] }
}

function roleMatch(roleName: string) {
  return effective.value?.roles.find((r) => r.name === roleName)
}

// 角色 chip 的悬停提示：Pattern + 覆盖任务数，Pattern 为空要明说不生效。
function roleTip(roleName: string): string {
  const rm = roleMatch(roleName)
  if (!rm) {
    return roleName
  }
  if (rm.kind === 'global') {
    return `全局角色：覆盖全部任务`
  }
  if (!rm.pattern.trim()) {
    return 'Pattern 为空 —— 这个任务角色不会生效（只能作为 Template 被引用）'
  }
  const n = rm.matched_jobs?.length ?? 0
  if (n === 0) {
    return `Pattern: ${rm.pattern}\n没有匹配到任何任务（检查正则是否写错）`
  }
  const names = (rm.matched_jobs ?? []).slice(0, 8).join('、')
  return `Pattern: ${rm.pattern}\n匹配 ${n} 个任务：${names}${n > 8 ? ' …' : ''}`
}

// 有角色却看不到任何任务：几乎总是 Pattern 写错或角色没给 job.read。
function looksBroken(subject: string): boolean {
  const info = subjectInfo(subject)
  if (!info || !info.roles.length) {
    return false
  }
  return !info.all_jobs && (info.visible_jobs ?? []).length === 0
}

function removeSubject(kind: RbacRoleKind, subject: string): void {
  if (builtinSubjects.includes(subject)) {
    ElMessage.warning('内置主体不能删除')
    return
  }
  const state = requireData()
  if (!state) {
    return
  }
  state.assignments = state.assignments.filter((a) => !(a.kind === kind && a.subject === subject))
}

async function save(): Promise<void> {
  saving.value = true
  try {
    // 丢弃 role 为空的占位行（历史数据里可能残留）。
    const list: RbacAssignment[] = (data.value?.assignments ?? []).filter((a) => a.role !== '')
    data.value = await rbacApi.saveAssignments(list)
    await reloadEffective()
    ElMessage.success('授权已保存')
  } catch (e) {
    ElMessage.error(describeError(e))
  } finally {
    saving.value = false
  }
}

onMounted(load)
</script>

<template>
  <div class="page manage">
    <ManagePageHeader
      title="分配角色"
      description="谁 → 哪个角色（角色自身权限在「管理角色」页定义）；一行一个主体，避免几十个角色横铺成表格"
    >
      <template #actions>
        <el-button @click="$router.push({ name: 'system-roles-manage' })">管理角色</el-button>
        <el-button @click="$router.push({ name: 'system-users' })">用户管理</el-button>
        <el-button type="primary" :loading="saving" @click="save">保存</el-button>
      </template>
    </ManagePageHeader>

    <el-alert type="info" :closable="false" show-icon style="margin-bottom: 12px">
      <template #title>
        勾选角色即授权。<b>任务角色</b>只对它的 Pattern 匹配到的任务生效，行尾显示该主体
        <b>实际能看到的任务数</b>（后端计算，与真实接口行为一致）；出现
        <el-tag size="small" type="warning" effect="plain">有角色但看不到任务</el-tag>
        说明角色缺 <span class="mono">job.read</span> 或 Pattern 写错了。
        新增账号请到「用户管理」创建（平台无外部安全域，因此不提供 Jenkins 的「Add Group」）。
      </template>
    </el-alert>

    <div class="filters">
      <el-input v-model="filterSubject" placeholder="按用户/组筛选" clearable style="width: 200px" />
      <el-input v-model="filterRole" placeholder="按角色筛选（任务角色）" clearable style="width: 220px" />
      <span class="filters__hint">
        全局角色 {{ globalRoles.length }} 个 · 任务角色 {{ allItemRoles.length }} 个
        <template v-if="filterRole.trim()">（已筛出 {{ itemRoles.length }} 个）</template>
        · 任务总数 {{ effective?.job_names.length ?? 0 }}
      </span>
    </div>

    <el-card v-loading="loading" shadow="never" class="assign-card">
      <template #header>
        <span class="card-title">全局角色</span>
        <span class="card-hint">权限作用于全部任务</span>
      </template>

      <div v-for="subject in globalSubjects" :key="'g-' + subject" class="subj-row">
        <div class="subj-row__id">
          <span class="mono" :class="{ 'is-builtin': builtinSubjects.includes(subject) }">{{ subject }}</span>
          <el-tag v-if="builtinSubjects.includes(subject)" size="small" type="info" effect="plain">内置</el-tag>
          <el-button
            v-else-if="!isPlatformUser(subject)"
            link
            type="danger"
            size="small"
            @click="removeSubject('global', subject)"
          >移除</el-button>
        </div>
        <div class="subj-row__roles">
          <el-tooltip v-for="role in globalRoles" :key="role.name" :content="roleTip(role.name)" placement="top">
            <el-check-tag
              :checked="isChecked('global', subject, role.name)"
              size="small"
              class="role-chip"
              @change="(v: boolean) => setChecked('global', subject, role.name, v)"
            >{{ role.name }}</el-check-tag>
          </el-tooltip>
          <span v-if="!globalRoles.length" class="muted">还没有全局角色</span>
        </div>
        <div class="subj-row__effect">
          <el-tooltip
            v-if="visibleJobs(subject).all"
            :content="`全部 ${effective?.job_names.length ?? 0} 个任务`"
            placement="top"
          >
            <el-tag size="small" type="success" effect="plain">可见全部任务</el-tag>
          </el-tooltip>
          <el-tooltip
            v-else-if="visibleJobs(subject).names.length"
            :content="visibleJobs(subject).names.join('、')"
            placement="top"
          >
            <el-tag size="small" type="success" effect="plain">
              可见 {{ visibleJobs(subject).names.length }} 个任务
            </el-tag>
          </el-tooltip>
          <el-tooltip v-else-if="looksBroken(subject)" content="有角色但看不到任何任务：角色缺 job.read，或 Pattern 匹配不到现有任务" placement="top">
            <el-tag size="small" type="warning" effect="plain">有角色但看不到任务</el-tag>
          </el-tooltip>
          <span v-else-if="subjectInfo(subject)?.roles.length" class="muted">可见 0 个任务</span>
          <span v-else class="muted">未授权</span>
        </div>
      </div>
    </el-card>

    <el-card v-loading="loading" shadow="never" class="assign-card">
      <template #header>
        <span class="card-title">任务角色</span>
        <span class="card-hint">只对 Pattern 匹配的任务生效；悬停角色名可看覆盖了哪些任务</span>
      </template>

      <div v-for="subject in itemSubjects" :key="'i-' + subject" class="subj-row">
        <div class="subj-row__id">
          <span class="mono" :class="{ 'is-builtin': builtinSubjects.includes(subject) }">{{ subject }}</span>
          <el-tag v-if="builtinSubjects.includes(subject)" size="small" type="info" effect="plain">内置</el-tag>
          <el-button
            v-else-if="!isPlatformUser(subject)"
            link
            type="danger"
            size="small"
            @click="removeSubject('item', subject)"
          >移除</el-button>
        </div>
        <div class="subj-row__roles">
          <el-tooltip v-for="role in itemRoles" :key="role.name" :content="roleTip(role.name)" placement="top">
            <el-check-tag
              :checked="isChecked('item', subject, role.name)"
              size="small"
              class="role-chip"
              @change="(v: boolean) => setChecked('item', subject, role.name, v)"
            >{{ role.name }}</el-check-tag>
          </el-tooltip>
          <span v-if="!itemRoles.length" class="muted">没有匹配的角色（检查角色筛选框）</span>
        </div>
        <div class="subj-row__effect">
          <el-tooltip
            v-if="visibleJobs(subject).names.length"
            :content="visibleJobs(subject).names.join('、')"
            placement="top"
          >
            <el-tag size="small" type="success" effect="plain">
              可见 {{ visibleJobs(subject).names.length }} 个任务
            </el-tag>
          </el-tooltip>
          <el-tooltip v-else-if="looksBroken(subject)" content="有角色但看不到任何任务：角色缺 job.read，或 Pattern 匹配不到现有任务" placement="top">
            <el-tag size="small" type="warning" effect="plain">有角色但看不到任务</el-tag>
          </el-tooltip>
          <span v-else class="muted">可见 0 个任务</span>
        </div>
      </div>
    </el-card>

    <div class="save-bar">
      <el-button type="primary" :loading="saving" @click="save">保存</el-button>
    </div>
  </div>
</template>

<style scoped>
.filters {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-bottom: 12px;
  flex-wrap: wrap;
}

.filters__hint {
  font-size: 12px;
  color: var(--el-text-color-secondary);
}

.assign-card {
  margin-bottom: 16px;
}

.card-title {
  font-weight: 600;
}

.card-hint {
  margin-left: 8px;
  font-size: 12px;
  color: var(--el-text-color-secondary);
}

/* 一行一个主体：名字 + 角色 chip 横排 + 生效预览 */
.subj-row {
  display: flex;
  align-items: flex-start;
  gap: 14px;
  padding: 8px 0;
  border-bottom: 1px dashed var(--el-border-color-lighter);
}

.subj-row:last-of-type {
  border-bottom: none;
}

.subj-row__id {
  display: flex;
  align-items: center;
  gap: 6px;
  width: 200px;
  flex: 0 0 auto;
  padding-top: 2px;
}

.subj-row__id .mono {
  word-break: break-all;
}

.subj-row__id .is-builtin {
  color: var(--el-text-color-secondary);
  font-style: italic;
}

.subj-row__roles {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  flex: 1 1 auto;
}

.subj-row__effect {
  flex: 0 0 auto;
  width: 160px;
  text-align: right;
  padding-top: 2px;
}

.role-chip {
  margin: 0 !important;
}

.muted {
  font-size: 12px;
  color: var(--el-text-color-secondary);
}

.save-bar {
  display: flex;
  justify-content: flex-end;
}
</style>

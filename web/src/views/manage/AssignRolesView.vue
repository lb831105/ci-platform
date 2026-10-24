<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { ElMessage } from 'element-plus'
import ManagePageHeader from '@/components/ManagePageHeader.vue'
import { rbacApi } from '@/api/rbac'
import { describeError } from '@/api/request'
import type { RbacAssignment, RbacData, RbacRoleKind } from '@/types/rbac'

const loading = ref(false)
const saving = ref(false)
// 本页只做「授权」：谁 -> 哪个角色。账号自身的增删改密在「用户管理」页。
const data = ref<RbacData | null>(null)

// 内置主体（对齐 Jenkins）：anonymous 未登录、authenticated 所有已登录用户。
const builtinSubjects = ['anonymous', 'authenticated']

const globalRoles = computed(() =>
  (data.value?.roles ?? []).filter((r) => r.kind === 'global'),
)
const itemRoles = computed(() => (data.value?.roles ?? []).filter((r) => r.kind === 'item'))

// 每类角色的主体行：内置主体 + 平台内用户 + 授权里出现过的其他主体（自由输入的用户/组）。
function subjectsFor(kind: RbacRoleKind): string[] {
  const set = new Set<string>(builtinSubjects)
  ;(data.value?.users ?? []).forEach((u) => set.add(u.name))
  ;(data.value?.assignments ?? [])
    .filter((a) => a.kind === kind)
    .forEach((a) => set.add(a.subject))
  return [...set]
}

const globalSubjects = computed(() => subjectsFor('global'))
const itemSubjects = computed(() => subjectsFor('item'))

const newUserSubject = ref('')
const newItemSubject = ref('')

async function load(): Promise<void> {
  loading.value = true
  try {
    data.value = await rbacApi.get()
  } catch (e) {
    ElMessage.error(describeError(e))
  } finally {
    loading.value = false
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

function addSubject(kind: RbacRoleKind): void {
  const raw = (kind === 'global' ? newUserSubject : newItemSubject).value.trim()
  if (!raw) {
    ElMessage.error('请输入用户/组名称')
    return
  }
  const state = requireData()
  if (!state) {
    return
  }
  const list = kind === 'global' ? globalSubjects : itemSubjects
  if (list.value.includes(raw)) {
    ElMessage.warning(`${raw} 已在列表中`)
  } else {
    // 自由输入的主体（外部用户/组）不落在 users 里，仅参与授权矩阵。
    state.assignments.push({ role: '', kind, subject: raw })
  }
  ;(kind === 'global' ? newUserSubject : newItemSubject).value = ''
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
    // 丢弃 role 为空的占位行（仅用于把自由输入主体带进矩阵）。
    const list: RbacAssignment[] = (data.value?.assignments ?? []).filter((a) => a.role !== '')
    data.value = await rbacApi.saveAssignments(list)
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
    <ManagePageHeader title="Assign Roles" description="把用户/组授权到全局角色与任务角色（Manage and Assign Roles）" />

    <el-alert
      type="info"
      :closable="false"
      show-icon
      title="任务角色的授权按任务名 Pattern 生效：勾选后用户只能对该角色 Pattern 匹配到的任务执行对应操作。anonymous 为未登录主体（仅认证关闭时有意义），authenticated 为所有已登录用户。"
      style="margin-bottom: 12px"
    />

    <el-card v-loading="loading" shadow="never" class="assign-card">
      <template #header>
        <div class="card-header">
          <span>Global roles</span>
          <router-link :to="{ name: 'system-users' }" class="users-entry">
            <el-button size="small">管理用户</el-button>
          </router-link>
        </div>
      </template>
      <el-table :data="globalSubjects" size="small">
        <el-table-column label="User/Group" width="200" fixed>
          <template #default="{ row }">
            <div class="subject-cell">
              <span class="mono" :class="{ 'is-builtin': builtinSubjects.includes(row) }">{{ row }}</span>
              <el-button
                v-if="!builtinSubjects.includes(row) && !isPlatformUser(row)"
                link
                type="danger"
                size="small"
                @click="removeSubject('global', row)"
              >移除</el-button>
            </div>
          </template>
        </el-table-column>
        <el-table-column
          v-for="role in globalRoles"
          :key="role.name"
          :label="role.name"
          min-width="110"
          align="center"
        >
          <template #default="{ row }">
            <el-checkbox
              :model-value="isChecked('global', row, role.name)"
              @update:model-value="(v: boolean) => setChecked('global', row, role.name, v)"
            />
          </template>
        </el-table-column>
      </el-table>
      <div class="add-row">
        <span class="add-row__label">Add User/Group</span>
        <el-input
          v-model="newUserSubject"
          placeholder="用户或组名称"
          style="width: 220px"
          @keyup.enter="addSubject('global')"
        />
        <el-button @click="addSubject('global')">Add</el-button>
      </div>
    </el-card>

    <el-card v-loading="loading" shadow="never" class="assign-card">
      <template #header>Item roles</template>
      <el-table :data="itemSubjects" size="small">
        <el-table-column label="User/Group" width="200" fixed>
          <template #default="{ row }">
            <div class="subject-cell">
              <span class="mono" :class="{ 'is-builtin': builtinSubjects.includes(row) }">{{ row }}</span>
              <el-button
                v-if="!builtinSubjects.includes(row) && !isPlatformUser(row)"
                link
                type="danger"
                size="small"
                @click="removeSubject('item', row)"
              >移除</el-button>
            </div>
          </template>
        </el-table-column>
        <el-table-column
          v-for="role in itemRoles"
          :key="role.name"
          :label="role.name"
          min-width="110"
          align="center"
        >
          <template #default="{ row }">
            <el-checkbox
              :model-value="isChecked('item', row, role.name)"
              @update:model-value="(v: boolean) => setChecked('item', row, role.name, v)"
            />
          </template>
        </el-table-column>
      </el-table>
      <div class="add-row">
        <span class="add-row__label">Add User/Group</span>
        <el-input
          v-model="newItemSubject"
          placeholder="用户或组名称"
          style="width: 220px"
          @keyup.enter="addSubject('item')"
        />
        <el-button @click="addSubject('item')">Add</el-button>
      </div>
    </el-card>

    <el-card shadow="never" class="assign-card is-unsupported">
      <template #header>
        Agent roles
        <el-tag size="small" type="info" effect="plain" style="margin-left: 8px">平台不支持</el-tag>
      </template>
      <div class="muted">平台为单机本地执行，无代理节点；代理角色授权在引入多节点执行后启用。</div>
    </el-card>

    <div class="save-bar">
      <el-button type="primary" :loading="saving" @click="save">Save</el-button>
    </div>

  </div>
</template>

<style scoped>
.assign-card {
  margin-bottom: 16px;
}

.assign-card.is-unsupported {
  opacity: 0.75;
}

.users-entry {
  text-decoration: none;
}

.card-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
}

.subject-cell {
  display: flex;
  align-items: center;
  gap: 8px;
}

.subject-cell .is-builtin {
  color: var(--el-text-color-secondary);
  font-style: italic;
}

.add-row {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-top: 12px;
}

.add-row__label {
  color: var(--el-text-color-secondary);
  font-size: 13px;
  white-space: nowrap;
}

.save-bar {
  display: flex;
  justify-content: flex-end;
}
</style>

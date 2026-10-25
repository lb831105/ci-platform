<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import ManagePageHeader from '@/components/ManagePageHeader.vue'
import { rbacApi } from '@/api/rbac'
import { pipelineApi } from '@/api/pipeline'
import { describeError } from '@/api/request'
import type { RbacData, RbacPermissionGroup, RbacRole } from '@/types/rbac'

const loading = ref(false)
const saving = ref(false)
const data = ref<RbacData | null>(null)
const jobNames = ref<string[]>([])
const newGlobalRole = ref('')
const newItemRole = ref('')

// 权限矩阵分类：只渲染 supported 的组，其余收进一行说明（原来每列都占宽度）。
const groups = computed(() => (data.value?.permission_groups ?? []).filter((g) => g.supported))
const unsupportedGroups = computed(() => (data.value?.permission_groups ?? []).filter((g) => !g.supported))
const globalRoles = computed(() => (data.value?.roles ?? []).filter((r) => r.kind === 'global'))
const itemRoles = computed(() => (data.value?.roles ?? []).filter((r) => r.kind === 'item'))
// 模板候选：无 Pattern 的任务角色（对齐 Jenkins Permission Templates）。
const templateCandidates = computed(() => itemRoles.value.filter((r) => !r.pattern))

async function load(): Promise<void> {
  loading.value = true
  try {
    data.value = await rbacApi.get()
    // 任务名用于实时预览 Pattern 到底匹配了哪些任务（让角色和任务真正挂钩可见）。
    try {
      jobNames.value = (await pipelineApi.list()).map((p) => p.name).sort()
    } catch {
      jobNames.value = []
    }
  } catch (e) {
    ElMessage.error(describeError(e))
  } finally {
    loading.value = false
  }
}

// matchJobs 用与后端相同的规则（忽略大小写、完全匹配）预览匹配的任务。
// 非法正则在保存时后端会报错，这里只返回空并标红输入框。
function matchJobs(pattern?: string): string[] {
  const raw = (pattern ?? '').trim()
  if (!raw) {
    return []
  }
  try {
    const re = new RegExp(`^(?:${raw})$`, 'i')
    return jobNames.value.filter((n) => re.test(n))
  } catch {
    return []
  }
}

function patternInvalid(pattern?: string): boolean {
  const raw = (pattern ?? '').trim()
  if (!raw) {
    return false
  }
  try {
    new RegExp(`^(?:${raw})$`, 'i')
    return false
  } catch {
    return true
  }
}

function has(role: RbacRole, perm: string): boolean {
  return role.permissions.includes(perm)
}

function toggle(role: RbacRole, perm: string, value: boolean): void {
  if (value) {
    if (!role.permissions.includes(perm)) {
      role.permissions.push(perm)
    }
  } else {
    role.permissions = role.permissions.filter((p) => p !== perm)
  }
}

// 整组开关：矩阵一行里点分类名就能给该分类全选/全清，省掉一个个点。
function groupAllChecked(role: RbacRole, group: RbacPermissionGroup): boolean {
  const items = group.permissions ?? []
  return items.length > 0 && items.every((i) => has(role, i.id))
}

function toggleGroup(role: RbacRole, group: RbacPermissionGroup): void {
  const items = group.permissions ?? []
  const on = !groupAllChecked(role, group)
  items.forEach((i) => toggle(role, i.id, on))
}

function addGlobalRole(): void {
  const name = newGlobalRole.value.trim()
  if (!name) {
    ElMessage.error('请输入角色名称')
    return
  }
  if (globalRoles.value.some((r) => r.name === name)) {
    ElMessage.error(`全局角色 ${name} 已存在`)
    return
  }
  // 直接写入 data.value.roles：globalRoles 是 computed 的 filter 结果（每次访问
  // 都是新数组），push 到它上面不会影响状态，角色会被静默丢弃。
  if (!data.value) {
    ElMessage.error('角色数据未加载，请刷新后重试')
    return
  }
  data.value.roles.push({ name, kind: 'global', permissions: [] })
  newGlobalRole.value = ''
}

function addItemRole(): void {
  const name = newItemRole.value.trim()
  if (!name) {
    ElMessage.error('请输入角色名称')
    return
  }
  if (itemRoles.value.some((r) => r.name === name)) {
    ElMessage.error(`任务角色 ${name} 已存在`)
    return
  }
  if (!data.value) {
    ElMessage.error('角色数据未加载，请刷新后重试')
    return
  }
  data.value.roles.push({ name, kind: 'item', pattern: '', permissions: [] })
  newItemRole.value = ''
}

async function removeRole(role: RbacRole): Promise<void> {
  try {
    await ElMessageBox.confirm(
      `删除角色「${role.name}」会同时移除该角色的全部授权。确认删除？`,
      '删除角色',
      { type: 'warning', confirmButtonText: '确认删除', cancelButtonText: '取消' },
    )
  } catch {
    return
  }
  if (!data.value) {
    return
  }
  if (role.kind === 'global') {
    data.value.roles = data.value.roles.filter((r) => !(r.kind === 'global' && r.name === role.name))
  } else {
    data.value.roles = data.value.roles.filter((r) => !(r.kind === 'item' && r.name === role.name))
    // 引用了被删模板的角色解除引用。
    data.value.roles.forEach((r) => {
      if (r.template === role.name) {
        r.template = undefined
      }
    })
  }
}

async function save(): Promise<void> {
  const bad = itemRoles.value.find((r) => patternInvalid(r.pattern))
  if (bad) {
    ElMessage.error(`任务角色「${bad.name}」的 Pattern 不是合法正则`)
    return
  }
  saving.value = true
  try {
    data.value = await rbacApi.saveRoles([...globalRoles.value, ...itemRoles.value])
    ElMessage.success('角色已保存')
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
      title="管理角色 / 分配角色"
      description="定义全局角色与任务角色及其权限；谁获得哪个角色在「分配角色」页设置"
    >
      <template #actions>
        <el-button @click="$router.push({ name: 'system-roles-assign' })">去分配角色</el-button>
        <el-button type="primary" :loading="saving" @click="save">保存</el-button>
      </template>
    </ManagePageHeader>

    <el-alert
      type="info"
      :closable="false"
      show-icon
      style="margin-bottom: 12px"
    >
      <template #title>
        <b>全局角色</b>的权限作用于全部任务；<b>任务角色</b>用 Pattern（任务名正则，忽略大小写、完全匹配）
        限定作用范围 —— 只被授予任务角色的用户，在任务列表里<b>只会看到匹配到的任务</b>，
        其它任务既看不到也打不开。<b>Template</b> 可复用无 Pattern 的模板角色的权限。
      </template>
    </el-alert>

    <!-- 全局角色：一行一个角色，权限用紧凑 chip 横排 -->
    <el-card v-loading="loading" shadow="never" class="roles-card">
      <template #header>
        <span class="card-title">全局角色</span>
        <span class="card-hint">作用于平台全部任务</span>
      </template>

      <div v-for="role in globalRoles" :key="'g-' + role.name" class="role-row">
        <div class="role-row__id">
          <span class="mono role-row__name">{{ role.name }}</span>
          <el-button link type="danger" size="small" @click="removeRole(role)">删除</el-button>
        </div>
        <div class="role-row__perms">
          <div v-for="group in groups" :key="group.id" class="perm-group">
            <span
              class="perm-group__label"
              :class="{ 'is-on': groupAllChecked(role, group) }"
              :title="`点击整组选中/取消：${group.title}`"
              @click="toggleGroup(role, group)"
            >{{ group.title }}</span>
            <el-check-tag
              v-for="item in group.permissions"
              :key="item.id"
              :checked="has(role, item.id)"
              size="small"
              class="perm-chip"
              @change="(v: boolean) => toggle(role, item.id, v)"
            >{{ item.title }}</el-check-tag>
          </div>
        </div>
      </div>
      <el-empty v-if="!globalRoles.length" description="还没有全局角色" :image-size="60" />

      <div class="add-row">
        <span class="add-row__label">新增全局角色</span>
        <el-input
          v-model="newGlobalRole"
          placeholder="如 deploy-ops"
          style="width: 200px"
          @keyup.enter="addGlobalRole"
        />
        <el-button @click="addGlobalRole">添加</el-button>
      </div>
    </el-card>

    <!-- 任务角色：一行一个角色，Pattern 旁边直接显示匹配到的任务 -->
    <el-card v-loading="loading" shadow="never" class="roles-card">
      <template #header>
        <span class="card-title">任务角色</span>
        <span class="card-hint">只对 Pattern 匹配的任务生效</span>
      </template>

      <div v-for="role in itemRoles" :key="'i-' + role.name" class="role-row">
        <div class="role-row__id">
          <span class="mono role-row__name">{{ role.name }}</span>
          <el-button link type="danger" size="small" @click="removeRole(role)">删除</el-button>
        </div>
        <div class="role-row__target">
          <el-input
            v-model="role.pattern"
            placeholder="任务名正则，如 as-doc-.+"
            class="mono role-row__pattern"
            :class="{ 'is-invalid': patternInvalid(role.pattern) }"
          >
            <template #prepend>Pattern</template>
          </el-input>
          <span v-if="patternInvalid(role.pattern)" class="role-row__match is-bad">正则无效</span>
          <span v-else-if="matchJobs(role.pattern).length" class="role-row__match">
            匹配 {{ matchJobs(role.pattern).length }} 个任务：{{ matchJobs(role.pattern).slice(0, 5).join('、') }}
            <template v-if="matchJobs(role.pattern).length > 5">…</template>
          </span>
          <span v-else-if="(role.pattern ?? '').trim()" class="role-row__match is-warn">
            没有匹配的任务（这个角色当前不会生效）
          </span>
          <el-select
            v-model="role.template"
            clearable
            placeholder="Template（可选）"
            class="role-row__template"
          >
            <el-option
              v-for="tpl in templateCandidates.filter((t) => t.name !== role.name)"
              :key="tpl.name"
              :label="tpl.name"
              :value="tpl.name"
            />
          </el-select>
        </div>
        <div class="role-row__perms">
          <div v-for="group in groups" :key="group.id" class="perm-group">
            <span
              class="perm-group__label"
              :class="{ 'is-on': groupAllChecked(role, group) }"
              :title="`点击整组选中/取消：${group.title}`"
              @click="toggleGroup(role, group)"
            >{{ group.title }}</span>
            <el-check-tag
              v-for="item in group.permissions"
              :key="item.id"
              :checked="has(role, item.id)"
              size="small"
              class="perm-chip"
              @change="(v: boolean) => toggle(role, item.id, v)"
            >{{ item.title }}</el-check-tag>
          </div>
        </div>
      </div>
      <el-empty v-if="!itemRoles.length" description="还没有任务角色" :image-size="60" />

      <div class="add-row">
        <span class="add-row__label">新增任务角色</span>
        <el-input
          v-model="newItemRole"
          placeholder="角色名，如 as-doc-dev"
          style="width: 200px"
          @keyup.enter="addItemRole"
        />
        <el-button @click="addItemRole">添加</el-button>
      </div>
    </el-card>

    <div v-if="unsupportedGroups.length" class="unsupported">
      平台不支持（已隐藏，避免误以为已生效）：
      <el-tooltip
        v-for="g in unsupportedGroups"
        :key="g.id"
        :content="g.hint"
        placement="top"
      >
        <span class="unsupported__item">{{ g.title }}</span>
      </el-tooltip>
    </div>
  </div>
</template>

<style scoped>
.roles-card {
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

/* 一行一个角色：左名字 + 右权限 chip 横排，避免每列纵向堆一排复选框 */
.role-row {
  display: flex;
  align-items: flex-start;
  gap: 14px;
  padding: 10px 0;
  border-bottom: 1px dashed var(--el-border-color-lighter);
}

.role-row:last-of-type {
  border-bottom: none;
}

.role-row__id {
  display: flex;
  flex-direction: column;
  gap: 2px;
  width: 150px;
  flex: 0 0 auto;
  padding-top: 2px;
}

.role-row__name {
  font-weight: 600;
  word-break: break-all;
}

.role-row__target {
  display: flex;
  flex-direction: column;
  gap: 4px;
  width: 240px;
  flex: 0 0 auto;
}

.role-row__pattern.is-invalid :deep(.el-input__wrapper) {
  box-shadow: 0 0 0 1px var(--el-color-danger) inset;
}

.role-row__match {
  font-size: 12px;
  color: var(--el-color-success);
  line-height: 1.5;
}

.role-row__match.is-warn {
  color: var(--el-color-warning);
}

.role-row__match.is-bad {
  color: var(--el-color-danger);
}

.role-row__template {
  width: 100%;
}

.role-row__perms {
  display: flex;
  flex-wrap: wrap;
  gap: 6px 16px;
  flex: 1 1 auto;
}

.perm-group {
  display: inline-flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 4px;
}

.perm-group__label {
  font-size: 12px;
  color: var(--el-text-color-secondary);
  cursor: pointer;
  user-select: none;
  padding: 0 2px;
  border-radius: 3px;
}

.perm-group__label:hover {
  background: var(--el-fill-color-light);
  color: var(--el-color-primary);
}

.perm-group__label.is-on {
  color: var(--el-color-primary);
  font-weight: 600;
}

.perm-chip {
  margin: 0 !important;
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

.unsupported {
  font-size: 12px;
  color: var(--el-text-color-secondary);
}

.unsupported__item {
  margin-left: 8px;
  padding: 0 6px;
  border: 1px dashed var(--el-border-color);
  border-radius: 4px;
  cursor: help;
}
</style>

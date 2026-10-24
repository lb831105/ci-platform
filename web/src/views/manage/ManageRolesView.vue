<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import ManagePageHeader from '@/components/ManagePageHeader.vue'
import { rbacApi } from '@/api/rbac'
import { describeError } from '@/api/request'
import type { RbacData, RbacRole } from '@/types/rbac'

const loading = ref(false)
const saving = ref(false)
const data = ref<RbacData | null>(null)
const newGlobalRole = ref('')
const newItemRole = ref('')
const newItemPattern = ref('')
const newItemTemplate = ref('')

// 权限矩阵分类（含平台不支持的禁用列）。
const groups = computed(() => data.value?.permission_groups ?? [])
const globalRoles = computed(() =>
  (data.value?.roles ?? []).filter((r) => r.kind === 'global'),
)
const itemRoles = computed(() => (data.value?.roles ?? []).filter((r) => r.kind === 'item'))
// 模板候选：无 Pattern 的任务角色（对齐 Jenkins Permission Templates）。
const templateCandidates = computed(() => itemRoles.value.filter((r) => !r.pattern))

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
  data.value.roles.push({
    name,
    kind: 'item',
    pattern: newItemPattern.value.trim(),
    template: newItemTemplate.value || undefined,
    permissions: [],
  })
  newItemRole.value = ''
  newItemPattern.value = ''
  newItemTemplate.value = ''
}

async function removeRole(role: RbacRole): Promise<void> {
  try {
    await ElMessageBox.confirm(`删除角色「${role.name}」会同时移除该角色的全部授权。确认删除？`, '删除角色', {
      type: 'warning',
      confirmButtonText: '确认删除',
      cancelButtonText: '取消',
    })
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
    <ManagePageHeader title="Manage Roles" description="定义全局角色与任务角色及其权限（Manage and Assign Roles）" />

    <el-alert
      type="info"
      :closable="false"
      show-icon
      title="任务角色通过 Pattern（任务名正则，忽略大小写、完全匹配）授权一批任务；Template 选择无 Pattern 的模板角色可复用权限配置。"
      style="margin-bottom: 12px"
    />

    <el-card v-loading="loading" shadow="never" class="roles-card">
      <template #header>Global roles</template>
      <el-table :data="globalRoles" stripe class="roles-table">
        <el-table-column label="Role" width="180" fixed>
          <template #default="{ row }">
            <span class="mono">{{ row.name }}</span>
          </template>
        </el-table-column>
        <el-table-column
          v-for="group in groups"
          :key="group.id"
          :label="group.title"
          :min-width="group.supported ? 110 : 90"
          align="center"
        >
          <template #header>
            <el-tooltip v-if="!group.supported" :content="group.hint" placement="top">
              <span class="group-title is-disabled">{{ group.title }}</span>
            </el-tooltip>
            <span v-else class="group-title">{{ group.title }}</span>
          </template>
          <template #default="{ row }">
            <div class="perm-cell">
              <template v-if="group.supported">
                <el-checkbox
                  v-for="item in group.permissions"
                  :key="item.id"
                  :model-value="has(row, item.id)"
                  @update:model-value="(v: boolean) => toggle(row, item.id, v)"
                >
                  <span class="perm-label">{{ item.title }}</span>
                </el-checkbox>
              </template>
              <template v-else>
                <span class="perm-unsupported">—</span>
              </template>
            </div>
          </template>
        </el-table-column>
        <el-table-column label="操作" width="80" align="right" fixed="right">
          <template #default="{ row }">
            <el-button size="small" type="danger" plain @click="removeRole(row)">删除</el-button>
          </template>
        </el-table-column>
      </el-table>
      <div class="add-row">
        <span class="add-row__label">Role to add</span>
        <el-input v-model="newGlobalRole" placeholder="如 deploy-ops" style="width: 220px" @keyup.enter="addGlobalRole" />
        <el-button @click="addGlobalRole">Add</el-button>
      </div>
    </el-card>

    <el-card v-loading="loading" shadow="never" class="roles-card">
      <template #header>Item roles</template>
      <el-table :data="itemRoles" stripe class="roles-table">
        <el-table-column label="Role" width="180" fixed>
          <template #default="{ row }">
            <span class="mono">{{ row.name }}</span>
          </template>
        </el-table-column>
        <el-table-column label="Pattern" width="180">
          <template #default="{ row }">
            <el-input v-model="row.pattern" class="mono" placeholder="如 as-doc.*" />
          </template>
        </el-table-column>
        <el-table-column label="Template" width="160">
          <template #default="{ row }">
            <el-select v-model="row.template" clearable placeholder="无" style="width: 100%">
              <el-option
                v-for="tpl in templateCandidates.filter((t) => t.name !== row.name)"
                :key="tpl.name"
                :label="tpl.name"
                :value="tpl.name"
              />
            </el-select>
          </template>
        </el-table-column>
        <el-table-column
          v-for="group in groups"
          :key="group.id"
          :label="group.title"
          :min-width="group.supported ? 110 : 90"
          align="center"
        >
          <template #header>
            <el-tooltip v-if="!group.supported" :content="group.hint" placement="top">
              <span class="group-title is-disabled">{{ group.title }}</span>
            </el-tooltip>
            <span v-else class="group-title">{{ group.title }}</span>
          </template>
          <template #default="{ row }">
            <div class="perm-cell">
              <template v-if="group.supported">
                <el-checkbox
                  v-for="item in group.permissions"
                  :key="item.id"
                  :model-value="has(row, item.id)"
                  @update:model-value="(v: boolean) => toggle(row, item.id, v)"
                >
                  <span class="perm-label">{{ item.title }}</span>
                </el-checkbox>
              </template>
              <template v-else>
                <span class="perm-unsupported">—</span>
              </template>
            </div>
          </template>
        </el-table-column>
        <el-table-column label="操作" width="80" align="right" fixed="right">
          <template #default="{ row }">
            <el-button size="small" type="danger" plain @click="removeRole(row)">删除</el-button>
          </template>
        </el-table-column>
      </el-table>
      <div class="add-row">
        <span class="add-row__label">Role to add</span>
        <el-input v-model="newItemRole" placeholder="角色名" style="width: 160px" />
        <el-input v-model="newItemPattern" placeholder="Pattern（任务名正则）" class="mono" style="width: 220px" />
        <el-button @click="addItemRole">Add</el-button>
      </div>
    </el-card>

    <el-card shadow="never" class="roles-card is-unsupported">
      <template #header>
        Agent roles
        <el-tooltip content="平台为单机本地执行，无代理节点，角色不生效" placement="top">
          <el-tag size="small" type="info" effect="plain" style="margin-left: 8px">平台不支持</el-tag>
        </el-tooltip>
      </template>
      <div class="muted">
        本平台直接在服务器本地执行构建（无 Jenkins Agent / 云节点），因此代理角色没有可授权的对象。
        如后续引入多节点执行，将在此处启用代理权限矩阵（Build / Configure / Connect / Delete / Disconnect）。
      </div>
    </el-card>

    <div class="save-bar">
      <el-button type="primary" :loading="saving" @click="save">Save</el-button>
    </div>
  </div>
</template>

<style scoped>
.roles-card {
  margin-bottom: 16px;
}

.roles-card.is-unsupported {
  opacity: 0.75;
}

.roles-table {
  width: 100%;
}

.group-title.is-disabled {
  color: var(--el-text-color-disabled);
  cursor: not-allowed;
}

.perm-cell {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 2px;
}

.perm-label {
  font-size: 12px;
}

.perm-unsupported {
  color: var(--el-text-color-disabled);
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

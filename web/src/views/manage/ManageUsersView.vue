<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import ManagePageHeader from '@/components/ManagePageHeader.vue'
import { rbacApi } from '@/api/rbac'
import { describeError } from '@/api/request'
import { formatTime } from '@/utils/format'
import type { UserAccount } from '@/types/rbac'

// 系统管理 → 用户管理：对齐 Jenkins 的 Manage Users。
// 与「Assign Roles」分开：这里是账号本身（增删改密/联系方式），那里是授权矩阵。
const loading = ref(false)
const users = ref<UserAccount[]>([])

const builtinCount = computed(() => users.value.filter((u) => u.builtin).length)
// 历史用户（旧版本允许邮箱留空）：提示补全，否则「选了人却收不到邮件」无从排查。
const missingEmail = computed(() =>
  users.value.filter((u) => !u.builtin && !u.email).map((u) => u.name),
)
const managedCount = computed(() => users.value.length - builtinCount.value)

// isEmail 与后端保持同一最小校验（单一 @、至少两段域名）。
function isEmail(value: string): boolean {
  const parts = value.split('@')
  if (parts.length !== 2) return false
  const [local, host] = parts
  if (!local || !host) return false
  const labels = host.split('.')
  return labels.length >= 2 && labels.every((l) => l.length > 0)
}

// 姓名只用于展示，用户名仍是登录标识；要求汉字，避免出现 libin / li bin 这类混杂称呼。
// 少数民族姓名允许「·」分隔（如：阿依古丽·买买提）。
function isChineseName(value: string): boolean {
  const name = value.trim()
  if (!/^\p{Script=Han}+(?:·\p{Script=Han}+)*$/u.test(name)) return false
  const length = Array.from(name).length
  return length >= 2 && length <= 20
}

async function load(): Promise<void> {
  loading.value = true
  try {
    users.value = await rbacApi.users()
  } catch (e) {
    ElMessage.error(describeError(e))
  } finally {
    loading.value = false
  }
}

onMounted(load)

// ---- 新建用户 ----
const createVisible = ref(false)
const creating = ref(false)
const createForm = ref({ name: '', display_name: '', password: '', email: '' })

function openCreate(): void {
  createForm.value = { name: '', display_name: '', password: '', email: '' }
  createVisible.value = true
}

async function submitCreate(): Promise<void> {
  const name = createForm.value.name.trim()
  if (!name) {
    ElMessage.error('请输入用户名')
    return
  }
  if (createForm.value.password.length < 8) {
    ElMessage.error('密码至少需要 8 个字符')
    return
  }
  const displayName = createForm.value.display_name.trim()
  if (!isChineseName(displayName)) {
    ElMessage.error('请输入 2-20 个汉字的姓名（少数民族姓名可用「·」分隔）')
    return
  }
  const email = createForm.value.email.trim()
  if (!isEmail(email)) {
    ElMessage.error('请输入有效的邮箱：构建通知会用该邮箱发信')
    return
  }
  creating.value = true
  try {
    await rbacApi.addUser(name, createForm.value.password, email, displayName)
    createVisible.value = false
    ElMessage.success(`用户 ${name} 已创建，请在「Assign Roles」为其分配角色`)
    await load()
  } catch (e) {
    ElMessage.error(describeError(e))
  } finally {
    creating.value = false
  }
}

// ---- 联系方式 ----
// 任务通知按用户名引用人员时，用邮箱发信、用企微 ID/手机号 @ 提醒。
const contactVisible = ref(false)
const contactSaving = ref(false)
const contactForm = ref({ name: '', display_name: '', email: '', wecom_id: '', phone: '' })

function openContact(user: UserAccount): void {
  contactForm.value = {
    name: user.name,
    display_name: user.display_name ?? '',
    email: user.email ?? '',
    wecom_id: user.wecom_id ?? '',
    phone: user.phone ?? '',
  }
  contactVisible.value = true
}

async function submitContact(): Promise<void> {
  const name = contactForm.value.name
  const displayName = contactForm.value.display_name.trim()
  if (!isChineseName(displayName)) {
    ElMessage.error('请输入 2-20 个汉字的姓名（少数民族姓名可用「·」分隔）')
    return
  }
  const email = contactForm.value.email.trim()
  if (!isEmail(email)) {
    ElMessage.error('邮箱不能为空且必须有效：构建通知会用该邮箱发信')
    return
  }
  contactSaving.value = true
  try {
    await rbacApi.setUserContact({
      name,
      display_name: displayName,
      email,
      wecom_id: contactForm.value.wecom_id.trim(),
      phone: contactForm.value.phone.trim(),
    })
    contactVisible.value = false
    ElMessage.success(`已更新 ${name} 的姓名与联系方式`)
    await load()
  } catch (e) {
    ElMessage.error(describeError(e))
  } finally {
    contactSaving.value = false
  }
}

// ---- 重置密码 ----
const resetVisible = ref(false)
const resetting = ref(false)
const resetForm = ref({ name: '', password: '' })

function openReset(user: UserAccount): void {
  resetForm.value = { name: user.name, password: '' }
  resetVisible.value = true
}

async function submitReset(): Promise<void> {
  const { name, password } = resetForm.value
  if (password.length < 8) {
    ElMessage.error('密码至少需要 8 个字符')
    return
  }
  resetting.value = true
  try {
    await rbacApi.resetUserPassword(name, password)
    resetVisible.value = false
    ElMessage.success(`用户 ${name} 的密码已重置，其登录会话已撤销`)
  } catch (e) {
    ElMessage.error(describeError(e))
  } finally {
    resetting.value = false
  }
}

// ---- 删除用户 ----
async function remove(user: UserAccount): Promise<void> {
  try {
    await ElMessageBox.confirm(
      `删除用户「${user.name}」会同时移除其全部授权与登录会话。确认删除？`,
      '删除用户',
      { type: 'warning', confirmButtonText: '确认删除', cancelButtonText: '取消' },
    )
  } catch {
    return
  }
  try {
    await rbacApi.deleteUser(user.name)
    ElMessage.success('用户已删除')
    await load()
  } catch (e) {
    ElMessage.error(describeError(e))
  }
}
</script>

<template>
  <div class="page manage">
    <ManagePageHeader
      title="用户管理"
      description="管理系统内的用户账号：新建、删除、重置密码与通知联系方式（保存在 data/rbac.json）"
    >
      <template #actions>
        <el-button :loading="loading" @click="load()">刷新</el-button>
        <el-button type="primary" @click="openCreate">新建用户</el-button>
      </template>
    </ManagePageHeader>

    <el-alert
      type="info"
      :closable="false"
      show-icon
      style="margin-bottom: 12px"
      title="用户只代表「能登录平台的账号」，能做什么由角色决定。新建用户后请到「Assign Roles」为其勾选全局/任务角色，否则登录后看不到任何任务；联系方式也可由用户在「个人资料」页自助维护并验证邮箱。"
    />

    <el-alert
      v-if="missingEmail.length"
      type="warning"
      :closable="false"
      show-icon
      style="margin-bottom: 12px"
      :title="`有 ${missingEmail.length} 个用户未配置邮箱（${missingEmail.join('、')}），构建通知的邮件无法送达，请逐个补全。`"
    />

    <el-card shadow="never" v-loading="loading">
      <template #header>
        <div class="users-header">
          <span>
            共 {{ users.length }} 个账号（内置 {{ builtinCount }} / 平台用户 {{ managedCount }}）
          </span>
          <router-link class="users-link" :to="{ name: 'system-roles-assign' }">
            去分配角色 →
          </router-link>
        </div>
      </template>

      <el-table :data="users" stripe size="small" empty-text="暂无用户">
        <el-table-column label="用户名" width="200">
          <template #default="{ row }">
            <div class="user-cell">
              <span class="mono">{{ row.name }}</span>
              <el-tag v-if="row.builtin" size="small" type="warning" disable-transitions>内置管理员</el-tag>
            </div>
          </template>
        </el-table-column>

        <el-table-column label="姓名" width="120">
          <template #default="{ row }">
            <span :class="row.display_name ? '' : 'muted'">{{ row.display_name || '-' }}</span>
          </template>
        </el-table-column>

        <el-table-column label="角色" min-width="240">
          <template #default="{ row }">
            <el-tag
              v-if="row.builtin && row.roles.length === 0"
              size="small"
              type="danger"
              disable-transitions
            >
              全部权限
            </el-tag>
            <template v-else>
              <el-tag
                v-for="r in row.roles"
                :key="r.kind + '-' + r.role"
                size="small"
                :type="r.kind === 'global' ? 'primary' : 'success'"
                disable-transitions
                style="margin-right: 4px"
              >
                {{ r.role }}<span v-if="r.pattern" class="muted"> ({{ r.pattern }})</span>
              </el-tag>
              <span v-if="row.roles.length === 0" class="muted">未分配</span>
            </template>
          </template>
        </el-table-column>

        <el-table-column label="邮箱" min-width="220">
          <template #default="{ row }">
            <div class="user-cell">
              <span class="mono" :class="row.email ? 'muted' : 'danger-text'">{{ row.email || '未配置' }}</span>
              <el-tag v-if="row.email && row.email_verified" size="small" type="success" disable-transitions>
                已验证
              </el-tag>
              <el-tag v-else-if="row.email" size="small" type="info" disable-transitions>未验证</el-tag>
              <el-tag v-else size="small" type="danger" disable-transitions>缺邮箱</el-tag>
            </div>
          </template>
        </el-table-column>

        <el-table-column label="企业微信 ID" width="150">
          <template #default="{ row }">
            <span class="mono muted">{{ row.wecom_id || '-' }}</span>
          </template>
        </el-table-column>

        <el-table-column label="手机号" width="140">
          <template #default="{ row }">
            <span class="mono muted">{{ row.phone || '-' }}</span>
          </template>
        </el-table-column>

        <el-table-column label="创建时间" width="170">
          <template #default="{ row }">
            <span class="mono muted">{{ row.builtin ? '系统配置' : formatTime(row.created_at) }}</span>
          </template>
        </el-table-column>

        <el-table-column label="操作" width="230" fixed="right">
          <template #default="{ row }">
            <el-button
              v-if="!row.builtin"
              link
              type="primary"
              size="small"
              @click="openContact(row)"
            >姓名/联系方式</el-button>
            <el-button
              v-if="!row.builtin"
              link
              type="primary"
              size="small"
              @click="openReset(row)"
            >重置密码</el-button>
            <el-button
              v-if="!row.builtin"
              link
              type="danger"
              size="small"
              @click="remove(row)"
            >删除</el-button>
            <span v-if="row.builtin" class="muted">
              在「全局安全配置」中修改
            </span>
          </template>
        </el-table-column>
      </el-table>
    </el-card>

    <el-dialog v-model="createVisible" title="新建用户" width="440px">
      <el-form label-width="80px">
        <el-form-item label="用户名">
          <el-input v-model="createForm.name" placeholder="如 dev1" />
        </el-form-item>
        <el-form-item label="姓名">
          <el-input v-model="createForm.display_name" placeholder="如：张三（仅汉字，2-20 字）" />
        </el-form-item>
        <el-form-item label="邮箱">
          <el-input v-model="createForm.email" class="mono" placeholder="name@example.com（必填，用于构建通知）" />
        </el-form-item>
        <el-form-item label="密码">
          <el-input
            v-model="createForm.password"
            type="password"
            show-password
            placeholder="至少 8 个字符"
          />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="createVisible = false">取消</el-button>
        <el-button type="primary" :loading="creating" @click="submitCreate">创建</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="contactVisible" title="姓名与联系方式" width="480px">
      <el-alert
        type="info"
        :closable="false"
        show-icon
        style="margin-bottom: 12px"
        title="任务通知会按用户名引用人员：姓名用于界面展示，邮箱用于发信，企业微信 ID / 手机号用于群机器人 @ 提醒。"
      />
      <el-form label-width="100px">
        <el-form-item label="用户名">
          <el-input :model-value="contactForm.name" disabled />
        </el-form-item>
        <el-form-item label="姓名" required>
          <el-input v-model="contactForm.display_name" placeholder="如：张三（仅汉字，2-20 字）" />
        </el-form-item>
        <el-form-item label="邮箱" required>
          <el-input v-model="contactForm.email" class="mono" placeholder="name@example.com（必填）" />
        </el-form-item>
        <el-form-item label="企业微信 ID">
          <el-input v-model="contactForm.wecom_id" class="mono" placeholder="企微通讯录 userid" />
        </el-form-item>
        <el-form-item label="手机号">
          <el-input v-model="contactForm.phone" class="mono" placeholder="钉钉 @ 用手机号" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="contactVisible = false">取消</el-button>
        <el-button type="primary" :loading="contactSaving" @click="submitContact">保存</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="resetVisible" title="重置密码" width="440px">
      <el-form label-width="80px">
        <el-form-item label="用户名">
          <el-input :model-value="resetForm.name" disabled />
        </el-form-item>
        <el-form-item label="新密码">
          <el-input
            v-model="resetForm.password"
            type="password"
            show-password
            placeholder="至少 8 个字符"
          />
        </el-form-item>
      </el-form>
      <div class="dialog-tip">重置后该用户的全部登录会话会立即失效。</div>
      <template #footer>
        <el-button @click="resetVisible = false">取消</el-button>
        <el-button type="primary" :loading="resetting" @click="submitReset">确认重置</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<style scoped>
.users-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  font-size: 13px;
}

.users-link {
  color: var(--el-color-primary);
  text-decoration: none;
}

.user-cell {
  display: flex;
  align-items: center;
  gap: 6px;
}

.danger-text {
  color: var(--el-color-danger);
}

.dialog-tip {
  color: var(--el-text-color-secondary);
  font-size: 12px;
  line-height: 1.6;
}
</style>

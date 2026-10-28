<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { authApi } from '@/api/auth'
import { profileApi } from '@/api/profile'
import { describeError } from '@/api/request'
import type { Profile } from '@/types/profile'

// 个人资料：用户自助维护联系方式（管理员不必代填），并验证邮箱是否可达。
// 联系方式被任务通知使用：邮箱用于发信，企微 ID / 手机号用于群机器人 @ 提醒。
const loading = ref(false)
const saving = ref(false)
const profile = ref<Profile | null>(null)

const form = reactive({ email: '', wecom_id: '', phone: '' })

const verifyVisible = ref(false)
const sending = ref(false)
const confirming = ref(false)
const code = ref('')
const sentTo = ref('')

const readonly = ref(false)

// 修改密码：内置管理员与平台内用户都走同一个接口（POST /api/auth/password），
// 服务端按账号类型分别写入 settings.json / rbac.json，并让其他设备的会话失效。
const pwdSaving = ref(false)
const pwd = reactive({ old_password: '', new_password: '', confirm: '' })

function resetPwd(): void {
  pwd.old_password = ''
  pwd.new_password = ''
  pwd.confirm = ''
}

async function changePassword(): Promise<void> {
  if (!pwd.old_password) {
    ElMessage.error('请输入当前密码')
    return
  }
  if (pwd.new_password.length < 8) {
    ElMessage.error('新密码至少需要 8 个字符')
    return
  }
  if (pwd.new_password !== pwd.confirm) {
    ElMessage.error('两次输入的新密码不一致')
    return
  }
  pwdSaving.value = true
  try {
    await authApi.changePassword(pwd.old_password, pwd.new_password)
    resetPwd()
    ElMessage.success('密码已修改，其他设备上的登录已失效')
  } catch (e) {
    ElMessage.error(describeError(e))
  } finally {
    pwdSaving.value = false
  }
}

async function load(): Promise<void> {
  loading.value = true
  try {
    const p = await profileApi.get()
    profile.value = p
    readonly.value = p.builtin
    form.email = p.email ?? ''
    form.wecom_id = p.wecom_id ?? ''
    form.phone = p.phone ?? ''
  } catch (e) {
    ElMessage.error(describeError(e))
  } finally {
    loading.value = false
  }
}

onMounted(load)

function isEmail(value: string): boolean {
  const parts = value.split('@')
  if (parts.length !== 2) return false
  const [local, host] = parts
  if (!local || !host) return false
  const labels = host.split('.')
  return labels.length >= 2 && labels.every((l) => l.length > 0)
}

async function save(): Promise<void> {
  // 邮箱是构建通知的发信地址，必填：留空等于放弃所有邮件告警。
  const email = form.email.trim()
  if (!isEmail(email)) {
    ElMessage.error('请填写有效邮箱：构建结果邮件会发送到该地址')
    return
  }
  saving.value = true
  try {
    const p = await profileApi.updateContact({
      email: form.email.trim(),
      wecom_id: form.wecom_id.trim(),
      phone: form.phone.trim(),
    })
    profile.value = p
    form.email = p.email ?? ''
    ElMessage.success('联系方式已保存')
    // 邮箱改动后验证状态会失效，主动提示，避免「以为已验证」。
    if (p.email && !p.email_verified) {
      ElMessage.info('邮箱已更新，请重新验证以便确认能收到告警邮件')
    }
  } catch (e) {
    ElMessage.error(describeError(e))
  } finally {
    saving.value = false
  }
}

async function sendCode(): Promise<void> {
  if (!form.email.trim()) {
    ElMessage.error('请先填写邮箱并保存')
    return
  }
  sending.value = true
  try {
    const res = await profileApi.requestEmailVerify()
    sentTo.value = res.email
    verifyVisible.value = true
    code.value = ''
    ElMessage.success(`验证码已发送到 ${res.email}，${Math.round(res.expires_in_sec / 60)} 分钟内有效`)
  } catch (e) {
    ElMessage.error(describeError(e))
  } finally {
    sending.value = false
  }
}

async function confirmCode(): Promise<void> {
  if (!code.value.trim()) {
    ElMessage.error('请输入验证码')
    return
  }
  confirming.value = true
  try {
    profile.value = await profileApi.confirmEmail(code.value.trim())
    verifyVisible.value = false
    ElMessage.success('邮箱验证成功，后续告警邮件会发送到该地址')
  } catch (e) {
    ElMessage.error(describeError(e))
  } finally {
    confirming.value = false
  }
}
</script>

<template>
  <div class="page">
    <el-card v-loading="loading" shadow="never" class="profile-card">
      <template #header>
        <div class="profile-header">
          <div>
            <h2 class="profile-title">个人资料</h2>
            <p class="profile-sub">
              维护自己的通知联系方式：任务告警按用户名引用人员时，用邮箱发信、用企业微信 ID / 手机号在群里 @ 你。
            </p>
          </div>
          <el-tag v-if="readonly" type="warning" disable-transitions>内置管理员（只读）</el-tag>
        </div>
      </template>

      <el-alert
        v-if="readonly"
        type="info"
        :closable="false"
        show-icon
        style="margin-bottom: 14px"
        title="内置管理员的账号信息来自系统配置，联系方式请在「系统管理 → 系统配置」中维护。"
      />

      <el-form label-width="120px" :disabled="readonly">
        <el-form-item label="用户名">
          <el-input :model-value="profile?.name ?? ''" disabled />
        </el-form-item>

        <el-form-item label="姓名">
          <el-input :model-value="profile?.display_name ?? ''" disabled placeholder="由管理员在用户管理中维护" />
        </el-form-item>

        <el-form-item label="邮箱" required>
          <div class="profile-email">
            <el-input v-model="form.email" class="mono" placeholder="name@example.com（必填，用于构建通知）" style="flex: 1" />
            <el-tag v-if="profile?.email && profile.email_verified" type="success" disable-transitions>
              已验证
            </el-tag>
            <el-tag v-else-if="profile?.email" type="info" disable-transitions>未验证</el-tag>
            <el-button
              v-if="profile?.email"
              :loading="sending"
              @click="sendCode"
            >发送验证码</el-button>
          </div>
          <div class="form-tip">
            邮箱必填：任务告警会发送到这里。修改邮箱后验证状态会重置；未验证的邮箱仍会收到通知，
            但无法确认地址是否正确。
          </div>
        </el-form-item>

        <el-form-item label="企业微信 ID">
          <el-input v-model="form.wecom_id" class="mono" placeholder="企微通讯录 userid（群里 @ 用）" />
        </el-form-item>

        <el-form-item label="手机号">
          <el-input v-model="form.phone" class="mono" placeholder="钉钉群机器人 @ 手机号" />
        </el-form-item>

        <el-form-item>
          <el-button type="primary" :loading="saving" :disabled="readonly" @click="save">保存</el-button>
          <el-button @click="load">重置</el-button>
        </el-form-item>
      </el-form>
    </el-card>

    <el-card shadow="never" class="profile-card password-card">
      <template #header>
        <div>
          <h2 class="profile-title">修改密码</h2>
          <p class="profile-sub">
            登录密码由你自己维护；修改成功后，除当前浏览器外的其他登录会立即失效。
          </p>
        </div>
      </template>

      <el-form label-width="120px" @submit.prevent>
        <el-form-item label="当前密码" required>
          <el-input
            v-model="pwd.old_password"
            type="password"
            show-password
            autocomplete="current-password"
            style="max-width: 320px"
          />
        </el-form-item>
        <el-form-item label="新密码" required>
          <el-input
            v-model="pwd.new_password"
            type="password"
            show-password
            autocomplete="new-password"
            style="max-width: 320px"
          />
          <div class="form-tip">至少 8 个字符，最多 72 字节。</div>
        </el-form-item>
        <el-form-item label="确认新密码" required>
          <el-input
            v-model="pwd.confirm"
            type="password"
            show-password
            autocomplete="new-password"
            style="max-width: 320px"
          />
        </el-form-item>
        <el-form-item>
          <el-button type="primary" :loading="pwdSaving" @click="changePassword">修改密码</el-button>
          <el-button @click="resetPwd">清空</el-button>
        </el-form-item>
      </el-form>
    </el-card>

    <el-dialog v-model="verifyVisible" title="验证邮箱" width="420px">
      <p class="verify-tip">
        验证码已发送到 <span class="mono">{{ sentTo }}</span>，10 分钟内有效。
      </p>
      <el-input
        v-model="code"
        class="mono"
        maxlength="6"
        placeholder="6 位数字验证码"
        @keyup.enter="confirmCode"
      />
      <template #footer>
        <el-button @click="verifyVisible = false">取消</el-button>
        <el-button :disabled="sending" @click="sendCode">重新发送</el-button>
        <el-button type="primary" :loading="confirming" @click="confirmCode">确认验证</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<style scoped>
.profile-card {
  max-width: 860px;
}

.password-card {
  margin-top: 16px;
}

.profile-header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;
}

.profile-title {
  margin: 0;
  font-size: 16px;
  font-weight: 600;
}

.profile-sub {
  margin: 6px 0 0;
  color: var(--text-3, #909399);
  font-size: 12.5px;
  line-height: 1.6;
}

.profile-email {
  display: flex;
  align-items: center;
  gap: 8px;
  width: 100%;
}

.verify-tip {
  margin: 0 0 12px;
  font-size: 13px;
  color: var(--text-2, #606266);
}
</style>

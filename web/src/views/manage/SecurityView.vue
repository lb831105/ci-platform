<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { ElMessage } from 'element-plus'
import ManagePageHeader from '@/components/ManagePageHeader.vue'
import { authApi } from '@/api/auth'
import { systemApi } from '@/api/system'
import { describeError } from '@/api/request'
import { useAuthStore } from '@/stores/authStore'
import { useSettingsStore } from '@/stores/settingsStore'
import type { Settings } from '@/types/system'

const auth = useAuthStore()
const settingsStore = useSettingsStore()

const loading = ref(false)
const saving = ref(false)
const changing = ref(false)
const form = ref<Settings | null>(null)

const password = reactive({ old_password: '', new_password: '', confirm: '' })

async function load(): Promise<void> {
  loading.value = true
  try {
    form.value = await systemApi.getSettings()
    settingsStore.set(form.value)
  } catch (e) {
    ElMessage.error(describeError(e))
  } finally {
    loading.value = false
  }
}

onMounted(load)

async function onSave(): Promise<void> {
  if (!form.value) {
    return
  }
  saving.value = true
  try {
    const updated = await systemApi.updateSettings(form.value)
    form.value = updated
    settingsStore.set(updated)
    auth.enabled = updated.auth_enabled
    if (updated.auth_user) {
      auth.username = updated.auth_user
    }
    ElMessage.success('安全配置已保存')
  } catch (e) {
    ElMessage.error(describeError(e))
  } finally {
    saving.value = false
  }
}

async function onChangePassword(): Promise<void> {
  if (!password.new_password) {
    ElMessage.error('请输入新密码')
    return
  }
  if (password.new_password.length < 8) {
    ElMessage.error('新密码至少需要 8 个字符')
    return
  }
  if (password.new_password !== password.confirm) {
    ElMessage.error('两次输入的新密码不一致')
    return
  }
  changing.value = true
  try {
    await authApi.changePassword(password.old_password, password.new_password)
    password.old_password = ''
    password.new_password = ''
    password.confirm = ''
    ElMessage.success('密码已修改')
  } catch (e) {
    ElMessage.error(describeError(e))
  } finally {
    changing.value = false
  }
}
</script>

<template>
  <div class="page">
    <ManagePageHeader title="全局安全配置" description="定义谁可以访问或使用系统" />

    <el-card v-loading="loading" shadow="never" class="security-card">
      <template #header>认证</template>
      <el-form v-if="form" label-width="140px" label-position="right">
        <el-form-item label="开启登录校验">
          <el-switch v-model="form.auth_enabled" active-text="已开启" inactive-text="已关闭" />
          <div class="form-tip">关闭后任何人都可直接访问，仅建议在受信任的内网或本地调试时使用。</div>
        </el-form-item>
        <el-form-item label="管理员用户名">
          <el-input v-model="form.auth_user" style="max-width: 320px" />
        </el-form-item>
        <el-form-item label="管理员密码">
          <el-input
            v-model="form.auth_password"
            type="password"
            show-password
            placeholder="留空表示保持当前密码不变"
            style="max-width: 320px"
          />
          <div class="form-tip" :class="{ 'is-warn': form.has_password === false }">
            {{ form.has_password ? '当前已设置密码。' : '当前未设置密码。' }}
          </div>
        </el-form-item>
        <el-form-item>
          <el-button type="primary" :loading="saving" @click="onSave">保存认证配置</el-button>
        </el-form-item>
      </el-form>
    </el-card>

    <el-card shadow="never" class="security-card">
      <template #header>修改密码</template>
      <el-form label-width="140px" label-position="right">
        <el-form-item label="当前密码">
          <el-input v-model="password.old_password" type="password" show-password style="max-width: 320px" />
        </el-form-item>
        <el-form-item label="新密码">
          <el-input
            v-model="password.new_password"
            type="password"
            show-password
            minlength="8"
            style="max-width: 320px"
          />
        </el-form-item>
        <el-form-item label="确认新密码">
          <el-input v-model="password.confirm" type="password" show-password style="max-width: 320px" />
        </el-form-item>
        <el-form-item>
          <el-button type="primary" :loading="changing" @click="onChangePassword">修改密码</el-button>
        </el-form-item>
      </el-form>
    </el-card>
  </div>
</template>

<style scoped>
.security-card {
  max-width: 760px;
  margin-bottom: 16px;
}
.form-tip.is-warn {
  color: #e6a23c;
}
</style>

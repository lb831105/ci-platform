<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import ManagePageHeader from '@/components/ManagePageHeader.vue'
import { credentialApi } from '@/api/system'
import { describeError } from '@/api/request'
import { formatTime } from '@/utils/format'
import type { Credential, CredentialPayload, CredentialType } from '@/types/system'

const loading = ref(false)
const saving = ref(false)
const items = ref<Credential[]>([])
const dialogVisible = ref(false)
const editingId = ref('')
const isEdit = ref(false)

const form = reactive<CredentialPayload>({
  name: '',
  type: 'username_password',
  username: '',
  description: '',
  secret: '',
})

const TYPE_LABELS: Record<CredentialType, string> = {
  username_password: '用户名密码',
  secret_text: '密钥文本',
}

async function load(): Promise<void> {
  loading.value = true
  try {
    items.value = await credentialApi.list()
  } catch (e) {
    ElMessage.error(describeError(e))
  } finally {
    loading.value = false
  }
}

onMounted(load)

function openCreate(): void {
  isEdit.value = false
  editingId.value = ''
  Object.assign(form, { name: '', type: 'username_password', username: '', description: '', secret: '' })
  dialogVisible.value = true
}

function openEdit(row: Credential): void {
  isEdit.value = true
  editingId.value = row.id
  Object.assign(form, {
    name: row.name,
    type: row.type,
    username: row.username ?? '',
    description: row.description,
    secret: '',
  })
  dialogVisible.value = true
}

async function onSubmit(): Promise<void> {
  if (!form.name.trim()) {
    ElMessage.error('请输入凭据名称')
    return
  }
  if (!isEdit.value && !form.secret) {
    ElMessage.error('请填写凭据内容')
    return
  }
  saving.value = true
  try {
    if (isEdit.value) {
      await credentialApi.update(editingId.value, { ...form })
      ElMessage.success('凭据已更新')
    } else {
      await credentialApi.create({ ...form })
      ElMessage.success('凭据已创建')
    }
    dialogVisible.value = false
    await load()
  } catch (e) {
    ElMessage.error(describeError(e))
  } finally {
    saving.value = false
  }
}

async function onRemove(row: Credential): Promise<void> {
  try {
    await ElMessageBox.confirm(`确认删除凭据「${row.name}」？`, '危险操作', {
      type: 'warning',
      confirmButtonText: '确认删除',
      cancelButtonText: '取消',
    })
  } catch {
    return
  }
  try {
    await credentialApi.remove(row.id)
    ElMessage.success('已删除')
    await load()
  } catch (e) {
    ElMessage.error(describeError(e))
  }
}
</script>

<template>
  <div class="page">
    <ManagePageHeader title="凭据管理" description="集中管理用户名密码、密钥文本与 SSH 私钥">
      <template #actions>
        <el-button type="primary" @click="openCreate">新增凭据</el-button>
      </template>
    </ManagePageHeader>

    <el-card v-loading="loading" shadow="never" class="cred-card">
      <el-table :data="items">
        <el-table-column label="名称" prop="name" min-width="180">
          <template #default="{ row }">
            <el-button text type="primary" @click="openEdit(row)">{{ row.name }}</el-button>
          </template>
        </el-table-column>
        <el-table-column label="类型" width="130">
          <template #default="{ row }">
            <el-tag effect="plain" disable-transitions>{{ TYPE_LABELS[row.type as CredentialType] ?? row.type }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column label="用户名" min-width="140">
          <template #default="{ row }">{{ row.username || '-' }}</template>
        </el-table-column>
        <el-table-column label="说明" prop="description" min-width="180" show-overflow-tooltip />
        <el-table-column label="更新时间" width="180">
          <template #default="{ row }">{{ formatTime(row.updated_at) }}</template>
        </el-table-column>
        <el-table-column label="操作" width="140" align="right">
          <template #default="{ row }">
            <el-button size="small" @click="openEdit(row)">编辑</el-button>
            <el-button size="small" type="danger" plain @click="onRemove(row)">删除</el-button>
          </template>
        </el-table-column>
        <template #empty>
          <el-empty description="还没有凭据" />
        </template>
      </el-table>
    </el-card>

    <el-dialog v-model="dialogVisible" :title="isEdit ? '编辑凭据' : '新增凭据'" width="560px">
      <el-form label-width="120px" label-position="right">
        <el-form-item label="名称">
          <el-input v-model="form.name" placeholder="如 prod-ssh" />
        </el-form-item>
        <el-form-item label="类型">
          <el-select v-model="form.type" style="width: 100%">
            <el-option label="用户名密码" value="username_password" />
            <el-option label="密钥文本" value="secret_text" />
          </el-select>
        </el-form-item>
        <el-form-item v-if="form.type === 'username_password'" label="用户名">
          <el-input v-model="form.username" />
        </el-form-item>
        <el-form-item label="凭据内容">
          <el-input
            v-model="form.secret"
            type="textarea"
            :rows="2"
            :placeholder="isEdit ? '留空表示保持原值不变' : form.type === 'secret_text' ? 'Token / 密钥文本' : '密码'"
          />
          <div v-if="isEdit" class="form-tip">原值已保存在服务端（data/credentials/*.json），留空不修改，填写则覆盖。</div>
        </el-form-item>
        <el-form-item label="说明">
          <el-input v-model="form.description" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="dialogVisible = false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="onSubmit">保存</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<style scoped>
.cred-card {
  max-width: 1080px;
}
</style>

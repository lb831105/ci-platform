<script setup lang="ts">
import { ref } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import { describeError } from '@/api/request'
import { viewApi } from '@/api/view'
import { VIEW_TYPES } from '@/types/view'
import type { ViewType } from '@/types/view'

const router = useRouter()

const name = ref('')
const type = ref<ViewType>('list')
const submitting = ref(false)

// 对齐 Jenkins New View：名称 + Type（列表视图 / 我的视图），Create 后进入编辑页。
async function onCreate(): Promise<void> {
  const trimmed = name.value.trim()
  if (!trimmed) {
    ElMessage.warning('请输入视图名称')
    return
  }
  submitting.value = true
  try {
    await viewApi.create({ name: trimmed, type: type.value })
    ElMessage.success(`视图「${trimmed}」已创建`)
    void router.push({ name: 'view-edit', params: { viewName: trimmed } })
  } catch (e) {
    ElMessage.error(describeError(e))
  } finally {
    submitting.value = false
  }
}
</script>

<template>
  <div class="page">
    <div class="page-header">
      <div>
        <h1 class="page-title">新建视图</h1>
        <div class="muted">视图本质是给任务归类，对齐 Jenkins New View</div>
      </div>
    </div>

    <el-card shadow="never" style="max-width: 640px">
      <el-form label-position="top" @submit.prevent="onCreate">
        <el-form-item label="视图名称" required>
          <el-input v-model="name" placeholder="例如：前端任务" maxlength="100" show-word-limit />
        </el-form-item>

        <el-form-item label="Type">
          <el-radio-group v-model="type">
            <div v-for="option in VIEW_TYPES" :key="option.value" class="view-type-option">
              <el-radio :value="option.value">{{ option.title }}</el-radio>
              <div class="view-type-option__desc muted">{{ option.desc }}</div>
            </div>
          </el-radio-group>
        </el-form-item>

        <el-form-item>
          <el-button type="primary" :loading="submitting" @click="onCreate">Create</el-button>
        </el-form-item>
      </el-form>
    </el-card>
  </div>
</template>

<style scoped>
.view-type-option {
  display: block;
  width: 100%;
  margin-bottom: 8px;
}
.view-type-option__desc {
  margin-left: 22px;
  font-size: 12px;
}
</style>

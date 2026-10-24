<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { JOB_TYPES, findJobType } from '@/types/jobType'
import { usePipeline } from '@/composables/usePipeline'
import { usePipelineStore } from '@/stores/pipelineStore'
import type { Pipeline } from '@/types/pipeline'

const route = useRoute()
const router = useRouter()
const { fetchList } = usePipeline()

const name = ref(typeof route.query.name === 'string' ? route.query.name : '')
const selected = ref(
  typeof route.query.type === 'string' && findJobType(route.query.type)
    ? route.query.type
    : 'freestyle',
)
const errorText = ref('')

// 任务模板：从现有任务复制配置（对齐 Jenkins New Item 的 Copy from）。
// 选择源任务后忽略任务类型（配置整体来自源任务），进入配置页后仍可修改。
const copyFrom = ref('')
const pipelines = ref<Pipeline[]>([])
const pipelinesLoading = ref(false)

onMounted(async () => {
  // 加载任务列表供 Copy from 选择；失败不影响手动创建流程。
  pipelinesLoading.value = true
  try {
    await fetchList()
    // fetchList 已写入 pipelineStore，这里只做快照供下拉使用。
    pipelines.value = [...usePipelineStore().items]
  } catch {
    // 列表加载失败仅导致 Copy from 为空，不阻塞新建。
  } finally {
    pipelinesLoading.value = false
  }
})

function onConfirm(): void {
  if (!name.value.trim()) {
    errorText.value = '请输入任务名称'
    return
  }
  errorText.value = ''
  // 模板模式：整体复制源任务配置，任务类型由源任务决定。
  if (copyFrom.value) {
    void router.push({
      name: 'pipeline-create-config',
      query: { name: name.value.trim(), copy: copyFrom.value },
    })
    return
  }
  const type = JOB_TYPES.find((item) => item.id === selected.value && item.enabled)
  if (!type) {
    errorText.value = '请选择一个受支持的任务类型'
    return
  }
  void router.push({
    name: 'pipeline-create-config',
    query: { name: name.value.trim(), type: type.id },
  })
}

function onCancel(): void {
  void router.push({ name: 'pipeline-list' })
}

function select(typeId: string, enabled: boolean): void {
  if (enabled) {
    selected.value = typeId
  }
}
</script>

<template>
  <div class="page new-item">
    <div class="page-header">
      <div>
        <h1 class="page-title">新建任务</h1>
        <div class="muted">输入任务名称，选择一种任务类型，然后进入详细配置</div>
      </div>
    </div>

    <el-card shadow="never" class="new-item__card">
      <div class="new-item__field">
        <label class="new-item__label" for="job-name">任务名称</label>
        <input
          id="job-name"
          v-model="name"
          class="new-item__input"
          type="text"
          placeholder="如 order-service-build"
          autofocus
          @keyup.enter="onConfirm"
        />
        <div class="new-item__hint">任务名称用于列表展示与目录名生成，创建后仍可修改。</div>
      </div>

      <div class="new-item__field">
        <label class="new-item__label" for="job-copy-from">Copy from（可选）</label>
        <el-select
          id="job-copy-from"
          v-model="copyFrom"
          class="new-item__input"
          clearable
          filterable
          :loading="pipelinesLoading"
          placeholder="从现有任务复制配置（选择后忽略任务类型）"
        >
          <el-option
            v-for="p in pipelines"
            :key="p.id"
            :label="p.name"
            :value="p.id"
          />
        </el-select>
        <div class="new-item__hint">选择后，新任务将复制该任务的全部配置（步骤、Git、发布、产物等），名称使用上方输入。</div>
      </div>

      <el-alert
        v-if="errorText"
        type="error"
        :closable="false"
        show-icon
        :title="errorText"
        style="margin: 6px 0 14px"
      />

      <div class="new-item__types" :class="{ 'new-item__types--dimmed': !!copyFrom }">
        <div class="new-item__types-title">选择任务类型</div>
        <ul class="type-list">
          <li
            v-for="type in JOB_TYPES"
            :key="type.id"
            class="type-item"
            :class="{
              'type-item--selected': selected === type.id && type.enabled,
              'type-item--disabled': !type.enabled,
            }"
            @click="select(type.id, type.enabled)"
          >
            <input
              v-model="selected"
              class="type-item__radio"
              type="radio"
              :value="type.id"
              :disabled="!type.enabled"
            />
            <span class="type-item__badge" :style="{ background: type.color }">{{ type.badge }}</span>
            <span class="type-item__text">
              <span class="type-item__title">{{ type.title }}</span>
              <span class="type-item__desc">{{ type.description }}</span>
            </span>
            <el-tag v-if="!type.enabled" type="info" effect="plain" disable-transitions>
              {{ type.disabledReason }}
            </el-tag>
          </li>
        </ul>
      </div>

      <div class="new-item__actions">
        <el-button type="primary" @click="onConfirm">确定</el-button>
        <el-button @click="onCancel">取消</el-button>
      </div>
    </el-card>
  </div>
</template>

<style scoped>
.new-item__card {
  max-width: 860px;
}
.new-item__field {
  display: flex;
  flex-direction: column;
  gap: 6px;
  margin-bottom: 8px;
}
.new-item__label {
  font-size: 14px;
  font-weight: 600;
  color: #303133;
}
.new-item__input {
  height: 38px;
  max-width: 480px;
  padding: 0 12px;
  border: 1px solid #dcdfe6;
  border-radius: 6px;
  font-size: 14px;
  outline: none;
  transition: border-color 0.2s;
}
.new-item__input:focus {
  border-color: var(--brand-600);
  box-shadow: 0 0 0 2px rgba(31, 111, 208, 0.15);
}
.new-item__hint {
  font-size: 12px;
  color: #909399;
}
.new-item__types {
  margin-top: 20px;
}
.new-item__types--dimmed {
  opacity: 0.45;
  pointer-events: none;
}
.new-item__types-title {
  font-size: 14px;
  font-weight: 600;
  color: #303133;
  margin-bottom: 10px;
}
.type-list {
  list-style: none;
  margin: 0;
  padding: 0;
  border: 1px solid #e4e7ed;
  border-radius: 8px;
  overflow: hidden;
}
.type-item {
  display: flex;
  align-items: center;
  gap: 14px;
  padding: 12px 16px;
  border-bottom: 1px solid #f0f2f5;
  cursor: pointer;
  transition: background 0.15s;
}
.type-item:last-child {
  border-bottom: none;
}
.type-item:hover:not(.type-item--disabled) {
  background: #f5f9ff;
}
.type-item--selected {
  background: var(--brand-50);
  box-shadow: inset 3px 0 0 var(--brand-600);
}
.type-item--disabled {
  cursor: not-allowed;
  opacity: 0.6;
}
.type-item__radio {
  flex: 0 0 auto;
}
.type-item__badge {
  flex: 0 0 auto;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 34px;
  height: 34px;
  border-radius: 8px;
  color: #fff;
  font-weight: 700;
  font-size: 15px;
}
.type-item__text {
  display: flex;
  flex-direction: column;
  gap: 3px;
  flex: 1;
  min-width: 0;
}
.type-item__title {
  font-size: 14px;
  font-weight: 600;
  color: #303133;
}
.type-item__desc {
  font-size: 12px;
  color: #909399;
  line-height: 1.5;
}
.new-item__actions {
  margin-top: 22px;
  display: flex;
  gap: 10px;
}
</style>

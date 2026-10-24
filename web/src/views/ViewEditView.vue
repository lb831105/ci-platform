<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import { describeError } from '@/api/request'
import { viewApi } from '@/api/view'
import { pipelineApi } from '@/api/pipeline'
import { VIEW_COLUMNS } from '@/types/view'
import type { View } from '@/types/view'
import type { Pipeline } from '@/types/pipeline'

const props = defineProps<{ viewName: string }>()

const router = useRouter()

const view = ref<View | null>(null)
const pipelines = ref<Pipeline[]>([])
const loading = ref(false)
const saving = ref(false)
const error = ref('')

const description = ref('')
const selectedJobs = ref<string[]>([])
const jobRegex = ref('')
const columns = ref<string[]>([])
const filterQueue = ref(false)
const filterExecutors = ref(false)

// 任务列表按名称排序，方便勾选。
const sortedPipelines = computed(() =>
  [...pipelines.value].sort((a, b) => a.name.localeCompare(b.name, 'zh-CN')),
)

async function load(): Promise<void> {
  loading.value = true
  try {
    const [views, list] = await Promise.all([viewApi.list(), pipelineApi.list()])
    const v = views.find((item) => item.name === props.viewName)
    if (!v) {
      error.value = `视图「${props.viewName}」不存在`
      return
    }
    view.value = v
    description.value = v.description ?? ''
    selectedJobs.value = [...(v.jobs ?? [])]
    jobRegex.value = v.job_regex ?? ''
    columns.value = [...(v.columns ?? VIEW_COLUMNS.map((c) => c.id))]
    filterQueue.value = v.filter_queue ?? false
    filterExecutors.value = v.filter_executors ?? false
    pipelines.value = list
    error.value = ''
  } catch (e) {
    error.value = describeError(e)
  } finally {
    loading.value = false
  }
}

onMounted(load)

function toggleJob(name: string, checked: string | number | boolean | undefined): void {
  const on = Boolean(checked)
  const set = new Set(selectedJobs.value)
  if (on) {
    set.add(name)
  } else {
    set.delete(name)
  }
  selectedJobs.value = [...set]
}

function isJobChecked(name: string): boolean {
  return selectedJobs.value.includes(name)
}

function isColumnChecked(id: string): boolean {
  return columns.value.includes(id)
}

function toggleColumn(id: string, checked: string | number | boolean | undefined): void {
  const on = Boolean(checked)
  const set = new Set(columns.value)
  if (on) {
    set.add(id)
  } else {
    set.delete(id)
  }
  columns.value = VIEW_COLUMNS.map((c) => c.id).filter((cid) => set.has(cid))
}

async function onSave(): Promise<void> {
  saving.value = true
  try {
    await viewApi.update(props.viewName, {
      description: description.value,
      type: view.value?.type ?? 'list',
      jobs: selectedJobs.value,
      job_regex: jobRegex.value.trim(),
      columns: columns.value,
      filter_queue: filterQueue.value,
      filter_executors: filterExecutors.value,
    })
    ElMessage.success('视图已保存')
    void router.push({ name: 'view-page', params: { viewName: props.viewName } })
  } catch (e) {
    ElMessage.error(describeError(e))
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <div class="page">
    <div class="page-header">
      <div>
        <h1 class="page-title">编辑视图：{{ view?.name || viewName }}</h1>
        <div class="muted">{{ view?.type === 'my' ? '我的视图：自动显示当前用户有权限访问的任务' : '列表视图：显式任务 + 正则归类' }}</div>
      </div>
      <div>
        <el-button :loading="loading" @click="load">刷新</el-button>
        <el-button type="primary" :loading="saving" @click="onSave">Save</el-button>
      </div>
    </div>

    <el-alert v-if="error" type="error" :closable="false" show-icon :title="error" style="margin-bottom: 16px" />

    <el-card v-if="view" shadow="never" style="max-width: 860px">
      <el-form label-position="top" @submit.prevent="onSave">
        <el-form-item label="名称">
          <el-input :model-value="view.name" disabled />
        </el-form-item>

        <el-form-item label="描述">
          <el-input
            v-model="description"
            type="textarea"
            :rows="3"
            placeholder="Describe the purpose of this view."
            maxlength="500"
            show-word-limit
          />
          <div class="muted" style="font-size: 12px; margin-top: 4px">纯文本</div>
        </el-form-item>

        <template v-if="view.type === 'list'">
          <el-form-item label="任务列表">
            <div class="muted" style="margin-bottom: 8px">Select the jobs to show in this view.</div>
            <el-checkbox-group class="job-picker">
              <el-checkbox
                v-for="p in sortedPipelines"
                :key="p.id"
                :model-value="isJobChecked(p.name)"
                :label="p.name"
                @change="(v: any) => toggleJob(p.name, v)"
              >
                {{ p.name }}
              </el-checkbox>
            </el-checkbox-group>
          </el-form-item>

          <el-form-item label="任务名正则">
            <el-input v-model="jobRegex" placeholder="使用正则表达式在视图中显示任务" clearable />
          </el-form-item>

          <el-form-item label="Filters">
            <div class="muted" style="font-size: 13px">
              平台暂不支持 Jenkins 任务过滤器（如构建状态/触发者条件过滤）。
            </div>
          </el-form-item>
        </template>

        <el-form-item label="列">
          <div class="muted" style="margin-bottom: 8px">Select the columns in this view.</div>
          <el-checkbox-group>
            <el-checkbox
              v-for="col in VIEW_COLUMNS"
              :key="col.id"
              :model-value="isColumnChecked(col.id)"
              :label="col.title"
              @change="(v: any) => toggleColumn(col.id, v)"
            >
              {{ col.title }}
            </el-checkbox>
          </el-checkbox-group>
        </el-form-item>

        <el-form-item label="Widgets">
          <el-checkbox v-model="filterQueue">过滤构建队列</el-checkbox>
          <div class="muted widget-hint">If checked, only jobs in this view will be shown in the queue.</div>
          <el-checkbox v-model="filterExecutors">过滤构建执行器</el-checkbox>
          <div class="muted widget-hint">If checked, only those build executors will be shown that could execute the jobs in this view.</div>
        </el-form-item>

        <el-form-item>
          <el-button type="primary" :loading="saving" @click="onSave">Save</el-button>
        </el-form-item>
      </el-form>
    </el-card>
  </div>
</template>

<style scoped>
.job-picker {
  display: flex;
  flex-direction: column;
  align-items: flex-start;
  gap: 4px;
  max-height: 360px;
  overflow-y: auto;
  width: 100%;
  padding: 8px;
  border: 1px solid var(--border-2, #e4e7ed);
  border-radius: 6px;
}
.widget-hint {
  margin: -4px 0 8px 24px;
  font-size: 12px;
}
</style>

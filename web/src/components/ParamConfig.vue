<script setup lang="ts">
// 构建参数编辑器：对齐 Jenkins Parameters（String / Boolean / Choice / Text）。
// 只负责编辑，命名与默认值校验由后端统一裁决（前后端规则只允许有一份）。
import { computed } from 'vue'
import { ElMessage } from 'element-plus'
import { emptyParam, type ParamDef, type ParamType } from '@/types/pipeline'

const props = defineProps<{ modelValue: ParamDef[] }>()

const emit = defineEmits<{
  (event: 'update:modelValue', value: ParamDef[]): void
}>()

const typeOptions: Array<{ value: ParamType; label: string }> = [
  { value: 'string', label: '字符串' },
  { value: 'bool', label: '布尔值' },
  { value: 'choice', label: '单选（Choice）' },
  { value: 'text', label: '多行文本' },
]

const items = computed(() => props.modelValue ?? [])

function commit(list: ParamDef[]): void {
  emit('update:modelValue', list)
}

function add(): void {
  const list: ParamDef[] = items.value.map((item) => ({
    ...item,
    options: [...(item.options ?? [])],
  }))
  list.push(emptyParam())
  commit(list)
}

function remove(index: number): void {
  commit(items.value.filter((_, i) => i !== index))
}

function patch(index: number, partial: Partial<ParamDef>): void {
  const list: ParamDef[] = items.value.map((item) => ({
    ...item,
    options: [...(item.options ?? [])],
  }))
  list[index] = { ...list[index], ...partial }
  if (partial.type && partial.type !== 'choice') {
    // 非 choice 类型不保留可选值，避免切换类型后残留脏数据。
    list[index].options = []
  }
  if (partial.type === 'bool' && !list[index].default) {
    list[index].default = 'false'
  }
  commit(list)
}

// optionsText 用换行分隔可选值（Jenkins 也是这样填的），提交时再按行拆分。
function optionsText(def: ParamDef): string {
  return (def.options ?? []).join('\n')
}

function setOptions(index: number, text: string): void {
  const options = text
    .split('\n')
    .map((line) => line.trim())
    .filter((line) => line !== '')
  patch(index, { options })
}

function booleanDefault(def: ParamDef): boolean {
  return def.default === 'true'
}

function setBooleanDefault(index: number, value: boolean): void {
  patch(index, { default: value ? 'true' : 'false' })
}

function onAdd(): void {
  if (items.value.length >= 30) {
    ElMessage.warning('最多支持 30 个构建参数')
    return
  }
  add()
}
</script>

<template>
  <div class="param-config">
    <el-table v-if="items.length" :data="items" size="small" border class="param-config__table">
      <el-table-column label="参数名" width="180">
        <template #default="{ row, $index }">
          <el-input
            :model-value="row.name"
            placeholder="如 VERSION"
            @update:model-value="(value: string) => patch($index, { name: value })"
          />
        </template>
      </el-table-column>
      <el-table-column label="类型" width="150">
        <template #default="{ row, $index }">
          <el-select
            :model-value="row.type"
            @update:model-value="(value: ParamType) => patch($index, { type: value })"
          >
            <el-option v-for="opt in typeOptions" :key="opt.value" :label="opt.label" :value="opt.value" />
          </el-select>
        </template>
      </el-table-column>
      <el-table-column label="默认值 / 可选值" min-width="240">
        <template #default="{ row, $index }">
          <el-switch
            v-if="row.type === 'bool'"
            :model-value="booleanDefault(row)"
            active-text="默认勾选"
            @update:model-value="(value: boolean) => setBooleanDefault($index, value)"
          />
          <el-select
            v-else-if="row.type === 'choice'"
            :model-value="row.default"
            placeholder="默认选中项"
            style="width: 100%"
            @update:model-value="(value: string) => patch($index, { default: value })"
          >
            <el-option v-for="opt in row.options ?? []" :key="opt" :label="opt" :value="opt" />
          </el-select>
          <el-input
            v-else
            :model-value="row.default"
            placeholder="留空则触发时必须填写（若勾选必填）"
            @update:model-value="(value: string) => patch($index, { default: value })"
          />
          <el-input
            v-if="row.type === 'choice'"
            type="textarea"
            :rows="2"
            :model-value="optionsText(row)"
            placeholder="可选值，每行一个"
            class="param-config__options"
            @update:model-value="(value: string) => setOptions($index, value)"
          />
        </template>
      </el-table-column>
      <el-table-column label="必填" width="70" align="center">
        <template #default="{ row, $index }">
          <el-checkbox
            :model-value="row.required"
            @update:model-value="(value: boolean) => patch($index, { required: value })"
          />
        </template>
      </el-table-column>
      <el-table-column label="说明" width="180">
        <template #default="{ row, $index }">
          <el-input
            :model-value="row.description ?? ''"
            placeholder="可选"
            @update:model-value="(value: string) => patch($index, { description: value })"
          />
        </template>
      </el-table-column>
      <el-table-column label="操作" width="70" align="center">
        <template #default="{ $index }">
          <el-button link type="danger" @click="remove($index)">删除</el-button>
        </template>
      </el-table-column>
    </el-table>
    <div class="param-config__actions">
      <el-button size="small" @click="onAdd">添加参数</el-button>
      <span class="param-config__hint">
        参数会以「参数名」和 CI_PARAM_&lt;大写名&gt; 两种环境变量注入构建步骤；参数名只能用字母、数字、下划线，且不能以 CI_ 开头。
      </span>
    </div>
  </div>
</template>

<style scoped>
.param-config__table {
  margin-bottom: 10px;
}
.param-config__options {
  margin-top: 6px;
}
.param-config__actions {
  display: flex;
  align-items: center;
  gap: 12px;
  flex-wrap: wrap;
}
.param-config__hint {
  color: var(--text-3, #909399);
  font-size: 12px;
  line-height: 1.5;
}
</style>

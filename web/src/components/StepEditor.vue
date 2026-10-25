<script setup lang="ts">
import { ref } from 'vue'
import { emptyStep, type BuildStep, type StepWhen } from '@/types/pipeline'

const props = defineProps<{ modelValue: BuildStep[] }>()

const emit = defineEmits<{
  (event: 'update:modelValue', value: BuildStep[]): void
}>()

function clone(): BuildStep[] {
  return props.modelValue.map((step) => ({ ...step }))
}

function commit(steps: BuildStep[]): void {
  emit('update:modelValue', steps)
}

function addStep(): void {
  const steps = clone()
  steps.push(emptyStep(`step-${steps.length + 1}`))
  commit(steps)
}

// 高级选项默认收起：绝大多数步骤只需要名称与命令，避免配置页噪音。
const expanded = ref<Record<number, boolean>>({})
function toggleAdvanced(index: number): void {
  expanded.value = { ...expanded.value, [index]: !expanded.value[index] }
}

const whenOptions: Array<{ value: StepWhen; label: string }> = [
  { value: 'success', label: '前置成功后执行（默认）' },
  { value: 'failure', label: '仅在失败后执行' },
  { value: 'always', label: '总是执行' },
]

// 兜底：后端返回的旧数据可能缺字段，渲染前补齐，避免输入框显示 undefined。
function timeoutOf(step: BuildStep): number {
  return step.timeout_sec ?? 0
}
function retryOf(step: BuildStep): number {
  return step.retry ?? 0
}
function retryDelayOf(step: BuildStep): number {
  return step.retry_delay_sec ?? 0
}
function whenOf(step: BuildStep): StepWhen {
  return step.when ?? 'success'
}

function removeStep(index: number): void {
  const steps = clone()
  steps.splice(index, 1)
  commit(steps)
}

function move(index: number, offset: number): void {
  const target = index + offset
  if (target < 0 || target >= props.modelValue.length) {
    return
  }
  const steps = clone()
  const [moved] = steps.splice(index, 1)
  steps.splice(target, 0, moved)
  commit(steps)
}

function updateStep(index: number, patch: Partial<BuildStep>): void {
  const steps = clone()
  steps[index] = { ...steps[index], ...patch }
  commit(steps)
}
</script>

<template>
  <div class="step-editor">
    <div v-for="(step, index) in modelValue" :key="index" class="step-editor__row">
      <div class="step-editor__index mono">{{ index + 1 }}</div>
      <div class="step-editor__body">
        <el-input
          :model-value="step.name"
          placeholder="步骤名称，如 build"
          size="small"
          @update:model-value="(value: string) => updateStep(index, { name: value })"
        />
        <el-input
          :model-value="step.run"
          type="textarea"
          :rows="2"
          class="mono"
          placeholder="在该任务工作区内执行的 shell 命令，如 mvn clean package -DskipTests"
          @update:model-value="(value: string) => updateStep(index, { run: value })"
        />
        <div class="step-editor__controls">
          <el-button size="small" link @click="toggleAdvanced(index)">
            {{ expanded[index] ? '收起高级选项' : '高级选项（超时 / 重试 / 失败处理）' }}
          </el-button>
          <el-tag v-if="step.allow_failure" size="small" type="warning" effect="plain">允许失败</el-tag>
          <el-tag v-if="whenOf(step) !== 'success'" size="small" type="info" effect="plain">
            {{ whenOf(step) === 'always' ? '总是执行' : '仅失败后执行' }}
          </el-tag>
          <el-tag v-if="timeoutOf(step) > 0" size="small" effect="plain">
            超时 {{ timeoutOf(step) }}s
          </el-tag>
          <el-tag v-if="retryOf(step) > 0" size="small" effect="plain">
            重试 {{ retryOf(step) }} 次
          </el-tag>
        </div>
        <div v-if="expanded[index]" class="step-editor__advanced">
          <div class="step-editor__field">
            <span class="step-editor__label">超时（秒）</span>
            <el-input-number
              :model-value="timeoutOf(step)"
              :min="0"
              :max="86400"
              :step="30"
              size="small"
              controls-position="right"
              @update:model-value="(value: number | undefined) => updateStep(index, { timeout_sec: value ?? 0 })"
            />
            <span class="step-editor__hint">0 = 不限；超时按退出码 124 视为失败</span>
          </div>
          <div class="step-editor__field">
            <span class="step-editor__label">重试次数</span>
            <el-input-number
              :model-value="retryOf(step)"
              :min="0"
              :max="5"
              size="small"
              controls-position="right"
              @update:model-value="(value: number | undefined) => updateStep(index, { retry: value ?? 0 })"
            />
            <span class="step-editor__label">间隔（秒）</span>
            <el-input-number
              :model-value="retryDelayOf(step)"
              :min="0"
              :max="60"
              size="small"
              controls-position="right"
              @update:model-value="(value: number | undefined) => updateStep(index, { retry_delay_sec: value ?? 0 })"
            />
          </div>
          <div class="step-editor__field">
            <span class="step-editor__label">执行条件</span>
            <el-select
              :model-value="whenOf(step)"
              size="small"
              style="width: 200px"
              @update:model-value="(value: StepWhen) => updateStep(index, { when: value })"
            >
              <el-option v-for="opt in whenOptions" :key="opt.value" :label="opt.label" :value="opt.value" />
            </el-select>
            <el-checkbox
              :model-value="step.allow_failure"
              :disabled="whenOf(step) === 'failure'"
              @update:model-value="(value: boolean) => updateStep(index, { allow_failure: value })"
            >允许失败（不阻断后续步骤）</el-checkbox>
          </div>
        </div>
      </div>
      <div class="step-editor__ops">
        <el-button size="small" text :disabled="index === 0" @click="move(index, -1)">↑</el-button>
        <el-button size="small" text :disabled="index === modelValue.length - 1" @click="move(index, 1)">↓</el-button>
        <el-button size="small" text type="danger" @click="removeStep(index)">删除</el-button>
      </div>
    </div>

    <el-button size="small" type="primary" plain @click="addStep">+ 添加构建步骤</el-button>
    <div class="form-tip">
      步骤串行执行；未设置「允许失败」的步骤退出码非零时终止后续步骤并标记构建失败，
      但「总是执行 / 仅失败后执行」的步骤仍会运行（适合清理、收集诊断信息）。
    </div>
  </div>
</template>

<style scoped>
.step-editor__controls {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}
.step-editor__advanced {
  border: 1px dashed var(--border-color, #dcdfe6);
  border-radius: 6px;
  padding: 8px 10px;
  display: flex;
  flex-direction: column;
  gap: 8px;
}
.step-editor__field {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}
.step-editor__label {
  font-size: 12.5px;
  color: var(--text-2, #606266);
}
.step-editor__hint {
  font-size: 12px;
  color: var(--text-3, #909399);
}

.step-editor__row {
  display: flex;
  align-items: flex-start;
  gap: 10px;
  margin-bottom: 10px;
}
.step-editor__index {
  width: 24px;
  height: 24px;
  line-height: 24px;
  text-align: center;
  border-radius: 50%;
  background: var(--brand-50);
  color: #409eff;
  font-size: 12px;
  flex: none;
  margin-top: 4px;
}
.step-editor__body {
  flex: 1;
  display: flex;
  flex-direction: column;
  gap: 6px;
}
.step-editor__ops {
  flex: none;
  display: flex;
  align-items: center;
}
</style>

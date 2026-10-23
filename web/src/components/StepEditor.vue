<script setup lang="ts">
import type { BuildStep } from '@/types/pipeline'

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
  steps.push({ name: `step-${steps.length + 1}`, run: '' })
  commit(steps)
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
      </div>
      <div class="step-editor__ops">
        <el-button size="small" text :disabled="index === 0" @click="move(index, -1)">↑</el-button>
        <el-button size="small" text :disabled="index === modelValue.length - 1" @click="move(index, 1)">↓</el-button>
        <el-button size="small" text type="danger" @click="removeStep(index)">删除</el-button>
      </div>
    </div>

    <el-button size="small" type="primary" plain @click="addStep">+ 添加构建步骤</el-button>
    <div class="form-tip">步骤串行执行，任一步退出码非零即终止后续步骤并标记构建失败。</div>
  </div>
</template>

<style scoped>
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

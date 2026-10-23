<script setup lang="ts">
import { computed } from 'vue'
import type { ArtifactConfig } from '@/types/pipeline'

const props = defineProps<{ modelValue: ArtifactConfig }>()

const emit = defineEmits<{
  (event: 'update:modelValue', value: ArtifactConfig): void
}>()

const config = computed(() => props.modelValue)

const PRESETS: Array<{ label: string; files: string[] }> = [
  { label: 'Go 可执行文件', files: ['app', 'app.exe'] },
  { label: 'Maven JAR', files: ['target/*.jar'] },
  { label: 'Java JAR', files: ['*.jar'] },
  { label: 'Node 前端 dist', files: ['dist/**'] },
]

function patch(part: Partial<ArtifactConfig>): void {
  emit('update:modelValue', { ...props.modelValue, ...part })
}

function updateFiles(value: string[]): void {
  patch({ files: value })
}

function applyPreset(files: string[]): void {
  const merged = [...config.value.files]
  for (const file of files) {
    if (!merged.includes(file)) {
      merged.push(file)
    }
  }
  patch({ files: merged })
}
</script>

<template>
  <el-collapse class="artifact-config">
    <el-collapse-item name="artifacts">
      <template #title>
        <span class="artifact-config__title">归档构建产物（Archive the artifacts）</span>
        <el-tag v-if="config.enabled" type="success" effect="plain" size="small" disable-transitions>
          已启用
        </el-tag>
        <el-tag v-else type="info" effect="plain" size="small" disable-transitions>未启用</el-tag>
      </template>

      <el-alert
        type="info"
        :closable="false"
        show-icon
        class="artifact-config__alert"
        title="每次构建成功后，把匹配文件复制到独立目录，旧版本不会被下次构建覆盖"
        description="保存位置：data/jobs/{任务ID}/builds/{构建编号}/artifacts/；出问题时可在构建页下载任意历史产物回滚。目录受「丢弃旧的构建」策略控制，想保留全部构建请把保留数量设为 0。"
      />

      <el-form label-width="140px" label-position="right">
        <el-form-item label="启用产物归档">
          <el-switch
            :model-value="config.enabled"
            @update:model-value="(value: boolean) => patch({ enabled: value })"
          />
          <span class="form-tip">构建步骤成功后才归档；SSH 发布失败也会保留本次产物。</span>
        </el-form-item>

        <template v-if="config.enabled">
          <el-form-item label="产物匹配模式">
            <el-select
              :model-value="config.files"
              multiple
              filterable
              allow-create
              default-first-option
              class="artifact-config__files"
              placeholder="输入 glob 后回车，如 target/*.jar"
              @update:model-value="updateFiles"
            >
              <el-option v-for="pattern in config.files" :key="pattern" :label="pattern" :value="pattern" />
            </el-select>
            <div class="form-tip">
              相对构建工作区的 glob，支持 <span class="mono">*</span>、<span class="mono">**</span>、<span class="mono">?</span>；
              至少匹配一个文件，否则构建会失败并提示，避免“构建成功但没有产物”。
            </div>
            <div class="artifact-config__presets">
              <span class="muted">快捷填充：</span>
              <el-button
                v-for="preset in PRESETS"
                :key="preset.label"
                size="small"
                plain
                @click="applyPreset(preset.files)"
              >
                {{ preset.label }}
              </el-button>
            </div>
          </el-form-item>

          <el-form-item label="路径示例">
            <div class="artifact-config__examples">
              <div><span class="mono">app.exe</span> / <span class="mono">app</span>：Go 编译出的可执行文件</div>
              <div><span class="mono">target/*.jar</span>：Maven 打包结果</div>
              <div><span class="mono">dist/**</span>：Node 前端构建目录（递归）</div>
              <div><span class="mono">*.jar</span>：工作区根目录下的 jar</div>
            </div>
          </el-form-item>
        </template>
      </el-form>
    </el-collapse-item>
  </el-collapse>
</template>

<style scoped>
.artifact-config__title {
  margin-right: 8px;
  font-weight: 500;
}
.artifact-config__alert {
  margin-bottom: 16px;
}
.artifact-config__files {
  width: 100%;
}
.artifact-config__presets {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
  margin-top: 8px;
}
.artifact-config__presets .el-button {
  margin-left: 0;
}
.artifact-config__examples {
  color: var(--el-text-color-secondary);
  font-size: 12px;
  line-height: 1.9;
}
</style>

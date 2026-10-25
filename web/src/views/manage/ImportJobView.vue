<script setup lang="ts">
// 系统管理 → 导入任务：两种来源，同一套「先预览（dry-run）→ 看告警 → 再导入」交互。
//   1. Jenkins config.xml —— XML → 平台任务（语义翻译）
//   2. 平台 config.json  —— 平台导出文档，用于实例间搬迁
// 迁移最容易出的事故是「导进去了但配置悄悄丢了」，所以告警必须挡在落盘之前。
import { computed, ref } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import ManagePageHeader from '@/components/ManagePageHeader.vue'
import { importApi } from '@/api/import'
import { describeError } from '@/api/request'
import type {
  ImportMode,
  ImportWarning,
  JenkinsImportResult,
  PlatformImportResult,
} from '@/types/import'

const router = useRouter()

type Source = 'jenkins' | 'platform'
const tab = ref<Source>('jenkins')

const content = ref('')
const name = ref('')
const mode = ref<ImportMode>('create')
const keepSource = ref(true)
const busy = ref(false)
const previewing = ref(false)
const result = ref<JenkinsImportResult | PlatformImportResult | null>(null)

const modeOptions: Array<{ value: ImportMode; label: string; tip: string }> = [
  { value: 'create', label: '新建（重名自动加后缀）', tip: '最安全：绝不会覆盖已有任务' },
  { value: 'update', label: '覆盖同名任务', tip: '迁移中断后重跑时使用' },
  { value: 'skip', label: '已存在则跳过', tip: '批量导入时避免重复处理' },
]

const sourceMeta = computed(() =>
  tab.value === 'jenkins'
    ? {
        label: 'Jenkins config.xml',
        placeholder: '粘贴 config.xml 内容，例如从 http://jenkins/job/NAME/config.xml 获取',
        accept: '.xml,application/xml,text/xml',
        nameRequired: true,
      }
    : {
        label: '平台 config.json',
        placeholder: '粘贴导出的 config.json（任务页「导出配置」下载的文件）',
        accept: '.json,application/json',
        nameRequired: false,
      },
)

const errors = computed(() => warningsOf('error'))
const warns = computed(() => warningsOf('warn'))
const infos = computed(() => warningsOf('info'))

function warningsOf(level: ImportWarning['level']): ImportWarning[] {
  return (result.value?.warnings ?? []).filter((w) => w.level === level)
}

const actionText = computed(() => {
  switch (result.value?.action) {
    case 'preview':
      return '预览结果（尚未写入磁盘）'
    case 'created':
      return '已创建任务'
    case 'updated':
      return '已更新任务'
    case 'skipped':
      return '已存在同名任务，已跳过'
    default:
      return ''
  }
})

// 预览结果按 JSON 展示：用户能直观看到「转换后到底是什么配置」。
const previewJSON = computed(() => (result.value ? JSON.stringify(result.value.pipeline, null, 2) : ''))

function onTabChange(): void {
  // 切换来源时清空上一次的结果，避免把 XML 的预览误当成 JSON 的结果。
  result.value = null
  content.value = ''
  name.value = ''
}

function onFile(event: Event): void {
  const input = event.target as HTMLInputElement
  const file = input.files?.[0]
  if (!file) return
  const reader = new FileReader()
  reader.onload = () => {
    content.value = String(reader.result ?? '')
    // Jenkins 导出习惯是 <任务名>.xml / <任务名>-config.json，用文件名兜底任务名。
    if (!name.value.trim()) {
      name.value = file.name.replace(/\.xml$/i, '').replace(/-config\.json$/i, '').replace(/\.json$/i, '')
    }
    result.value = null
    ElMessage.success(`已读取 ${file.name}`)
  }
  reader.onerror = () => ElMessage.error('读取文件失败')
  reader.readAsText(file)
  input.value = ''
}

function validateInput(): boolean {
  if (!content.value.trim()) {
    ElMessage.warning(tab.value === 'jenkins' ? '请粘贴或上传 Jenkins 的 config.xml' : '请粘贴或上传平台导出的 config.json')
    return false
  }
  // Jenkins 的 config.xml 不含任务名，必须手填；平台配置文档自带 name，可留空。
  if (tab.value === 'jenkins' && !name.value.trim()) {
    ElMessage.warning('请填写任务名（Jenkins 的 config.xml 不含任务名）')
    return false
  }
  return true
}

function run(dryRun: boolean): Promise<JenkinsImportResult | PlatformImportResult> {
  const trimmedName = name.value.trim()
  if (tab.value === 'jenkins') {
    return importApi.jenkins(content.value, {
      name: trimmedName,
      mode: mode.value,
      dryRun,
      keepSource: keepSource.value,
    })
  }
  return importApi.config(content.value, { name: trimmedName, mode: mode.value, dryRun })
}

async function onPreview(): Promise<void> {
  if (!validateInput()) return
  previewing.value = true
  try {
    result.value = await run(true)
  } catch (e) {
    ElMessage.error(describeError(e))
  } finally {
    previewing.value = false
  }
}

async function onImport(): Promise<void> {
  if (!validateInput()) return
  busy.value = true
  try {
    const res = await run(false)
    result.value = res
    const target = res.pipeline
    ElMessage.success(`导入完成：${target.name}（#${target.id}）`)
    // 直接跳到任务页，方便立刻核对配置并试跑一次。
    if (target.id) {
      await router.push({ name: 'build-history', params: { pipelineId: target.id } })
    }
  } catch (e) {
    ElMessage.error(describeError(e))
  } finally {
    busy.value = false
  }
}

const levelType: Record<ImportWarning['level'], 'danger' | 'warning' | 'info'> = {
  error: 'danger',
  warn: 'warning',
  info: 'info',
}
</script>

<template>
  <div class="page manage">
    <ManagePageHeader
      title="导入任务"
      description="从 Jenkins（config.xml）或平台导出文件（config.json）导入任务配置"
    />

    <el-alert type="info" :closable="false" show-icon class="import__intro">
      <template #title>先预览、再导入</template>
      <div v-if="tab === 'jenkins'">
        config.xml 里的<b>凭据</b>（Git 密码/私钥）与 <b>Publish Over SSH 服务器</b>无法自动迁移：
        凭据需在「凭据管理」重建，SSH 服务器需先在「系统配置 → SSH 服务器」建好，
        导入时会按服务器名自动匹配。Jenkinsfile（Pipeline 脚本）任务无法自动转换，需拆成 shell 步骤。
      </div>
      <div v-else>
        平台配置文档同样<b>不含密钥</b>（SSH 密码、Webhook 密钥、群机器人 token）：
        导入后需在任务配置里重新填写。SSH 服务器与 Git 凭据按<b>名称</b>自动对齐到目标实例。
      </div>
    </el-alert>

    <el-tabs v-model="tab" @tab-change="onTabChange">
      <el-tab-pane label="从 Jenkins 导入（config.xml）" name="jenkins" />
      <el-tab-pane label="从平台配置导入（config.json）" name="platform" />
    </el-tabs>

    <el-row :gutter="16">
      <el-col :span="14">
        <el-card shadow="never">
          <template #header><span class="import__card-title">{{ sourceMeta.label }}</span></template>
          <el-form label-width="110px" label-position="right">
            <el-form-item label="任务名" :required="sourceMeta.nameRequired">
              <el-input
                v-model="name"
                :placeholder="sourceMeta.nameRequired ? '如 order-service（config.xml 不含任务名）' : '留空则使用文档里的任务名'"
              />
            </el-form-item>
            <el-form-item label="重名处理">
              <el-select v-model="mode" style="width: 100%">
                <el-option v-for="opt in modeOptions" :key="opt.value" :label="opt.label" :value="opt.value">
                  <div class="import__option">
                    <span>{{ opt.label }}</span>
                    <span class="muted">{{ opt.tip }}</span>
                  </div>
                </el-option>
              </el-select>
            </el-form-item>
            <el-form-item v-if="tab === 'jenkins'" label="保留源 XML">
              <el-switch v-model="keepSource" />
              <div class="form-tip">保存为 data/jobs/{{ '{id}' }}/jenkins-config.xml，便于审计与后续重导。</div>
            </el-form-item>
            <el-form-item label="上传文件">
              <input type="file" :accept="sourceMeta.accept" @change="onFile" />
            </el-form-item>
            <el-form-item label="文件内容">
              <el-input
                v-model="content"
                type="textarea"
                :rows="16"
                class="mono"
                :placeholder="sourceMeta.placeholder"
              />
            </el-form-item>
            <el-form-item>
              <el-button :loading="previewing" @click="onPreview">预览转换结果</el-button>
              <el-button type="primary" :loading="busy" @click="onImport">导入</el-button>
            </el-form-item>
          </el-form>
        </el-card>
      </el-col>

      <el-col :span="10">
        <el-card shadow="never">
          <template #header>
            <div class="import__preview-head">
              <span class="import__card-title">转换结果</span>
              <el-tag v-if="result" size="small" effect="plain">{{ actionText }}</el-tag>
            </div>
          </template>
          <el-empty v-if="!result" description="先点「预览转换结果」查看告警与配置" :image-size="80" />
          <template v-else>
            <div v-if="result.warnings.length" class="import__warnings">
              <div v-for="group in [
                { level: 'error', list: errors, title: '必须处理' },
                { level: 'warn', list: warns, title: '已降级' },
                { level: 'info', list: infos, title: '提示' },
              ]" :key="group.level">
                <template v-if="group.list.length">
                  <div class="import__warning-title">
                    <el-tag size="small" :type="levelType[group.level as ImportWarning['level']]">
                      {{ group.title }}（{{ group.list.length }}）
                    </el-tag>
                  </div>
                  <div
                    v-for="(warning, index) in group.list"
                    :key="group.level + index"
                    class="import__warning"
                  >
                    <span class="mono import__warning-field">{{ warning.field }}</span>
                    <span>{{ warning.message }}</span>
                  </div>
                </template>
              </div>
            </div>
            <el-alert v-else type="success" :closable="false" show-icon title="没有发现需要注意的差异" />
            <pre class="import__json mono">{{ previewJSON }}</pre>
          </template>
        </el-card>
      </el-col>
    </el-row>
  </div>
</template>

<style scoped>
.import__intro {
  margin-bottom: 16px;
}
.import__card-title {
  font-weight: 600;
}
.import__option {
  display: flex;
  flex-direction: column;
  line-height: 1.4;
}
.import__preview-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
}
.import__warnings {
  display: flex;
  flex-direction: column;
  gap: 10px;
  margin-bottom: 12px;
}
.import__warning-title {
  margin-bottom: 4px;
}
.import__warning {
  display: flex;
  gap: 8px;
  font-size: 12.5px;
  line-height: 1.6;
  padding: 4px 0;
  border-bottom: 1px dashed var(--border-color, #ebeef5);
}
.import__warning-field {
  flex: none;
  color: var(--text-3, #909399);
}
.import__json {
  max-height: 420px;
  overflow: auto;
  background: var(--bg-code, #f6f8fa);
  border-radius: 6px;
  padding: 10px 12px;
  font-size: 12px;
  line-height: 1.5;
  white-space: pre-wrap;
  word-break: break-word;
}
</style>

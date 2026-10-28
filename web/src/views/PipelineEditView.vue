<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import ArtifactConfig from '@/components/ArtifactConfig.vue'
import NotifyConfig from '@/components/NotifyConfig.vue'
import ParamConfig from '@/components/ParamConfig.vue'
import StepEditor from '@/components/StepEditor.vue'
import SshPublishEditor from '@/components/SshPublishConfig.vue'
import { describeError } from '@/api/request'
import { pipelineApi } from '@/api/pipeline'
import { credentialApi } from '@/api/system'
import { usePipeline } from '@/composables/usePipeline'
import { useSettingsStore } from '@/stores/settingsStore'
import { useAuthStore } from '@/stores/authStore'
import {
  emptyPipeline,
  emptyEscalation,
  emptyOnCall,
  emptyRoute,
  emptySshPublish,
  noNotifyEvents,
  type NotifyContact,
  type NotifyTarget,
  type Pipeline,
  type PipelinePayload,
  type SshPublishConfig,
} from '@/types/pipeline'
import type { Credential } from '@/types/system'
import { applyJobTypePreset, findJobType } from '@/types/jobType'
import { validatePipeline } from '@/utils/validate'
import { normalizeSshPublish, sshPublishIsBlank } from '@/utils/pipelineConfig'
import { downloadText } from '@/utils/download'

const props = defineProps<{ pipelineId?: string }>()

const route = useRoute()
const router = useRouter()
const { loading, fetchOne, save } = usePipeline()
const settingsStore = useSettingsStore()
const auth = useAuthStore()

const isEdit = ref(false)
// Jenkins 2.x 的 Configure 页是 General / Triggers / 流水线 / Advanced 四个页签；
// 这里保留同样的信息架构，避免长页面滚动时找不到配置项。
type ConfigureTab = 'general' | 'triggers' | 'pipeline' | 'advanced'
const activeTab = ref<ConfigureTab>('general')
const globalQuietPeriod = computed(() => settingsStore.data?.quiet_period_seconds ?? 0)
// 编辑既有任务时的配置权限（按任务名判断，普通用户可能只被授权部分任务）。
const canEditExisting = ref(true)
const saving = ref(false)
const form = ref<PipelinePayload>(emptyPipeline())
const credentials = ref<Credential[]>([])

// 通知：人员联系方式（选人用）与测试发送状态。
const notifyContacts = ref<NotifyContact[]>([])
const testingNotify = ref(false)

async function loadNotifyContacts(): Promise<void> {
  try {
    notifyContacts.value = await pipelineApi.notifyContacts()
  } catch {
    // 无权限（如仅有任务角色）时保持空列表，仍可手动输入用户名。
    notifyContacts.value = []
  }
}

async function onTestNotify(): Promise<void> {
  if (!props.pipelineId) {
    ElMessage.warning('请先保存任务，再发送测试通知')
    return
  }
  testingNotify.value = true
  try {
    const res = await pipelineApi.testNotify(props.pipelineId)
    if (res.errors?.length) {
      ElMessage.warning(`测试通知部分失败：${res.errors.join('；')}`)
    } else {
      ElMessage.success(res.summary || '测试通知已发送')
    }
  } catch (e) {
    ElMessage.error(describeError(e))
  } finally {
    testingNotify.value = false
  }
}

const credDialog = ref(false)
const credSaving = ref(false)
const credForm = reactive({ name: '', username: '', password: '' })

const jobTypeTitle = computed(() => findJobType(jobTypeId())?.title ?? '')

function jobTypeId(): string {
  const value = route.query.type
  return typeof value === 'string' ? value : ''
}

// 任务模板：copy 模式下从源任务复制配置（New Item 的 Copy from）。
function copySourceId(): string {
  const value = route.query.copy
  return typeof value === 'string' ? value : ''
}

// 源码管理：无 / Git（独立状态，避免由 git_url 推导导致无法切换）。
// 切换为“无”时不清空已填内容，保存时再按 scm 决定是否提交 Git 配置，
// 避免用户误切后丢失仓库地址与已选凭据。
const scm = ref('none')

// 丢弃旧的构建：勾选后至少保留一个维度
const retentionEnabled = computed({
  get: () => form.value.build_retention_max_per_job > 0 || form.value.build_retention_max_age_days > 0,
  set: (value: boolean) => {
    if (value) {
      if (form.value.build_retention_max_per_job <= 0 && form.value.build_retention_max_age_days <= 0) {
        form.value.build_retention_max_per_job = 10
      }
    } else {
      form.value.build_retention_max_per_job = 0
      form.value.build_retention_max_age_days = 0
    }
  },
})

// 服务端不会回传密钥，只告诉我们是否已配置；用于在编辑页提示当前状态。
const webhookSecretSet = ref(false)

function generateWebhookSecret(): void {
  const bytes = new Uint8Array(24)
  crypto.getRandomValues(bytes)
  form.value.webhook_secret = Array.from(bytes, (b) => b.toString(16).padStart(2, '0')).join('')
  form.value.clear_webhook_secret = false
  webhookSecretSet.value = false
}

function clearWebhookSecret(): void {
  form.value.webhook_secret = ''
  form.value.clear_webhook_secret = true
  webhookSecretSet.value = false
}

const webhookUrl = computed(() => {
  const base = (settingsStore.data?.jenkins_url || window.location.origin).replace(/\/$/, '')
  const id = props.pipelineId ?? '{id}'
  return `${base}/api/webhook/${id}`
})

function scrollTo(id: string): void {
  document.getElementById(id)?.scrollIntoView({ behavior: 'smooth', block: 'start' })
}

function toPayload(pipeline: Pipeline): PipelinePayload {
  return {
    name: pipeline.name,
    description: pipeline.description,
    image: '',
    steps: pipeline.steps.map((step) => ({ ...step })),
    git_url: pipeline.git_url,
    git_branch: pipeline.git_branch,
    // 每个任务的工具版本选择必须原样带回（只改描述也不能丢）
    tool_selections: { ...(pipeline.tool_selections ?? {}) },
    git_credential_id: pipeline.git_credential_id ?? '',
    webhook_secret: pipeline.webhook_secret,
    // 触发器策略与状态回写：编辑/只改描述时都必须原样带回，否则保存会清空配置。
    webhook_branch_mode: pipeline.webhook_branch_mode ?? '',
    webhook_branches: [...(pipeline.webhook_branches ?? [])],
    webhook_events: [...(pipeline.webhook_events ?? [])],
    commit_status: {
      enabled: pipeline.commit_status?.enabled ?? false,
      credential_id: pipeline.commit_status?.credential_id ?? '',
      project: pipeline.commit_status?.project ?? '',
    },
    build_retention_max_per_job: pipeline.build_retention_max_per_job ?? 0,
    build_retention_max_age_days: pipeline.build_retention_max_age_days ?? 0,
    cron_spec: pipeline.cron_spec ?? '',
    delete_workspace_before_build: pipeline.delete_workspace_before_build ?? false,
    executor: pipeline.executor ?? 'local',
    artifacts: {
      enabled: pipeline.artifacts?.enabled ?? false,
      files: [...(pipeline.artifacts?.files ?? [])],
    },
    // 参数定义：深拷贝 options，避免编辑表单时改到 store 里的任务对象。
    params: (pipeline.params ?? []).map((def) => ({
      ...def,
      options: [...(def.options ?? [])],
    })),
    // 通知：任务级配置（人员/群/事件）；null 表示沿用全局默认。
    notify: pipeline.notify
      ? {
          enabled: pipeline.notify.enabled,
          use_global: pipeline.notify.use_global,
          people: [...(pipeline.notify.people ?? [])],
          emails: [...(pipeline.notify.emails ?? [])],
          // 目标事件用 noNotifyEvents() 兜底：后端「全 false」= 继承任务事件，
          // 若用 emptyNotifyEvents()（failure=true）兜底，打开再保存会把「继承」
          // 静默改成「只发失败+修复」，属于回写破坏配置。
          targets: mapNotifyTargets(pipeline.notify.targets),
          events: { ...noNotifyEvents(), ...(pipeline.notify.events ?? {}) },
          // 运维增强：默认值兜底，确保「打开再保存」不会丢字段。
          on_call: { ...emptyOnCall(), ...(pipeline.notify.on_call ?? {}) },
          escalation: {
            ...emptyEscalation(),
            ...(pipeline.notify.escalation ?? {}),
            people: [...(pipeline.notify.escalation?.people ?? [])],
            emails: [...(pipeline.notify.escalation?.emails ?? [])],
            targets: mapNotifyTargets(pipeline.notify.escalation?.targets),
          },
          routes: (pipeline.notify.routes ?? []).map((r) => ({
            ...emptyRoute(),
            ...r,
            people: [...(r.people ?? [])],
            emails: [...(r.emails ?? [])],
            targets: mapNotifyTargets(r.targets),
          })),
          suppress_window_min: pipeline.notify.suppress_window_min ?? 0,
          template: pipeline.notify.template ?? '',
        }
      : null,
    // 敏感字段不回显：密码置空，服务端会沿用旧值。
    ssh_publish: null,
    ssh_publishes: sshPublishesOf(pipeline),
  }
}

// sshPublishesOf 把接口返回的发布配置归一为列表：
// 新数据用 ssh_publishes；旧数据只有 ssh_publish（单目标），迁移成一个目标。
function sshPublishesOf(pipeline: Pipeline): SshPublishConfig[] {
  const raw =
    pipeline.ssh_publishes && pipeline.ssh_publishes.length > 0
      ? pipeline.ssh_publishes
      : pipeline.ssh_publish
        ? [pipeline.ssh_publish]
        : []
  // 统一走 normalizeSshPublish：服务端带 omitempty 的字段可能缺失，
  // 补齐后表单里永远是确定的字符串/数组（否则 .trim() 会在导入的任务上崩）。
  const list = raw.map((item, index) => normalizeSshPublish(item, index))
  return list.length > 0 ? list : [emptySshPublish()]
}

// 导出配置：只读接口，不会保存当前未保存的修改（避免把半成品导出成「正式配置」）。
const exporting = ref(false)
async function onExport(): Promise<void> {
  if (!props.pipelineId) return
  exporting.value = true
  try {
    const text = await pipelineApi.exportConfig(props.pipelineId)
    downloadText(`${form.value.name || props.pipelineId}-config.json`, text)
    ElMessage.success('配置已导出（不含密钥）')
  } catch (e) {
    ElMessage.error(describeError(e))
  } finally {
    exporting.value = false
  }
}

// paramCount 与 envTools 只用于在配置页展示「当前实际生效」的状态，
// 避免出现「页面写着不支持、其实已经支持」这类误导性提示。
const paramCount = computed(() => form.value.params?.length ?? 0)

// envTools 列出与本任务相关的工具类别，以及全局工具配置里可选的安装项。
// 对齐 Jenkins：每个任务自己选 JDK / NodeJS / Maven 版本，而不是全局一刀切。
const envTools = computed(() => {
  const categories = settingsStore.data?.tool_categories ?? []
  const wanted: Array<{ id: string; label: string; env: string }> = [
    { id: 'jdk', label: 'JDK', env: 'JAVA_HOME' },
    { id: 'nodejs', label: 'NodeJS', env: 'NODE_HOME' },
    { id: 'maven', label: 'Maven', env: 'MAVEN_HOME' },
    { id: 'gradle', label: 'Gradle', env: 'GRADLE_HOME' },
    { id: 'golang', label: 'Go', env: 'GOROOT' },
  ]
  return wanted.map((item) => {
    const category = categories.find((c) => c.id === item.id)
    const installations = category?.installations ?? []
    // 第一个可用的安装项 = 未选择时的实际生效项（与后端规则一致）
    const fallback = installations.find((i) => i.install_mode === 'auto' || (i.home ?? '').trim() !== '')
    const picked = form.value.tool_selections?.[item.id]
    const effective = installations.find((i) => i.name === picked) ?? fallback
    return { ...item, installations, fallback, effective, picked: picked ?? '' }
  })
})

// installationLabel 展示「名称 + 版本/路径」，让选择时就能看出用的是哪个版本。
function installationLabel(item: { name: string; install_mode?: string; version?: string; home?: string }): string {
  if (item.install_mode === 'auto') {
    return `${item.name}（自动安装 ${item.version ?? ''}）`
  }
  return item.home ? `${item.name}（${item.home}）` : item.name
}

// effectiveToolText 显示该类别实际会用的东西：路径 / 自动安装 / 未配置。
function effectiveToolText(tool: { effective?: { install_mode?: string; version?: string; home?: string }; env: string }): string {
  if (!tool.effective) {
    return `未配置（按系统 PATH 执行）`
  }
  if (tool.effective.install_mode === 'auto') {
    return tool.effective.home
      ? tool.effective.home
      : `自动安装 ${tool.effective.version}（构建时下载）`
  }
  return tool.effective.home || '未填写路径（按系统 PATH 执行）'
}

function setToolSelection(categoryId: string, name: string): void {
  const selections = { ...(form.value.tool_selections ?? {}) }
  if (!name) {
    delete selections[categoryId]
  } else {
    selections[categoryId] = name
  }
  form.value.tool_selections = selections
}

function addSshPublish(): void {
  const list = form.value.ssh_publishes ?? []
  form.value.ssh_publishes = [...list, emptySshPublish()]
}

function removeSshPublish(index: number): void {
  const list = form.value.ssh_publishes ?? []
  form.value.ssh_publishes = list.filter((_, i) => i !== index)
}

onMounted(async () => {
  // 强制刷新：确保刚在「系统配置」中新增的 SSH 服务器/工具主目录能立即生效。
  await Promise.all([settingsStore.fetch(true), loadCredentials(), loadNotifyContacts()])
  if (!props.pipelineId) {
    // 模板模式：从源任务复制配置；拉取失败（404/无权限）回退到类型预设。
    const sourceId = copySourceId()
    if (sourceId) {
      const source = await fetchOne(sourceId)
      if (source !== null) {
        form.value = toPayload(source)
        form.value.name = ''
        const nameFromQuery = route.query.name
        if (typeof nameFromQuery === 'string' && nameFromQuery.trim()) {
          form.value.name = nameFromQuery.trim()
        }
        // 群机器人 URL 在接口返回时已脱敏为 ***；新建任务没有旧配置可回填，
        // 直接保存会把 *** 当成真实地址导致通知永久失效，故清空并要求重填。
        const stripped = stripMaskedNotifyURLs(form.value.notify)
        if (stripped > 0) {
          ElMessage.warning(`已复制配置，但有 ${stripped} 个群机器人的 URL 含密钥无法复制，请重新填写`)
        }
        scm.value = form.value.git_url ? 'git' : 'none'
        return
      }
      ElMessage.error('模板任务不存在或无权访问，已回退到空白配置')
    }
    const preset = applyJobTypePreset(jobTypeId())
    if (!preset.git_branch || preset.git_branch === 'main') {
      preset.git_branch = settingsStore.defaultBranch
    }
    form.value = preset
    const nameFromQuery = route.query.name
    if (typeof nameFromQuery === 'string' && nameFromQuery.trim()) {
      form.value.name = nameFromQuery.trim()
    }
    scm.value = form.value.git_url ? 'git' : 'none'
    return
  }
  isEdit.value = true
  const pipeline = await fetchOne(props.pipelineId)
  if (pipeline !== null) {
    // 任务级权限：普通用户可能只有「某一个任务」的配置权限。
    canEditExisting.value = auth.canJob('job.configure', pipeline.name)

    form.value = toPayload(pipeline)
    webhookSecretSet.value = pipeline.has_webhook_secret ?? false
    scm.value = form.value.git_url ? 'git' : 'none'
  }
})

// mapNotifyTargets 统一映射群目标：events 用「全 false」兜底（= 继承任务事件），
// 不能用 emptyNotifyEvents()，否则「打开再保存」会把继承改成只发失败+修复。
function mapNotifyTargets(targets: NotifyTarget[] | undefined): NotifyTarget[] {
  return (targets ?? []).map((t) => ({
    channel: t.channel,
    url: t.url,
    events: { ...noNotifyEvents(), ...(t.events ?? {}) },
  }))
}

// stripMaskedNotifyURLs 清空含 *** 的群机器人 URL（复制任务时无旧值可回填），
// 升级群与分支路由的群同样会脱敏，必须一并处理；返回被清空的数量。
function stripMaskedNotifyURLs(notify: PipelinePayload['notify']): number {
  if (!notify) {
    return 0
  }
  let count = 0
  const strip = (targets: NotifyTarget[] | undefined): void => {
    for (const target of targets ?? []) {
      if (target.url.includes('***')) {
        target.url = ''
        count++
      }
    }
  }
  strip(notify.targets)
  strip(notify.escalation?.targets)
  for (const route of notify.routes ?? []) {
    strip(route.targets)
  }
  return count
}

async function loadCredentials(): Promise<void> {
  try {
    const all = await credentialApi.list()
    // SSH 私钥不适用于 HTTPS 拉取，这里只展示账号密码与令牌。
    credentials.value = all.filter((c) => c.type === 'username_password' || c.type === 'secret_text')
  } catch {
    // 保留上一次的列表：一次网络抖动不应让已选凭据在界面上“消失”。
  }
}

// 内联“新增 Git 账号”弹窗的草稿：只记住名称与用户名（JSON 存 localStorage），
// 密码不落浏览器；凭据本身由服务端以 JSON 持久化。
const GIT_CRED_DRAFT_KEY = 'ci.gitCredentialDraft'

interface GitCredDraft {
  name: string
  username: string
}

function loadCredDraft(): GitCredDraft | null {
  try {
    const raw = localStorage.getItem(GIT_CRED_DRAFT_KEY)
    if (!raw) return null
    const value = JSON.parse(raw) as Partial<GitCredDraft>
    return {
      name: typeof value.name === 'string' ? value.name : '',
      username: typeof value.username === 'string' ? value.username : '',
    }
  } catch {
    return null
  }
}

function saveCredDraft(): void {
  try {
    localStorage.setItem(
      GIT_CRED_DRAFT_KEY,
      JSON.stringify({ name: credForm.name.trim(), username: credForm.username.trim() }),
    )
  } catch {
    // localStorage 不可用时忽略（不影响凭据落盘）。
  }
}

function openCredDialog(): void {
  const draft = loadCredDraft()
  credForm.name = draft?.name ?? ''
  credForm.username = draft?.username ?? ''
  credForm.password = ''
  credDialog.value = true
}

async function saveCredential(): Promise<void> {
  const name = credForm.name.trim()
  const username = credForm.username.trim()
  const password = credForm.password

  if (!name) {
    ElMessage.error('请填写凭据名称')
    return
  }
  if (!username || !password) {
    ElMessage.error('请填写 Git 用户名与密码')
    return
  }

  // 同名同用户名的凭据直接复用，避免重复创建后下拉里出现一堆同名账号。
  const existing = credentials.value.find(
    (c) => c.name === name && (c.username ?? '') === username,
  )
  if (existing) {
    form.value.git_credential_id = existing.id
    saveCredDraft()
    credDialog.value = false
    ElMessage.success(`已选择已有凭据「${existing.name}」`)
    return
  }

  credSaving.value = true
  try {
    let gitHost = ''
    try {
      gitHost = new URL(form.value.git_url).hostname.toLowerCase()
    } catch {
      // URL 尚未填完整时允许创建不带作用域的凭据，稍后可在凭据管理里补。
    }
    const created = await credentialApi.create({
      name,
      type: 'username_password',
      username,
      description: 'Git 账号密码',
      secret: password,
      allowed_hosts: gitHost ? [gitHost] : [],
    })
    saveCredDraft()
    // 先本地插入：即使随后列表刷新失败，新建的账号也能被选中并显示。
    credentials.value = [created, ...credentials.value.filter((c) => c.id !== created.id)]
    await loadCredentials()
    form.value.git_credential_id = created.id
    credDialog.value = false
    ElMessage.success('凭据已保存到凭据库并选中')
  } catch (e) {
    ElMessage.error(describeError(e))
  } finally {
    credSaving.value = false
  }
}

async function onSave(): Promise<void> {
  if (!canEditExisting.value) {
    ElMessage.error('没有该任务的配置权限')
    return
  }
  const errors = validatePipeline(form.value, { keepSshPassword: isEdit.value })
  if (errors.length > 0) {
    ElMessage.error(errors[0])
    return
  }
  saving.value = true
  try {
    // 关闭的发布目标仍保留已填写内容（与 Jenkins 一致），只丢弃完全空白的行。
    const publishes = (form.value.ssh_publishes ?? [])
      .map((item, index) => normalizeSshPublish(item, index))
      .filter((item) => !sshPublishIsBlank(item))
    const payload: PipelinePayload = {
      ...form.value,
      // 只有选择 Git 时才提交仓库与凭据；切回“无”后保存即禁用，但切回 Git 时已填内容仍在。
      git_url: scm.value === 'git' ? form.value.git_url.trim() : '',
      git_credential_id: scm.value === 'git' ? form.value.git_credential_id : '',
      ssh_publish: null,
      ssh_publishes: publishes,
    }
    const pipeline = await save(props.pipelineId ?? null, payload)
    ElMessage.success(isEdit.value ? '已保存' : `任务已创建（id=${pipeline.id}）`)
    void router.push({ name: 'pipeline-list' })
  } catch (e) {
    ElMessage.error(describeError(e))
  } finally {
    saving.value = false
  }
}

function onCancel(): void {
  if (isEdit.value) {
    void router.push({ name: 'pipeline-list' })
    return
  }
  void router.push({
    name: 'pipeline-create',
    query: {
      ...(form.value.name ? { name: form.value.name } : {}),
      ...(jobTypeId() ? { type: jobTypeId() } : {}),
    },
  })
}

function copyWebhook(): void {
  if (!navigator.clipboard) {
    ElMessage.warning('当前浏览器不支持剪贴板，请手动复制')
    return
  }
  navigator.clipboard
    .writeText(webhookUrl.value)
    .then(() => ElMessage.success('已复制 Webhook 地址'))
    .catch(() => ElMessage.warning('复制失败，请手动复制'))
}
</script>

<template>
  <div class="page configure">
    <div class="page-header">
      <div>
        <h1 class="page-title">
          {{ isEdit ? 'Configure' : '新建任务' }}
          <el-tag v-if="!isEdit && jobTypeTitle" class="type-tag" type="info" effect="plain" disable-transitions>
            {{ jobTypeTitle }}
          </el-tag>
        </h1>
        <div class="muted">{{ isEdit ? `${form.name} · 任务配置` : '构建在服务器本地执行，工具路径由「全局工具配置」提供' }}</div>
      </div>
      <div>
        <el-button @click="onCancel">{{ isEdit ? '返回' : '上一步' }}</el-button>
        <el-button v-if="canEditExisting" type="primary" :loading="saving" @click="onSave">保存</el-button>
        <el-tooltip v-else content="你对这个任务没有「配置」权限（任务角色的 job.configure）" placement="bottom">
          <span><el-button type="primary" disabled>保存</el-button></span>
        </el-tooltip>
      </div>
    </div>

    <el-alert
      v-if="isEdit && !canEditExisting"
      type="warning"
      :closable="false"
      show-icon
      title="你对当前任务没有配置权限，本页仅可查看；保存会被后端拒绝（403）。"
      style="margin-bottom: 12px"
    />

    <nav class="configure__breadcrumb" aria-label="breadcrumb">
      <RouterLink :to="{ name: 'pipeline-list' }">任务</RouterLink>
      <span class="configure__breadcrumb-sep">/</span>
      <span>{{ form.name || '新建任务' }}</span>
      <span class="configure__breadcrumb-sep">/</span>
      <span>Configure</span>
    </nav>

    <el-card v-loading="loading" shadow="never" class="configure__card">
      <el-tabs v-model="activeTab" class="configure__tabs">
        <el-tab-pane label="General" name="general">
        <section id="general" class="config-section">
        <h2 class="config-section__title">General</h2>
        <el-form label-width="160px" label-position="right">
          <el-form-item label="任务名称">
            <el-input v-model="form.name" placeholder="如 java-demo-build" style="max-width: 480px" />
          </el-form-item>
          <el-form-item label="描述">
            <el-input v-model="form.description" type="textarea" :rows="3" placeholder="显示在任务页顶部" />
          </el-form-item>
          <el-form-item label="丢弃旧的构建">
            <el-checkbox v-model="retentionEnabled">丢弃旧的构建</el-checkbox>
          </el-form-item>
          <template v-if="retentionEnabled">
            <el-form-item label="保持构建的天数">
              <el-input-number v-model="form.build_retention_max_age_days" :min="0" :max="3650" />
            </el-form-item>
            <el-form-item label="保持构建的最大个数">
              <el-input-number v-model="form.build_retention_max_per_job" :min="0" :max="10000" />
            </el-form-item>
          </template>
          <el-form-item label="参数化构建过程">
            <div class="inline-row">
              <el-tag v-if="paramCount > 0" type="success" effect="plain" disable-transitions>
                已定义 {{ paramCount }} 个参数
              </el-tag>
              <el-tag v-else type="info" effect="plain" disable-transitions>未定义参数</el-tag>
              <el-button size="small" @click="scrollTo('params')">编辑参数</el-button>
            </div>
            <div class="form-tip">定义参数后，触发构建会先弹出参数表单；参数以环境变量注入步骤并快照进构建记录。</div>
          </el-form-item>
          <el-form-item label="不允许并发构建">
            <el-checkbox :model-value="true" disabled>不允许并发构建</el-checkbox>
            <div class="form-tip">
              平台固定同一任务串行执行（避免多个构建同时写同一工作区），不同任务按 worker 数量并行；
              因此这个 Jenkins 选项在平台中等价于始终勾选。
            </div>
          </el-form-item>
          <el-form-item label="禁用任务">
            <el-alert
              type="info"
              :closable="false"
              show-icon
              title="平台没有「禁用任务」开关：需要暂停自动触发时，清空「定时构建」并删除代码平台的 Webhook 即可；全站暂停请用「系统管理 → 准备关机」。"
            />
          </el-form-item>
        </el-form>
        </section>
        <el-alert
          type="info"
          :closable="false"
          show-icon
          style="margin: 0 0 18px"
        title="GitHub 项目、GitLab Connection、Preserve stashes、Throttle builds 等 Jenkins 插件项未启用；代码仓库在「流水线」配置，GitLab 状态回写在「Triggers」配置。"
      />
        <section id="params" class="config-section">
        <h2 class="config-section__title">参数化构建</h2>
        <p class="config-section__hint">
          定义参数后，点击「构建」会先弹出参数表单；参数以环境变量形式传给构建步骤。
        </p>
        <el-form label-width="160px" label-position="right">
          <el-form-item label="构建参数">
            <ParamConfig v-model="form.params" />
          </el-form-item>
        </el-form>
        </section>
        </el-tab-pane>

        <el-tab-pane label="Triggers" name="triggers" lazy>
        <section id="triggers" class="config-section">
        <h2 class="config-section__title">构建触发器</h2>
        <p class="config-section__hint">根据事件自动触发构建，如代码提交或远程调用。</p>
        <el-form label-width="160px" label-position="right">
          <el-form-item label="触发远程构建">
            <div class="webhook-row">
              <el-input :model-value="webhookUrl" readonly class="mono" />
              <el-button @click="copyWebhook">复制</el-button>
            </div>
            <div class="form-tip">通过该地址触发构建。密钥校验兼容三种来源（任一匹配即通过）：请求头 <span class="mono">X-Webhook-Secret</span>（curl / CI 脚本）、<span class="mono">X-Gitlab-Token</span>（GitLab Webhook）、<span class="mono">X-Hub-Signature-256</span>（GitHub Webhook HMAC）。</div>
          </el-form-item>
          <el-form-item label="Webhook 密钥">
            <div class="webhook-row">
              <el-input
                v-model="form.webhook_secret"
                type="password"
                show-password
                :placeholder="webhookSecretSet ? '已配置；留空保存会保留原密钥' : '留空表示禁用远程触发'"
                style="max-width: 480px"
                @input="form.clear_webhook_secret = false"
              />
              <el-button @click="generateWebhookSecret">生成密钥</el-button>
              <el-button v-if="webhookSecretSet" @click="clearWebhookSecret">清除密钥</el-button>
            </div>
            <div class="form-tip">
              远程触发必须配置密钥；留空保存不会清空已有密钥，填写新值即轮换。任务列表/详情接口永远不会回传密钥。
            </div>
          </el-form-item>
          <el-form-item label="GitLab / GitHub">
            <div class="form-tip">
              GitLab：项目 Settings → Webhooks 中填入上方 URL，Secret token 与「Webhook 密钥」一致（以
              <span class="mono">X-Gitlab-Token</span> 头发送），Push / Tag 事件即可触发构建；内网 HTTP 地址请关闭「SSL verification」。
            </div>
          </el-form-item>
          <el-form-item label="定时构建">
            <el-input
              v-model="form.cron_spec"
              class="mono"
              placeholder="留空表示不启用，如 H/30 * * * * 或 0 2 * * *（每天凌晨 2 点）"
              style="max-width: 480px"
            />
            <div class="form-tip">
              5 字段 cron 规格（分 时 日 月 周），对齐 Jenkins Build periodically。
              支持 <span class="mono">H</span> 散列均衡（如 <span class="mono">H/15 * * * *</span> 每小时 4 次，
              按任务名散列偏移，避免集中触发）；修改后保存即生效，触发记录在构建历史中显示为「定时触发」。
            </div>
          </el-form-item>
          <el-form-item label="允许的事件">
            <el-checkbox-group v-model="form.webhook_events">
              <el-checkbox value="push">Push（分支推送）</el-checkbox>
              <el-checkbox value="tag_push">Tag 推送</el-checkbox>
            </el-checkbox-group>
            <div class="form-tip">
              默认只接受 <span class="mono">Push</span>：Merge Request / 评论 / Pipeline 等事件会被忽略（返回 200 但不构建），
              避免误配置导致每次都白跑。GitLab 网页上点「Test」发的是 Push 事件，会真触发一次构建。
            </div>
          </el-form-item>
          <el-form-item label="构建分支">
            <el-radio-group v-model="form.webhook_branch_mode">
              <el-radio value="payload">按推送的分支构建</el-radio>
              <el-radio value="fixed">始终构建「指定分支」</el-radio>
            </el-radio-group>
            <div class="form-tip">
              选「按推送的分支构建」时，一个任务即可服务所有分支（推 <span class="mono">dev</span> 就构建 dev），
              这是 GitLab CI 的默认心智模型；选另一项则忽略推送分支，始终构建上面配置的那条分支。
            </div>
          </el-form-item>
          <el-form-item label="允许的分支">
            <el-select
              v-model="form.webhook_branches"
              multiple
              filterable
              allow-create
              default-first-option
              :reserve-keyword="false"
              placeholder="留空表示任意分支，例如 main、release/*、hotfix/**"
              style="max-width: 560px; width: 100%"
            >
              <el-option v-for="item in form.webhook_branches ?? []" :key="item" :label="item" :value="item" />
            </el-select>
            <div class="form-tip">
              支持精确名与通配：<span class="mono">release/*</span> 匹配一层，<span class="mono">hotfix/**</span> 匹配多级。
              不在白名单内的推送会返回成功但不构建。
            </div>
          </el-form-item>
          <el-form-item label="跳过 CI">
            <div class="form-tip">
              提交说明包含 <span class="mono">[ci skip]</span> / <span class="mono">[skip ci]</span> 时不会触发构建（对齐 GitLab）。
            </div>
          </el-form-item>
          <el-form-item label="回写构建状态">
            <el-switch v-model="form.commit_status.enabled" />
            <span class="muted" style="margin-left: 10px">把构建状态写回 GitLab，MR 里可直接看到结果</span>
          </el-form-item>
          <template v-if="form.commit_status.enabled">
            <el-form-item label="GitLab 凭据">
              <el-select v-model="form.commit_status.credential_id" placeholder="选择存有 Personal Access Token 的凭据" style="max-width: 420px">
                <el-option v-for="cred in credentials" :key="cred.id" :label="cred.name" :value="cred.id" />
              </el-select>
              <div class="form-tip">
                凭据类型选「密钥文本」，内容填 GitLab 的 Personal Access Token（需要 <span class="mono">api</span> 权限）。
                站点地址在「系统管理 → 系统配置 → GitLab 地址」里配置。
              </div>
            </el-form-item>
            <el-form-item label="GitLab 项目">
              <el-input
                v-model="form.commit_status.project"
                class="mono"
                placeholder="留空表示用 Webhook 事件里的项目，如 ops/order-api 或 项目 ID"
                style="max-width: 480px"
              />
            </el-form-item>
          </template>
          <el-form-item label="其他触发器">
            <el-alert
              type="info"
              :closable="false"
              show-icon
              title="SCM 轮询在当前版本中暂不支持，可用定时构建替代；上游/下游任务链（build after other projects）也不支持。"
            />
          </el-form-item>
        </el-form>
        </section>
        </el-tab-pane>

        <el-tab-pane label="流水线" name="pipeline" lazy>
        <el-alert
          type="info"
          :closable="false"
          show-icon
          style="margin-bottom: 14px"
        title="平台不使用 Jenkinsfile / Groovy 沙盒：流水线由「源码管理 + 构建步骤」定义，构建后操作在下方配置。"
      />
        <section id="scm" class="config-section">
        <h2 class="config-section__title">源码管理</h2>
        <p class="config-section__hint">连接并管理代码仓库，构建时自动拉取最新代码。</p>
        <el-form label-width="160px" label-position="right">
          <el-form-item label="源码管理">
            <el-radio-group v-model="scm">
              <el-radio value="none">无</el-radio>
              <el-radio value="git">Git</el-radio>
            </el-radio-group>
            <div class="form-tip">选择「无」表示直接在工作区执行步骤，不拉取代码。</div>
          </el-form-item>
          <template v-if="scm === 'git'">
            <el-form-item label="Repository URL">
              <el-input v-model="form.git_url" class="mono" placeholder="https://git.example.com/group/repo.git" />
            </el-form-item>
            <el-form-item label="Credentials">
              <div class="cred-row">
                <el-select
                  v-model="form.git_credential_id"
                  clearable
                  filterable
                  placeholder="选择账号密码 / 令牌凭据（私有仓库必填）"
                  style="flex: 1; min-width: 260px"
                >
                  <el-option
                    v-for="cred in credentials"
                    :key="cred.id"
                    :label="cred.username ? `${cred.name}（${cred.username}）` : cred.name"
                    :value="cred.id"
                  />
                </el-select>
                <el-button v-if="auth.hasPerm('credential.create')" @click="openCredDialog">新增账号密码</el-button>
                <RouterLink v-if="auth.hasPerm('credential.view')" :to="{ name: 'system-credentials' }">
                  <el-button>管理凭据</el-button>
                </RouterLink>
              </div>
              <div class="form-tip">私有仓库需要账号密码或访问令牌；凭据保存到凭据库后，构建拉取时自动使用。</div>
            </el-form-item>
            <el-form-item label="指定分支">
              <el-input v-model="form.git_branch" class="mono" placeholder="main（留空默认 main，暂不支持 any）" style="max-width: 320px" />
            </el-form-item>
            <el-form-item label="Additional Behaviours">
              <el-alert type="info" :closable="false" show-icon title="基础版使用浅克隆（depth=1），不提供额外行为扩展。" />
            </el-form-item>
          </template>
        </el-form>
        </section>
        <section id="environment" class="config-section">
        <h2 class="config-section__title">构建环境</h2>
        <p class="config-section__hint">定义构建运行时的上下文，如工作区清理策略。</p>
        <el-form label-width="220px" label-position="right">
          <el-form-item label="Delete workspace before build starts">
            <el-switch v-model="form.delete_workspace_before_build" />
            <el-alert
              v-if="form.delete_workspace_before_build"
              type="warning"
              :closable="false"
              show-icon
              style="margin-top: 8px; width: 100%"
              title="每次构建前删除整个工作区并重新 clone 代码，构建产物与依赖缓存会丢失；仅在需要彻底清理时使用。"
            />
            <div v-else class="form-tip">
              默认复用工作区：Git 只做增量 fetch，保留上次构建产物与依赖缓存。
            </div>
          </el-form-item>
          <el-form-item label="构建工具版本">
            <div class="tool-homes">
              <div v-for="tool in envTools" :key="tool.id" class="tool-homes__item">
                <span class="tool-homes__label">{{ tool.label }} <span class="mono">{{ tool.env }}</span></span>
                <el-select
                  :model-value="tool.picked"
                  clearable
                  placeholder="默认（全局第一个可用）"
                  style="width: 260px"
                  @update:model-value="(v: string) => setToolSelection(tool.id, v)"
                >
                  <el-option
                    v-for="inst in tool.installations"
                    :key="inst.name"
                    :label="installationLabel(inst)"
                    :value="inst.name"
                  />
                </el-select>
                <span class="mono tool-homes__value">{{ effectiveToolText(tool) }}</span>
                <span v-if="!tool.installations.length" class="tool-homes__missing">
                  未在「全局工具配置」中配置，按系统 PATH 执行
                </span>
              </div>
            </div>
            <div class="form-tip">
              「用什么项目就用什么配置」：这里给当前任务单独选 JDK / NodeJS / Maven 版本，
              不选则用「系统管理 → 全局工具配置」里第一个可用安装项。选中的版本在构建时注入
              <span class="mono">JAVA_HOME</span>/<span class="mono">NODE_HOME</span> 等变量并前置到
              <span class="mono">PATH</span>；标注「自动安装」的版本会在首次构建时自动下载（只下载本任务真正用到的工具）。
            </div>
          </el-form-item>
          <el-form-item label="其他环境配置">
            <el-alert
              type="info"
              :closable="false"
              show-icon
              title="密钥文件、配置文件、时间戳前缀等由全局环境变量提供；平台日志自带时间戳，无需 Timestamper。"
            />
          </el-form-item>
        </el-form>
        </section>
        <section id="steps" class="config-section">
        <h2 class="config-section__title">构建步骤</h2>
        <p class="config-section__hint">按顺序编排构建任务，如编译、测试与打包；每步可单独设置超时、重试与失败处理。</p>
        <el-form label-width="160px" label-position="right">
          <el-form-item label="执行方式">
            <el-tag type="info" effect="plain" disable-transitions>本地服务器</el-tag>
            <div class="form-tip">
              构建直接在运行服务的服务器上执行（不再使用 Docker）。工具路径在「系统管理 → 全局工具配置」中维护；
              Windows 默认使用 <span class="mono">cmd.exe</span>，也可在「系统配置 → 执行 shell」指定 PowerShell / Git Bash 的完整路径。
              会以 CI 服务进程的权限运行命令，请只给可信用户任务权限。
            </div>
          </el-form-item>
          <el-form-item label="执行 shell">
            <StepEditor v-model="form.steps" />
            <div class="form-tip">
              步骤通过 shell 串行执行，退出码非零即失败（可在「高级选项」里设置超时/重试/允许失败/执行条件）。
            </div>
          </el-form-item>
        </el-form>
        </section>
        <section id="postbuild" class="config-section">
        <h2 class="config-section__title">构建后操作</h2>
        <p class="config-section__hint">构建完成后的动作：先归档产物，再执行 SSH 发布与通知。</p>
        <ArtifactConfig v-if="form.artifacts" v-model="form.artifacts" />
        <div class="ssh-targets">
          <div class="ssh-targets__head">
            <span class="ssh-targets__title">SSH 发布（可配置多个目标）</span>
            <el-button size="small" @click="addSshPublish">添加发布目标</el-button>
          </div>
          <div
            v-for="(target, index) in form.ssh_publishes"
            :key="'ssh-' + index"
            class="ssh-targets__item"
          >
            <div class="ssh-targets__item-head">
              <span class="ssh-targets__name">目标 {{ index + 1 }}</span>
              <el-input
                :model-value="target.name ?? ''"
                placeholder="目标名称，如 生产 / 备份"
                style="width: 220px"
                @update:model-value="(value: string) => (target.name = value)"
              />
              <el-button
                v-if="(form.ssh_publishes ?? []).length > 1"
                link
                type="danger"
                @click="removeSshPublish(index)"
              >删除</el-button>
            </div>
            <SshPublishEditor
              v-model="form.ssh_publishes[index]"
              :title="`发布目标：${target.name || '未命名'}`"
            />
          </div>
          <el-alert
            v-if="!(form.ssh_publishes ?? []).length"
            type="info"
            :closable="false"
            show-icon
            title="当前任务未配置 SSH 发布"
          />
        </div>
        <NotifyConfig
          v-model="form.notify"
          :contacts="notifyContacts"
          :testing="testingNotify"
          @test="onTestNotify"
        />
        </section>
        </el-tab-pane>

        <el-tab-pane label="Advanced" name="advanced" lazy>
        <section id="advanced" class="config-section">
        <h2 class="config-section__title">Advanced</h2>
        <p class="config-section__hint">Jenkins 高级项在平台上的对应位置。</p>
        <el-form label-width="160px" label-position="right">
          <el-form-item label="静默期">
            <el-input-number :model-value="globalQuietPeriod" :min="0" :max="3600" disabled />
            <span class="muted" style="margin-left: 8px">秒</span>
            <div class="form-tip">
              平台在「系统管理 → 系统配置 → 静默期」统一配置，任务级暂不覆盖；
              当前值 {{ globalQuietPeriod }} 秒。<RouterLink :to="{ name: 'system-configure' }">去修改</RouterLink>
            </div>
          </el-form-item>
          <el-form-item label="显示的名称">
            <el-input :model-value="form.name" disabled />
            <div class="form-tip">
              平台以任务名作为唯一标识和显示名；重命名会同步工作区目录与角色 Pattern，避免两个名称不一致。
            </div>
          </el-form-item>
          <el-form-item label="其他 Jenkins 选项">
            <el-alert
              type="info"
              :closable="false"
              show-icon
              title="平台不执行 Jenkinsfile / Groovy，也不依赖 Jenkins master：不允许并发构建、当 master 重启后不允许恢复、流水线效率/持久保存等选项不适用。并发策略固定为同一任务串行，不同任务按 worker 并行。"
            />
          </el-form-item>
        </el-form>
        </section>
        </el-tab-pane>
      </el-tabs>

      <div class="configure__footer">
        <el-button type="primary" :loading="saving" @click="onSave">保存</el-button>
        <el-button @click="onCancel">取消</el-button>
        <el-button v-if="props.pipelineId" :loading="exporting" @click="onExport">导出配置</el-button>
      </div>
    </el-card>

    <el-dialog v-model="credDialog" title="新增 Git 账号密码" width="480px">
      <el-form label-width="90px" label-position="right">
        <el-form-item label="名称">
          <el-input v-model="credForm.name" placeholder="如 gitlab-readonly" />
        </el-form-item>
        <el-form-item label="用户名">
          <el-input v-model="credForm.username" placeholder="Git 用户名 / 令牌用户名" />
        </el-form-item>
        <el-form-item label="密码">
          <el-input v-model="credForm.password" type="password" show-password placeholder="密码 / 访问令牌" />
        </el-form-item>
      </el-form>
      <div class="cred-dialog-tip">
        账号会保存到服务端凭据库（<span class="mono">data/credentials/*.json</span>），重启后仍然保留；
        密码出于安全不回显，名称与用户名会记住以便下次添加。
      </div>
      <template #footer>
        <el-button @click="credDialog = false">取消</el-button>
        <el-button type="primary" :loading="credSaving" @click="saveCredential">保存并选中</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<style scoped>
.inline-row {
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
}
.tool-homes {
  display: flex;
  flex-direction: column;
  gap: 4px;
  width: 100%;
}
.tool-homes__item {
  display: flex;
  gap: 10px;
  font-size: 12.5px;
  line-height: 1.6;
}
.tool-homes__label {
  flex: none;
  width: 170px;
  color: var(--text-2, #606266);
}
.tool-homes__value {
  color: var(--text-1, #303133);
  word-break: break-all;
}
.tool-homes__missing {
  color: var(--text-3, #909399);
}

.ssh-targets {
  margin-top: 12px;
}
.ssh-targets__head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 8px;
}
.ssh-targets__title {
  font-weight: 600;
  font-size: 14px;
}
.ssh-targets__item {
  border: 1px solid var(--border-color, #e4e7ed);
  border-radius: 6px;
  padding: 10px 12px 4px;
  margin-bottom: 12px;
}
.ssh-targets__item-head {
  display: flex;
  align-items: center;
  gap: 10px;
  margin-bottom: 6px;
}
.ssh-targets__name {
  color: var(--text-3, #909399);
  font-size: 12.5px;
}

.type-tag {
  margin-left: 10px;
  vertical-align: middle;
}
.configure__breadcrumb {
  display: flex;
  align-items: center;
  gap: 8px;
  margin: 0 0 12px;
  color: var(--text-2, #606266);
  font-size: 13px;
}
.configure__breadcrumb a {
  color: var(--brand-600);
  text-decoration: none;
}
.configure__breadcrumb a:hover {
  text-decoration: underline;
}
.configure__breadcrumb-sep {
  color: #c0c4cc;
}
.configure__tabs :deep(.el-tabs__header) {
  margin-bottom: 18px;
}
.configure__tabs :deep(.el-tabs__item) {
  font-size: 14px;
}
.configure__card {
  max-width: 980px;
}
.config-section {
  padding: 6px 0 18px;
  margin-bottom: 18px;
  border-bottom: 1px solid #eef0f3;
  scroll-margin-top: 16px;
}
.config-section:last-of-type {
  border-bottom: none;
}
.config-section__title {
  font-size: 18px;
  font-weight: 600;
  color: #303133;
  margin: 0 0 6px;
}
.config-section__hint {
  margin: 0 0 14px;
  color: #909399;
  font-size: 13px;
}
.webhook-row {
  display: flex;
  gap: 8px;
  width: 100%;
  max-width: 620px;
}
.cred-row {
  display: flex;
  gap: 8px;
  width: 100%;
  align-items: center;
  flex-wrap: wrap;
}
.cred-dialog-tip {
  margin-top: -4px;
  color: var(--text-muted, #909399);
  font-size: 12px;
  line-height: 1.7;
}
.configure__footer {
  display: flex;
  gap: 10px;
  margin-top: 12px;
}
</style>

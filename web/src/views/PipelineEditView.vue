<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { RouterLink, useRoute, useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import ArtifactConfig from '@/components/ArtifactConfig.vue'
import StepEditor from '@/components/StepEditor.vue'
import SshPublishConfig from '@/components/SshPublishConfig.vue'
import { describeError } from '@/api/request'
import { credentialApi } from '@/api/system'
import { usePipeline } from '@/composables/usePipeline'
import { useSettingsStore } from '@/stores/settingsStore'
import { emptyPipeline, emptySshPublish, type Pipeline, type PipelinePayload } from '@/types/pipeline'
import type { Credential } from '@/types/system'
import { applyJobTypePreset, findJobType } from '@/types/jobType'
import { validatePipeline } from '@/utils/validate'

const props = defineProps<{ pipelineId?: string }>()

const route = useRoute()
const router = useRouter()
const { loading, fetchOne, save } = usePipeline()
const settingsStore = useSettingsStore()

const isEdit = ref(false)
const saving = ref(false)
const form = ref<PipelinePayload>(emptyPipeline())
const credentials = ref<Credential[]>([])

const credDialog = ref(false)
const credSaving = ref(false)
const credForm = reactive({ name: '', username: '', password: '' })

const jobTypeTitle = computed(() => findJobType(jobTypeId())?.title ?? '')

function jobTypeId(): string {
  const value = route.query.type
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
    git_credential_id: pipeline.git_credential_id ?? '',
    webhook_secret: pipeline.webhook_secret,
    build_retention_max_per_job: pipeline.build_retention_max_per_job ?? 0,
    build_retention_max_age_days: pipeline.build_retention_max_age_days ?? 0,
    delete_workspace_before_build: pipeline.delete_workspace_before_build ?? false,
    executor: pipeline.executor ?? 'local',
    artifacts: {
      enabled: pipeline.artifacts?.enabled ?? false,
      files: [...(pipeline.artifacts?.files ?? [])],
    },
    // 敏感字段不回显：密码置空，服务端会沿用旧值。
    ssh_publish: pipeline.ssh_publish
      ? {
          ...pipeline.ssh_publish,
          server_id: pipeline.ssh_publish.server_id ?? '',
          password: '',
          source_files: [...pipeline.ssh_publish.source_files],
        }
      : emptySshPublish(),
  }
}

onMounted(async () => {
  // 强制刷新：确保刚在「系统配置」中新增的 SSH 服务器/工具主目录能立即生效。
  await Promise.all([settingsStore.fetch(true), loadCredentials()])
  if (!props.pipelineId) {
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
    form.value = toPayload(pipeline)
    scm.value = form.value.git_url ? 'git' : 'none'
  }
})

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
    const created = await credentialApi.create({
      name,
      type: 'username_password',
      username,
      description: 'Git 账号密码',
      secret: password,
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
  const errors = validatePipeline(form.value, { keepSshPassword: isEdit.value })
  if (errors.length > 0) {
    ElMessage.error(errors[0])
    return
  }
  saving.value = true
  try {
    // 关闭 SSH 发布时保留已填写的配置（与 Jenkins 一致），只有完全空白时才落为 null。
    const ssh = form.value.ssh_publish
    const sshConfigured =
      !!ssh &&
      (ssh.enabled ||
        ssh.host.trim() !== '' ||
        ssh.username.trim() !== '' ||
        ssh.remote_base_dir.trim() !== '' ||
        ssh.private_key_path.trim() !== '' ||
        ssh.source_files.length > 0 ||
        ssh.exec_command.trim() !== '')
    const payload: PipelinePayload = {
      ...form.value,
      // 只有选择 Git 时才提交仓库与凭据；切回“无”后保存即禁用，但切回 Git 时已填内容仍在。
      git_url: scm.value === 'git' ? form.value.git_url.trim() : '',
      git_credential_id: scm.value === 'git' ? form.value.git_credential_id : '',
      ssh_publish: sshConfigured ? ssh : null,
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
        <el-button type="primary" :loading="saving" @click="onSave">保存</el-button>
      </div>
    </div>

    <nav class="configure__nav">
      <a @click="scrollTo('general')">General</a>
      <a @click="scrollTo('scm')">源码管理</a>
      <a @click="scrollTo('triggers')">Triggers</a>
      <a @click="scrollTo('environment')">构建环境</a>
      <a @click="scrollTo('steps')">Build Steps</a>
      <a @click="scrollTo('postbuild')">构建后操作</a>
    </nav>

    <el-card v-loading="loading" shadow="never" class="configure__card">
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
            <el-alert type="info" :closable="false" show-icon title="基础版暂不支持参数化构建。" />
          </el-form-item>
          <el-form-item label="在必要的时候并发构建">
            <el-alert type="info" :closable="false" show-icon title="基础版不做并发限制，构建按 worker 数量串行排队。" />
          </el-form-item>
        </el-form>
      </section>

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
                <el-button @click="openCredDialog">新增账号密码</el-button>
                <RouterLink :to="{ name: 'system-credentials' }">
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

      <section id="triggers" class="config-section">
        <h2 class="config-section__title">Triggers</h2>
        <p class="config-section__hint">根据事件自动触发构建，如代码提交或远程调用。</p>
        <el-form label-width="160px" label-position="right">
          <el-form-item label="触发远程构建">
            <div class="webhook-row">
              <el-input :model-value="webhookUrl" readonly class="mono" />
              <el-button @click="copyWebhook">复制</el-button>
            </div>
            <div class="form-tip">通过该地址触发构建，请求头 <span class="mono">X-Webhook-Secret</span> 用于校验。</div>
          </el-form-item>
          <el-form-item label="Webhook 密钥">
            <el-input v-model="form.webhook_secret" placeholder="留空表示该 Webhook 不校验密钥（不建议）" style="max-width: 480px" />
          </el-form-item>
          <el-form-item label="其他触发器">
            <el-alert
              type="info"
              :closable="false"
              show-icon
              title="定时构建、SCM 轮询、GitLab / GitHub / Generic Webhook Trigger 在基础版中暂不支持。"
            />
          </el-form-item>
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
          <el-form-item label="其他环境配置">
            <el-alert
              type="info"
              :closable="false"
              show-icon
              title="密钥文件、配置文件、NodeJS 环境、时间戳前缀等由全局环境变量与「全局工具配置」中的主目录统一提供。"
            />
          </el-form-item>
        </el-form>
      </section>

      <section id="steps" class="config-section">
        <h2 class="config-section__title">Build Steps</h2>
        <p class="config-section__hint">按顺序编排构建任务，如编译、测试与打包。</p>
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
            <div class="form-tip">每个步骤通过 shell 串行执行，退出码非零即失败。</div>
          </el-form-item>
        </el-form>
      </section>

      <section id="postbuild" class="config-section">
        <h2 class="config-section__title">构建后操作</h2>
        <p class="config-section__hint">构建完成后的动作：先归档产物，再执行 SSH 发布与通知。</p>
        <ArtifactConfig v-if="form.artifacts" v-model="form.artifacts" />
        <SshPublishConfig v-if="form.ssh_publish" v-model="form.ssh_publish" />
        <el-alert v-else type="info" :closable="false" show-icon title="当前任务未配置 SSH 发布" />
        <el-alert
          style="margin-top: 12px"
          type="info"
          :closable="false"
          show-icon
          title="构建扫描暂不支持；邮件通知由「系统管理 → 系统配置」统一处理。"
        />
      </section>

      <div class="configure__footer">
        <el-button type="primary" :loading="saving" @click="onSave">保存</el-button>
        <el-button @click="onCancel">取消</el-button>
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
.type-tag {
  margin-left: 10px;
  vertical-align: middle;
}
.configure__nav {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  margin-bottom: 16px;
}
.configure__nav a {
  padding: 5px 14px;
  border: 1px solid #dcdfe6;
  border-radius: 16px;
  background: #fff;
  color: var(--brand-600);
  font-size: 13px;
  cursor: pointer;
}
.configure__nav a:hover {
  border-color: var(--brand-600);
  background: var(--brand-50);
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

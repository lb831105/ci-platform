<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import ManagePageHeader from '@/components/ManagePageHeader.vue'
import { systemApi } from '@/api/system'
import { describeError } from '@/api/request'
import { useSettingsStore } from '@/stores/settingsStore'
import type { DataHomeInfo, Settings, SshServer, SystemInfo } from '@/types/system'

const settingsStore = useSettingsStore()

const loading = ref(false)
const saving = ref(false)
const reloading = ref(false)
const testing = ref(false)
const form = ref<Settings>(defaultForm())
const info = ref<SystemInfo | null>(null)
const dataHome = ref<DataHomeInfo | null>(null)
const dataHomeForm = ref('')
const migrateData = ref(true)
const savingDataHome = ref(false)
const testTo = ref('')

const activeNames = ref(['system-message', 'location', 'behavior'])

function defaultForm(): Settings {
  return {
    display_name: '',
    system_message: '',
    jenkins_url: '',
    gitlab_base_url: '',
    admin_email: '',
    default_branch: 'main',
    job_name_regex: '',
    quiet_period_seconds: 0,
    scm_retry_count: 0,
    shell: navigator.userAgent.includes('Windows') ? 'cmd.exe' : '/bin/sh',
    global_build_timeout_minutes: 0,
    build_retention_max_per_job: 0,
    build_retention_max_age_days: 0,
    artifact_max_total_mb: 4096,
    artifact_max_files: 10000,
    log_max_mb: 100,
    global_env: [],
    git_user_name: '',
    git_user_email: '',
    tool_categories: [],
    ssh_servers: [],
    maven: { maven_opts: '', local_repo: '' },
    email: {
      enabled: false,
      smtp_host: '',
      smtp_port: 25,
      use_tls: false,
      username: '',
      password: '',
      from: '',
      default_suffix: '',
      recipients: '',
      notify_on_failure: true,
      notify_on_success: false,
      subject_template: '',
    },
    webhook_notify: {
      enabled: false,
      channel: 'dingtalk',
      url: '',
      notify_on_failure: true,
      notify_on_success: false,
    },
    quiet_mode: false,
    auth_enabled: true,
    auth_user: 'admin',
    auth_password: '',
    workers: 3,
    queue_size: 100,
    workspace_ttl_minutes: 60,
    clean_interval_minutes: 5,
    updated_at: '',
    has_password: false,
  }
}

async function load(): Promise<void> {
  loading.value = true
  try {
    const [s, i, dh] = await Promise.all([
      systemApi.getSettings(),
      systemApi.info(),
      systemApi.getDataHome(),
    ])
    form.value = s
    info.value = i
    dataHome.value = dh
    dataHomeForm.value = dh.configured_data_home || dh.data_home
    settingsStore.set(s)
  } catch (e) {
    ElMessage.error(describeError(e))
  } finally {
    loading.value = false
  }
}

onMounted(() => {
  void load()
})

async function onSave(): Promise<void> {
  saving.value = true
  try {
    const updated = await systemApi.updateSettings(form.value)
    form.value = updated
    settingsStore.set(updated)
    ElMessage.success('系统配置已保存')
  } catch (e) {
    ElMessage.error(describeError(e))
  } finally {
    saving.value = false
  }
}

async function onReload(): Promise<void> {
  reloading.value = true
  try {
    const updated = await systemApi.reload()
    form.value = updated
    settingsStore.set(updated)
    ElMessage.success('已从磁盘重新读取设置')
  } catch (e) {
    ElMessage.error(describeError(e))
  } finally {
    reloading.value = false
  }
}

async function onSaveDataHome(): Promise<void> {
  const target = dataHomeForm.value.trim()
  if (!target) {
    ElMessage.warning('请填写数据目录')
    return
  }
  try {
    await ElMessageBox.confirm(
      `将把数据目录配置为：\n${target}\n\n该配置需重启服务后生效。` +
        (migrateData.value ? '\n\n保存时会先把现有任务 / 凭据 / 设置复制到新目录。' : ''),
      '修改数据目录',
      { type: 'warning', confirmButtonText: '保存', cancelButtonText: '取消' },
    )
  } catch {
    return
  }
  savingDataHome.value = true
  try {
    const dh = await systemApi.updateDataHome({ data_home: target, migrate: migrateData.value })
    dataHome.value = dh
    dataHomeForm.value = dh.configured_data_home || dh.data_home
    ElMessage.success('数据目录已保存，重启服务后生效')
  } catch (e) {
    ElMessage.error(describeError(e))
  } finally {
    savingDataHome.value = false
  }
}

function addEnv(): void {
  form.value.global_env.push({ key: '', value: '' })
}

function addSshServer(): void {
  const server: SshServer = {
    id: '',
    name: '',
    host: '',
    port: 22,
    username: '',
    auth_type: 'key',
    private_key_path: '',
    password: '',
    known_hosts_path: '',
    remote_base_dir: '',
    note: '',
    has_password: false,
  }
  form.value.ssh_servers.push(server)
}

function removeSshServer(index: number): void {
  form.value.ssh_servers.splice(index, 1)
}

function removeEnv(index: number): void {
  form.value.global_env.splice(index, 1)
}

async function onQuiet(quiet: boolean): Promise<void> {
  try {
    const updated = await systemApi.setQuiet(quiet)
    form.value.quiet_mode = updated.quiet_mode
    settingsStore.set(updated)
    ElMessage.success(quiet ? '已进入静默模式，将不再接受新构建' : '已退出静默模式')
  } catch (e) {
    ElMessage.error(describeError(e))
  }
}

async function confirmQuiet(): Promise<void> {
  try {
    await ElMessageBox.confirm(
      '静默模式（准备关机）将停止接受新的构建任务，正在运行的构建不受影响。确认开启？',
      '准备关机',
      { type: 'warning', confirmButtonText: '开启静默', cancelButtonText: '取消' },
    )
  } catch {
    return
  }
  await onQuiet(true)
}

async function onTestEmail(): Promise<void> {
  if (!testTo.value.trim()) {
    ElMessage.error('请填写测试收件人')
    return
  }
  testing.value = true
  try {
    await systemApi.testEmail(testTo.value.trim())
    ElMessage.success('测试邮件已发送')
  } catch (e) {
    ElMessage.error(describeError(e))
  } finally {
    testing.value = false
  }
}

// 发送 Webhook 测试通知。
const testingWebhook = ref(false)
async function onTestWebhook(): Promise<void> {
  testingWebhook.value = true
  try {
    await systemApi.testWebhookNotify()
    ElMessage.success('测试通知已发送')
  } catch (e) {
    ElMessage.error(describeError(e))
  } finally {
    testingWebhook.value = false
  }
}
</script>

<template>
  <div class="page">
    <ManagePageHeader title="系统配置" description="配置全局设置和路径，修改后立即生效">
      <template #actions>
        <el-button :loading="reloading" @click="onReload">读取设置</el-button>
        <el-button type="primary" :loading="saving" @click="onSave">保存</el-button>
      </template>
    </ManagePageHeader>

    <el-card v-loading="loading" shadow="never" class="config-card">
      <el-collapse v-model="activeNames">
        <el-collapse-item name="home">
          <template #title>主目录</template>
          <div class="muted">
            运行产生数据（任务、构建记录、凭据、设置）的存放目录。修改后需重启服务生效；工作区（workspace）为临时产物，不参与迁移。
          </div>
          <el-input
            v-model="dataHomeForm"
            class="mono"
            style="max-width: 560px; margin-top: 8px"
            placeholder="例如 D:/ci-data 或 /var/lib/ci-platform"
          />
          <div class="data-home-actions">
            <el-checkbox v-model="migrateData">同时迁移现有数据（任务 / 凭据 / 设置）</el-checkbox>
            <el-button size="small" :loading="savingDataHome" @click="onSaveDataHome">保存数据目录</el-button>
          </div>
          <div class="form-tip">
            任务目录 <span class="mono">{{ dataHome?.jobs_dir }}</span>；工作区
            <span class="mono">{{ dataHome?.workspace_dir }}</span>；引导配置
            <span class="mono">{{ dataHome?.config_file }}</span>
          </div>
          <el-alert
            v-if="dataHome?.restart_required"
            type="warning"
            :closable="false"
            show-icon
            style="max-width: 640px; margin-top: 8px"
            title="数据目录已修改，重启后生效"
            :description="`当前生效：${dataHome.data_home}；重启后使用：${dataHome.configured_data_home}`"
          />
          <el-alert
            v-if="dataHome?.explicit"
            type="info"
            :closable="false"
            show-icon
            style="max-width: 640px; margin-top: 8px"
            title="启动参数已显式指定数据目录"
            description="当前通过 -data-home 或 CI_DATA_HOME 指定，优先级高于此处配置。若要让此处配置生效，请去掉启动参数后重启。"
          />
        </el-collapse-item>

        <el-collapse-item name="system-message">
          <template #title>系统消息</template>
          <el-input
            v-model="form.system_message"
            type="textarea"
            :rows="3"
            placeholder="该消息会显示在控制台主页面顶部，可用于向用户发布通知"
          />
          <div v-if="form.system_message" class="message-preview">{{ form.system_message }}</div>
        </el-collapse-item>

        <el-collapse-item name="location">
          <template #title>系统地址（Jenkins Location）</template>
          <el-form label-width="200px" label-position="right">
            <el-form-item label="Jenkins URL">
              <el-input v-model="form.jenkins_url" placeholder="如 https://ci.example.com" style="max-width: 480px" />
              <div class="form-tip">用于生成回显链接（Webhook、通知中的地址）。</div>
            </el-form-item>
            <el-form-item label="GitLab 地址">
              <el-input
                v-model="form.gitlab_base_url"
                class="mono"
                placeholder="如 https://gitlab.example.com（不要带 /api/v4）"
                style="max-width: 480px"
              />
              <div class="form-tip">
                任务开启「回写构建状态」时用它调 GitLab API（commit status），让 Merge Request 里直接看到构建结果。
              </div>
            </el-form-item>
            <el-form-item label="系统管理员邮件地址">
              <el-input v-model="form.admin_email" placeholder="admin@example.com" style="max-width: 480px" />
            </el-form-item>
          </el-form>
        </el-collapse-item>

        <el-collapse-item name="defaults">
          <template #title>默认构建参数</template>
          <el-form label-width="200px" label-position="right">
            <el-form-item label="默认分支">
              <el-input v-model="form.default_branch" class="mono" style="max-width: 480px" />
            </el-form-item>
            <el-form-item label="工程命名限制">
              <el-input
                v-model="form.job_name_regex"
                class="mono"
                placeholder="正则，如 ^[a-z][a-z0-9-]*$（留空不限制）"
                style="max-width: 480px"
              />
              <div class="form-tip">创建 / 重命名任务时校验名称必须匹配该正则。</div>
            </el-form-item>
          </el-form>
        </el-collapse-item>

        <el-collapse-item name="behavior">
          <template #title>执行器与构建行为</template>
          <el-form label-width="200px" label-position="right">
            <el-form-item label="静默期（秒）">
              <el-input-number v-model="form.quiet_period_seconds" :min="0" :max="3600" />
              <div class="form-tip">构建触发后等待若干秒再开始，便于合并连续提交。</div>
            </el-form-item>
            <el-form-item label="SCM 签出重试次数">
              <el-input-number v-model="form.scm_retry_count" :min="0" :max="10" />
            </el-form-item>
            <el-form-item label="Shell 解释器">
              <el-input v-model="form.shell" class="mono" style="max-width: 320px" />
              <div class="form-tip">本地执行构建步骤使用的 shell。Windows 默认 cmd.exe（可填 powershell.exe 或 Git Bash 的 bash.exe 完整路径）；Linux 默认 /bin/sh。</div>
            </el-form-item>
            <el-form-item label="全局构建超时（分钟）">
              <el-input-number v-model="form.global_build_timeout_minutes" :min="0" :max="1440" />
              <div class="form-tip">0 表示不限制；超时后构建失败。</div>
            </el-form-item>
          </el-form>
        </el-collapse-item>

        <el-collapse-item name="discarders">
          <template #title>全局构建保留策略（Global Build Discarders）</template>
          <el-form label-width="200px" label-position="right">
            <el-form-item label="每个任务最多保留">
              <el-input-number v-model="form.build_retention_max_per_job" :min="0" :max="10000" />
              <div class="form-tip">0 表示不限制；超出后自动删除最旧的构建记录与日志。</div>
            </el-form-item>
            <el-form-item label="最多保留天数">
              <el-input-number v-model="form.build_retention_max_age_days" :min="0" :max="3650" />
            </el-form-item>
            <el-form-item label="单次产物大小上限（MB）">
              <el-input-number v-model="form.artifact_max_total_mb" :min="1" :max="102400" />
              <div class="form-tip">防止异常产物写满磁盘；超过上限时构建失败，不会删除已有历史产物。</div>
            </el-form-item>
            <el-form-item label="单次产物文件数上限">
              <el-input-number v-model="form.artifact_max_files" :min="1" :max="100000" />
              <div class="form-tip">二项上限按匹配文件累计计算；设置过小会导致构建失败（错误信息中会提示当前值与修改位置）。</div>
            </el-form-item>
            <el-form-item label="构建日志上限（MB）">
              <el-input-number v-model="form.log_max_mb" :min="1" :max="10240" />
              <div class="form-tip">单次构建的日志文件大小上限；超过后日志截断并标注（不影响构建结果），避免超长输出拖慢读取与占用磁盘。在线实时日志不受影响。</div>
            </el-form-item>
          </el-form>
        </el-collapse-item>

        <el-collapse-item name="properties">
          <template #title>全局属性</template>
          <div class="section-label">环境变量</div>
          <el-table :data="form.global_env">
            <el-table-column label="名称" min-width="200">
              <template #default="{ row }">
                <el-input v-model="row.key" class="mono" placeholder="如 LANG" />
              </template>
            </el-table-column>
            <el-table-column label="值" min-width="280">
              <template #default="{ row }">
                <el-input v-model="row.value" class="mono" />
              </template>
            </el-table-column>
            <el-table-column label="操作" width="100" align="right">
              <template #default="{ $index }">
                <el-button size="small" type="danger" plain @click="removeEnv($index)">删除</el-button>
              </template>
            </el-table-column>
            <template #empty>
              <div class="empty-tip">尚未配置全局环境变量</div>
            </template>
          </el-table>
          <el-button size="small" style="margin-top: 10px" @click="addEnv">新增环境变量</el-button>
          <div class="form-tip">这些变量会注入到每次构建的环境中。</div>

          <div class="section-label" style="margin-top: 18px">工具位置</div>
          <el-alert
            type="info"
            :closable="false"
            show-icon
            title="工具位置（JAVA_HOME / GOROOT 等）由「全局工具配置」中的主目录决定，此处不再单独设置。"
          />
        </el-collapse-item>

        <el-collapse-item name="git">
          <template #title>Git 全局身份（Git plugin）</template>
          <el-form label-width="240px" label-position="right">
            <el-form-item label="Global Config user.name Value">
              <el-input v-model="form.git_user_name" style="max-width: 360px" />
            </el-form-item>
            <el-form-item label="Global Config user.email Value">
              <el-input v-model="form.git_user_email" style="max-width: 360px" />
            </el-form-item>
            <div class="form-tip" style="margin-left: 200px">
              以 GIT_AUTHOR_* / GIT_COMMITTER_* 环境变量注入构建环境。
            </div>
          </el-form>
        </el-collapse-item>

        <el-collapse-item name="ssh-servers">
          <template #title>SSH 服务器（Publish Over SSH）</template>
          <div class="muted">
            SSH 发布目标集中在这里维护，保存到 <span class="mono">data/settings.json</span>；
            任务只需选择服务器，不再重复保存主机、账号与密码。
          </div>
          <el-button size="small" style="margin: 10px 0" @click="addSshServer">新增 SSH 服务器</el-button>
          <el-table :data="form.ssh_servers" size="small">
            <el-table-column label="名称" min-width="140">
              <template #default="{ row }"><el-input v-model="row.name" placeholder="如 prod-web" /></template>
            </el-table-column>
            <el-table-column label="主机" min-width="160">
              <template #default="{ row }"><el-input v-model="row.host" class="mono" placeholder="deploy.example.com" /></template>
            </el-table-column>
            <el-table-column label="端口" width="100">
              <template #default="{ row }">
                <el-input-number v-model="row.port" :min="1" :max="65535" controls-position="right" style="width: 100%" />
              </template>
            </el-table-column>
            <el-table-column label="用户名" min-width="120">
              <template #default="{ row }"><el-input v-model="row.username" class="mono" placeholder="deploy" /></template>
            </el-table-column>
            <el-table-column label="认证" width="110">
              <template #default="{ row }">
                <el-select v-model="row.auth_type" style="width: 100%">
                  <el-option label="密钥" value="key" />
                  <el-option label="密码" value="password" />
                </el-select>
              </template>
            </el-table-column>
            <el-table-column label="私钥路径 / 密码" min-width="210">
              <template #default="{ row }">
                <el-input
                  v-if="row.auth_type === 'key'"
                  v-model="row.private_key_path"
                  class="mono"
                  placeholder="/path/to/id_rsa"
                />
                <el-input
                  v-else
                  v-model="row.password"
                  type="password"
                  show-password
                  :placeholder="row.has_password ? '已保存，留空不修改' : '密码 / 口令'"
                />
              </template>
            </el-table-column>
            <el-table-column label="known_hosts（可选）" min-width="190">
              <template #default="{ row }">
                <el-input v-model="row.known_hosts_path" class="mono" placeholder="~/.ssh/known_hosts" />
              </template>
            </el-table-column>
            <el-table-column label="默认远程目录" min-width="180">
              <template #default="{ row }">
                <el-input v-model="row.remote_base_dir" class="mono" placeholder="/opt/app/releases" />
              </template>
            </el-table-column>
            <el-table-column label="操作" width="90" align="right">
              <template #default="{ $index }">
                <el-button size="small" type="danger" plain @click="removeSshServer($index)">删除</el-button>
              </template>
            </el-table-column>
          </el-table>
          <div class="form-tip">
            密码保存后不回显（留空保持原值）；任务选择服务器时自动使用这里的连接信息。
            密钥认证的私钥路径必须是运行构建的服务器上可读的路径。填写 known_hosts 后会校验服务器主机密钥，
            不填写则跳过校验（存在中间人风险，生产环境建议填写）。
          </div>
        </el-collapse-item>

        <el-collapse-item name="email">
          <template #title>邮件通知</template>
          <el-form label-width="200px" label-position="right">
            <el-form-item label="启用邮件通知">
              <el-switch v-model="form.email.enabled" />
            </el-form-item>
            <el-form-item label="SMTP 服务器">
              <el-input v-model="form.email.smtp_host" style="max-width: 360px" />
            </el-form-item>
            <el-form-item label="SMTP 端口">
              <el-input-number v-model="form.email.smtp_port" :min="1" :max="65535" />
            </el-form-item>
            <el-form-item label="使用 STARTTLS">
              <el-switch v-model="form.email.use_tls" />
            </el-form-item>
            <el-form-item label="用户名">
              <el-input v-model="form.email.username" style="max-width: 360px" />
            </el-form-item>
            <el-form-item label="密码">
              <el-input
                v-model="form.email.password"
                type="password"
                show-password
                placeholder="留空表示不修改"
                style="max-width: 360px"
              />
            </el-form-item>
            <el-form-item label="发件人">
              <el-input v-model="form.email.from" style="max-width: 360px" />
            </el-form-item>
            <el-form-item label="用户默认邮件后缀">
              <el-input v-model="form.email.default_suffix" placeholder="如 @example.com" style="max-width: 360px" />
            </el-form-item>
            <el-form-item label="默认收件人">
              <el-input v-model="form.email.recipients" placeholder="逗号分隔" style="max-width: 480px" />
            </el-form-item>
            <el-form-item label="通知时机">
              <el-checkbox v-model="form.email.notify_on_failure">失败时通知</el-checkbox>
              <el-checkbox v-model="form.email.notify_on_success">成功时通知</el-checkbox>
            </el-form-item>
            <el-form-item label="邮件标题模板">
              <el-input v-model="form.email.subject_template" class="mono" style="max-width: 560px" />
            </el-form-item>
            <el-form-item label="测试邮件">
              <el-input v-model="testTo" placeholder="收件人" style="max-width: 300px" />
              <el-button style="margin-left: 10px" :loading="testing" @click="onTestEmail">发送测试邮件</el-button>
            </el-form-item>
          </el-form>
        </el-collapse-item>

        <el-collapse-item name="webhook-notify">
          <template #title>Webhook 通知（钉钉 / 企业微信 / Slack）</template>
          <el-form label-width="200px" label-position="right">
            <el-form-item label="启用 Webhook 通知">
              <el-switch v-model="form.webhook_notify.enabled" />
            </el-form-item>
            <el-form-item label="通知渠道">
              <el-radio-group v-model="form.webhook_notify.channel">
                <el-radio value="dingtalk">钉钉</el-radio>
                <el-radio value="wecom">企业微信</el-radio>
                <el-radio value="slack">Slack</el-radio>
              </el-radio-group>
            </el-form-item>
            <el-form-item label="Webhook 地址">
              <el-input
                v-model="form.webhook_notify.url"
                class="mono"
                placeholder="机器人 Webhook 地址（含 access_token / key）"
                style="max-width: 560px"
              />
              <div class="form-tip">URL 含密钥，保存后不再回传（留空表示沿用旧值）；钉钉机器人需在安全设置中包含关键词「构建」或改用加签方式。</div>
            </el-form-item>
            <el-form-item label="通知时机">
              <el-checkbox v-model="form.webhook_notify.notify_on_failure">失败时通知</el-checkbox>
              <el-checkbox v-model="form.webhook_notify.notify_on_success">成功时通知</el-checkbox>
            </el-form-item>
            <el-form-item label="测试通知">
              <el-button :loading="testingWebhook" @click="onTestWebhook">发送测试通知</el-button>
            </el-form-item>
          </el-form>
        </el-collapse-item>

        <el-collapse-item name="shutdown">
          <template #title>准备关机</template>
          <el-form label-width="200px" label-position="right">
            <el-form-item label="静默模式">
              <el-switch
                :model-value="form.quiet_mode"
                active-text="已开启"
                inactive-text="已关闭"
                @update:model-value="(v: boolean) => (v ? confirmQuiet() : onQuiet(false))"
              />
              <div class="form-tip">开启后调度器拒绝新的构建任务（正在运行的不受影响），用于安全关机。</div>
            </el-form-item>
          </el-form>
        </el-collapse-item>

        <el-collapse-item name="startup">
          <template #title>启动参数（只读，修改后需重启）</template>
          <el-form label-width="200px" label-position="right">
            <el-form-item label="构建 worker 数">
              <el-input-number :model-value="info?.workers ?? form.workers" disabled />
            </el-form-item>
            <el-form-item label="队列容量">
              <el-input-number :model-value="info?.queue_capacity ?? form.queue_size" disabled />
            </el-form-item>
            <el-form-item label="孤儿工作区保留（分钟）">
              <el-input-number :model-value="info?.workspace_ttl_minutes ?? 0" disabled />
              <div class="form-tip">仅清理已删除任务遗留的孤儿工作区；仍存在的任务会保留工作区以支持增量构建。</div>
            </el-form-item>
            <el-form-item label="清理扫描间隔（分钟）">
              <el-input-number :model-value="info?.clean_interval_minutes ?? 0" disabled />
            </el-form-item>
            <div class="form-tip" style="margin-left: 200px">
              以上为启动参数的实际生效值（来自命令行参数 / 环境变量），修改需重启服务。
            </div>
          </el-form>
        </el-collapse-item>

        <el-collapse-item name="unsupported">
          <template #title>其他（基础版暂不支持）</template>
          <ul class="unsupported-list">
            <li>Disk Space Monitoring Thresholds、Metrics / Access keys、Fingerprints、管理监控</li>
            <li>GitLab / GitHub / Generic Webhook Trigger、Develocity、Timestamper</li>
            <li>HTTP Proxy、Resource Root URL、使用统计、Pipeline 库</li>
            <li>Maven settings.xml 托管（把文件放在服务器的 ~/.m2/ 下，或在步骤里用 -s 指定）</li>
          </ul>
        </el-collapse-item>
      </el-collapse>

      <div class="config-footer">
        <el-button type="primary" :loading="saving" @click="onSave">保存</el-button>
        <el-button @click="load()">重置</el-button>
      </div>
    </el-card>
  </div>
</template>

<style scoped>
.config-card {
  max-width: 1000px;
}
.section-label {
  font-size: 13px;
  font-weight: 600;
  color: #303133;
  margin-bottom: 10px;
}
.message-preview {
  margin-top: 10px;
  padding: 10px 14px;
  background: #fff8e6;
  border: 1px solid #f5deb3;
  border-radius: 6px;
  color: #8a6d1a;
  font-size: 13px;
  white-space: pre-wrap;
}
.empty-tip {
  padding: 10px 0;
  color: #a8abb2;
  font-size: 13px;
}
.unsupported-list {
  margin: 0;
  padding-left: 18px;
  color: #909399;
  font-size: 13px;
  line-height: 2;
}
.config-footer {
  display: flex;
  gap: 10px;
  margin-top: 18px;
}
.data-home-actions {
  display: flex;
  align-items: center;
  gap: 12px;
  max-width: 640px;
  margin-top: 12px;
}
</style>

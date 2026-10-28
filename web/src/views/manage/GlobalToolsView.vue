<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'
import { ElMessage } from 'element-plus'
import ManagePageHeader from '@/components/ManagePageHeader.vue'
import { systemApi } from '@/api/system'
import { describeError } from '@/api/request'
import { useSettingsStore } from '@/stores/settingsStore'
import type {
  MavenSettings,
  Settings,
  ToolCatalogTool,
  ToolCategory,
  ToolInstallation,
  ToolInstallTask,
} from '@/types/system'

const settingsStore = useSettingsStore()

const loading = ref(false)
const saving = ref(false)
const categories = ref<ToolCategory[]>([])
// 自动安装：可选版本 + 已安装状态（后端 data/tools 的真实情况）
const catalogs = ref<ToolCatalogTool[]>([])
// 后台安装任务：安装跑在服务端，离开页面也不会中断；
// 页面通过轮询 /tools/tasks 显示进度，关掉再打开也能续上。
const tasks = ref<ToolInstallTask[]>([])
let poller: number | undefined
const maven = ref<MavenSettings>({ maven_opts: '', local_repo: '' })
let base: Settings | null = null

function cloneCategory(category: ToolCategory): ToolCategory {
  return { ...category, installations: category.installations.map((item) => ({ ...item })) }
}

async function load(): Promise<void> {
  loading.value = true
  try {
    base = await systemApi.getSettings()
    categories.value = base.tool_categories.map(cloneCategory)
    maven.value = { ...base.maven }
    settingsStore.set(base)
    await loadCatalog()
  } catch (e) {
    ElMessage.error(describeError(e))
  } finally {
    loading.value = false
  }
}

// loadCatalog 拉取各工具的可选版本与安装状态；失败不阻塞页面（手动路径仍可用）。
async function loadCatalog(): Promise<void> {
  try {
    catalogs.value = await systemApi.toolCatalog()
    tasks.value = await systemApi.toolTasks().catch(() => [])
    syncPolling()
  } catch {
    catalogs.value = []
  }
}

// syncPolling 有运行中的任务才轮询：没任务时不要空转。
function syncPolling(): void {
  const running = tasks.value.some((task) => task.state === 'queued' || task.state === 'downloading' || task.state === 'extracting')
  if (running && poller === undefined) {
    poller = window.setInterval(() => void refreshTasks(), 1500)
    return
  }
  if (!running && poller !== undefined) {
    window.clearInterval(poller)
    poller = undefined
  }
}

async function refreshTasks(): Promise<void> {
  try {
    tasks.value = await syncTaskState(await systemApi.toolTasks())
  } catch {
    return
  }
  syncPolling()
}

// syncTaskState 把任务结果写回安装项（完成时自动填主目录，用户什么都不用做）。
async function syncTaskState(list: ToolInstallTask[]): Promise<ToolInstallTask[]> {
  for (const task of list) {
    if (task.state !== 'done') {
      continue
    }
    const category = categories.value.find((item) => item.id === task.type)
    const item = category?.installations.find((inst) => inst.version === task.version)
    if (item && task.home && item.home !== task.home) {
      item.home = task.home
      if (!item.name.trim()) {
        item.name = `${category?.title ?? task.type} ${task.version}`
      }
    }
  }
  const done = list.filter((task) => task.state === 'done')
  if (done.length > 0) {
    // 完成的任务不再显示进度，并刷新一次「已安装」状态
    const refreshed = await systemApi.toolCatalog().catch(() => null)
    if (refreshed) {
      catalogs.value = refreshed
    }
  }
  return list.filter((task) => task.state !== 'done')
}

function taskOf(type: string, version?: string): ToolInstallTask | undefined {
  return tasks.value.find((task) => task.type === type && task.version === version)
}

// taskPercent 计算进度百分比（总大小未知时返回 0，前端显示不确定进度）。
function taskPercent(task?: ToolInstallTask): number {
  if (!task || !task.total) {
    return 0
  }
  return Math.min(100, Math.round((task.downloaded / task.total) * 100))
}

function taskText(task?: ToolInstallTask): string {
  if (!task) {
    return ''
  }
  switch (task.state) {
    case 'queued':
      return '排队中…'
    case 'downloading':
      return task.total
        ? `下载中 ${taskPercent(task)}%（${formatMB(task.downloaded)} / ${formatMB(task.total)}）`
        : `下载中 ${formatMB(task.downloaded)}`
    case 'extracting':
      return '解压中…'
    case 'failed':
      return `失败：${task.error ?? '未知错误'}`
    default:
      return '已完成'
  }
}

function formatMB(bytes: number): string {
  if (bytes > 1024 * 1024) {
    return `${(bytes / 1024 / 1024).toFixed(1)} MB`
  }
  return `${Math.max(0, Math.round(bytes / 1024))} KB`
}

onMounted(load)
onBeforeUnmount(() => {
  if (poller !== undefined) {
    window.clearInterval(poller)
  }
})

function catalogOf(categoryId: string): ToolCatalogTool | undefined {
  return catalogs.value.find((tool) => tool.type === categoryId)
}

function versionsOf(categoryId: string) {
  return catalogOf(categoryId)?.versions ?? []
}

// 某个安装项的版本状态（用于显示「已安装 / 未下载 / 当前平台无包」）。
function versionState(categoryId: string, version?: string) {
  return versionsOf(categoryId).find((item) => item.version === version)
}

// 安装方式切换：切到自动安装时给个默认版本，切回手动时清掉版本与平台回填的路径。
function setMode(category: ToolCategory, item: ToolInstallation, mode: 'manual' | 'auto'): void {
  item.install_mode = mode
  if (mode === 'manual') {
    item.version = ''
    return
  }
  // 切到自动安装：默认选最新版本，省得用户再点一次
  const versions = versionsOf(category.id)
  if (!item.version && versions.length > 0) {
    item.version = versions[0].version
  }
}

// versionInvalid 手填版本的基本格式检查（最终以后端校验为准）：
// 只拦明显写错的（如 latest、12），避免保存后才报错。
function versionInvalid(category: ToolCategory, version?: string): boolean {
  const raw = (version ?? '').trim()
  if (!raw) {
    return false
  }
  if (category.id === 'nodejs') {
    return !/^\d+\.\d+\.\d+$/.test(raw.replace(/^v/, ''))
  }
  return false
}

// installNow 启动后台安装（服务端下载解压，离开页面也继续）。
async function installNow(category: ToolCategory, item: ToolInstallation): Promise<void> {
  if (!item.version) {
    ElMessage.error('请先选择版本')
    return
  }
  if (versionInvalid(category, item.version)) {
    ElMessage.error(`版本号格式不对：${item.version}（${catalogOf(category.id)?.version_hint || '需形如 18.20.4'}）`)
    return
  }
  try {
    const task = await systemApi.installTool(category.id, item.version)
    tasks.value = [task, ...tasks.value.filter((t) => !(t.type === task.type && t.version === task.version))]
    syncPolling()
    if (task.state === 'done' && task.home) {
      item.home = task.home
      ElMessage.success(`已安装到 ${task.home}`)
      return
    }
    ElMessage.success('已开始后台安装，可以离开本页，完成后自动可用')
  } catch (e) {
    ElMessage.error(describeError(e))
  }
}

function addInstallation(category: ToolCategory): void {
  category.installations.push({ name: '', home: '', note: '', install_mode: 'manual', version: '' })
}

function removeInstallation(category: ToolCategory, index: number): void {
  category.installations.splice(index, 1)
}

// effectiveIndex 返回该类别的「生效项」下标：与后端一致，取第一个填写了主目录的安装项。
// 其余项只做登记（名称/说明），不会注入环境变量——界面上标出来，避免以为可以多版本并存。
function effectiveIndex(category: ToolCategory): number {
  // 自动安装的项即使还没下载也算生效：构建开始时会先下载再用。
  return category.installations.findIndex(
    (item) => item.install_mode === 'auto' || (item.home ?? '').trim() !== '',
  )
}

// 按工具类别给出主目录示例，降低填写成本。
function homePlaceholder(categoryId: string): string {
  switch (categoryId) {
    case 'jdk':
      return '如 C:/Program Files/Java/jdk-17 或 /usr/lib/jvm/jdk-17'
    case 'maven':
      return '如 C:/apache-maven-3.9.6 或 /opt/maven'
    case 'gradle':
      return '如 C:/gradle-8.7 或 /opt/gradle'
    case 'ant':
      return '如 C:/apache-ant-1.10.14 或 /opt/ant'
    case 'golang':
      return '如 C:/Program Files/Go 或 /usr/local/go'
    case 'nodejs':
      return '如 C:/Program Files/nodejs 或 /usr/local/node'
    case 'git':
      return '如 C:/Program Files/Git 或 /usr/bin/git'
    default:
      return '工具主目录（绝对路径）'
  }
}

async function onSave(): Promise<void> {
  if (!base) {
    return
  }
  const invalid = categories.value.some((category) =>
    category.installations.some((item) => !item.name.trim()),
  )
  if (invalid) {
    ElMessage.error('安装项的名称不能为空')
    return
  }
  saving.value = true
  try {
    const updated = await systemApi.updateSettings({
      ...base,
      tool_categories: categories.value.map((category) => ({
        ...category,
        installations: category.installations.map((item) => ({
          name: item.name.trim(),
          // 自动安装的主目录由平台下载后回填；手动模式才是用户填的路径
          home: (item.home ?? '').trim(),
          note: (item.note ?? '').trim(),
          install_mode: item.install_mode === 'auto' ? 'auto' : 'manual',
          version: item.install_mode === 'auto' ? (item.version ?? '') : '',
        })),
      })),
      maven: maven.value,
    })
    base = updated
    categories.value = updated.tool_categories.map(cloneCategory)
    maven.value = { ...updated.maven }
    settingsStore.set(updated)
    ElMessage.success('全局工具配置已保存')
  } catch (e) {
    ElMessage.error(describeError(e))
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <div class="page">
    <ManagePageHeader title="全局工具配置" description="配置服务器上已安装的构建工具位置（对应 Jenkins 全局工具配置）">
      <template #actions>
        <el-button :loading="loading" @click="load">重新加载</el-button>
        <el-button type="primary" :loading="saving" @click="onSave">保存</el-button>
      </template>
    </ManagePageHeader>

    <div v-loading="loading">
      <el-alert
        type="info"
        :closable="false"
        show-icon
        class="tool-alert"
        title="两种用法：手动填服务器上已装的路径，或选版本让平台自动下载安装"
        description="生效的安装项会把主目录注入 JAVA_HOME / MAVEN_HOME / GRADLE_HOME / ANT_HOME / GOROOT / NODE_HOME，并把「主目录/bin」与主目录本身加入 PATH（兼容 Windows 版 Node，npm 才找得到）。每个类别只取第一个可用的安装项。自动安装的工具放在 data/tools，首次构建时按需下载；内网无法直连外网时用 -tools-mirror 指定镜像，或改用手动路径。"
      />

      <el-card shadow="never" class="tool-card">
        <template #header>
          <div class="tool-card__head">
            <div>
              <span class="tool-card__title">Maven 配置</span>
              <span class="tool-card__hint">settings.xml 内容仅作记录，方便团队共享</span>
            </div>
          </div>
        </template>
        <el-form label-width="180px" label-position="right">
          <el-form-item label="全局 MAVEN_OPTS">
            <el-input v-model="maven.maven_opts" class="mono" placeholder="如 -Xmx1024m" style="max-width: 520px" />
            <div class="form-tip">注入到每次构建环境的 <span class="mono">MAVEN_OPTS</span>。</div>
          </el-form-item>
          <el-form-item label="本地仓库路径">
            <el-input
              v-model="maven.local_repo"
              class="mono"
              placeholder="如 C:/Users/you/.m2/repository 或 /root/.m2/repository"
              style="max-width: 520px"
            />
            <div class="form-tip">
              以 <span class="mono">-Dmaven.repo.local</span> 注入构建，空值表示沿用服务器默认的 ~/.m2/repository。
            </div>
          </el-form-item>
          <el-form-item label="settings.xml">
            <div class="form-tip">
              平台不托管 settings.xml：需要私有仓库 / 镜像时，把文件放在服务器的 ~/.m2/settings.xml，
              或在构建步骤里用 <span class="mono">mvn -s /path/to/settings.xml</span> 指定。
            </div>
          </el-form-item>
        </el-form>
      </el-card>

      <el-card v-for="category in categories" :key="category.id" shadow="never" class="tool-card">
        <template #header>
          <div class="tool-card__head">
            <div>
              <span class="tool-card__title">{{ category.title }}</span>
              <span v-if="category.hint" class="tool-card__hint">{{ category.hint }}</span>
            </div>
            <el-button size="small" @click="addInstallation(category)">新增{{ category.title }}</el-button>
          </div>
        </template>

        <el-table :data="category.installations">
          <el-table-column label="名称" min-width="150">
            <template #default="{ row }">
              <el-input v-model="row.name" placeholder="如 JDK 17 / Maven 3.9.6" />
            </template>
          </el-table-column>
          <el-table-column label="安装方式" width="150">
            <template #default="{ row }">
              <el-radio-group
                :model-value="row.install_mode === 'auto' ? 'auto' : 'manual'"
                size="small"
                @update:model-value="(v: string) => setMode(category, row, v === 'auto' ? 'auto' : 'manual')"
              >
                <el-radio-button value="manual">手动路径</el-radio-button>
                <el-radio-button
                  value="auto"
                  :disabled="!catalogOf(category.id)?.auto_install"
                >自动安装</el-radio-button>
              </el-radio-group>
            </template>
          </el-table-column>
          <el-table-column label="主目录 / 版本" min-width="380">
            <template #default="{ row }">
              <!-- 自动安装：选版本 → 平台下载解压 → 回填主目录 -->
              <div v-if="row.install_mode === 'auto'" class="tool-auto">
                <el-select
                  v-model="row.version"
                  placeholder="选择版本"
                  style="width: 190px"
                  filterable
                  allow-create
                  default-first-option
                  :filter-method="undefined"
                >
                  <el-option
                    v-for="v in versionsOf(category.id)"
                    :key="v.version"
                    :label="v.version + (v.note ? `（${v.note}）` : '')"
                    :value="v.version"
                    :disabled="!v.supported"
                  />
                  <!-- 手填的补丁版本（如 Node 18.20.4）按下载地址模板现算，无需预置 -->
                  <template #footer>
                    <span class="tool-auto__tip">
                      {{ catalogOf(category.id)?.version_hint || '可直接输入版本号' }}
                    </span>
                  </template>
                </el-select>
                <el-button
                  size="small"
                  :loading="!!taskOf(category.id, row.version) && taskOf(category.id, row.version)?.state !== 'failed'"
                  :disabled="!!taskOf(category.id, row.version) && taskOf(category.id, row.version)?.state !== 'failed'"
                  @click="installNow(category, row)"
                >{{ versionState(category.id, row.version)?.installed ? '重新安装' : '立即下载' }}</el-button>
                <!-- 后台任务进度：离开页面也会继续，回来还能看到 -->
                <template v-if="taskOf(category.id, row.version)">
                  <el-progress
                    v-if="taskOf(category.id, row.version)?.state === 'downloading' || taskOf(category.id, row.version)?.state === 'extracting'"
                    :percentage="taskPercent(taskOf(category.id, row.version))"
                    :indeterminate="!taskOf(category.id, row.version)?.total"
                    :stroke-width="10"
                    style="width: 140px"
                  />
                  <span
                    class="tool-auto__task"
                    :class="{ 'is-error': taskOf(category.id, row.version)?.state === 'failed' }"
                  >{{ taskText(taskOf(category.id, row.version)) }}</span>
                </template>
                <el-tag
                  v-if="versionState(category.id, row.version)?.installed"
                  size="small"
                  type="success"
                  effect="plain"
                >已安装</el-tag>
                <el-tag
                  v-else-if="row.version && versionState(category.id, row.version)?.supported === false"
                  size="small"
                  type="warning"
                  effect="plain"
                >当前平台无下载包</el-tag>
                <el-tag v-else size="small" type="info" effect="plain">未下载（构建时自动下载）</el-tag>
                <span v-if="row.home" class="mono tool-auto__home" :title="row.home">{{ row.home }}</span>
              </div>
              <!-- 手动路径：用服务器上已装好的工具 -->
              <el-input
                v-else
                v-model="row.home"
                class="mono"
                :placeholder="homePlaceholder(category.id)"
              />
            </template>
          </el-table-column>
          <el-table-column label="构建时" width="110">
            <template #default="{ $index }">
              <el-tooltip
                v-if="effectiveIndex(category) === $index"
                :content="`会注入 ${catalogOf(category.id)?.home_env || '主目录环境变量'} 并加入 PATH`"
                placement="top"
              >
                <el-tag size="small" type="success" effect="plain" disable-transitions>生效</el-tag>
              </el-tooltip>
              <el-tooltip
                v-else
                content="同类别只取第一个可用（有主目录）的安装项，其余仅作登记"
                placement="top"
              >
                <el-tag size="small" type="info" effect="plain" disable-transitions>仅记录</el-tag>
              </el-tooltip>
            </template>
          </el-table-column>
          <el-table-column label="说明" min-width="140">
            <template #default="{ row }">
              <el-input v-model="row.note" placeholder="可选" />
            </template>
          </el-table-column>
          <el-table-column label="操作" width="90" align="right">
            <template #default="{ $index }">
              <el-button size="small" type="danger" plain @click="removeInstallation(category, $index)">删除</el-button>
            </template>
          </el-table-column>
          <template #empty>
            <div class="tool-empty">
              尚未配置：点右上角「新增{{ category.title }}」，选「手动路径」填服务器已有路径，或选「自动安装」挑版本由平台下载。
            </div>
          </template>
        </el-table>
      </el-card>
    </div>
  </div>
</template>

<style scoped>
.tool-auto {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}

.tool-auto__tip {
  font-size: 11px;
  color: var(--el-text-color-secondary);
  padding: 0 4px;
}

.tool-auto__task {
  font-size: 11px;
  color: var(--el-color-primary);
  max-width: 240px;
}

.tool-auto__task.is-error {
  color: var(--el-color-danger);
}

.tool-auto__home {
  font-size: 11px;
  color: var(--el-text-color-secondary);
  max-width: 240px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.tool-card {
  max-width: 1240px;
  margin-bottom: 16px;
}
.tool-alert {
  max-width: 1240px;
  margin-bottom: 16px;
}
.tool-card__head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}
.tool-card__title {
  font-weight: 600;
  color: #303133;
}
.tool-card__hint {
  margin-left: 12px;
  font-size: 12px;
  color: #909399;
}
.tool-empty {
  padding: 12px 0;
  color: #a8abb2;
  font-size: 13px;
}
</style>

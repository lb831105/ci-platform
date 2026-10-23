<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { ElMessage } from 'element-plus'
import ManagePageHeader from '@/components/ManagePageHeader.vue'
import { systemApi } from '@/api/system'
import { describeError } from '@/api/request'
import { useSettingsStore } from '@/stores/settingsStore'
import type { MavenSettings, Settings, ToolCategory } from '@/types/system'

const settingsStore = useSettingsStore()

const loading = ref(false)
const saving = ref(false)
const categories = ref<ToolCategory[]>([])
const maven = ref<MavenSettings>({ default_settings: '', global_settings: '', maven_opts: '', local_repo: '' })
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
  } catch (e) {
    ElMessage.error(describeError(e))
  } finally {
    loading.value = false
  }
}

onMounted(load)

function addInstallation(category: ToolCategory): void {
  category.installations.push({ name: '', home: '', note: '' })
}

function removeInstallation(category: ToolCategory, index: number): void {
  category.installations.splice(index, 1)
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
          home: (item.home ?? '').trim(),
          note: (item.note ?? '').trim(),
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
        title="平台直接在服务器上执行构建，不需要 Docker 镜像"
        description="填写各工具的主目录后，构建时会注入 JAVA_HOME / MAVEN_HOME / GRADLE_HOME / ANT_HOME / GOROOT / NODE_HOME 等变量，并把「主目录/bin」与主目录本身加入 PATH（兼容 Windows 版 Node）。每个类别取第一个填写了主目录的安装项。"
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
          <el-form-item label="默认 settings 提供">
            <el-input
              v-model="maven.default_settings"
              type="textarea"
              :rows="6"
              placeholder="settings.xml 内容"
              class="xml-input"
            />
            <div class="form-tip">
              对应 Jenkins 的「默认 settings 提供」。当前版本不会自动写入服务器的 ~/.m2/settings.xml，
              如需生效请手动放置，或在构建步骤中通过 <span class="mono">-s</span> 指定文件。
            </div>
          </el-form-item>
          <el-form-item label="默认全局 settings 提供">
            <el-input
              v-model="maven.global_settings"
              type="textarea"
              :rows="6"
              placeholder="全局 settings.xml 内容（通常由 CI 管理员统一维护）"
              class="xml-input"
            />
          </el-form-item>
          <el-form-item label="Pipeline Maven Configuration">
            <el-alert
              type="info"
              :closable="false"
              show-icon
              title="基础版不注入数据库：无 DAO / JDBC 配置，Maven 构建直接使用服务器上的 mvn。"
            />
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
          <el-table-column label="名称" min-width="180">
            <template #default="{ row }">
              <el-input v-model="row.name" placeholder="如 JDK 17 / Maven 3.9.6" />
            </template>
          </el-table-column>
          <el-table-column label="主目录（服务器路径）" min-width="300">
            <template #default="{ row }">
              <el-input
                v-model="row.home"
                class="mono"
                :placeholder="homePlaceholder(category.id)"
              />
            </template>
          </el-table-column>
          <el-table-column label="说明" min-width="160">
            <template #default="{ row }">
              <el-input v-model="row.note" placeholder="可选" />
            </template>
          </el-table-column>
          <el-table-column label="操作" width="100" align="right">
            <template #default="{ $index }">
              <el-button size="small" type="danger" plain @click="removeInstallation(category, $index)">删除</el-button>
            </template>
          </el-table-column>
          <template #empty>
            <div class="tool-empty">
              尚未配置。点击右上角「新增{{ category.title }}」填写服务器上的工具主目录。
            </div>
          </template>
        </el-table>
      </el-card>
    </div>
  </div>
</template>

<style scoped>
.tool-card {
  max-width: 1100px;
  margin-bottom: 16px;
}
.tool-alert {
  max-width: 1100px;
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
.xml-input :deep(textarea) {
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  font-size: 12px;
}
</style>

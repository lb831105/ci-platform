<script setup lang="ts">
import { computed } from 'vue'
import { RouterLink } from 'vue-router'
import { useSettingsStore } from '@/stores/settingsStore'
import type { SshPublishConfig } from '@/types/pipeline'

const props = defineProps<{
  modelValue: SshPublishConfig
  // 多目标编辑时由父组件传入「发布目标：<名称>」，单目标可省略。
  title?: string
}>()

const emit = defineEmits<{
  (event: 'update:modelValue', value: SshPublishConfig): void
}>()

const settings = useSettingsStore()
const config = computed(() => props.modelValue)
const servers = computed(() => settings.sshServers)
const selectedServer = computed(
  () => servers.value.find((server) => server.id === config.value.server_id) ?? null,
)
// 旧版任务内联配置：没有 server_id，但有 host/username。
const isLegacy = computed(
  () => !config.value.server_id && (config.value.host !== '' || config.value.username !== ''),
)

function patch(part: Partial<SshPublishConfig>): void {
  emit('update:modelValue', { ...props.modelValue, ...part })
}

function updateSourceFiles(value: string[]): void {
  patch({ source_files: value })
}
</script>

<template>
  <el-collapse class="ssh-config">
    <el-collapse-item name="ssh">
      <template #title>
        <span class="ssh-config__title">{{ title ?? 'SSH 发布（Publish Over SSH）' }}</span>
        <el-tag v-if="config.enabled" type="warning" effect="plain" size="small" disable-transitions>
          已启用
        </el-tag>
        <el-tag v-else type="info" effect="plain" size="small" disable-transitions>未启用</el-tag>
      </template>

      <el-alert
        type="info"
        :closable="false"
        show-icon
        class="ssh-config__alert"
        title="SSH 连接信息统一在「系统管理 → 系统配置 → SSH 服务器」中维护，并持久化到 settings.json"
        description="任务只选择服务器与要上传的文件，不再重复保存主机、账号和密码；服务器信息修改后所有引用它的任务自动生效。"
      />

      <el-form label-width="140px" label-position="right">
        <el-form-item label="启用 SSH 发布">
          <el-switch
            :model-value="config.enabled"
            @update:model-value="(value: boolean) => patch({ enabled: value })"
          />
          <span class="form-tip">关闭时构建成功即结束，不会尝试连接目标服务器。</span>
        </el-form-item>

        <template v-if="config.enabled">
          <el-form-item label="SSH 服务器">
            <template v-if="servers.length > 0">
              <el-select
                :model-value="config.server_id"
                clearable
                filterable
                placeholder="选择系统配置中的 SSH 服务器"
                style="width: 100%"
                @update:model-value="(value: string) => patch({ server_id: value ?? '' })"
              >
                <el-option
                  v-for="server in servers"
                  :key="server.id"
                  :label="`${server.name}（${server.username}@${server.host}）`"
                  :value="server.id"
                />
              </el-select>
              <div class="form-tip">
                在「系统配置 → SSH 服务器」中集中维护；密码保存后不回显，任务侧不保存任何密钥。
              </div>
            </template>
            <el-alert v-else type="warning" :closable="false" show-icon title="还没有配置 SSH 服务器">
              <template #default>
                <RouterLink :to="{ name: 'system-configure' }">前往系统配置添加 SSH 服务器</RouterLink>
              </template>
            </el-alert>
          </el-form-item>

          <el-descriptions
            v-if="selectedServer"
            :column="2"
            border
            size="small"
            class="ssh-config__summary"
          >
            <el-descriptions-item label="主机">
              {{ selectedServer.host }}:{{ selectedServer.port }}
            </el-descriptions-item>
            <el-descriptions-item label="用户">{{ selectedServer.username }}</el-descriptions-item>
            <el-descriptions-item label="认证方式">
              {{ selectedServer.auth_type === 'password' ? '密码' : `密钥（${selectedServer.private_key_path || '-'}）` }}
            </el-descriptions-item>
            <el-descriptions-item label="默认远程目录">
              {{ selectedServer.remote_base_dir || '-' }}
            </el-descriptions-item>
          </el-descriptions>

          <el-alert
            v-if="isLegacy"
            type="warning"
            :closable="false"
            show-icon
            class="ssh-config__alert"
            title="该任务仍使用旧版内联 SSH 配置"
          >
            <template #default>
              当前主机为 <span class="mono">{{ config.host }}</span>。建议在「系统配置 → SSH 服务器」新增对应服务器后，
              在上方选择它并保存；保存后任务里的主机/账号/密码会被清除，只保留服务器引用。
            </template>
          </el-alert>

          <el-form-item label="远程基础目录">
            <el-input
              :model-value="config.remote_base_dir"
              class="mono"
              :placeholder="selectedServer?.remote_base_dir || '/opt/app/releases'"
              @update:model-value="(value: string) => patch({ remote_base_dir: value })"
            />
            <div class="form-tip">相对服务器「默认远程目录」拼接：填 gomysqldump、服务器默认 /data/projects 时，上传到 /data/projects/gomysqldump；留空时直接使用服务器配置里的默认远程目录。</div>
          </el-form-item>

          <el-form-item label="待上传文件">
            <el-select
              :model-value="config.source_files"
              multiple
              filterable
              allow-create
              default-first-option
              class="ssh-config__files"
              placeholder="输入 glob 后回车，如 target/*.jar"
              @update:model-value="updateSourceFiles"
            >
              <el-option v-for="pattern in config.source_files" :key="pattern" :label="pattern" :value="pattern" />
            </el-select>
            <div class="form-tip">相对构建工作区的 glob 表达式，支持多个。</div>
          </el-form-item>

          <el-form-item label="剥离前缀">
            <el-input
              :model-value="config.remove_prefix"
              class="mono"
              placeholder="如 target/，上传时从路径中移除"
              @update:model-value="(value: string) => patch({ remove_prefix: value })"
            />
          </el-form-item>

          <el-form-item label="目录与文件">
            <div class="ssh-config__options">
              <el-checkbox
                :model-value="config.flatten ?? false"
                @update:model-value="(value: boolean) => patch({ flatten: value })"
              >平铺文件（Flatten files）</el-checkbox>
              <el-checkbox
                :model-value="config.clean_remote ?? false"
                @update:model-value="(value: boolean) => patch({ clean_remote: value })"
              >发布前清空远程目录（Clean remote）</el-checkbox>
            </div>
            <div class="form-tip">
              「平铺」只上传文件名、不保留工作区里的目录结构（对齐 Jenkins Flatten files）；
              「清空」会在上传前递归删除远程基础目录，适合整目录替换部署——不可逆，请确认目录填写正确。
            </div>
            <el-alert
              v-if="config.clean_remote"
              type="warning"
              :closable="false"
              show-icon
              style="width: 100%; margin-top: 8px"
              :title="`每次发布都会先删除远程目录 ${config.remote_base_dir || selectedServer?.remote_base_dir || '（未填写）'} 下的全部内容`"
            />
          </el-form-item>

          <el-form-item label="远程执行命令">
            <el-input
              :model-value="config.exec_command"
              type="textarea"
              :rows="2"
              class="mono"
              placeholder="如 cd /opt/app/current && ./restart.sh"
              @update:model-value="(value: string) => patch({ exec_command: value })"
            />
            <div class="form-tip">为空则只上传文件不执行命令；输出会合并到构建日志。</div>
          </el-form-item>

          <el-form-item label="命令超时（秒）">
            <el-input-number
              :model-value="config.exec_timeout_sec ?? 0"
              :min="0"
              :max="86400"
              :step="30"
              :disabled="!(config.exec_command ?? '').trim()"
              controls-position="right"
              @update:model-value="(value: number | undefined) => patch({ exec_timeout_sec: value ?? 0 })"
            />
            <div class="form-tip">
              0 表示沿用构建级超时（全局「单次构建超时」）；超时会终止远程会话并让发布失败（对齐 Jenkins execTimeout）。
            </div>
          </el-form-item>
        </template>
      </el-form>
    </el-collapse-item>
  </el-collapse>
</template>

<style scoped>
.ssh-config__options {
  display: flex;
  align-items: center;
  gap: 20px;
  flex-wrap: wrap;
}

.ssh-config__title {
  margin-right: 8px;
  font-weight: 500;
}
.ssh-config__alert {
  margin-bottom: 16px;
}
.ssh-config__summary {
  max-width: 760px;
  margin: 0 0 16px 140px;
}
.ssh-config__files {
  width: 100%;
}
</style>

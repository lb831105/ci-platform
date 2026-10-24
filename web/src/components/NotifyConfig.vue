<script setup lang="ts">
import { computed } from 'vue'
import {
  NOTIFY_TEMPLATE_VARS,
  emptyEscalation,
  emptyJobNotify,
  emptyOnCall,
  emptyRoute,
  noNotifyEvents,
  type EscalationConfig,
  type JobNotify,
  type NotifyChannel,
  type NotifyContact,
  type NotifyRoute,
  type NotifyTarget,
  type OnCallConfig,
} from '@/types/pipeline'

const props = defineProps<{
  modelValue: JobNotify | null
  contacts: NotifyContact[]
  testing: boolean
}>()
const emit = defineEmits<{
  (e: 'update:modelValue', value: JobNotify | null): void
  (e: 'test'): void
}>()

// 关闭（null）表示沿用全局默认；首次开启时用默认事件（失败 + 修复）。
const notify = computed<JobNotify>(() => props.modelValue ?? emptyJobNotify())
const enabled = computed({
  get: () => props.modelValue?.enabled ?? false,
  set: (value: boolean) => {
    const base = props.modelValue ?? emptyJobNotify()
    emit('update:modelValue', { ...base, enabled: value })
  },
})

function patch(partial: Partial<JobNotify>): void {
  emit('update:modelValue', { ...notify.value, ...partial })
}

const CHANNELS: Array<{ value: NotifyChannel; label: string }> = [
  { value: 'dingtalk', label: '钉钉' },
  { value: 'wecom', label: '企业微信' },
  { value: 'slack', label: 'Slack' },
]

function addTarget(): void {
  // 默认 events 全 false = 继承任务级事件；不能用 emptyNotifyEvents()，
  // 否则新群会悄悄只订阅「失败+修复」，覆盖任务级勾选的成功/取消。
  const targets: NotifyTarget[] = [
    ...notify.value.targets,
    { channel: 'dingtalk', url: '', events: noNotifyEvents() },
  ]
  patch({ targets })
}

function removeTarget(index: number): void {
  const targets = notify.value.targets.filter((_, i) => i !== index)
  patch({ targets })
}

// 群目标的更新一律走 patch（生成新数组 + emit），不直接改 props 上的对象。
function patchTarget(index: number, partial: Partial<NotifyTarget>): void {
  const targets = notify.value.targets.map((t, i) => (i === index ? { ...t, ...partial } : t))
  patch({ targets })
}

// 每个群的事件订阅：用一个下拉表达常见组合（后端仍存 4 个布尔）。
type TargetEventMode = 'inherit' | 'failure' | 'failure_fixed' | 'all' | 'custom'

const TARGET_EVENT_MODES: Array<{ value: TargetEventMode; label: string }> = [
  { value: 'inherit', label: '继承任务事件' },
  { value: 'failure', label: '仅失败' },
  { value: 'failure_fixed', label: '失败 + 修复' },
  { value: 'all', label: '全部事件' },
  // 后端或旧数据里的非预设组合：只展示，避免用户误触后被静默改写。
  { value: 'custom', label: '自定义（保留原设置）' },
]

function targetEventMode(t: NotifyTarget): TargetEventMode {
  const e = t.events
  if (!e.on_failure && !e.on_success && !e.on_fixed && !e.on_aborted) return 'inherit'
  if (e.on_failure && !e.on_success && !e.on_fixed && !e.on_aborted) return 'failure'
  if (e.on_failure && !e.on_success && e.on_fixed && !e.on_aborted) return 'failure_fixed'
  if (e.on_failure && e.on_success && e.on_fixed && e.on_aborted) return 'all'
  // 非预设组合（例如仅「成功」）：标记为自定义，切换时才按用户选择覆盖。
  return 'custom'
}

function setTargetEventMode(index: number, mode: TargetEventMode): void {
  if (mode === 'custom') {
    return // 自定义值保持不变
  }
  const map: Record<Exclude<TargetEventMode, 'custom'>, NotifyTarget['events']> = {
    inherit: noNotifyEvents(),
    failure: { on_failure: true, on_success: false, on_fixed: false, on_aborted: false },
    failure_fixed: { on_failure: true, on_success: false, on_fixed: true, on_aborted: false },
    all: { on_failure: true, on_success: true, on_fixed: true, on_aborted: true },
  }
  patchTarget(index, { events: map[mode] })
}

// ---- 值班轮值 ----
const onCall = computed<OnCallConfig>(() => props.modelValue?.on_call ?? emptyOnCall())
function patchOnCall(partial: Partial<OnCallConfig>): void {
  patch({ on_call: { ...onCall.value, ...partial } })
}

// ---- 升级策略 ----
const escalation = computed<EscalationConfig>(
  () => props.modelValue?.escalation ?? emptyEscalation(),
)
function patchEscalation(partial: Partial<EscalationConfig>): void {
  patch({ escalation: { ...escalation.value, ...partial } })
}
function addEscalationTarget(): void {
  patchEscalation({
    targets: [
      ...escalation.value.targets,
      { channel: 'dingtalk', url: '', events: noNotifyEvents() },
    ],
  })
}
function patchEscalationTarget(index: number, partial: Partial<NotifyTarget>): void {
  const targets = escalation.value.targets.map((t, i) => (i === index ? { ...t, ...partial } : t))
  patchEscalation({ targets })
}
function removeEscalationTarget(index: number): void {
  patchEscalation({ targets: escalation.value.targets.filter((_, i) => i !== index) })
}

// ---- 分支路由 ----
const routes = computed<NotifyRoute[]>(() => props.modelValue?.routes ?? [])
function patchRoute(index: number, partial: Partial<NotifyRoute>): void {
  patch({ routes: routes.value.map((r, i) => (i === index ? { ...r, ...partial } : r)) })
}
function addRoute(): void {
  patch({ routes: [...routes.value, emptyRoute()] })
}
function removeRoute(index: number): void {
  patch({ routes: routes.value.filter((_, i) => i !== index) })
}
function addRouteTarget(index: number): void {
  patchRoute(index, {
    targets: [
      ...routes.value[index].targets,
      { channel: 'dingtalk', url: '', events: noNotifyEvents() },
    ],
  })
}
function patchRouteTarget(routeIndex: number, targetIndex: number, partial: Partial<NotifyTarget>): void {
  const targets = routes.value[routeIndex].targets.map((t, i) =>
    i === targetIndex ? { ...t, ...partial } : t,
  )
  patchRoute(routeIndex, { targets })
}
function removeRouteTarget(routeIndex: number, targetIndex: number): void {
  patchRoute(routeIndex, {
    targets: routes.value[routeIndex].targets.filter((_, i) => i !== targetIndex),
  })
}

// ---- 抑制窗口 ----
function setSuppressWindow(value: number | undefined): void {
  // 1 分钟几乎等于永久静默，后端会直接拒绝；这里归零避免用户撞错误提示。
  const n = typeof value === 'number' && Number.isFinite(value) ? Math.trunc(value) : 0
  patch({ suppress_window_min: n === 1 ? 0 : Math.max(0, Math.min(1440, n)) })
}

// ---- 消息模板 ----
function setTemplate(value: string): void {
  patch({ template: value })
}

// 占位符提示在 script 里拼好：直接在模板插值里写 '{{' / '}}' 会被 Vue 解析成嵌套插值。
const templateVarHint = NOTIFY_TEMPLATE_VARS.map((v) => '{{' + v + '}}').join(' ')
const templatePlaceholder =
  '留空使用内置格式。例：❌ {{job}} #{{build}} 在 {{branch}} 失败（连败 {{streak}} 次），值班 {{oncall}}'

// 人员下拉：显示「用户名（邮箱）」，未配置邮箱的人标注出来，避免配了却收不到。
const peopleOptions = computed(() =>
  props.contacts.map((c) => ({
    value: c.name,
    label: c.email ? `${c.name}（${c.email}）` : `${c.name}（缺邮箱，收不到邮件）`,
  })),
)

function contactHint(name: string): string {
  const c = props.contacts.find((x) => x.name === name)
  if (!c) return '用户不存在'
  const parts: string[] = []
  if (c.email) parts.push(c.email)
  if (c.wecom_id) parts.push('企微:' + c.wecom_id)
  if (c.phone) parts.push('钉钉:' + c.phone)
  return parts.length ? parts.join(' / ') : '未配置联系方式'
}

// 事件标签（任务级 / 群级共用）。
const EVENT_FIELDS: Array<{ key: 'on_failure' | 'on_success' | 'on_fixed' | 'on_aborted'; label: string }> = [
  { key: 'on_failure', label: '失败时' },
  { key: 'on_success', label: '成功时' },
  { key: 'on_fixed', label: '修复时（上次失败→本次成功）' },
  { key: 'on_aborted', label: '取消时' },
]
</script>

<template>
  <div class="notify">
    <div class="notify__head">
      <h3 class="notify__title">构建通知（项目 × 人员 / 群）</h3>
      <el-switch v-model="enabled" active-text="启用任务级通知" />
    </div>
    <p class="notify__hint">
      为当前任务指定收件人与群机器人：不同项目的告警发给不同的人/群。
      {{ modelValue?.enabled === false ? '当前沿用「系统配置 → 邮件通知 / Webhook 通知」的全局默认。' : '' }}
    </p>

    <template v-if="enabled">
      <el-form label-width="150px" label-position="right">
        <el-form-item label="通知事件">
          <div class="notify__events">
            <el-checkbox
              v-for="f in EVENT_FIELDS"
              :key="f.key"
              :model-value="notify.events[f.key]"
              @update:model-value="(v: boolean) => patch({ events: { ...notify.events, [f.key]: v } })"
            >{{ f.label }}</el-checkbox>
          </div>
        </el-form-item>

        <el-form-item label="相关人员">
          <el-select
            :model-value="notify.people"
            multiple
            filterable
            allow-create
            default-first-option
            placeholder="选择平台用户（自动解析邮箱 / 企微 ID / 手机号），也可直接输入用户名"
            style="width: 100%"
            @update:model-value="(v: string[]) => patch({ people: v })"
          >
            <el-option
              v-for="opt in peopleOptions"
              :key="opt.value"
              :label="opt.label"
              :value="opt.value"
            />
          </el-select>
          <div v-if="notify.people.length" class="notify__people">
            <el-tag v-for="name in notify.people" :key="name" size="small" effect="plain" class="mono">
              {{ name }} · {{ contactHint(name) }}
            </el-tag>
          </div>
          <div class="form-tip">
            联系方式在「系统管理 → 用户管理」维护，用户也可在「个人资料」页自助修改；
            邮箱是邮件通知的收件地址，缺邮箱的用户收不到邮件（仍会参与群 @ 提醒），构建日志会给出警告。
          </div>
        </el-form-item>

        <el-form-item label="额外邮箱">
          <el-select
            :model-value="notify.emails"
            multiple
            filterable
            allow-create
            default-first-option
            placeholder="输入邮箱后回车（外部人员、邮件组）"
            style="width: 100%"
            @update:model-value="(v: string[]) => patch({ emails: v })"
          />
        </el-form-item>

        <el-form-item label="同时发送全局默认">
          <el-switch
            :model-value="notify.use_global"
            @update:model-value="(v: boolean) => patch({ use_global: v })"
          />
          <span class="form-tip" style="margin-left: 10px">
            开启后，除上方人员/群外，再发送「系统配置」中的全局默认收件人与机器人（叠加，不替换）。
          </span>
        </el-form-item>

        <el-form-item label="群机器人">
          <div class="notify__targets">
            <el-empty v-if="!notify.targets.length" description="未配置任务专属群（可添加钉钉/企微/Slack 机器人）" :image-size="60" />
            <div v-for="(t, i) in notify.targets" :key="t.channel + '-' + i" class="notify__target">
              <el-select
                :model-value="t.channel"
                style="width: 120px"
                @update:model-value="(v: NotifyChannel) => patchTarget(i, { channel: v })"
              >
                <el-option v-for="c in CHANNELS" :key="c.value" :label="c.label" :value="c.value" />
              </el-select>
              <el-input
                :model-value="t.url"
                class="mono"
                placeholder="机器人 Webhook 地址（URL 中的 token 保存后以 *** 显示）"
                style="flex: 1"
                @update:model-value="(v: string) => patchTarget(i, { url: v })"
              />
              <el-select
                :model-value="targetEventMode(t)"
                style="width: 130px"
                @update:model-value="(v: TargetEventMode) => setTargetEventMode(i, v)"
              >
                <el-option v-for="m in TARGET_EVENT_MODES" :key="m.value" :label="m.label" :value="m.value" />
              </el-select>
              <el-button link type="danger" @click="removeTarget(i)">删除</el-button>
            </div>
            <div>
              <el-button size="small" @click="addTarget">添加群机器人</el-button>
            </div>
            <div class="form-tip">
              钉钉/企微机器人需在安全设置中包含关键词「构建」；失败时会在群里 @ 相关人员（钉钉用手机号、企微用 userid）。
            </div>
          </div>
        </el-form-item>

        <el-divider content-position="left">运维增强</el-divider>

        <el-form-item label="值班轮值">
          <el-switch
            :model-value="onCall.enabled"
            active-text="失败时 @ 当周值班人"
            @update:model-value="(v: boolean) => patchOnCall({ enabled: v })"
          />
          <div v-if="onCall.enabled" class="notify__ops">
            <el-select
              :model-value="onCall.rotation"
              multiple
              filterable
              allow-create
              default-first-option
              placeholder="按顺序轮转的值班人（用户名或邮箱）"
              style="width: 100%"
              @update:model-value="(v: string[]) => patchOnCall({ rotation: v })"
            >
              <el-option v-for="opt in peopleOptions" :key="opt.value" :label="opt.label" :value="opt.value" />
            </el-select>
            <div class="notify__ops-row">
              <span class="notify__ops-label">轮值起点</span>
              <el-date-picker
                :model-value="onCall.start_date"
                type="date"
                value-format="YYYY-MM-DD"
                placeholder="留空=固定锚点轮转"
                style="width: 190px"
                @update:model-value="(v: string | null) => patchOnCall({ start_date: v ?? '' })"
              />
              <span class="notify__ops-label">每人值班</span>
              <el-input-number
                :model-value="onCall.period_days"
                :min="1"
                :max="90"
                controls-position="right"
                style="width: 120px"
                @update:model-value="(v: number | undefined) => patchOnCall({ period_days: v ?? 7 })"
              />
              <span class="form-tip">天（由日期推算，重启不影响结果）</span>
            </div>
          </div>
        </el-form-item>

        <el-form-item label="升级策略">
          <el-switch
            :model-value="escalation.enabled"
            active-text="连续失败达到阈值时升级"
            @update:model-value="(v: boolean) => patchEscalation({ enabled: v })"
          />
          <div v-if="escalation.enabled" class="notify__ops">
            <div class="notify__ops-row">
              <span class="notify__ops-label">连续失败</span>
              <el-input-number
                :model-value="escalation.after"
                :min="2"
                :max="20"
                controls-position="right"
                style="width: 120px"
                @update:model-value="(v: number | undefined) => patchEscalation({ after: v ?? 3 })"
              />
              <span class="form-tip">次后追加上级 / 更大群（升级优先于抑制窗口）</span>
            </div>
            <el-select
              :model-value="escalation.people"
              multiple
              filterable
              allow-create
              default-first-option
              placeholder="升级通知的人员（上级、负责人）"
              style="width: 100%"
              @update:model-value="(v: string[]) => patchEscalation({ people: v })"
            >
              <el-option v-for="opt in peopleOptions" :key="opt.value" :label="opt.label" :value="opt.value" />
            </el-select>
            <el-select
              :model-value="escalation.emails"
              multiple
              filterable
              allow-create
              default-first-option
              placeholder="升级邮箱（外部负责人、邮件组）"
              style="width: 100%"
              @update:model-value="(v: string[]) => patchEscalation({ emails: v })"
            />
            <div v-for="(t, i) in escalation.targets" :key="'esc-' + i" class="notify__target">
              <el-select
                :model-value="t.channel"
                style="width: 120px"
                @update:model-value="(v: NotifyChannel) => patchEscalationTarget(i, { channel: v })"
              >
                <el-option v-for="c in CHANNELS" :key="c.value" :label="c.label" :value="c.value" />
              </el-select>
              <el-input
                :model-value="t.url"
                class="mono"
                placeholder="升级群机器人 Webhook 地址"
                style="flex: 1"
                @update:model-value="(v: string) => patchEscalationTarget(i, { url: v })"
              />
              <el-button link type="danger" @click="removeEscalationTarget(i)">删除</el-button>
            </div>
            <div><el-button size="small" @click="addEscalationTarget">添加上级群</el-button></div>
          </div>
        </el-form-item>

        <el-form-item label="失败抑制">
          <el-input-number
            :model-value="modelValue?.suppress_window_min ?? 0"
            :min="0"
            :max="1440"
            :step="5"
            controls-position="right"
            style="width: 140px"
            @update:model-value="setSuppressWindow"
          />
          <span class="form-tip" style="margin-left: 10px">
            分钟。窗口内同一任务连续失败只发首条，后续合并（仍记录到通知历史）；0 表示不抑制。
          </span>
        </el-form-item>

        <el-form-item label="分支路由">
          <div class="notify__ops">
            <div v-for="(r, i) in routes" :key="'route-' + i" class="notify__route">
              <div class="notify__ops-row">
                <span class="notify__ops-label">分支</span>
                <el-input
                  :model-value="r.match"
                  class="mono"
                  placeholder="main / feature/* / release/**（留空=所有分支）"
                  style="width: 260px"
                  @update:model-value="(v: string) => patchRoute(i, { match: v })"
                />
                <el-checkbox
                  :model-value="r.on_call"
                  @update:model-value="(v: boolean) => patchRoute(i, { on_call: v })"
                >值班人</el-checkbox>
                <el-checkbox
                  :model-value="r.committer"
                  @update:model-value="(v: boolean) => patchRoute(i, { committer: v })"
                >提交者</el-checkbox>
                <el-button link type="danger" @click="removeRoute(i)">删除</el-button>
              </div>
              <el-select
                :model-value="r.people"
                multiple
                filterable
                allow-create
                default-first-option
                placeholder="该分支额外通知的人员"
                style="width: 100%"
                @update:model-value="(v: string[]) => patchRoute(i, { people: v })"
              >
                <el-option v-for="opt in peopleOptions" :key="opt.value" :label="opt.label" :value="opt.value" />
              </el-select>
              <el-select
                :model-value="r.emails"
                multiple
                filterable
                allow-create
                default-first-option
                placeholder="该分支额外通知的邮箱"
                style="width: 100%"
                @update:model-value="(v: string[]) => patchRoute(i, { emails: v })"
              />
              <div v-for="(t, ti) in r.targets" :key="'route-' + i + '-t' + ti" class="notify__target">
                <el-select
                  :model-value="t.channel"
                  style="width: 120px"
                  @update:model-value="(v: NotifyChannel) => patchRouteTarget(i, ti, { channel: v })"
                >
                  <el-option v-for="c in CHANNELS" :key="c.value" :label="c.label" :value="c.value" />
                </el-select>
                <el-input
                  :model-value="t.url"
                  class="mono"
                  placeholder="该分支通知的群机器人 Webhook 地址"
                  style="flex: 1"
                  @update:model-value="(v: string) => patchRouteTarget(i, ti, { url: v })"
                />
                <el-button link type="danger" @click="removeRouteTarget(i, ti)">删除</el-button>
              </div>
              <div>
                <el-button size="small" @click="addRouteTarget(i)">添加该分支的群</el-button>
              </div>
            </div>
            <div><el-button size="small" @click="addRoute">添加分支路由</el-button></div>
            <div class="form-tip">
              例：main 命中「值班人 + 运维群」；feature/* 勾选「提交者」即只通知本次 git 提交作者。
              `*` 不跨 `/`，`release/**` 可匹配多级目录。
            </div>
          </div>
        </el-form-item>

        <el-form-item label="消息模板">
          <el-input
            :model-value="modelValue?.template ?? ''"
            type="textarea"
            :rows="4"
            class="mono"
            :placeholder="templatePlaceholder"
            @update:model-value="setTemplate"
          />
          <div class="form-tip">
            占位符：<span class="mono">{{ templateVarHint }}</span>；
            未知占位符原样保留。邮件与群消息共用该模板，@ 提醒始终追加在末尾。
          </div>
        </el-form-item>

        <el-form-item label="测试">
          <el-button :loading="testing" @click="emit('test')">发送测试通知</el-button>
          <span class="form-tip" style="margin-left: 10px">先保存配置，再点此验证「项目 → 人员/群」是否正确。</span>
        </el-form-item>
      </el-form>
    </template>
  </div>
</template>

<style scoped>
.notify {
  border-top: 1px dashed var(--border-color, #e4e7ed);
  margin-top: 16px;
  padding-top: 14px;
}
.notify__head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}
.notify__title {
  margin: 0;
  font-size: 15px;
  font-weight: 600;
}
.notify__hint {
  color: var(--text-3, #909399);
  font-size: 12.5px;
  margin: 6px 0 14px;
}
.notify__events {
  display: flex;
  flex-wrap: wrap;
  gap: 4px 18px;
}
.notify__people {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  margin-top: 6px;
}
.notify__targets {
  width: 100%;
  display: flex;
  flex-direction: column;
  gap: 8px;
}
.notify__target {
  display: flex;
  gap: 8px;
  align-items: center;
}
.notify__ops {
  width: 100%;
  display: flex;
  flex-direction: column;
  gap: 8px;
  margin-top: 8px;
}
.notify__ops-row {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}
.notify__ops-label {
  color: var(--text-2, #606266);
  font-size: 12.5px;
}
.notify__route {
  border: 1px solid var(--border-color, #e4e7ed);
  border-radius: 6px;
  padding: 10px;
  display: flex;
  flex-direction: column;
  gap: 8px;
}
</style>

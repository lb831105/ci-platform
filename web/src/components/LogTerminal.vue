<script setup lang="ts">
import { nextTick, ref, watch } from 'vue'

const props = defineProps<{ lines: string[] }>()

const container = ref<HTMLDivElement | null>(null)
const autoScroll = ref(true)

watch(
  () => props.lines.length,
  async () => {
    if (!autoScroll.value) {
      return
    }
    await nextTick()
    const el = container.value
    if (el !== null) {
      el.scrollTop = el.scrollHeight
    }
  },
)

function toggleAutoScroll(): void {
  autoScroll.value = !autoScroll.value
  if (autoScroll.value) {
    void nextTick(() => {
      const el = container.value
      if (el !== null) {
        el.scrollTop = el.scrollHeight
      }
    })
  }
}

function scrollToBottom(): void {
  const el = container.value
  if (el !== null) {
    el.scrollTop = el.scrollHeight
  }
}
</script>

<template>
  <div class="log-terminal">
    <div class="log-terminal__toolbar">
      <span class="muted">{{ lines.length }} 行输出</span>
      <div class="log-terminal__ops">
        <el-button size="small" :type="autoScroll ? 'primary' : 'default'" @click="toggleAutoScroll">
          自动滚底：{{ autoScroll ? '开' : '关' }}
        </el-button>
        <el-button size="small" @click="scrollToBottom">跳到最新</el-button>
      </div>
    </div>

    <div ref="container" class="log-terminal__body mono">
      <div v-if="lines.length === 0" class="muted log-terminal__empty">暂无日志输出</div>
      <pre v-for="(line, index) in lines" :key="index" class="log-terminal__line">{{ line }}</pre>
    </div>
  </div>
</template>

<style scoped>
.log-terminal {
  border: 1px solid #2b2f36;
  border-radius: 6px;
  overflow: hidden;
  background: #14171c;
}
.log-terminal__toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 8px 12px;
  background: #1d2128;
  border-bottom: 1px solid #2b2f36;
}
.log-terminal__toolbar .muted {
  color: #8b93a1;
}
.log-terminal__ops {
  display: flex;
  gap: 8px;
}
.log-terminal__body {
  height: 60vh;
  min-height: 320px;
  overflow: auto;
  padding: 12px;
  color: #d7dde6;
  font-size: 12.5px;
  line-height: 1.55;
}
.log-terminal__line {
  margin: 0;
  white-space: pre-wrap;
  word-break: break-all;
}
.log-terminal__empty {
  color: #6b7280;
}
</style>

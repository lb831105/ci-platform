<script setup lang="ts">
import { computed } from 'vue'
import type { BuildStatus } from '@/types/build'

const props = defineProps<{ status: BuildStatus | 'none' }>()

const LABELS: Record<string, string> = {
  success: '成功',
  failed: '失败',
  running: '构建中',
  pending: '等待中',
  aborted: '已取消',
  none: '暂无构建',
}

const GLYPHS: Record<string, string> = {
  success: '✓',
  failed: '✕',
  running: '',
  pending: '',
  aborted: '■',
  none: '',
}

const label = computed(() => LABELS[props.status] ?? props.status)
const glyph = computed(() => GLYPHS[props.status] ?? '')
</script>

<template>
  <span class="ball" :class="`ball--${status}`" :title="label" :aria-label="label">
    <span v-if="glyph" class="ball__glyph">{{ glyph }}</span>
  </span>
</template>

<style scoped>
.ball {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 18px;
  height: 18px;
  border-radius: 50%;
  border: 1px solid rgba(0, 0, 0, 0.15);
  vertical-align: middle;
}
.ball__glyph {
  color: #fff;
  font-size: 11px;
  line-height: 1;
  font-weight: 700;
}
.ball--success {
  background: #4caf50;
}
.ball--failed {
  background: #f44336;
}
.ball--running {
  background: #2196f3;
  border-color: transparent;
  box-shadow: 0 0 0 2px rgba(33, 150, 243, 0.25);
  animation: pulse 1.2s ease-in-out infinite;
}
.ball--pending {
  background: #ffb300;
  border-color: transparent;
}
.ball--aborted {
  background: #9e9e9e;
}
.ball--none {
  background: #e0e0e0;
}
@keyframes pulse {
  0%,
  100% {
    opacity: 1;
  }
  50% {
    opacity: 0.45;
  }
}
</style>

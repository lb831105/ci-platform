<script setup lang="ts">
import { computed } from 'vue'
import type { BuildStatus } from '@/types/build'

// Jenkins 的天气列：由最近若干次构建的成功率决定健康度。
const props = defineProps<{ recent?: BuildStatus[] }>()

const weather = computed<{ level: string; label: string }>(() => {
  const finished = (props.recent ?? []).filter((s) => s === 'success' || s === 'failed')
  if (finished.length === 0) {
    return { level: 'none', label: '暂无足够构建数据' }
  }
  const ratio = finished.filter((s) => s === 'success').length / finished.length
  if (ratio >= 0.8) {
    return { level: 'sunny', label: `健康度 ${Math.round(ratio * 100)}%（晴朗）` }
  }
  if (ratio >= 0.5) {
    return { level: 'cloudy', label: `健康度 ${Math.round(ratio * 100)}%（多云）` }
  }
  if (ratio > 0) {
    return { level: 'rainy', label: `健康度 ${Math.round(ratio * 100)}%（有雨）` }
  }
  return { level: 'storm', label: `健康度 ${Math.round(ratio * 100)}%（暴风雨）` }
})
</script>

<template>
  <span class="weather" :class="`weather--${weather.level}`" :title="weather.label" :aria-label="weather.label" />
</template>

<style scoped>
.weather {
  display: inline-block;
  width: 16px;
  height: 16px;
  border-radius: 50%;
  vertical-align: middle;
  border: 1px solid rgba(0, 0, 0, 0.1);
}
.weather--sunny {
  background: linear-gradient(180deg, #ffd54f 0%, #ffca28 100%);
}
.weather--cloudy {
  background: linear-gradient(180deg, #cfd8dc 0%, #b0bec5 100%);
}
.weather--rainy {
  background: linear-gradient(180deg, #90a4ae 0%, #607d8b 100%);
}
.weather--storm {
  background: linear-gradient(180deg, #5c6bc0 0%, #3949ab 100%);
}
.weather--none {
  background: #eeeeee;
}
</style>

<script setup lang="ts">
/** 桌面端容器：居中弹窗 */
defineProps<{
  show: boolean
  width?: string
  height?: string
}>()

const emit = defineEmits<{
  (e: 'update:show', value: boolean): void
}>()
</script>

<template>
  <van-popup
    :show="show"
    teleport="body"
    closeable
    :close-on-click-overlay="true"
    :style="{
      width: width || 'min(780px, 92vw)',
      height: height || '86vh',
      borderRadius: '12px',
      overflow: 'hidden',
    }"
    @update:show="(v: boolean) => emit('update:show', v)"
  >
    <slot />
  </van-popup>
</template>

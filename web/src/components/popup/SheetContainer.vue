<script setup lang="ts">
/** 移动端容器：底部上拉抽屉 */
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
    position="bottom"
    round
    teleport="body"
    closeable
    :close-on-click-overlay="true"
    :style="{ height: height || '90vh' }"
    @update:show="(v: boolean) => emit('update:show', v)"
  >
    <slot />
  </van-popup>
</template>

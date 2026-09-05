<script setup lang="ts">
/**
 * ResponsivePopup 平台自适应弹层容器。
 *
 * - 桌面（PC / 宽屏）：居中弹窗（居中、固定宽高比），鼠标交互体验更好
 * - App / 移动端：底部上拉抽屉（round + 90vh），符合触屏习惯
 *
 * 两个容器均为 defineAsyncComponent 懒加载：首次打开时才拉取对应代码块，
 * 且只会加载当前平台的那一个（v-if="opened" 延迟到首次打开才渲染）。
 */
import { ref, watch, defineAsyncComponent } from 'vue'

const props = defineProps<{
  show: boolean
  /** 弹窗宽度（桌面端生效），默认 760px */
  width?: string
  /** 弹层高度，默认桌面 86vh / 移动 90vh */
  height?: string
}>()

const emit = defineEmits<{
  (e: 'update:show', value: boolean): void
}>()

/** 平台判定：移动 UA 优先；触屏 + 窄屏视为移动端 */
const isMobile = (() => {
  if (/Android|iPhone|iPad|iPod|Mobile|Mobi/i.test(navigator.userAgent)) return true
  const narrow = window.matchMedia('(max-width: 768px)').matches
  return narrow && 'ontouchstart' in window
})()

const SheetContainer = defineAsyncComponent(() => import('./popup/SheetContainer.vue'))
const DialogContainer = defineAsyncComponent(() => import('./popup/DialogContainer.vue'))

/** 首次打开才挂载容器（触发对应平台组件的按需加载），之后保持挂载以保留关闭动画与内部状态 */
const opened = ref(false)
watch(
  () => props.show,
  (v) => {
    if (v) opened.value = true
  },
)
</script>

<template>
  <component
    :is="isMobile ? SheetContainer : DialogContainer"
    v-if="opened"
    :show="show"
    :width="width"
    :height="height"
    @update:show="(v: boolean) => emit('update:show', v)"
  >
    <slot />
  </component>
</template>

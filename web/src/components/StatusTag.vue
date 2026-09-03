<script setup lang="ts">
import { computed } from 'vue'
import type { TaskStatus } from '@/types'

type Tone = 'default' | 'primary' | 'success' | 'warning' | 'danger'

const props = defineProps<{
  status?: TaskStatus
  type?: Tone
  text?: string
  dot?: boolean
}>()

const statusMap: Record<TaskStatus, { text: string; type: Tone }> = {
  pending: { text: '等待中', type: 'default' },
  downloading: { text: '下载中', type: 'primary' },
  paused: { text: '已暂停', type: 'warning' },
  completed: { text: '已完成', type: 'success' },
  failed: { text: '失败', type: 'danger' },
  canceled: { text: '已取消', type: 'default' },
}

const tone = computed<Tone>(() =>
  props.status ? statusMap[props.status].type : (props.type ?? 'default'),
)
const label = computed(() => props.text ?? (props.status ? statusMap[props.status].text : ''))
</script>

<template>
  <span class="status-tag" :class="`status-tag--${tone}`">
    <span v-if="dot" class="status-tag__dot" />
    {{ label }}
  </span>
</template>

<style scoped lang="scss">
.status-tag {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  padding: 2px 8px;
  font-size: 11.5px;
  line-height: 18px;
  white-space: nowrap;
  border-radius: 999px;

  &__dot {
    width: 5px;
    height: 5px;
    border-radius: 50%;
    background: currentColor;
  }

  &--default {
    color: $color-text-secondary;
    background: rgba(107, 114, 128, 0.1);
  }

  &--primary {
    color: $color-primary;
    background: $color-primary-light;

    .status-tag__dot {
      animation: blink 1.2s ease-in-out infinite;
    }
  }

  &--success {
    color: #16a34a;
    background: rgba(34, 197, 94, 0.12);
  }

  &--warning {
    color: #d97706;
    background: rgba(245, 158, 11, 0.14);
  }

  &--danger {
    color: #dc2626;
    background: rgba(239, 68, 68, 0.12);
  }
}

@keyframes blink {
  0%,
  100% {
    opacity: 1;
  }
  50% {
    opacity: 0.25;
  }
}
</style>

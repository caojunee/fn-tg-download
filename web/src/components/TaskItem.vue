<script setup lang="ts">
import { computed, ref } from 'vue'
import AppIcon from '@/components/AppIcon.vue'
import StatusTag from '@/components/StatusTag.vue'
import type { DownloadTask, MediaKind } from '@/types'
import { formatEta, formatSize, formatSpeed, formatTime } from '@/utils/format'

const props = defineProps<{ task: DownloadTask; compact?: boolean }>()
const emit = defineEmits<{
  (e: 'pause', id: string): void
  (e: 'resume', id: string): void
  (e: 'cancel', id: string): void
  (e: 'retry', id: string): void
  (e: 'remove', id: string): void
}>()

const expanded = ref(false)

const kindLabel: Record<MediaKind, string> = {
  single: '单条消息',
  album: '相册',
  group: '组合消息',
  channel: '整频道',
}

const kindIcon = (kind: MediaKind) => (kind === 'single' ? 'file' : kind === 'channel' ? 'folder' : 'layers')

const isActive = computed(() => ['pending', 'downloading', 'paused'].includes(props.task.status))
const doneFiles = computed(() => props.task.files.filter((f) => f.progress >= 100).length)
const eta = computed(() => {
  const t = props.task
  if (t.status !== 'downloading' || !t.speed) return NaN
  return (t.totalSize - t.downloadedSize) / t.speed
})
const progressText = computed(() => props.task.progress.toFixed(1))
</script>

<template>
  <div class="task" :class="{ 'is-compact': compact }">
    <div class="task__head">
      <div class="task__icon" :class="`task__icon--${task.kind}`">
        <AppIcon :name="kindIcon(task.kind)" :size="17" />
      </div>

      <div class="task__main">
        <div class="task__title-row">
          <span class="task__title" :title="task.name">{{ task.name }}</span>
          <StatusTag :status="task.status" dot />
          <span v-if="task.kind !== 'single'" class="task__kind">{{ kindLabel[task.kind] }}</span>
          <span v-if="task.group" class="task__group-flag">--group</span>
        </div>

        <div class="task__meta">
          <a class="task__url" :href="task.url" target="_blank" rel="noreferrer">{{ task.url }}</a>
          <span class="task__dot">·</span>
          <span class="task__path" :title="task.savePath">{{ task.savePath }}</span>
        </div>
      </div>

      <div class="task__ops">
        <button
          v-if="task.status === 'downloading'"
          class="task__btn"
          title="暂停"
          @click="emit('pause', task.id)"
        >
          <AppIcon name="pause" :size="15" />
        </button>
        <button
          v-if="task.status === 'paused' || task.status === 'pending'"
          class="task__btn"
          title="继续"
          @click="emit('resume', task.id)"
        >
          <AppIcon name="play" :size="15" />
        </button>
        <button
          v-if="task.status === 'failed'"
          class="task__btn"
          title="重试"
          @click="emit('retry', task.id)"
        >
          <AppIcon name="refresh" :size="15" />
        </button>
        <button
          v-if="isActive"
          class="task__btn task__btn--danger"
          title="取消任务"
          @click="emit('cancel', task.id)"
        >
          <AppIcon name="close" :size="15" />
        </button>
        <button v-if="!isActive" class="task__btn" title="打开目录">
          <AppIcon name="folder" :size="15" />
        </button>
        <button
          v-if="!isActive"
          class="task__btn task__btn--danger"
          title="删除记录"
          @click="emit('remove', task.id)"
        >
          <AppIcon name="trash" :size="15" />
        </button>
      </div>
    </div>

    <div class="task__progress">
      <div class="task__bar">
        <div
          class="task__bar-inner"
          :class="`is-${task.status}`"
          :style="{ width: `${Math.min(task.progress, 100)}%` }"
        />
      </div>
      <span class="task__percent">{{ progressText }}%</span>
    </div>

    <div class="task__stats">
      <span>{{ formatSize(task.downloadedSize) }} / {{ formatSize(task.totalSize) }}</span>
      <span v-if="task.status === 'downloading'" class="task__speed">
        <AppIcon name="zap" :size="12" />{{ formatSpeed(task.speed) }}
      </span>
      <span v-if="task.status === 'downloading'">剩余 {{ formatEta(eta) }}</span>
      <span>
        文件
        <b class="task__files" @click="expanded = !expanded">
          {{ doneFiles }}/{{ task.files.length }}
          <AppIcon :name="expanded ? 'chevronDown' : 'chevronRight'" :size="12" />
        </b>
      </span>
      <span v-if="task.status === 'completed' && task.finishedAt">
        {{ formatTime(task.finishedAt) }} 完成
      </span>
      <span v-if="task.status === 'failed' && task.error" class="task__error">
        <AppIcon name="alert" :size="12" />{{ task.error }}
      </span>
    </div>

    <div v-if="expanded" class="task__files-panel">
      <div v-for="f in task.files" :key="f.id" class="file">
        <AppIcon name="file" :size="13" />
        <span class="file__name" :title="f.name">{{ f.name }}</span>
        <div class="file__bar">
          <div class="file__bar-inner" :style="{ width: `${Math.min(f.progress, 100)}%` }" />
        </div>
        <span class="file__size">{{ formatSize(f.downloaded) }} / {{ formatSize(f.size) }}</span>
        <StatusTag :status="f.status" />
      </div>
    </div>
  </div>
</template>

<style scoped lang="scss">
.task {
  padding: 14px 16px;
  background: $color-bg-card;
  border: 1px solid $color-border;
  border-radius: $radius-md;
  transition:
    border-color 0.16s,
    box-shadow 0.16s;

  &:hover {
    border-color: rgba(42, 171, 238, 0.4);
  }

  &__head {
    display: flex;
    gap: 12px;
    align-items: flex-start;
  }

  &__icon {
    display: flex;
    flex: none;
    align-items: center;
    justify-content: center;
    width: 34px;
    height: 34px;
    color: $color-text-secondary;
    background: $color-bg;
    border-radius: 9px;

    &--album,
    &--group {
      color: $color-primary;
      background: $color-primary-light;
    }

    &--channel {
      color: $color-success;
      background: rgba(34, 197, 94, 0.12);
    }
  }

  &__main {
    flex: 1;
    min-width: 0;
  }

  &__title-row {
    display: flex;
    flex-wrap: wrap;
    gap: 8px;
    align-items: center;
  }

  &__title {
    overflow: hidden;
    font-size: 13.5px;
    font-weight: 600;
    white-space: nowrap;
    text-overflow: ellipsis;
  }

  &__kind,
  &__group-flag {
    padding: 1px 7px;
    font-size: 11px;
    color: $color-text-secondary;
    background: $color-bg;
    border-radius: 999px;
  }

  &__group-flag {
    font-family: Consolas, Monaco, monospace;
    color: $color-primary;
    background: $color-primary-light;
  }

  &__meta {
    display: flex;
    gap: 6px;
    align-items: center;
    margin-top: 5px;
    overflow: hidden;
    font-size: 11.5px;
    color: $color-text-placeholder;
    white-space: nowrap;
  }

  &__url {
    color: $color-primary;
    font-family: Consolas, Monaco, monospace;

    &:hover {
      text-decoration: underline;
    }
  }

  &__path {
    overflow: hidden;
    text-overflow: ellipsis;
  }

  &__dot {
    color: $color-border;
  }

  &__ops {
    display: flex;
    flex: none;
    gap: 4px;
  }

  &__btn {
    display: flex;
    align-items: center;
    justify-content: center;
    width: 28px;
    height: 28px;
    color: $color-text-secondary;
    cursor: pointer;
    background: transparent;
    border: 1px solid transparent;
    border-radius: 7px;
    transition: all 0.16s;

    &:hover {
      color: $color-primary;
      background: $color-primary-light;
    }

    &--danger:hover {
      color: $color-danger;
      background: rgba(239, 68, 68, 0.08);
    }
  }

  &__progress {
    display: flex;
    align-items: center;
    gap: 10px;
    margin-top: 12px;
  }

  &__bar {
    flex: 1;
    height: 6px;
    overflow: hidden;
    background: $color-bg;
    border-radius: 3px;
  }

  &__bar-inner {
    height: 100%;
    background: linear-gradient(90deg, #37bcf0, $color-primary);
    border-radius: 3px;
    transition: width 0.4s ease;

    &.is-paused {
      background: $color-warning;
    }
    &.is-completed {
      background: $color-success;
    }
    &.is-failed {
      background: $color-danger;
    }
  }

  &__percent {
    width: 46px;
    font-size: 12px;
    font-variant-numeric: tabular-nums;
    text-align: right;
    color: $color-text-secondary;
  }

  &__stats {
    display: flex;
    flex-wrap: wrap;
    gap: 14px;
    margin-top: 9px;
    font-size: 11.5px;
    font-variant-numeric: tabular-nums;
    color: $color-text-secondary;
  }

  &__speed {
    display: inline-flex;
    align-items: center;
    gap: 3px;
    color: $color-primary;
  }

  &__files {
    display: inline-flex;
    align-items: center;
    gap: 2px;
    color: $color-primary;
    cursor: pointer;
  }

  &__error {
    display: inline-flex;
    gap: 4px;
    align-items: center;
    color: $color-danger;
  }

  &__files-panel {
    display: flex;
    flex-direction: column;
    gap: 6px;
    padding: 10px 12px;
    margin-top: 10px;
    background: #fbfcfd;
    border: 1px solid $color-border;
    border-radius: $radius-sm;
  }
}

.file {
  display: flex;
  gap: 8px;
  align-items: center;
  font-size: 11.5px;
  color: $color-text-secondary;

  &__name {
    flex: 1;
    min-width: 0;
    overflow: hidden;
    white-space: nowrap;
    text-overflow: ellipsis;
  }

  &__bar {
    width: 90px;
    height: 4px;
    background: $color-bg;
    border-radius: 2px;
  }

  &__bar-inner {
    height: 100%;
    background: $color-primary;
    border-radius: 2px;
  }

  &__size {
    font-variant-numeric: tabular-nums;
  }
}
</style>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { showConfirmDialog, showSuccessToast } from 'vant'
import AppIcon from '@/components/AppIcon.vue'
import TaskItem from '@/components/TaskItem.vue'
import { useDownloadStore } from '@/stores/download'
import { useIsMobile } from '@/composables/useIsMobile'
import { formatSpeed } from '@/utils/format'

const download = useDownloadStore()
const tab = ref(0)
const isMobile = useIsMobile()

// 速率值拆成数值+单位渲染（单位小号），避免长值换行撑高卡片
const speedParts = computed(() => {
  const [v, ...u] = formatSpeed(download.totalSpeed).split(' ')
  return { value: v, unit: u.join(' ') }
})

type StatItem = {
  key: string
  label: string
  value: string | number
  unit?: string
  icon: string
  tone: string
}

const stats = computed<StatItem[]>(() => [
  {
    key: 'speed',
    label: '总下载速率',
    value: speedParts.value.value,
    unit: speedParts.value.unit,
    icon: 'zap',
    tone: 'primary',
  },
  {
    key: 'downloading',
    label: '下载中',
    value: download.downloadingCount,
    icon: 'download',
    tone: 'primary',
  },
  { key: 'pending', label: '等待中', value: download.pendingCount, icon: 'clock', tone: 'default' },
  { key: 'paused', label: '已暂停', value: download.pausedCount, icon: 'pause', tone: 'warning' },
  {
    key: 'completed',
    label: '已完成',
    value: download.completedCount,
    icon: 'checkCircle',
    tone: 'success',
  },
  { key: 'failed', label: '失败', value: download.failedCount, icon: 'alert', tone: 'danger' },
])

async function onPause(id: string) {
  await download.pause(id)
}
async function onResume(id: string) {
  await download.resume(id)
}
async function onRetry(id: string) {
  await download.retry(id)
}
const cancelShow = ref(false)
const cancelTarget = ref('')
function onCancel(id: string) {
  cancelTarget.value = id
  cancelShow.value = true
}
async function doCancel(deletePartial: boolean) {
  cancelShow.value = false
  await download.cancel(cancelTarget.value, deletePartial)
  showSuccessToast(deletePartial ? '任务已取消，分片已删除' : '任务已取消，分片已保留')
}
async function onRemove(id: string) {
  try {
    await showConfirmDialog({
      title: '删除记录',
      message: '仅删除任务记录，不会删除磁盘上已下载的文件。确定删除吗？',
    })
  } catch {
    return
  }
  await download.removeHistory([id])
}
async function onClearHistory() {
  try {
    await showConfirmDialog({
      title: '清空历史记录',
      message: `将删除 ${download.historyTasks.length} 条记录（不会删除已下载的文件）`,
    })
  } catch {
    return
  }
  await download.removeHistory()
  showSuccessToast('历史记录已清空')
}
async function onPauseAll() {
  for (const t of download.activeTasks) {
    if (t.status === 'downloading') await download.pause(t.id)
  }
}
async function onResumeAll() {
  for (const t of download.activeTasks) {
    if (t.status !== 'downloading') await download.resume(t.id)
  }
}
</script>

<template>
  <div class="page">
    <div class="stats">
      <div v-for="s in stats" :key="s.key" class="stats__item" :class="`is-${s.tone}`">
        <div class="stats__icon"><AppIcon :name="s.icon" :size="16" /></div>
        <div class="stats__body">
          <div class="stats__value">
            {{ s.value }}<span v-if="s.unit" class="stats__unit">{{ s.unit }}</span>
          </div>
          <div class="stats__label">{{ s.label }}</div>
        </div>
      </div>
    </div>

    <div class="app-card">
      <van-tabs v-model:active="tab" class="tabs">
        <van-tab :title="`进行中 (${download.activeTasks.length})`">
          <div class="list">
            <div v-if="download.activeTasks.length" class="list__bar">
              <span class="list__count">{{ download.activeTasks.length }} 个任务</span>
              <div class="list__bar-ops">
                <span class="list__op" @click="onPauseAll">
                  <AppIcon name="pause" :size="13" />全部暂停
                </span>
                <span class="list__op" @click="onResumeAll">
                  <AppIcon name="play" :size="13" />全部继续
                </span>
                <span class="list__op" @click="download.refresh()">
                  <AppIcon name="refresh" :size="13" />刷新
                </span>
              </div>
            </div>

            <TaskItem
              v-for="task in download.activeTasks"
              :key="task.id"
              :task="task"
              @pause="onPause"
              @resume="onResume"
              @cancel="onCancel"
              @retry="onRetry"
              @remove="onRemove"
            />

            <van-empty
              v-if="!download.activeTasks.length"
              description="暂无进行中的任务，去「新建下载」添加一个吧"
            />
          </div>
        </van-tab>

        <van-tab :title="`历史记录 (${download.historyTasks.length})`">
          <div class="list">
            <div v-if="download.historyTasks.length" class="list__bar">
              <span class="list__count">{{ download.historyTasks.length }} 条记录</span>
              <div class="list__bar-ops">
                <span class="list__op is-danger" @click="onClearHistory">
                  <AppIcon name="trash" :size="13" />清空历史
                </span>
              </div>
            </div>

            <TaskItem
              v-for="task in download.historyTasks"
              :key="task.id"
              compact
              :task="task"
              @pause="onPause"
              @resume="onResume"
              @cancel="onCancel"
              @retry="onRetry"
              @remove="onRemove"
            />

            <van-empty v-if="!download.historyTasks.length" description="还没有下载记录" />
          </div>
        </van-tab>
      </van-tabs>
    </div>

    <van-popup
      v-model:show="cancelShow"
      :position="isMobile ? 'bottom' : 'center'"
      round
      :style="isMobile ? { maxHeight: '70%' } : { width: '380px' }"
    >
      <div class="cancel-pop" :class="{ 'is-desktop': !isMobile }">
        <div class="cancel-pop__desc">
          选择取消方式：保留分片下次可续传，删除分片则从零重新下载
        </div>
        <button class="cancel-pop__opt" @click="doCancel(false)">
          保留分片并取消<span class="cancel-pop__hint">可断点续传</span>
        </button>
        <button class="cancel-pop__opt is-danger" @click="doCancel(true)">
          删除分片并取消<span class="cancel-pop__hint">从零重新下载</span>
        </button>
        <button class="cancel-pop__opt is-plain" @click="cancelShow = false">返回</button>
      </div>
    </van-popup>
  </div>
</template>

<style scoped lang="scss">
.cancel-pop {
  padding: 16px 16px calc(16px + env(safe-area-inset-bottom));

  &__desc {
    padding: 4px 8px 14px;
    font-size: 13px;
    color: var(--van-text-color-2);
    line-height: 1.5;
  }

  &__opt {
    display: flex;
    align-items: center;
    justify-content: space-between;
    width: 100%;
    padding: 13px 14px;
    border: none;
    border-radius: 10px;
    background: var(--van-active-color, #f2f3f5);
    color: var(--van-text-color);
    font-size: 15px;
    cursor: pointer;
    text-align: left;

    & + & {
      margin-top: 10px;
    }

    &.is-danger {
      color: var(--van-danger-color, #ee0a24);
    }

    &.is-plain {
      background: transparent;
      color: var(--van-text-color-2);
      justify-content: center;
    }
  }

  &__hint {
    font-size: 12px;
    color: var(--van-text-color-3);
  }

  &.is-desktop {
    padding-bottom: 16px;

    .cancel-pop__opt:hover {
      filter: brightness(0.97);
    }
  }
}

.stats {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(150px, 1fr));
  grid-auto-rows: 1fr; /* 同行卡片强制等高 */
  gap: 12px;

  &__item {
    display: flex;
    gap: 11px;
    align-items: center;
    min-width: 0;
    padding: 14px 16px;
    background: $color-bg-card;
    border: 1px solid $color-border;
    border-radius: $radius-md;

    &.is-primary .stats__icon {
      color: $color-primary;
      background: $color-primary-light;
    }
    &.is-success .stats__icon {
      color: $color-success;
      background: rgba(34, 197, 94, 0.12);
    }
    &.is-warning .stats__icon {
      color: $color-warning;
      background: rgba(245, 158, 11, 0.14);
    }
    &.is-danger .stats__icon {
      color: $color-danger;
      background: rgba(239, 68, 68, 0.12);
    }
  }

  &__icon {
    display: flex;
    flex: none;
    align-items: center;
    justify-content: center;
    width: 32px;
    height: 32px;
    color: $color-text-secondary;
    background: $color-bg;
    border-radius: 9px;
  }

  &__body {
    flex: 1;
    min-width: 0;
  }

  &__value {
    font-size: 18px;
    font-weight: 600;
    font-variant-numeric: tabular-nums;
    line-height: 1.2;
    white-space: nowrap;
  }

  &__unit {
    margin-left: 2px;
    font-size: 12px;
    font-weight: 500;
    color: $color-text-secondary;
  }

  &__label {
    margin-top: 2px;
    font-size: 11.5px;
    color: $color-text-placeholder;
    white-space: nowrap;
  }
}

.list {
  display: flex;
  flex-direction: column;
  gap: 10px;
  padding: 14px 16px 18px;

  &__bar {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding-bottom: 4px;
  }

  &__count {
    font-size: 12px;
    color: $color-text-secondary;
  }

  &__bar-ops {
    display: flex;
    gap: 6px;
  }

  &__op {
    display: inline-flex;
    gap: 4px;
    align-items: center;
    padding: 5px 9px;
    font-size: 12px;
    color: $color-text-secondary;
    cursor: pointer;
    border-radius: 6px;

    &:hover {
      color: $color-primary;
      background: $color-primary-light;
    }

    &.is-danger:hover {
      color: $color-danger;
      background: rgba(239, 68, 68, 0.08);
    }
  }
}
</style>

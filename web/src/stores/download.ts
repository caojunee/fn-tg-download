import { computed, ref } from 'vue'
import { defineStore } from 'pinia'
import {
  cancelTask,
  clearHistory,
  createDownload,
  listTasks,
  pauseTask,
  resumeTask,
  retryTask,
} from '@/api/download'
import type { CreateDownloadPayload, DownloadTask } from '@/types'

const ACTIVE: DownloadTask['status'][] = ['pending', 'downloading', 'paused']
const HISTORY: DownloadTask['status'][] = ['completed', 'failed', 'canceled']

export const useDownloadStore = defineStore('download', () => {
  const tasks = ref<DownloadTask[]>([])
  const loading = ref(false)
  const creating = ref(false)
  let timer: number | undefined
  let polling = false

  const activeTasks = computed(() =>
    tasks.value.filter((t) => ACTIVE.includes(t.status)).sort((a, b) => b.createdAt - a.createdAt),
  )
  const historyTasks = computed(() =>
    tasks.value
      .filter((t) => HISTORY.includes(t.status))
      .sort((a, b) => (b.finishedAt ?? b.createdAt) - (a.finishedAt ?? a.createdAt)),
  )

  const downloadingCount = computed(
    () => tasks.value.filter((t) => t.status === 'downloading').length,
  )
  const pendingCount = computed(() => tasks.value.filter((t) => t.status === 'pending').length)
  const pausedCount = computed(() => tasks.value.filter((t) => t.status === 'paused').length)
  const completedCount = computed(() => tasks.value.filter((t) => t.status === 'completed').length)
  const failedCount = computed(() => tasks.value.filter((t) => t.status === 'failed').length)

  /** 总下载速率（字节/秒） */
  const totalSpeed = computed(() =>
    tasks.value.reduce((sum, t) => sum + (t.status === 'downloading' ? t.speed : 0), 0),
  )

  async function refresh() {
    tasks.value = await listTasks()
  }

  function startPolling(interval = 1000) {
    if (polling) return
    polling = true
    refresh()
    timer = window.setInterval(async () => {
      try {
        await refresh()
      } catch {
        /* 轮询失败静默，等下个周期 */
      }
    }, interval)
  }

  function stopPolling() {
    polling = false
    clearInterval(timer)
    timer = undefined
  }

  async function create(payload: CreateDownloadPayload) {
    creating.value = true
    try {
      const created = await createDownload(payload)
      await refresh()
      return created
    } finally {
      creating.value = false
    }
  }

  async function pause(id: string) {
    await pauseTask(id)
    await refresh()
  }
  async function resume(id: string) {
    await resumeTask(id)
    await refresh()
  }
  async function cancel(id: string, deletePartial = false) {
    await cancelTask(id, deletePartial)
    await refresh()
  }
  async function retry(id: string) {
    await retryTask(id)
    await refresh()
  }
  async function removeHistory(ids?: string[]) {
    await clearHistory(ids)
    await refresh()
  }

  return {
    tasks,
    loading,
    creating,
    activeTasks,
    historyTasks,
    downloadingCount,
    pendingCount,
    pausedCount,
    completedCount,
    failedCount,
    totalSpeed,
    refresh,
    startPolling,
    stopPolling,
    create,
    pause,
    resume,
    cancel,
    retry,
    removeHistory,
  }
})

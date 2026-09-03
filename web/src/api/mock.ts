/**
 * 前端 mock 数据：仅在 VITE_USE_MOCK=true 时生效，用于无后端情况下评审 UI。
 * 接入 Go 后端后把 .env 的 VITE_USE_MOCK 改成 false 即可，业务代码无需改动。
 */
import type {
  DownloadFile,
  DownloadTask,
  MediaKind,
  SystemSettings,
  TaskStatus,
  UserInfo,
} from '@/types'
import { uid } from '@/utils/format'

/* ---------------- 账号 ---------------- */

export const mockUser: UserInfo = {
  id: 66881234,
  phone: '+8613800138000',
  firstName: '牛逼',
  lastName: '哥',
  username: 'niubige',
  initial: '牛',
}

/* ---------------- 系统设置 ---------------- */

export const mockSettings: SystemSettings = {
  downloadPath: '/vol1/1000/Telegram',
  proxy: 'http://192.168.31.180:20172',
  concurrency: 4,
  autoGroup: true,
  maxSizeMB: 0,
  speedLimitKB: 0,
  tdlPath: '/usr/local/bin/tdl',
  tdlDataDir: '~/.tdl/data',
  version: 'dev',
}

/* ---------------- 下载任务 ---------------- */

interface TaskSeed {
  name: string
  url: string
  chatTitle: string
  messageId: number
  kind: MediaKind
  group: boolean
  fileCount: number
  totalSize: number
  downloadedSize: number
  speed: number
  status: TaskStatus
  savePath: string
  ageMinutes: number
}

const seeds: TaskSeed[] = [
  {
    name: '【纪录片】蓝色星球 II · 全 8 集合集',
    url: 'https://t.me/siwass/6729',
    chatTitle: '纪录片资源库',
    messageId: 6729,
    kind: 'album',
    group: true,
    fileCount: 8,
    totalSize: 8.6 * 1024 ** 3,
    downloadedSize: 3.1 * 1024 ** 3,
    speed: 4.2 * 1024 ** 2,
    status: 'downloading',
    savePath: '/vol1/1000/Telegram/纪录片资源库',
    ageMinutes: 26,
  },
  {
    name: '摄影素材包 2026-08 · 原图直出',
    url: 'https://t.me/siwass/6702',
    chatTitle: '摄影素材库',
    messageId: 6702,
    kind: 'group',
    group: true,
    fileCount: 24,
    totalSize: 1.9 * 1024 ** 3,
    downloadedSize: 620 * 1024 ** 2,
    speed: 1.1 * 1024 ** 2,
    status: 'downloading',
    savePath: '/vol1/1000/Telegram/摄影素材库',
    ageMinutes: 8,
  },
  {
    name: '无损音乐合辑 FLAC · 第三辑',
    url: 'https://t.me/music_hub/1188',
    chatTitle: 'Music Hub',
    messageId: 1188,
    kind: 'album',
    group: true,
    fileCount: 32,
    totalSize: 2.4 * 1024 ** 3,
    downloadedSize: 0,
    speed: 0,
    status: 'pending',
    savePath: '/vol1/1000/Telegram/Music Hub',
    ageMinutes: 1,
  },
  {
    name: '公开课视频 · 计算机体系结构',
    url: 'https://t.me/cs_open/334',
    chatTitle: 'CS Open Course',
    messageId: 334,
    kind: 'single',
    group: false,
    fileCount: 1,
    totalSize: 1.2 * 1024 ** 3,
    downloadedSize: 430 * 1024 ** 2,
    speed: 0,
    status: 'paused',
    savePath: '/vol1/1000/Telegram/CS Open Course',
    ageMinutes: 95,
  },
  {
    name: '设计参考图包 · Vol.12',
    url: 'https://t.me/design_ref/901',
    chatTitle: 'Design Reference',
    messageId: 901,
    kind: 'album',
    group: true,
    fileCount: 16,
    totalSize: 480 * 1024 ** 2,
    downloadedSize: 480 * 1024 ** 2,
    speed: 0,
    status: 'completed',
    savePath: '/vol1/1000/Telegram/Design Reference',
    ageMinutes: 320,
  },
  {
    name: '软件安装包合集',
    url: 'https://t.me/soft_zone/77',
    chatTitle: 'Soft Zone',
    messageId: 77,
    kind: 'group',
    group: true,
    fileCount: 6,
    totalSize: 3.4 * 1024 ** 3,
    downloadedSize: 1.02 * 1024 ** 3,
    speed: 0,
    status: 'failed',
    savePath: '/vol1/1000/Telegram/Soft Zone',
    ageMinutes: 140,
  },
  {
    name: '电子书 · Go 语言设计与实现',
    url: 'https://t.me/golang_books/516',
    chatTitle: 'Go Books',
    messageId: 516,
    kind: 'single',
    group: false,
    fileCount: 1,
    totalSize: 24 * 1024 ** 2,
    downloadedSize: 24 * 1024 ** 2,
    speed: 0,
    status: 'completed',
    savePath: '/vol1/1000/Telegram/Go Books',
    ageMinutes: 1500,
  },
]

const extPool = ['mp4', 'mkv', 'jpg', 'png', 'flac', 'pdf', 'zip', 'mov']

function buildFiles(seed: TaskSeed): DownloadFile[] {
  const perSize = seed.totalSize / seed.fileCount
  const doneCount = Math.floor((seed.downloadedSize / seed.totalSize) * seed.fileCount)
  const ext = extPool[Math.abs(seed.messageId) % extPool.length]

  return Array.from({ length: seed.fileCount }, (_, i) => {
    const isDone = i < doneCount
    const isActive = !isDone && i === doneCount && seed.status === 'downloading'
    const extName = seed.fileCount === 1 ? ext : extPool[i % extPool.length]
    return {
      id: uid('f'),
      name: `${seed.name.replace(/[·\s【】]/g, '_').slice(0, 24)}_${String(i + 1).padStart(2, '0')}.${extName}`,
      size: perSize,
      downloaded: isDone ? perSize : isActive ? perSize * 0.42 : 0,
      progress: isDone ? 100 : isActive ? 42 : 0,
      status: isDone ? 'completed' : isActive ? 'downloading' : ('pending' as TaskStatus),
    }
  })
}

function buildTask(seed: TaskSeed): DownloadTask {
  return {
    id: uid('t'),
    name: seed.name,
    url: seed.url,
    chatTitle: seed.chatTitle,
    messageId: seed.messageId,
    kind: seed.kind,
    group: seed.group,
    totalSize: seed.totalSize,
    downloadedSize: seed.downloadedSize,
    speed: seed.speed,
    progress: seed.totalSize ? (seed.downloadedSize / seed.totalSize) * 100 : 0,
    status: seed.status,
    savePath: seed.savePath,
    createdAt: Date.now() - seed.ageMinutes * 60_000,
    finishedAt: ['completed', 'failed'].includes(seed.status)
      ? Date.now() - seed.ageMinutes * 60_000 + 5 * 60_000
      : undefined,
    error:
      seed.status === 'failed' ? '部分分片校验失败：network is unreachable (proxy)' : undefined,
    files: buildFiles(seed),
  }
}

/** mock 任务库 */
export const mockTasks: DownloadTask[] = seeds.map(buildTask)

/** 每秒推进一次进度，模拟真实下载 */
function tick() {
  for (const task of mockTasks) {
    if (task.status !== 'downloading') continue
    // 速率小幅抖动
    task.speed = Math.max(128 * 1024, task.speed * (0.85 + Math.random() * 0.35))
    task.downloadedSize = Math.min(task.totalSize, task.downloadedSize + task.speed)
    task.progress = (task.downloadedSize / task.totalSize) * 100

    const active = task.files.find((f) => f.status === 'downloading')
    if (active) {
      active.downloaded = Math.min(active.size, active.downloaded + task.speed)
      active.progress = (active.downloaded / active.size) * 100
      if (active.downloaded >= active.size) {
        active.status = 'completed'
        active.progress = 100
      }
    }

    if (task.downloadedSize >= task.totalSize) {
      task.status = 'completed'
      task.speed = 0
      task.progress = 100
      task.finishedAt = Date.now()
    }
  }

  // 队列里有位置就把等待中的任务拉起
  const running = mockTasks.filter((t) => t.status === 'downloading').length
  if (running < mockSettings.concurrency) {
    const next = mockTasks.find((t) => t.status === 'pending')
    if (next) {
      next.status = 'downloading'
      next.speed = 800 * 1024
      const f = next.files.find((x) => x.status === 'pending')
      if (f) f.status = 'downloading'
    }
  }
}

let timer: number | undefined
export function startMockEngine() {
  if (timer) return
  timer = window.setInterval(tick, 1000)
}

/* ---------------- 行为模拟 ---------------- */

const delay = (ms = 300) => new Promise((r) => setTimeout(r, ms))

export const mockApi = {
  async sendCode(_phone: string) {
    await delay(600)
    return { sent: true, timeout: 60 }
  },

  async login(payload: { phone: string; code: string; password?: string }) {
    await delay(700)
    if (payload.code.length < 5) throw new Error('验证码不正确')
    return { ...mockUser, phone: payload.phone }
  },

  async logout() {
    await delay(200)
    return true
  },

  async createDownload(payload: { urls: string[]; group: boolean; savePath?: string }) {
    await delay(500)
    const created: DownloadTask[] = payload.urls.map((url, i) => {
      const m = url.match(/t\.me\/([^/]+)\/(\d+)/)
      const chat = m?.[1] ?? 'unknown'
      const msgId = Number(m?.[2] ?? Math.floor(Math.random() * 9000))
      const size = (0.4 + Math.random() * 3) * 1024 ** 3
      const files: DownloadFile[] = Array.from(
        { length: 1 + Math.floor(Math.random() * 5) },
        (_, k) => ({
          id: uid('f'),
          name: `${chat}_${msgId}_${k + 1}.mp4`,
          size: size / (k + 2),
          downloaded: 0,
          progress: 0,
          status: 'pending' as TaskStatus,
        }),
      )
      return {
        id: uid('t'),
        name: `${chat} #${msgId}`,
        url,
        chatTitle: chat,
        messageId: msgId,
        kind: (payload.group ? 'album' : 'single') as MediaKind,
        group: payload.group,
        totalSize: files.reduce((s, f) => s + f.size, 0),
        downloadedSize: 0,
        speed: 0,
        progress: 0,
        status: i === 0 ? 'downloading' : 'pending',
        savePath: payload.savePath || mockSettings.downloadPath,
        createdAt: Date.now() + i,
        files,
      } satisfies DownloadTask
    })
    mockTasks.unshift(...created)
    return created
  },

  async probe(urls: string[]) {
    await delay(700)
    return urls.map((url) => {
      const m = url.match(/t\.me\/([^/]+)\/(\d+)/)
      const chat = m?.[1] ?? 'unknown'
      const msgId = Number(m?.[2] ?? 0)
      const count = 1 + (msgId % 7)
      return {
        url,
        chatTitle: chat,
        messageId: msgId,
        kind: (count > 1 ? 'album' : 'single') as MediaKind,
        fileCount: count,
        totalSize: count * (80 + (msgId % 400)) * 1024 ** 2,
        title: `${chat} #${msgId}${count > 1 ? ` · 相册（${count} 项）` : ''}`,
      }
    })
  },

  async listTasks() {
    startMockEngine()
    await delay(120)
    return mockTasks.map((t) => ({ ...t }))
  },

  async pause(id: string) {
    const t = mockTasks.find((x) => x.id === id)
    if (t) {
      t.status = 'paused'
      t.speed = 0
    }
    return true
  },

  async resume(id: string) {
    const t = mockTasks.find((x) => x.id === id)
    if (t) {
      t.status = 'downloading'
      t.speed = 900 * 1024
    }
    return true
  },

  async cancel(id: string) {
    const i = mockTasks.findIndex((x) => x.id === id)
    if (i >= 0) mockTasks.splice(i, 1)
    return true
  },

  async retry(id: string) {
    const t = mockTasks.find((x) => x.id === id)
    if (t) {
      t.status = 'downloading'
      t.error = undefined
      t.speed = 700 * 1024
    }
    return true
  },

  async clearHistory(ids?: string[]) {
    if (!ids?.length) {
      for (let i = mockTasks.length - 1; i >= 0; i--) {
        const s = mockTasks[i].status
        if (s === 'completed' || s === 'failed' || s === 'canceled') mockTasks.splice(i, 1)
      }
    } else {
      for (let i = mockTasks.length - 1; i >= 0; i--) {
        if (ids.includes(mockTasks[i].id)) mockTasks.splice(i, 1)
      }
    }
    return true
  },

  async getSettings() {
    await delay(200)
    return { ...mockSettings }
  },

  async saveSettings(next: SystemSettings) {
    await delay(400)
    Object.assign(mockSettings, next)
    return { ...mockSettings }
  },

  async testProxy(proxy: string) {
    await delay(900)
    if (!proxy) throw new Error('代理地址为空')
    return { ok: true, latency: 128 }
  },
}

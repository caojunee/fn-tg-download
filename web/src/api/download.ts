import type { ApiResult, CreateDownloadPayload, DownloadTask, MediaKind } from '@/types'
import { showToast } from 'vant'
import { USE_MOCK, httpGet, httpPost } from './request'
import service from './request'

/** 解析结果：探测链接是单条消息还是相册 / 组合消息 */
export interface ProbeResult {
  url: string
  chatTitle: string
  messageId: number
  kind: MediaKind
  fileCount: number
  totalSize: number
  title: string
  error?: string
}

/** mock 按需加载，生产构建不会下载该 chunk */
const mock = () => import('./mock')

export async function probe(urls: string[]): Promise<ProbeResult[]> {
  if (USE_MOCK) return (await mock()).mockApi.probe(urls)
  return httpPost<ProbeResult[]>('/download/probe', { urls })
}

/** 链接台账项：该 URL 最近一次任务的快照（持久化，清历史不丢） */
export interface UrlLedgerEntry {
  url: string
  status: string
  name: string
  finishedAt: number
}

/** 批量查询链接是否已建过任务（后端持久化，跨会话可查） */
export async function lookupUrls(urls: string[]): Promise<UrlLedgerEntry[]> {
  if (USE_MOCK) return []
  try {
    return await httpPost<UrlLedgerEntry[]>('/download/urls/lookup', { urls })
  } catch {
    return [] // 台账查询失败不阻塞解析流程
  }
}

export async function createDownload(payload: CreateDownloadPayload): Promise<DownloadTask[]> {
  if (USE_MOCK)
    return (await mock()).mockApi.createDownload({
      urls: payload.urls,
      group: payload.group,
      savePath: payload.savePath,
    })
  // 部分链接探测失败被跳过时，后端在 X-Skipped-Reasons 响应头里带原因
  const res = await service.post<ApiResult<DownloadTask[]>>('/download/create', payload)
  const skipped = res.headers['x-skipped-reasons'] as string | undefined
  if (skipped) showToast(skipped.replace(/\n/g, '；'))
  return res.data.data
}

export async function listTasks(): Promise<DownloadTask[]> {
  if (USE_MOCK) return (await mock()).mockApi.listTasks()
  return httpGet<DownloadTask[]>('/download/tasks')
}

export async function pauseTask(id: string) {
  if (USE_MOCK) return (await mock()).mockApi.pause(id)
  return httpPost<boolean>(`/download/tasks/${id}/pause`)
}

export async function resumeTask(id: string) {
  if (USE_MOCK) return (await mock()).mockApi.resume(id)
  return httpPost<boolean>(`/download/tasks/${id}/resume`)
}

export async function cancelTask(id: string, deletePartial = false) {
  if (USE_MOCK) return (await mock()).mockApi.cancel(id)
  return httpPost<boolean>(`/download/tasks/${id}/cancel`, { deletePartial })
}

export async function retryTask(id: string) {
  if (USE_MOCK) return (await mock()).mockApi.retry(id)
  return httpPost<boolean>(`/download/tasks/${id}/retry`)
}

export async function clearHistory(ids?: string[]) {
  if (USE_MOCK) return (await mock()).mockApi.clearHistory(ids)
  return httpPost<boolean>('/download/history/clear', { ids })
}

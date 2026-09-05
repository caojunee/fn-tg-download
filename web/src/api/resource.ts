import type { ResourceSet } from '@/types'
import { httpPost } from './request'

/** 列出一条链接里可选的全部媒体资源（整频道会拉取全部消息，较慢） */
export async function listResources(url: string): Promise<ResourceSet> {
  return httpPost<ResourceSet>('/resource/list', { url })
}

/** 缩略图地址（后端按需下载并缓存，未就绪时返回 404，前端显示为占位） */
export function thumbUrl(key: string, url: string): string {
  return `/api/resource/thumb?key=${encodeURIComponent(key)}&url=${encodeURIComponent(url)}`
}

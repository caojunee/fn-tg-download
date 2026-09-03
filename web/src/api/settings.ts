import type { SystemSettings } from '@/types'
import { USE_MOCK, httpGet, httpPost } from './request'

/** mock 按需加载，生产构建不会下载该 chunk */
const mock = () => import('./mock')

export async function getSettings(): Promise<SystemSettings> {
  if (USE_MOCK) return (await mock()).mockApi.getSettings()
  return httpGet<SystemSettings>('/settings')
}

export async function saveSettings(payload: SystemSettings): Promise<SystemSettings> {
  if (USE_MOCK) return (await mock()).mockApi.saveSettings(payload)
  return httpPost<SystemSettings>('/settings', payload)
}

export async function testProxy(proxy: string) {
  if (USE_MOCK) return (await mock()).mockApi.testProxy(proxy)
  return httpPost<{ ok: boolean; latency: number }>('/settings/proxy/test', { proxy })
}

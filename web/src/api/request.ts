import axios, { type AxiosInstance } from 'axios'
import { showToast } from 'vant'
import type { ApiResult } from '@/types'

/** 无后端时预览 UI 用的开关 */
export const USE_MOCK = import.meta.env.VITE_USE_MOCK === 'true'

const service: AxiosInstance = axios.create({
  baseURL: import.meta.env.VITE_API_BASE || '/api',
  timeout: 60_000,
  headers: { 'Content-Type': 'application/json' },
})

service.interceptors.response.use(
  (response) => {
    const body = response.data as ApiResult<unknown>
    if (body && typeof body === 'object' && 'code' in body && body.code !== 0) {
      // silent 请求（如启动时的登录态探测）失败不打扰用户
      if (!(response.config as unknown as { silent?: boolean }).silent) {
        showToast(body.message || '请求失败')
      }
      return Promise.reject(new Error(body.message || '请求失败'))
    }
    return response
  },
  (error) => {
    const cfg = error.config as (typeof error.config & { silent?: boolean }) | undefined
    if (!cfg?.silent) {
      const msg =
        (error.response?.data as ApiResult<unknown> | undefined)?.message ||
        error.message ||
        '网络异常'
      showToast(msg)
    }
    return Promise.reject(error)
  },
)

export async function httpGet<T>(
  url: string,
  params?: Record<string, unknown>,
  opts?: { silent?: boolean },
): Promise<T> {
  const res = await service.get<ApiResult<T>>(url, { params, ...opts })
  return res.data.data
}

export async function httpPost<T>(url: string, data?: unknown): Promise<T> {
  const res = await service.post<ApiResult<T>>(url, data)
  return res.data.data
}

export default service

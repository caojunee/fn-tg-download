import type { UserInfo } from '@/types'
import { USE_MOCK, httpGet, httpPost } from './request'

export const countryCodes = [
  { label: '中国 +86', value: '+86', code: 'CN' },
  { label: '中国香港 +852', value: '+852', code: 'HK' },
  { label: '中国澳门 +853', value: '+853', code: 'MO' },
  { label: '中国台湾 +886', value: '+886', code: 'TW' },
  { label: '新加坡 +65', value: '+65', code: 'SG' },
  { label: '马来西亚 +60', value: '+60', code: 'MY' },
  { label: '日本 +81', value: '+81', code: 'JP' },
  { label: '韩国 +82', value: '+82', code: 'KR' },
  { label: '美国 +1', value: '+1', code: 'US' },
  { label: '英国 +44', value: '+44', code: 'GB' },
]

/** mock 按需加载，生产构建不会下载该 chunk */
const mock = () => import('./mock')

export async function sendCode(phone: string) {
  if (USE_MOCK) return (await mock()).mockApi.sendCode(phone)
  return httpPost<{ sent: boolean; timeout: number }>('/auth/send-code', { phone })
}

export async function login(payload: { phone: string; code: string; password?: string }) {
  if (USE_MOCK) return (await mock()).mockApi.login(payload)
  return httpPost<UserInfo>('/auth/login', payload)
}

export async function logout() {
  if (USE_MOCK) return (await mock()).mockApi.logout()
  return httpPost<boolean>('/auth/logout')
}

export async function currentUser(): Promise<UserInfo> {
  if (USE_MOCK) return (await mock()).mockUser
  // silent：启动探测登录态失败时不弹错误提示
  return httpGet<UserInfo>('/auth/me', undefined, { silent: true })
}

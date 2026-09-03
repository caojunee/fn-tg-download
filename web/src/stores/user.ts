import { computed, ref } from 'vue'
import { defineStore } from 'pinia'
import { currentUser, login, logout, sendCode } from '@/api/auth'
import type { LoginStep, UserInfo } from '@/types'

export const useUserStore = defineStore('user', () => {
  const user = ref<UserInfo | null>(null)
  const loading = ref(false)
  /** 登录流程当前阶段 */
  const step = ref<LoginStep>('phone')
  const needPassword = ref(false)
  const codeCountdown = ref(0)

  const logged = computed(() => !!user.value)
  const displayName = computed(() => {
    if (!user.value) return '未登录'
    return [user.value.firstName, user.value.lastName].filter(Boolean).join(' ') || user.value.phone
  })

  let timer: number | undefined

  async function init() {
    try {
      user.value = await currentUser()
    } catch {
      user.value = null
    }
  }

  async function requestCode(phone: string) {
    loading.value = true
    try {
      const res = await sendCode(phone)
      step.value = 'code'
      codeCountdown.value = res.timeout ?? 60
      clearInterval(timer)
      timer = window.setInterval(() => {
        codeCountdown.value -= 1
        if (codeCountdown.value <= 0) clearInterval(timer)
      }, 1000)
      return true
    } finally {
      loading.value = false
    }
  }

  async function submitLogin(payload: { phone: string; code: string; password?: string }) {
    loading.value = true
    try {
      user.value = await login(payload)
      step.value = 'phone'
      clearInterval(timer)
      codeCountdown.value = 0
    } finally {
      loading.value = false
    }
  }

  async function doLogout() {
    loading.value = true
    try {
      await logout()
    } finally {
      user.value = null
      step.value = 'phone'
      loading.value = false
    }
  }

  function reset() {
    step.value = 'phone'
    needPassword.value = false
    clearInterval(timer)
    codeCountdown.value = 0
  }

  return {
    user,
    loading,
    step,
    needPassword,
    codeCountdown,
    logged,
    displayName,
    init,
    requestCode,
    submitLogin,
    doLogout,
    reset,
  }
})

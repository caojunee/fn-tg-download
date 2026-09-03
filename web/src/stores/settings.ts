import { ref } from 'vue'
import { defineStore } from 'pinia'
import { getSettings, saveSettings, testProxy } from '@/api/settings'
import type { SystemSettings } from '@/types'

const defaults: SystemSettings = {
  downloadPath: '/vol1/1000/Telegram',
  proxy: '',
  concurrency: 4,
  autoGroup: true,
  maxSizeMB: 0,
  speedLimitKB: 0,
  tdlPath: '/usr/local/bin/tdl',
}

export const useSettingsStore = defineStore('settings', () => {
  const settings = ref<SystemSettings>({ ...defaults })
  const loading = ref(false)
  const saving = ref(false)

  async function load() {
    loading.value = true
    try {
      settings.value = { ...defaults, ...(await getSettings()) }
    } finally {
      loading.value = false
    }
  }

  async function persist() {
    saving.value = true
    try {
      settings.value = await saveSettings(settings.value)
      return true
    } finally {
      saving.value = false
    }
  }

  async function checkProxy() {
    return testProxy(settings.value.proxy)
  }

  return { settings, loading, saving, load, persist, checkProxy }
})

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { showConfirmDialog, showSuccessToast, type ActionSheetAction } from 'vant'
import SideMenu from './SideMenu.vue'
import AppIcon from '@/components/AppIcon.vue'
import { useDownloadStore } from '@/stores/download'
import { useSettingsStore } from '@/stores/settings'
import { useUserStore } from '@/stores/user'
import { useIsMobile } from '@/composables/useIsMobile'
import { formatSpeed } from '@/utils/format'

const route = useRoute()
const router = useRouter()
const download = useDownloadStore()
const settings = useSettingsStore()
const user = useUserStore()
const isMobile = useIsMobile()

const title = computed(() => (route.meta.title as string) || 'TG Download')
const hasActive = computed(() => download.downloadingCount > 0)

onMounted(() => {
  download.startPolling(1000)
  settings.load()
})

onUnmounted(() => download.stopPolling())

/** 移动端底部 tabbar 的「用户」项：未登录去登录页，已登录弹操作面板 */
const userSheet = ref(false)
const userActions = computed<ActionSheetAction[]>(() => [
  { name: `账号：${user.displayName}`, disabled: true },
  { name: '退出登录', color: '#ef4444' },
])

function onMobileUser() {
  if (!user.logged) {
    router.push({ name: 'login' })
    return
  }
  userSheet.value = true
}

function onUserAction(action: ActionSheetAction) {
  if (action.name !== '退出登录') return
  showConfirmDialog({
    title: '退出登录',
    message: '退出后需要重新验证手机号才能继续下载，确定退出吗？',
    confirmButtonText: '退出登录',
    cancelButtonText: '再想想',
    confirmButtonColor: '#ef4444',
  })
    .then(async () => {
      await user.doLogout()
      showSuccessToast('已退出登录')
      router.push({ name: 'login' })
    })
    .catch(() => {})
}
</script>

<template>
  <div class="layout" :class="{ 'is-mobile': isMobile }">
    <SideMenu v-if="!isMobile" />

    <section class="layout__main">
      <header class="layout__header">
        <div class="layout__heading">
          <h1 class="layout__title">{{ title }}</h1>
          <span class="layout__subtitle">基于 tdl 的 Telegram 下载器</span>
        </div>

        <div class="layout__status">
          <div v-if="hasActive" class="layout__speed">
            <AppIcon name="zap" :size="15" />
            <span>{{ formatSpeed(download.totalSpeed) }}</span>
          </div>
          <div v-if="download.downloadingCount" class="layout__badge">
            <span class="layout__dot" />
            下载中 {{ download.downloadingCount }}
          </div>
        </div>
      </header>

      <main class="layout__content">
        <router-view v-slot="{ Component }">
          <component :is="Component" />
        </router-view>
      </main>
    </section>

    <!-- 移动端底部导航 -->
    <van-tabbar v-if="isMobile" route safe-area-inset-bottom>
      <van-tabbar-item :to="{ name: 'new' }" icon="plus">新建</van-tabbar-item>
      <van-tabbar-item
        :to="{ name: 'tasks' }"
        icon="orders-o"
        :badge="download.activeTasks.length || undefined"
      >
        任务
      </van-tabbar-item>
      <van-tabbar-item :to="{ name: 'settings' }" icon="setting-o">设置</van-tabbar-item>
      <van-tabbar-item icon="contact" @click="onMobileUser">
        {{ user.logged ? '我的' : '登录' }}
      </van-tabbar-item>
    </van-tabbar>

    <van-action-sheet
      v-model:show="userSheet"
      :actions="userActions"
      cancel-text="取消"
      close-on-click-action
      @select="onUserAction"
    />
  </div>
</template>

<style scoped lang="scss">
.layout {
  display: flex;
  height: 100%;
  background: $color-bg;

  &__main {
    display: flex;
    flex: 1;
    flex-direction: column;
    min-width: 0;
  }

  &__header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 16px;
    height: $header-height;
    padding: 0 24px;
    background: $color-bg-card;
    border-bottom: 1px solid $color-border;
  }

  &__heading {
    display: flex;
    align-items: baseline;
    gap: 10px;
    min-width: 0;
  }

  &__title {
    margin: 0;
    font-size: 16px;
    font-weight: 600;
    color: $color-text;
  }

  &__subtitle {
    font-size: 12px;
    color: $color-text-placeholder;
  }

  &__status {
    display: flex;
    align-items: center;
    gap: 10px;
  }

  &__speed {
    display: flex;
    align-items: center;
    gap: 6px;
    padding: 5px 10px;
    font-size: 12px;
    font-variant-numeric: tabular-nums;
    color: $color-primary;
    background: $color-primary-light;
    border-radius: 999px;
  }

  &__badge {
    display: flex;
    align-items: center;
    gap: 6px;
    padding: 5px 10px;
    font-size: 12px;
    color: $color-text-secondary;
    background: $color-bg;
    border: 1px solid $color-border;
    border-radius: 999px;
  }

  &__dot {
    width: 6px;
    height: 6px;
    background: $color-success;
    border-radius: 50%;
    animation: pulse 1.4s ease-in-out infinite;
  }

  &__content {
    flex: 1;
    min-height: 0;
    padding: 20px 24px;
    overflow: auto;
  }

  /* ---- 移动端 ---- */
  &.is-mobile {
    padding-bottom: calc(50px + env(safe-area-inset-bottom));

    .layout__header {
      gap: 8px;
      padding: 0 14px;
    }

    .layout__subtitle {
      display: none;
    }

    .layout__content {
      padding: 12px 12px 16px;
    }

    .layout__speed {
      padding: 4px 8px;
      font-size: 11px;
    }

    .layout__badge {
      padding: 4px 8px;
      font-size: 11px;
      white-space: nowrap;
    }
  }
}

@keyframes pulse {
  0%,
  100% {
    opacity: 1;
  }
  50% {
    opacity: 0.3;
  }
}
</style>

<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { showConfirmDialog, showSuccessToast } from 'vant'
import AppIcon from '@/components/AppIcon.vue'
import { useUserStore } from '@/stores/user'
import { maskPhone } from '@/utils/format'

const user = useUserStore()
const router = useRouter()
const visible = ref(false)
const rootRef = ref<HTMLElement>()

function onClick() {
  if (!user.logged) {
    router.push({ name: 'login' })
    return
  }
  visible.value = !visible.value
}

async function onLogout() {
  visible.value = false
  try {
    await showConfirmDialog({
      title: '退出登录',
      message: '退出后需要重新验证手机号才能继续下载，确定退出吗？',
      confirmButtonText: '退出登录',
      cancelButtonText: '再想想',
      confirmButtonColor: '#ef4444',
    })
  } catch {
    return
  }
  await user.doLogout()
  showSuccessToast('已退出登录')
  router.push({ name: 'login' })
}

function onDocumentClick(e: MouseEvent) {
  if (!visible.value) return
  const target = e.target as HTMLElement | null
  if (!target) return
  if (rootRef.value?.contains(target)) return
  if (target.closest('.van-popover')) return
  visible.value = false
}

onMounted(() => document.addEventListener('click', onDocumentClick))
onBeforeUnmount(() => document.removeEventListener('click', onDocumentClick))
</script>

<template>
  <div ref="rootRef">
    <van-popover
      v-model:show="visible"
      theme="light"
      trigger="manual"
      placement="right-end"
      :offset="[10, 0]"
      :teleport="'body'"
    >
      <div class="userpanel">
        <div class="userpanel__head">
          <div class="userpanel__avatar">{{ user.user?.initial ?? '?' }}</div>
          <div class="userpanel__info">
            <div class="userpanel__name">{{ user.displayName }}</div>
            <div class="userpanel__phone">
              {{ user.user ? maskPhone(user.user.phone) : '' }}
            </div>
          </div>
        </div>
        <div class="userpanel__divider" />
        <div class="userpanel__actions">
          <div
            class="userpanel__action"
            @click="((visible = false), router.push({ name: 'login' }))"
          >
            <AppIcon name="user" :size="15" />
            <span>账号信息</span>
          </div>
          <div class="userpanel__action is-danger" @click="onLogout">
            <AppIcon name="logout" :size="15" />
            <span>退出登录</span>
          </div>
        </div>
      </div>

      <template #reference>
        <div class="sidemenu-user" :class="{ 'is-logged': user.logged }" @click="onClick">
          <div v-if="user.logged" class="sidemenu-user__avatar">{{ user.user?.initial }}</div>
          <AppIcon v-else name="user" :size="17" />
          <div class="sidemenu-user__text">
            <span class="sidemenu-user__title">{{ user.logged ? '用户登录' : '用户登录' }}</span>
            <span v-if="user.logged" class="sidemenu-user__sub">
              {{ user.user ? maskPhone(user.user.phone) : '' }}
            </span>
            <span v-else class="sidemenu-user__sub is-warn">未登录</span>
          </div>
          <AppIcon v-if="user.logged" name="chevronRight" :size="14" />
        </div>
      </template>
    </van-popover>
  </div>
</template>

<style scoped lang="scss">
/* Vant 会在 reference 外包一层 van-popover__wrapper，默认不撑满，
   导致「用户登录」项比其他三个菜单项窄 */
:deep(.van-popover__wrapper) {
  display: block;
  width: 100%;
}

.sidemenu-user {
  display: flex;
  align-items: center;
  gap: 10px;
  width: 100%;
  padding: 8px 12px;
  margin-bottom: 4px;
  color: $color-text-secondary;
  cursor: pointer;
  border: 1px solid transparent;
  border-radius: $radius-md;
  transition:
    background 0.16s,
    color 0.16s;

  &:hover {
    background: $color-bg-hover;
    color: $color-text;
  }

  &.is-logged {
    margin-bottom: 8px;
    background: $color-bg;
    border-color: $color-border;
  }

  &__avatar {
    display: flex;
    flex: none;
    align-items: center;
    justify-content: center;
    width: 26px;
    height: 26px;
    font-size: 12px;
    font-weight: 600;
    color: #fff;
    background: linear-gradient(135deg, #37bcf0, #1c86cf);
    border-radius: 50%;
  }

  &__text {
    display: flex;
    flex: 1;
    flex-direction: column;
    min-width: 0;
  }

  &__title {
    font-size: 13.5px;
    line-height: 1.3;
    color: $color-text;
  }

  &__sub {
    overflow: hidden;
    font-size: 11px;
    color: $color-text-placeholder;
    white-space: nowrap;
    text-overflow: ellipsis;

    &.is-warn {
      color: $color-warning;
    }
  }
}
</style>

<style lang="scss">
/* 弹出面板 teleport 到 body，样式不能 scoped */
.userpanel {
  width: 208px;
  padding: 6px;

  &__head {
    display: flex;
    gap: 10px;
    align-items: center;
    padding: 8px 10px 10px;
  }

  &__avatar {
    display: flex;
    flex: none;
    align-items: center;
    justify-content: center;
    width: 34px;
    height: 34px;
    font-size: 14px;
    font-weight: 600;
    color: #fff;
    background: linear-gradient(135deg, #37bcf0, #1c86cf);
    border-radius: 50%;
  }

  &__info {
    min-width: 0;
  }

  &__name {
    font-size: 13.5px;
    font-weight: 600;
    color: $color-text;
  }

  &__phone {
    font-size: 11.5px;
    color: $color-text-placeholder;
  }

  &__divider {
    height: 1px;
    margin: 2px 6px;
    background: $color-border;
  }

  &__actions {
    padding: 4px 0 2px;
  }

  &__action {
    display: flex;
    align-items: center;
    gap: 8px;
    padding: 8px 10px;
    font-size: 13px;
    color: $color-text;
    cursor: pointer;
    border-radius: $radius-sm;

    &:hover {
      background: $color-bg-hover;
    }

    &.is-danger {
      color: $color-danger;

      &:hover {
        background: rgba(239, 68, 68, 0.08);
      }
    }
  }
}
</style>

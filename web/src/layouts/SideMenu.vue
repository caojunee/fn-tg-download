<script setup lang="ts">
import { computed } from 'vue'
import UserMenu from './UserMenu.vue'
import AppIcon from '@/components/AppIcon.vue'
import { useDownloadStore } from '@/stores/download'

const download = useDownloadStore()

/** 嵌在飞牛 iframe 里时系统标题栏已展示应用名+图标，品牌区隐藏避免重复 */
const embedded = window.self !== window.top

const items = computed(() => [
  {
    name: 'new',
    title: '新建下载',
    icon: 'plus',
    badge: 0,
  },
  {
    name: 'tasks',
    title: '下载管理',
    icon: 'list',
    badge: download.activeTasks.length,
  },
  {
    name: 'settings',
    title: '系统管理',
    icon: 'settings',
    badge: 0,
  },
])
</script>

<template>
  <aside class="sidemenu">
    <div v-if="!embedded" class="sidemenu__brand">
      <div class="sidemenu__logo">
        <AppIcon name="send" :size="18" />
      </div>
      <div class="sidemenu__brand-text">
        <div class="sidemenu__name">TG Download</div>
        <div class="sidemenu__slogan">Telegram 下载器</div>
      </div>
    </div>

    <nav class="sidemenu__nav">
      <UserMenu />

      <router-link
        v-for="item in items"
        :key="item.name"
        v-slot="{ isActive, navigate }"
        :to="{ name: item.name }"
        custom
      >
        <div class="sidemenu__item" :class="{ 'is-active': isActive }" @click="navigate">
          <AppIcon :name="item.icon" :size="17" />
          <span class="sidemenu__label">{{ item.title }}</span>
          <span v-if="item.badge" class="sidemenu__badge">{{ item.badge }}</span>
        </div>
      </router-link>
    </nav>

    <div class="sidemenu__footer">
      <div class="sidemenu__engine">
        <span class="sidemenu__engine-dot" />
        <span>tdl 引擎就绪</span>
      </div>
      <div class="sidemenu__version">v0.3.4 · fpk</div>
    </div>
  </aside>
</template>

<style scoped lang="scss">
.sidemenu {
  display: flex;
  flex: none;
  flex-direction: column;
  width: $sidebar-width;
  background: $color-bg-card;
  border-right: 1px solid $color-border;

  &__brand {
    display: flex;
    align-items: center;
    gap: 10px;
    height: $header-height;
    padding: 0 16px;
    border-bottom: 1px solid $color-border;
  }

  &__logo {
    display: flex;
    align-items: center;
    justify-content: center;
    width: 32px;
    height: 32px;
    color: #fff;
    background: linear-gradient(135deg, #37bcf0, #2196d9);
    border-radius: 9px;
  }

  &__brand-text {
    min-width: 0;
  }

  &__name {
    font-size: 14px;
    font-weight: 600;
    line-height: 1.2;
  }

  &__slogan {
    font-size: 11px;
    color: $color-text-placeholder;
  }

  &__nav {
    display: flex;
    flex: 1;
    flex-direction: column;
    gap: 4px;
    padding: 12px 10px;
    overflow: auto;
  }

  &__item {
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 9px 12px;
    font-size: 13.5px;
    color: $color-text-secondary;
    cursor: pointer;
    border-radius: $radius-md;
    transition:
      background 0.16s,
      color 0.16s;
    user-select: none;

    &:hover {
      background: $color-bg-hover;
      color: $color-text;
    }

    &.is-active {
      color: $color-primary;
      background: $color-primary-light;
      font-weight: 600;

      .sidemenu__badge {
        color: #fff;
        background: $color-primary;
      }
    }
  }

  &__label {
    flex: 1;
    min-width: 0;
    overflow: hidden;
    white-space: nowrap;
    text-overflow: ellipsis;
  }

  &__badge {
    min-width: 18px;
    padding: 0 5px;
    font-size: 11px;
    font-weight: 600;
    line-height: 17px;
    text-align: center;
    color: $color-text-secondary;
    background: $color-bg;
    border-radius: 9px;
  }

  &__footer {
    padding: 12px 16px;
    border-top: 1px solid $color-border;
  }

  &__engine {
    display: flex;
    align-items: center;
    gap: 6px;
    font-size: 12px;
    color: $color-text-secondary;
  }

  &__engine-dot {
    width: 6px;
    height: 6px;
    background: $color-success;
    border-radius: 50%;
  }

  &__version {
    margin-top: 3px;
    font-size: 11px;
    color: $color-text-placeholder;
  }
}
</style>

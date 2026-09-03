<script setup lang="ts">
import { ref, watch, computed } from 'vue'
import { showToast } from 'vant'
import AppIcon from '@/components/AppIcon.vue'
import { listDirs, type FsDirEntry } from '@/api/fs'
import { useIsMobile } from '@/composables/useIsMobile'

const isMobile = useIsMobile()

const show = defineModel<boolean>('show', { default: false })
const path = defineModel<string>('path', { default: '' })

const current = ref('')
const parent = ref('')
const dirs = ref<FsDirEntry[]>([])
const loading = ref(false)

const segments = computed(() => {
  if (!current.value || current.value === '/') return [{ name: '/', path: '/' }]
  const parts = current.value.split('/').filter(Boolean)
  const list: { name: string; path: string }[] = [{ name: '/', path: '/' }]
  let acc = ''
  for (const p of parts) {
    acc += `/${p}`
    list.push({ name: p, path: acc })
  }
  return list
})

async function load(target: string) {
  loading.value = true
  try {
    const res = await listDirs(target)
    current.value = res.path
    parent.value = res.parent
    dirs.value = res.dirs
  } catch (e) {
    // 初始目录已不存在时回落到根目录重新浏览
    if (!current.value && target && target !== '/' && target !== '') {
      showToast('原目录不存在，已回到根目录')
      await load('')
    } else {
      showToast((e as Error).message || '目录读取失败')
    }
  } finally {
    loading.value = false
  }
}

watch(show, (v) => {
  if (v) {
    current.value = ''
    void load(path.value || '')
  }
})

function enter(dir: FsDirEntry) {
  void load(dir.path)
}

function up() {
  if (parent.value) void load(parent.value)
}

function confirm() {
  if (!current.value) {
    showToast('请先选择目录')
    return
  }
  path.value = current.value
  show.value = false
}
</script>

<template>
  <van-popup
    v-model:show="show"
    :position="isMobile ? 'bottom' : 'center'"
    round
    :style="isMobile ? { height: '62%' } : { width: '540px', height: '500px' }"
    teleport="body"
  >
    <div class="dirpicker">
      <div class="dirpicker__header">
        <div class="dirpicker__title">选择目录</div>
        <div class="dirpicker__current" :title="current">
          <template v-for="(seg, i) in segments" :key="seg.path">
            <span
              class="dirpicker__crumb"
              :class="{ 'is-last': i === segments.length - 1 }"
              @click="i !== segments.length - 1 && load(seg.path)"
            >
              {{ seg.name }}
            </span>
            <span v-if="i !== segments.length - 1" class="dirpicker__sep">/</span>
          </template>
        </div>
      </div>

      <div class="dirpicker__list">
        <div v-if="parent" class="dirpicker__row dirpicker__row--up" @click="up">
          <AppIcon name="chevronRight" :size="15" class="dirpicker__up-icon" />
          <span>返回上级</span>
        </div>

        <div v-if="loading" class="dirpicker__loading">
          <van-loading size="20">加载中...</van-loading>
        </div>

        <template v-else>
          <div v-for="d in dirs" :key="d.path" class="dirpicker__row" @click="enter(d)">
            <AppIcon name="folder" :size="17" class="dirpicker__folder" />
            <span class="dirpicker__name">{{ d.name }}</span>
            <AppIcon name="chevronRight" :size="13" class="dirpicker__arrow" />
          </div>
          <van-empty
            v-if="!dirs.length"
            image="search"
            description="没有子目录了，可以选当前目录"
          />
        </template>
      </div>

      <div class="dirpicker__footer">
        <van-button block plain @click="show = false">取消</van-button>
        <van-button block type="primary" @click="confirm">选择此目录</van-button>
      </div>
    </div>
  </van-popup>
</template>

<style scoped lang="scss">
.dirpicker {
  display: flex;
  flex-direction: column;
  height: 100%;

  &__header {
    padding: 16px 16px 10px;
    border-bottom: 1px solid $color-border;
  }

  &__title {
    font-size: 15px;
    font-weight: 600;
  }

  &__current {
    display: flex;
    align-items: center;
    flex-wrap: wrap;
    gap: 2px;
    margin-top: 8px;
    padding: 6px 8px;
    font-family: Consolas, Monaco, monospace;
    font-size: 12px;
    line-height: 1.5;
    word-break: break-all;
    color: $color-text-secondary;
    background: $color-bg;
    border-radius: $radius-sm;
    max-height: 54px;
    overflow: auto;
  }

  &__crumb {
    cursor: pointer;
    border-radius: 4px;

    &:hover {
      color: $color-primary;
    }

    &.is-last {
      color: $color-primary;
      font-weight: 600;
      cursor: default;
    }
  }

  &__sep {
    color: $color-text-placeholder;
  }

  &__list {
    flex: 1;
    min-height: 0;
    padding: 8px 12px;
    overflow: auto;
  }

  &__row {
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 11px 10px;
    font-size: 13.5px;
    color: $color-text;
    cursor: pointer;
    border-radius: $radius-md;

    &:hover {
      background: $color-bg-hover;
    }

    &:active {
      background: $color-bg;
    }
  }

  &__row--up {
    color: $color-primary;
    margin-bottom: 4px;
  }

  &__up-icon {
    transform: rotate(-90deg);
  }

  &__folder {
    flex: none;
    color: $color-primary;
  }

  &__name {
    flex: 1;
    min-width: 0;
    overflow: hidden;
    white-space: nowrap;
    text-overflow: ellipsis;
  }

  &__arrow {
    flex: none;
    color: $color-text-placeholder;
  }

  &__loading {
    display: flex;
    justify-content: center;
    padding: 32px 0;
  }

  &__footer {
    display: flex;
    gap: 10px;
    padding: 10px 16px calc(12px + constant(safe-area-inset-bottom));
    padding-bottom: calc(12px + env(safe-area-inset-bottom));
    border-top: 1px solid $color-border;
  }
}
</style>

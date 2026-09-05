<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { showToast } from 'vant'
import AppIcon from './AppIcon.vue'
import ResponsivePopup from './ResponsivePopup.vue'
import { listResources, thumbUrl } from '@/api/resource'
import type { DownloadSelection, Resource, ResourceSet } from '@/types'
import { formatSize, formatTime } from '@/utils/format'

const props = defineProps<{
  show: boolean
  /** 要枚举的链接（整频道 / 相册 / 单条） */
  url: string
}>()

const emit = defineEmits<{
  (e: 'update:show', value: boolean): void
  (e: 'confirm', selection: DownloadSelection & { totalSize: number }): void
}>()

const loading = ref(false)
const set = ref<ResourceSet | null>(null)
const error = ref('')
const picked = ref<Set<number>>(new Set())
const keyword = ref('')
const kindFilter = ref<'all' | 'video' | 'photo' | 'other'>('all')
/** 缩略图加载失败的资源 key，回退为图标占位 */
const brokenThumbs = ref<Set<string>>(new Set())

watch(
  () => props.show,
  (v) => {
    if (v) load()
  },
)

async function load() {
  loading.value = true
  error.value = ''
  set.value = null
  picked.value = new Set()
  brokenThumbs.value = new Set()
  keyword.value = ''
  try {
    set.value = await listResources(props.url)
    if (set.value.error) {
      error.value = set.value.error
      return
    }
    // 默认全选，用户再做反选 / 取消
    set.value.resources.forEach((r) => picked.value.add(r.msgId))
  } catch (e) {
    error.value = e instanceof Error ? e.message : String(e)
  } finally {
    loading.value = false
  }
}

/** 按关键字与类型过滤后的可见条目 */
const visible = computed<Resource[]>(() => {
  const list = set.value?.resources ?? []
  const kw = keyword.value.trim().toLowerCase()
  return list.filter((r) => {
    if (kindFilter.value === 'video' && r.kind !== 'video') return false
    if (kindFilter.value === 'photo' && r.kind !== 'photo') return false
    if (kindFilter.value === 'other' && (r.kind === 'video' || r.kind === 'photo')) return false
    if (kw && !r.name.toLowerCase().includes(kw)) return false
    return true
  })
})

const selectedCount = computed(() => picked.value.size)
const selectedSize = computed(() =>
  (set.value?.resources ?? []).reduce((s, r) => (picked.value.has(r.msgId) ? s + r.size : s), 0),
)

function toggle(id: number) {
  if (picked.value.has(id)) picked.value.delete(id)
  else picked.value.add(id)
}

function selectAll() {
  visible.value.forEach((r) => picked.value.add(r.msgId))
}

/** 反选：对当前可见条目取反 */
function invert() {
  visible.value.forEach((r) => toggle(r.msgId))
}

function clear() {
  picked.value.clear()
}

function onThumbError(key: string) {
  brokenThumbs.value.add(key)
}

function close() {
  emit('update:show', false)
}

function confirm() {
  if (!set.value || selectedCount.value === 0) {
    showToast('请至少选择一个资源')
    return
  }
  // 按清单原始顺序输出，下载顺序与频道展示顺序一致
  const msgIds = (set.value.resources ?? [])
    .filter((r) => picked.value.has(r.msgId))
    .map((r) => r.msgId)
  emit('confirm', { peerId: set.value.peerId, msgIds, totalSize: selectedSize.value })
  close()
}

const kindIcon = (kind: string) =>
  kind === 'video' ? 'play' : kind === 'photo' ? 'image' : kind === 'audio' ? 'zap' : 'file'

const FILTERS: { key: 'all' | 'video' | 'photo' | 'other'; label: string }[] = [
  { key: 'all', label: '全部' },
  { key: 'video', label: '视频' },
  { key: 'photo', label: '图片' },
  { key: 'other', label: '其它' },
]
</script>

<template>
  <!-- 桌面居中弹窗 / 移动端上拉抽屉，容器内按平台懒加载 -->
  <ResponsivePopup :show="show" @update:show="(v: boolean) => emit('update:show', v)">
    <div class="picker">
      <div class="picker__head">
        <div class="picker__title">
          <AppIcon name="layers" :size="15" />
          选择要下载的资源
        </div>
        <div class="picker__sub">{{ url }}</div>
      </div>

      <div v-if="loading" class="picker__loading">
        <van-loading size="22" vertical>正在读取资源列表…</van-loading>
        <div class="picker__hint">整频道会遍历全部消息，频道越大耗时越长</div>
      </div>

      <div v-else-if="error" class="picker__error">
        <AppIcon name="alert" :size="18" />
        <div>{{ error }}</div>
        <van-button plain type="primary" size="small" @click="load">重试</van-button>
      </div>

      <template v-else-if="set">
        <div class="picker__bar">
          <input v-model="keyword" class="picker__search" placeholder="按文件名筛选" />
          <div class="picker__filters">
            <span
              v-for="f in FILTERS"
              :key="f.key"
              class="picker__chip"
              :class="{ 'picker__chip--on': kindFilter === f.key }"
              @click="kindFilter = f.key"
              >{{ f.label }}</span
            >
          </div>
        </div>

        <div class="picker__actions">
          <span class="picker__act" @click="selectAll">全选</span>
          <span class="picker__act" @click="invert">反选</span>
          <span class="picker__act" @click="clear">清空</span>
          <span class="picker__stat">
            可见 {{ visible.length }} / 共 {{ set.total }} 条
          </span>
        </div>

        <div class="picker__grid">
          <div
            v-for="r in visible"
            :key="r.key"
            class="card"
            :class="{ 'card--on': picked.has(r.msgId) }"
            @click="toggle(r.msgId)"
          >
            <div class="card__thumb">
              <img
                v-if="r.hasThumb && !brokenThumbs.has(r.key)"
                :src="thumbUrl(r.key, url)"
                :alt="r.name"
                loading="lazy"
                decoding="async"
                @error="onThumbError(r.key)"
              />
              <div v-else class="card__ph">
                <AppIcon :name="kindIcon(r.kind)" :size="20" />
              </div>
              <span class="card__check">
                <AppIcon v-if="picked.has(r.msgId)" name="check" :size="11" />
              </span>
              <span class="card__badge">{{ r.kind === 'video' ? '视频' : r.kind === 'photo' ? '图片' : '文件' }}</span>
            </div>
            <div class="card__body">
              <div class="card__name" :title="r.name">{{ r.name }}</div>
              <div class="card__meta">
                <span>{{ formatSize(r.size) }}</span>
                <span v-if="r.date">{{ formatTime(r.date) }}</span>
              </div>
            </div>
          </div>
          <van-empty v-if="!visible.length" description="没有匹配的资源" />
        </div>

        <div class="picker__foot">
          <div class="picker__sum">
            已选 <b>{{ selectedCount }}</b> 个 · {{ formatSize(selectedSize) }}
          </div>
          <div class="picker__btns">
            <van-button plain @click="close">取消</van-button>
            <van-button type="primary" :disabled="!selectedCount" @click="confirm">
              下载选中{{ selectedCount ? `（${selectedCount}）` : '' }}
            </van-button>
          </div>
        </div>
      </template>
    </div>
  </ResponsivePopup>
</template>

<style scoped lang="scss">
.picker {
  display: flex;
  flex-direction: column;
  height: 100%;
  min-height: 0;

  &__head {
    position: relative;
    padding: 14px 16px 12px;
    border-bottom: 1px solid $color-border;
  }

  &__title {
    display: flex;
    gap: 7px;
    align-items: center;
    font-size: 14.5px;
    font-weight: 600;
    color: $color-text;
  }

  &__sub {
    margin-top: 4px;
    overflow: hidden;
    font-family: Consolas, Monaco, monospace;
    font-size: 11.5px;
    color: $color-text-placeholder;
    white-space: nowrap;
    text-overflow: ellipsis;
  }

  &__loading {
    display: flex;
    flex: 1;
    flex-direction: column;
    gap: 10px;
    align-items: center;
    justify-content: center;
    padding: 40px 20px;
  }

  &__hint {
    font-size: 12px;
    color: $color-text-secondary;
  }

  &__error {
    display: flex;
    flex: 1;
    flex-direction: column;
    gap: 12px;
    align-items: center;
    justify-content: center;
    padding: 40px 30px;
    font-size: 13px;
    line-height: 1.7;
    color: $color-danger;
    text-align: center;
  }

  &__bar {
    display: flex;
    flex-wrap: wrap;
    gap: 10px;
    align-items: center;
    padding: 10px 16px 0;
  }

  &__search {
    flex: 1;
    min-width: 180px;
    padding: 7px 11px;
    font-size: 12.5px;
    color: $color-text;
    background: #fbfcfd;
    border: 1px solid $color-border;
    border-radius: $radius-md;
    outline: none;

    &:focus {
      border-color: $color-primary;
      background: #fff;
    }
  }

  &__filters {
    display: flex;
    gap: 6px;
  }

  &__chip {
    padding: 5px 11px;
    font-size: 12px;
    color: $color-text-secondary;
    cursor: pointer;
    background: $color-bg;
    border-radius: 999px;

    &--on {
      color: #fff;
      background: $color-primary;
    }
  }

  &__actions {
    display: flex;
    flex-wrap: wrap;
    gap: 14px;
    align-items: center;
    padding: 10px 16px;
    font-size: 12.5px;
  }

  &__act {
    color: $color-primary;
    cursor: pointer;
    user-select: none;
  }

  &__stat {
    margin-left: auto;
    color: $color-text-secondary;
  }

  &__grid {
    display: grid;
    flex: 1;
    grid-template-columns: repeat(auto-fill, minmax(148px, 1fr));
    gap: 12px;
    align-content: start;
    min-height: 0;
    padding: 4px 16px 16px;
    overflow-y: auto;
  }

  &__foot {
    display: flex;
    flex-wrap: wrap;
    gap: 10px;
    align-items: center;
    justify-content: space-between;
    padding: 11px 16px;
    border-top: 1px solid $color-border;
  }

  &__sum {
    font-size: 13px;
    color: $color-text-secondary;

    b {
      color: $color-primary;
    }
  }

  &__btns {
    display: flex;
    gap: 9px;
  }
}

.card {
  overflow: hidden;
  cursor: pointer;
  background: #fff;
  border: 1px solid $color-border;
  border-radius: $radius-md;
  transition:
    border-color 0.16s,
    box-shadow 0.16s;

  &--on {
    border-color: $color-primary;
    box-shadow: 0 0 0 2px rgba(45, 140, 240, 0.14);
  }

  &__thumb {
    position: relative;
    aspect-ratio: 16 / 9;
    background: $color-bg;

    img {
      object-fit: cover;
      width: 100%;
      height: 100%;
      display: block;
    }
  }

  &__ph {
    display: flex;
    align-items: center;
    justify-content: center;
    width: 100%;
    height: 100%;
    color: $color-text-placeholder;
  }

  &__check {
    position: absolute;
    top: 6px;
    left: 6px;
    display: flex;
    align-items: center;
    justify-content: center;
    width: 18px;
    height: 18px;
    color: #fff;
    background: rgba(0, 0, 0, 0.28);
    border: 1.5px solid rgba(255, 255, 255, 0.85);
    border-radius: 5px;

    .card--on & {
      background: $color-primary;
      border-color: #fff;
    }
  }

  &__badge {
    position: absolute;
    right: 6px;
    bottom: 6px;
    padding: 2px 6px;
    font-size: 10.5px;
    color: #fff;
    background: rgba(0, 0, 0, 0.55);
    border-radius: 4px;
  }

  &__body {
    padding: 8px 9px 9px;
  }

  &__name {
    display: -webkit-box;
    overflow: hidden;
    font-size: 12px;
    line-height: 1.45;
    color: $color-text;
    -webkit-box-orient: vertical;
    -webkit-line-clamp: 2;
    word-break: break-all;
  }

  &__meta {
    display: flex;
    justify-content: space-between;
    margin-top: 5px;
    font-size: 11px;
    color: $color-text-secondary;
  }
}
</style>

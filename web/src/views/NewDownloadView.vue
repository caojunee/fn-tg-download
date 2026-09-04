<script setup lang="ts">
import { computed, ref } from 'vue'
import { useRouter } from 'vue-router'
import { showSuccessToast, showToast } from 'vant'
import AppIcon from '@/components/AppIcon.vue'
import DirPicker from '@/components/DirPicker.vue'
import { probe } from '@/api/download'
import type { ProbeResult } from '@/api/download'
import { useDownloadStore } from '@/stores/download'
import { useSettingsStore } from '@/stores/settings'
import { useUserStore } from '@/stores/user'
import { formatSize } from '@/utils/format'

const router = useRouter()
const download = useDownloadStore()
const settings = useSettingsStore()
const user = useUserStore()

const SAMPLE = 'https://t.me/siwass/6729'

const rawUrls = ref('')
const probing = ref(false)
const results = ref<ProbeResult[]>([])
const probed = ref(false)

/** 系统设置异步加载完成后自动同步；用户手动修改后以用户输入为准 */
function follow<T>(getter: () => T) {
  const override = ref<T | null>(null)
  return computed({
    get: () => (override.value === null ? getter() : override.value),
    set: (v: T) => {
      override.value = v
    },
  })
}

/** 相册 / 组合消息探测（对应 tdl --group），默认跟随系统设置 */
const withGroup = follow(() => settings.settings.autoGroup)
const savePath = follow(() => settings.settings.downloadPath)
const concurrency = follow(() => settings.settings.concurrency)
const includeExt = ref('')
const excludeExt = ref('')
const pickerShow = ref(false)

const urlList = computed(() =>
  Array.from(
    new Set(
      rawUrls.value
        .split(/\s+|,|，/)
        .map((s) => s.trim())
        .filter(Boolean),
    ),
  ),
)

const totalFiles = computed(() => results.value.reduce((s, r) => s + r.fileCount, 0))
const totalSize = computed(() => results.value.reduce((s, r) => s + r.totalSize, 0))
const albumCount = computed(
  () => results.value.filter((r) => r.kind !== 'single' && r.kind !== 'channel').length,
)
const channelCount = computed(() => results.value.filter((r) => r.kind === 'channel').length)
const kindIcon = (kind: string) => (kind === 'single' ? 'file' : kind === 'channel' ? 'folder' : 'layers')

function fillSample() {
  rawUrls.value = [SAMPLE, 'https://t.me/tdl/1'].join('\n')
}

function clearAll() {
  rawUrls.value = ''
  results.value = []
  probed.value = false
}

async function onProbe() {
  if (!urlList.value.length) {
    showToast('请先输入至少一个链接')
    return
  }
  const invalid = urlList.value.filter((u) => !/^https?:\/\/t\.me\/.+/i.test(u))
  if (invalid.length) {
    showToast(`链接格式不正确：${invalid[0]}`)
    return
  }
  probing.value = true
  try {
    results.value = await probe(urlList.value)
    probed.value = true
    if (!results.value.length) showToast('未解析到可下载的内容')
  } finally {
    probing.value = false
  }
}

function removeResult(index: number) {
  results.value.splice(index, 1)
}

async function onStart() {
  if (!urlList.value.length) {
    showToast('请先输入链接')
    return
  }
  if (!user.logged) {
    showToast('请先登录 Telegram 账号')
    router.push({ name: 'login' })
    return
  }
  const created = await download.create({
    urls: urlList.value,
    group: withGroup.value,
    savePath: savePath.value,
    concurrency: concurrency.value,
    include: includeExt.value || undefined,
    exclude: excludeExt.value || undefined,
  })
  showSuccessToast(`已创建 ${created.length} 个下载任务`)
  rawUrls.value = ''
  results.value = []
  probed.value = false
  router.push({ name: 'tasks' })
}
</script>

<template>
  <div class="page">
    <div class="new">
      <!-- 左：链接输入 + 解析结果 -->
      <div class="new__col">
        <div class="app-card">
          <div class="app-card__header">
            <div class="app-card__header-title">
              <AppIcon name="download" :size="16" />
              添加下载链接
            </div>
            <div class="new__tools">
              <span class="new__tool" @click="fillSample">填入示例</span>
              <span class="new__divider" />
              <span class="new__tool" @click="clearAll">清空</span>
            </div>
          </div>

          <div class="app-card__body">
            <textarea
              v-model="rawUrls"
              class="new__textarea"
              rows="6"
              :placeholder="`每行一个链接，支持空格 / 逗号分隔\n例如：${SAMPLE}`"
            />
            <div class="new__meta">
              <span
                >已识别 <b>{{ urlList.length }}</b> 个链接</span
              >
              <span class="new__format">支持 https://t.me/频道/消息ID</span>
            </div>

            <div class="new__actions">
              <van-button
                plain
                type="primary"
                :loading="probing"
                :disabled="!urlList.length"
                @click="onProbe"
              >
                <template #icon><AppIcon name="search" :size="14" /></template>
                解析链接
              </van-button>
              <van-button
                type="primary"
                :loading="download.creating"
                :disabled="!urlList.length"
                @click="onStart"
              >
                <template #icon><AppIcon name="download" :size="14" /></template>
                开始下载
              </van-button>
            </div>
          </div>
        </div>

        <!-- 解析结果 -->
        <div v-if="probed" class="app-card">
          <div class="app-card__header">
            <div class="app-card__header-title">
              <AppIcon name="layers" :size="16" />
              解析结果
            </div>
            <div class="new__summary">
              共 {{ results.length }} 条消息 · {{ totalFiles }} 个文件 ·
              {{ formatSize(totalSize) }}
              <span v-if="albumCount" class="new__album-tip">
                检测到 {{ albumCount }} 组相册 / 组合消息
              </span>
              <span v-if="channelCount" class="new__album-tip">
                检测到 {{ channelCount }} 个整频道任务
              </span>
            </div>
          </div>

          <div class="app-card__body new__results">
            <div v-for="(item, index) in results" :key="item.url" class="probe">
              <div class="probe__icon" :class="`probe__icon--${item.kind}`">
                <AppIcon :name="kindIcon(item.kind)" :size="17" />
              </div>
              <div class="probe__body">
                <div class="probe__title">{{ item.title }}</div>
                <div class="probe__meta">
                  <span class="probe__url">{{ item.url }}</span>
                  <span v-if="item.kind === 'channel'" class="probe__channel-tip">文件数在任务启动后统计</span>
                  <template v-else>
                    <span>{{ item.fileCount }} 个文件</span>
                    <span>{{ formatSize(item.totalSize) }}</span>
                  </template>
                </div>
              </div>
              <span v-if="item.kind === 'channel'" class="probe__badge"> 整频道下载 </span>
              <span v-else-if="item.kind !== 'single'" class="probe__badge"> 相册 / 组合消息 </span>
              <span class="probe__remove" @click="removeResult(index)">
                <AppIcon name="close" :size="14" />
              </span>
            </div>

            <van-empty v-if="!results.length" description="没有可下载的内容" />
          </div>
        </div>
      </div>

      <!-- 右：下载选项 -->
      <div class="new__col new__col--side">
        <div class="app-card">
          <div class="app-card__header">
            <div class="app-card__header-title">
              <AppIcon name="settings" :size="16" />
              下载选项
            </div>
          </div>

          <div class="app-card__body new__options">
            <div class="opt">
              <div class="opt__main">
                <div class="opt__title">相册 / 组合消息探测</div>
                <div class="opt__desc">
                  自动检测消息是否为相册或组合消息，并下载其中的全部文件（tdl
                  <code>--group</code>）
                </div>
              </div>
              <van-switch v-model="withGroup" size="20" />
            </div>

            <div class="opt">
              <div class="opt__main">
                <div class="opt__title">保存路径</div>
                <div class="opt__desc">点击下方输入框选择目录，留空则使用系统设置中的默认下载路径</div>
              </div>
            </div>
            <input
              v-model="savePath"
              class="opt__input opt__input--picker"
              placeholder="默认下载路径（点击选择）"
              readonly
              @click="pickerShow = true"
            />
            <DirPicker v-model:show="pickerShow" v-model:path="savePath" />

            <div class="opt">
              <div class="opt__main">
                <div class="opt__title">并发数</div>
                <div class="opt__desc">同时进行的文件下载数量</div>
              </div>
              <van-stepper v-model="concurrency" :min="1" :max="16" integer button-size="22" />
            </div>

            <div class="opt__divider" />

            <div class="opt__sub">文件过滤（可选）</div>
            <input v-model="includeExt" class="opt__input" placeholder="仅下载：mp4,mkv,jpg" />
            <input v-model="excludeExt" class="opt__input" placeholder="排除：zip,rar" />
          </div>
        </div>

        <div class="app-card new__tip-card">
          <div class="new__tip">
            <AppIcon name="alert" :size="14" />
            <div>
              需要下载私有频道 / 群组内容时，请先在「用户登录」中完成手机号验证，否则解析会失败。
            </div>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped lang="scss">
.new {
  display: grid;
  grid-template-columns: minmax(0, 1fr) 320px;
  gap: 16px;
  align-items: start;

  @media (max-width: 1080px) {
    grid-template-columns: minmax(0, 1fr);
  }

  &__col {
    display: flex;
    flex-direction: column;
    gap: 16px;
    min-width: 0;
  }

  &__tools {
    display: flex;
    align-items: center;
    gap: 10px;
    font-size: 12.5px;
    color: $color-text-secondary;
  }

  &__tool {
    color: $color-primary;
    cursor: pointer;
  }

  &__divider {
    width: 1px;
    height: 11px;
    background: $color-border;
  }

  &__textarea {
    width: 100%;
    padding: 12px 14px;
    font-family: Consolas, Monaco, monospace;
    font-size: 13px;
    line-height: 1.7;
    color: $color-text;
    background: #fbfcfd;
    border: 1px solid $color-border;
    border-radius: $radius-md;
    outline: none;
    resize: vertical;
    transition: border-color 0.16s;

    &::placeholder {
      font-family: $font-family;
      color: $color-text-placeholder;
    }

    &:focus {
      border-color: $color-primary;
      background: #fff;
    }
  }

  &__meta {
    display: flex;
    align-items: center;
    justify-content: space-between;
    margin-top: 9px;
    font-size: 12px;
    color: $color-text-secondary;

    b {
      color: $color-primary;
    }
  }

  &__format {
    color: $color-text-placeholder;
  }

  &__actions {
    display: flex;
    gap: 10px;
    margin-top: 16px;

    .van-button {
      flex: 1;
    }
  }

  &__summary {
    font-size: 12px;
    color: $color-text-secondary;
  }

  &__album-tip {
    padding: 2px 7px;
    margin-left: 4px;
    color: $color-primary;
    background: $color-primary-light;
    border-radius: 999px;
  }

  &__results {
    display: flex;
    flex-direction: column;
    gap: 10px;
    padding: 14px;
  }

  &__options {
    display: flex;
    flex-direction: column;
    gap: 14px;
  }

  &__tip-card {
    padding: 14px 16px;
  }

  &__tip {
    display: flex;
    gap: 8px;
    font-size: 12px;
    line-height: 1.6;
    color: $color-warning;
  }
}

.probe {
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 12px;
  background: #fbfcfd;
  border: 1px solid $color-border;
  border-radius: $radius-md;

  &__icon {
    display: flex;
    flex: none;
    align-items: center;
    justify-content: center;
    width: 34px;
    height: 34px;
    color: $color-text-secondary;
    background: $color-bg;
    border-radius: 9px;

    &--album,
    &--group {
      color: $color-primary;
      background: $color-primary-light;
    }

    &--channel {
      color: $color-success;
      background: rgba(82, 196, 26, 0.12);
    }
  }

  &__body {
    flex: 1;
    min-width: 0;
  }

  &__title {
    overflow: hidden;
    font-size: 13.5px;
    font-weight: 600;
    white-space: nowrap;
    text-overflow: ellipsis;
  }

  &__meta {
    display: flex;
    flex-wrap: wrap;
    gap: 10px;
    margin-top: 4px;
    font-size: 11.5px;
    color: $color-text-secondary;
  }

  &__url {
    font-family: Consolas, Monaco, monospace;
    color: $color-text-placeholder;
  }

  &__channel-tip {
    color: $color-text-placeholder;
  }

  &__badge {
    flex: none;
    padding: 3px 8px;
    font-size: 11px;
    color: $color-primary;
    background: $color-primary-light;
    border-radius: 999px;
  }

  &__remove {
    display: flex;
    flex: none;
    align-items: center;
    justify-content: center;
    width: 24px;
    height: 24px;
    color: $color-text-placeholder;
    cursor: pointer;
    border-radius: 6px;

    &:hover {
      color: $color-danger;
      background: rgba(239, 68, 68, 0.08);
    }
  }
}

.opt {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;

  &__main {
    min-width: 0;
  }

  &__title {
    font-size: 13.5px;
    font-weight: 600;
  }

  &__desc {
    margin-top: 4px;
    font-size: 11.5px;
    line-height: 1.6;
    color: $color-text-secondary;

    code {
      padding: 1px 4px;
      font-family: Consolas, Monaco, monospace;
      background: $color-bg;
      border-radius: 4px;
    }
  }

  &__input {
    width: 100%;
    padding: 9px 12px;
    margin-top: -6px;
    font-family: Consolas, Monaco, monospace;
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

    &--picker {
      cursor: pointer;

      &:hover {
        border-color: $color-primary;
      }
    }
  }

  &__divider {
    height: 1px;
    background: $color-border;
  }

  &__sub {
    font-size: 12px;
    color: $color-text-secondary;
  }
}
</style>

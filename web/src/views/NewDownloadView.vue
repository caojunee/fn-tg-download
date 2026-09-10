<script setup lang="ts">
import { computed, ref } from 'vue'
import { useRouter } from 'vue-router'
import { showSuccessToast, showToast } from 'vant'
import AppIcon from '@/components/AppIcon.vue'
import DirPicker from '@/components/DirPicker.vue'
import ResourcePicker from '@/components/ResourcePicker.vue'
import { probe, lookupUrls } from '@/api/download'
import type { ProbeResult, UrlLedgerEntry } from '@/api/download'
import type { DownloadSelection } from '@/types'
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

/** 资源选择器状态 */
const resourceShow = ref(false)
const resourceUrl = ref('')
/** 「下载选中」直接建任务：提交中（按钮 loading + 期间不允许关闭弹窗） */
const resourceSubmitting = ref(false)
/** 链接 → 用户勾选的范围（含总大小，仅用于界面统计） */
interface SelectionState extends DownloadSelection {
  totalSize: number
}
const selections = ref<Record<string, SelectionState>>({})

/** 支持资源选择的链接类型：整频道 / 相册 / 组合消息 */
const selectable = (item: ProbeResult) => item.kind === 'channel' || item.kind === 'album' || item.kind === 'group'

function openResources(item: ProbeResult) {
  resourceUrl.value = item.url
  resourceShow.value = true
}

/** 已登录校验：未登录时提示并跳转登录页 */
function ensureLogged(): boolean {
  if (!user.logged) {
    showToast('请先登录 Telegram 账号')
    router.push({ name: 'login' })
    return false
  }
  return true
}

/** 组装一次创建请求的参数（保存路径 / 并发 / 过滤等取自右侧下载选项） */
function createPayload(urls: string[], selected?: Record<string, DownloadSelection>) {
  return {
    urls,
    group: withGroup.value,
    savePath: savePath.value,
    concurrency: concurrency.value,
    include: includeExt.value || undefined,
    exclude: excludeExt.value || undefined,
    selected,
  }
}

/**
 * 立即下载后把该链接从待下载列表（输入框 + 解析结果）里摘掉。
 * 否则再点「开始下载」会重复创建同一个任务。
 */
function dropUrl(url: string) {
  rawUrls.value = urlList.value.filter((u) => u !== url).join('\n')
  results.value = results.value.filter((r) => r.url !== url)
  const next = { ...selections.value }
  delete next[url]
  selections.value = next
  if (!results.value.length) {
    probed.value = false
    rawUrls.value = ''
  }
}

/**
 * 资源选择器点「下载选中」：直接创建任务开始下载，不用再回页面点「开始下载」。
 * 失败时保持弹窗与已选范围，便于重试或改用「开始下载」。
 */
async function onPicked(url: string, sel: DownloadSelection & { totalSize: number }) {
  if (resourceSubmitting.value) return
  if (!ensureLogged()) return
  // 先记下勾选范围：创建失败时列表里仍显示"已选 N 个"，可改用「开始下载」兜底
  selections.value = { ...selections.value, [url]: { ...sel } }
  resourceSubmitting.value = true
  try {
    const created = await download.create(
      createPayload([url], { [url]: { peerId: sel.peerId, msgIds: sel.msgIds } }),
    )
    resourceShow.value = false
    const empty = results.value.length <= 1
    dropUrl(url)
    showSuccessToast(created.length ? `已开始下载 ${created.length} 个任务` : '已开始下载')
    if (empty) router.push({ name: 'tasks' })
  } catch {
    /* 具体错误已由请求层统一 toast，这里保留弹窗与勾选便于重试 */
  } finally {
    resourceSubmitting.value = false
  }
}

/** 已手动勾选下载范围的链接数 */
const pickedCount = computed(() => Object.keys(selections.value).length)

function clearSelection(url: string) {
  const next = { ...selections.value }
  delete next[url]
  selections.value = next
}

/** 一条链接当前勾选状态的展示文案 */
function selectionText(url: string): string {
  const sel = selections.value[url]
  if (!sel) return '选择资源'
  return `已选 ${sel.msgIds.length} 个 · ${formatSize(sel.totalSize)}`
}

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
/** 私有链接（t.me/c/...）：探测失败（无 access hash 缓存）时单独提示 */
const privateCount = computed(
  () => results.value.filter((r) => r.error && isPrivateUrl(r.url)).length,
)
const isPrivateUrl = (u: string) => /^https?:\/\/t\.me\/c\//i.test(u)
const kindIcon = (kind: string) => (kind === 'single' ? 'file' : kind === 'channel' ? 'folder' : 'layers')

function fillSample() {
  rawUrls.value = [SAMPLE, 'https://t.me/tdl/1'].join('\n')
}

function clearAll() {
  rawUrls.value = ''
  results.value = []
  probed.value = false
  selections.value = {}
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
    // 查链接台账（后端持久化，清历史/重开页面仍可查），标记已下载过的链接
    if (results.value.length) {
      lookupUrls(results.value.map((r) => r.url)).then((entries) => {
        const map: Record<string, UrlLedgerEntry> = {}
        for (const e of entries) map[e.url] = e
        ledger.value = map
      })
    }
  } finally {
    probing.value = false
  }
}

/** 链接台账：url → 最近一次任务快照（后端持久化，跨会话可查） */
const ledger = ref<Record<string, UrlLedgerEntry>>({})
const LEDGER_TEXT: Record<string, string> = {
  completed: '已下载',
  downloading: '下载中',
  pending: '排队中',
  paused: '已暂停',
  failed: '失败过',
  canceled: '已取消',
}
function ledgerStatus(url: string): string {
  return ledger.value[url]?.status || ''
}
function ledgerText(url: string): string {
  const s = ledgerStatus(url)
  return s ? (LEDGER_TEXT[s] || '已存在') : ''
}
/** 已存在于任务列表（任意状态）的解析结果数 */
const ledgerCount = computed(
  () => results.value.filter((r) => ledgerStatus(r.url)).length,
)

function removeResult(index: number) {
  const [item] = results.value.splice(index, 1)
  if (item) clearSelection(item.url)
}

async function onStart() {
  if (!urlList.value.length) {
    showToast('请先输入链接')
    return
  }
  if (!ensureLogged()) return
  const created = await download.create(
    createPayload(urlList.value, pickedCount.value ? selections.value : undefined),
  )
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
              <span v-if="privateCount" class="new__album-tip">
                其中 {{ privateCount }} 条私有链接启动后统计
              </span>
              <span v-if="ledgerCount" class="new__album-tip new__ledger-tip">
                {{ ledgerCount }} 条链接已在任务列表（{{ results.length - ledgerCount }} 条未下载）
              </span>
              <span v-if="pickedCount" class="new__album-tip">
                已手动选择 {{ pickedCount }} 条链接的下载范围
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
                  <span
                    v-else-if="item.error && isPrivateUrl(item.url)"
                    class="probe__channel-tip"
                  >私有链接 · 文件数在任务启动后统计</span>
                  <template v-else>
                    <span>{{ item.fileCount }} 个文件</span>
                    <span>{{ formatSize(item.totalSize) }}</span>
                  </template>
                </div>
              </div>
              <span
                v-if="ledgerStatus(item.url)"
                class="probe__badge"
                :class="`probe__badge--${ledgerStatus(item.url)}`"
              >
                {{ ledgerText(item.url) }}
              </span>
              <span v-else-if="item.kind === 'channel'" class="probe__badge"> 整频道下载 </span>
              <span v-else-if="item.error && isPrivateUrl(item.url)" class="probe__badge"> 私有链接 </span>
              <span v-else-if="item.kind !== 'single'" class="probe__badge"> 相册 / 组合消息 </span>
              <span
                v-if="selectable(item)"
                class="probe__pick"
                :class="{ 'probe__pick--on': selections[item.url] }"
                @click="openResources(item)"
              >
                <AppIcon name="list" :size="13" />
                {{ selectionText(item.url) }}
              </span>
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

    <!-- 资源选择器：解析后按链接选择部分资源，点「下载选中」即直接开始下载 -->
    <ResourcePicker
      v-model:show="resourceShow"
      :url="resourceUrl"
      :busy="resourceSubmitting"
      @confirm="(sel) => onPicked(resourceUrl, sel)"
    />
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

  /* 台账汇总：绿色（已完成）而非蓝色，与"检测到相册"等提示区分 */
  &__ledger-tip {
    color: $color-success;
    background: rgba(34, 197, 94, 0.12);
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

    /* 链接台账状态：一眼区分下载过没 */
    &--completed {
      color: $color-success;
      background: rgba(34, 197, 94, 0.12);
    }
    &--downloading,
    &--pending {
      color: $color-primary;
      background: $color-primary-light;
    }
    &--paused {
      color: $color-warning;
      background: rgba(245, 158, 11, 0.14);
    }
    &--failed,
    &--canceled {
      color: $color-text-secondary;
      background: $color-bg;
    }
  }

  &__pick {
    display: flex;
    flex: none;
    gap: 5px;
    align-items: center;
    max-width: 190px;
    padding: 4px 10px;
    overflow: hidden;
    font-size: 11.5px;
    color: $color-primary;
    cursor: pointer;
    white-space: nowrap;
    border: 1px solid rgba(45, 140, 240, 0.4);
    border-radius: 999px;
    transition:
      background 0.16s,
      color 0.16s;

    &:hover {
      color: #fff;
      background: $color-primary;
    }

    &--on {
      color: #fff;
      background: $color-primary;
      border-color: $color-primary;
    }
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

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { showSuccessToast, showToast } from 'vant'
import AppIcon from '@/components/AppIcon.vue'
import DirPicker from '@/components/DirPicker.vue'
import { useSettingsStore } from '@/stores/settings'

const settings = useSettingsStore()
const testing = ref(false)
const testResult = ref<{ ok: boolean; text: string } | null>(null)
const pickerShow = ref(false)

onMounted(() => settings.load())

async function onSave() {
  await settings.persist()
  showSuccessToast('设置已保存')
}

async function onTestProxy() {
  if (!settings.settings.proxy) {
    showToast('请先填写代理地址')
    return
  }
  testing.value = true
  testResult.value = null
  try {
    const res = await settings.checkProxy()
    testResult.value = { ok: res.ok, text: `连接正常，延迟 ${res.latency} ms` }
  } catch (e) {
    testResult.value = { ok: false, text: (e as Error).message || '代理连接失败' }
  } finally {
    testing.value = false
  }
}
</script>

<template>
  <div class="page">
    <div class="settings">
      <!-- 存储 -->
      <div class="app-card">
        <div class="app-card__header">
          <div class="app-card__header-title">
            <AppIcon name="folder" :size="16" />
            下载路径
          </div>
        </div>
        <div class="app-card__body">
          <div class="set">
            <div class="set__label">默认保存目录</div>
            <div class="set__row">
              <input
                v-model="settings.settings.downloadPath"
                class="set__input"
                placeholder="/vol1/1000/Telegram"
                @click="pickerShow = true"
              />
              <button class="set__btn" @click="pickerShow = true">选择</button>
            </div>
            <DirPicker v-model:show="pickerShow" v-model:path="settings.settings.downloadPath" />
            <div class="set__desc">
              下载的文件会按「频道名」自动分目录保存，路径需为飞牛 NAS 上已存在的目录。
            </div>
          </div>
        </div>
      </div>

      <!-- 网络 -->
      <div class="app-card">
        <div class="app-card__header">
          <div class="app-card__header-title">
            <AppIcon name="wifi" :size="16" />
            代理设置
          </div>
        </div>
        <div class="app-card__body">
          <div class="set">
            <div class="set__label">代理地址</div>
            <div class="set__row">
              <input
                v-model="settings.settings.proxy"
                class="set__input"
                placeholder="http://192.168.31.180:20172"
              />
              <button class="set__btn" :disabled="testing" @click="onTestProxy">
                {{ testing ? '测试中' : '测试连接' }}
              </button>
            </div>
            <div v-if="testResult" class="set__result" :class="{ 'is-ok': testResult.ok }">
              <AppIcon :name="testResult.ok ? 'checkCircle' : 'alert'" :size="13" />
              {{ testResult.text }}
            </div>
            <div class="set__desc">
              支持 <code>http://</code>、<code>https://</code>、<code>socks5://</code>，例如
              <code>http://192.168.31.180:20172</code>。Telegram 不可直连时必填。
            </div>
          </div>
        </div>
      </div>

      <!-- 下载 -->
      <div class="app-card">
        <div class="app-card__header">
          <div class="app-card__header-title">
            <AppIcon name="download" :size="16" />
            下载策略
          </div>
        </div>
        <div class="app-card__body set__list">
          <div class="opt">
            <div class="opt__main">
              <div class="opt__title">默认开启相册 / 组合消息探测</div>
              <div class="opt__desc">
                新建下载时默认勾选，自动检测并下载相册与组合消息中的全部文件（tdl
                <code>--group</code>）
              </div>
            </div>
            <van-switch v-model="settings.settings.autoGroup" size="20" />
          </div>

          <div class="set__divider" />

          <div class="opt">
            <div class="opt__main">
              <div class="opt__title">默认并发数</div>
              <div class="opt__desc">同时下载的文件数量，过高可能触发 Telegram 限流</div>
            </div>
            <van-stepper
              v-model="settings.settings.concurrency"
              :min="1"
              :max="16"
              integer
              button-size="22"
            />
          </div>

          <div class="set__divider" />

          <div class="opt">
            <div class="opt__main">
              <div class="opt__title">下载限速（KB/s）</div>
              <div class="opt__desc">0 表示不限速</div>
            </div>
            <van-stepper
              v-model="settings.settings.speedLimitKB"
              :min="0"
              :step="512"
              :max="102400"
              integer
              button-size="22"
            />
          </div>

          <div class="set__divider" />

          <div class="opt">
            <div class="opt__main">
              <div class="opt__title">单文件大小上限（MB）</div>
              <div class="opt__desc">超过该大小的文件将跳过不下载，0 表示不限制</div>
            </div>
            <van-stepper
              v-model="settings.settings.maxSizeMB"
              :min="0"
              :step="512"
              :max="102400"
              integer
              button-size="22"
            />
          </div>
        </div>
      </div>

      <!-- 引擎 -->
      <div class="app-card">
        <div class="app-card__header">
          <div class="app-card__header-title">
            <AppIcon name="settings" :size="16" />
            tdl 引擎
          </div>
        </div>
        <div class="app-card__body">
          <div class="set">
            <div class="set__label">tdl 可执行文件位置</div>
            <input
              v-model="settings.settings.tdlPath"
              class="set__input"
              placeholder="/usr/local/bin/tdl"
            />
            <div class="set__desc">
              当前版本 <code>v{{ settings.settings.version || 'x' }}</code> · 认证信息保存在
              <code>{{ settings.settings.tdlDataDir || '（后端未提供）' }}</code>
              下的命名空间文件
            </div>
          </div>
        </div>
      </div>

      <div class="settings__footer">
        <button class="settings__btn" @click="settings.load()">重新载入</button>
        <button
          class="settings__btn settings__btn--primary"
          :disabled="settings.saving"
          @click="onSave"
        >
          {{ settings.saving ? '保存中' : '保存设置' }}
        </button>
      </div>
    </div>
  </div>
</template>

<style scoped lang="scss">
.settings {
  display: flex;
  flex-direction: column;
  gap: 16px;
  max-width: 760px;

  &__footer {
    display: flex;
    gap: 10px;
    justify-content: flex-end;
    padding-bottom: 8px;
  }

  &__btn {
    padding: 8px 20px;
    font-family: inherit;
    font-size: 13.5px;
    color: $color-text;
    cursor: pointer;
    background: $color-bg-card;
    border: 1px solid $color-border;
    border-radius: $radius-md;

    &:hover {
      border-color: $color-primary;
      color: $color-primary;
    }

    &--primary {
      color: #fff;
      background: $color-primary;
      border-color: $color-primary;

      &:hover {
        color: #fff;
        background: $color-primary-dark;
        border-color: $color-primary-dark;
      }
    }
  }
}

.set {
  &__label {
    margin-bottom: 8px;
    font-size: 12.5px;
    color: $color-text-secondary;
  }

  &__row {
    display: flex;
    gap: 8px;
  }

  &__input {
    flex: 1;
    width: 100%;
    padding: 10px 13px;
    font-family: Consolas, Monaco, monospace;
    font-size: 13px;
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

  &__btn {
    flex: none;
    padding: 0 16px;
    font-family: inherit;
    font-size: 13px;
    color: $color-text-secondary;
    cursor: pointer;
    background: $color-bg-card;
    border: 1px solid $color-border;
    border-radius: $radius-md;

    &:hover {
      color: $color-primary;
      border-color: $color-primary;
    }
  }

  &__desc {
    margin-top: 9px;
    font-size: 11.5px;
    line-height: 1.7;
    color: $color-text-placeholder;

    code {
      padding: 1px 5px;
      font-family: Consolas, Monaco, monospace;
      color: $color-text-secondary;
      background: $color-bg;
      border-radius: 4px;
    }
  }

  &__result {
    display: inline-flex;
    gap: 5px;
    align-items: center;
    padding: 5px 10px;
    margin-top: 9px;
    font-size: 12px;
    color: $color-danger;
    background: rgba(239, 68, 68, 0.08);
    border-radius: $radius-sm;

    &.is-ok {
      color: #16a34a;
      background: rgba(34, 197, 94, 0.1);
    }
  }

  &__list {
    display: flex;
    flex-direction: column;
    gap: 14px;
  }

  &__divider {
    height: 1px;
    background: $color-border;
  }
}

.opt {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;

  &__main {
    flex: 1;
    min-width: 0;
  }

  // 移动端窄屏下 stepper 会被 flex 压缩导致 +/- 换行，禁止收缩与折行
  :deep(.van-stepper) {
    flex-shrink: 0;
    white-space: nowrap;
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
}
</style>

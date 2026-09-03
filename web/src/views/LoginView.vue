<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { showConfirmDialog, showSuccessToast, showToast } from 'vant'
import AppIcon from '@/components/AppIcon.vue'
import StatusTag from '@/components/StatusTag.vue'
import { countryCodes } from '@/api/auth'
import { useUserStore } from '@/stores/user'
import { maskPhone } from '@/utils/format'

const route = useRoute()
const router = useRouter()
const user = useUserStore()

const phoneCode = ref('+86')
const phoneNumber = ref('')
const code = ref('')
const password = ref('')
const showPicker = ref(false)

const phone = computed(() => `${phoneCode.value}${phoneNumber.value.trim()}`)
const phoneValid = computed(() => /^\d{6,15}$/.test(phoneNumber.value.trim()))
const codeValid = computed(() => code.value.trim().length >= 5)
const countdownText = computed(() =>
  user.codeCountdown > 0 ? `${user.codeCountdown}s 后重发` : '重新发送',
)

const pickerColumns = computed(() => countryCodes.map((c) => ({ text: c.label, value: c.value })))

watch(phoneNumber, (v) => {
  phoneNumber.value = v.replace(/\D/g, '').slice(0, 15)
})
watch(code, (v) => {
  code.value = v.replace(/\D/g, '').slice(0, 6)
})

function onPickCode({ selectedOptions }: { selectedOptions: Array<{ value?: string | number }> }) {
  phoneCode.value = String(selectedOptions[0]?.value ?? '+86')
  showPicker.value = false
}

async function onSendCode() {
  if (!phoneValid.value) {
    showToast('请输入正确的手机号')
    return
  }
  await user.requestCode(phone.value)
  showSuccessToast(`验证码已发送至 ${maskPhone(phone.value)}`)
}

async function onLogin() {
  if (!codeValid.value) {
    showToast('请输入 5-6 位验证码')
    return
  }
  if (user.needPassword && !password.value) {
    showToast('请输入两步验证密码')
    return
  }
  await user.submitLogin({ phone: phone.value, code: code.value, password: password.value })
  showSuccessToast('登录成功')
  const redirect = route.query.redirect as string | undefined
  router.push(redirect || { name: 'new' })
}

async function onLogout() {
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
  user.reset()
}

function onBackToPhone() {
  user.reset()
  code.value = ''
}
</script>

<template>
  <div class="page login">
    <!-- 已登录：展示账号信息 + 退出登录 -->
    <div v-if="user.logged" class="login__center">
      <div class="app-card account">
        <div class="account__head">
          <div class="account__avatar">{{ user.user?.initial }}</div>
          <div class="account__id">
            <div class="account__name">
              {{ user.displayName }}
              <StatusTag type="success" text="已登录" />
            </div>
            <div class="account__username">@{{ user.user?.username || '未设置用户名' }}</div>
          </div>
        </div>

        <van-cell-group :border="false" class="account__group">
          <van-cell title="手机号" :value="user.user ? maskPhone(user.user.phone) : ''" />
          <van-cell title="用户 ID" :value="String(user.user?.id ?? '-')" />
          <van-cell title="认证方式" value="tdl session（本地 .tdl）" />
          <van-cell title="登录状态">
            <template #value>
              <span class="account__online"> <span class="account__online-dot" />连接正常 </span>
            </template>
          </van-cell>
        </van-cell-group>

        <div class="account__actions">
          <van-button block plain type="primary" @click="onLogout">
            <template #icon><AppIcon name="logout" :size="15" /></template>
            退出登录
          </van-button>
        </div>

        <p class="account__tip">
          登录凭证保存在 NAS 的 <code>.tdl</code> 目录中，重启应用后无需重新验证。
        </p>
      </div>
    </div>

    <!-- 未登录：区号 + 手机号 + 验证码 -->
    <div v-else class="login__center">
      <div class="app-card login__card">
        <div class="login__head">
          <div class="login__logo"><AppIcon name="send" :size="22" /></div>
          <h2 class="login__title">登录 Telegram 账号</h2>
          <p class="login__desc">输入手机号获取验证码，登录后即可下载频道 / 群组中的媒体文件</p>
        </div>

        <div class="login__steps">
          <div class="login__step" :class="{ 'is-active': user.step === 'phone' }">
            <span class="login__step-no">1</span>输入手机号
          </div>
          <div class="login__step-line" />
          <div
            class="login__step"
            :class="{ 'is-active': user.step === 'code' || user.step === 'password' }"
          >
            <span class="login__step-no">2</span>验证码登录
          </div>
        </div>

        <!-- 第一步：手机号 -->
        <template v-if="user.step === 'phone'">
          <div class="login__field">
            <label class="login__label">手机号</label>
            <div class="login__phone">
              <div class="login__code" @click="showPicker = true">
                <span>{{ phoneCode }}</span>
                <AppIcon name="chevronDown" :size="14" />
              </div>
              <input
                v-model="phoneNumber"
                class="login__input"
                type="tel"
                inputmode="numeric"
                placeholder="请输入手机号"
                @keyup.enter="onSendCode"
              />
            </div>
          </div>

          <van-button
            block
            type="primary"
            :loading="user.loading"
            :disabled="!phoneValid"
            @click="onSendCode"
          >
            获取验证码
          </van-button>

          <p class="login__hint">
            验证码由 Telegram 官方下发，不会经由本应用转发；请留意 Telegram 内的官方通知。
          </p>
        </template>

        <!-- 第二步：验证码 -->
        <template v-else>
          <div class="login__sent">
            <AppIcon name="check" :size="14" />
            验证码已发送至
            <b>{{ maskPhone(phone) }}</b>
            <span class="login__modify" @click="onBackToPhone">修改</span>
          </div>

          <div class="login__field">
            <label class="login__label">验证码</label>
            <input
              v-model="code"
              class="login__input login__input--code"
              type="text"
              inputmode="numeric"
              placeholder="5-6 位数字"
              maxlength="6"
              @keyup.enter="onLogin"
            />
            <div class="login__resend" :class="{ 'is-disabled': user.codeCountdown > 0 }">
              <span v-if="user.codeCountdown > 0">{{ countdownText }}</span>
              <span v-else class="login__resend-btn" @click="onSendCode">重新发送</span>
            </div>
          </div>

          <div v-if="user.needPassword" class="login__field">
            <label class="login__label">两步验证密码</label>
            <input
              v-model="password"
              class="login__input"
              type="password"
              placeholder="请输入云端密码"
              @keyup.enter="onLogin"
            />
          </div>

          <van-button
            block
            type="primary"
            :loading="user.loading"
            :disabled="!codeValid"
            @click="onLogin"
          >
            登录
          </van-button>

          <p class="login__hint">
            若收不到验证码，可先在手机端 Telegram 中查看官方账号发来的登录码。
          </p>
        </template>
      </div>
    </div>

    <van-popup v-model:show="showPicker" position="bottom" round>
      <van-picker
        :columns="pickerColumns"
        :default-index="countryCodes.findIndex((c) => c.value === phoneCode)"
        @confirm="onPickCode"
        @cancel="showPicker = false"
      />
    </van-popup>
  </div>
</template>

<style scoped lang="scss">
.login {
  &__center {
    display: flex;
    justify-content: center;
    padding: 24px 0;
  }

  &__card {
    width: 100%;
    max-width: 440px;
    padding: 28px 28px 24px;
  }

  &__head {
    text-align: center;
  }

  &__logo {
    display: inline-flex;
    align-items: center;
    justify-content: center;
    width: 44px;
    height: 44px;
    color: #fff;
    background: linear-gradient(135deg, #37bcf0, #2196d9);
    border-radius: 13px;
  }

  &__title {
    margin: 14px 0 6px;
    font-size: 17px;
    font-weight: 600;
  }

  &__desc {
    margin: 0;
    font-size: 12.5px;
    line-height: 1.6;
    color: $color-text-secondary;
  }

  &__steps {
    display: flex;
    align-items: center;
    gap: 10px;
    margin: 22px 0 20px;
  }

  &__step {
    display: flex;
    align-items: center;
    gap: 6px;
    font-size: 12.5px;
    color: $color-text-placeholder;

    &.is-active {
      color: $color-primary;
      font-weight: 600;
    }
  }

  &__step-no {
    display: flex;
    align-items: center;
    justify-content: center;
    width: 18px;
    height: 18px;
    font-size: 11px;
    color: #fff;
    background: $color-text-placeholder;
    border-radius: 50%;

    .is-active & {
      background: $color-primary;
    }
  }

  &__step-line {
    flex: 1;
    height: 1px;
    background: $color-border;
  }

  &__field {
    margin-bottom: 16px;
  }

  &__label {
    display: block;
    margin-bottom: 7px;
    font-size: 12.5px;
    color: $color-text-secondary;
  }

  &__phone {
    display: flex;
    overflow: hidden;
    border: 1px solid $color-border;
    border-radius: $radius-md;
    transition: border-color 0.16s;

    &:focus-within {
      border-color: $color-primary;
    }
  }

  &__code {
    display: flex;
    flex: none;
    align-items: center;
    gap: 3px;
    padding: 0 12px;
    font-size: 13.5px;
    color: $color-text;
    cursor: pointer;
    background: $color-bg;
    border-right: 1px solid $color-border;
  }

  &__input {
    flex: 1;
    min-width: 0;
    padding: 11px 13px;
    font-family: inherit;
    font-size: 14px;
    color: $color-text;
    background: transparent;
    border: none;
    outline: none;

    &::placeholder {
      color: $color-text-placeholder;
    }

    &--code {
      width: 100%;
      font-size: 20px;
      font-weight: 600;
      letter-spacing: 10px;
      border: 1px solid $color-border;
      border-radius: $radius-md;

      &:focus {
        border-color: $color-primary;
      }
    }
  }

  &__sent {
    display: flex;
    align-items: center;
    gap: 5px;
    padding: 9px 12px;
    margin-bottom: 16px;
    font-size: 12.5px;
    color: $color-text-secondary;
    background: rgba(34, 197, 94, 0.08);
    border-radius: $radius-md;
  }

  &__modify {
    margin-left: auto;
    color: $color-primary;
    cursor: pointer;
  }

  &__resend {
    margin-top: 7px;
    font-size: 12px;
    color: $color-text-placeholder;

    &.is-disabled {
      opacity: 0.8;
    }
  }

  &__resend-btn {
    color: $color-primary;
    cursor: pointer;
  }

  &__hint {
    margin: 14px 0 0;
    font-size: 11.5px;
    line-height: 1.6;
    color: $color-text-placeholder;
  }
}

/* 已登录账号卡片 */
.account {
  width: 100%;
  max-width: 480px;
  padding: 24px;

  &__head {
    display: flex;
    gap: 14px;
    align-items: center;
    padding-bottom: 18px;
    border-bottom: 1px solid $color-border;
  }

  &__avatar {
    display: flex;
    flex: none;
    align-items: center;
    justify-content: center;
    width: 52px;
    height: 52px;
    font-size: 20px;
    font-weight: 600;
    color: #fff;
    background: linear-gradient(135deg, #37bcf0, #1c86cf);
    border-radius: 50%;
  }

  &__id {
    min-width: 0;
  }

  &__name {
    display: flex;
    align-items: center;
    gap: 8px;
    font-size: 16px;
    font-weight: 600;
  }

  &__username {
    margin-top: 2px;
    font-size: 12.5px;
    color: $color-text-placeholder;
  }

  &__group {
    margin: 6px -24px 18px;
  }

  &__online {
    display: inline-flex;
    align-items: center;
    gap: 5px;
    color: $color-success;
  }

  &__online-dot {
    width: 6px;
    height: 6px;
    background: $color-success;
    border-radius: 50%;
  }

  &__actions {
    margin-top: 4px;
  }

  &__tip {
    margin: 14px 0 0;
    font-size: 11.5px;
    line-height: 1.6;
    color: $color-text-placeholder;

    code {
      padding: 1px 5px;
      font-family: Consolas, Monaco, monospace;
      background: $color-bg;
      border-radius: 4px;
    }
  }
}
</style>

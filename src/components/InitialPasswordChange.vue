<script setup lang="ts">
import { onBeforeUnmount, reactive, ref, watch } from 'vue'
import { api } from '../api'
import { initialPasswordError } from '../initialPassword'
import { t } from '../i18n'
import LocaleSwitcher from './LocaleSwitcher.vue'

const props = defineProps<{ user: { id: string; name: string }; blocked?: boolean; blockReason?: string; impersonated?: boolean; returning?: boolean }>()
const emit = defineEmits<{ completed: []; logout: []; refresh: []; returnAdministrator: [] }>()
const form = reactive({ currentPassword: '', newPassword: '', confirmPassword: '' })
const submitting = ref(false), error = ref('')
let disposed = false, controller: AbortController | undefined

// 此组件替代整个业务外壳，不提供关闭/跳过入口；服务端仍独立拦截所有业务请求。
// 密码只留在当前表单内存，提交成功或组件销毁后清除，不能加入邮箱记忆或草稿缓存。
function clearPasswords() { form.currentPassword = ''; form.newPassword = ''; form.confirmPassword = '' }
async function submit() {
  if (submitting.value || disposed || props.blocked || props.impersonated) return
  error.value = initialPasswordError(form.currentPassword, form.newPassword, form.confirmPassword)
  if (error.value) return
  const userID = props.user.id
  submitting.value = true
  controller = new AbortController()
  try {
    const data = await api<{ changed: boolean; requiresLogin: boolean }>('/auth/initial-password', { method: 'POST', body: JSON.stringify(form), signal: controller.signal })
    if (disposed || props.blocked || props.impersonated || props.user.id !== userID) return
    if (data.changed !== true || data.requiresLogin !== true) throw new Error(t('改密结果未确认，请重新登录核实'))
    clearPasswords()
    emit('completed')
  } catch (cause) {
    if (!disposed && !props.blocked && props.user.id === userID) error.value = cause instanceof Error ? cause.message : '修改密码失败，请稍后重试'
  } finally {
    if (!disposed) submitting.value = false
  }
}
// 标签页身份冲突时清空口令并冻结表单，不将旧账号的改密输入用于新账号。
watch(() => [props.user.id, props.blocked, props.impersonated], () => { controller?.abort(); clearPasswords(); error.value = '' }, { flush: 'sync' })
onBeforeUnmount(() => { disposed = true; controller?.abort(); clearPasswords() })
</script>

<template>
  <main class="initial-password-page">
    <div class="initial-password-language"><LocaleSwitcher /></div>
    <section class="initial-password-card" aria-labelledby="initial-password-title">
      <span class="initial-password-mark" aria-hidden="true">D</span>
      <p class="initial-password-eyebrow">TaskLoom · {{ t('账号安全') }}</p>
      <h1 id="initial-password-title">{{ t('首次登录，请修改密码') }}</h1>
      <p class="initial-password-intro">{{ user.name }}，{{ t('当前使用临时密码。设置个人密码后才能进入工作台。') }}</p>
      <div v-if="blocked" role="alert"><p>{{ blockReason }}</p><button type="button" class="btn" @click="emit('refresh')">{{ t('刷新') }}</button></div>
      <div v-else-if="impersonated" role="status"><p class="initial-password-intro">{{ t('该成员尚未完成首次改密。管理员不能代为设置个人密码，请返回管理员账号。') }}</p><p v-if="blockReason" role="alert" class="initial-password-error">{{ blockReason }}</p><button type="button" class="btn primary" :disabled="returning" @click="emit('returnAdministrator')">{{ t('返回管理员') }}</button></div>
      <form v-else @submit.prevent="submit">
        <label for="initial-current">{{ t('当前临时密码') }}<input id="initial-current" v-model="form.currentPassword" type="password" name="current-password" autocomplete="current-password" maxlength="72" :disabled="submitting" required autofocus></label>
        <label for="initial-new">{{ t('新密码') }}<input id="initial-new" v-model="form.newPassword" type="password" name="new-password" autocomplete="new-password" minlength="8" maxlength="72" :disabled="submitting" aria-describedby="initial-password-policy" required></label>
        <p id="initial-password-policy" class="initial-password-policy">{{ t('至少 8 个字符，同时包含字母和数字；最多 72 个 UTF-8 字节。') }}</p>
        <label for="initial-confirm">{{ t('确认新密码') }}<input id="initial-confirm" v-model="form.confirmPassword" type="password" name="confirm-password" autocomplete="new-password" minlength="8" maxlength="72" :disabled="submitting" required></label>
        <p v-if="error" class="initial-password-error" role="alert">{{ t(error) }}</p>
        <button type="submit" class="btn primary" :disabled="submitting">{{ t(submitting ? '正在修改密码…' : '确认修改并重新登录') }}</button>
      </form>
      <p class="initial-password-note">{{ t('修改后所有旧登录会话将失效，请使用新密码重新登录。') }}</p>
      <button type="button" class="initial-password-logout" :disabled="submitting" @click="emit('logout')">{{ t('退出登录') }}</button>
    </section>
  </main>
</template>

<style scoped>
.initial-password-page{height:100dvh;overflow:auto;box-sizing:border-box;padding:80px 20px 32px;display:grid;place-items:start center;background:var(--page-bg,#f5f7fb);color:var(--ink,#243247)}.initial-password-language{position:absolute;right:24px;top:20px}.initial-password-card{width:min(100%,460px);box-sizing:border-box;background:var(--surface,#fff);border:1px solid var(--line,#dce2ec);border-radius:16px;padding:32px;box-shadow:0 12px 40px #1018280a}.initial-password-mark{display:grid;place-items:center;width:42px;height:42px;border-radius:12px;background:var(--primary,#3370eb);color:#fff;font-size:24px;font-weight:700}.initial-password-eyebrow{font-size:12px;color:var(--muted);margin:18px 0 8px}.initial-password-card h1{font-size:24px;line-height:1.45;margin:0 0 12px}.initial-password-intro,.initial-password-note,.initial-password-policy{font-size:12px;line-height:1.8;color:var(--muted);overflow-wrap:anywhere}.initial-password-intro{margin-bottom:24px}.initial-password-card form{display:grid;gap:16px}.initial-password-card label{display:grid;gap:8px;font-size:13px}.initial-password-card input{box-sizing:border-box;width:100%;min-height:44px;padding:10px 12px;border:1px solid var(--line,#dce2ec);border-radius:8px;background:var(--surface,#fff);color:var(--ink,#243247);font:inherit}.initial-password-card input:focus-visible{outline:2px solid var(--primary,#3370eb);outline-offset:2px}.initial-password-policy{margin:-8px 0 0}.initial-password-card .btn{min-height:44px;justify-content:center;width:100%;font:inherit}.initial-password-error{margin:0;padding:10px 12px;border-radius:8px;background:color-mix(in srgb,var(--danger,#c24145) 10%,var(--surface,#fff));color:var(--danger,#c24145);font-size:12px;line-height:1.6}.initial-password-note{margin:18px 0 6px}.initial-password-logout{border:0;background:transparent;color:var(--primary,#3370eb);padding:8px 0;min-height:40px;font:inherit;font-size:12px;cursor:pointer}.initial-password-card button:disabled{opacity:.6;cursor:wait}@media(max-width:600px){.initial-password-page{padding:70px 12px 24px}.initial-password-card{padding:24px 20px}.initial-password-card h1{font-size:21px}.initial-password-card input{font-size:16px}}
</style>

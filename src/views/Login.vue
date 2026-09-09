<script setup lang="ts">
import { nextTick, reactive, ref } from 'vue'
import { api } from '../api'
import { t } from '../i18n'
import LocaleSwitcher from '../components/LocaleSwitcher.vue'
import WechatLoginButton from '../components/WechatLoginButton.vue'
import { clearLoginEmail, readLoginEmail, saveLoginEmail } from '../loginIdentity'

defineProps<{ notice?: string }>()
const emit = defineEmits<{ authenticated: [] }>()
function storage(){try{return typeof localStorage==='undefined'?undefined:localStorage}catch{return undefined}}
const lastEmail = ref(readLoginEmail(storage()))
const form = reactive({ email: lastEmail.value, password: '' })
const passwordInput = ref<HTMLInputElement|null>(null)
const submitting = ref(false)
const error = ref('')
async function useLastEmail(){if(submitting.value||!lastEmail.value)return;form.email=lastEmail.value;error.value='';await nextTick();passwordInput.value?.focus()}
function forgetEmail(){if(submitting.value)return;clearLoginEmail(storage());if(form.email===lastEmail.value)form.email='';lastEmail.value=''}

async function login() {
  if(submitting.value)return
  submitting.value = true
  error.value = ''
  try {
    await api('/auth/login', { method: 'POST', body: JSON.stringify(form) })
    lastEmail.value = saveLoginEmail(storage(), form.email)
    form.password = ''
    emit('authenticated')
  } catch (cause: any) {
    error.value = cause.message || t('登录失败，请检查邮箱和密码')
  } finally {
    submitting.value = false
  }
}
</script>

<template>
  <main class="login-page">
    <div class="login-language"><LocaleSwitcher /></div>
    <section class="login-brand">
      <span class="brand-mark">D</span>
      <div><strong>TaskLoom</strong><small>{{ t('研发管理平台 · V3') }}</small></div>
    </section>
    <form class="login-card" @submit.prevent="login">
      <header><span>{{ t('星河示例企业') }}</span><h1>{{ t('登录 TaskLoom') }}</h1><p>{{ t('使用企业目录中的账号继续进入研发工作台。') }}</p></header>
      <div v-if="lastEmail" class="login-recent" :aria-label="t('上次登录的邮箱')"><span>{{ t('上次登录') }}</span><button type="button" class="login-email-chip" :disabled="submitting" :title="t('快速填入邮箱')" @click="useLastEmail">{{ lastEmail }}</button><button type="button" class="login-forget" :disabled="submitting" :aria-label="t('清除邮箱记忆')" @click="forgetEmail">×</button></div>
      <label for="login-email"><span>{{ t('账号或邮箱') }}</span><input id="login-email" v-model.trim="form.email" name="username" type="text" autocomplete="username" maxlength="254" :disabled="submitting" required autofocus></label>
      <label for="login-password"><span>{{ t('密码') }}</span><input id="login-password" ref="passwordInput" v-model="form.password" name="password" type="password" autocomplete="current-password" maxlength="72" :disabled="submitting" required></label>
      <p v-if="notice" class="login-stay" role="status">{{ t(notice) }}</p>
      <p v-if="error" class="login-error" role="alert">{{ error }}</p>
      <button class="btn primary" :disabled="submitting">{{ t(submitting ? '正在登录…' : '登录') }}</button>
      <WechatLoginButton />
      <footer>{{ t('会话凭证仅保存在由服务端签名的 HTTP-only Cookie 中。') }}</footer>
      <p class="login-stay">{{t('默认保持登录 30 天；退出或修改密码后会话失效。')}}</p>
      <p class="login-slogan">{{t('努力只能及格，拼命Vibe才能优秀！')}}</p>
    </form>
  </main>
</template>
<style scoped>.login-language{position:absolute;top:24px;right:28px}.login-stay{font-size:11px;line-height:1.7;color:var(--muted);text-align:center}.login-slogan{font-size:12px;text-align:center;color:var(--primary);margin:20px 0 0}</style>
<style scoped>
.login-recent{display:flex;align-items:center;gap:8px;min-width:0;padding:9px 10px;margin-bottom:16px;background:var(--surface-subtle,#f5f7fb);border:1px solid var(--line,#dce0e8);border-radius:8px}.login-recent>span{font-size:11px;color:var(--muted);flex:none}.login-recent button{border:0;background:transparent;color:var(--primary,#3370eb);cursor:pointer}.login-email-chip{flex:1;min-width:0;overflow:hidden;text-overflow:ellipsis;text-align:left;font-size:12px;padding:5px 0}.login-forget{flex:none;padding:4px;font-size:18px;line-height:1}.login-recent button:focus-visible{outline:2px solid var(--primary);outline-offset:2px;border-radius:3px}@media(max-width:700px){.login-recent{flex-wrap:wrap}.login-recent>span{flex-basis:100%}.login-recent button{min-height:40px}}
</style>

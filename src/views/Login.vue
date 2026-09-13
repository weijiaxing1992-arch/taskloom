<script setup lang="ts">
import { nextTick, reactive, ref, watch } from 'vue'
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
type LoginChallenge={id:string;image:string;expiresAt:number}
const challenge=ref<LoginChallenge|null>(null),challengeAnswer=ref('')
function parseChallenge(value:unknown):LoginChallenge|null {
  if(!value||typeof value!=='object')return null
  const item=value as Record<string,unknown>
  if(typeof item.id!=='string'||!/^[A-Za-z0-9_-]{32}$/.test(item.id)||typeof item.image!=='string'||item.image.length>20000||!/^data:image\/png;base64,[A-Za-z0-9+/]+=*$/.test(item.image)||typeof item.expiresAt!=='number'||!Number.isSafeInteger(item.expiresAt)||item.expiresAt<=Date.now()/1000||item.expiresAt>Date.now()/1000+180)return null
  return{id:item.id,image:item.image,expiresAt:item.expiresAt}
}
watch(()=>form.email.trim().toLowerCase(),()=>{challenge.value=null;challengeAnswer.value=''})
async function useLastEmail(){if(submitting.value||!lastEmail.value)return;form.email=lastEmail.value;error.value='';await nextTick();passwordInput.value?.focus()}
function forgetEmail(){if(submitting.value)return;clearLoginEmail(storage());if(form.email===lastEmail.value)form.email='';lastEmail.value=''}

async function login(refreshChallenge=false) {
  if(submitting.value)return
  if(challenge.value&&!refreshChallenge){
    if(challenge.value.expiresAt<=Date.now()/1000){error.value=t('验证码已过期，请换一张后重试');return}
    if(!challengeAnswer.value.trim()){error.value=t('请输入图片中的验证码');return}
  }
  submitting.value = true
  error.value = ''
  try {
    await api('/auth/login', { method: 'POST', body: JSON.stringify({email:form.email,password:form.password,...(challenge.value?{challengeId:challenge.value.id,challengeAnswer:challengeAnswer.value}:{}),...(refreshChallenge?{refreshChallenge:true}:{})}) })
    lastEmail.value = saveLoginEmail(storage(), form.email)
    form.password = ''
    challenge.value=null;challengeAnswer.value=''
    emit('authenticated')
  } catch (cause: any) {
    error.value = cause.message || t('登录失败，请检查邮箱和密码')
    if(cause.code==='login_challenge_required'){
      challenge.value=parseChallenge(cause.details?.challenge);challengeAnswer.value=''
      if(!challenge.value)error.value=t('安全验证暂时不可用，请稍后重试')
    }else if(cause.status&&cause.status!==429){challenge.value=null;challengeAnswer.value=''}
  } finally {
    submitting.value = false
  }
}
</script>

<template>
  <main class="login-page">
    <div class="login-language"><LocaleSwitcher /></div>
    <section class="login-brand">
      <span class="brand-mark" aria-hidden="true">D</span>
      <div><strong>TaskLoom</strong><small>{{ t('研发管理平台 · V3') }}</small></div>
    </section>
    <form class="login-card" @submit.prevent="login()">
      <header><span>{{ t('星河示例企业') }}</span><h1>{{ t('登录 TaskLoom') }}</h1><p>{{ t('使用企业目录中的账号继续进入研发工作台。') }}</p></header>
      <div v-if="lastEmail" class="login-recent" :aria-label="t('上次登录的邮箱')"><span>{{ t('上次登录') }}</span><button type="button" class="login-email-chip" :disabled="submitting" :title="t('快速填入邮箱')" @click="useLastEmail">{{ lastEmail }}</button><button type="button" class="login-forget" :disabled="submitting" :aria-label="t('清除邮箱记忆')" @click="forgetEmail">×</button></div>
      <label for="login-email"><span>{{ t('账号或邮箱') }}</span><input id="login-email" v-model.trim="form.email" name="username" type="text" autocomplete="username" maxlength="254" :disabled="submitting" required autofocus></label>
      <label for="login-password"><span>{{ t('密码') }}</span><input id="login-password" ref="passwordInput" v-model="form.password" name="password" type="password" autocomplete="current-password" maxlength="72" :disabled="submitting" required></label>
      <section v-if="challenge" class="login-challenge" :aria-label="t('登录安全验证')">
        <div><img :src="challenge.image" :alt="t('登录安全验证码')" width="190" height="52"><button type="button" :disabled="submitting" @click="login(true)">{{t('换一张')}}</button></div>
        <label for="login-challenge-answer"><span>{{t('图片验证码')}}</span><input id="login-challenge-answer" v-model.trim="challengeAnswer" name="devflow-login-challenge" type="text" autocomplete="off" autocapitalize="characters" spellcheck="false" maxlength="6" :disabled="submitting" aria-describedby="login-challenge-help" required></label>
        <small id="login-challenge-help">{{t('验证码 2 分钟内有效，不区分大小写。需要无障碍协助请联系管理员，或使用已启用的微信登录。')}}</small>
      </section>
      <p v-if="notice" class="login-stay" role="status">{{ t(notice) }}</p>
      <p v-if="error" class="login-error" role="alert">{{ error }}</p>
      <button class="btn primary" :disabled="submitting">{{ t(submitting ? '正在登录…' : '登录') }}</button>
      <WechatLoginButton />
    </form>
  </main>
</template>
<style scoped>.login-language{position:absolute;top:24px;right:28px}.login-stay{font-size:12px;line-height:1.5;color:var(--muted);text-align:center}.login-card{border-radius:8px;box-shadow:none;padding:28px}.login-card header p{margin-bottom:18px}.login-card input{border-radius:5px}.login-card input:focus{border-color:var(--primary,#3370eb);box-shadow:none}.login-card input:focus-visible{outline:2px solid var(--primary,#3370eb);outline-offset:1px;box-shadow:none}.login-brand .brand-mark{overflow:hidden;background:#507bfd url('../assets/taskloom-mark.svg') center/cover no-repeat;box-shadow:none;color:transparent;font-size:0}.login-page{gap:20px}</style>
<style scoped>
.login-recent{display:flex;align-items:center;gap:8px;min-width:0;padding:9px 10px;margin-bottom:16px;background:var(--surface-subtle,#f5f7fb);border:1px solid var(--line,#dce0e8);border-radius:8px}.login-recent>span{font-size:11px;color:var(--muted);flex:none}.login-recent button{border:0;background:transparent;color:var(--primary,#3370eb);cursor:pointer}.login-email-chip{flex:1;min-width:0;overflow:hidden;text-overflow:ellipsis;text-align:left;font-size:12px;padding:5px 0}.login-forget{flex:none;padding:4px;font-size:18px;line-height:1}.login-recent button:focus-visible{outline:2px solid var(--primary);outline-offset:2px;border-radius:3px}@media(max-width:700px){.login-recent{flex-wrap:wrap}.login-recent>span{flex-basis:100%}.login-recent button{min-height:40px}}
</style>
<style scoped>
.login-challenge{margin:0 0 14px;padding:10px;border:1px solid var(--line,#dce0e8);border-radius:5px;background:var(--surface-subtle,#f5f7fb)}.login-challenge>div{display:flex;align-items:center;gap:10px;flex-wrap:wrap;margin-bottom:8px}.login-challenge img{max-width:100%;height:auto;border-radius:3px}.login-challenge button{border:0;background:transparent;color:var(--primary,#3370eb);min-height:32px;padding:4px}.login-challenge label{margin-bottom:6px}.login-challenge small{display:block;color:var(--muted);font-size:11px;line-height:1.5}.login-challenge button:focus-visible{outline:2px solid var(--primary,#3370eb);outline-offset:2px}@media(max-width:700px){.login-challenge button{min-height:44px}.login-challenge input{font-size:16px;min-height:44px}}
</style>

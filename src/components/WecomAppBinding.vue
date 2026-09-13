<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'
import { api } from '../api'
import { t } from '../i18n'

type Binding = { bound:boolean; boundAt:string; requiresRebind:boolean; enabled:boolean; live:boolean; providerMode:string }

const resultMessages:Record<string,string> = {
  bound:'企业微信绑定成功，后续符合条件的通知可由企业微信自建应用投递。',
  expired:'授权已过期或账号状态发生变化，请重新开始绑定。',
  failed:'企业微信授权未完成，请重试；仍失败请联系管理员检查应用配置。',
  conflict:'该企业微信账号已绑定其他系统账号，请先解绑后重试。',
  mock:'当前服务器处于模拟模式，不能完成真实企业微信绑定。',
  not_connected:'授权状态不完整，请重新从个人设置发起绑定。',
}

const status=ref<Binding|null>(null), password=ref(''), busy=ref(false), error=ref('')
const notice=ref(resultMessages[new URLSearchParams(window.location.search).get('wecom')||'']||'')
let disposed=false

function safeWecomAuthorization(value:unknown):string {
  if(typeof value!=='string') throw Error('企业微信授权地址无效')
  const target=new URL(value)
  if(target.protocol!=='https:'||target.host!=='open.weixin.qq.com'||target.pathname!=='/connect/oauth2/authorize'||target.username||target.password||
    target.searchParams.get('scope')!=='snsapi_base'||target.searchParams.get('response_type')!=='code'||
    !/^ww[A-Za-z0-9_-]{6,62}$/.test(target.searchParams.get('appid')||'')||! /^[a-f0-9]{64}$/.test(target.searchParams.get('state')||'')) throw Error('企业微信授权地址无效')
  const callback=new URL(target.searchParams.get('redirect_uri')||'')
  if(callback.origin!==window.location.origin||callback.pathname!=='/api/auth/wecom/callback'||callback.search||callback.hash||callback.username||callback.password) throw Error('请从管理员配置的正式域名发起企业微信绑定')
  return target.href
}

async function load(){
  try { const value=await api<Binding>('/profile/wecom-app'); if(!disposed) status.value=value }
  catch(cause){ if(!disposed) error.value=cause instanceof Error?cause.message:t('企业微信绑定状态加载失败') }
}
onMounted(load)
onBeforeUnmount(()=>{disposed=true;password.value=''})
defineExpose({canLeave:()=>!busy.value})

async function act(){
  if(busy.value||!status.value||!password.value)return
  const unbind=status.value.bound
  if(unbind&&!window.confirm(t('解绑后将停止企业微信通知投递。确认解绑？')))return
  busy.value=true;error.value='';notice.value=''
  try{
    const response=await api<{url?:string}>('/profile/wecom-app/'+(unbind?'unbind':'bind'),{method:'POST',body:JSON.stringify({password:password.value})})
    password.value=''
    if(disposed)return
    if(unbind){notice.value='已解绑企业微信，站内通知不受影响。';await load()}
    else window.location.assign(safeWecomAuthorization(response.url))
  }catch(cause){if(!disposed)error.value=cause instanceof Error?t(cause.message):t('企业微信绑定操作失败')}
  finally{password.value='';if(!disposed)busy.value=false}
}
</script>

<template>
  <section class="wecom-app-binding">
    <header><h2>{{t('企业微信账号绑定')}}</h2><p>{{t('仅使用企业微信官方授权返回的成员 ID 绑定账号，不会按姓名、邮箱或昵称推测身份。')}}</p></header>
    <p v-if="error" role="alert">{{error}}</p><p v-if="notice" role="status">{{t(notice)}}</p>
    <template v-if="status">
      <div class="binding-state"><strong>{{t(status.bound?'已绑定企业微信':'尚未绑定企业微信')}}</strong><small v-if="status.bound">{{t('绑定时间')}} · {{new Date(status.boundAt).toLocaleString()}}</small><small v-else-if="status.requiresRebind">{{t('历史绑定不能安全用于投递，请重新完成官方授权。')}}</small></div>
      <p v-if="!status.enabled">{{t('管理员尚未完成并启用企业微信自建应用。')}}</p>
      <p v-else-if="!status.live">{{t('服务器当前处于模拟模式：可以查看配置，但不会跳转或真实投递。')}}</p>
      <form @submit.prevent="act">
        <label for="wecom-app-current-password">{{t('确认当前账号密码')}}<input id="wecom-app-current-password" v-model="password" type="password" autocomplete="current-password" maxlength="72" required :disabled="busy||(!status.bound&&(!status.enabled||!status.live))"/></label>
        <button type="submit" class="btn" :class="status.bound?'danger-outline':'primary'" :disabled="busy||!password||(!status.bound&&(!status.enabled||!status.live))">{{t(busy?'处理中…':status.bound?'解绑企业微信':'绑定企业微信（官方授权）')}}</button>
      </form>
      <p class="binding-help">{{t('绑定仅用于企业微信自建应用通知。站内通知始终可用；外部投递由管理员的通知开关、成员绑定和服务器 live 模式共同控制。')}}</p>
    </template>
  </section>
</template>

<style scoped>
.wecom-app-binding{width:100%;max-width:680px;min-width:0;box-sizing:border-box}.wecom-app-binding header{display:block;min-width:0}.wecom-app-binding h2{margin:0 0 12px;font-size:17px;line-height:1.5}.wecom-app-binding p{line-height:1.75;overflow-wrap:anywhere}.binding-state{display:grid;gap:8px;padding:16px;border:1px solid var(--border);border-radius:8px;background:var(--secondary);margin:18px 0;min-width:0;overflow-wrap:anywhere}.binding-state small,.binding-help{color:var(--muted-foreground)}.wecom-app-binding form{display:grid;gap:16px;width:100%;max-width:440px;min-width:0}.wecom-app-binding label{display:grid;gap:8px;min-width:0}.wecom-app-binding input{box-sizing:border-box;width:100%;min-width:0;max-width:100%;border:1px solid var(--border);border-radius:6px;padding:8px 10px;background:var(--background);color:var(--foreground);font:inherit}.wecom-app-binding button{justify-self:start;max-width:100%;white-space:normal;overflow-wrap:anywhere}.wecom-app-binding [role=alert]{color:var(--destructive)}
</style>

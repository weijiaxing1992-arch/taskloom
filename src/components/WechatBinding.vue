<script setup lang="ts">
import { onMounted, onBeforeUnmount, ref } from 'vue'
import { api } from '../api'
import { t } from '../i18n'
import { safeWechatAuthorization,wechatResultMessages } from '../wechatLogin'
type Binding={bound:boolean;boundAt:string;enabled:boolean;origin:string}
const status=ref<Binding|null>(null),password=ref(''),busy=ref(false),error=ref(''),notice=ref(wechatResultMessages[new URLSearchParams(window.location.search).get('wechat')||'']||'')
let disposed=false
async function load(){try{const value=await api<Binding>('/profile/wechat');if(!disposed)status.value=value}catch(cause){if(!disposed)error.value=cause instanceof Error?cause.message:t('微信绑定状态加载失败')}}
onMounted(load)
onBeforeUnmount(()=>{disposed=true;password.value=''})
defineExpose({canLeave:()=>!busy.value})
async function act(){
 if(busy.value||!status.value||!password.value)return
 const unbind=status.value.bound
 if(unbind&&!window.confirm(t('解绑后微信将不能登录，其他浏览器会话会退出。确认解绑？')))return
 busy.value=true;error.value='';notice.value=''
 try{
   const response=await api<{url?:string}>('/profile/wechat/'+(unbind?'unbind':'bind'),{method:'POST',body:JSON.stringify({password:password.value})})
   password.value=''
   if(disposed)return
   if(unbind){notice.value='已解绑微信，可以继续使用邮箱密码登录。';await load()}
   else window.location.assign(safeWechatAuthorization(response.url))
 }catch(cause){if(!disposed)error.value=cause instanceof Error?t(cause.message):t('微信绑定操作失败')}
 finally{password.value='';if(!disposed)busy.value=false}
}
</script>
<template>
 <section class="wechat-binding">
  <header><h2>{{t('微信绑定与登录')}}</h2><p>{{t('绑定现有账号后，可使用微信扫码登录；不会按微信昵称创建或合并人员。')}}</p></header>
  <p v-if="error" role="alert">{{error}}</p><p v-if="notice" role="status">{{t(notice)}}</p>
  <template v-if="status">
   <div class="binding-state"><strong>{{t(status.bound?'已绑定微信':'尚未绑定微信')}}</strong><small v-if="status.bound">{{t('绑定时间')}} · {{new Date(status.boundAt).toLocaleString()}}</small></div>
   <p v-if="!status.enabled">{{t('管理员尚未启用微信扫码登录')}}</p>
   <form @submit.prevent="act">
    <label for="wechat-current-password">{{t('确认当前账号密码')}}<input id="wechat-current-password" v-model="password" type="password" autocomplete="current-password" maxlength="72" required :disabled="busy||(!status.bound&&!status.enabled)"/></label>
    <button type="submit" class="btn" :class="status.bound?'danger-outline':'primary'" :disabled="busy||!password||(!status.bound&&!status.enabled)">{{t(busy?'处理中…':status.bound?'解绑微信':'绑定微信（扫码）')}}</button>
   </form>
   <p class="binding-help">{{t('微信登录与企微机器人通知相互独立。请保留可用密码，以便解绑、换绑或微信服务不可用时登录。')}}</p>
  </template>
 </section>
</template>
<style scoped>
.wechat-binding{max-width:680px;min-width:0}.wechat-binding h2{margin:0 0 12px}.wechat-binding p{line-height:1.75;overflow-wrap:anywhere}.binding-state{display:grid;gap:8px;padding:16px;border:1px solid var(--border);border-radius:8px;background:var(--secondary);margin:18px 0}.binding-state small,.binding-help{color:var(--muted-foreground)}.wechat-binding form{display:grid;gap:16px;max-width:440px}.wechat-binding label{display:grid;gap:8px}.wechat-binding input{width:100%;min-width:0}.wechat-binding button{justify-self:start}.wechat-binding [role=alert]{color:var(--destructive)}
</style>

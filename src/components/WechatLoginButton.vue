<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { api } from '../api'
import { t } from '../i18n'
import { safeWechatAuthorization, wechatResultMessages } from '../wechatLogin'
const enabled=ref(false),loading=ref(true),busy=ref(false),error=ref('')
const result=wechatResultMessages[new URLSearchParams(window.location.search).get('wechat')||'']||''
onMounted(async()=>{try{const data=await api<{enabled:boolean}>('/auth/wechat/status');enabled.value=data.enabled}catch{error.value=t('微信登录状态加载失败，请刷新重试')}finally{loading.value=false}})
async function start(){
  if(!enabled.value||busy.value)return
  busy.value=true;error.value=''
  try{const result=await api<{url:string}>('/auth/wechat/start',{method:'POST',body:'{}'});window.location.assign(safeWechatAuthorization(result.url))}
  catch(cause){error.value=cause instanceof Error?t(cause.message):t('微信扫码启动失败');busy.value=false}
}
</script>
<template>
  <section class="wechat-login-choice" :aria-label="t('微信扫码登录')">
    <p v-if="result" role="status">{{t(result)}}</p>
    <button v-if="enabled" type="button" class="btn wechat-login-action" :disabled="loading||busy||!enabled" @click="start">{{t(busy?'正在打开微信…':'微信扫码登录')}}</button>
    <small v-else-if="!error" class="wechat-login-unavailable" role="status">{{t(loading?'正在检查微信登录…':'微信登录未启用')}}</small>
    <p v-if="error" role="alert">{{error}}</p>
  </section>
</template>
<style scoped>
.wechat-login-choice{display:grid;gap:6px;margin-top:12px;padding-top:12px;border-top:1px solid var(--border);min-width:0}.wechat-login-choice p{margin:0;font-size:12px;line-height:1.5;overflow-wrap:anywhere}.wechat-login-unavailable{color:var(--muted-foreground);font-size:11px;line-height:1.5;text-align:center}.wechat-login-action{width:100%;min-height:34px;border:1px solid var(--border);border-radius:5px;background:var(--card);color:var(--primary,#3370eb);box-shadow:none}.wechat-login-action:hover:not(:disabled){background:var(--accent)}.wechat-login-action:focus-visible{outline:2px solid var(--primary,#3370eb);outline-offset:2px}.wechat-login-choice [role=alert]{color:var(--destructive)}
</style>

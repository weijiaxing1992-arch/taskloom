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
    <button type="button" class="btn wechat-login-action" :disabled="loading||busy||!enabled" @click="start">{{t(busy?'正在打开微信…':'微信扫码登录')}}</button>
    <small>{{t(loading?'正在检查微信登录配置…':enabled?'将打开微信官方扫码页；扫码确认后自动返回。':'管理员尚未启用微信扫码登录')}}</small>
    <p v-if="error" role="alert">{{error}}</p>
  </section>
</template>
<style scoped>
.wechat-login-choice{display:grid;gap:8px;margin-top:20px;padding-top:18px;border-top:1px solid var(--border);min-width:0}.wechat-login-choice p{margin:0;font-size:13px;line-height:1.7;overflow-wrap:anywhere}.wechat-login-choice small{color:var(--muted-foreground);line-height:1.6}.wechat-login-action{width:100%}.wechat-login-choice [role=alert]{color:var(--destructive)}
</style>

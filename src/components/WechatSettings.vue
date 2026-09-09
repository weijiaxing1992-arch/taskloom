<script setup lang="ts">
import { computed,onMounted,onBeforeUnmount,reactive,ref } from 'vue'
import { onBeforeRouteLeave, onBeforeRouteUpdate } from 'vue-router'
import { t } from '../i18n'
import { useSettingsScope } from './settingsScope'
type Settings={appId:string;origin:string;callbackUrl:string;enabled:boolean;secretConfigured:boolean;version:number}
const scope=useSettingsScope(),settings=ref<Settings|null>(null),busy=ref(false),error=ref(''),notice=ref('')
const form=reactive({appId:'',origin:'https://taskloom.example.com',secret:'',clearSecret:false,enabled:false,version:0})
let disposed=false
const dirty=computed(()=>!!form.secret||form.clearSecret||!!settings.value&&(form.appId!==settings.value.appId||form.origin!==settings.value.origin||form.enabled!==settings.value.enabled))
async function load(){busy.value=true;error.value='';try{const data=await scope.request<Settings>('/organization/wechat-login');if(!disposed){settings.value=data;Object.assign(form,{...data,secret:'',clearSecret:false})}}catch(cause){if(!disposed)error.value=cause instanceof Error?t(cause.message):t('微信配置读取失败')}finally{if(!disposed)busy.value=false}}
onMounted(load);onBeforeUnmount(()=>{disposed=true;form.secret=''})
function canLeave(){return !busy.value&&(!dirty.value||window.confirm(t('微信配置尚未保存，确认离开？')))}
onBeforeRouteLeave(canLeave);onBeforeRouteUpdate(canLeave)
function reload(){if(!dirty.value||window.confirm(t('丢弃未保存的微信配置？')))void load()}
async function save(){
 if(busy.value||scope.locked.value)return
 busy.value=true;error.value='';notice.value=''
 try{
  const data=await scope.request<Settings>('/organization/wechat-login',{method:'PATCH',body:JSON.stringify({appId:form.appId,origin:form.origin,secret:form.secret,clearSecret:form.clearSecret,enabled:form.enabled,version:form.version})})
  if(!disposed){settings.value=data;Object.assign(form,{...data,secret:'',clearSecret:false});notice.value='配置已保存，请用已绑定账号进行真实扫码验收。'}
 }catch(cause){if(!disposed)error.value=cause instanceof Error?t(cause.message):t('微信配置保存失败')}
 finally{form.secret='';if(!disposed)busy.value=false}
}
</script>
<template>
 <section class="wechat-settings">
  <p>{{t('配置微信开放平台“网站应用”登录服务，不是公众号网页授权、小程序或企业微信机器人。')}}</p>
  <ol><li>{{t('在微信开放平台申请网站应用并开通微信登录，取得 AppID 与 AppSecret。')}}</li><li>{{t('授权回调域填写正式站点域名，完整回调地址见下方。站点必须可通过 HTTPS 访问。')}}</li><li>{{t('保存并启用后，成员先用密码登录，在个人设置中绑定微信，再验收扫码登录。')}}</li></ol>
  <p v-if="scope.locked.value" role="alert">{{t('项目或账号已变化，请刷新页面后继续')}}</p>
  <p v-if="error" role="alert">{{error}}</p><p v-if="notice" role="status">{{t(notice)}}</p>
  <form v-if="settings" @submit.prevent="save">
   <fieldset :disabled="busy||scope.locked.value">
    <label>{{t('网站应用 AppID')}}<input v-model.trim="form.appId" maxlength="18" placeholder="wx…" required autocomplete="off"/></label>
    <label>{{t('正式站点 HTTPS 域名')}}<input v-model.trim="form.origin" type="url" maxlength="250" placeholder="https://taskloom.example.com" required/></label>
    <label>{{t('完整授权回调地址')}}<code>{{form.origin.replace(/\/$/,'')}}/api/auth/wechat/callback</code></label>
    <label>AppSecret <input v-model="form.secret" type="password" autocomplete="new-password" maxlength="32" :disabled="form.clearSecret" :placeholder="t(settings.secretConfigured?'已加密保存，留空保持不变':'填写微信开放平台 AppSecret')"/></label>
    <label class="wechat-check"><input v-model="form.clearSecret" type="checkbox" @change="form.clearSecret&&(form.secret='',form.enabled=false)"/>{{t('清除已保存的 AppSecret')}}</label>
    <label class="wechat-check"><input v-model="form.enabled" type="checkbox" :disabled="form.clearSecret"/>{{t('启用微信扫码登录')}}</label>
    <p class="wechat-config-note">{{t('密钥不会回显。修改配置会使未完成的扫码请求失效；已有绑定时不能直接更换 AppID。关闭登录不撤销已登录会话。')}}</p>
    <div class="wechat-settings-actions"><button type="submit" class="btn primary">{{t(busy?'保存中…':'保存配置')}}</button><button type="button" class="btn" @click="reload">{{t('刷新')}}</button></div>
   </fieldset>
  </form>
  <a href="https://open.weixin.qq.com/" target="_blank" rel="noopener noreferrer">{{t('前往微信开放平台')}}</a>
 </section>
</template>
<style scoped>
.wechat-settings{max-width:800px;min-width:0;padding:20px;background:var(--card);border:1px solid var(--border);border-radius:8px}.wechat-settings p,.wechat-settings li{line-height:1.8;overflow-wrap:anywhere}.wechat-settings fieldset{display:grid;gap:16px;border:0;padding:0;margin:24px 0}.wechat-settings label{display:grid;gap:8px;min-width:0}.wechat-settings input:not([type=checkbox]){width:100%;min-width:0}.wechat-settings code{padding:12px;background:var(--secondary);border-radius:6px;white-space:normal;overflow-wrap:anywhere}.wechat-settings .wechat-check{display:flex;align-items:center;gap:10px}.wechat-config-note{color:var(--muted-foreground);font-size:13px}.wechat-settings-actions{display:flex;gap:8px;flex-wrap:wrap}.wechat-settings [role=alert]{color:var(--destructive)}@media(max-width:760px){.wechat-settings{padding:16px}.wechat-settings ol{padding-left:20px}}
</style>

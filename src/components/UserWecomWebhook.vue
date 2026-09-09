<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { formatDate, t } from '../i18n'
import { useSettingsScope } from './settingsScope'
import { Button } from './ui/button'
import Icon from './Icon.vue'
import { validWecomWebhookURL, wecomDeliveryErrors, wecomDeliveryStatuses, wecomWebhookEndpoint, wecomWebhookPatch, type WecomWebhookSettings } from '../wecomWebhook'

const props=defineProps<{userId?:string;readOnly?:boolean}>(),emit=defineEmits<{(event:'busy',value:boolean):void;(event:'dirty',value:boolean):void;(event:'saved'):void}>()
const scope=useSettingsScope(),settings=ref<WecomWebhookSettings|null>(null),url=ref(''),enabled=ref(false),loading=ref(true),saving=ref(false),error=ref(''),notice=ref('')
const dirty=computed(()=>!!url.value.trim()||!!settings.value&&enabled.value!==settings.value.enabled)
const disabled=computed(()=>!!props.readOnly||loading.value||saving.value||scope.locked.value||!settings.value)
let version=0,disposed=false,projectLeaveApproved=false
watch(dirty,value=>emit('dirty',value),{flush:'sync'});watch(saving,value=>emit('busy',value),{flush:'sync'})
function apply(value:WecomWebhookSettings){settings.value=value;enabled.value=value.enabled;url.value=''}
function clearMemory(){version++;url.value='';enabled.value=false;settings.value=null;loading.value=false;saving.value=false;notice.value='';error.value='';projectLeaveApproved=false}
watch(scope.locked,value=>{if(value)clearMemory()},{flush:'sync'})
function current(request:number){return !disposed&&request===version&&scope.current()}
async function load(force=false){
  if(saving.value||!scope.current()||disposed)return
  if(dirty.value&&!force&&!window.confirm(t('刷新将放弃尚未保存的机器人设置，继续吗？')))return
  const request=++version,endpoint=wecomWebhookEndpoint(props.userId);loading.value=true;error.value='';notice.value=''
  try{const result=await scope.request<WecomWebhookSettings>(endpoint);if(current(request))apply(result)}
  catch(cause){if(current(request))error.value=cause instanceof Error?cause.message:'机器人设置加载失败，请重试'}
  finally{if(current(request))loading.value=false}
}
async function save(){
  if(disabled.value||!dirty.value||!scope.current())return
  error.value='';notice.value=''
  if(url.value.trim()&&!validWecomWebhookURL(url.value)){error.value='请输入有效的企业微信官方机器人地址';return}
  if((url.value.trim()||enabled.value)&&!settings.value?.ready){error.value='机器人加密配置不可用，请联系管理员';return}
  if(enabled.value&&!settings.value?.configured&&!url.value.trim()){error.value='请先填写机器人地址';return}
  const request=++version,endpoint=wecomWebhookEndpoint(props.userId),body=wecomWebhookPatch(url.value,enabled.value);saving.value=true
  try{const result=await scope.request<WecomWebhookSettings>(endpoint,{method:'PATCH',body:JSON.stringify(body)});if(current(request)){apply(result);notice.value='机器人设置已保存';emit('saved')}}
  catch(cause){if(current(request))error.value=cause instanceof Error?cause.message:'机器人设置保存失败，请重试'}
  finally{if(current(request))saving.value=false}
}
async function remove(){
  if(disabled.value||!settings.value?.configured||!scope.current()||!window.confirm(t('移除机器人地址并停用推送？未保存的机器人草稿也会被放弃。')))return
  const request=++version,endpoint=wecomWebhookEndpoint(props.userId);saving.value=true;error.value='';notice.value=''
  try{const result=await scope.request<WecomWebhookSettings>(endpoint,{method:'PATCH',body:JSON.stringify({clear:true})});if(current(request)){apply(result);notice.value='机器人地址已移除';emit('saved')}}
  catch(cause){if(current(request))error.value=cause instanceof Error?cause.message:'机器人设置保存失败，请重试'}
  finally{if(current(request))saving.value=false}
}
function discard(){if(saving.value)return;url.value='';enabled.value=settings.value?.enabled||false;error.value='';notice.value=''}
function canLeave(){return !saving.value&&(projectLeaveApproved||!dirty.value||window.confirm(t('机器人设置尚未保存，离开将放弃修改，继续吗？')))}
function requestClose(){if(!canLeave())return false;discard();return true}
function beforeProjectChange(event:Event){projectLeaveApproved=false;if(!canLeave())event.preventDefault();else projectLeaveApproved=true}
function cancelProjectLeave(){projectLeaveApproved=false}
function beforeUnload(event:BeforeUnloadEvent){const switching=projectLeaveApproved&&(localStorage.getItem('devflow-project')||'prj_orbit')!==scope.project;if(!switching&&(dirty.value||saving.value)){event.preventDefault();event.returnValue=''}}
watch(()=>props.userId,()=>{clearMemory();void load(true)})
onMounted(()=>{void load();window.addEventListener('devflow-before-project-change',beforeProjectChange);window.addEventListener('devflow-project-change-cancelled',cancelProjectLeave);window.addEventListener('beforeunload',beforeUnload);window.addEventListener('devflow-identity-changed',clearMemory);window.addEventListener('devflow-auth-expired',clearMemory);window.addEventListener('devflow-project-changed',clearMemory)})
onBeforeUnmount(()=>{disposed=true;clearMemory();window.removeEventListener('devflow-before-project-change',beforeProjectChange);window.removeEventListener('devflow-project-change-cancelled',cancelProjectLeave);window.removeEventListener('beforeunload',beforeUnload);window.removeEventListener('devflow-identity-changed',clearMemory);window.removeEventListener('devflow-auth-expired',clearMemory);window.removeEventListener('devflow-project-changed',clearMemory)})
defineExpose({dirty,saving,canLeave,requestClose})
</script>

<template>
  <section class="wecom-settings" :aria-busy="loading||saving">
    <header class="wecom-heading"><div><span class="wecom-mark"><Icon name="bell" :size="20"/></span><div><h2>{{t('企业微信机器人')}}</h2><p>{{t('将所有新站内通知（含自动化规则）同步到绑定的群机器人。')}}</p></div></div><Button variant="outline" size="sm" :disabled="loading||saving||scope.locked.value" @click="load()">{{t('刷新设置与记录')}}</Button></header>
    <p v-if="scope.locked.value" class="wecom-error" role="alert">{{t('项目或账号已变化，请刷新页面后继续')}}</p>
    <p v-if="error" class="wecom-error" role="alert">{{t(error)}}</p><p v-if="notice" class="wecom-success" role="status">{{t(notice)}}</p>
    <p v-if="loading&&!settings" class="wecom-loading" role="status">{{t('正在加载机器人设置…')}}</p>
    <template v-if="settings&&!scope.locked.value">
      <div class="wecom-mode" :class="{live:settings.deliveryMode==='live'}"><b>{{t(settings.deliveryMode==='live'?'当前为真实投递模式':'当前为模拟投递模式')}}</b><p>{{t(settings.deliveryMode==='live'?'启用并保存后，新通知将由服务端推送到企业微信群。':'当前只记录模拟投递，不会向企业微信发送真实消息。')}}</p><small>{{t('投递模式由服务端配置控制，页面不能切换为真实外发。')}}</small></div>
      <p v-if="settings.publicUrlConfigured===false" class="wecom-mode">{{t('详情链接尚未配置，请管理员设置团队可访问的 DEVFLOW_PUBLIC_URL。')}}</p>
      <p class="wecom-privacy">{{t('完整通知按分类推送，包含操作人、项目、事项概况和详情入口；长内容分段发送，全部段确认后才算完成。')}}</p>
      <p v-if="!settings.ready" class="wecom-error" role="alert">{{t('机器人加密配置不可用，请联系管理员')}}</p>
      <p v-if="readOnly">{{t(settings.configured&&settings.enabled?'机器人已配置并启用':'机器人尚未配置或未启用')}} <router-link to="/profile">{{t('配置机器人')}}</router-link></p>
      <form v-if="!readOnly" class="wecom-form" @submit.prevent="save"><fieldset :disabled="disabled"><label class="wecom-url"><span>{{t(settings.configured?'替换机器人地址':'机器人 Webhook 地址')}}</span><input v-model="url" type="password" autocomplete="off" autocapitalize="off" spellcheck="false" maxlength="1024" :placeholder="t(settings.configured?'留空保留已有地址；填写则替换':'粘贴企业微信官方机器人 Webhook 地址')" :aria-label="t('机器人 Webhook 地址')"><small>{{t('仅支持 qyapi.weixin.qq.com 的 HTTPS 群机器人地址；保存后不回显密钥。')}}</small></label><div v-if="settings.configured" class="wecom-configured"><span>{{t('已配置')}}</span><code>{{settings.maskedUrl}}</code></div><label class="wecom-toggle"><div><b>{{t('启用机器人通知')}}</b><small>{{t('只投递此成员新产生的站内通知；停用不会影响站内通知。')}}</small></div><input v-model="enabled" type="checkbox" role="switch" :aria-label="t('启用机器人通知')"></label></fieldset><footer><small>{{t('最近更新：{date}',{date:formatDate(settings.updatedAt)})}}</small><div><Button v-if="settings.configured" type="button" variant="ghost" :disabled="disabled" @click="remove">{{t('移除地址')}}</Button><Button v-if="dirty" type="button" variant="outline" :disabled="saving" @click="discard">{{t('放弃修改')}}</Button><Button type="submit" :disabled="disabled||!dirty">{{t(saving?'保存中…':'保存机器人设置')}}</Button></div></footer></form>
      <section class="wecom-deliveries"><header><h3>{{t('最近投递记录')}}</h3><span>{{t('最近 20 条；标题、状态与时间')}}</span></header><div class="wecom-table-wrap" tabindex="0" :aria-label="t('机器人投递记录')"><table><thead><tr><th>{{t('通知标题')}}</th><th>{{t('投递状态')}}</th><th>{{t('尝试次数')}}</th><th>{{t('通知时间 / 发送时间')}}</th><th>{{t('说明')}}</th></tr></thead><tbody><tr v-for="item in settings.deliveries" :key="item.id"><td>{{item.title||'—'}}<small v-if="item.group">{{t(item.group)}}</small></td><td><span class="wecom-status" :class="item.status">{{t(wecomDeliveryStatuses[item.status]||item.status)}}</span><small v-if="item.parts">{{t('已确认 {sent}/{total} 段',{sent:item.sentParts||0,total:item.parts})}}</small></td><td>{{item.attempts}}</td><td>{{formatDate(item.createdAt)}}<small v-if="item.sentAt">{{formatDate(item.sentAt)}}</small></td><td>{{item.errorCode?t(wecomDeliveryErrors[item.errorCode]||'未知投递错误，请联系管理员'):'—'}}</td></tr><tr v-if="!settings.deliveries.length"><td colspan="5" class="wecom-no-records">{{t('暂无投递记录。保存配置不会主动发送测试消息。')}}</td></tr></tbody></table></div></section>
      <p class="wecom-privacy">{{t('机器人地址相当于发送凭据，请仅配置团队授权的群。消息可能包含需求标题、通知内容、时间与访问链接。')}}</p>
    </template>
  </section>
</template>

<style scoped>
.wecom-settings{color:var(--ink);font-size:12px}.wecom-heading{display:flex;align-items:center;justify-content:space-between;gap:18px;margin-bottom:24px}.wecom-heading>div{display:flex;gap:13px;align-items:center}.wecom-mark{display:grid;place-items:center;background:#e6f5ee;color:#159267;border-radius:10px;width:42px;height:42px;flex:none}.wecom-heading h2{margin:0 0 6px;font-size:18px;font-weight:600}.wecom-heading p{margin:0;color:var(--muted);font-size:11px;line-height:1.6}.wecom-mode{padding:16px 18px;border:1px solid #f3ddac;background:#fffaeb;color:#966316;border-radius:9px;margin-bottom:20px}.wecom-mode.live{background:#ebf7ef;border-color:#c8e9d5;color:#187249}.wecom-mode b{font-size:12px}.wecom-mode p{font-size:12px;line-height:1.6;margin:6px 0}.wecom-mode small{font-size:10px;opacity:.8}.wecom-form fieldset{padding:0;margin:0;border:0;min-width:0}.wecom-url{display:flex;flex-direction:column;gap:9px}.wecom-url>span{font-weight:550;font-size:12px}.wecom-url input{width:100%;font-size:13px;color:var(--ink);background:var(--surface);border:1px solid var(--line);border-radius:7px;padding:11px 12px;box-sizing:border-box}.wecom-url small{color:var(--muted);font-size:10px;line-height:1.7}.wecom-configured{display:flex;flex-wrap:wrap;gap:10px;align-items:center;padding:13px 0;color:var(--muted)}.wecom-configured>span{font-size:10px;padding:3px 6px;background:#e6f5ee;color:#159267;border-radius:4px}.wecom-configured code{font-size:11px;overflow-wrap:anywhere}.wecom-toggle{display:flex;align-items:center;justify-content:space-between;gap:15px;padding:20px 0;margin-top:12px;border-top:1px solid var(--line)}.wecom-toggle b{font-size:12px}.wecom-toggle small{display:block;color:var(--muted);margin-top:6px;font-size:10px;line-height:1.7}.wecom-toggle input{width:18px;height:18px;accent-color:var(--primary);flex:none}.wecom-form footer{display:flex;justify-content:space-between;align-items:center;gap:16px;border-top:1px solid var(--line);padding:16px 0}.wecom-form footer>small{font-size:10px;color:var(--muted)}.wecom-form footer>div{display:flex;gap:8px;flex-wrap:wrap}.wecom-deliveries{border:1px solid var(--line);border-radius:9px;margin-top:22px;overflow:hidden}.wecom-deliveries>header{padding:14px 16px;display:flex;align-items:center;justify-content:space-between;gap:12px}.wecom-deliveries h3{font-size:13px;margin:0}.wecom-deliveries header>span{font-size:10px;color:var(--muted)}.wecom-table-wrap{overflow:auto;max-height:360px}.wecom-table-wrap table{width:100%;min-width:660px;border-collapse:collapse;text-align:left;font-size:11px}.wecom-table-wrap th{position:sticky;top:0;background:var(--surface-subtle);font-weight:500;color:var(--muted)}.wecom-table-wrap th,.wecom-table-wrap td{border-top:1px solid var(--line);padding:11px 14px;line-height:1.6}.wecom-table-wrap td:first-child{max-width:240px;overflow-wrap:anywhere}.wecom-table-wrap td small{display:block;font-size:10px;color:var(--muted)}.wecom-status{white-space:nowrap;color:var(--muted)}.wecom-status.sent{color:#168558}.wecom-status.mock_sent{color:#986214}.wecom-status.failed{color:#c63a40}.wecom-no-records{text-align:center!important;color:var(--muted);padding:28px 16px!important}.wecom-privacy{margin:18px 0 0;font-size:10px;line-height:1.8;color:var(--muted)}.wecom-error,.wecom-success{padding:12px 15px;border:1px solid;border-radius:7px;font-size:12px;line-height:1.6}.wecom-error{color:#b42318;background:#fff1f0;border-color:#f3c7c5}.wecom-success{color:#188552;background:#ebf7ef;border-color:#c8e9d5}.wecom-loading{padding:24px;text-align:center;color:var(--muted)}@media(max-width:700px){.wecom-heading{flex-direction:column;align-items:flex-start}.wecom-form footer{flex-direction:column;align-items:flex-start}.wecom-deliveries>header{align-items:flex-start;flex-direction:column}}
</style>

<style scoped>
/* 企微投递状态与设置弹层共享系统级成功/警告/错误语义，防止暗色模式沿用浅色告警底。 */
.wecom-mark,.wecom-configured>span{background:var(--success-background);color:var(--success)}
.wecom-mode{background:var(--warning-background);border-color:var(--warning-border);color:var(--warning)}
.wecom-mode.live{background:var(--success-background);border-color:var(--success-border);color:var(--success)}
.wecom-status.sent,.wecom-success{color:var(--success)}
.wecom-status.mock_sent{color:var(--warning)}
.wecom-status.failed,.wecom-error{color:var(--danger)}
.wecom-error{background:var(--danger-background);border-color:var(--danger-border)}
.wecom-success{background:var(--success-background);border-color:var(--success-border)}
.wecom-url input{background:var(--background);color:var(--foreground);border-color:var(--input)}
.wecom-url input:focus-visible{outline:2px solid var(--ring);outline-offset:2px;border-color:var(--ring)}
.wecom-deliveries,.wecom-table-wrap th{border-color:var(--border)}
.wecom-table-wrap th{background:var(--secondary);color:var(--muted-foreground)}
.wecom-table-wrap td{border-color:var(--border)}
.wecom-toggle input{background:var(--secondary);border-color:var(--input)}
.wecom-toggle input:checked{background:var(--primary);border-color:var(--primary)}
</style>
<style scoped>
.wecom-settings{min-width:0;max-width:100%}.wecom-heading{flex-wrap:wrap;align-items:flex-start;gap:12px}.wecom-heading>div{flex:1 1 250px;min-width:0}.wecom-heading>div>div{min-width:0}.wecom-heading h2,.wecom-heading p{overflow-wrap:anywhere}.wecom-heading>button{flex:none}.wecom-table-wrap{min-width:0;width:100%;overscroll-behavior:contain}.wecom-mode,.wecom-privacy,.wecom-url small{overflow-wrap:anywhere}.wecom-form footer{flex-wrap:wrap}.wecom-form footer>div{margin-left:auto;justify-content:flex-end}.wecom-deliveries>header{flex-wrap:wrap}.wecom-toggle input{appearance:none!important;position:relative;display:block;width:38px!important;height:22px!important;min-width:38px;border:1px solid var(--line,#ccd4df);border-radius:999px;background:var(--surface-subtle,#e5e9f0);cursor:pointer;transition:background .15s}.wecom-toggle input:before{content:'';position:absolute;top:2px;left:2px;width:16px;height:16px;border-radius:50%;background:var(--muted,#7a8799);transition:transform .15s}.wecom-toggle input:checked{background:var(--primary,#3370eb);border-color:var(--primary,#3370eb)}.wecom-toggle input:checked:before{background:#fff;transform:translateX(16px)}.wecom-toggle input:focus-visible{outline:2px solid var(--primary);outline-offset:3px}.wecom-toggle input:disabled{opacity:.5;cursor:not-allowed}@media(max-width:700px){.wecom-heading>div{flex-basis:auto}.wecom-form footer>div{margin-left:0;justify-content:flex-start}}@media(prefers-reduced-motion:reduce){.wecom-toggle input,.wecom-toggle input:before{transition:none}}
</style>

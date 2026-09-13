<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, reactive, ref } from 'vue'
import { onBeforeRouteLeave, onBeforeRouteUpdate } from 'vue-router'
import { t } from '../i18n'
import { useSettingsScope } from './settingsScope'

type Settings = {
  corpId:string; agentId:string; origin:string; callbackUrl:string; verifyFilename:string; verifyUrl:string; apiBase:string
  enabled:boolean; notificationDeliveryEnabled:boolean; secretConfigured:boolean; configurationReady:boolean; deliveryReady:boolean; externalCallsEnabled:boolean; providerMode:string; version:number
  deliveryStats?:{boundMembers:number;pending:number;retry:number;sending:number;sent:number;mockSent:number;failed:number;skipped:number}
  recentDeliveries?:{id:number;status:string;attempts:number;errorCode:string;createdAt:string;sentAt:string;title:string;category:string}[]
}

const scope=useSettingsScope(),settings=ref<Settings|null>(null),busy=ref(false),error=ref(''),notice=ref('')
const deliveryStatusNames:Record<string,string>={pending:'待投递',retry:'等待重试',sending:'投递中',sent:'已发送',mock_sent:'模拟完成',failed:'投递失败',skipped:'已跳过'}
const deliveryErrorNames:Record<string,string>={configuration_changed:'配置已变化',binding_removed:'成员已解绑',recipient_or_binding_changed:'接收成员或绑定已变化',project_access_revoked:'项目权限已撤销',attempts_exhausted:'重试次数已用尽',delivery_unavailable:'企业微信暂时不可用',rate_limited:'企业微信限流',token_rejected:'企业微信令牌已失效',provider_rejected:'企业微信拒绝投递',invalid_response:'企业微信响应无效',encryption_key_unavailable:'服务器加密密钥不可用',database_unavailable:'数据服务暂时不可用'}
const form=reactive({corpId:'',agentId:'',origin:'https://taskloom.example.com',verifyFilename:'',secret:'',clearSecret:false,enabled:false,notificationDeliveryEnabled:false,version:0})
let disposed=false
const dirty=computed(()=>!!form.secret||form.clearSecret||!!settings.value&&(form.corpId!==settings.value.corpId||form.agentId!==settings.value.agentId||form.origin!==settings.value.origin||form.verifyFilename!==settings.value.verifyFilename||form.enabled!==settings.value.enabled||form.notificationDeliveryEnabled!==settings.value.notificationDeliveryEnabled))
const callbackUrl=computed(()=>form.origin.replace(/\/$/,'')+'/api/auth/wecom/callback')
const verifyUrl=computed(()=>form.origin.replace(/\/$/,'')+(form.verifyFilename?'/'+form.verifyFilename:''))

async function load(){
  busy.value=true;error.value=''
  try{const data=await scope.request<Settings>('/organization/wecom-app');if(!disposed){settings.value=data;Object.assign(form,{corpId:data.corpId,agentId:data.agentId,origin:data.origin,verifyFilename:data.verifyFilename,secret:'',clearSecret:false,enabled:data.enabled,notificationDeliveryEnabled:data.notificationDeliveryEnabled,version:data.version})}}
  catch(cause){if(!disposed)error.value=cause instanceof Error?t(cause.message):t('企业微信自建应用配置读取失败')}
  finally{if(!disposed)busy.value=false}
}
function canLeave(){return !busy.value&&(!dirty.value||window.confirm(t('企业微信自建应用配置尚未保存，确认离开？')))}
onMounted(load);onBeforeUnmount(()=>{disposed=true;form.secret=''})
onBeforeRouteLeave(canLeave);onBeforeRouteUpdate(canLeave)
function reload(){if(!dirty.value||window.confirm(t('丢弃未保存的企业微信自建应用配置？')))void load()}
async function save(){
  if(busy.value||scope.locked.value)return
  busy.value=true;error.value='';notice.value=''
  try{
    const data=await scope.request<Settings>('/organization/wecom-app',{method:'PATCH',body:JSON.stringify({corpId:form.corpId,agentId:form.agentId,origin:form.origin,verifyFilename:form.verifyFilename,secret:form.secret,clearSecret:form.clearSecret,enabled:form.enabled,notificationDeliveryEnabled:form.notificationDeliveryEnabled,version:form.version})})
    if(!disposed){settings.value=data;Object.assign(form,{corpId:data.corpId,agentId:data.agentId,origin:data.origin,verifyFilename:data.verifyFilename,secret:'',clearSecret:false,enabled:data.enabled,notificationDeliveryEnabled:data.notificationDeliveryEnabled,version:data.version});notice.value=data.externalCallsEnabled?'配置已安全保存；后续已绑定成员的新站内通知会异步投递到企业微信。':'配置已安全保存；当前仍为模拟模式或通知投递未启用，不会向企业微信外发。'}
  }catch(cause){if(!disposed)error.value=cause instanceof Error?t(cause.message):t('企业微信自建应用配置保存失败')}
  finally{form.secret='';if(!disposed)busy.value=false}
}
</script>

<template>
  <section class="wecom-app-settings" :aria-busy="busy">
    <header>
      <div><p class="wecom-kicker">{{t('企业微信自建应用')}}</p><h2>{{t('账号绑定与通知投递')}}</h2></div>
      <span class="wecom-state" :class="{ready:settings?.deliveryReady}">{{t(settings?.deliveryReady?'投递已配置':'待完成配置')}}</span>
    </header>
    <p>{{t('在企业微信管理后台创建自建应用后，将 CorpID、AgentId、应用 Secret、可信域名和校验文件名保存到此处。密钥仅加密保存，后续读取不会回显。')}}</p>
    <p class="wecom-safety">{{t(settings?.providerMode==='live'?'仅在“启用自建应用”和“启用通知中心企业微信投递”均打开、成员完成官方授权绑定后，服务端才会异步调用企业微信接口。站内通知不会因外发失败而丢失。':'当前服务器处于模拟模式：会保留安全的投递状态，但不会调用企业微信接口。真实投递由服务器运维将 DEVFLOW_WECOM_MODE 设为 live 后才会开启。')}}</p>
    <p v-if="scope.locked.value" role="alert" class="wecom-error">{{t('项目或账号已变化，请刷新页面后继续')}}</p>
    <p v-if="error" role="alert" class="wecom-error">{{error}}</p><p v-if="notice" role="status" class="wecom-notice">{{t(notice)}}</p>
    <form v-if="settings" @submit.prevent="save">
      <fieldset :disabled="busy||scope.locked.value">
        <div class="wecom-field-grid">
          <label>{{t('企业 ID CorpID')}}<input v-model.trim="form.corpId" required maxlength="64" autocomplete="off" placeholder="ww…"></label>
          <label>{{t('应用 AgentId')}}<input v-model.trim="form.agentId" required inputmode="numeric" maxlength="12" autocomplete="off" placeholder="1000001"></label>
        </div>
        <label>{{t('正式站点 HTTPS 域名')}}<input v-model.trim="form.origin" required type="url" maxlength="250" placeholder="https://taskloom.example.com"></label>
        <label>{{t('域名校验文件名')}}<input v-model.trim="form.verifyFilename" required maxlength="150" autocomplete="off" placeholder="WW_verify_xxxxxxxxxx.txt"><small>{{t('请将企业微信下载的原始校验文件部署在正式域名根目录；系统只保存文件名，不保存或代管校验文件内容。')}}</small></label>
        <div class="wecom-generated">
          <span><small>{{t('授权回调地址')}}</small><code>{{callbackUrl}}</code></span>
          <span><small>{{t('校验文件访问地址')}}</small><code>{{verifyUrl||'—'}}</code></span>
          <span><small>{{t('企业微信 API 网关')}}</small><code>{{settings.apiBase}}</code></span>
        </div>
        <label>{{t('应用 Secret')}}<input v-model="form.secret" type="password" autocomplete="new-password" maxlength="128" :disabled="form.clearSecret" :placeholder="t(settings.secretConfigured?'已加密保存，留空保持不变':'填写企业微信自建应用 Secret')"></label>
        <label class="wecom-check"><input v-model="form.clearSecret" type="checkbox" @change="form.clearSecret&&(form.secret='',form.enabled=false,form.notificationDeliveryEnabled=false)"><span>{{t('清除已保存的应用 Secret')}}<small>{{t('清除后将自动关闭自建应用与通知投递开关。')}}</small></span></label>
        <label class="wecom-check"><input v-model="form.enabled" type="checkbox" :disabled="form.clearSecret" @change="!form.enabled&&(form.notificationDeliveryEnabled=false)"><span>{{t('启用企业微信自建应用')}}<small>{{t('启用前必须已保存完整配置；成员可在个人设置完成企业微信授权绑定。')}}</small></span></label>
        <label class="wecom-check delivery-check"><input v-model="form.notificationDeliveryEnabled" type="checkbox" :disabled="form.clearSecret||!form.enabled"><span><b>{{t('启用通知中心企业微信投递')}}</b><small>{{t('只投递此开关开启之后新产生的站内通知；仅已完成官方绑定、仍有项目访问权限的成员会收到消息。失败会重试并保留状态，不影响站内通知。')}}</small></span></label>
        <section v-if="settings" class="wecom-delivery-overview" :class="{live:settings.externalCallsEnabled}">
          <div><b>{{t(settings.externalCallsEnabled?'真实投递已具备条件':settings.providerMode==='live'?'等待启用投递或成员绑定':'模拟投递模式')}}</b><small>{{t(settings.externalCallsEnabled?'企业微信接口将在后台异步调用；不补发历史通知。':'配置、绑定和服务器模式三项全部满足后，才会真实外发。')}}</small></div>
          <dl v-if="settings.deliveryStats"><div><dt>{{t('已绑定成员')}}</dt><dd>{{settings.deliveryStats.boundMembers}}</dd></div><div><dt>{{t('待投递')}}</dt><dd>{{settings.deliveryStats.pending+settings.deliveryStats.retry+settings.deliveryStats.sending}}</dd></div><div><dt>{{t('失败')}}</dt><dd>{{settings.deliveryStats.failed}}</dd></div><div><dt>{{t('已完成')}}</dt><dd>{{settings.deliveryStats.sent+settings.deliveryStats.mockSent}}</dd></div></dl>
          <div v-if="settings.recentDeliveries?.length" class="wecom-delivery-list"><p>{{t('最近投递记录')}}</p><div v-for="item in settings.recentDeliveries" :key="item.id"><span>{{item.category}}</span><b>{{item.title}}</b><em>{{t(deliveryStatusNames[item.status]||item.status)}}</em><small v-if="item.errorCode">{{t(deliveryErrorNames[item.errorCode]||'投递未完成')}}</small></div></div>
        </section>
        <div class="wecom-actions"><button type="submit" class="btn primary">{{t(busy?'保存中…':'保存配置')}}</button><button type="button" class="btn" @click="reload">{{t('刷新')}}</button><a href="https://work.weixin.qq.com/wework_admin/frame#apps" target="_blank" rel="noopener noreferrer">{{t('打开企业微信管理后台')}}</a></div>
      </fieldset>
    </form>
    <details>
      <summary>{{t('上线前核对清单')}}</summary>
      <ol><li>{{t('在企业微信管理后台创建自建应用，并设置研发成员可见范围与消息发送权限。')}}</li><li>{{t('将 WW_verify 文件原样部署到正式站点根目录，再在企业微信后台完成可信域名校验。')}}</li><li>{{t('将授权回调域设置为正式域名，并在企业可信 IP 中加入服务器公网出口 IP。')}}</li><li>{{t('将服务器 DEVFLOW_WECOM_MODE 设置为 live 后，使用非管理员测试账号在个人设置完成授权绑定，再打开通知投递开关进行验收。')}}</li><li>{{t('不要在聊天、工单、截图、浏览器存储或代码仓库中传播应用 Secret。')}}</li></ol>
    </details>
  </section>
</template>

<style scoped>
.wecom-app-settings{max-width:880px;min-width:0;padding:24px;border:1px solid var(--border);border-radius:12px;background:var(--card);box-shadow:none}.wecom-app-settings header{display:flex;align-items:flex-start;justify-content:space-between;gap:16px}.wecom-kicker{margin:0 0 4px;color:var(--primary);font-size:12px;font-weight:700;letter-spacing:.06em}.wecom-app-settings h2{margin:0;font-size:20px;line-height:1.4}.wecom-app-settings>p{line-height:1.8;overflow-wrap:anywhere}.wecom-state{display:inline-flex;flex:none;align-items:center;min-height:28px;padding:0 10px;border:1px solid var(--border);border-radius:999px;background:var(--secondary);color:var(--muted-foreground);font-size:12px;white-space:nowrap}.wecom-state.ready{border-color:color-mix(in srgb,var(--primary) 30%,var(--border));background:color-mix(in srgb,var(--primary) 10%,var(--card));color:var(--primary)}.wecom-safety{padding:12px 14px;border-left:3px solid var(--primary);background:var(--secondary);color:var(--muted-foreground);font-size:13px}.wecom-app-settings fieldset{display:grid;gap:16px;border:0;margin:22px 0 0;padding:0}.wecom-app-settings label{display:grid;gap:7px;min-width:0;font-weight:600}.wecom-app-settings input:not([type=checkbox]){box-sizing:border-box;width:100%;min-width:0}.wecom-app-settings label small{color:var(--muted-foreground);font-weight:400;line-height:1.6}.wecom-field-grid{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:14px}.wecom-generated{display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:10px;padding:12px;border:1px solid var(--border);border-radius:8px;background:var(--secondary)}.wecom-generated span{display:grid;gap:5px;min-width:0}.wecom-generated small{color:var(--muted-foreground);font-size:12px}.wecom-generated code{overflow-wrap:anywhere;color:var(--foreground);font:12px/1.55 ui-monospace,SFMono-Regular,Menlo,monospace}.wecom-check{display:flex!important;grid-template-columns:auto minmax(0,1fr);align-items:start;gap:10px!important;font-weight:400!important}.wecom-check input{margin-top:3px}.wecom-check span{display:grid;gap:4px}.delivery-check{padding:13px;border:1px solid color-mix(in srgb,var(--primary) 24%,var(--border));border-radius:9px;background:color-mix(in srgb,var(--primary) 5%,var(--card))}.delivery-check b{font-weight:650}.wecom-delivery-overview{display:grid;gap:14px;padding:15px;border:1px solid var(--border);border-radius:10px;background:var(--secondary)}.wecom-delivery-overview>div:first-child{display:grid;gap:4px}.wecom-delivery-overview>div:first-child small{color:var(--muted-foreground);line-height:1.6}.wecom-delivery-overview.live{border-color:color-mix(in srgb,var(--primary) 40%,var(--border));background:color-mix(in srgb,var(--primary) 7%,var(--card))}.wecom-delivery-overview dl{display:grid;grid-template-columns:repeat(4,minmax(0,1fr));gap:8px;margin:0}.wecom-delivery-overview dl div{display:grid;gap:4px;padding:9px;border:1px solid var(--border);border-radius:7px;background:var(--card)}.wecom-delivery-overview dt{color:var(--muted-foreground);font-size:12px}.wecom-delivery-overview dd{margin:0;font-size:18px;font-weight:700}.wecom-delivery-list{display:grid;gap:7px;padding-top:3px}.wecom-delivery-list>p{margin:0;font-size:13px;font-weight:650}.wecom-delivery-list>div{display:grid;grid-template-columns:auto minmax(0,1fr) auto auto;gap:8px;align-items:center;padding:8px 0;border-top:1px solid var(--border);font-size:12px}.wecom-delivery-list span,.wecom-delivery-list em{font-style:normal;color:var(--muted-foreground)}.wecom-delivery-list b{overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.wecom-delivery-list small{color:var(--destructive);overflow-wrap:anywhere}.wecom-actions{display:flex;align-items:center;flex-wrap:wrap;gap:9px}.wecom-actions a{font-size:13px;color:var(--primary);text-decoration:none}.wecom-actions a:hover{text-decoration:underline}.wecom-error{color:var(--destructive)}.wecom-notice{color:var(--primary)}details{margin-top:24px;padding-top:18px;border-top:1px solid var(--border)}summary{cursor:pointer;font-weight:650}details ol{padding-left:20px;line-height:1.85;color:var(--muted-foreground)}
@media(max-width:700px){.wecom-app-settings{padding:16px;border-radius:10px}.wecom-app-settings header{flex-wrap:wrap}.wecom-field-grid,.wecom-generated{grid-template-columns:minmax(0,1fr)}.wecom-delivery-overview dl{grid-template-columns:repeat(2,minmax(0,1fr))}.wecom-delivery-list>div{grid-template-columns:auto minmax(0,1fr)}.wecom-delivery-list em,.wecom-delivery-list small{grid-column:2}.wecom-actions>*{min-height:38px}.wecom-actions a{display:inline-flex;align-items:center}}
</style>

<script setup lang="ts">
import AIButton from '../components/AIButton.vue'
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { onBeforeRouteLeave, onBeforeRouteUpdate } from 'vue-router'
import { t } from '../i18n'
import { useSettingsScope } from '../components/settingsScope'
import { Button } from '../components/ui/button'
type Settings={provider:string;configured:boolean;enabled:boolean;model:string;models:string[];canManage:boolean;baseUrl?:string;endpointMode?:'default'|'custom';version?:number;autoReleaseNotes?:boolean}
const defaultBaseUrl='https://api.openai.com/v1'
const endpointMode=ref<'default'|'custom'>('default'),baseUrl=ref('')
// 浏览器只做即时格式提示；服务端仍负责 DNS、内网地址和请求目的地的安全校验。
function normalizedBaseUrl(value:string){
 const raw=value.trim()
 if(!raw||raw.length>2048||/[\s\\]/.test(raw))throw Error('请输入有效的 HTTPS API 地址，不包含账号、参数或片段')
 const url=new URL(raw)
 if(url.protocol!=='https:'||url.username||url.password||url.search||url.hash||!url.hostname)throw Error('请输入有效的 HTTPS API 地址，不包含账号、参数或片段')
 url.pathname=url.pathname.replace(/\/+$/,'')||'/v1'
 return url.href.replace(/\/+$/,'')
}
const effectiveBaseUrl=computed(()=>{try{return endpointMode.value==='default'?defaultBaseUrl:normalizedBaseUrl(baseUrl.value)}catch{return ''}})
const requestAddress=computed(()=>effectiveBaseUrl.value?effectiveBaseUrl.value+'/responses':'')
const scope=useSettingsScope(),settings=ref<Settings|null>(null),apiKey=ref(''),model=ref(''),enabled=ref(false),loading=ref(true),saving=ref(false),error=ref(''),notice=ref('')
const autoReleaseNotes=ref(false)
const supportsAutoReleaseNotes=computed(()=>typeof settings.value?.autoReleaseNotes==='boolean'&&settings.value.version!==undefined)
const dirty=computed(()=>!!apiKey.value.trim()||!!settings.value&&(model.value!==settings.value.model||enabled.value!==settings.value.enabled||autoReleaseNotes.value!==(settings.value.autoReleaseNotes??false)||effectiveBaseUrl.value!==(settings.value.baseUrl||defaultBaseUrl)))
const disabled=computed(()=>loading.value||saving.value||scope.locked.value||!settings.value?.canManage)
let requestVersion=0,disposed=false,projectLeaveApproved=false
function current(version:number){return !disposed&&version===requestVersion&&scope.current()}
function validate(value:Settings){
 if(!value||value.provider!=='openai'||typeof value.configured!=='boolean'||typeof value.enabled!=='boolean'||typeof value.canManage!=='boolean'||!Array.isArray(value.models)||!value.models.length||value.models.some(item=>typeof item!=='string'||!item)||typeof value.model!=='string'||!value.model)throw Error('AI 设置返回格式不正确，请重试')
 if(value.autoReleaseNotes!==undefined&&typeof value.autoReleaseNotes!=='boolean')throw Error('AI 设置返回格式不正确，请重试')
 try{if(value.baseUrl!==undefined&&(typeof value.baseUrl!=='string'||normalizedBaseUrl(value.baseUrl)!==value.baseUrl))throw Error();if(value.endpointMode!==undefined&&value.endpointMode!==((value.baseUrl||defaultBaseUrl)===defaultBaseUrl?'default':'custom'))throw Error();if(value.version!==undefined&&(!Number.isSafeInteger(value.version)||value.version<0))throw Error()}catch{throw Error('AI 设置返回格式不正确，请重试')}
 return value
}
function apply(value:Settings){validate(value);const address=value.baseUrl||defaultBaseUrl;settings.value={provider:value.provider,configured:value.configured,enabled:value.enabled,model:value.model,models:[...value.models],canManage:value.canManage,baseUrl:address,endpointMode:address===defaultBaseUrl?'default':'custom',version:value.version,autoReleaseNotes:value.autoReleaseNotes};model.value=value.model;enabled.value=value.enabled;autoReleaseNotes.value=value.autoReleaseNotes??false;endpointMode.value=settings.value.endpointMode!;baseUrl.value=address===defaultBaseUrl?'':address;apiKey.value=''}
function scrub(){++requestVersion;apiKey.value='';baseUrl.value='';endpointMode.value='default';settings.value=null;model.value='';enabled.value=false;autoReleaseNotes.value=false;loading.value=false;saving.value=false;error.value='';notice.value='';projectLeaveApproved=false}
async function load(){
 if(saving.value||!scope.current()||disposed)return
 if(dirty.value&&!window.confirm(t('刷新将放弃尚未保存的 AI 设置，继续吗？')))return
 const version=++requestVersion;loading.value=true;error.value=''
 try{const data=await scope.request<Settings>('/organization/ai-settings');if(current(version))apply(data)}
 catch(cause){if(current(version))error.value=cause instanceof Error?cause.message:'AI 设置暂时无法加载，请重试'}
 finally{if(current(version))loading.value=false}
}
async function save(){
 if(disabled.value||!dirty.value||!settings.value||!scope.current())return
 error.value='';notice.value=''
 if(!effectiveBaseUrl.value){error.value='请输入有效的 HTTPS API 地址，不包含账号、参数或片段';return}
 if(enabled.value&&!settings.value.configured&&!apiKey.value.trim()){error.value='请先填写 API 密钥';return}
 if(autoReleaseNotes.value&&!enabled.value){error.value='自动升级日志需要先启用 AI 服务；停用服务前请关闭自动生成';return}
 if(model.value!==settings.value.model&&!settings.value.models.includes(model.value)){error.value='请选择服务端支持的模型';return}
 // 所有保存均带版本；密钥替换还显式绑定页面展示的地址，防止并发修改目的地。
 const body:Record<string,unknown>=settings.value.version===undefined?{}:{expectedVersion:settings.value.version};if(apiKey.value.trim()){body.apiKey=apiKey.value.trim();if(settings.value.version!==undefined)body.baseUrl=effectiveBaseUrl.value}if(enabled.value!==settings.value.enabled)body.enabled=enabled.value;if(model.value!==settings.value.model)body.model=model.value
 if(autoReleaseNotes.value!==(settings.value.autoReleaseNotes??false)){
  if(!supportsAutoReleaseNotes.value){error.value='当前服务端尚不支持自动升级日志，请升级服务端后刷新页面';return}
  body.autoReleaseNotes=autoReleaseNotes.value
 }
 if(autoReleaseNotes.value&&(!settings.value.autoReleaseNotes||effectiveBaseUrl.value!==(settings.value.baseUrl||defaultBaseUrl))){
  if(!window.confirm(t('启用后，完成迭代时将自动把已完成需求的标题、正文、验收标准以及图片候选 ID 和文件名发送至 {address}，用于生成升级日志草稿；排除缺陷，不发送图片字节。可能产生模型费用，生成后仍需人工复核。确认启用？',{address:effectiveBaseUrl.value})))return
  body.confirmAutomatic=true
 }
 if(effectiveBaseUrl.value!==(settings.value.baseUrl||defaultBaseUrl)){
  if(settings.value.version===undefined){error.value='当前服务端尚不支持自定义地址，请升级服务端后刷新页面';return}
  body.expectedVersion=settings.value.version
  body.baseUrl=effectiveBaseUrl.value
  if(settings.value.configured&&!apiKey.value.trim()){
   if(!window.confirm(t('后续 AI 调用将把现有密钥和所选业务内容发送至 {address}。请确认该密钥属于此服务，继续保留密钥并切换地址吗？',{address:effectiveBaseUrl.value})))return
   body.reuseKey=true
  }
 }
 const version=++requestVersion;saving.value=true
 try{const data=await scope.request<Settings>('/organization/ai-settings',{method:'PATCH',body:JSON.stringify(body)});if(current(version)){apply(data);notice.value='AI 设置已保存；保存本身不会发起模型调用'}}
 catch(cause){if(current(version))error.value=cause instanceof Error?cause.message:'AI 设置保存失败，请重试'}
 finally{if(current(version))saving.value=false}
}
async function remove(){
 if(disabled.value||!settings.value?.configured||!scope.current()||!window.confirm(t('移除 API 密钥并停用 AI 功能？未保存的密钥草稿也会被放弃。')))return
 const version=++requestVersion;saving.value=true;error.value='';notice.value=''
 try{const data=await scope.request<Settings>('/organization/ai-settings',{method:'PATCH',body:JSON.stringify({clear:true,...(settings.value.version===undefined?{}:{expectedVersion:settings.value.version})})});if(current(version)){apply(data);notice.value='API 密钥已移除，AI 功能已停用'}}
 catch(cause){if(current(version))error.value=cause instanceof Error?cause.message:'AI 设置保存失败，请重试'}
 finally{if(current(version))saving.value=false}
}
function discard(){if(saving.value||!settings.value)return;apply(settings.value);error.value='';notice.value=''}
function canLeave(){return !saving.value&&(projectLeaveApproved||!dirty.value||window.confirm(t('AI 设置尚未保存，离开将放弃修改，继续吗？')))}
function beforeProjectChange(event:Event){projectLeaveApproved=false;if(!canLeave())event.preventDefault();else projectLeaveApproved=true}
function cancelProjectLeave(){projectLeaveApproved=false}
function beforeUnload(event:BeforeUnloadEvent){const switching=projectLeaveApproved&&(localStorage.getItem('devflow-project')||'prj_orbit')!==scope.project;if(!switching&&(dirty.value||saving.value)){event.preventDefault();event.returnValue=''}}
watch(scope.locked,value=>{if(value)scrub()},{flush:'sync'})
onBeforeRouteLeave(canLeave);onBeforeRouteUpdate(canLeave)
onMounted(()=>{void load();window.addEventListener('beforeunload',beforeUnload);window.addEventListener('devflow-before-project-change',beforeProjectChange);window.addEventListener('devflow-project-change-cancelled',cancelProjectLeave)})
onBeforeUnmount(()=>{disposed=true;scrub();window.removeEventListener('beforeunload',beforeUnload);window.removeEventListener('devflow-before-project-change',beforeProjectChange);window.removeEventListener('devflow-project-change-cancelled',cancelProjectLeave)})
</script>
<template>
 <div class="module-page ai-settings-page">
  <header class="ai-settings-header"><div><span class="eyebrow">{{t('企业管理')}}</span><h1>{{t('AI 服务设置')}}</h1><p>{{t('统一配置 AI 服务：专业 PRD 需求描述优化、缺陷描述与复现分析、需求标题、测试用例及迭代升级日志。')}}</p></div><Button variant="outline" :disabled="loading||saving||scope.locked.value" @click="load">{{t('刷新')}}</Button></header>
  <p v-if="scope.locked.value" class="ai-message error" role="alert">{{t('项目或账号已变化，请刷新页面后继续')}}</p><p v-if="error" class="ai-message error" role="alert">{{t(error)}}</p><p v-if="notice" class="ai-message success" role="status">{{t(notice)}}</p>
  <p v-if="loading&&!settings" class="ai-loading" role="status">{{t('正在加载 AI 设置…')}}</p>
  <form v-if="settings&&!scope.locked.value" class="ai-settings-card" autocomplete="off" @submit.prevent="save">
   <fieldset :disabled="disabled">
    <div class="ai-provider"><span class="ai-provider-mark">AI</span><div><b>{{t('AI 服务连接')}}</b><small>{{t(settings.configured?'API 密钥已配置':'尚未配置 API 密钥')}}</small></div><span class="ai-config-status">{{t(settings.enabled?'已启用':'已停用')}}</span></div>
    <section class="ai-endpoint" :aria-label="t('API 地址')">
     <span class="ai-field-label">{{t('API 地址')}}</span>
     <div class="ai-endpoint-modes" role="radiogroup" :aria-label="t('API 地址来源')"><label :class="{active:endpointMode==='default'}"><input v-model="endpointMode" type="radio" name="ai-endpoint" value="default"><span>{{t('默认服务')}}</span></label><label :class="{active:endpointMode==='custom'}"><input v-model="endpointMode" type="radio" name="ai-endpoint" value="custom"><span>{{t('自定义地址')}}</span></label></div>
     <label v-if="endpointMode==='custom'" class="ai-input ai-address-input" for="ai-api-base-url"><span>{{t('API 基础地址')}}</span><input id="ai-api-base-url" name="devflow-ai-api-base-url" v-model="baseUrl" type="url" inputmode="url" autocomplete="off" autocapitalize="off" spellcheck="false" maxlength="2048" placeholder="https://api.owlai.tech" :aria-describedby="'ai-address-help'"></label>
     <code v-else class="ai-default-address">{{defaultBaseUrl}}</code>
     <small id="ai-address-help">{{t('支持 OpenAI Responses 兼容服务。根地址自动补全 /v1；已有 API 前缀不会重复添加。')}}</small>
     <p v-if="requestAddress" class="ai-request-address"><span>{{t('实际请求地址')}}</span><code>{{requestAddress}}</code></p>
    </section>
    <label class="ai-input" for="ai-api-key"><span>{{t(settings.configured?'替换 API 密钥':'API 密钥')}}</span><input id="ai-api-key" name="devflow-ai-api-key" v-model="apiKey" type="password" autocomplete="new-password" autocapitalize="off" spellcheck="false" maxlength="1024" :placeholder="t(settings.configured?'留空保留已有密钥':'输入团队授权的 API 密钥')"><small>{{t('密钥只在输入时保留于内存；服务端保存后不回显，不写入浏览器存储。')}}</small></label>
    <label class="ai-input"><span>{{t('生成模型')}}</span><select v-model="model"><option v-if="model&&!settings.models.includes(model)" :value="model" disabled>{{model}}</option><option v-for="value in settings.models" :key="value" :value="value">{{value}}{{value==='gpt-5.6'?t('（Sol 别名）'):value==='gpt-5.6-cyber'?t('（安全专用，需审批）'):''}}</option></select><small>{{t('模型列表由服务端提供；能否调用取决于所选服务及密钥权限。')}}</small></label>
    <div class="ai-model-guidance"><p>{{t('GPT-6 Astra：复杂推理；GPT-5.6 Sol：旗舰能力；Terra：均衡；Luna：低成本。')}}</p><p>{{t('统一使用 Responses API；模型是否可调用取决于 API 账号权限，Cyber 需要单独审批。保存不会验证模型权限或自动发起付费调用。')}}</p><p>{{t('GPT-5.6 Pro 是推理模式，不是单独模型名称；当前业务保持低推理强度，不自动开启 Pro。')}}</p><a href="https://developers.openai.com/api/docs/models/all" target="_blank" rel="noopener noreferrer">{{t('查看官方模型目录')}}</a></div>
    <label class="ai-enable"><span><b>{{t('启用 AI 服务')}}</b><small>{{t('手动生成需成员确认；自动升级日志由下方开关单独控制。')}}</small></span><input v-model="enabled" type="checkbox" role="switch" :aria-label="t('启用 AI 服务')"></label>
    <label class="ai-enable ai-release-enable"><span><b>{{t('完成迭代后自动生成升级日志')}}</b><small>{{t('默认关闭。开启后，完成迭代会异步发送已完成需求的标题、正文、验收标准以及图片候选 ID 和文件名到所配置的 AI 服务，可能产生模型费用；不包含缺陷，不发送图片字节。')}}</small><small>{{t('仅 AI 服务已配置并启用时生效；失败不影响迭代完成，可在升级日志抽屉重试，结果需人工复核。')}}</small><small v-if="!supportsAutoReleaseNotes">{{t('当前服务端尚不支持自动升级日志，请升级服务端后刷新页面')}}</small></span><input v-model="autoReleaseNotes" type="checkbox" role="switch" :disabled="!supportsAutoReleaseNotes" :aria-label="t('完成迭代后自动生成升级日志')"></label>
   </fieldset>
   <footer><Button v-if="settings.configured" type="button" variant="ghost" :disabled="disabled" @click="remove">{{t('移除密钥')}}</Button><span></span><Button v-if="dirty" type="button" variant="outline" :disabled="saving" @click="discard">{{t('放弃修改')}}</Button><AIButton type="submit" :busy="saving" :disabled="disabled||!dirty">{{t(saving?'保存中…':'保存 AI 设置')}}</AIButton></footer>
  </form>
  <section class="ai-capabilities" :aria-label="t('已接入的 AI 能力')">
   <article><h2>{{t('需求标题总结')}}</h2><p>{{t('标题为空时，仅发送需求描述的文字内容。生成后先预览并确认，再保存需求。')}}</p></article>
   <article><h2>{{t('测试用例生成')}}</h2><p>{{t('发送已保存需求的标题、正文和验收标准；人工审核后再导入用例草稿。')}}</p></article>
   <article><h2>{{t('迭代升级日志')}}</h2><p>{{t('仅汇总完成需求，按七类生成可编辑草稿；截图引用真实需求附件，无图标记待补图，支持导出 Markdown 和 JSON。')}}</p></article>
  </section>
  <aside class="ai-boundary"><h2>{{t('数据发送边界')}}</h2><p>{{t('不会发送附件文件、图片字节、评论或人员资料；生成升级日志时会发送真实图片候选的附件文件名和 ID。不要提交未经授权的个人信息、客户密钥或商业机密。')}}</p><p>{{t('生成结果需人工复核；只有确认导入后才会创建测试用例草稿。保存配置不会验证密钥，也不会产生模型调用。')}}</p></aside>
 </div>
</template>
<style scoped>
.ai-endpoint{display:grid;gap:10px;margin:0 0 22px;min-width:0}.ai-field-label{font-size:12px;font-weight:600}.ai-endpoint-modes{display:flex;gap:6px;flex-wrap:wrap}.ai-endpoint-modes label{display:inline-flex;align-items:center;gap:7px;border:1px solid var(--line);border-radius:6px;background:var(--surface);padding:8px 12px;cursor:pointer;font-size:12px}.ai-endpoint-modes label.active{border-color:var(--primary);background:var(--primary-soft);color:var(--primary)}.ai-endpoint-modes input{accent-color:var(--primary);margin:0;width:14px;height:14px}.ai-endpoint small{font-size:11px;color:var(--muted);line-height:1.7}.ai-address-input{margin:0!important;gap:7px!important}.ai-default-address{padding:10px 12px;border:1px solid var(--line);border-radius:6px;background:var(--surface-subtle,var(--surface));font-size:12px;overflow-wrap:anywhere}.ai-request-address{margin:0;display:grid;gap:5px;font-size:11px;color:var(--muted)}.ai-request-address code{font-size:12px;color:var(--ink);overflow-wrap:anywhere}.ai-endpoint-modes label:focus-within{outline:2px solid var(--primary);outline-offset:2px}fieldset:disabled .ai-endpoint-modes label{cursor:not-allowed;opacity:.65}
.ai-model-guidance{font-size:12px;line-height:1.7;margin-bottom:16px;color:var(--muted)}.ai-model-guidance a{color:var(--primary)}

.ai-settings-page{max-width:980px;margin:auto;color:var(--ink)}.ai-settings-header{display:flex;justify-content:space-between;align-items:flex-start;gap:18px;margin-bottom:25px}.ai-settings-header h1{font-size:26px;margin:8px 0}.ai-settings-header p{margin:0;color:var(--muted);font-size:12px;line-height:1.7}.ai-settings-card{border:1px solid var(--line);border-radius:12px;background:var(--surface);overflow:hidden}.ai-settings-card fieldset{padding:24px;border:0;min-width:0}.ai-provider{display:flex;align-items:center;gap:12px;padding-bottom:22px;border-bottom:1px solid var(--line);margin-bottom:23px}.ai-provider-mark{width:40px;height:40px;display:grid;place-items:center;background:var(--primary-soft);color:var(--primary);border-radius:9px;font-size:14px;font-weight:700}.ai-provider b{font-size:15px}.ai-provider small{display:block;color:var(--muted);font-size:11px;margin-top:5px}.ai-config-status{margin-left:auto;font-size:11px;color:var(--primary);border:1px solid var(--line);border-radius:5px;padding:4px 7px}.ai-input{display:grid;gap:9px;margin-bottom:24px;font-size:12px}.ai-input>span{font-weight:600}.ai-input input,.ai-input select{border:1px solid var(--line);border-radius:7px;padding:11px;background:var(--surface);color:var(--ink);font:inherit;width:100%}.ai-input small{color:var(--muted);font-size:11px;line-height:1.7}.ai-enable{display:flex;align-items:center;justify-content:space-between;gap:15px;padding-top:17px;border-top:1px solid var(--line);font-size:12px}.ai-enable small{display:block;margin-top:5px;color:var(--muted);font-size:11px;line-height:1.7}.ai-enable input{width:18px;height:18px;accent-color:var(--primary)}.ai-settings-card footer{display:flex;align-items:center;gap:9px;border-top:1px solid var(--line);padding:17px 24px}.ai-settings-card footer>span{flex:1}.ai-boundary{margin-top:20px;border:1px solid var(--line);border-radius:10px;background:var(--surface-soft,var(--surface));padding:20px 23px}.ai-boundary h2{font-size:13px;margin:0 0 10px}.ai-boundary p{font-size:12px;line-height:1.9;color:var(--muted);margin:6px 0}.ai-message{border:1px solid var(--line);border-radius:8px;padding:12px 15px;font-size:12px;line-height:1.7}.ai-message.error{color:var(--danger,#b83c42)}.ai-message.success{color:var(--success,#238158)}.ai-loading{text-align:center;color:var(--muted);padding:25px}
.ai-settings-page,.ai-settings-header>div,.ai-provider>div,.ai-enable>span{min-width:0}.ai-settings-page p,.ai-settings-page small{overflow-wrap:anywhere}.ai-input input,.ai-input select{min-width:0;max-width:100%;box-sizing:border-box}.ai-provider-mark,.ai-config-status,.ai-enable input{flex:none}.ai-settings-card footer{flex-wrap:wrap}
.ai-capabilities{display:grid;grid-template-columns:repeat(auto-fit,minmax(min(100%,260px),1fr));gap:12px;margin-top:20px}.ai-capabilities article{border:1px solid var(--line);border-radius:10px;background:var(--surface);padding:18px 20px;min-width:0}.ai-capabilities h2{font-size:13px;margin:0 0 9px;color:var(--ink)}.ai-capabilities p{font-size:12px;line-height:1.9;color:var(--muted);margin:0}
@media(max-width:640px){.ai-settings-header{gap:12px}.ai-settings-header h1{font-size:23px}.ai-settings-card fieldset{padding:16px;margin:0}.ai-settings-card footer{padding:14px 16px;gap:8px}.ai-settings-card footer>span{display:none}.ai-settings-card footer :deep(button){flex:1;white-space:normal;height:auto;min-height:42px;padding-block:9px}.ai-provider{gap:9px;flex-wrap:wrap}.ai-config-status{margin-left:0}.ai-input input,.ai-input select{font-size:16px}.ai-boundary{padding:16px}.ai-enable input{width:20px;height:20px}}
</style>

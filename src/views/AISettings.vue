<script setup lang="ts">
import AIButton from '../components/AIButton.vue'
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { onBeforeRouteLeave, onBeforeRouteUpdate } from 'vue-router'
import { t } from '../i18n'
import { useSettingsScope } from '../components/settingsScope'
import { Button } from '../components/ui/button'
type Settings={provider:string;configured:boolean;enabled:boolean;model:string;models:string[];canManage:boolean}
const scope=useSettingsScope(),settings=ref<Settings|null>(null),apiKey=ref(''),model=ref(''),enabled=ref(false),loading=ref(true),saving=ref(false),error=ref(''),notice=ref('')
const dirty=computed(()=>!!apiKey.value.trim()||!!settings.value&&(model.value!==settings.value.model||enabled.value!==settings.value.enabled))
const disabled=computed(()=>loading.value||saving.value||scope.locked.value||!settings.value?.canManage)
let requestVersion=0,disposed=false,projectLeaveApproved=false
function current(version:number){return !disposed&&version===requestVersion&&scope.current()}
function validate(value:Settings){if(!value||value.provider!=='openai'||typeof value.configured!=='boolean'||typeof value.enabled!=='boolean'||typeof value.canManage!=='boolean'||!Array.isArray(value.models)||!value.models.length||value.models.some(item=>typeof item!=='string'||!item)||typeof value.model!=='string'||!value.model)throw Error('AI 设置返回格式不正确，请重试');return value}
function apply(value:Settings){validate(value);settings.value={provider:value.provider,configured:value.configured,enabled:value.enabled,model:value.model,models:[...value.models],canManage:value.canManage};model.value=value.model;enabled.value=value.enabled;apiKey.value=''}
function scrub(){++requestVersion;apiKey.value='';settings.value=null;model.value='';enabled.value=false;loading.value=false;saving.value=false;error.value='';notice.value='';projectLeaveApproved=false}
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
 if(enabled.value&&!settings.value.configured&&!apiKey.value.trim()){error.value='请先填写 API 密钥';return}
 if(model.value!==settings.value.model&&!settings.value.models.includes(model.value)){error.value='请选择服务端支持的模型';return}
 const body:Record<string,unknown>={};if(apiKey.value.trim())body.apiKey=apiKey.value.trim();if(enabled.value!==settings.value.enabled)body.enabled=enabled.value;if(model.value!==settings.value.model)body.model=model.value
 const version=++requestVersion;saving.value=true
 try{const data=await scope.request<Settings>('/organization/ai-settings',{method:'PATCH',body:JSON.stringify(body)});if(current(version)){apply(data);notice.value='AI 设置已保存；保存本身不会发起模型调用'}}
 catch(cause){if(current(version))error.value=cause instanceof Error?cause.message:'AI 设置保存失败，请重试'}
 finally{if(current(version))saving.value=false}
}
async function remove(){
 if(disabled.value||!settings.value?.configured||!scope.current()||!window.confirm(t('移除 API 密钥并停用 AI 功能？未保存的密钥草稿也会被放弃。')))return
 const version=++requestVersion;saving.value=true;error.value='';notice.value=''
 try{const data=await scope.request<Settings>('/organization/ai-settings',{method:'PATCH',body:JSON.stringify({clear:true})});if(current(version)){apply(data);notice.value='API 密钥已移除，AI 功能已停用'}}
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
  <header class="ai-settings-header"><div><span class="eyebrow">{{t('企业管理')}}</span><h1>{{t('AI 服务设置')}}</h1><p>{{t('由企业管理员统一配置模型，支持需求标题总结和测试用例生成。')}}</p></div><Button variant="outline" :disabled="loading||saving||scope.locked.value" @click="load">{{t('刷新')}}</Button></header>
  <p v-if="scope.locked.value" class="ai-message error" role="alert">{{t('项目或账号已变化，请刷新页面后继续')}}</p><p v-if="error" class="ai-message error" role="alert">{{t(error)}}</p><p v-if="notice" class="ai-message success" role="status">{{t(notice)}}</p>
  <p v-if="loading&&!settings" class="ai-loading" role="status">{{t('正在加载 AI 设置…')}}</p>
  <form v-if="settings&&!scope.locked.value" class="ai-settings-card" @submit.prevent="save">
   <fieldset :disabled="disabled">
    <div class="ai-provider"><span class="ai-provider-mark">AI</span><div><b>OpenAI</b><small>{{t(settings.configured?'API 密钥已配置':'尚未配置 API 密钥')}}</small></div><span class="ai-config-status">{{t(settings.enabled?'已启用':'已停用')}}</span></div>
    <label class="ai-input"><span>{{t(settings.configured?'替换 API 密钥':'API 密钥')}}</span><input v-model="apiKey" type="password" autocomplete="off" autocapitalize="off" spellcheck="false" maxlength="1024" :placeholder="t(settings.configured?'留空保留已有密钥':'输入团队授权的 API 密钥')"><small>{{t('密钥只在输入时保留于内存；服务端保存后不回显，不写入浏览器存储。')}}</small></label>
    <label class="ai-input"><span>{{t('生成模型')}}</span><select v-model="model"><option v-if="model&&!settings.models.includes(model)" :value="model" disabled>{{model}}</option><option v-for="value in settings.models" :key="value" :value="value">{{value}}{{value==='gpt-5.6'?t('（Sol 别名）'):value==='gpt-5.6-cyber'?t('（安全专用，需审批）'):''}}</option></select><small>{{t('仅展示服务端支持的模型；页面不能修改服务商或外部请求地址。')}}</small></label>
    <div class="ai-model-guidance"><p>{{t('GPT-6 Astra：复杂推理；GPT-5.6 Sol：旗舰能力；Terra：均衡；Luna：低成本。')}}</p><p>{{t('统一使用 Responses API；模型是否可调用取决于 API 账号权限，Cyber 需要单独审批。保存不会验证模型权限或自动发起付费调用。')}}</p><p>{{t('GPT-5.6 Pro 是推理模式，不是单独模型名称；当前业务保持低推理强度，不自动开启 Pro。')}}</p><a href="https://developers.openai.com/api/docs/models/all" target="_blank" rel="noopener noreferrer">{{t('查看官方模型目录')}}</a></div>
    <label class="ai-enable"><span><b>{{t('启用 AI 服务')}}</b><small>{{t('不会自动发送需求；每次生成均需成员明确确认。')}}</small></span><input v-model="enabled" type="checkbox" role="switch" :aria-label="t('启用 AI 服务')"></label>
   </fieldset>
   <footer><Button v-if="settings.configured" type="button" variant="ghost" :disabled="disabled" @click="remove">{{t('移除密钥')}}</Button><span></span><Button v-if="dirty" type="button" variant="outline" :disabled="saving" @click="discard">{{t('放弃修改')}}</Button><AIButton type="submit" :busy="saving" :disabled="disabled||!dirty">{{t(saving?'保存中…':'保存 AI 设置')}}</AIButton></footer>
  </form>
  <section class="ai-capabilities" :aria-label="t('已接入的 AI 能力')">
   <article><h2>{{t('需求标题总结')}}</h2><p>{{t('标题为空时，仅发送需求描述的文字内容。生成后先预览并确认，再保存需求。')}}</p></article>
   <article><h2>{{t('测试用例生成')}}</h2><p>{{t('发送已保存需求的标题、正文和验收标准；人工审核后再导入用例草稿。')}}</p></article>
  </section>
  <aside class="ai-boundary"><h2>{{t('数据发送边界')}}</h2><p>{{t('不会发送附件、图片、评论或人员资料。不要提交未经授权的个人信息、客户密钥或商业机密。')}}</p><p>{{t('生成结果需人工复核；只有确认导入后才会创建测试用例草稿。保存配置不会验证密钥，也不会产生模型调用。')}}</p></aside>
 </div>
</template>
<style scoped>
.ai-model-guidance{font-size:12px;line-height:1.7;margin-bottom:16px;color:var(--muted)}.ai-model-guidance a{color:var(--primary)}

.ai-settings-page{max-width:980px;margin:auto;color:var(--ink)}.ai-settings-header{display:flex;justify-content:space-between;align-items:flex-start;gap:18px;margin-bottom:25px}.ai-settings-header h1{font-size:26px;margin:8px 0}.ai-settings-header p{margin:0;color:var(--muted);font-size:12px;line-height:1.7}.ai-settings-card{border:1px solid var(--line);border-radius:12px;background:var(--surface);overflow:hidden}.ai-settings-card fieldset{padding:24px;border:0;min-width:0}.ai-provider{display:flex;align-items:center;gap:12px;padding-bottom:22px;border-bottom:1px solid var(--line);margin-bottom:23px}.ai-provider-mark{width:40px;height:40px;display:grid;place-items:center;background:var(--primary-soft);color:var(--primary);border-radius:9px;font-size:14px;font-weight:700}.ai-provider b{font-size:15px}.ai-provider small{display:block;color:var(--muted);font-size:11px;margin-top:5px}.ai-config-status{margin-left:auto;font-size:11px;color:var(--primary);border:1px solid var(--line);border-radius:5px;padding:4px 7px}.ai-input{display:grid;gap:9px;margin-bottom:24px;font-size:12px}.ai-input>span{font-weight:600}.ai-input input,.ai-input select{border:1px solid var(--line);border-radius:7px;padding:11px;background:var(--surface);color:var(--ink);font:inherit;width:100%}.ai-input small{color:var(--muted);font-size:11px;line-height:1.7}.ai-enable{display:flex;align-items:center;justify-content:space-between;gap:15px;padding-top:17px;border-top:1px solid var(--line);font-size:12px}.ai-enable small{display:block;margin-top:5px;color:var(--muted);font-size:11px;line-height:1.7}.ai-enable input{width:18px;height:18px;accent-color:var(--primary)}.ai-settings-card footer{display:flex;align-items:center;gap:9px;border-top:1px solid var(--line);padding:17px 24px}.ai-settings-card footer>span{flex:1}.ai-boundary{margin-top:20px;border:1px solid var(--line);border-radius:10px;background:var(--surface-soft,var(--surface));padding:20px 23px}.ai-boundary h2{font-size:13px;margin:0 0 10px}.ai-boundary p{font-size:12px;line-height:1.9;color:var(--muted);margin:6px 0}.ai-message{border:1px solid var(--line);border-radius:8px;padding:12px 15px;font-size:12px;line-height:1.7}.ai-message.error{color:var(--danger,#b83c42)}.ai-message.success{color:var(--success,#238158)}.ai-loading{text-align:center;color:var(--muted);padding:25px}
.ai-settings-page,.ai-settings-header>div,.ai-provider>div,.ai-enable>span{min-width:0}.ai-settings-page p,.ai-settings-page small{overflow-wrap:anywhere}.ai-input input,.ai-input select{min-width:0;max-width:100%;box-sizing:border-box}.ai-provider-mark,.ai-config-status,.ai-enable input{flex:none}.ai-settings-card footer{flex-wrap:wrap}
.ai-capabilities{display:grid;grid-template-columns:repeat(auto-fit,minmax(min(100%,260px),1fr));gap:12px;margin-top:20px}.ai-capabilities article{border:1px solid var(--line);border-radius:10px;background:var(--surface);padding:18px 20px;min-width:0}.ai-capabilities h2{font-size:13px;margin:0 0 9px;color:var(--ink)}.ai-capabilities p{font-size:12px;line-height:1.9;color:var(--muted);margin:0}
@media(max-width:640px){.ai-settings-header{gap:12px}.ai-settings-header h1{font-size:23px}.ai-settings-card fieldset{padding:16px;margin:0}.ai-settings-card footer{padding:14px 16px;gap:8px}.ai-settings-card footer>span{display:none}.ai-settings-card footer :deep(button){flex:1;white-space:normal;height:auto;min-height:42px;padding-block:9px}.ai-provider{gap:9px;flex-wrap:wrap}.ai-config-status{margin-left:0}.ai-input input,.ai-input select{font-size:16px}.ai-boundary{padding:16px}.ai-enable input{width:20px;height:20px}}
</style>

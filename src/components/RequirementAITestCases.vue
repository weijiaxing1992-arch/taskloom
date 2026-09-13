<script setup lang="ts">
import AIButton from './AIButton.vue'
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { t } from '../i18n'
import { useSettingsScope } from './settingsScope'
import { Button } from './ui/button'
type Capability={configured:boolean;enabled:boolean;model:string;canGenerate:boolean;inputFields:string[];maxCases:number;focusOptions?:string[];baseUrl?:string}
type PreviewCase={index:number;title:string;preconditions:string;priority:string;caseType:string;stepsDetail:{order:number;action:string;expected:string}[]}
type Draft={draftId:string;requirementId:number;requirementUpdatedAt:string;expiresAt:string;cases:PreviewCase[];reused?:boolean}
type Imported={items:{id:number;code:string;title:string}[];importedCount:number;replayed:boolean}
const props=defineProps<{requirementId:number;requirementUpdatedAt:string;disabled?:boolean;hasUnsavedChanges?:boolean;libraryId?:number|null;folderId?:number|null}>()
const emit=defineEmits<{(event:'imported',value:Imported):void;(event:'busy',value:boolean):void;(event:'busy-change',value:boolean):void;(event:'dirty',value:boolean):void}>()
const focusChoices=[
 {value:'normal',label:'常规流程'},{value:'boundary',label:'边界条件'},{value:'permission',label:'权限与角色'},{value:'failure',label:'异常与恢复'},
 {value:'security',label:'安全风险'},{value:'performance',label:'性能与容量'},{value:'compatibility',label:'兼容性'},{value:'automation',label:'自动化回归'},
]
const scope=useSettingsScope(),capability=ref<Capability|null>(null),loading=ref(true),generating=ref(false),importing=ref(false),error=ref(''),notice=ref(''),consent=ref(false),draft=ref<Draft|null>(null),selected=ref<number[]>([]),imported=ref(false),focus=ref<string[]>([]),extraInstructions=ref(''),count=ref(5)
const saving=computed(()=>generating.value||importing.value),dirty=computed(()=>!!draft.value&&!imported.value)
const stale=computed(()=>!!draft.value&&draft.value.requirementUpdatedAt!==props.requirementUpdatedAt)
const expired=computed(()=>!!draft.value&&Date.parse(draft.value.expiresAt)<=now.value),now=ref(Date.now())
const blocked=computed(()=>saving.value||scope.locked.value||props.disabled===true)
const canGenerate=computed(()=>!blocked.value&&!loading.value&&!!capability.value?.canGenerate&&!!capability.value.configured&&!!capability.value.enabled&&!!props.requirementUpdatedAt&&!props.hasUnsavedChanges)
const canImport=computed(()=>!blocked.value&&!loading.value&&!!capability.value?.canGenerate&&!!draft.value&&!imported.value&&!stale.value&&!expired.value&&selected.value.length>0&&!props.hasUnsavedChanges)
const supportedFocus=computed(()=>{const allowed=capability.value?.focusOptions;return Array.isArray(allowed)&&allowed.length?focusChoices.filter(item=>allowed.includes(item.value)):focusChoices})
const countOptions=computed(()=>Array.from({length:Math.max(1,Math.min(10,capability.value?.maxCases||10))},(_,index)=>index+1))
const importDestination=computed(()=>{
 const library=Number.isSafeInteger(props.libraryId)&&Number(props.libraryId)>0?Number(props.libraryId):null
 const folder=library&&Number.isSafeInteger(props.folderId)&&Number(props.folderId)>0?Number(props.folderId):null
 return {library,folder}
})
let version=0,disposed=false,timer:ReturnType<typeof setInterval>|undefined
function current(request:number,id:number){return !disposed&&request===version&&id===props.requirementId&&scope.current()}
function reset(){++version;capability.value=null;draft.value=null;selected.value=[];consent.value=false;imported.value=false;focus.value=[];extraInstructions.value='';count.value=5;loading.value=false;generating.value=false;importing.value=false;error.value='';notice.value=''}
async function load(){
 if(saving.value||!scope.current()||disposed||!Number.isSafeInteger(props.requirementId)||props.requirementId<1)return
 const request=++version,id=props.requirementId;loading.value=true;error.value=''
 try{const data=await scope.request<Capability>('/requirements/'+id+'/ai-test-cases');if(current(request,id)){if(!data||typeof data.configured!=='boolean'||typeof data.enabled!=='boolean'||typeof data.canGenerate!=='boolean'||typeof data.model!=='string'||!Array.isArray(data.inputFields)||!Number.isSafeInteger(data.maxCases)||data.maxCases<1||data.maxCases>10||data.focusOptions!==undefined&&(!Array.isArray(data.focusOptions)||data.focusOptions.some(item=>typeof item!=='string'||!focusChoices.some(choice=>choice.value===item))))throw Error('AI 生成能力返回格式不正确，请重试');capability.value=data;focus.value=focus.value.filter(item=>supportedFocus.value.some(choice=>choice.value===item));count.value=Math.max(1,Math.min(data.maxCases,count.value))}}
 catch(cause){if(current(request,id))error.value=cause instanceof Error?cause.message:'AI 生成能力暂时无法加载，请重试'}
 finally{if(current(request,id))loading.value=false}
}
async function generate(){
 if(!canGenerate.value||!consent.value)return
 if(dirty.value&&!window.confirm(t('重新生成将替换未导入的预览，继续吗？')))return
 const request=++version,id=props.requirementId,updatedAt=props.requirementUpdatedAt;generating.value=true;error.value='';notice.value=''
 try{
  const safeFocus=focus.value.filter(item=>supportedFocus.value.some(choice=>choice.value===item));const requestedCount=Math.max(1,Math.min(capability.value?.maxCases||10,Math.round(count.value)||1));const instructions=extraInstructions.value.trim()
  const result=await scope.request<Draft>('/requirements/'+id+'/ai-test-cases',{method:'POST',body:JSON.stringify({confirmed:true,requirementUpdatedAt:updatedAt,focus:safeFocus,count:requestedCount,...(instructions?{extraInstructions:instructions}:{}),...(draft.value?{forceNew:true}:{})})})
  if(!current(request,id))return
  if(!result||result.requirementId!==id||typeof result.draftId!=='string'||!result.draftId||typeof result.requirementUpdatedAt!=='string'||!result.requirementUpdatedAt||!Array.isArray(result.cases)||!result.cases.length||result.cases.length>(capability.value?.maxCases||10)||typeof result.expiresAt!=='string'||!Number.isFinite(Date.parse(result.expiresAt))||result.cases.some(item=>!item||!Number.isSafeInteger(item.index)||item.index<0||typeof item.title!=='string'||!item.title.trim()||typeof item.preconditions!=='string'||typeof item.priority!=='string'||typeof item.caseType!=='string'||!Array.isArray(item.stepsDetail)||item.stepsDetail.some(step=>!step||!Number.isSafeInteger(step.order)||step.order<1||typeof step.action!=='string'||typeof step.expected!=='string'))||new Set(result.cases.map(item=>item.index)).size!==result.cases.length)throw Error('AI 用例预览格式不正确，请重新生成')
  draft.value=result;selected.value=[];imported.value=false;consent.value=false;notice.value=result.reused===true?'已恢复近期相同输入的预览，本次未再次调用外部 AI 服务。请复核并选择需要导入的用例。':'用例预览已生成，请逐条复核并选择需要导入的用例'
 }catch(cause){if(current(request,id))error.value=cause instanceof Error?cause.message:'AI 生成失败，请重试'}
 finally{if(current(request,id))generating.value=false}
}
function toggle(index:number){if(blocked.value||imported.value||!draft.value?.cases.some(item=>item.index===index))return;selected.value=selected.value.includes(index)?selected.value.filter(value=>value!==index):[...selected.value,index]}
function selectAll(){if(blocked.value||imported.value||!draft.value)return;selected.value=selected.value.length===draft.value.cases.length?[]:draft.value.cases.map(item=>item.index)}
async function importCases(){
 if(!canImport.value||!draft.value)return
 const request=++version,id=props.requirementId,indexes=[...selected.value].sort((a,b)=>a-b),draftId=draft.value.draftId;importing.value=true;error.value='';notice.value=''
 try{const destination=importDestination.value;const result=await scope.request<Imported>('/requirements/'+id+'/ai-test-cases/import',{method:'POST',body:JSON.stringify({draftId,indexes,...(destination.library?{libraryId:destination.library,folderId:destination.folder}: {})})});if(!current(request,id))return;if(!result||!Array.isArray(result.items)||!Number.isSafeInteger(result.importedCount)||result.importedCount!==indexes.length||result.items.length!==result.importedCount||result.items.some(item=>!item||!Number.isSafeInteger(item.id)||item.id<1))throw Error('导入结果暂时无法确认，请保持选择后重试');imported.value=true;notice.value='所选测试用例已作为草稿导入，请在测试模块继续完善';emit('imported',result)}
 catch(cause){if(current(request,id))error.value=cause instanceof Error?cause.message:'测试用例导入失败，请保持选择后重试'}
 finally{if(current(request,id))importing.value=false}
}
function discard(){if(blocked.value)return;if(dirty.value&&!window.confirm(t('放弃未导入的 AI 用例预览？不会创建或删除正式用例。')))return;draft.value=null;selected.value=[];imported.value=false;notice.value='';error.value=''}
function canLeave(){return !saving.value&&(!dirty.value||window.confirm(t('AI 用例预览尚未导入，离开将放弃当前预览，继续吗？')))}
function beforeUnload(event:BeforeUnloadEvent){if(dirty.value||saving.value){event.preventDefault();event.returnValue=''}}
watch(saving,value=>{emit('busy',value);emit('busy-change',value)},{flush:'sync'});watch(dirty,value=>emit('dirty',value),{flush:'sync'})
watch(scope.locked,value=>{if(value)reset()},{flush:'sync'})
watch(()=>props.requirementId,()=>{reset();void load()})
watch(()=>props.requirementUpdatedAt,()=>{consent.value=false})
onMounted(()=>{void load();timer=setInterval(()=>now.value=Date.now(),10000);window.addEventListener('beforeunload',beforeUnload)})
onBeforeUnmount(()=>{disposed=true;reset();clearInterval(timer);window.removeEventListener('beforeunload',beforeUnload)})
defineExpose({canLeave,saving,dirty})
</script>
<template>
 <section class="ai-test-cases" :aria-busy="loading||saving">
  <header><div><span class="ai-case-badge">AI</span><h3>{{t('AI 生成测试用例')}}</h3></div><Button size="sm" variant="ghost" :disabled="loading||saving||scope.locked.value" @click="load">{{t('刷新')}}</Button></header>
  <p v-if="scope.locked.value" class="ai-case-alert" role="alert">{{t('项目或账号已变化，请刷新页面后继续')}}</p><p v-if="error" class="ai-case-alert" role="alert">{{t(error)}}</p><p v-if="notice" class="ai-case-notice" role="status">{{t(notice)}}</p>
  <p v-if="loading&&!capability" class="ai-case-hint" role="status">{{t('正在检查 AI 生成功能…')}}</p>
  <template v-if="capability&&!scope.locked.value">
   <p v-if="!capability.configured||!capability.enabled" class="ai-case-hint">{{t('企业尚未启用 AI 生成功能，请联系企业管理员配置。')}}</p>
   <p v-else-if="!capability.canGenerate" class="ai-case-hint">{{t('当前身份没有生成测试用例的权限。')}}</p>
   <template v-else>
    <p class="ai-case-disclosure">{{t('仅使用已保存的需求标题、正文和验收标准，并发送给企业配置的 AI 服务。附件、评论和未保存草稿不会发送。')}}</p>
    <details class="ai-generation-settings">
     <summary>{{t('生成设置')}} <span>{{t('最多生成 {count} 条用例',{count})}}</span></summary>
     <p class="ai-case-hint">{{t('模型')}} <code>{{capability.model}}</code></p>
     <p v-if="capability.baseUrl" class="ai-case-hint">{{t('服务地址')}} · {{capability.baseUrl}}</p>
     <p class="ai-case-hint">{{t('相同内容与配置会优先恢复近期预览；点击重新生成会再次调用 AI。')}}</p>
     <fieldset class="ai-generation-options" :disabled="blocked||loading">
      <legend>{{t('生成偏好')}}</legend>
      <div class="ai-focus-options"><span>{{t('重点覆盖')}}</span><label v-for="choice in supportedFocus" :key="choice.value"><input v-model="focus" type="checkbox" :value="choice.value"/><span>{{t(choice.label)}}</span></label></div>
      <div class="ai-generation-fields"><label><span>{{t('生成数量')}}</span><select v-model.number="count" :aria-label="t('生成数量')"><option v-for="value in countOptions" :key="value" :value="value">{{value}}</option></select></label><label class="ai-extra-instructions"><span>{{t('补充生成说明')}}</span><textarea v-model="extraInstructions" maxlength="1000" rows="2" :placeholder="t('只补充业务背景或覆盖侧重点；不会改变已保存需求。')"/></label></div>
      <p class="ai-case-hint">{{importDestination.library?t('目标用例库 / 目录由测试用例库选择器决定。'):t('默认用例库根目录')}}</p>
     </fieldset>
    </details>
    <p v-if="hasUnsavedChanges" class="ai-case-warning">{{t('需求有未保存修改，请先保存或还原，再生成或导入用例。')}}</p>
    <label class="ai-case-consent"><input v-model="consent" type="checkbox" :disabled="blocked||loading"><span>{{t('我确认这些内容已获授权，可以发送给外部 AI 服务。')}}</span></label>
    <div class="ai-generate-action"><AIButton :busy="generating" :disabled="!canGenerate||!consent" @click="generate">{{t(generating?'正在生成，请稍候…':draft?'重新生成预览':'生成用例预览')}}</AIButton><small>{{t('此操作会调用外部模型，可能产生服务商费用。')}}</small></div>
   </template>
  </template>
  <section v-if="draft&&!scope.locked.value" class="ai-preview"><header><div><b>{{t('用例预览')}}</b><small>{{t('已选择 {selected} / {total} 条',{selected:selected.length,total:draft.cases.length})}}</small></div><Button size="sm" variant="outline" :disabled="blocked||imported" @click="selectAll">{{t(selected.length===draft.cases.length?'取消全选':'全选')}}</Button></header><p v-if="stale||expired" class="ai-case-warning">{{t(stale?'需求内容已更新，请重新生成后再导入。':'用例预览已过期，请重新生成。')}}</p><article v-for="item in draft.cases" :key="item.index" :class="{selected:selected.includes(item.index)}"><label class="ai-preview-title"><input type="checkbox" :checked="selected.includes(item.index)" :disabled="blocked||imported" @change="toggle(item.index)"><strong>{{item.title}}</strong><span>{{item.priority}}</span></label><dl><dt>{{t('前置条件')}}</dt><dd>{{item.preconditions||'—'}}</dd></dl><ol><li v-for="step in item.stepsDetail" :key="step.order"><b>{{step.action}}</b><p><span>{{t('预期结果')}}：</span>{{step.expected}}</p></li></ol></article><footer><p>{{t('导入后为测试用例草稿；需要人工校验。网络重试相同选择不会重复导入。')}}</p><div><Button variant="ghost" :disabled="blocked" @click="discard">{{t(imported?'关闭预览':'放弃预览')}}</Button><Button :disabled="!canImport" @click="importCases">{{t(importing?'正在导入…':imported?'已导入':'确认导入所选用例')}}</Button></div></footer></section>
 </section>
</template>
<style scoped>
.ai-generation-settings{margin:12px 0;border:1px solid var(--line);border-radius:8px;padding:10px 12px}.ai-generation-settings>summary{cursor:pointer;font-size:12px;font-weight:600;line-height:1.8}.ai-generation-settings>summary>span{font-weight:400;color:var(--muted);margin-left:8px}.ai-generation-settings>summary:focus-visible{outline:2px solid var(--primary);outline-offset:4px;border-radius:3px}
.ai-test-cases{margin:20px 0;border:1px solid var(--line);border-radius:10px;padding:18px;background:var(--surface);color:var(--ink)}.ai-test-cases>header,.ai-test-cases>header>div{display:flex;align-items:center;gap:8px}.ai-test-cases>header{justify-content:space-between;margin-bottom:13px}.ai-test-cases h3{font-size:14px;margin:0}.ai-case-badge{font-size:10px;font-weight:700;padding:4px;border-radius:4px;background:var(--primary-soft);color:var(--primary)}.ai-case-hint,.ai-case-disclosure{font-size:12px;color:var(--muted);line-height:1.8;margin:9px 0}.ai-case-disclosure{border:1px solid var(--line);border-radius:7px;padding:12px;background:var(--surface-soft,var(--surface))}.ai-generation-options{border:1px solid var(--line);border-radius:8px;margin:12px 0;padding:10px 12px;min-inline-size:0}.ai-generation-options legend{font-size:12px;font-weight:650;padding:0 4px}.ai-focus-options{display:flex;align-items:center;gap:7px;flex-wrap:wrap;font-size:11px;line-height:1.5}.ai-focus-options>span{color:var(--muted);margin-right:2px}.ai-focus-options label{display:inline-flex;align-items:center;gap:4px;padding:4px 7px;border:1px solid var(--line);border-radius:999px;cursor:pointer}.ai-focus-options input{accent-color:var(--primary);margin:0}.ai-generation-fields{display:grid;grid-template-columns:minmax(106px,.24fr) minmax(0,1fr);gap:10px;margin-top:10px}.ai-generation-fields label{display:grid;gap:4px;font-size:11px;color:var(--muted)}.ai-generation-fields select,.ai-generation-fields textarea{width:100%;box-sizing:border-box;border:1px solid var(--line);border-radius:6px;background:var(--surface);color:var(--ink);font:inherit;padding:7px 8px}.ai-generation-fields textarea{resize:vertical;min-height:52px;line-height:1.5}.ai-case-consent{display:flex;align-items:flex-start;gap:7px;font-size:12px;line-height:1.7;margin:15px 0}.ai-case-consent input,.ai-preview input{accent-color:var(--primary);flex:none;width:15px;margin:3px 0 0}.ai-generate-action{display:flex;align-items:center;gap:11px;flex-wrap:wrap}.ai-generate-action small{color:var(--muted);font-size:10px}.ai-case-alert,.ai-case-notice,.ai-case-warning{font-size:12px;line-height:1.7;padding:10px 12px;border-radius:6px;border:1px solid var(--line);margin:9px 0}.ai-case-alert{color:var(--danger,#bf3944)}.ai-case-notice{color:var(--success,#228158)}.ai-case-warning{color:var(--warning,#a66d15)}.ai-preview{border-top:1px solid var(--line);margin-top:20px;padding-top:15px}.ai-preview>header{display:flex;justify-content:space-between;align-items:center;margin-bottom:13px;gap:10px;font-size:12px}.ai-preview>header small{font-size:10px;color:var(--muted);margin-left:8px}.ai-preview article{border:1px solid var(--line);border-radius:8px;margin:10px 0;padding:13px 14px}.ai-preview article.selected{border-color:color-mix(in srgb,var(--primary) 50%,var(--line));background:color-mix(in srgb,var(--primary) 3%,var(--surface))}.ai-preview-title{display:flex;align-items:flex-start;gap:8px;line-height:1.7;font-size:12px;cursor:pointer}.ai-preview-title strong{flex:1}.ai-preview-title>span{font-size:10px;color:var(--muted)}.ai-preview dl{display:grid;grid-template-columns:65px 1fr;font-size:11px;line-height:1.8;margin:12px 0}.ai-preview dt{color:var(--muted)}.ai-preview dd{margin:0;white-space:pre-wrap;overflow-wrap:anywhere}.ai-preview ol{padding-left:23px;margin:0;font-size:11px;line-height:1.8}.ai-preview li{padding:5px 0}.ai-preview li>b{font-weight:500}.ai-preview li p{color:var(--muted);margin:3px 0;white-space:pre-wrap;overflow-wrap:anywhere}.ai-preview>footer{border-top:1px solid var(--line);margin-top:15px;padding-top:12px}.ai-preview>footer p{font-size:10px;line-height:1.8;color:var(--muted);margin:0 0 12px}.ai-preview>footer>div{display:flex;justify-content:flex-end;gap:8px;flex-wrap:wrap}
.ai-test-cases,.ai-preview,.ai-preview-title strong{min-width:0}.ai-test-cases p,.ai-test-cases small,.ai-preview-title strong,.ai-preview li>b{overflow-wrap:anywhere}.ai-preview-title>span{flex:none}.ai-preview>header{flex-wrap:wrap}.ai-preview>header>div{display:flex;align-items:baseline;gap:6px;flex-wrap:wrap}.ai-preview dl{grid-template-columns:65px minmax(0,1fr)}
@media(max-width:640px){.ai-preview article{padding:12px 10px}.ai-preview-title{gap:6px}.ai-preview dl{grid-template-columns:1fr;gap:3px}.ai-preview ol{padding-left:20px}.ai-generation-fields{grid-template-columns:1fr}.ai-generate-action{align-items:stretch}.ai-generate-action :deep(button){width:100%}.ai-preview>footer>div :deep(button){flex:1}.ai-test-cases :deep(button){height:auto;min-height:40px;white-space:normal;overflow-wrap:anywhere;padding-block:8px}.ai-case-consent input,.ai-preview input{width:18px;height:18px}.ai-case-consent{gap:9px}.ai-preview>header small{margin-left:0}}
</style>

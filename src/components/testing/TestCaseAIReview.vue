<script setup lang="ts">
import AIButton from '../AIButton.vue'
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { t } from '../../i18n'
import { useSettingsScope } from '../settingsScope'
import { Button } from '../ui/button'

type Mode='standard'|'logic'
type Capability={configured:boolean;enabled:boolean;model:string;canReview:boolean;modes:string[];baseUrl?:string}
type Issue={severity:'high'|'medium'|'low';field:string;message:string;suggestion:string}
type Review={summary:string;issues:Issue[];model:string;replayed?:boolean}
const props=defineProps<{caseId:number;updatedAt:string;disabled?:boolean}>()
const emit=defineEmits<{(event:'busy-change',value:boolean):void;(event:'change',value:{mode:Mode;review:Review;replayed:boolean}):void}>()
const scope=useSettingsScope(),capability=ref<Capability|null>(null),loading=ref(true),reviewing=ref(false),confirmed=ref(false),mode=ref<Mode>('standard'),review=ref<Review|null>(null),error=ref(''),notice=ref('')
const modes:Mode[]=['standard','logic']
let requestVersion=0,disposed=false
const blocked=computed(()=>props.disabled===true||scope.locked.value||loading.value||reviewing.value)
const canSubmit=computed(()=>!blocked.value&&!!capability.value?.configured&&!!capability.value?.enabled&&!!capability.value?.canReview&&!!props.updatedAt&&confirmed.value)
function current(sequence:number,id:number,updatedAt:string){return !disposed&&sequence===requestVersion&&id===props.caseId&&updatedAt===props.updatedAt&&scope.current()}
function reset(){++requestVersion;capability.value=null;loading.value=false;reviewing.value=false;confirmed.value=false;review.value=null;error.value='';notice.value=''}
function validIssue(value:unknown):value is Issue{return !!value&&typeof value==='object'&&['high','medium','low'].includes((value as Issue).severity)&&typeof (value as Issue).field==='string'&&(value as Issue).field.trim().length>0&&(value as Issue).field.length<=160&&typeof (value as Issue).message==='string'&&(value as Issue).message.trim().length>0&&(value as Issue).message.length<=2000&&typeof (value as Issue).suggestion==='string'&&(value as Issue).suggestion.trim().length>0&&(value as Issue).suggestion.length<=2000}
function validReview(value:unknown):value is Review{return !!value&&typeof value==='object'&&typeof (value as Review).summary==='string'&&(value as Review).summary.trim().length>0&&(value as Review).summary.length<=4000&&typeof (value as Review).model==='string'&&(value as Review).model.length>0&&(value as Review).model.length<=100&&Array.isArray((value as Review).issues)&&(value as Review).issues.length<=20&&(value as Review).issues.every(validIssue)}
async function load(){
 if(scope.locked.value||disposed||!Number.isSafeInteger(props.caseId)||props.caseId<1)return
 const sequence=++requestVersion,id=props.caseId,updatedAt=props.updatedAt;loading.value=true;error.value=''
 try{const data=await scope.request<Capability>('/test-cases/'+id+'/ai-review');if(current(sequence,id,updatedAt)){if(!data||typeof data.configured!=='boolean'||typeof data.enabled!=='boolean'||typeof data.canReview!=='boolean'||typeof data.model!=='string'||!Array.isArray(data.modes)||!data.modes.every(value=>value==='standard'||value==='logic'))throw Error('AI 审查能力返回格式不正确，请重试');capability.value=data;if(!data.modes.includes(mode.value))mode.value=data.modes.includes('standard')?'standard':'logic'}}
 catch(cause){if(current(sequence,id,updatedAt))error.value=cause instanceof Error?cause.message:'AI 审查能力暂时无法加载，请重试'}
 finally{if(current(sequence,id,updatedAt))loading.value=false}
}
async function start(){
 if(!canSubmit.value||!confirmed.value)return
 const sequence=++requestVersion,id=props.caseId,updatedAt=props.updatedAt,currentMode=mode.value;reviewing.value=true;error.value='';notice.value=''
 try{const data=await scope.request<Review>('/test-cases/'+id+'/ai-review',{method:'POST',body:JSON.stringify({confirmed:true,caseUpdatedAt:updatedAt,mode:currentMode})});if(!current(sequence,id,updatedAt))return;if(!validReview(data))throw Error('AI 审查返回格式不正确，请重试');review.value=data;confirmed.value=false;const replayed=data.replayed===true;notice.value=replayed?'该版本的审查结果已复用':'审查已按当前版本完成';emit('change',{mode:currentMode,review:data,replayed})}
 catch(cause){if(current(sequence,id,updatedAt))error.value=cause instanceof Error?cause.message:'AI 审查失败，请重试'}
 finally{if(current(sequence,id,updatedAt))reviewing.value=false}
}
function severityLabel(value:Issue['severity']){return value==='high'?'严重':value==='medium'?'一般':'提示'}
function canLeave(){return !reviewing.value}
watch(reviewing,value=>emit('busy-change',value),{flush:'sync'})
watch(scope.locked,value=>{if(value)reset()},{flush:'sync'})
watch([()=>props.caseId,()=>props.updatedAt],()=>{reset();void load()})
onMounted(()=>void load())
onBeforeUnmount(()=>{disposed=true;reset()})
defineExpose({canLeave,reviewing})
</script>

<template>
 <section class="test-case-ai-review" :aria-busy="loading||reviewing">
  <header><div><span class="test-ai-mark">AI</span><h3>{{t('AI 用例审查')}}</h3></div><Button size="sm" variant="ghost" :disabled="blocked||scope.locked.value" @click="load">{{t('刷新')}}</Button></header>
  <p v-if="scope.locked.value" class="test-ai-alert" role="alert">{{t('项目或账号已变化，请刷新页面后继续')}}</p><p v-if="error" class="test-ai-alert" role="alert">{{t(error)}}</p><p v-if="notice" class="test-ai-notice" role="status">{{t(notice)}}</p>
  <p v-if="loading&&!capability" class="test-ai-hint" role="status">{{t('正在检查 AI 生成功能…')}}</p>
  <template v-else-if="capability&&!scope.locked.value">
   <p v-if="!capability.configured||!capability.enabled" class="test-ai-hint">{{t('企业尚未启用 AI 生成功能，请联系企业管理员配置。')}}</p>
   <p v-else-if="!capability.canReview" class="test-ai-hint">{{t('当前身份没有审查测试用例的权限。')}}</p>
   <template v-else>
    <p class="test-ai-hint">{{t('模型')}} <code>{{capability.model}}</code></p><p v-if="capability.baseUrl" class="test-ai-hint">{{t('服务地址')}} · {{capability.baseUrl}}</p>
    <div class="test-ai-mode" role="radiogroup" :aria-label="t('审查模式')"><label v-for="choice in modes" :key="choice" :class="{active:mode===choice}"><input v-model="mode" type="radio" :value="choice" :disabled="blocked||!capability.modes.includes(choice)"/><span><b>{{t(choice==='standard'?'标准审查':'逻辑审查')}}</b><small>{{t(choice==='standard'?'审查覆盖结构、步骤完整性、可验证性和风险。':'审查业务逻辑、前后条件、边界和依赖一致性。')}}</small></span></label></div>
    <p class="test-ai-disclosure">{{t('仅发送标题、前置条件、步骤、预期结果、类型、优先级和当前项目可访问的关联需求摘要；不会发送评论、附件或未保存修改。')}}</p>
    <label class="test-ai-consent"><input v-model="confirmed" type="checkbox" :disabled="blocked"/><span>{{t('我确认可将当前已保存测试用例及其关联需求摘要发送给外部 AI 服务。')}}</span></label>
    <AIButton :busy="reviewing" :disabled="!canSubmit" @click="start">{{t(reviewing?'正在审查…':'开始 AI 审查')}}</AIButton>
   </template>
  </template>
  <section v-if="review" class="test-ai-result" aria-live="polite"><header><h4>{{t('审查结果')}}</h4><small>{{review.model}}</small></header><p class="test-ai-summary">{{review.summary}}</p><p v-if="!review.issues.length" class="test-ai-empty">{{t('未发现需要人工确认的问题')}}</p><ol v-else><li v-for="(issue,index) in review.issues" :key="`${issue.field}-${index}`"><span :class="['test-ai-severity',issue.severity]">{{t(severityLabel(issue.severity))}}</span><div><b>{{t('字段')}}：{{issue.field}}</b><p><strong>{{t('问题')}}：</strong>{{issue.message}}</p><p><strong>{{t('建议')}}：</strong>{{issue.suggestion}}</p></div></li></ol></section>
 </section>
</template>

<style scoped>
.test-case-ai-review{margin-top:18px;border:1px solid var(--line);border-radius:10px;padding:15px;background:var(--surface)}.test-case-ai-review>header,.test-case-ai-review>header>div,.test-ai-result>header{display:flex;align-items:center;gap:8px}.test-case-ai-review>header{justify-content:space-between;margin-bottom:10px}.test-case-ai-review h3,.test-ai-result h4{font-size:14px;margin:0}.test-ai-mark{font-size:10px;font-weight:700;border-radius:4px;padding:4px;background:var(--primary-soft);color:var(--primary)}.test-ai-hint,.test-ai-disclosure{font-size:12px;line-height:1.7;color:var(--muted);margin:9px 0}.test-ai-disclosure{border:1px solid var(--line);border-radius:7px;padding:10px;background:var(--surface-soft,var(--surface))}.test-ai-alert,.test-ai-notice{font-size:12px;line-height:1.7;border:1px solid var(--line);padding:9px 11px;border-radius:7px;margin:8px 0}.test-ai-alert{color:var(--danger,#bf3944)}.test-ai-notice{color:var(--success,#228158)}.test-ai-mode{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:8px;margin:12px 0}.test-ai-mode label{display:flex;align-items:flex-start;gap:7px;border:1px solid var(--line);border-radius:8px;padding:9px;cursor:pointer}.test-ai-mode label.active{border-color:color-mix(in srgb,var(--primary) 55%,var(--line));background:color-mix(in srgb,var(--primary) 4%,var(--surface))}.test-ai-mode input{accent-color:var(--primary);margin-top:3px}.test-ai-mode span{display:grid;gap:3px;min-width:0}.test-ai-mode b{font-size:12px}.test-ai-mode small{font-size:10px;line-height:1.5;color:var(--muted)}.test-ai-consent{display:flex;align-items:flex-start;gap:7px;font-size:12px;line-height:1.7;margin:13px 0}.test-ai-consent input{accent-color:var(--primary);width:15px;flex:none;margin:3px 0 0}.test-ai-result{border-top:1px solid var(--line);margin-top:16px;padding-top:14px}.test-ai-result>header{justify-content:space-between}.test-ai-result small{color:var(--muted);font-size:10px}.test-ai-summary{white-space:pre-wrap;overflow-wrap:anywhere;font-size:12px;line-height:1.75;margin:10px 0}.test-ai-empty{font-size:12px;color:var(--success,#228158);margin:10px 0}.test-ai-result ol{list-style:none;padding:0;margin:10px 0;display:grid;gap:8px}.test-ai-result li{display:grid;grid-template-columns:auto minmax(0,1fr);gap:8px;border:1px solid var(--line);border-radius:8px;padding:10px}.test-ai-result li div{min-width:0}.test-ai-result li b,.test-ai-result li p{font-size:12px;line-height:1.7;overflow-wrap:anywhere}.test-ai-result li p{margin:4px 0;color:var(--muted)}.test-ai-result li p strong{color:var(--ink)}.test-ai-severity{align-self:start;font-size:10px;border-radius:999px;padding:3px 6px;background:var(--surface-soft,var(--surface));color:var(--muted)}.test-ai-severity.high{background:#fff0f0;color:#b42318}.test-ai-severity.medium{background:#fff7e8;color:#9a6700}.test-ai-severity.low{background:#edf6ff;color:#175cd3}@media(max-width:640px){.test-ai-mode{grid-template-columns:1fr}.test-case-ai-review :deep(button){width:100%;height:auto;min-height:40px;white-space:normal}.test-ai-consent input{width:18px;height:18px}.test-ai-result li{grid-template-columns:1fr}.test-ai-severity{justify-self:start}}
</style>

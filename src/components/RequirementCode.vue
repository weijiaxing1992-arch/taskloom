<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, useId, watch } from 'vue'
import { api } from '../api'
import { t, formatDate } from '../i18n'
import { layoutScope } from '../layoutScope'
import { clipboardNeedsMembers, completeClipboardRequirement, requirementClipboard, type ClipboardMember, type ClipboardRequirement } from '../requirementClipboard'

const props=withDefaults(defineProps<{requirement:ClipboardRequirement;members?:ClipboardMember[];projectId?:string;disabled?:boolean;openOnClick?:boolean}>(),{members:()=>[],openOnClick:true})
const emit=defineEmits<{(event:'open'):void}>()
const busy=ref(false),invalid=ref(false),feedback=ref(''),failed=ref(false),fallbackText=ref('')
const trigger=ref<HTMLButtonElement|null>(null),dialog=ref<HTMLDialogElement|null>(null),textarea=ref<HTMLTextAreaElement|null>(null)
const labelID='requirement-copy-'+useId()
const pageProject=()=>{try{return localStorage.getItem('devflow-project')||'prj_orbit'}catch{return''}}
const targetProject=computed(()=>props.projectId||props.requirement.projectId||pageProject())
const scopeIdentity=layoutScope.value
let version=0,disposed=false,clickTimer:ReturnType<typeof setTimeout>|undefined,noticeTimer:ReturnType<typeof setTimeout>|undefined,controller:AbortController|undefined
const code=computed(()=>props.requirement.code||String(props.requirement.id))
function cancelClick(){clearTimeout(clickTimer);clickTimer=undefined}
function valid(){return !disposed&&!invalid.value&&!props.disabled&&!!scopeIdentity&&scopeIdentity===layoutScope.value}
function capture(){return{version:++version,id:Number(props.requirement.id),target:targetProject.value,page:pageProject(),identity:layoutScope.value}}
function current(request:ReturnType<typeof capture>){return valid()&&request.version===version&&request.id===Number(props.requirement.id)&&request.target===targetProject.value&&request.page===pageProject()&&request.identity===layoutScope.value}
function announce(message:string,isError=false){clearTimeout(noticeTimer);feedback.value=message;failed.value=isError;noticeTimer=setTimeout(()=>{feedback.value=''},5000)}
function click(event:MouseEvent){cancelClick();if(!valid()||busy.value||!props.openOnClick)return;if(event.detail===0){emit('open');return}if(event.detail>1)return;const id=props.requirement.id,project=targetProject.value;clickTimer=setTimeout(()=>{if(valid()&&!busy.value&&id===props.requirement.id&&project===targetProject.value)emit('open')},550)}
function press(event:MouseEvent){if(event.detail>1)cancelClick()}
function doubleClick(){cancelClick();void copy()}
function keydown(event:KeyboardEvent){if((event.ctrlKey||event.metaKey)&&event.key.toLowerCase()==='c'){event.preventDefault();event.stopPropagation();cancelClick();void copy()}}
function closeFallback(){if(dialog.value?.open)dialog.value.close();fallbackText.value='';if(valid())trigger.value?.focus()}
async function fallback(text:string,request:ReturnType<typeof capture>){
  if(!current(request))return
  fallbackText.value=text;announce('未能自动复制，请在弹窗中选择文本复制',true)
  await nextTick();if(!current(request)){fallbackText.value='';return}
  try{if(dialog.value?.showModal)dialog.value.showModal();else dialog.value?.setAttribute('open','')}catch{dialog.value?.setAttribute('open','')}
  textarea.value?.focus();textarea.value?.select()
}
async function writeClipboard(text:string,request:ReturnType<typeof capture>){
  if(!current(request))return
  try{if(!navigator.clipboard?.writeText)throw new Error('clipboard unavailable');await navigator.clipboard.writeText(text);if(current(request)){closeFallback();announce('已复制需求协作摘要')}}catch{if(current(request))await fallback(text,request)}
}
async function copy(){
  cancelClick();if(!valid()||busy.value)return
  const request=capture();if(!Number.isSafeInteger(request.id)||request.id<=0||!request.target){announce('需求编号无效，无法复制',true);return}
  if(props.requirement.objectType&&props.requirement.objectType!=='requirement'||['缺陷','迭代','测试用例','测试执行','测试计划','项目','defect'].includes(props.requirement.type||'')){announce('仅需求编号支持协作摘要',true);return}
  controller?.abort();controller=new AbortController();const options={headers:{'X-DevFlow-Project':request.target},signal:controller.signal}
  busy.value=true;feedback.value='';failed.value=false
  try{
    let item:ClipboardRequirement=JSON.parse(JSON.stringify(props.requirement)),members=[...props.members]
    if(!completeClipboardRequirement(item)){item=await api<ClipboardRequirement>('/requirements/'+request.id,options);if(!current(request))return;if(Number(item.id)!==request.id||item.projectId&&item.projectId!==request.target)throw new Error('需求协作信息与当前项目不一致')}
    if(item.projectId&&item.projectId!==request.target)throw new Error('需求协作信息与当前项目不一致')
    if(!completeClipboardRequirement(item))throw new Error('需求协作信息不完整，请刷新后重试')
    if(clipboardNeedsMembers(item,members)){const directory=await api<{items:ClipboardMember[]}>('/members',options);if(!current(request))return;if(!Array.isArray(directory.items))throw new Error('成员信息加载失败，未复制摘要');members=directory.items}
    if(current(request))await writeClipboard(requirementClipboard(item,members,{translate:t,formatDate:value=>formatDate(value,{year:'numeric',month:'2-digit',day:'2-digit',hour:'2-digit',minute:'2-digit',second:'2-digit',hour12:false,timeZoneName:'short'})}),request)
  }catch(cause){if(current(request))announce(cause instanceof Error?cause.message:'需求协作摘要加载失败，请重试',true)}
  finally{if(current(request))busy.value=false}
}
async function retry(){if(!valid()||busy.value||!fallbackText.value)return;const request=capture(),text=fallbackText.value;busy.value=true;try{await writeClipboard(text,request)}finally{if(current(request))busy.value=false}}
function reset(){version++;controller?.abort();cancelClick();clearTimeout(noticeTimer);busy.value=false;feedback.value='';closeFallback()}
function invalidate(){invalid.value=true;reset()}
watch(()=>[props.requirement.id,props.projectId,props.requirement.projectId],reset,{flush:'sync'})
watch(()=>props.disabled,disabled=>{if(disabled)reset()},{flush:'sync'})
watch(layoutScope,()=>{if(scopeIdentity!==layoutScope.value)invalidate()},{flush:'sync'})
onMounted(()=>{window.addEventListener('devflow-project-changed',invalidate);window.addEventListener('devflow-identity-changed',invalidate);window.addEventListener('devflow-auth-expired',invalidate)})
onBeforeUnmount(()=>{disposed=true;reset();window.removeEventListener('devflow-project-changed',invalidate);window.removeEventListener('devflow-identity-changed',invalidate);window.removeEventListener('devflow-auth-expired',invalidate)})
</script>

<template>
  <span class="requirement-code-control" @click.stop @dblclick.stop @keydown.stop>
    <button ref="trigger" type="button" class="requirement-code-value" :disabled="disabled||invalid||busy" :aria-busy="busy" :aria-label="t(openOnClick?'需求 {code}：单击查看，双击复制协作摘要':'需求 {code}：双击复制协作摘要',{code})" :title="t('双击复制协作摘要；聚焦编号后按 Ctrl/Cmd+C 也可复制')" @click="click" @mousedown="press" @dblclick.prevent="doubleClick" @keydown="keydown">{{code}}</button>
    <button type="button" class="requirement-code-copy" :disabled="disabled||invalid||busy" :aria-label="t('复制需求 {code} 的协作摘要',{code})" :title="t('复制协作摘要')" @click="copy"><svg v-if="!busy" aria-hidden="true" viewBox="0 0 20 20"><rect x="7" y="7" width="10" height="10" rx="2"/><path d="M12 7V5a2 2 0 0 0-2-2H5a2 2 0 0 0-2 2v5a2 2 0 0 0 2 2h2"/></svg><span v-else aria-hidden="true">…</span></button>
    <span v-if="feedback" class="requirement-copy-feedback" :class="{failed}" role="status" aria-live="polite">{{t(feedback)}}</span>
    <dialog v-if="fallbackText" ref="dialog" class="requirement-copy-dialog" :aria-labelledby="labelID" @cancel.prevent="closeFallback" @click.self.stop="closeFallback" @dblclick.stop @keydown.stop>
      <header><h2 :id="labelID">{{t('复制需求协作摘要')}}</h2><button type="button" :aria-label="t('关闭')" @click="closeFallback">×</button></header>
      <p>{{t('浏览器未允许自动复制。以下文本已选中，可按 Ctrl/Cmd+C，或长按选择复制。')}}</p>
      <textarea ref="textarea" readonly :value="fallbackText" :aria-label="t('需求协作摘要文本')" @focus="textarea?.select()"></textarea>
      <footer><button type="button" @click="textarea?.focus();textarea?.select()">{{t('选择全部文本')}}</button><button type="button" :disabled="busy" @click="retry">{{t('再次复制')}}</button><button type="button" @click="closeFallback">{{t('关闭')}}</button></footer>
    </dialog>
  </span>
</template>

<style scoped>
.requirement-code-control{display:inline-flex;align-items:center;gap:3px;max-width:100%;vertical-align:baseline;white-space:nowrap}.requirement-code-value{font:inherit;font-variant-numeric:tabular-nums;color:inherit;background:transparent;border:0;padding:0;cursor:pointer;white-space:nowrap}.requirement-code-value:hover{text-decoration:underline;color:var(--primary)}.requirement-code-copy{display:inline-flex;align-items:center;justify-content:center;flex:none;width:20px;height:22px;border:0;border-radius:4px;padding:3px;background:transparent;color:var(--muted);cursor:pointer}.requirement-code-copy:hover{background:var(--palette-tint-blue,#eaf1ff);color:var(--primary)}.requirement-code-copy svg{width:14px;height:14px;fill:none;stroke:currentColor;stroke-width:1.5}.requirement-code-control button:focus-visible{outline:2px solid #1677ff;outline-offset:2px}.requirement-code-control button:disabled{opacity:.55;cursor:wait}.requirement-copy-feedback{position:fixed;bottom:28px;left:50%;transform:translateX(-50%);z-index:2500;max-width:min(90vw,620px);white-space:normal;background:var(--surface,#fff);color:var(--ink,#344054);border:1px solid var(--line,#d0d5dd);box-shadow:0 8px 30px #17203330;border-radius:8px;padding:12px 18px;font-size:12px;line-height:1.6}.requirement-copy-feedback.failed{border-color:#d97706}.requirement-copy-dialog{position:fixed;inset:0;margin:auto;box-sizing:border-box;width:min(620px,calc(100vw - 32px));max-height:calc(100dvh - 40px);overflow:auto;background:var(--surface,#fff);color:var(--ink,#344054);border:1px solid var(--line,#d0d5dd);border-radius:12px;padding:20px;white-space:normal;text-align:left;box-shadow:0 20px 70px #17203345;z-index:2600}.requirement-copy-dialog::backdrop{background:#10182880}.requirement-copy-dialog header{display:flex;align-items:center;justify-content:space-between;gap:12px}.requirement-copy-dialog h2{font-size:17px;margin:0}.requirement-copy-dialog p{font-size:12px;line-height:1.7;color:var(--muted,#667085)}.requirement-copy-dialog textarea{display:block;box-sizing:border-box;width:100%;height:min(310px,46dvh);resize:vertical;border:1px solid var(--line,#d0d5dd);border-radius:6px;background:var(--surface,#fff);color:var(--ink,#344054);padding:12px;font:13px/1.8 ui-monospace,monospace;white-space:pre-wrap}.requirement-copy-dialog footer{display:flex;justify-content:flex-end;gap:8px;flex-wrap:wrap;margin-top:14px}.requirement-copy-dialog button{border:1px solid var(--line,#d0d5dd);border-radius:6px;background:var(--surface,#fff);color:var(--ink,#344054);padding:7px 12px;cursor:pointer}
</style>

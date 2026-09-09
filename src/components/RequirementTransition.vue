<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { api } from '../api'
import { t } from '../i18n'
import { stateInfo, statusLabel, workflowStyle, type RequirementState } from '../requirementWorkflow'
import { Popover, PopoverContent, PopoverTrigger } from './ui/popover'
const props=defineProps<{requirementId:number;status:string;definitions:RequirementState[];disabled?:boolean;refreshKey?:number}>()
const emit=defineEmits<{(event:'change',status:string):void}>()
const loading=ref(false),error=ref(''),allowed=ref<string[]>([])
const open=ref(false),query=ref('')
const project=localStorage.getItem('devflow-project')||'prj_orbit'
let version=0,disposed=false,invalid=false
const valid=()=>!disposed&&!invalid&&project===(localStorage.getItem('devflow-project')||'prj_orbit')
const unavailable=computed(()=>props.disabled||loading.value||!!error.value||!allowed.value.length)
const phases:Record<string,string>={todo:'待办阶段',doing:'进行阶段',done:'完成阶段',cancelled:'取消阶段'}
const options=computed(()=>[...new Set(allowed.value)].filter(key=>key!==props.status&&statusLabel(key,props.definitions,t).toLocaleLowerCase().includes(query.value.trim().toLocaleLowerCase())))
const groups=computed(()=>Object.entries(phases).map(([phase,label])=>({phase,label,items:options.value.filter(key=>stateInfo(key,props.definitions).category===phase)})).filter(group=>group.items.length))
async function load(){
  if(!valid())return
  const request=++version,id=props.requirementId,status=props.status
  loading.value=true;error.value='';allowed.value=[];open.value=false
  try{const data=await api<any>('/requirements/'+id+'/transitions',{headers:{'X-DevFlow-Project':project}});if(request!==version||!valid()||id!==props.requirementId||status!==props.status)return;if(data.currentStatus!==status)throw new Error('需求状态已变化，请刷新后重试');allowed.value=Array.isArray(data.allowedTransitions)?data.allowedTransitions.filter((key:unknown)=>typeof key==='string'):[]}
  catch(cause){if(request===version&&valid())error.value=cause instanceof Error?cause.message:'流转权限加载失败，请重试'}
  finally{if(request===version&&valid())loading.value=false}
}
function choose(next:string){if(valid()&&!unavailable.value&&allowed.value.includes(next)&&next!==props.status){open.value=false;query.value='';emit('change',next)}}
function change(event:Event){const input=event.target as HTMLSelectElement,next=input.value;input.value=props.status;choose(next)}
function invalidate(){invalid=true;version++;allowed.value=[];loading.value=false;open.value=false}
watch(()=>props.disabled,disabled=>{if(disabled)open.value=false})
watch(open,value=>{if(value)query.value=''})
watch(()=>[props.requirementId,props.status,props.refreshKey],()=>{void load()},{immediate:true})
onMounted(()=>{window.addEventListener('devflow-identity-changed',invalidate);window.addEventListener('devflow-auth-expired',invalidate);window.addEventListener('devflow-project-changed',invalidate);window.addEventListener('devflow-workflow-changed',load);window.addEventListener('focus',load)})
onBeforeUnmount(()=>{disposed=true;version++;window.removeEventListener('devflow-identity-changed',invalidate);window.removeEventListener('devflow-auth-expired',invalidate);window.removeEventListener('devflow-project-changed',invalidate);window.removeEventListener('devflow-workflow-changed',load);window.removeEventListener('focus',load)})
</script>
<template>
  <div class="transition-control">
    <Popover v-model:open="open">
      <PopoverTrigger as-child><button type="button" class="workflow-color transition-trigger" :style="workflowStyle(status,definitions)" :aria-label="t('需求工作状态')" :disabled="unavailable"><i :style="{background:stateInfo(status,definitions).color}"></i><span>{{statusLabel(status,definitions,t)}}</span><span class="transition-chevron" aria-hidden="true">⌄</span></button></PopoverTrigger>
      <PopoverContent v-if="!unavailable" class="transition-menu flex flex-col overflow-hidden w-[300px] max-w-[calc(100vw-24px)] p-0" align="start" :side-offset="8" :collision-padding="12" @escape-key-down="$event.stopPropagation()">
        <header><b>{{t('切换工作状态')}}</b><small>{{t('仅显示当前角色可执行的流转')}}</small></header>
        <div class="transition-current"><span>{{t('当前状态')}}</span><b class="workflow-color" :style="workflowStyle(status,definitions)">{{statusLabel(status,definitions,t)}}</b><span aria-hidden="true">✓</span></div>
        <input v-model="query" class="transition-search" :aria-label="t('查找状态')" :placeholder="t('查找状态')" @keydown.escape.stop="open=false">
        <div class="transition-options">
          <section v-for="group in groups" :key="group.phase"><h4>{{t(group.label)}}</h4><button v-for="key in group.items" :key="key" type="button" class="transition-option" @click="choose(key)"><i :style="{background:stateInfo(key,definitions).color}"></i><span>{{statusLabel(key,definitions,t)}}</span><span class="transition-arrow" aria-hidden="true">→</span></button></section>
          <p v-if="!options.length">{{t('没有匹配的状态')}}</p>
        </div>
        <footer>{{t('选择后立即保存并记录流转历史')}}</footer>
      </PopoverContent>
    </Popover>
    <small v-if="loading">{{t('正在检查流转权限…')}}</small><div v-if="error" class="transition-error" role="alert">{{t(error)}} <button type="button" :disabled="loading||disabled" @click="load">{{t('重试')}}</button></div><small v-else-if="!loading&&!disabled&&!allowed.length">{{t('当前角色暂无可用流转')}}</small>
  </div>
</template>
<style scoped>
.transition-control{min-width:0}.transition-trigger{width:100%;height:auto;min-height:32px;display:flex;gap:8px;align-items:center;padding:6px 10px;border:1px solid!important;border-radius:8px;font-size:12px;text-align:left;box-shadow:none;white-space:normal}.transition-trigger>span:not(.transition-chevron){flex:1}.transition-trigger i,.transition-option i{display:block;width:7px;height:7px;border-radius:50%;flex:none}.transition-chevron{font-size:14px;opacity:.7}.transition-control>small{display:block;color:var(--muted);font-size:10px;line-height:1.5;margin-top:5px}.transition-error{font-size:10px;color:#b42318;line-height:1.6;margin-top:5px}.transition-error button{padding:0;border:0;background:none;color:inherit;text-decoration:underline;font:inherit}
.transition-menu{z-index:280;width:300px;max-width:calc(100vw - 24px);padding:0;border:1px solid var(--line);border-radius:12px;background:var(--surface,#fff);color:var(--ink);box-shadow:0 16px 48px #0f172a29;overflow:hidden}.transition-menu header{padding:16px 16px 10px;display:grid;gap:4px}.transition-menu header b{font-size:13px}.transition-menu header small{font-size:11px;color:var(--muted)}.transition-current{margin:0 12px 10px;padding:9px 10px;display:flex;align-items:center;gap:8px;border-radius:7px;background:var(--surface-soft,#f6f8fb);font-size:11px}.transition-current>span:first-child{color:var(--muted)}.transition-current b{flex:1;text-align:right;font-size:12px;font-weight:550}.transition-menu .transition-search{display:block;width:calc(100% - 24px);margin:0 12px 8px;padding:8px 10px;background:var(--surface,#fff);border:1px solid var(--line);border-radius:7px;outline:none;font-size:12px;color:inherit}.transition-search:focus{border-color:var(--primary)!important;box-shadow:0 0 0 2px var(--primary-soft)}.transition-options{max-height:min(310px,45vh);overflow:auto;padding:0 8px 8px;scrollbar-width:thin;scrollbar-color:#94a3b8 transparent}.transition-options h4{font-size:10px;letter-spacing:.06em;margin:10px 8px 4px;color:var(--muted);font-weight:550}.transition-option{display:flex;width:100%;gap:10px;align-items:center;padding:10px 9px;border:0;border-radius:7px;text-align:left;background:transparent;color:var(--ink);font-size:12px}.transition-option>span:first-of-type{flex:1}.transition-option:hover,.transition-option:focus-visible{background:var(--primary-soft);outline:none}.transition-arrow{opacity:0;color:var(--primary)}.transition-option:hover .transition-arrow,.transition-option:focus-visible .transition-arrow{opacity:1}.transition-options>p{font-size:12px;color:var(--muted);text-align:center;padding:20px 0}.transition-menu footer{padding:10px 16px;font-size:10px;color:var(--muted);border-top:1px solid var(--line);background:var(--surface-soft,#f8fafc)}
</style>
<style scoped>
.transition-menu{display:flex;flex-direction:column}
.transition-menu>header,.transition-menu>.transition-current,.transition-menu>.transition-search,.transition-menu>footer{flex:none}
.transition-options{min-height:0;flex:1;overscroll-behavior:contain}
.transition-trigger:focus-visible{outline:2px solid var(--primary);outline-offset:2px}
.transition-trigger:disabled{cursor:not-allowed;opacity:.7}
</style>

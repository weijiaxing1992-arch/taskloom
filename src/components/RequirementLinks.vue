<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { onBeforeRouteLeave, onBeforeRouteUpdate } from 'vue-router'
import { t } from '../i18n'
import { statusLabel, workflowStyle } from '../requirementWorkflow'
import RequirementCode from './RequirementCode.vue'
import { useSettingsScope } from './settingsScope'
type LinkedRequirement={id:number;code:string;title:string;status:string;statusName?:string;statusColor?:string;statusCategory?:string;statusSystem?:boolean}
const props=defineProps<{requirementId:number;canEdit:boolean}>()
const emit=defineEmits<{(event:'open',id:number):void;(event:'count',count:number):void}>()
const scope=useSettingsScope(),items=ref<LinkedRequirement[]>([]),candidates=ref<LinkedRequirement[]>([]),loading=ref(false),searching=ref(false),saving=ref(false),error=ref(''),searchError=ref(''),query=ref(''),picker=ref(false),removeTarget=ref<LinkedRequirement|null>(null)
const matches=computed(()=>candidates.value.filter(item=>item.id!==props.requirementId&&!items.value.some(link=>link.id===item.id)).slice(0,30))
let epoch=0,readVersion=0,searchVersion=0,disposed=false,timer:ReturnType<typeof setTimeout>|undefined
const target=()=>({epoch,id:props.requirementId})
const current=(request:ReturnType<typeof target>)=>!disposed&&scope.current()&&request.epoch===epoch&&request.id===props.requirementId
const message=(cause:unknown)=>cause instanceof Error?cause.message:'关联需求操作失败，请重试'
async function load(){
 if(!scope.current())return
 const request=target(),version=++readVersion;loading.value=true;error.value=''
 try{const data=await scope.request<{items:LinkedRequirement[]}>(`/requirements/${request.id}/links`);if(current(request)&&version===readVersion){if(!Array.isArray(data.items))throw Error('关联需求数据格式不正确');items.value=data.items;emit('count',items.value.length)}}catch(cause){if(current(request)&&version===readVersion)error.value=message(cause)}finally{if(current(request)&&version===readVersion)loading.value=false}
}
async function search(){
 if(!scope.current()||!picker.value||!props.canEdit)return
 const request=target(),version=++searchVersion;searching.value=true;searchError.value=''
 try{const data=await scope.request<{items:LinkedRequirement[]}>('/requirements?'+new URLSearchParams({q:query.value.trim()}));if(current(request)&&version===searchVersion){if(!Array.isArray(data.items))throw Error('需求候选数据格式不正确');candidates.value=data.items}}catch(cause){if(current(request)&&version===searchVersion)searchError.value=message(cause)}finally{if(current(request)&&version===searchVersion)searching.value=false}
}
function showPicker(){if(!props.canEdit||saving.value||loading.value||!scope.current())return;picker.value=true;void search()}
async function change(id:number,remove=false){
 if(!props.canEdit||saving.value||loading.value||!scope.current()||!Number.isSafeInteger(id)||id<=0||id===props.requirementId)return
 const request=target();saving.value=true;error.value=''
 try{await scope.request(`/requirements/${request.id}/links${remove?'/'+id:''}`,{method:remove?'DELETE':'POST',...(remove?{}:{body:JSON.stringify({requirementId:id})})});if(!current(request))return;removeTarget.value=null;await load();if(!remove){picker.value=false;query.value='';candidates.value=[]}}catch(cause){if(current(request))error.value=message(cause)}finally{if(current(request))saving.value=false}
}
function open(item:LinkedRequirement){if(!saving.value&&scope.current())emit('open',item.id)}
watch(query,()=>{searchVersion++;clearTimeout(timer);if(picker.value){searching.value=true;timer=setTimeout(()=>void search(),180)}},{flush:'sync'})
watch(()=>props.requirementId,()=>{epoch++;readVersion++;searchVersion++;clearTimeout(timer);items.value=[];candidates.value=[];query.value='';picker.value=false;removeTarget.value=null;saving.value=false;searching.value=false;void load()},{immediate:true,flush:'sync'})
watch(scope.locked,value=>{if(value){epoch++;clearTimeout(timer);loading.value=false;searching.value=false;saving.value=false;items.value=[];candidates.value=[]}},{flush:'sync'})
onBeforeRouteLeave(()=>!saving.value);onBeforeRouteUpdate(()=>!saving.value)
onBeforeUnmount(()=>{disposed=true;epoch++;clearTimeout(timer)})
defineExpose({saving})
</script>
<template>
 <section class="requirement-links" :aria-label="t('关联需求')"><header><div><h3>{{t('关联需求')}}</h3><p>{{t('关联是双向协作关系，不会改变父子层级或迭代归属。')}}</p></div><button v-if="canEdit" class="btn primary compact" type="button" :disabled="loading||saving||scope.locked.value" @click="showPicker">{{t('关联已有需求')}}</button></header>
  <p v-if="scope.locked.value" class="field-error" role="alert">{{t('项目或账号已变化，请刷新页面后继续')}}</p><p v-if="error" class="field-error" role="alert">{{t(error)}} <button type="button" class="link" :disabled="loading||saving" @click="load">{{t('重试')}}</button></p>
  <p v-if="loading" class="links-empty" role="status">{{t('正在载入关联需求…')}}</p>
  <div v-if="picker" class="link-picker"><label>{{t('搜索要关联的需求')}}<input v-model="query" type="search" :disabled="saving||scope.locked.value" :placeholder="t('搜索编号或标题')"/></label><button class="btn compact" type="button" :disabled="saving" @click="picker=false;query=''">{{t('取消')}}</button><p v-if="searchError" role="alert" class="field-error">{{t(searchError)}} <button type="button" class="link" @click="search">{{t('重试')}}</button></p><p v-if="searching" role="status">{{t('搜索中…')}}</p><ul v-else><li v-for="item in matches" :key="item.id"><span>{{item.code}} · {{item.title}}</span><button class="btn compact" type="button" :disabled="saving||scope.locked.value" :aria-label="t('关联需求 {code}',{code:item.code})" @click="change(item.id)">{{t('关联')}}</button></li><li v-if="!matches.length">{{t('没有可关联的需求，请调整搜索条件')}}</li></ul><small>{{t('仅展示当前项目可访问的需求，已关联项和当前需求已排除。')}}</small></div>
  <div v-if="removeTarget" class="link-remove-confirm" role="alertdialog" :aria-label="t('解除需求关联')"><p>{{t('确定解除与「{title}」的关联？双方需求不会被删除。',{title:removeTarget.title})}}</p><button class="btn compact" :disabled="saving" type="button" @click="removeTarget=null">{{t('取消')}}</button><button class="btn compact" :disabled="saving||scope.locked.value" type="button" @click="change(removeTarget.id,true)">{{t('解除关联')}}</button></div>
  <article v-for="item in items" :key="item.id" class="linked-requirement"><div><RequirementCode :requirement="item" :project-id="scope.project" :disabled="saving||scope.locked.value" @open="open(item)"/><button type="button" class="link linked-title" :disabled="saving||scope.locked.value" @click="open(item)">{{item.title}}</button></div><span class="status workflow-color" :style="workflowStyle(item)">{{statusLabel(item,[],t)}}</span><button v-if="canEdit" type="button" class="link" :disabled="saving||scope.locked.value" :aria-label="t('解除与需求 {code} 的关联',{code:item.code})" @click="removeTarget=item">{{t('解除关联')}}</button></article>
  <p v-if="!loading&&!items.length&&!error" class="links-empty">{{t('暂无关联需求')}}</p>
 </section>
</template>
<style scoped>
.requirement-links>header{display:flex;justify-content:space-between;align-items:flex-start;gap:16px}.requirement-links h3{margin:0;font-size:16px}.requirement-links p,.requirement-links small{font-size:12px;line-height:1.7;color:var(--muted)}.requirement-links button{cursor:pointer}.linked-requirement{display:flex;align-items:center;gap:12px;border:1px solid var(--line);background:var(--surface);padding:14px;border-radius:9px;margin-top:12px}.linked-requirement>div{flex:1;min-width:0}.linked-title{display:block;text-align:left;padding:6px 0 0;white-space:normal;overflow-wrap:anywhere;font-size:13px}.link-picker,.link-remove-confirm{border:1px solid var(--line);border-radius:8px;padding:16px;background:var(--surface-soft);margin:16px 0}.link-picker>label{display:grid;gap:8px;font-size:12px;margin-bottom:10px}.link-picker input{border:1px solid var(--line);background:var(--surface);color:var(--ink);padding:9px;border-radius:6px;width:100%}.link-picker ul{list-style:none;padding:0;max-height:280px;overflow:auto}.link-picker li{display:flex;gap:15px;align-items:center;justify-content:space-between;padding:10px 0;font-size:12px;border-bottom:1px solid var(--line)}.link-remove-confirm button+button{margin-left:10px}.links-empty{padding:24px 0}.requirement-links .field-error{color:var(--error-fg,#b42318)}
.requirement-links,.requirement-links>header>div,.link-picker>label{min-width:0}.requirement-links>header{flex-wrap:wrap}.requirement-links p,.requirement-links small,.link-picker li>span{overflow-wrap:anywhere}.link-picker input{box-sizing:border-box;min-width:0}.link-picker li>span{min-width:0}.link-picker li>button{flex:none}.linked-requirement>.status{max-width:100%;white-space:normal;overflow-wrap:anywhere}.linked-requirement>button{flex:none}
@media(max-width:640px){.linked-requirement{flex-wrap:wrap;padding:12px;gap:9px}.linked-requirement>div{flex-basis:100%}.link-picker,.link-remove-confirm{padding:12px}.link-picker input{font-size:16px;min-height:42px}.link-picker ul{max-height:min(280px,45dvh);overscroll-behavior:contain}.link-picker li{gap:10px}.requirement-links .btn{min-height:40px;white-space:normal}.link-remove-confirm button{margin-top:6px}}
</style>

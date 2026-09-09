<script setup lang="ts">
import { computed, onBeforeUnmount, ref, useId, watch } from 'vue'
import { t } from '../i18n'
import { useSettingsScope } from './settingsScope'
export type RequirementChoice={id:number;code:string;title:string}
const props=withDefaults(defineProps<{modelValue:number|null;disabled?:boolean;label?:string;inputId?:string;selectedRequirement?:RequirementChoice|null}>(),{disabled:false,label:'关联需求'})
const emit=defineEmits<{(event:'update:modelValue',id:number|null):void;(event:'change',item:RequirementChoice|null):void}>()
const scope=useSettingsScope(),uid=useId(),controlID=computed(()=>props.inputId||'requirement-picker-'+uid),listID=computed(()=>controlID.value+'-options')
const container=ref<HTMLElement|null>(null),input=ref<HTMLInputElement|null>(null),menuStyle=ref<Record<string,string>>({}),query=ref(''),opened=ref(false),loading=ref(false),error=ref(''),selectedError=ref(''),selected=ref<RequirementChoice|null>(null),items=ref<RequirementChoice[]>([]),highlighted=ref(0)
const selectedLabel=computed(()=>selected.value?.id===props.modelValue?`${selected.value.code} · ${selected.value.title}`:props.modelValue?`#${props.modelValue}`:t('不关联需求'))
let disposed=false,resetting=false,version=0,selectedVersion=0,timer:ReturnType<typeof setTimeout>|undefined
const available=()=>!disposed&&scope.current()&&!props.disabled
function valid(item:any):item is RequirementChoice{return item&&Number.isSafeInteger(item.id)&&item.id>0&&typeof item.code==='string'&&typeof item.title==='string'}
async function loadSelected(){
 const id=props.modelValue,sequence=++selectedVersion;selectedError.value='';selected.value=null
 if(!id||!Number.isSafeInteger(id)||id<1||!scope.current())return
 if(props.selectedRequirement?.id===id&&valid(props.selectedRequirement)){selected.value=props.selectedRequirement;return}
 try{const item=await scope.request<RequirementChoice>('/requirements/'+id);if(disposed||sequence!==selectedVersion||id!==props.modelValue||!scope.current())return;if(!valid(item)||item.id!==id)throw Error('关联需求数据格式不正确');selected.value=item}catch(cause){if(!disposed&&sequence===selectedVersion&&id===props.modelValue&&scope.current())selectedError.value=cause instanceof Error?cause.message:'关联需求名称暂不可用，可重试或重新选择'}
}
async function search(){
 if(!available()||!opened.value)return
 const sequence=++version,value=query.value.trim();loading.value=true;error.value=''
 // 选择器只需要可展示的最小引用信息。服务端分页后再返回，避免先下载整项目
 // 的富需求数据再在浏览器截断，既控制大项目响应量，也不会把正文等无关字段带入弹窗。
 try{const data=await scope.request<{items:RequirementChoice[]}>('/requirements?'+new URLSearchParams({q:value,page:'1',pageSize:'30',projection:'reference'}));if(!available()||sequence!==version||!opened.value)return;if(!Array.isArray(data.items)||data.items.some(item=>!valid(item)))throw Error('需求候选数据格式不正确');items.value=data.items;highlighted.value=0}catch(cause){if(available()&&sequence===version){items.value=[];error.value=cause instanceof Error?cause.message:'需求搜索失败，请重试'}}finally{if(!disposed&&sequence===version)loading.value=false}
}
function open(){if(!available())return;opened.value=true;void search()}
function close(){opened.value=false;version++;loading.value=false;clearTimeout(timer)}
function clearQuery(){resetting=true;query.value='';resetting=false}
function choose(item:RequirementChoice|null){if(!available()||(item&&!items.value.some(candidate=>candidate.id===item.id)))return;selectedError.value='';close();clearQuery();if((item?.id??null)!==props.modelValue){emit('update:modelValue',item?.id??null);emit('change',item)}}
function keydown(event:KeyboardEvent){if(!available()||event.isComposing)return;if(event.key==='Escape'&&opened.value){event.preventDefault();event.stopPropagation();close();return}if(['ArrowDown','ArrowUp'].includes(event.key)){event.preventDefault();if(!opened.value){open();return}if(items.value.length)highlighted.value=(highlighted.value+(event.key==='ArrowDown'?1:-1)+items.value.length)%items.value.length}else if(event.key==='Enter'&&opened.value){event.preventDefault();if(!loading.value&&items.value[highlighted.value])choose(items.value[highlighted.value]!)}}
function blur(event:FocusEvent){if(!container.value?.contains(event.relatedTarget as Node|null))close()}
// 弹层必须适应抽屉内的滚动区域，而不只判断浏览器高度。下方不足时向上展开，
// 限制列表自身滚动，避免候选被固定底栏或父容器裁掉；不挪动用户正在输入的表单。
function menuPlacement(top:number,bottom:number,boundTop:number,boundBottom:number,containerTop:number,containerBottom:number){
 const below=Math.max(0,boundBottom-bottom-6),above=Math.max(0,top-boundTop-6),up=below<260&&above>below
 return {top:up?'auto':`${bottom-containerTop+4}px`,bottom:up?`${containerBottom-top+4}px`:'auto',maxHeight:`${Math.min(330,up?above:below)}px`}
}
function updateMenu(){
 if(!opened.value||!input.value||!container.value||typeof input.value.getBoundingClientRect!=='function')return
 const anchor=input.value.getBoundingClientRect(),box=container.value.getBoundingClientRect()
 let boundTop=8,boundBottom=window.innerHeight-8,parent=container.value.parentElement
 while(parent){
  const overflow=window.getComputedStyle(parent).overflowY
  if(/^(auto|scroll|hidden|clip)$/.test(overflow)){const rect=parent.getBoundingClientRect();boundTop=Math.max(boundTop,rect.top+4);boundBottom=Math.min(boundBottom,rect.bottom-4)}
  // 表单可声明粘底操作栏，候选菜单不能覆盖“取消/保存”等主要操作。
  for(const bar of parent.querySelectorAll(':scope > [data-menu-boundary="bottom"]')){const rect=bar.getBoundingClientRect();if(rect.top>anchor.bottom)boundBottom=Math.min(boundBottom,rect.top-4)}
  parent=parent.parentElement
 }
 menuStyle.value=menuPlacement(anchor.top,anchor.bottom,boundTop,boundBottom,box.top,box.bottom)
}
function removeLayoutListeners(){window.removeEventListener('resize',updateMenu);window.removeEventListener('scroll',updateMenu,true)}
watch(opened,value=>{removeLayoutListeners();if(value){updateMenu();window.addEventListener('resize',updateMenu);window.addEventListener('scroll',updateMenu,true)}},{flush:'post'})
watch(query,()=>{if(resetting)return;version++;clearTimeout(timer);items.value=[];highlighted.value=0;if(available()){opened.value=true;loading.value=true;timer=setTimeout(()=>void search(),180)}},{flush:'sync'})
watch(()=>[props.modelValue,props.selectedRequirement],()=>{close();clearQuery();void loadSelected()},{immediate:true,flush:'sync'})
watch(()=>props.disabled,value=>{if(value)close()},{flush:'sync'})
watch(scope.locked,value=>{if(value){selectedVersion++;close();items.value=[];selected.value=null;selectedError.value='项目或账号已变化，请刷新页面后继续'}},{flush:'sync'})
onBeforeUnmount(()=>{disposed=true;selectedVersion++;close();removeLayoutListeners()})
</script>
<template>
 <div ref="container" class="requirement-picker" @focusout="blur"><label :for="controlID">{{t(label)}}</label><div class="requirement-picker-selected"><span :title="selectedLabel">{{selectedLabel}}</span><button v-if="modelValue" type="button" :disabled="disabled||scope.locked.value" :aria-label="t('清除关联需求')" @click="choose(null)">×</button></div><input ref="input" :id="controlID" v-model="query" type="search" autocomplete="off" role="combobox" :disabled="disabled||scope.locked.value" :placeholder="t('输入需求编号或标题搜索')" :aria-expanded="opened" :aria-controls="listID" :aria-activedescendant="opened&&items.length&&!loading?listID+'-'+highlighted:undefined" aria-autocomplete="list" @focus="open" @keydown="keydown"/>
  <p v-if="selectedError" class="picker-error" role="alert">{{t(selectedError)}} <button type="button" :disabled="scope.locked.value" @click="loadSelected">{{t('重试')}}</button></p>
  <div v-if="opened" class="requirement-picker-popover" :style="menuStyle"><p v-if="loading" role="status">{{t('搜索中…')}}</p><p v-else-if="error" class="picker-error" role="alert">{{t(error)}} <button type="button" @click="search">{{t('重试')}}</button></p><ul v-else :id="listID" role="listbox" :aria-label="t('需求搜索结果')"><li v-for="(item,index) in items" :id="listID+'-'+index" :key="item.id" role="option" :aria-selected="index===highlighted" @mousedown.prevent @click="choose(item)" @mouseenter="highlighted=index"><b>{{item.code}}</b><span>{{item.title}}</span></li><li v-if="!items.length" role="presentation" class="picker-empty">{{t('没有匹配的需求，请调整编号或标题')}}</li></ul><small>{{t('仅显示当前项目可访问的需求，最多展示 30 项。')}}</small></div>
 </div>
</template>
<style scoped>
.requirement-picker{position:relative;min-width:0}.requirement-picker>label{display:block;font-size:12px;color:var(--muted);margin-bottom:7px}.requirement-picker-selected{display:flex;gap:8px;align-items:center;min-height:29px;font-size:12px;color:var(--ink);margin-bottom:6px}.requirement-picker-selected>span{overflow:hidden;text-overflow:ellipsis;white-space:nowrap;flex:1}.requirement-picker button{border:0;background:transparent;color:var(--primary);cursor:pointer;padding:3px 5px}.requirement-picker input{width:100%;border:1px solid var(--line);border-radius:7px;padding:8px 10px;background:var(--surface);color:var(--ink);font:inherit;font-size:12px}.requirement-picker input:focus{outline:2px solid #1677ff;outline-offset:1px}.requirement-picker-popover{position:absolute;top:100%;left:0;right:0;z-index:8;min-width:220px;max-width:min(460px,85vw);background:var(--surface);color:var(--ink);border:1px solid var(--line);border-radius:8px;box-shadow:0 8px 24px #10182826;padding:6px}.requirement-picker ul{list-style:none;padding:0;margin:0;max-height:250px;overflow:auto}.requirement-picker li{display:grid;gap:4px;padding:10px;cursor:pointer;border-radius:5px;font-size:12px}.requirement-picker li[aria-selected=true]{background:var(--primary-soft);color:var(--primary)}.requirement-picker li>span{overflow-wrap:anywhere}.requirement-picker small,.requirement-picker p{font-size:11px;line-height:1.6;color:var(--muted);display:block;margin:7px}.requirement-picker .picker-error{color:var(--error-fg,#b42318)}.requirement-picker li.picker-empty{cursor:default;color:var(--muted)}
.requirement-picker input{box-sizing:border-box;min-width:0}.requirement-picker-popover{min-width:0;max-width:100%;box-sizing:border-box}.requirement-picker li>b,.requirement-picker small,.requirement-picker p{overflow-wrap:anywhere}.requirement-picker-selected>span{min-width:0}.requirement-picker-selected>button{flex:none;min-width:30px;min-height:30px}
@media(max-width:760px){.requirement-picker input{font-size:16px;min-height:42px}.requirement-picker ul{max-height:min(250px,40dvh);overscroll-behavior:contain}.requirement-picker li{min-height:44px;box-sizing:border-box}}
.requirement-picker-popover{display:flex;flex-direction:column;overflow:hidden}.requirement-picker-popover ul{min-height:0;flex:1 1 auto;overscroll-behavior:contain}.requirement-picker-popover>small{flex:none}
</style>

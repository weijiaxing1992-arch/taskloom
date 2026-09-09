<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, useId, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { api } from '../api'
import { t } from '../i18n'
import { useWorkspaceStore } from '../stores/workspace'
import { searchItemsInProject, topSearchTarget, type TopSearchItem } from '../topSearch'
import Icon from './Icon.vue'

const props=defineProps<{disabled?:boolean}>(),workspace=useWorkspaceStore(),route=useRoute(),router=useRouter()
const root=ref<HTMLElement|null>(null),input=ref<HTMLInputElement|null>(null),query=ref(''),opened=ref(false),items=ref<TopSearchItem[]>([]),total=ref(0),loading=ref(false),navigating=ref(false),error=ref(''),searched=ref(false),activeIndex=ref(0),invalidated=ref(false)
const listID='top-search-'+useId(),composing=ref(false)
const projectID=computed(()=>String(workspace.currentProject?.id||''))
const scopeKey=computed(()=>[workspace.session?.tenant.id||'',workspace.currentUser?.id||'',projectID.value].join('\0'))
const available=computed(()=>!!workspace.session&&!!projectID.value&&!workspace.operationDisabled&&!workspace.identityConflict&&!workspace.switchingProject&&!invalidated.value&&!props.disabled)
// 防抖降低连续输入的请求量；Abort 只取消传输，仍需序号/作用域双校验来丢弃迟到结果。
let sequence=0,navigationVersion=0,disposed=false,timer:ReturnType<typeof setTimeout>|undefined,controller:AbortController|null=null,appliedQuery=''
function stopRequest(){++sequence;clearTimeout(timer);controller?.abort();controller=null;loading.value=false}
function clearMemory(){stopRequest();++navigationVersion;query.value='';items.value=[];total.value=0;error.value='';searched.value=false;opened.value=false;navigating.value=false;activeIndex.value=0;appliedQuery=''}
function invalidate(){invalidated.value=true;clearMemory()}
function current(version:number,key:string){return !disposed&&version===sequence&&key===scopeKey.value&&available.value&&opened.value}
async function search(){
 if(!available.value||!opened.value||composing.value)return
 const text=query.value.trim();if(!text){stopRequest();items.value=[];total.value=0;searched.value=false;error.value='';return}
 stopRequest();const version=sequence,key=scopeKey.value,project=projectID.value;controller=new AbortController();const signal=controller.signal
 loading.value=true;error.value=''
 try{const data=await api<any>('/search?'+new URLSearchParams({q:text,project,limit:'12'}),{signal,headers:{'X-DevFlow-Project':project}});if(!current(version,key))return;const results=searchItemsInProject(data.items,project);if(!Number.isSafeInteger(data.total)||data.total<results.length)throw Error('搜索结果格式不正确，请重试');items.value=results;total.value=data.total;searched.value=true;appliedQuery=text;activeIndex.value=0}
 catch(cause){if(current(version,key)&&!signal.aborted){items.value=[];total.value=0;searched.value=true;error.value=cause instanceof Error?cause.message:'搜索暂时不可用，请重试'}}
 finally{if(current(version,key))loading.value=false}
}
function schedule(){stopRequest();items.value=[];total.value=0;error.value='';searched.value=false;activeIndex.value=0;appliedQuery='';if(available.value&&opened.value&&query.value.trim()&&!composing.value){loading.value=true;timer=setTimeout(()=>void search(),180)}}
function openPopup(){if(!available.value)return;opened.value=true;if(query.value.trim()&&appliedQuery!==query.value.trim())schedule()}
async function focus(){if(!available.value)return false;await nextTick();if(!input.value||input.value.closest('[inert],[hidden]'))return false;input.value.focus();openPopup();return true}
function close(){opened.value=false;stopRequest()}
function clear(){clearMemory();void focus()}
async function choose(item:TopSearchItem){
 // 只能打开当前项目、当前结果集中真实存在的项；导航后仍校验身份，避免旧结果串页。
 if(!available.value||navigating.value||loading.value||item.projectId!==projectID.value||!items.value.some(candidate=>candidate.id===item.id&&candidate.type===item.type))return
 const key=scopeKey.value,version=++navigationVersion;navigating.value=true;error.value=''
 try{const failure=await router.push(topSearchTarget(item,route.path,route.query));if(!failure&&key===scopeKey.value&&version===navigationVersion)close()}
 catch(cause){if(key===scopeKey.value&&version===navigationVersion&&available.value)error.value=cause instanceof Error?cause.message:'无法打开搜索结果，请重试'}
 finally{if(version===navigationVersion)navigating.value=false}
}
async function more(){if(!available.value||navigating.value)return;const version=++navigationVersion,key=scopeKey.value;navigating.value=true;try{const failure=await router.push({path:'/search',query:{q:query.value.trim()}});if(!failure&&version===navigationVersion&&key===scopeKey.value)close()}catch(cause){if(version===navigationVersion&&key===scopeKey.value)error.value=cause instanceof Error?cause.message:'无法打开搜索结果，请重试'}finally{if(version===navigationVersion)navigating.value=false}}
function keydown(event:KeyboardEvent){
 // 中文输入法确认候选字的 Enter 不能当作提交搜索/打开需求。
 if(event.isComposing||composing.value||event.keyCode===229)return
 if(event.key==='Escape'&&opened.value){event.preventDefault();event.stopImmediatePropagation();close();return}
 if(event.key==='ArrowDown'||event.key==='ArrowUp'){event.preventDefault();if(!opened.value){openPopup();return}if(items.value.length)activeIndex.value=(activeIndex.value+(event.key==='ArrowDown'?1:-1)+items.value.length)%items.value.length;void nextTick(()=>root.value?.querySelector<HTMLElement>('#'+listID+'-'+activeIndex.value)?.scrollIntoView({block:'nearest'}));return}
 if(event.key==='Enter'){event.preventDefault();if(!opened.value)openPopup();else if(items.value[activeIndex.value])void choose(items.value[activeIndex.value]!);else if(!loading.value)void search()}
}
function pointerdown(event:PointerEvent){if(opened.value&&root.value&&!root.value.contains(event.target as Node))close()}
function focusout(event:FocusEvent){if(event.relatedTarget&&root.value&&!root.value.contains(event.relatedTarget as Node))close()}
watch(query,schedule,{flush:'sync'})
watch(scopeKey,()=>{invalidated.value=false;clearMemory()},{flush:'sync'})
watch(()=>workspace.session,()=>{invalidated.value=false;clearMemory()},{flush:'sync'})
watch(available,value=>{if(!value)clearMemory()},{flush:'sync'})
watch(()=>route.fullPath,close)
onMounted(()=>{document.addEventListener('pointerdown',pointerdown,true);window.addEventListener('devflow-identity-changed',invalidate);window.addEventListener('devflow-auth-expired',invalidate);window.addEventListener('devflow-account-disabled',invalidate);window.addEventListener('devflow-project-changed',clearMemory)})
onBeforeUnmount(()=>{disposed=true;clearMemory();document.removeEventListener('pointerdown',pointerdown,true);window.removeEventListener('devflow-identity-changed',invalidate);window.removeEventListener('devflow-auth-expired',invalidate);window.removeEventListener('devflow-account-disabled',invalidate);window.removeEventListener('devflow-project-changed',clearMemory)})
defineExpose({focus})
</script>
<template>
 <div ref="root" class="top-search-widget" :data-open="opened&&available" @focusout="focusout">
  <div class="top-search-input"><Icon name="search" :size="15"/><input ref="input" v-model="query" maxlength="200" :disabled="!available" role="combobox" aria-autocomplete="list" :aria-expanded="opened" :aria-controls="listID" :aria-activedescendant="opened&&items[activeIndex]?listID+'-'+activeIndex:undefined" :aria-label="t('全局搜索')" :placeholder="t('搜索内容、人员或部门…')" @focus="openPopup" @keydown="keydown" @compositionstart="composing=true;stopRequest()" @compositionend="composing=false;schedule()"><button v-if="query" type="button" :disabled="!available||navigating" :aria-label="t('清空搜索')" @click="clear">×</button><kbd v-else>⌘ K</kbd></div>
  <section v-if="opened&&available" class="top-search-popup" :aria-label="t('搜索建议')" :aria-busy="loading||navigating">
   <header><span>{{t('当前项目')}} · {{workspace.currentProject?.name}}</span><small v-if="searched&&!error">{{t('共 {count} 条结果',{count:total})}}</small></header>
   <p v-if="!query.trim()" class="top-search-state">{{t('输入标题、编号、负责人、开发人员或部门名称，即时查找相关事项。')}}</p>
   <p v-else-if="loading" class="top-search-state" role="status">{{t('正在检索…')}}</p>
   <div v-else-if="error" class="top-search-state top-search-error" role="alert">{{t(error)}} <button type="button" @click="search">{{t('重试')}}</button></div>
   <p v-else-if="searched&&!items.length" class="top-search-state">{{t('当前项目没有匹配结果，可查看跨项目结果。')}}</p>
   <div :id="listID" class="top-search-results" role="listbox" :aria-label="t('搜索结果')">
    <button v-for="(item,index) in items" :id="listID+'-'+index" :key="item.type+'-'+item.id" type="button" role="option" :aria-selected="activeIndex===index" :disabled="navigating||loading" :class="{active:activeIndex===index}" @mouseenter="activeIndex=index" @mousedown.prevent @click="choose(item)"><span class="top-search-type">{{t(item.type).slice(0,1)}}</span><span class="top-search-copy"><span><small>{{item.code}}</small><b>{{item.title}}</b></span><small v-if="item.snippet">{{item.snippet}}</small></span><em>{{t(item.type)}}</em></button>
   </div>
   <footer><span>{{t('↑ ↓ 选择 · Enter 打开 · Esc 关闭')}}</span><button type="button" :disabled="navigating" @click="more">{{t('更多跨项目结果')}} ↗</button></footer>
  </section>
 </div>
</template>
<style scoped>
.top-search-widget{position:relative;width:clamp(200px,23vw,340px);min-width:170px;color:var(--ink);font-size:12px}.top-search-input{display:flex;align-items:center;gap:8px;border:1px solid var(--line);border-radius:8px;background:var(--surface-soft,var(--surface));padding:7px 9px;min-height:35px;box-sizing:border-box;color:var(--muted)}.top-search-input:focus-within{border-color:var(--primary);box-shadow:0 0 0 3px color-mix(in srgb,var(--primary) 12%,transparent)}.top-search-input input{flex:1;min-width:0;border:0!important;outline:0!important;background:transparent!important;color:var(--ink);padding:0!important;font:inherit;box-shadow:none!important}.top-search-input input::placeholder{color:var(--muted)}.top-search-input kbd{flex:none;font-size:10px;color:var(--muted);border:1px solid var(--line);border-radius:3px;padding:1px 4px}.top-search-input button{background:transparent;border:0;font:inherit;color:var(--muted);cursor:pointer;padding:0 3px;font-size:17px}.top-search-popup{position:absolute;top:calc(100% + 8px);right:0;width:min(570px,calc(100vw - 24px));z-index:180;background:var(--surface);border:1px solid var(--line);box-shadow:0 16px 48px #0d142933;border-radius:12px;overflow:hidden}.top-search-popup header{padding:12px 14px;border-bottom:1px solid var(--line);display:flex;justify-content:space-between;gap:12px;color:var(--muted);font-size:11px}.top-search-popup header span{overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.top-search-popup header small{white-space:nowrap;font-size:10px}.top-search-state{padding:25px 16px;margin:0;color:var(--muted);font-size:12px;line-height:1.6;text-align:center}.top-search-error{color:var(--danger,#c24145)}.top-search-state button{background:none;border:0;text-decoration:underline;color:var(--primary);font:inherit}.top-search-results{max-height:min(410px,55vh);overflow:auto;overscroll-behavior:contain;padding:5px}.top-search-results:empty{padding:0}.top-search-results>button{width:100%;display:flex;align-items:center;gap:10px;text-align:left;border:0;border-radius:7px;background:transparent;padding:11px 9px;color:var(--ink);cursor:pointer}.top-search-results>button.active,.top-search-results>button:hover{background:var(--primary-soft)}.top-search-results>button:disabled{opacity:.5}.top-search-type{flex:none;width:27px;height:29px;border-radius:5px;display:grid;place-items:center;background:color-mix(in srgb,var(--primary) 12%,var(--surface));color:var(--primary);font-weight:600;font-size:12px}.top-search-copy{min-width:0;flex:1}.top-search-copy>span{display:flex;align-items:baseline;gap:7px;min-width:0}.top-search-copy>span>small{flex:none;color:var(--muted);font-size:10px}.top-search-copy b{font-size:12px;font-weight:550;overflow:hidden;white-space:nowrap;text-overflow:ellipsis}.top-search-copy>small{display:block;margin-top:5px;font-size:10px;color:var(--muted);overflow:hidden;white-space:nowrap;text-overflow:ellipsis}.top-search-results em{font-size:10px;font-style:normal;color:var(--muted);flex:none}.top-search-popup footer{padding:11px 14px;border-top:1px solid var(--line);display:flex;align-items:center;justify-content:space-between;gap:8px}.top-search-popup footer>span{font-size:10px;color:var(--muted)}.top-search-popup footer button{border:0;background:transparent;color:var(--primary);font-size:11px;padding:2px;cursor:pointer}.top-search-popup button:focus-visible{outline:2px solid var(--primary);outline-offset:-2px}@media(max-width:850px){.top-search-widget{width:210px}.top-search-popup footer>span{display:none}}@media(max-width:650px){.top-search-widget{width:180px;min-width:120px}.top-search-popup{right:-50px}.top-search-input kbd{display:none}}
:global(.workspace>.topbar:has(.top-search-widget[data-open="true"])){position:relative;z-index:190}
@media(max-width:760px){.top-search-widget{width:100%;min-width:0}.top-search-popup{left:0;right:0;width:100%;max-width:100%;max-height:calc(100dvh - 155px);display:flex;flex-direction:column;box-sizing:border-box}.top-search-popup header,.top-search-popup footer{flex:none;padding:11px 10px}.top-search-results{min-height:0;flex:1;max-height:min(410px,48dvh)}.top-search-input{min-height:40px}.top-search-input input{font-size:16px;min-height:24px}.top-search-input button{min-width:32px;min-height:28px}.top-search-copy>span{display:block}.top-search-copy b{display:block;line-height:1.5}.top-search-copy>span>small{font-size:11px}.top-search-results>button{min-height:54px;padding:10px 7px;gap:8px}.top-search-popup footer button{min-height:32px;text-align:left;white-space:normal}.top-search-input kbd{display:none}}
</style>

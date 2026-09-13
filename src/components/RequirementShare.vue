<script setup lang="ts">
import {ref,watch} from 'vue'
import {t,formatDate} from '../i18n'
import {useSettingsScope,useSettingsDialog} from './settingsScope'
import ResizableDrawer from './ResizableDrawer.vue'
const props=defineProps<{requirementId:number}>()
const scope=useSettingsScope(),open=ref(false),publicAccess=ref(false),confirmed=ref(false),busy=ref(false),error=ref(''),url=ref(''),items=ref<{id:number;expiresAt:string}[]>([])
const dialog=useSettingsDialog(open,()=>{if(!busy.value)open.value=false})
const endpoint=()=>'/requirement-shares?requirementId='+props.requirementId
async function load(){try{const data=await scope.request<{items:typeof items.value}>(endpoint());if(scope.current())items.value=data.items}catch(e){error.value=e instanceof Error?e.message:'读取失败'}}
watch(open,value=>{if(value){url.value='';error.value='';void load()}})
watch([publicAccess,scope.locked],()=>{url.value='';confirmed.value=false})
async function generate(){
 if(busy.value||!scope.current())return;busy.value=true;error.value=''
 try{
  if(publicAccess.value){if(!confirmed.value)return;const result=await scope.request<{path:string}>(endpoint(),{method:'POST',body:JSON.stringify({confirmed:true})});if(!scope.current())return;url.value=new URL(result.path,location.origin).href;await load()}
  else {const session=await scope.request<any>('/session');if(!scope.current())return;url.value=location.origin+'/requirements?req='+props.requirementId+'&project='+encodeURIComponent(session.project.id)}
 }catch(e){error.value=e instanceof Error?e.message:'生成失败'}finally{busy.value=false}
}
async function revoke(id:number){busy.value=true;error.value='';try{await scope.request(endpoint()+'&shareId='+id,{method:'DELETE'});url.value='';await load()}catch(e){error.value=e instanceof Error?e.message:'撤销失败'}finally{busy.value=false}}
async function copy(){try{await navigator.clipboard.writeText(url.value)}catch{error.value='请选中下方链接并复制'}}
</script>
<template>
 <button type="button" :disabled="scope.locked.value" @click="open=true">{{t('分享')}}</button>
 <Teleport to="body"><div v-if="open" class="share-shade" @click.self="!busy&&(open=false)"><ResizableDrawer :label="t('分享需求')" :initial-width="540" :min-width="320"><section ref="dialog" class="share-content" tabindex="-1">
 <header><h2>{{t('分享需求')}}</h2><button type="button" :disabled="busy" :aria-label="t('关闭分享')" @click="open=false">×</button></header>
 <label><input v-model="publicAccess" type="checkbox" :disabled="busy">{{t('允许免登录查看')}}</label>
 <p v-if="!publicAccess">{{t('默认需要登录，并且拥有该项目的访问权限。')}}</p>
 <template v-else><p>{{t('公开标题、正文文字及验收标准的只读快照，7 天后失效。不会公开内部评论、人员信息和附件文件；正文中的敏感文字仍会公开，请先检查。')}}</p><label><input v-model="confirmed" type="checkbox" :disabled="busy">{{t('我已检查正文，同意任何持有链接的人查看')}}</label></template>
 <button class="btn primary" type="button" :disabled="busy||scope.locked.value||(publicAccess&&!confirmed)" @click="generate">{{t(busy?'处理中…':'生成分享链接')}}</button>
 <div v-if="url"><label>{{t('分享链接')}}<input :value="url" readonly :aria-label="t('分享链接')" @focus="($event.target as HTMLInputElement).select()"></label><button class="btn" type="button" @click="copy">{{t('复制链接')}}</button></div>
 <p v-if="error" role="alert">{{t(error)}}</p>
 <h3>{{t('我的有效公开分享')}}</h3><p v-if="!items.length">{{t('暂无公开分享')}}</p><div v-for="item in items" :key="item.id" class="share-row"><span>{{t('有效至')}} {{formatDate(item.expiresAt)}}</span><button class="btn" type="button" :disabled="busy||scope.locked.value" @click="revoke(item.id)">{{t('撤销')}}</button></div>
 </section></ResizableDrawer></div></Teleport>
</template>
<style scoped>
.share-shade{position:fixed;inset:0;background:#1b254d55;z-index:1800;display:flex;justify-content:flex-end}.share-content{padding:24px;min-width:0;overflow:auto;height:100%;background:var(--surface,#fff)}header,.share-row{display:flex;align-items:center;justify-content:space-between;gap:12px}h2{font-size:20px}p{color:var(--muted);line-height:1.7;font-size:14px}label{display:block;margin:16px 0;line-height:1.7}input[readonly]{display:block;width:100%;min-width:0;margin-top:8px}.share-row{padding:12px 0;border-bottom:1px solid var(--line);font-size:13px}button{flex-shrink:0}
</style>

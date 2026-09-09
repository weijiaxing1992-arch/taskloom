<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { api, apiDownload } from '../api'
import { t, formatDate } from '../i18n'
import ImagePreview from './ImagePreview.vue'
import AssetIcon from './AssetIcon.vue'
import AssetCodePreview from './AssetCodePreview.vue'
import {assetCategories,assetCategory,assetLanguage,assetLabel} from '../attachmentAssets'
type ResourceDraft={url:string;title:string;opened:boolean}
const props=defineProps<{requirementId:number;canEdit:boolean;modelValue?:ResourceDraft;refreshToken?:number}>()
const emit=defineEmits<{(event:'update:modelValue',value:ResourceDraft):void}>()
type Attachment={id:number;name:string;size:number;createdByName:string;createdAt:string;contentType?:string;category?:string;language?:string}
type DesignLink={id:number;title:string;url:string}
const attachments=ref<Attachment[]>([]),links=ref<DesignLink[]>([]),loading=ref(false),busy=ref(false),error=ref(''),message=ref('')
const filter=ref('all'),uploadCategory=ref('auto'),codeTarget=ref<Attachment|null>(null)
const categoryOf=(item:Attachment)=>assetCategory(item.name,item.category)
const filteredAttachments=computed(()=>attachments.value.filter(item=>filter.value==='all'||categoryOf(item)===filter.value))
const filteredLinks=computed(()=>filter.value==='all'||filter.value==='design'?links.value:[])
async function classify(item:Attachment,event:Event){if(busy.value||loading.value||!props.canEdit)return;const value=(event.target as HTMLSelectElement).value,request=version,path=root();busy.value=true;error.value='';try{const saved=await api<{category:string;language?:string}>(path+'/attachments/'+item.id,{method:'PATCH',body:JSON.stringify({category:value})});if(request===version){Object.assign(item,saved);window.dispatchEvent(new CustomEvent('devflow-asset-classified',{detail:{requirementId:props.requirementId,attachmentId:item.id}}));message.value='附件分类已保存'}}catch(cause){if(request===version)error.value=cause instanceof Error?cause.message:'分类保存失败'}finally{if(request===version){busy.value=false;(event.target as HTMLSelectElement).value=categoryOf(item)}}}
const previewOpen=ref(false),previewIndex=ref(0)
function canPreviewCode(item:Attachment){return !!(item.language||assetLanguage(item.name))||['code','api'].includes(categoryOf(item))}
function isPreviewImage(item:Attachment){return ['image/png','image/jpeg','image/gif'].includes((item.contentType||'').split(';')[0]!.toLowerCase())||/\.(png|jpe?g|gif)$/i.test(item.name)}
const previewItems=computed(()=>attachments.value.filter(isPreviewImage).map(item=>({key:'attachment-'+item.id,name:item.name,attachmentId:item.id})))
function openPreview(item:Attachment){if(busy.value||loading.value)return;const index=previewItems.value.findIndex(image=>image.attachmentId===item.id);if(index<0)return;previewIndex.value=index;previewOpen.value=true}
const localDraft=ref<ResourceDraft>({url:'',title:'',opened:false})
const draft=computed(()=>props.modelValue||localDraft.value)
function updateDraft(change:Partial<ResourceDraft>){const value={...draft.value,...change};if(!props.modelValue)localDraft.value=value;emit('update:modelValue',value)}
const url=computed({get:()=>draft.value.url,set:value=>updateDraft({url:value})}),title=computed({get:()=>draft.value.title,set:value=>updateDraft({title:value})}),showLink=computed({get:()=>draft.value.opened,set:value=>updateDraft({opened:value})})
const removeTarget=ref<{kind:'attachments'|'design-links';id:number;name:string}|null>(null)
const input=ref<HTMLInputElement|null>(null)
let version=0
const root=()=>`/requirements/${props.requirementId}`
function sizeLabel(bytes:number){return bytes<1024?`${bytes} B`:bytes<1024*1024?`${(bytes/1024).toFixed(1)} KB`:`${(bytes/1024/1024).toFixed(1)} MB`}
function safeFigma(value:string){try{const u=new URL(value);return u.protocol==='https:'&&['figma.com','www.figma.com'].includes(u.hostname)&&!u.username&&!u.password&&(!u.port||u.port==='443')&&/^\/(design|file|proto)\/[^/]+/.test(u.pathname)}catch{return false}}
async function load(){
  if(busy.value)return
  const request=++version;loading.value=true;error.value=''
  try{const [a,l]=await Promise.all([api<{items:Attachment[]}>(root()+'/attachments'),api<{items:DesignLink[]}>(root()+'/design-links')]);if(request===version){attachments.value=a.items||[];links.value=l.items||[]}}
  catch(cause){if(request===version)error.value=cause instanceof Error?cause.message:t('请求失败')}
  finally{if(request===version)loading.value=false}
}
async function upload(event:Event){
  const file=(event.target as HTMLInputElement).files?.[0];if(!file||busy.value||!props.canEdit)return
  error.value='';message.value=''
  if(file.size>10*1024*1024){error.value=t('单个附件不能超过 10 MB');if(input.value)input.value.value='';return}
  if(loading.value)return
  const request=version,path=root();busy.value=true
  try{const body=new FormData();body.append('file',file);const result=await api<Attachment>(path+'/attachments?category='+encodeURIComponent(uploadCategory.value),{method:'POST',body});if(request===version){attachments.value.unshift(result);filter.value='all';message.value=t('附件已上传')}}
  catch(cause){if(request===version)error.value=cause instanceof Error?cause.message:t('请求失败')}
  finally{if(request===version){busy.value=false;if(input.value)input.value.value=''}}
}
async function download(item:Attachment){
  if(busy.value)return;const request=version,path=root();busy.value=true;error.value=''
  try{const blob=await apiDownload(path+'/attachments/'+item.id);if(request!==version)return;const href=URL.createObjectURL(blob),anchor=document.createElement('a');anchor.href=href;anchor.download=item.name;anchor.click();setTimeout(()=>URL.revokeObjectURL(href),1000)}
  catch(cause){if(request===version)error.value=cause instanceof Error?cause.message:t('请求失败')}
  finally{if(request===version)busy.value=false}
}
async function addLink(){
  if(busy.value||loading.value||!props.canEdit)return;error.value='';message.value=''
  if(!safeFigma(url.value.trim())){error.value=t('请输入有效的 Figma 文件链接');return}
  const request=version,path=root();busy.value=true
  try{const result=await api<DesignLink>(path+'/design-links',{method:'POST',body:JSON.stringify({title:title.value.trim(),url:url.value.trim()})});if(request===version){links.value.unshift(result);discardLink();message.value=t('Figma 文件已关联')}}
  catch(cause){if(request===version)error.value=cause instanceof Error?cause.message:t('请求失败')}
  finally{if(request===version)busy.value=false}
}
async function remove(){
  if(!removeTarget.value||busy.value||loading.value||!props.canEdit)return;const target=removeTarget.value,request=version,path=root();busy.value=true;error.value='';message.value=''
  try{await api(path+'/'+target.kind+'/'+target.id,{method:'DELETE'});if(request===version){if(target.kind==='attachments')attachments.value=attachments.value.filter(x=>x.id!==target.id);else links.value=links.value.filter(x=>x.id!==target.id);removeTarget.value=null;message.value=t('关联资源已移除')}}
  catch(cause){if(request===version)error.value=cause instanceof Error?cause.message:t('请求失败')}
  finally{if(request===version)busy.value=false}
}
function discardLink(){updateDraft({url:'',title:'',opened:false})}
watch(()=>props.requirementId,(_,previous)=>{previewOpen.value=false;codeTarget.value=null;filter.value='all';attachments.value=[];links.value=[];busy.value=false;message.value='';if(previous!==undefined)discardLink();removeTarget.value=null;void load()},{immediate:true})
watch(()=>props.refreshToken,()=>{void load()})
onBeforeUnmount(()=>{version++})
</script>
<template>
 <section class="detail-block resources-block" :aria-busy="loading||busy">
  <header><div><h3>{{t('附件资产')}}</h3><p>{{t('缺陷、接口、设计稿和代码统一归档，正文与评论附件自动汇入。')}}</p></div><div v-if="canEdit" class="resource-actions"><select v-model="uploadCategory" :aria-label="t('上传附件分类')" :disabled="busy||loading"><option value="auto">{{t('自动分类')}}</option><option v-for="kind in assetCategories" :key="kind.value" :value="kind.value">{{t(kind.label)}}</option></select><button type="button" class="btn compact" :disabled="busy||loading" @click="input?.click()">{{t('上传附件')}}</button><button type="button" class="btn compact" :disabled="busy||loading" @click="showLink=true">{{t('关联 Figma')}}</button><input ref="input" type="file" class="file-input" :aria-label="t('上传需求附件')" @change="upload"></div></header>
  <p v-if="error" class="inline-notice" role="alert">{{error}} <button type="button" class="link" :disabled="loading||busy" @click="load">{{t('重试')}}</button></p>
  <p v-if="message" class="resource-success" role="status">{{message}}</p>
  <p v-if="loading" class="resource-note" role="status">{{t('加载中…')}}</p>
  <form v-if="showLink&&canEdit" class="figma-form" @submit.prevent="addLink">
   <label>{{t('Figma 文件标题')}}<input v-model="title" maxlength="200" :disabled="busy" :placeholder="t('例如：需求详情交互设计')"></label>
   <label>{{t('Figma 文件链接')}}<input v-model="url" type="url" required maxlength="2048" :disabled="busy" placeholder="https://www.figma.com/design/…"></label>
   <small>{{t('仅保存链接；访问设计文件仍遵循 Figma 原有权限。')}}</small><small>{{t('取消将清空未关联的链接草稿。')}}</small><div><button type="button" class="btn compact" :disabled="busy" @click="discardLink">{{t('取消')}}</button><button type="submit" class="btn primary compact" :disabled="busy||loading">{{t(busy?'保存中…':'关联文件')}}</button></div>
  </form>
  <div v-if="removeTarget" class="resource-confirm" role="alertdialog" :aria-label="t('移除资源确认')"><p>{{t('确定移除「{name}」？附件内容将被删除，Figma 原文件不受影响。',{name:removeTarget.name})}}</p><button class="btn compact" :disabled="busy" @click="removeTarget=null">{{t('取消')}}</button><button class="btn compact" :disabled="busy" @click="remove">{{t('确认移除')}}</button></div>
  <nav class="asset-filters" :aria-label="t('附件分类筛选')"><button type="button" :aria-pressed="filter==='all'" @click="filter='all'">{{t('全部')}} {{attachments.length+links.length}}</button><button v-for="kind in assetCategories" :key="kind.value" type="button" :aria-pressed="filter===kind.value" @click="filter=kind.value">{{t(kind.label)}} {{attachments.filter(item=>categoryOf(item)===kind.value).length+(kind.value==='design'?links.length:0)}}</button></nav>
  <AssetCodePreview v-if="codeTarget" :name="codeTarget.name" :requirement-id="requirementId" :attachment-id="codeTarget.id" @close="codeTarget=null"/>
  <ul class="resource-list">
   <li v-for="item in filteredAttachments" :key="'a'+item.id"><AssetIcon :category="categoryOf(item)" :language="item.language||assetLanguage(item.name)"/><div><button type="button" class="link file-name" :disabled="busy" @click="isPreviewImage(item)?openPreview(item):canPreviewCode(item)?codeTarget=item:download(item)">{{item.name}}</button><small>{{sizeLabel(item.size)}} · {{item.createdByName}} · {{formatDate(item.createdAt)}}</small></div><select v-if="canEdit" :value="categoryOf(item)" :disabled="busy||loading" :aria-label="t('{name} 的资产分类',{name:item.name})" @change="classify(item,$event)"><option v-for="kind in assetCategories" :key="kind.value" :value="kind.value">{{t(kind.label)}}</option><option value="auto">{{t('自动分类')}}</option></select><span v-else class="asset-category-label">{{t(assetLabel(categoryOf(item)))}}</span><template v-if="isPreviewImage(item)"><button type="button" class="link resource-preview" :disabled="busy||loading" :aria-label="t('预览 {name}',{name:item.name})" @click="openPreview(item)">{{t('预览')}}</button></template><button v-else-if="canPreviewCode(item)" type="button" class="link resource-preview" :disabled="busy||loading" @click="codeTarget=item">{{t(item.language||assetLanguage(item.name)?'代码预览':'文本预览')}}</button><button type="button" class="link resource-preview" :disabled="busy" @click="download(item)">{{t('下载')}}</button><button v-if="canEdit" class="remove-resource" :disabled="busy" :aria-label="t('移除 {name}',{name:item.name})" @click="removeTarget={kind:'attachments',id:item.id,name:item.name}">×</button></li>
   <li v-for="item in filteredLinks" :key="'l'+item.id"><AssetIcon category="design"/><div><a v-if="safeFigma(item.url)" :href="item.url" target="_blank" rel="noopener noreferrer" class="link file-name">{{item.title||t('Figma 设计文件')}} ↗</a><span v-else>{{item.title}}</span><small>{{item.url}}</small></div><button v-if="canEdit" class="remove-resource" :disabled="busy" :aria-label="t('移除 {name}',{name:item.title||'Figma'})" @click="removeTarget={kind:'design-links',id:item.id,name:item.title||'Figma'}">×</button></li>
  </ul>
  <p v-if="!loading&&!filteredAttachments.length&&!filteredLinks.length" class="resource-note">{{t('暂无附件或设计文件')}}</p><small class="resource-note">{{t('单个附件最大 10 MB；请勿上传密码或密钥。')}}</small>
  <ImagePreview v-model:open="previewOpen" :items="previewItems" :initial-index="previewIndex" :requirement-id="requirementId" />
 </section>
</template>
<style scoped>
.resources-block>header{display:flex;justify-content:space-between;gap:16px;flex-wrap:wrap}.resources-block h3{margin:0}.resources-block header p,.resource-note{font-size:12px;color:#667085;line-height:1.7}.resource-actions{display:flex;flex-wrap:wrap;gap:8px;align-items:flex-start}.file-input{display:none}.resource-list{list-style:none;padding:0;margin:12px 0}.resource-list li{display:flex;flex-wrap:wrap;gap:12px;align-items:center;border:1px solid #e7eaf1;border-radius:8px;padding:12px;margin:8px 0;background:#fcfcfe}.resource-list li>div{min-width:120px;flex:1}.resource-list small{display:block;color:#667085;font-size:11px;margin-top:5px;overflow-wrap:anywhere}.file-name{font-size:13px;overflow-wrap:anywhere;text-align:left}.resource-icon{width:32px;height:36px;background:#e9edff;color:#5b5bd6;display:grid;place-items:center;border-radius:6px}.resource-icon.figma{background:#f2ebff;color:#933de3;font-weight:700}.remove-resource{background:none;border:0;color:#667085;font-size:20px;cursor:pointer}.figma-form{margin:12px 0;background:#f8f9fc;border:1px solid var(--line);border-radius:8px;padding:16px;display:grid;gap:10px}.figma-form label{display:grid;gap:6px;font-size:12px}.figma-form input{border:1px solid #d0d5dd;border-radius:6px;padding:9px;width:100%;font:inherit}.figma-form>div{text-align:right;display:flex;justify-content:flex-end;gap:8px}.figma-form small{color:#667085;font-size:11px}.resource-success{color:#07856b;font-size:12px}.resource-confirm{background:#fff5eb;border:1px solid #ffd3a1;padding:12px;border-radius:8px;margin:12px 0;font-size:13px}.resource-confirm button+button{margin-left:8px}
.asset-filters{display:flex;gap:6px;flex-wrap:wrap;margin-top:12px}.asset-filters button,.resource-actions select,.resource-list select{border:1px solid var(--line,#e4e7ec);border-radius:4px;background:transparent;padding:5px 8px;font:inherit;font-size:12px;color:var(--text,#344054)}.asset-filters button[aria-pressed=true]{border-color:#c6d4ff;background:#eef3ff;color:#365ed5}.asset-category-label{font-size:12px;color:var(--muted,#667085)}
</style>

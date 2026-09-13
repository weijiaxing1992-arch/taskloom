<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { onBeforeRouteLeave, onBeforeRouteUpdate } from 'vue-router'
import { apiDownload } from '../api'
import { t, formatDate } from '../i18n'
import { downloadFile } from '../workItemExport'
import { cloneReleaseNotes, parseReleaseNotes, releaseEntriesError, releaseImageMime, releaseImagePath, releaseNoteCategories, releaseNoteFilename, releaseNoteGroups, releaseNoteJSON, releaseNotesPending, type ReleaseNotes, type ReleaseNoteEntry, type ReleaseNoteImage } from '../releaseNotes'
import { useSettingsScope } from './settingsScope'
import OrganizationModal from './OrganizationModal.vue'
import AIButton from './AIButton.vue'
import AppSelect from './AppSelect.vue'
import { Button } from './ui/button'

const props=defineProps<{projectId:string;sprintId:number;sprintName:string}>()
const emit=defineEmits<{(event:'close'):void;(event:'updated',state:string):void}>()
const scope=useSettingsScope(),note=ref<ReleaseNotes|null>(null),entries=ref<ReleaseNoteEntry[]>([])
const loading=ref(false),writing=ref(false),editing=ref(false),error=ref(''),notice=ref(''),conflict=ref(false)
const exporting=ref(false)
const imageURLs=ref<Record<number,string>>({}),imageErrors=ref<Record<number,string>>({}),imageLoading=ref<Record<number,boolean>>({})
const dirty=computed(()=>!!note.value&&JSON.stringify(entries.value)!==JSON.stringify(note.value.entries))
const pending=computed(()=>!!note.value&&releaseNotesPending(note.value.state))
const groups=computed(()=>releaseNoteGroups(entries.value))
const categoryOptions=computed(()=>releaseNoteCategories.map(category=>({value:category,label:t(category)})))
const missingImageCount=computed(()=>entries.value.filter(entry=>entry.imageIds.length===0).length)
const releaseReady=computed(()=>!!note.value?.entries.length&&note.value.state==='draft'&&!note.value.sourceChanged&&!dirty.value&&!conflict.value&&missingImageCount.value===0)
const canGenerate=computed(()=>!!note.value?.canGenerate&&note.value.configured&&note.value.enabled&&!pending.value&&!writing.value&&!loading.value&&!exporting.value&&!scope.locked.value&&!dirty.value&&!conflict.value)
const canEdit=computed(()=>!!note.value?.canEdit&&!pending.value&&!writing.value&&!loading.value&&!exporting.value&&!scope.locked.value)
const stateText=computed(()=>({none:'尚未生成',queued:'排队中',generating:'正在生成',draft:'待复核草稿',failed:'生成失败'}[note.value?.state||'none']))
let disposed=false,version=0,poll:ReturnType<typeof setTimeout>|undefined,projectLeaveApproved=false
let imageController=new AbortController()
let bundleController=new AbortController()
const context=()=>`${props.projectId}:${props.sprintId}`
const valid=(key=context())=>!disposed&&scope.current()&&props.projectId===scope.project&&key===context()&&Number.isSafeInteger(props.sprintId)&&props.sprintId>0
function stopPoll(){clearTimeout(poll);poll=undefined}
function clearImages(){imageController.abort();imageController=new AbortController();for(const url of Object.values(imageURLs.value))URL.revokeObjectURL(url);imageURLs.value={};imageErrors.value={};imageLoading.value={}}
function apply(data:unknown){note.value=parseReleaseNotes(data);entries.value=cloneReleaseNotes(note.value.entries);error.value='';conflict.value=false;clearImages();emit('updated',note.value.state)}
function schedule(){stopPoll();if(valid()&&pending.value&&!dirty.value)poll=setTimeout(()=>{void load(false)},2500)}
async function load(manual=true){
  if(!valid()||writing.value||exporting.value)return
  if(manual&&dirty.value&&!window.confirm(t('刷新将放弃尚未保存的升级日志修改，继续吗？')))return
  const key=context(),request=++version;stopPoll();loading.value=true;error.value=''
  try{const data=await scope.request<ReleaseNotes>(`/sprints/${props.sprintId}/release-notes`);if(!valid(key)||request!==version)return;apply(data);if(manual)editing.value=false;schedule()}
  catch(cause){if(valid(key)&&request===version)error.value=cause instanceof Error?cause.message:'升级日志暂时无法加载，请重试'}
  finally{if(valid(key)&&request===version)loading.value=false}
}
async function generate(){
  if(!valid()||!canGenerate.value||!note.value)return
  const previous=note.value,key=context(),replace=previous.entries.length>0
  if(replace&&!window.confirm(t('重新生成将替换已有升级日志草稿及人工编辑，是否继续？')))return
  if(!window.confirm(t('将把本迭代已完成需求的标题、正文、验收标准以及图片候选 ID 和文件名发送至 {address}，用于生成升级日志草稿；不包含缺陷，不发送图片字节。可能产生模型费用，是否继续？',{address:previous.baseUrl})))return
  if(!valid(key)||!canGenerate.value)return
  const request=++version;writing.value=true;error.value='';notice.value='';stopPoll()
  try{
    const data=await scope.request<ReleaseNotes>(`/sprints/${props.sprintId}/release-notes`,{method:'POST',body:JSON.stringify({confirmed:true,expectedRevision:previous.revision,expectedSettingsVersion:previous.settingsVersion,...(replace?{replaceDraft:true}:{})})})
    if(!valid(key)||request!==version)return
    apply(data);editing.value=false;notice.value='任务已提交，可关闭抽屉；生成不会阻塞迭代完成';schedule()
  }catch(cause){if(valid(key)&&request===version){error.value=cause instanceof Error?cause.message:'升级日志生成请求失败，请刷新确认任务状态';conflict.value=(cause as {status?:number})?.status===409}}
  finally{if(valid(key)&&request===version)writing.value=false}
}
async function save(){
  if(!valid()||!canEdit.value||!dirty.value||!note.value||conflict.value)return
  const problem=releaseEntriesError(entries.value,note.value.images,note.value.sources);if(problem){error.value=problem;return}
  const key=context(),request=++version,patch=cloneReleaseNotes(entries.value);writing.value=true;error.value='';notice.value=''
  try{const data=await scope.request<ReleaseNotes>(`/sprints/${props.sprintId}/release-notes`,{method:'PATCH',body:JSON.stringify({expectedRevision:note.value.revision,entries:patch})});if(valid(key)&&request===version){apply(data);editing.value=false;notice.value='升级日志修改已保存'}}
  catch(cause){if(valid(key)&&request===version){error.value=cause instanceof Error?cause.message:'升级日志保存失败，修改已保留';conflict.value=(cause as {status?:number})?.status===409}}
  finally{if(valid(key)&&request===version)writing.value=false}
}
function discard(){if(writing.value||!note.value)return;entries.value=cloneReleaseNotes(note.value.entries);editing.value=false;error.value='';notice.value=''}
function caption(entry:ReleaseNoteEntry,image:ReleaseNoteImage){return entry.imageCaptions?.[String(image.id)]??image.name}
function setCaption(entry:ReleaseNoteEntry,image:ReleaseNoteImage,value:string){if(!canEdit.value||!editing.value)return;entry.imageCaptions={...(entry.imageCaptions||{}),[String(image.id)]:value}}
function availableImages(entry:ReleaseNoteEntry){return (note.value?.images||[]).filter(image=>entry.requirementIds.includes(image.requirementId))}
function imagesFor(entry:ReleaseNoteEntry){return (note.value?.images||[]).filter(image=>entry.imageIds.includes(image.id)&&entry.requirementIds.includes(image.requirementId))}
function toggleImage(entry:ReleaseNoteEntry,image:ReleaseNoteImage,checked:boolean){
  if(!canEdit.value||!editing.value||!availableImages(entry).some(item=>item.id===image.id))return
  const selected=new Set(entry.imageIds)
  if(checked){
    if(selected.has(image.id))return
    if(selected.size>=8){error.value='每项升级内容最多选择 8 张截图';return}
    selected.add(image.id);void loadImage(image)
  }else{
    selected.delete(image.id)
    if(entry.imageCaptions){const captions={...entry.imageCaptions};delete captions[String(image.id)];entry.imageCaptions=Object.keys(captions).length?captions:undefined}
  }
  entry.imageIds=availableImages(entry).map(item=>item.id).filter(id=>selected.has(id))
  error.value=''
}
async function loadImage(image:ReleaseNoteImage){
  if(!valid()||imageLoading.value[image.id]||imageURLs.value[image.id]||!note.value?.images.some(item=>item.id===image.id&&item.requirementId===image.requirementId))return
  const key=context(),controller=imageController
  imageLoading.value={...imageLoading.value,[image.id]:true};imageErrors.value={...imageErrors.value,[image.id]:''}
  try{
    const blob=await apiDownload(releaseImagePath(image),{headers:{'X-TaskLoom-Project':props.projectId},signal:controller.signal})
    if(blob.size>20*1024*1024)throw Error('截图超过 20 MB，请查看原需求附件')
    const type=releaseImageMime(new Uint8Array(await blob.slice(0,12).arrayBuffer()))
    if(!valid(key)||controller.signal.aborted)return
    imageURLs.value={...imageURLs.value,[image.id]:URL.createObjectURL(new Blob([blob],{type}))}
  }catch(cause){if(valid(key)&&!controller.signal.aborted)imageErrors.value={...imageErrors.value,[image.id]:cause instanceof Error?cause.message:'截图加载失败，请重试'}}
  finally{if(valid(key)&&!controller.signal.aborted)imageLoading.value={...imageLoading.value,[image.id]:false}}
}
function toggleImagePreview(image:ReleaseNoteImage){
  const url=imageURLs.value[image.id]
  if(!url){void loadImage(image);return}
  URL.revokeObjectURL(url)
  const next={...imageURLs.value};delete next[image.id];imageURLs.value=next
}
function exportNotes(format:'md'|'json'){
  if(!valid()||!note.value||note.value.sourceChanged||dirty.value||writing.value||exporting.value||!note.value.entries.length)return
  if(format==='md'&&!note.value.markdown){error.value='升级日志 Markdown 暂不可用，请刷新重试';return}
  downloadFile(new Blob([format==='md'?note.value.markdown:releaseNoteJSON(note.value)],{type:format==='md'?'text/markdown;charset=utf-8':'application/json;charset=utf-8'}),releaseNoteFilename(note.value.sprintName||props.sprintName,format))
}
async function downloadBundle(){
  if(!valid()||!note.value?.entries.length||note.value.state!=='draft'||note.value.sourceChanged||dirty.value||writing.value||loading.value||exporting.value||conflict.value)return
  const key=context(),revision=note.value.revision,name=note.value.sprintName||props.sprintName,controller=bundleController
  exporting.value=true;error.value=''
  try{
    const blob=await apiDownload('/sprints/'+props.sprintId+'/release-notes/bundle?expectedRevision='+revision,{headers:{'X-TaskLoom-Project':props.projectId},signal:controller.signal})
    if(!valid(key)||controller.signal.aborted||note.value?.revision!==revision||dirty.value)return
    downloadFile(blob,releaseNoteFilename(name,'zip'))
  }catch(cause){if(valid(key)&&!controller.signal.aborted){error.value=cause instanceof Error?cause.message:'图文包下载失败，请刷新后重试';conflict.value=(cause as {status?:number})?.status===409}}
  finally{if(valid(key)&&!controller.signal.aborted)exporting.value=false}
}
function canLeave(){return !writing.value&&(projectLeaveApproved||!dirty.value||window.confirm(t('升级日志尚未保存，离开将放弃修改，继续吗？')))}
function close(){if(canLeave()){stopPoll();emit('close')}}
function beforeProjectChange(event:Event){projectLeaveApproved=false;if(!canLeave())event.preventDefault();else projectLeaveApproved=true}
function cancelProjectLeave(){projectLeaveApproved=false}
function beforeUnload(event:BeforeUnloadEvent){if(writing.value||dirty.value&&!projectLeaveApproved){event.preventDefault();event.returnValue=''}}
function invalidate(){version++;stopPoll();clearImages();bundleController.abort();bundleController=new AbortController();note.value=null;entries.value=[];editing.value=false;loading.value=false;writing.value=false;exporting.value=false;error.value='';notice.value='';conflict.value=false}
watch(scope.locked,locked=>{if(locked)invalidate()},{flush:'sync'})
watch(context,()=>{invalidate();void load(false)},{flush:'sync'})
onBeforeRouteLeave(canLeave);onBeforeRouteUpdate(canLeave)
onMounted(()=>{void load(false);window.addEventListener('devflow-before-project-change',beforeProjectChange);window.addEventListener('devflow-project-change-cancelled',cancelProjectLeave);window.addEventListener('beforeunload',beforeUnload)})
onBeforeUnmount(()=>{disposed=true;invalidate();window.removeEventListener('devflow-before-project-change',beforeProjectChange);window.removeEventListener('devflow-project-change-cancelled',cancelProjectLeave);window.removeEventListener('beforeunload',beforeUnload)})
defineExpose({canLeave,dirty,load})
</script>
<template>
  <OrganizationModal :title="t('迭代升级日志')" :busy="writing" wide @close="close">
    <section class="release-notes-panel" :aria-busy="loading||writing">
      <header class="release-heading"><div><h3>{{note?.sprintName||sprintName}}</h3><p>{{note?.completedAt?formatDate(note.completedAt):t('完成时间待确认')}}</p></div><span class="release-state" :class="note?.state" role="status">{{t(stateText)}}</span></header>
      <p v-if="scope.locked.value" class="release-error" role="alert">{{t('项目或账号已变化，请刷新页面后继续')}}</p>
      <p v-if="error" class="release-error" role="alert">{{t(error)}}</p>
      <p v-if="conflict" class="release-error">{{t('服务器版本已变化，当前修改已保留；请复制所需内容后刷新并重新编辑。')}}</p>
      <p v-if="notice" class="release-notice" role="status">{{t(notice)}}</p>
      <p v-if="note?.state==='failed'&&note.error" class="release-error" role="alert">{{note.error}}</p>
      <p v-if="note?.sourceChanged" class="release-error" role="alert">{{t('完成需求已变化，现有草稿仅供参考；请重新生成后再编辑或导出。')}}</p>
      <div class="release-tools"><AIButton :busy="writing" :disabled="!canGenerate" @click="generate">{{t(note?.state==='failed'?'重试生成':note?.entries.length?'重新生成升级日志':'AI 生成升级日志')}}</AIButton><Button variant="outline" :disabled="writing||loading||scope.locked.value" @click="load(true)">{{t('刷新')}}</Button><Button v-if="canEdit&&entries.length&&!editing" variant="outline" @click="editing=true">{{t('编辑日志')}}</Button></div>
      <details class="release-boundary"><summary>{{t('生成范围与数据说明')}}</summary><p>{{t('仅汇总本迭代完成需求，排除缺陷；来源文字以及图片候选 ID 和文件名会发送给企业配置的 AI 服务，图片字节不外发。内容可能包含正文或文件名中手动填写的敏感信息，请先核实授权。')}}</p><p v-if="note?.model">{{t('模型')}}：{{note.model}}</p><p v-if="note?.baseUrl">{{t('服务地址')}}：{{note.baseUrl}}</p><p>{{t('截图仅来自真实需求附件，无图标记待补图；AI 草稿需人工核实，不代表已发布。')}}</p></details>
      <p v-if="loading&&!note" role="status">{{t('正在加载升级日志…')}}</p>
      <p v-if="pending" class="release-notice">{{t('升级日志正在后台处理，可关闭抽屉，稍后从迭代入口查看。')}}</p>
      <p v-if="note&&!note.sourceCount&&!entries.length">{{t('当前没有可汇总的完成需求；缺陷不会进入升级日志。')}}</p>
      <p v-if="note&&(!note.configured||!note.enabled)">{{t('企业尚未配置或启用 AI，可查看已有日志；如需生成请联系企业管理员。')}}</p>
      <aside v-if="entries.length" class="release-readiness" :class="{ready:releaseReady}" aria-live="polite"><div><b>{{t('发布检查')}}</b><span>{{t('已覆盖 {done}/{total} 个完成需求',{done:entries.length,total:note?.sourceCount||entries.length})}}</span></div><strong v-if="releaseReady">{{t('文案与配图已齐，可下载发布图文包')}}</strong><strong v-else>{{t('仍有 {count} 项待补图，当前仅可作为审核草稿',{count:missingImageCount})}}</strong></aside>
      <div v-if="entries.length" class="release-groups">
        <section v-for="group in groups" :key="group.category" class="release-group"><h3>{{t(group.category)}}</h3><p v-if="!group.entries.length" class="release-empty">{{t('本分类暂无升级条目')}}</p>
          <article v-for="{entry,index} in group.entries" :key="index" class="release-entry">
            <template v-if="editing"><label>{{t('分类')}}<AppSelect v-model="entry.category" :options="categoryOptions" :label="t('升级日志分类')" :disabled="!canEdit" /></label><label>{{t('功能标题')}}<input v-model="entry.title" maxlength="120" :disabled="!canEdit"></label><label>{{t('功能说明')}}<textarea v-model="entry.description" maxlength="2000" rows="4" :disabled="!canEdit"></textarea><small>{{t('标题最多 120 字，说明最多 2000 字；填写不含链接的单段纯文本。')}}</small></label></template>
            <template v-else><h4>{{entry.title}}</h4><p class="release-description">{{entry.description}}</p></template>
            <div class="release-sources"><span>{{t('来源需求')}}</span><span v-for="id in entry.requirementIds" :key="id">{{note?.sources.find(source=>source.id===id)?.code||'#'+id}} · {{note?.sources.find(source=>source.id===id)?.title}}</span></div>
            <div v-if="editing&&availableImages(entry).length" class="release-image-picker"><div class="release-image-picker-head"><b>{{t('选择真实配图')}}</b><span>{{t('已选 {count}/8 张',{count:entry.imageIds.length})}}</span></div><article v-for="image in availableImages(entry)" :key="image.id" :class="{selected:entry.imageIds.includes(image.id)}"><label><input type="checkbox" :checked="entry.imageIds.includes(image.id)" :disabled="!canEdit" @change="toggleImage(entry,image,($event.target as HTMLInputElement).checked)"><span>{{image.name}}</span></label><Button variant="ghost" size="sm" :disabled="imageLoading[image.id]||scope.locked.value" @click="toggleImagePreview(image)">{{t(imageLoading[image.id]?'加载中…':imageURLs[image.id]?'收起预览':'预览')}}</Button><img v-if="imageURLs[image.id]" :src="imageURLs[image.id]" :alt="caption(entry,image)"><p v-if="imageErrors[image.id]" class="release-error" role="alert">{{t(imageErrors[image.id])}}</p><label v-if="entry.imageIds.includes(image.id)" class="release-caption">{{t('图注')}}<input :value="caption(entry,image)" maxlength="500" :disabled="!canEdit" @input="setCaption(entry,image,($event.target as HTMLInputElement).value)"></label></article></div>
            <p v-if="!entry.imageIds.length" class="release-missing-image">{{t(availableImages(entry).length?'待补图：请从当前需求的真实截图中选择配图。':'待补图：当前需求没有可用截图，不生成虚构配图。')}}</p>
            <template v-if="!editing"><figure v-for="image in imagesFor(entry)" :key="image.id" class="release-image"><img v-if="imageURLs[image.id]" :src="imageURLs[image.id]" :alt="caption(entry,image)"><Button v-else variant="outline" :disabled="imageLoading[image.id]||scope.locked.value" @click="loadImage(image)">{{t(imageLoading[image.id]?'正在加载截图…':'查看真实截图')}}</Button><p v-if="imageErrors[image.id]" class="release-error" role="alert">{{t(imageErrors[image.id])}}</p><figcaption>{{caption(entry,image)}}</figcaption></figure></template>
          </article>
        </section>
      </div>
    </section>
    <template #footer><span v-if="dirty" class="release-unsaved">{{t('未保存修改')}}</span><Button v-if="editing" variant="outline" :disabled="writing" @click="discard">{{t('放弃修改')}}</Button><Button v-if="editing" :disabled="!canEdit||!dirty||conflict" @click="save">{{t(writing?'保存中…':'保存日志')}}</Button><Button variant="outline" :disabled="!note?.entries.length||note?.sourceChanged||dirty||writing||scope.locked.value" @click="exportNotes('md')">{{t('导出 Markdown')}}</Button><Button variant="outline" :disabled="!note?.entries.length||note?.sourceChanged||dirty||writing||scope.locked.value" @click="exportNotes('json')">{{t('导出 JSON')}}</Button><Button :variant="releaseReady?'default':'outline'" :disabled="!note?.entries.length||note?.state!=='draft'||note?.sourceChanged||dirty||writing||loading||exporting||conflict||scope.locked.value" @click="downloadBundle">{{t(exporting?'正在打包…':releaseReady?'下载发布图文包':'下载审核图文包')}}</Button><Button variant="outline" :disabled="writing" @click="close">{{t('关闭')}}</Button></template>
  </OrganizationModal>
</template>
<style scoped>
.release-notes-panel{min-width:0;color:var(--foreground)}.release-heading{display:flex;align-items:flex-start;justify-content:space-between;gap:12px;margin-bottom:14px}.release-heading h3{margin:0;overflow-wrap:anywhere}.release-heading p{font-size:var(--ui-font-caption);color:var(--muted-foreground);margin:6px 0}.release-state{flex:none;padding:4px 8px;border:1px solid var(--border);border-radius:6px;font-size:var(--ui-font-caption)}.release-state.queued,.release-state.generating{color:var(--primary);background:var(--accent)}.release-state.failed{color:var(--destructive)}.release-state.draft{color:var(--success)}.release-tools{display:flex;gap:8px;flex-wrap:wrap;margin:12px 0}.release-boundary{padding:10px 12px;border:1px solid var(--border);border-radius:6px;font-size:var(--ui-font-caption);color:var(--muted-foreground)}.release-boundary summary{cursor:pointer}.release-boundary p{margin:8px 0;line-height:1.6;overflow-wrap:anywhere}.release-error{color:var(--destructive);line-height:1.6;overflow-wrap:anywhere}.release-notice{color:var(--muted-foreground);line-height:1.6}.release-readiness{display:flex;align-items:center;justify-content:space-between;gap:12px;margin:16px 0 0;padding:11px 12px;border:1px solid color-mix(in srgb,var(--warning,#d97706) 35%,var(--border));border-radius:8px;background:color-mix(in srgb,var(--warning,#d97706) 6%,var(--background));font-size:var(--ui-font-caption)}.release-readiness>div{display:flex;align-items:center;gap:8px;min-width:0;flex-wrap:wrap}.release-readiness span{color:var(--muted-foreground)}.release-readiness>strong{color:var(--warning,#b45309);text-align:right}.release-readiness.ready{border-color:color-mix(in srgb,var(--success) 35%,var(--border));background:color-mix(in srgb,var(--success) 6%,var(--background))}.release-readiness.ready>strong{color:var(--success)}.release-groups{display:grid;gap:24px;margin-top:24px}.release-group>h3{margin:0 0 10px;padding-bottom:8px;border-bottom:1px solid var(--border)}.release-empty{color:var(--muted-foreground);font-size:var(--ui-font-caption)}.release-entry{padding:14px 0;border-bottom:1px solid var(--border);min-width:0}.release-entry h4{margin:0 0 8px;overflow-wrap:anywhere}.release-description{white-space:pre-wrap;overflow-wrap:anywhere;line-height:1.8}.release-entry>label{display:grid;gap:6px;margin:10px 0;font-size:var(--ui-font-caption)}.release-entry input,.release-entry textarea{width:100%;min-width:0;border:1px solid var(--input);border-radius:6px;padding:8px;background:var(--background);color:var(--foreground)}.release-entry textarea{resize:vertical;max-height:55dvh}.release-entry :deep(.app-select-trigger){width:100%}.release-sources{display:flex;gap:6px;flex-wrap:wrap;font-size:var(--ui-font-caption);color:var(--muted-foreground)}.release-sources>span{overflow-wrap:anywhere}.release-sources>span+span{border:1px solid var(--border);padding:2px 6px;border-radius:4px}.release-missing-image{padding:12px;border:1px dashed var(--border);color:var(--muted-foreground);font-size:var(--ui-font-caption)}.release-image-picker{display:grid;gap:8px;margin:14px 0}.release-image-picker-head{display:flex;align-items:center;justify-content:space-between;gap:8px;font-size:var(--ui-font-caption)}.release-image-picker-head span{color:var(--muted-foreground)}.release-image-picker>article{display:grid;grid-template-columns:minmax(0,1fr) auto;gap:8px;padding:9px 10px;border:1px solid var(--border);border-radius:7px;background:var(--background)}.release-image-picker>article.selected{border-color:color-mix(in srgb,var(--primary) 42%,var(--border));background:color-mix(in srgb,var(--primary) 4%,var(--background))}.release-image-picker>article>label:first-child{display:flex;align-items:center;gap:8px;min-width:0;margin:0;font-size:var(--ui-font-caption)}.release-image-picker input[type=checkbox]{width:15px;height:15px;flex:none;padding:0;accent-color:var(--primary)}.release-image-picker>article>label span{overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.release-image-picker>article>img{grid-column:1/-1;display:block;max-width:100%;max-height:42dvh;object-fit:contain;border:1px solid var(--border);border-radius:4px}.release-image-picker .release-error,.release-image-picker .release-caption{grid-column:1/-1}.release-image-picker .release-caption{display:grid;gap:6px;margin:0;font-size:var(--ui-font-caption)}.release-image{margin:14px 0;min-width:0}.release-image img{display:block;max-width:100%;height:auto;max-height:60dvh;object-fit:contain;border:1px solid var(--border);border-radius:4px}.release-image figcaption{margin:6px 0;color:var(--muted-foreground);font-size:var(--ui-font-caption);overflow-wrap:anywhere}.release-unsaved{margin-right:auto;color:var(--muted-foreground);font-size:var(--ui-font-caption)}@media(max-width:820px){.release-heading,.release-readiness{flex-wrap:wrap}.release-readiness>strong{text-align:left}.release-tools :deep(button){flex:1}.release-entry textarea{font-size:16px}.release-image img{max-height:50dvh}.release-image-picker>article{grid-template-columns:minmax(0,1fr)}.release-image-picker>article>button{justify-self:start}}
</style>

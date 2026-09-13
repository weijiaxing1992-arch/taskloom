<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { api, apiDownload } from '../api'
import { formatDate, t } from '../i18n'
import { useWorkspaceStore } from '../stores/workspace'
import { downloadFile } from '../workItemExport'
import { releaseImageMime, releaseImagePath, releaseNoteFilename, type ReleaseNoteEntry } from '../releaseNotes'
import { parseReleaseNotesCenterDetail, parseReleaseNotesCenterList, releaseNotesCenterGroups, releaseNotesCenterImages, releaseNotesCenterRequirement, type ReleaseNotesCenterDetail, type ReleaseNotesCenterImage, type ReleaseNotesCenterList, type ReleaseNotesCenterSummary } from '../releaseNotesCenter'
import { Button } from '../components/ui/button'
import Icon from '../components/Icon.vue'

const workspace=useWorkspaceStore()
const list=ref<ReleaseNotesCenterList|null>(null),detail=ref<ReleaseNotesCenterDetail|null>(null),selected=ref<number|null>(null),loading=ref(false),detailLoading=ref(false),exporting=ref(false),error=ref(''),searchInput=ref(''),searchQuery=ref(''),imageURLs=ref<Record<number,string>>({}),imageLoading=ref<Record<number,boolean>>({}),imageErrors=ref<Record<number,string>>({})
let disposed=false,generation=0,controller:AbortController|undefined,searchTimer:number|undefined
const projectID=computed(()=>workspace.currentProject?.id||'')
const groups=computed(()=>detail.value?releaseNotesCenterGroups(detail.value):[])
const projectHeaders=()=>({'X-TaskLoom-Project':projectID.value})
const current=(stamp:number,project:string,user:string)=>!disposed&&stamp===generation&&!workspace.identityConflict&&projectID.value===project&&workspace.currentUser?.id===user
function clearImages(){for(const url of Object.values(imageURLs.value))URL.revokeObjectURL(url);imageURLs.value={};imageLoading.value={};imageErrors.value={}}
function invalidate(){generation++;controller?.abort();controller=undefined;clearImages();list.value=null;detail.value=null;selected.value=null;loading.value=false;detailLoading.value=false;exporting.value=false;error.value=''}
function validContext(){return !!projectID.value&&!!workspace.currentUser&&!workspace.identityConflict&&!workspace.operationDisabled&&!workspace.mustChangePassword}

async function load(){
  if(!validContext())return
  const stamp=++generation,project=projectID.value,user=workspace.currentUser!.id
  controller?.abort();controller=new AbortController();loading.value=true;error.value=''
  try{
    const query=searchQuery.value.trim()
    const endpoint='/reports/release-notes?page=1&pageSize=100'+(query?'&q='+encodeURIComponent(query):'')
    const data=parseReleaseNotesCenterList(await api<unknown>(endpoint,{headers:projectHeaders(),signal:controller.signal}))
    if(!current(stamp,project,user))return
    if(data.projectId!==project)throw Error('升级日志项目上下文已变化，请刷新重试')
    list.value=data
    const target=data.items.find(item=>item.sprintId===selected.value)||data.items[0]
    if(target)await open(target,stamp,project,user)
    else {selected.value=null;detail.value=null;clearImages()}
  }catch(cause){if(current(stamp,project,user)){list.value=null;detail.value=null;selected.value=null;error.value=cause instanceof Error?cause.message:t('升级日志暂时无法加载，请重试')}}
  finally{if(current(stamp,project,user))loading.value=false}
}
function scheduleSearch(){
  if(searchTimer!==undefined)window.clearTimeout(searchTimer)
  searchTimer=window.setTimeout(()=>{
    searchTimer=undefined
    const next=searchInput.value.trim()
    if(next===searchQuery.value)return
    searchQuery.value=next
    retry()
  },260)
}
function clearSearch(){
  if(searchTimer!==undefined){window.clearTimeout(searchTimer);searchTimer=undefined}
  if(!searchInput.value&&!searchQuery.value)return
  searchInput.value='';searchQuery.value='';retry()
}

async function open(item:ReleaseNotesCenterSummary, inheritedStamp?:number, inheritedProject?:string, inheritedUser?:string){
  if(!validContext())return
  const stamp=inheritedStamp??++generation,project=inheritedProject??projectID.value,user=inheritedUser??workspace.currentUser!.id
  if(!inheritedStamp){controller?.abort();controller=new AbortController()}
  selected.value=item.sprintId;detail.value=null;clearImages();detailLoading.value=true;error.value=''
  try{
    const data=parseReleaseNotesCenterDetail(await api<unknown>('/reports/release-notes/'+item.sprintId,{headers:projectHeaders(),signal:controller?.signal}))
    if(!current(stamp,project,user))return
    if(data.projectId!==project||data.sprintId!==item.sprintId)throw Error('升级日志项目上下文已变化，请刷新重试')
    detail.value=data
  }catch(cause){if(current(stamp,project,user))error.value=cause instanceof Error?cause.message:t('升级日志暂时无法加载，请重试')}
  finally{if(current(stamp,project,user))detailLoading.value=false}
}

function caption(entry:ReleaseNoteEntry,image:ReleaseNotesCenterImage){return entry.imageCaptions?.[String(image.id)]||image.name}
function images(entry:ReleaseNoteEntry){return detail.value?releaseNotesCenterImages(detail.value,entry):[]}
function requirement(entry:ReleaseNoteEntry){return detail.value?releaseNotesCenterRequirement(detail.value,entry.requirementIds[0]||0):undefined}
async function toggleImage(image:ReleaseNotesCenterImage){
  if(!detail.value||imageLoading.value[image.id])return
  if(imageURLs.value[image.id]){URL.revokeObjectURL(imageURLs.value[image.id]!);const next={...imageURLs.value};delete next[image.id];imageURLs.value=next;return}
  const stamp=generation,project=projectID.value,user=workspace.currentUser?.id||'',sprint=detail.value.sprintId
  imageLoading.value={...imageLoading.value,[image.id]:true};imageErrors.value={...imageErrors.value,[image.id]:''}
  try{
    const blob=await apiDownload(releaseImagePath(image),{headers:projectHeaders(),signal:controller?.signal})
    if(blob.size>20*1024*1024)throw Error('截图超过 20 MB，请查看原需求附件')
    const type=releaseImageMime(new Uint8Array(await blob.slice(0,12).arrayBuffer()))
    if(!current(stamp,project,user)||detail.value?.sprintId!==sprint)return
    imageURLs.value={...imageURLs.value,[image.id]:URL.createObjectURL(new Blob([blob],{type}))}
  }catch(cause){if(current(stamp,project,user)&&detail.value?.sprintId===sprint)imageErrors.value={...imageErrors.value,[image.id]:cause instanceof Error?cause.message:t('截图加载失败，请重试')}}
  finally{if(current(stamp,project,user)&&detail.value?.sprintId===sprint)imageLoading.value={...imageLoading.value,[image.id]:false}}
}
function downloadSnapshot(format:'md'|'json'){
  if(!detail.value)return
  const body=format==='md'?detail.value.markdown:JSON.stringify(detail.value,null,2)+'\n'
  downloadFile(new Blob([body],{type:format==='md'?'text/markdown;charset=utf-8':'application/json;charset=utf-8'}),releaseNoteFilename(detail.value.versionName,format))
}
async function downloadBundle(){
  if(!detail.value||detail.value.state!=='draft'||exporting.value)return
  const stamp=generation,project=projectID.value,user=workspace.currentUser?.id||'',sprint=detail.value.sprintId,revision=detail.value.revision
  exporting.value=true;error.value=''
  try{
    const blob=await apiDownload('/sprints/'+sprint+'/release-notes/bundle?expectedRevision='+revision,{headers:projectHeaders(),signal:controller?.signal})
    if(current(stamp,project,user)&&detail.value?.sprintId===sprint&&detail.value.revision===revision)downloadFile(blob,releaseNoteFilename(detail.value.versionName,'zip'))
  }catch(cause){if(current(stamp,project,user))error.value=cause instanceof Error?cause.message:t('图文包下载失败，请刷新后重试')}
  finally{if(current(stamp,project,user))exporting.value=false}
}
function retry(){invalidate();void load()}
function identityChanged(){invalidate()}

watch(()=>[projectID.value,workspace.currentUser?.id,workspace.identityConflict,workspace.operationDisabled,workspace.mustChangePassword],()=>{invalidate();void load()})
onMounted(()=>{void load();window.addEventListener('devflow-project-changed',retry);window.addEventListener('devflow-identity-changed',identityChanged);window.addEventListener('devflow-auth-expired',identityChanged)})
onBeforeUnmount(()=>{if(searchTimer!==undefined)window.clearTimeout(searchTimer);disposed=true;invalidate();window.removeEventListener('devflow-project-changed',retry);window.removeEventListener('devflow-identity-changed',identityChanged);window.removeEventListener('devflow-auth-expired',identityChanged)})
</script>

<template>
  <section class="release-center module-page">
    <header class="release-center-heading">
      <div class="release-center-title"><span class="release-center-eyebrow">{{t('团队洞察')}}</span><h1>{{t('升级日志中心')}}</h1><p>{{t('按已完成迭代保存版本快照：正文、验收标准、真实截图与配图说明均可追溯；缺陷不会纳入。')}}</p></div>
      <div class="release-center-controls"><label class="release-center-search"><Icon name="search" :size="15"/><input v-model="searchInput" type="search" enterkeyhint="search" maxlength="100" :aria-label="t('搜索升级日志')" :placeholder="t('搜索版本、需求或内容')" @input="scheduleSearch" @search="scheduleSearch" @keydown.esc.prevent="clearSearch"></label><Button variant="outline" :disabled="loading||detailLoading||workspace.identityConflict" @click="retry"><Icon name="refresh" :size="15"/>{{t('刷新')}}</Button></div>
    </header>
    <p v-if="error" class="release-center-error" role="alert">{{t(error)}} <button type="button" class="link" @click="retry">{{t('重试')}}</button></p>
    <div v-if="loading&&!list" class="release-center-empty" role="status"><span class="spinner"></span>{{t('正在读取升级日志…')}}</div>
    <div v-else-if="!list?.items.length" class="release-center-empty"><Icon name="review" :size="28"/><h2>{{t(searchQuery?'未找到匹配的升级日志':'暂无已保存的升级日志')}}</h2><p>{{t(searchQuery?'请调整关键词，或清空搜索后查看当前项目的全部版本。':'完成迭代并生成日志草稿后，会在这里保留项目范围内的可追溯版本快照。')}}</p><Button v-if="searchQuery" variant="outline" size="sm" @click="clearSearch">{{t('清空搜索')}}</Button></div>
    <div v-else class="release-center-layout">
      <aside class="release-center-list" :aria-label="t('升级日志列表')"><p>{{t('当前项目')}} · {{t('{count} 个版本',{count:list?.total||0})}}</p><button v-for="item in list?.items" :key="item.sprintId" type="button" :class="{active:selected===item.sprintId}" @click="open(item)"><span><b>{{item.versionName}}</b><small>{{item.sprintCode}} · {{formatDate(item.releaseDate)}}</small></span><em>{{item.requirementCount}} {{t('项')}} · {{item.selectedImageCount}} {{t('张配图')}}</em></button></aside>
      <main class="release-center-detail" :aria-busy="detailLoading">
        <div v-if="detailLoading" class="release-center-empty" role="status"><span class="spinner"></span>{{t('正在读取版本详情…')}}</div>
        <template v-else-if="detail">
          <header class="release-detail-heading"><div><span>{{detail.sprintCode}}</span><h2>{{detail.versionName}}</h2><p>{{t('版本时间')}}：{{formatDate(detail.releaseDate)}} · {{t('快照修订')}} #{{detail.revision}}</p></div><div class="release-detail-actions"><Button variant="outline" size="sm" @click="downloadSnapshot('md')">{{t('导出 Markdown')}}</Button><Button variant="outline" size="sm" @click="downloadSnapshot('json')">{{t('导出 JSON')}}</Button><Button size="sm" :disabled="detail.state!=='draft'||exporting" @click="downloadBundle">{{t(exporting?'正在打包…':'下载图文包')}}</Button></div></header>
          <p v-if="detail.state!=='draft'" class="release-center-notice">{{t('该记录保留为历史快照；图文包需要当前草稿状态及原需求截图指纹均未变化。')}}</p>
          <section v-for="group in groups" :key="group.category" class="release-center-group"><header><h3>{{t(group.category)}}</h3><span>{{group.entries.length}} {{t('项')}}</span></header><p v-if="!group.entries.length" class="release-center-muted">{{t('本分类暂无升级条目')}}</p>
            <article v-for="entry in group.entries" :key="entry.requirementIds[0]" class="release-center-entry"><div class="release-entry-title"><div><h4>{{entry.title}}</h4><p>{{entry.description}}</p></div><router-link v-if="requirement(entry)" :to="{path:'/requirements',query:{req:String(requirement(entry)?.id)}}">{{requirement(entry)?.code}} ↗</router-link></div>
              <details open class="release-entry-source"><summary>{{t('需求正文与验收')}}</summary><div><b>{{t('正文')}}</b><p>{{requirement(entry)?.description||t('暂无正文')}}</p></div><div><b>{{t('验收标准')}}</b><p>{{requirement(entry)?.acceptance||t('暂无验收标准')}}</p></div></details>
              <p v-if="!images(entry).length" class="release-center-muted">{{t('待补图：当前版本未选择真实截图。')}}</p>
              <figure v-for="image in images(entry)" :key="image.id" class="release-center-image"><Button v-if="!imageURLs[image.id]" variant="outline" size="sm" :disabled="imageLoading[image.id]" @click="toggleImage(image)">{{t(imageLoading[image.id]?'正在加载截图…':'查看真实截图')}}</Button><img v-else :src="imageURLs[image.id]" :alt="caption(entry,image)" @click="toggleImage(image)"><p v-if="imageErrors[image.id]" class="release-center-error" role="alert">{{t(imageErrors[image.id])}}</p><figcaption>{{caption(entry,image)}} · {{image.name}}</figcaption></figure>
            </article>
          </section>
        </template>
        <div v-else class="release-center-empty"><Icon name="list" :size="28"/><h2>{{t('选择一个升级版本')}}</h2><p>{{t('查看固定分类、需求正文、验收标准和已选择的真实截图。')}}</p></div>
      </main>
    </div>
  </section>
</template>

<style scoped>
/* 升级日志是团队的“发布画布”：保留信息密度，同时以轻玻璃层次区分浏览、内容与操作。 */
.release-center{position:relative;isolation:isolate;max-width:1540px;margin:0 auto;padding-bottom:42px}
.release-center-heading{display:grid;grid-template-columns:minmax(0,1fr) auto;position:relative;align-items:center;gap:20px;isolation:isolate;overflow:hidden;margin:0 0 18px;padding:24px 26px;border:1px solid color-mix(in srgb,var(--primary) 15%,var(--border));border-radius:18px;background:linear-gradient(125deg,color-mix(in srgb,var(--card) 88%,#dce9ff),color-mix(in srgb,var(--card) 94%,#eee9ff));box-shadow:0 14px 38px color-mix(in srgb,#2455a3 11%,transparent)}
.release-center-heading::before,.release-center-heading::after{position:absolute;z-index:-1;width:260px;height:260px;border-radius:999px;content:"";filter:blur(3px);opacity:.56;pointer-events:none}
.release-center-heading::before{top:-170px;right:12%;background:radial-gradient(circle,#86b7ff 0,transparent 68%);animation:release-drift 13s ease-in-out infinite alternate}
.release-center-heading::after{right:-108px;bottom:-184px;background:radial-gradient(circle,#b7a1ff 0,transparent 69%);animation:release-drift 16s ease-in-out -5s infinite alternate-reverse}
.release-center-heading>*{position:relative;z-index:1}.release-center-title{min-width:0}.release-center-eyebrow{display:inline-flex;align-items:center;min-height:22px;padding:0 8px;border:1px solid color-mix(in srgb,var(--primary) 18%,transparent);border-radius:999px;background:color-mix(in srgb,var(--primary) 10%,var(--card));color:var(--primary);font-size:11px;font-weight:750;letter-spacing:.04em;line-height:20px}.release-center-heading h1{margin:8px 0 6px;color:var(--foreground);font-size:26px;font-weight:720;letter-spacing:-.035em;line-height:1.2;overflow-wrap:anywhere}.release-center-heading p{max-width:780px;margin:0;color:var(--muted-foreground);font-size:13px;line-height:1.7;overflow-wrap:anywhere}.release-center-controls{display:flex;align-items:center;justify-content:flex-end;gap:8px;min-width:min(100%,400px)}.release-center-search{display:flex;align-items:center;gap:7px;width:clamp(190px,23vw,310px);min-height:36px;padding:0 10px;border:1px solid color-mix(in srgb,var(--primary) 18%,var(--border));border-radius:10px;background:color-mix(in srgb,var(--card) 82%,transparent);box-shadow:inset 0 1px 0 color-mix(in srgb,#fff 70%,transparent);color:var(--muted-foreground);backdrop-filter:blur(12px);transition:border-color 160ms ease,box-shadow 160ms ease,background-color 160ms ease}.release-center-search:focus-within{border-color:color-mix(in srgb,var(--primary) 58%,var(--border));background:color-mix(in srgb,var(--card) 96%,transparent);box-shadow:0 0 0 3px color-mix(in srgb,var(--primary) 13%,transparent)}.release-center-search input{width:100%;min-width:0;border:0;outline:0;background:transparent;color:var(--foreground);font:inherit;font-size:12px}.release-center-search input::placeholder{color:var(--muted-foreground);opacity:.82}.release-center-controls>.btn{align-self:center;min-height:36px;border-color:color-mix(in srgb,var(--primary) 20%,var(--border));background:color-mix(in srgb,var(--card) 80%,transparent);box-shadow:0 5px 14px color-mix(in srgb,var(--primary) 8%,transparent);backdrop-filter:blur(12px)}
.release-center-error{margin:0 0 14px;padding:10px 12px;border:1px solid var(--destructive);border-radius:10px;background:color-mix(in srgb,var(--destructive) 7%,var(--background));color:var(--destructive);font-size:12px;line-height:1.6}.release-center-empty{display:flex;min-height:260px;flex-direction:column;align-items:center;justify-content:center;gap:10px;padding:24px;text-align:center;color:var(--muted-foreground);font-size:13px}.release-center>.release-center-empty{min-height:clamp(220px,30vh,300px);border:1px solid color-mix(in srgb,var(--primary) 12%,var(--border));border-radius:18px;background:linear-gradient(145deg,color-mix(in srgb,var(--card) 92%,#eaf2ff),var(--card));box-shadow:0 14px 34px color-mix(in srgb,#174a96 7%,transparent)}.release-center-empty :deep(svg){color:var(--primary);filter:drop-shadow(0 5px 9px color-mix(in srgb,var(--primary) 20%,transparent))}.release-center-empty h2{margin:0;color:var(--foreground);font-size:17px;letter-spacing:-.02em}.release-center-empty p{max-width:520px;margin:0;line-height:1.75}
.release-center-layout{display:grid;grid-template-columns:minmax(245px,300px) minmax(0,1fr);min-height:620px;border:1px solid color-mix(in srgb,var(--primary) 11%,var(--border));border-radius:18px;background:color-mix(in srgb,var(--card) 88%,transparent);box-shadow:0 16px 42px color-mix(in srgb,#174a96 9%,transparent);overflow:hidden;backdrop-filter:blur(18px)}.release-center-list{padding:12px;border-right:1px solid color-mix(in srgb,var(--primary) 10%,var(--border));background:linear-gradient(180deg,color-mix(in srgb,var(--secondary) 60%,var(--card)),color-mix(in srgb,var(--card) 90%,transparent))}.release-center-list>p{margin:4px 6px 12px;color:var(--muted-foreground);font-size:11px}.release-center-list button{display:grid;width:100%;gap:9px;padding:13px;border:1px solid transparent;border-radius:12px;background:transparent;color:var(--foreground);text-align:left;cursor:pointer;transition:transform 180ms ease,background-color 180ms ease,border-color 180ms ease,box-shadow 180ms ease}.release-center-list button:hover{border-color:color-mix(in srgb,var(--primary) 14%,transparent);background:color-mix(in srgb,var(--primary) 6%,var(--card));transform:translateX(2px)}.release-center-list button.active{border-color:color-mix(in srgb,var(--primary) 28%,var(--border));background:linear-gradient(135deg,color-mix(in srgb,var(--primary) 13%,var(--card)),color-mix(in srgb,var(--primary) 4%,var(--card)));box-shadow:0 7px 18px color-mix(in srgb,var(--primary) 10%,transparent)}.release-center-list button>span{display:grid;gap:5px;min-width:0}.release-center-list b{overflow:hidden;text-overflow:ellipsis;white-space:nowrap;font-size:13px}.release-center-list small,.release-center-list em{overflow:hidden;color:var(--muted-foreground);font-size:11px;font-style:normal;text-overflow:ellipsis;white-space:nowrap}.release-center-list em{color:var(--primary);font-variant-numeric:tabular-nums}.release-center-detail{min-width:0;padding:26px;background:color-mix(in srgb,var(--card) 82%,transparent)}.release-detail-heading{display:flex;align-items:flex-start;justify-content:space-between;gap:18px;padding:4px 0 20px;border-bottom:1px solid color-mix(in srgb,var(--primary) 10%,var(--border))}.release-detail-heading span{font-size:11px;color:var(--primary);font-weight:750}.release-detail-heading h2{margin:5px 0;font-size:23px;letter-spacing:-.025em;overflow-wrap:anywhere}.release-detail-heading p{margin:0;color:var(--muted-foreground);font-size:12px}.release-detail-actions{display:flex;flex-wrap:wrap;justify-content:flex-end;gap:8px}.release-center-notice{margin:16px 0;padding:10px 12px;border:1px solid color-mix(in srgb,var(--warning) 35%,var(--border));border-radius:10px;background:color-mix(in srgb,var(--warning) 7%,var(--background));color:var(--warning);font-size:12px;line-height:1.6}.release-center-group{padding-top:26px}.release-center-group>header{display:flex;align-items:center;justify-content:space-between;gap:12px;margin-bottom:10px;border-bottom:1px solid color-mix(in srgb,var(--primary) 9%,var(--border));padding-bottom:10px}.release-center-group h3{margin:0;font-size:16px;letter-spacing:-.015em}.release-center-group header span{min-width:26px;padding:3px 7px;border-radius:999px;background:color-mix(in srgb,var(--primary) 8%,var(--card));color:var(--primary);font-size:11px;font-variant-numeric:tabular-nums}.release-center-entry{padding:17px 0;border-bottom:1px solid color-mix(in srgb,var(--primary) 8%,var(--border))}.release-entry-title{display:flex;align-items:flex-start;justify-content:space-between;gap:14px}.release-entry-title>div{min-width:0}.release-entry-title h4{margin:0 0 7px;font-size:15px;overflow-wrap:anywhere}.release-entry-title p{margin:0;color:var(--muted-foreground);font-size:13px;line-height:1.75;white-space:pre-wrap;overflow-wrap:anywhere}.release-entry-title a{flex:none;padding:4px 7px;border-radius:7px;background:color-mix(in srgb,var(--primary) 7%,transparent);color:var(--primary);font-size:12px;font-weight:700;text-decoration:none;transition:background-color 150ms ease,transform 150ms ease}.release-entry-title a:hover{background:color-mix(in srgb,var(--primary) 14%,transparent);transform:translateY(-1px)}.release-entry-source{display:grid;gap:10px;margin-top:14px;padding:12px 13px;border:1px solid color-mix(in srgb,var(--primary) 10%,var(--border));border-radius:12px;background:linear-gradient(145deg,color-mix(in srgb,var(--secondary) 55%,var(--card)),color-mix(in srgb,var(--card) 92%,transparent))}.release-entry-source summary{cursor:pointer;color:var(--foreground);font-size:12px;font-weight:700}.release-entry-source>div{display:grid;gap:4px}.release-entry-source b{color:var(--muted-foreground);font-size:11px}.release-entry-source p{margin:0;color:var(--foreground);font-size:12px;line-height:1.7;white-space:pre-wrap;overflow-wrap:anywhere}.release-center-muted{margin:11px 0;color:var(--muted-foreground);font-size:12px;line-height:1.65}.release-center-image{margin:14px 0 0}.release-center-image img{display:block;max-width:100%;max-height:560px;border:1px solid color-mix(in srgb,var(--primary) 12%,var(--border));border-radius:12px;box-shadow:0 10px 25px color-mix(in srgb,#173b73 13%,transparent);cursor:zoom-out;object-fit:contain}.release-center-image figcaption{margin-top:7px;color:var(--muted-foreground);font-size:11px;line-height:1.5;overflow-wrap:anywhere}
@keyframes release-drift{from{transform:translate3d(-8px,-5px,0) scale(.96)}to{transform:translate3d(14px,12px,0) scale(1.05)}}
@media(max-width:980px){.release-center-layout{grid-template-columns:1fr}.release-center-list{display:grid;grid-template-columns:repeat(auto-fit,minmax(190px,1fr));gap:7px;border-right:0;border-bottom:1px solid var(--border)}.release-center-list>p{grid-column:1/-1}.release-center-list button{min-height:84px}.release-detail-heading{flex-wrap:wrap}.release-detail-actions{justify-content:flex-start}}@media(max-width:620px){.release-center{padding-inline:0}.release-center-heading{grid-template-columns:1fr;gap:14px;margin-bottom:14px;padding:20px 18px;border-radius:14px}.release-center-controls{width:100%;flex-direction:column;align-items:stretch}.release-center-search{width:auto}.release-center-controls>.btn{width:100%;justify-content:center}.release-center-heading h1{font-size:22px}.release-center-heading p{font-size:12px;line-height:1.65}.release-center>.release-center-empty{min-height:220px;padding:20px 16px;border-radius:14px}.release-center-layout{border-radius:14px}.release-center-detail{padding:18px}.release-center-list{grid-template-columns:1fr}.release-detail-actions{display:grid;width:100%;grid-template-columns:1fr 1fr}.release-detail-actions :deep(button:last-child){grid-column:1/-1}.release-entry-title{flex-direction:column;gap:8px}}@media(prefers-reduced-motion:reduce){.release-center-heading::before,.release-center-heading::after{animation:none}.release-center-list button,.release-entry-title a,.release-center-search{transition:none}.release-center-list button:hover,.release-entry-title a:hover{transform:none}}
</style>

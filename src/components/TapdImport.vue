<script setup lang="ts">
import { computed, onBeforeUnmount, ref } from 'vue'
import { t } from '../i18n'
import { buildTapdRequirement, suggestTapdMapping, tapdFileError, type TapdMapping, type TapdTarget } from '../tapdImport'
import { plainTextToDocument } from '../richText'
import { normalizeSprints, sprintSelectable } from '../requirementFields'
import { useSettingsScope } from './settingsScope'
import OrganizationModal from './OrganizationModal.vue'
import AppSelect from './AppSelect.vue'
import MemberMultiSelect from './MemberMultiSelect.vue'
import Icon from './Icon.vue'

const emit=defineEmits<{imported:[id:number]}>()
const scope=useSettingsScope()
const opened=ref(false),loading=ref(false),saving=ref(false),error=ref(''),notice=ref(''),progress=ref(''),reviewed=ref(false)
const parsed=ref<Awaited<ReturnType<typeof import('../tapdPdf').readTapdPdf>>|null>(null)
const members=ref<any[]>([]),targets=ref<TapdTarget[]>([]),rows=ref<TapdMapping[]>([]),title=ref(''),description=ref(''),page=ref(0),fileInput=ref<HTMLInputElement|null>(null)
let controller:AbortController|null=null,version=0
const dragDepth=ref(0),tab=ref<'fields'|'body'>('fields'),showArchived=ref(false),onlyIssues=ref(false)
const fileDisabled=computed(()=>loading.value||saving.value||!targets.value.length||scope.locked.value)
const shownRows=computed(()=>rows.value.map((row,index)=>({row,index})).filter(({row})=>onlyIssues.value?row.target&&row.issue:showArchived.value||row.target||row.field.label==='手动补充'))
const mapped=computed(()=>rows.value.filter(row=>row.target).length)
const unresolved=computed(()=>rows.value.filter(row=>row.target&&row.issue).length)
const targetOptions=computed(()=>[{value:'',label:t('仅保留来源字段')},...targets.value.map(item=>({value:item.key,label:t(item.label)}))])
const targetOf=(row:TapdMapping)=>targets.value.find(item=>item.key===row.target)
const choiceOptions=(row:TapdMapping)=>[{value:'',label:t('请选择目标值')},...(targetOf(row)?.options||[])]
function stop(){version++;controller?.abort();controller=null;loading.value=false;dragDepth.value=0}
function close(){if(saving.value)return;stop();opened.value=false;parsed.value=null;rows.value=[];error.value=''}
async function open(){
  if(!scope.current())return
  stop();opened.value=true;notice.value='';error.value='';parsed.value=null;rows.value=[];targets.value=[];members.value=[];reviewed.value=false;loading.value=true
  const ticket=version
  try{
    const [people,sprints,categories,states,fields]=await Promise.all(['/members','/sprints','/requirement-categories','/requirement-statuses','/field-definitions?objectType=requirement'].map(path=>scope.request<any>(path)))
    if(ticket!==version)return
    if(!categories.canManage)throw Error('仅项目管理员或企业管理员可以迁移需求')
    // 保留整个项目目录供姓名匹配和不可用原因提示；选择器与匹配器各自排除不可分配成员。
    members.value=people.items
    const choices=(items:any[])=>items.map(x=>({value:x.key||x.name,label:x.name}))
    targets.value=[
      {key:'status',label:'状态',kind:'choice',options:choices(states.items.filter((x:any)=>x.enabled))},
      {key:'category',label:'需求分类',kind:'choice',options:[{value:'未分类',label:t('未分类')},...choices(categories.items.filter((x:any)=>x.name!=='未分类'))]},
      {key:'sprint',label:'迭代',kind:'choice',options:[{value:'待规划',label:t('待规划')},...choices(normalizeSprints(sprints.items).filter(sprintSelectable))]},
      {key:'priority',label:'优先级',kind:'choice',options:['P0','P1','P2','P3'].map(value=>({value,label:value}))},
      {key:'ownerUserIds',label:'产品负责人',kind:'members'},{key:'assigneeUserIds',label:'处理人',kind:'members'},
      ...(['frontend','backend','algorithm','ui','product'] as const).flatMap((key,index)=>[
        {key:`role.${key}.members`,label:['前端工程师','后端工程师','算法工程师','UI设计师','产品人员'][index]!,kind:'members' as const},
        {key:`role.${key}.value`,label:['前端开发难度','后端开发难度','算法架构难度','UI 难度','产品难度'][index]!,kind:'number' as const}]),
      ...['tags','remarks','acceptance'].map((key,index)=>({key,label:['标签','备注','验收标准'][index]!,kind:'text' as const})),
      {key:'sensitive',label:'涉及敏感数据',kind:'boolean'},{key:'authImpact',label:'涉及权限认证',kind:'boolean'},
      {key:'startDate',label:'计划开始',kind:'date'},{key:'endDate',label:'计划结束',kind:'date'},
      ...fields.items.filter((f:any)=>f.enabled&&['text','textarea','number','boolean','date','select','user','users'].includes(f.type)).map((f:any)=>({key:'cf.'+f.key,label:f.name,kind:({select:'choice',user:'members',users:'members',textarea:'text'} as any)[f.type]||f.type,single:f.type==='user',options:(f.options||[]).map((value:string)=>({value,label:value}))})),
    ]
  }catch(cause){if(ticket===version)error.value=cause instanceof Error?cause.message:'读取导入选项失败'}finally{if(ticket===version)loading.value=false}
}
async function upload(event:Event){
  const files=Array.from((event.target as HTMLInputElement).files||[])
  if(files.length)await selectFiles(files)
  if(fileInput.value)fileInput.value.value=''
}
function dragOver(event:DragEvent){if(event.dataTransfer)event.dataTransfer.dropEffect=fileDisabled.value?'none':'copy'}
function dragEnter(){if(!fileDisabled.value)dragDepth.value++}
function dragLeave(){dragDepth.value=Math.max(0,dragDepth.value-1)}
async function drop(event:DragEvent){dragDepth.value=0;if(!fileDisabled.value)await selectFiles(Array.from(event.dataTransfer?.files||[]))}
async function selectFiles(files:File[]){
  if(fileDisabled.value||!scope.current())return
  const invalid=tapdFileError(files)
  if(invalid){error.value=invalid;return}
  const file=files[0]!
  stop();const ticket=version;controller=new AbortController();const signal=controller.signal
  loading.value=true;error.value='';parsed.value=null;reviewed.value=false;rows.value=[];progress.value=''
  try{
    const {readTapdPdf}=await import('../tapdPdf')
    const result=await readTapdPdf(file,signal,value=>{if(ticket===version)progress.value=value})
    if(ticket!==version||!scope.current())return
    parsed.value=result;title.value=result.title;description.value=result.description;page.value=0
    tab.value='fields';showArchived.value=false;onlyIssues.value=false
    rows.value=result.fields.map(field=>suggestTapdMapping(field,targets.value,members.value))
  }catch(cause){if(ticket===version)error.value=cause instanceof Error?cause.message:'PDF 解析失败，请检查文件'}finally{if(ticket===version)loading.value=false;if(fileInput.value)fileInput.value.value=''}
}
function setTarget(row:TapdMapping,value:string|number){
  row.target=String(value);reviewed.value=false
  if(!row.target){row.issue='';row.value='';return}
  const target=targetOf(row)!
  const suggestion=suggestTapdMapping({...row.field,label:target.label},[{...target,key:'cf.manual'}],members.value)
  row.value=suggestion.value;row.issue=suggestion.issue
  if(target.kind==='members'&&!Array.isArray(row.value))row.value=[]
}
function changed(row:TapdMapping){row.issue='';reviewed.value=false}
function addRow(){rows.value.push({field:{label:'手动补充',value:''},target:'',value:'',issue:''});reviewed.value=false;onlyIssues.value=false}
async function submit(){
  if(saving.value||loading.value||!parsed.value||!reviewed.value||!scope.current())return
  error.value=''
  try{
    if(!title.value.trim())throw Error('标题不能为空')
    const payload=buildTapdRequirement(title.value.trim(),description.value,rows.value,targets.value)
    const doc=plainTextToDocument(description.value)
    // 页面图像作为受权限保护的正文附件保存；不执行 PDF 脚本，也不导入远程活动链接。
    doc.content!.push({type:'heading',attrs:{level:2},content:[{type:'text',text:'TAPD 原始页面（保真参考）'}]})
    parsed.value.images.forEach((data,index)=>doc.content!.push({type:'image',attrs:{name:`TAPD-page-${index+1}.jpg`,alt:`TAPD 原始 PDF 第 ${index+1} 页`,data:data.split(',')[1]}}))
    const {images,description:originalDescription,title:originalTitle,displayId,...source}=parsed.value
    const body=JSON.stringify({...payload,descriptionDoc:doc,tapdImport:{...source,reviewed:true}})
    if(new Blob([body]).size>30*1024*1024)throw Error('导入内容超过 30 MiB，请拆分 PDF 后重试')
    saving.value=true
    const result=await scope.request<{id:number;code:string;alreadyImported?:boolean}>('/requirements',{method:'POST',body})
    notice.value=result.alreadyImported?t('此文件已导入，未重复创建')+' · '+result.code:t('需求已导入')+' · '+result.code
    emit('imported',result.id);saving.value=false;close()
  }catch(cause){error.value=cause instanceof Error?cause.message:'导入失败，请重试'}finally{saving.value=false}
}
onBeforeUnmount(stop)
</script>

<template>
 <div class="tapd-entry"><button type="button" class="btn" :disabled="scope.locked.value" @click="open">{{t('TAPD 迁移')}}</button><span v-if="notice" role="status" class="tapd-notice">{{notice}}</span></div>
 <OrganizationModal v-if="opened" :title="t('TAPD PDF 迁移')" wide :busy="saving" @close="close">
  <div class="tapd-content" :aria-busy="loading||saving">
   <input ref="fileInput" class="tapd-file-input" type="file" accept="application/pdf,.pdf" :aria-label="t('选择 TAPD PDF')" :disabled="fileDisabled" @change="upload"/>
   <button type="button" class="tapd-dropzone" :class="{'is-dragging':dragDepth>0,'has-file':parsed,'is-disabled':fileDisabled}" :aria-disabled="fileDisabled" :aria-label="t('拖拽或选择 PDF')" @click="!fileDisabled&&fileInput?.click()" @dragenter.prevent.stop="dragEnter" @dragover.prevent.stop="dragOver" @dragleave.prevent.stop="dragLeave" @drop.prevent.stop="drop">
    <span class="tapd-file-icon"><Icon :name="parsed?'review':'folder'" :size="24"/></span>
    <span class="tapd-upload-copy"><strong>{{parsed?parsed.fileName:t(dragDepth?'松开文件，开始解析':'将 TAPD PDF 拖到这里')}}</strong><span>{{parsed?'TAPD '+parsed.displayId+' · '+parsed.pageCount+' '+t('页'):t('或点击选择文件 · PDF ≤ 10 MiB · 最多 30 页')}}</span></span>
    <span v-if="parsed" class="tapd-replace">{{t('更换文件')}}</span>
   </button>
   <div v-if="loading" class="tapd-progress" role="status"><span class="tapd-progress-dot"/>{{t('正在准备导入…')}} {{progress}}</div>
   <p v-else-if="!parsed&&!error" class="tapd-privacy"><Icon name="shield" :size="14"/>{{t('本地解析，不上传第三方；确认后才创建需求。')}}</p>
   <p v-if="error" class="tapd-error tapd-alert" role="alert">{{t(error)}}</p>
   <p v-if="scope.locked.value" class="tapd-error" role="alert">{{t('项目或账号已变化，请刷新页面后继续')}}</p>
   <template v-if="parsed">
    <label class="tapd-title"><span>{{t('需求标题')}}</span><input v-model="title" :disabled="saving" maxlength="300" @input="reviewed=false"/></label>
    <div class="tapd-tabs" role="tablist" :aria-label="t('导入预览')">
     <button type="button" id="tapd-fields-tab" role="tab" :aria-selected="tab==='fields'" aria-controls="tapd-fields-panel" @click="tab='fields'"><Icon name="fields"/>{{t('字段核对')}}<span>{{mapped}}</span></button>
     <button type="button" id="tapd-body-tab" role="tab" :aria-selected="tab==='body'" aria-controls="tapd-body-panel" @click="tab='body'"><Icon name="review"/>{{t('正文与截图')}}</button>
     <span class="tapd-source-count">{{parsed.fields.length}} {{t('个源字段')}}</span>
    </div>
    <section v-show="tab==='fields'" id="tapd-fields-panel" role="tabpanel" aria-labelledby="tapd-fields-tab">
     <div class="tapd-controls"><span><Icon name="user" :size="14"/>{{t('人员按姓名匹配')}}</span><button type="button" :class="{'is-active':onlyIssues}" :aria-pressed="onlyIssues" @click="onlyIssues=!onlyIssues">{{t('待确认')}} {{unresolved}}</button><label><input v-model="showArchived" type="checkbox" @change="onlyIssues=false"/>{{t('显示全部源字段')}}</label></div>
     <div class="tapd-field-head"><span>{{t('TAPD 原字段')}}</span><span>{{t('对应系统字段')}}</span><span>{{t('导入值')}}</span></div>
     <div class="tapd-fields" role="group" :aria-label="t('字段对照')">
      <div v-for="{row,index} in shownRows" :key="index" class="tapd-field" :class="{'has-issue':row.target&&row.issue}">
       <div class="tapd-source"><strong>{{row.field.label}}</strong><span :title="row.field.value">{{row.field.value||'—'}}</span></div>
       <AppSelect :model-value="row.target" :options="targetOptions" :label="t('目标字段')+' '+row.field.label" :disabled="saving" @update:model-value="setTarget(row,$event)"/>
       <div class="tapd-value">
        <template v-if="row.target">
         <MemberMultiSelect v-if="targetOf(row)?.kind==='members'" v-model="row.value" :members="members" :single="targetOf(row)?.single" :show-lead="false" :label="row.field.label" compact :disabled="saving" @update:model-value="changed(row)"/>
         <AppSelect v-else-if="targetOf(row)?.kind==='choice'" v-model="row.value" :options="choiceOptions(row)" :label="row.field.label" :disabled="saving" @update:model-value="changed(row)"/>
         <AppSelect v-else-if="targetOf(row)?.kind==='boolean'" v-model="row.value" :options="[{value:'',label:t('请选择目标值')},{value:'true',label:t('是')},{value:'false',label:t('否')}]" :label="row.field.label" :disabled="saving" @update:model-value="changed(row)"/>
         <input v-else v-model="row.value" :type="targetOf(row)?.kind==='number'?'number':targetOf(row)?.kind==='date'?'date':'text'" :aria-label="row.field.label" :disabled="saving" @input="changed(row)"/>
        </template><small v-else>{{t('仅留档，不写入业务字段')}}</small>
        <small v-if="row.target&&row.issue" class="tapd-error">{{t(row.issue)}}</small>
       </div>
      </div>
      <p v-if="!shownRows.length" class="tapd-empty">{{t(onlyIssues?'没有待确认字段':'暂无已映射字段，请显示全部源字段后配置')}}</p>
     </div>
     <div class="tapd-table-footer"><span>{{rows.filter(row=>!row.target).length}} {{t('个未映射字段已保留，不会丢失')}}</span><button type="button" class="btn" :disabled="saving" @click="addRow"><Icon name="plus" :size="14"/>{{t('补充目标字段')}}</button></div>
    </section>
    <section v-show="tab==='body'" id="tapd-body-panel" role="tabpanel" aria-labelledby="tapd-body-tab" class="tapd-body-grid">
     <label class="tapd-description"><span>{{t('可编辑正文')}}</span><textarea v-model="description" :disabled="saving" rows="14" @input="reviewed=false"/></label>
     <div class="tapd-original"><div class="tapd-pages"><strong>{{t('原始页面')}}</strong><button type="button" class="btn" :disabled="page===0" @click="page--" :aria-label="t('上一页')">‹</button><span>{{page+1}} / {{parsed.pageCount}}</span><button type="button" class="btn" :disabled="page+1===parsed.pageCount" @click="page++" :aria-label="t('下一页')">›</button></div><img :src="parsed.images[page]" :alt="t('原始 PDF 页面预览')"/></div>
    </section>
    <details class="tapd-help"><summary>{{t('迁移规则与来源留存')}}</summary><p>{{t('未映射时默认：项目初始状态、未分类、待规划、P2。未知人员不会自动新增，同名人员需要人工选择。')}}</p><ul><li v-for="warning in parsed.warnings" :key="warning">{{t(warning)}}</li><li>{{t('前后端组长、UI 奖励等没有等义目标的字段仅保留来源，不会冒充工程师或难度。')}}</li></ul></details>
   </template>
   <div v-else-if="!loading" class="tapd-steps"><span><b>1</b>{{t('上传 PDF')}}</span><span><b>2</b>{{t('核对字段与人员')}}</span><span><b>3</b>{{t('确认创建需求')}}</span></div>
  </div>
  <template #footer><div class="tapd-footer"><label v-if="parsed" class="tapd-confirm" :title="t('已核对所有字段、正文与页面预览，同意按以上映射创建需求')"><input v-model="reviewed" type="checkbox" :disabled="saving"/>{{t('已核对字段与正文')}}</label><span v-else class="tapd-footer-note">{{t('原 PDF 与字段对照将随需求保存')}}</span><div class="tapd-actions"><button type="button" class="btn" :disabled="saving" @click="close">{{t('取消')}}</button><button type="button" class="btn primary" :disabled="!parsed||!reviewed||unresolved>0||loading||saving||scope.locked.value" @click="submit">{{t(saving?'正在导入…':'确认导入需求')}}</button></div></div></template>
 </OrganizationModal>
</template>

<style scoped>
.tapd-entry,.tapd-actions{display:flex;align-items:center;gap:8px;flex-wrap:wrap}
.tapd-content{font-size:13px;min-width:0;color:var(--ink)}
.tapd-notice,.tapd-footer-note{font-size:12px;color:var(--muted)}
.tapd-file-input{display:none}
.tapd-content .tapd-dropzone{width:100%;display:flex;flex-direction:column;align-items:center;justify-content:center;gap:16px;min-height:200px;padding:32px 20px;border:1px dashed var(--line);border-radius:8px;background:var(--surface,#fff);color:var(--ink);cursor:pointer;box-shadow:none;text-align:center;transition:background .15s,border-color .15s}
.tapd-content .tapd-dropzone:hover,.tapd-content .tapd-dropzone.is-dragging{border-color:var(--primary,#3370ff);background:color-mix(in srgb,var(--primary,#3370ff) 6%,var(--surface,#fff))}
.tapd-content .tapd-dropzone.is-disabled{cursor:wait;opacity:.65}
.tapd-content .tapd-dropzone.has-file{min-height:0;flex-direction:row;padding:12px 14px;gap:12px;border-style:solid;text-align:left}
.tapd-file-icon{display:grid;place-items:center;flex:none;width:44px;height:44px;border-radius:8px;color:var(--primary,#3370ff);background:color-mix(in srgb,var(--primary,#3370ff) 8%,var(--surface,#fff))}
.tapd-upload-copy{display:grid;gap:6px;min-width:0}.tapd-upload-copy strong{font-size:14px;font-weight:600;overflow-wrap:anywhere;line-height:1.5}.tapd-upload-copy>span,.tapd-replace{font-size:12px;color:var(--muted)}.tapd-replace{margin-left:auto;flex:none;color:var(--primary,#3370ff)}
.tapd-privacy,.tapd-progress{display:flex;align-items:center;justify-content:center;gap:6px;margin:12px 0;color:var(--muted);font-size:12px}.tapd-progress{justify-content:flex-start}.tapd-progress-dot{width:6px;height:6px;border-radius:50%;background:var(--primary,#3370ff)}
.tapd-title{display:flex;align-items:center;gap:14px;margin:16px 0}.tapd-title>span{flex:none;font-size:12px;color:var(--muted)}
.tapd-content input:not([type=checkbox]):not([type=file]),.tapd-content textarea{width:100%;min-width:0;box-sizing:border-box;border:1px solid var(--line);border-radius:6px;background:var(--surface,#fff);color:var(--ink);padding:7px 9px;font:inherit;box-shadow:none}
.tapd-content input:focus-visible,.tapd-content textarea:focus-visible,.tapd-dropzone:focus-visible,.tapd-tabs button:focus-visible{outline:2px solid var(--primary,#3370ff);outline-offset:2px}
.tapd-tabs{display:flex;align-items:center;gap:18px;border-bottom:1px solid var(--line);margin-top:4px}.tapd-tabs button{display:flex;align-items:center;gap:6px;background:transparent;border:0;border-bottom:2px solid transparent;border-radius:0;padding:10px 0;font:inherit;color:var(--muted);cursor:pointer}.tapd-tabs button[aria-selected=true]{color:var(--primary,#3370ff);border-bottom-color:var(--primary,#3370ff)}.tapd-tabs button>span{padding:1px 5px;background:color-mix(in srgb,var(--primary,#3370ff) 8%,var(--surface,#fff));border-radius:4px;font-size:11px}.tapd-source-count{margin-left:auto;color:var(--muted);font-size:12px;white-space:nowrap}
.tapd-controls{display:flex;align-items:center;gap:10px;padding:12px 0;font-size:12px;color:var(--muted);flex-wrap:wrap}.tapd-controls>span{display:flex;align-items:center;gap:5px;margin-right:auto}.tapd-controls button{border:1px solid var(--line);border-radius:5px;padding:4px 8px;background:transparent;color:var(--muted);font:inherit;cursor:pointer}.tapd-controls button.is-active{color:var(--primary,#3370ff);border-color:currentColor}.tapd-controls label,.tapd-confirm{display:flex;align-items:center;gap:6px}
.tapd-field,.tapd-field-head{display:grid;grid-template-columns:minmax(120px,1fr) minmax(145px,.85fr) minmax(175px,1.25fr);gap:14px;align-items:start}.tapd-field-head{padding:8px 10px;background:color-mix(in srgb,var(--muted) 5%,var(--surface,#fff));color:var(--muted);font-size:12px;border-radius:5px}
.tapd-field{padding:12px 10px;border-bottom:1px solid var(--line)}.tapd-source,.tapd-value{display:grid;gap:5px;min-width:0}.tapd-source>span{white-space:pre-wrap;overflow-wrap:anywhere;color:var(--muted);font-size:12px;line-height:1.55}.tapd-source strong{font-weight:500;font-size:13px}.tapd-value>small{color:var(--muted);font-size:12px;line-height:1.5}
.tapd-error{color:var(--danger,#c43d3d)!important;overflow-wrap:anywhere}.tapd-alert{border:1px solid color-mix(in srgb,var(--danger,#c43d3d) 25%,var(--surface,#fff));padding:10px;border-radius:6px;font-size:12px}.tapd-table-footer{display:flex;align-items:center;justify-content:space-between;gap:10px;padding-top:12px;font-size:12px;color:var(--muted);flex-wrap:wrap}.tapd-empty{padding:24px;text-align:center;color:var(--muted)}
.tapd-body-grid{display:grid;grid-template-columns:minmax(0,1fr) minmax(0,1fr);gap:18px;padding-top:16px}.tapd-description{display:grid;gap:10px;align-content:start;font-size:12px;color:var(--muted)}.tapd-content textarea{resize:vertical;min-height:280px;max-height:55vh;line-height:1.7}.tapd-original{min-width:0}.tapd-original img{display:block;width:100%;height:auto;border:1px solid var(--line);border-radius:4px}.tapd-pages{display:flex;align-items:center;gap:8px;margin-bottom:10px;font-size:12px}.tapd-pages strong{margin-right:auto;font-weight:500}.tapd-pages .btn{min-width:28px}
.tapd-help{margin-top:16px;padding-top:12px;border-top:1px solid var(--line);font-size:12px;color:var(--muted)}.tapd-help summary{cursor:pointer}.tapd-help p{margin:10px 0 0;line-height:1.7}.tapd-help ul{padding-left:18px;line-height:1.7}
.tapd-footer{display:flex;align-items:center;justify-content:space-between;gap:12px;width:100%;flex-wrap:wrap}.tapd-confirm{font-size:12px;color:var(--muted);cursor:pointer}.tapd-confirm input,.tapd-controls input{accent-color:var(--primary,#3370ff)}.tapd-actions{margin-left:auto;flex:none}
.tapd-steps{display:flex;justify-content:center;gap:32px;padding:22px 0 6px;color:var(--muted);font-size:12px}.tapd-steps>span{display:flex;align-items:center;gap:8px}.tapd-steps b{display:grid;place-items:center;width:20px;height:20px;border-radius:50%;background:color-mix(in srgb,var(--muted) 8%,var(--surface,#fff));font-size:11px;font-weight:500}
@media(max-width:700px){.tapd-field-head{display:none}.tapd-field{grid-template-columns:1fr;gap:8px;padding:12px 0}.tapd-source{grid-template-columns:100px 1fr}.tapd-body-grid{grid-template-columns:1fr}.tapd-steps{gap:12px;flex-wrap:wrap}.tapd-title{align-items:flex-start;flex-direction:column;gap:6px}.tapd-source-count{display:none}.tapd-footer-note{display:none}.tapd-content .tapd-dropzone.has-file{align-items:flex-start}.tapd-replace{max-width:60px}.tapd-tabs{gap:12px}}
@media(prefers-reduced-motion:reduce){.tapd-content .tapd-dropzone{transition:none}}
</style>

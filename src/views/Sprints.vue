<script setup lang="ts">
import { t, locale, timezone, categoryLabel, formatDate, formatNumber } from '../i18n'
import DatePicker from '../components/DatePicker.vue'
import { defaultSprintDates, linkedSprintDates, isCalendarDate } from '../calendarDates'
import SprintWeightPanel from '../components/SprintWeightPanel.vue'
import MemberMultiSelect from '../components/MemberMultiSelect.vue'
import WorkItemFilters from '../components/WorkItemFilters.vue'
import WorkItemColumns from '../components/WorkItemColumns.vue'
import StatusMultiSelect from '../components/StatusMultiSelect.vue'
import AppSelect from '../components/AppSelect.vue'
import RequirementCode from '../components/RequirementCode.vue'
import RequirementListExport from '../components/RequirementListExport.vue'
import SprintTabs from '../components/SprintTabs.vue'
import Requirements from './Requirements.vue'
import SidebarCollapseButton from '../components/SidebarCollapseButton.vue'
import { useLayoutBoolean } from '../layoutScope'
import { useSprintTabs } from '../sprintTabs'
import { stateInfo, statusLabel, statusOptionLabel, workflowOptions, workflowStyle, defectStatusOptions, matchesSelectedStatuses, type RequirementState } from '../requirementWorkflow'
import { queryWorkItems, workItemFields, workItemValue, workItemPersonNames, type WorkField, type WorkFilter } from '../workItemQuery'
import { splitTags, tagStyle } from '../requirementFields'
import { sprintParticipants, sprintTeamParticipation, sprintWorkPayload } from '../sprintPeople'
import type { SprintWeightSummary } from '../sprintWeights'
import{computed,nextTick,onBeforeUnmount,onMounted,reactive,ref,watch}from'vue';import{useRoute,useRouter}from'vue-router';import{api}from'../api'
const route=useRoute(),router=useRouter()
const iterationSidebarExpanded=useLayoutBoolean('iterations.sidebar',true)
const pageProject=localStorage.getItem('devflow-project')||'prj_orbit'
let disposed=false,columnsVersion=0,refreshVersion=0
let requirementTrigger:HTMLElement|null=null
const currentPage=()=>!disposed&&(localStorage.getItem('devflow-project')||'prj_orbit')===pageProject
const projectHeaders=()=>({'X-DevFlow-Project':pageProject})
const backlogWeightSummary=ref<SprintWeightSummary|null>(null)
const statusDefinitions=ref<RequirementState[]>([]),listStatuses=ref<string[]>([])
const statusFilterOptions=computed(()=>{const options=workflowOptions(statusDefinitions.value,(selected.value?.items||[]).map((x:any)=>x.status));for(const option of defectStatusOptions)if(!options.some(item=>item.value===option.value))options.push(option);return options})
const listMine=ref(false)
const filteredWorkItems=computed(()=>(selected.value?.items||[]).filter((item:any)=>(!listMine.value||item.relatedToMe===true)&&matchesSelectedStatuses(item.status,listStatuses.value)))
const fieldDefinitions=ref<any[]>([]),listRules=ref<WorkFilter[]>([]),listSearch=ref(''),listSort=ref('updatedAt'),listOrder=ref('desc'),listPage=ref(1),listColumns=ref<string[]>([]),columnsSaving=ref(false),columnsError=ref(''),columnPanel=ref<InstanceType<typeof WorkItemColumns>|null>(null)
const listFields=computed(()=>workItemFields(fieldDefinitions.value,members.value,true).map(field=>field.key==='status'?{...field,options:statusFilterOptions.value.map(option=>({...option,label:statusOptionLabel(option,t)}))}:field))
const visibleColumns=computed(()=>listColumns.value.map(key=>listFields.value.find(field=>field.key===key)).filter(Boolean) as WorkField[])
const listRows=computed(()=>queryWorkItems(filteredWorkItems.value,listFields.value,listRules.value,listSort.value,listOrder.value,members.value,timezone.value).filter(item=>!listSearch.value||[item.title,item.code].some(value=>String(value||'').toLowerCase().includes(listSearch.value.toLowerCase()))))
const listPages=computed(()=>Math.max(1,Math.ceil(listRows.value.length/30)))
const pagedRows=computed(()=>listRows.value.slice((listPage.value-1)*30,listPage.value*30))
function sprintColumnWidth(field:WorkField){
 const base=field.width||130
 if(field.key==='code')return Math.max(base,132)
 if(field.key==='title')return Math.max(base,300)
 if(field.key==='status')return Math.max(base,112)
 return base
}
const listWidth=computed(()=>visibleColumns.value.reduce((total,field)=>total+sprintColumnWidth(field),0))
const listSortOptions=computed(()=>listFields.value.map(field=>({value:field.key,label:listFieldLabel(field)})))
function listFieldLabel(field:WorkField){return field.custom?field.label:t(field.label)}
function sortColumn(key:string){if(listSort.value===key)listOrder.value=listOrder.value==='asc'?'desc':'asc';else{listSort.value=key;listOrder.value='asc'}}
function selectListSort(value:string|number){listSort.value=String(value)}
function displayCell(item:any,field:WorkField):string{
 if(field.kind==='person')return workItemPersonNames(item,field.key,members.value).join('、')||'—'
 const value=workItemValue(item,field.key)
 if(value==null||value==='')return '—'
 if(field.kind==='boolean')return t(value?'是':'否')
 if(field.kind==='number')return formatNumber(Number(value),{maximumFractionDigits:6})
 if(field.kind==='date')return formatDate(value)
 if(field.key==='objectType')return t(value==='defect'?'缺陷':'需求')
 if(field.key==='category')return categoryLabel(value)
 if(field.key==='discipline')return t(disciplineNames[value]||value)
 if(field.key==='status')return statusLabel(item,item.objectType==='defect'?[]:statusDefinitions.value,t)
 if(field.key==='type')return t(value)
 if(field.key==='sprint'&&value==='待规划')return t(value)
 return Array.isArray(value)?value.join('、')||'—':String(value)
}
async function loadColumns(){
 if(!currentPage()||columnsSaving.value)return
 const version=++columnsVersion
 try{const data=await api<any>('/preferences/requirement-list?view=sprint-list',{headers:projectHeaders()});if(!currentPage()||version!==columnsVersion)return;listColumns.value=['code','title',...(Array.isArray(data.columns)?data.columns:listFields.value.filter(field=>field.default).map(field=>field.key)).filter((key:string,index:number,keys:string[])=>!['code','title'].includes(key)&&keys.indexOf(key)===index&&listFields.value.some(field=>field.key===key))];columnsError.value=''}catch(cause){if(currentPage()&&version===columnsVersion){listColumns.value=listFields.value.filter(field=>field.fixed||field.default).map(field=>field.key);columnsError.value=errorMessage(cause)}}
}
async function saveColumns(columns:string[]){
 if(!currentPage()||columnsSaving.value)return
 const version=++columnsVersion,next=[...columns],panel=columnPanel.value
 columnsSaving.value=true;columnsError.value=''
 try{await api('/preferences/requirement-list?view=sprint-list',{method:'PATCH',headers:projectHeaders(),body:JSON.stringify({columns:next})});if(currentPage()&&version===columnsVersion){listColumns.value=next;if(columnPanel.value===panel)panel?.close()}}catch(cause){if(currentPage()&&version===columnsVersion)columnsError.value=errorMessage(cause)}finally{if(currentPage()&&version===columnsVersion)columnsSaving.value=false}
}
const sprints=ref<any[]>([]),members=ref<any[]>([]),selected=ref<any>(null),backlog=ref<any[]>([]),activeTab=ref('工作项列表'),showSprint=ref(false),showWork=ref(false),showComplete=ref(false),error=ref(''),completionTarget=ref('待规划'),loading=ref(true),saving=ref(false)
const sprintForm=reactive<any>({name:'',goal:'',startDate:'',endDate:'',status:'规划中',capacity:80})
const sprintCustomized=reactive({name:false,endDate:false}),sprintDateValidity=reactive({start:true,end:true})
function openSprint(){if(!currentPage()||saving.value||!canManage.value)return;Object.assign(sprintForm,{goal:'',status:'规划中',capacity:80},defaultSprintDates());Object.assign(sprintCustomized,{name:false,endDate:false});Object.assign(sprintDateValidity,{start:true,end:true});error.value='';showSprint.value=true}
function changeSprintStart(value:string){Object.assign(sprintForm,linkedSprintDates(value,sprintForm,sprintCustomized))}
const workForm=reactive<any>({objectType:'requirement',title:'',description:'',priority:'P2',assignee:'',assigneeUserId:'',assigneeUserIds:[],discipline:'product',progress:0,estimatedHours:8,actualHours:0,sprint:''})
const tabs=useSprintTabs()
const roleNames:Record<string,string>={tenant_admin:'企业管理员',project_admin:'项目管理员',product:'产品',frontend:'前端',backend:'后端',algorithm:'算法',ui:'UI 设计',frontend_lead:'前端组长',backend_lead:'后端组长',qa:'测试',viewer:'只读'}
const disciplineNames:Record<string,string>={product:'产品',frontend:'前端',backend:'后端',algorithm:'算法',ui:'UI 设计',qa:'测试'}
const canManage=computed(()=>['tenant_admin','project_admin','product','frontend_lead','backend_lead'].includes(members.value.find(x=>x.isCurrent)?.projectRole))
const canWrite=computed(()=>{const role=members.value.find(x=>x.isCurrent)?.projectRole;return !!role&&role!=='viewer'})
const canCreateWork=computed(()=>canWrite.value&&!!selected.value&&(selected.value.backlog||['规划中','进行中'].includes(selected.value.sprint.status)))
const done=(x:any)=>x.objectType==='defect'?x.status==='已关闭':stateInfo(x,statusDefinitions.value).category==='done'
const terminal=(x:any)=>done(x)||(x.objectType==='defect'?x.status==='已拒绝':stateInfo(x,statusDefinitions.value).category==='cancelled')
// Released is an explicit workflow label, not every completed or cancelled state.
function isReleasedRequirement(item:any):boolean{
 if(item.objectType!=='requirement')return false
 return ['已上线','已经上线'].includes(stateInfo(item,statusDefinitions.value).name)
}
let loadVersion=0
const errorMessage=(cause:unknown)=>cause instanceof Error?cause.message:'操作失败，请稍后重试'
async function load(preferredId?:number){
  if(!currentPage())return
  const version=++loadVersion
  loading.value=true;error.value=''
  try{
    if(preferredId!==undefined&&(!Number.isInteger(preferredId)||preferredId<=0))throw new Error('迭代链接无效，请从左侧选择迭代')
    const options={headers:projectHeaders()}
    const [s,m,b,w,f,states]=await Promise.all([api<any>('/sprints',options),api<any>('/members',options),api<any>('/sprints/backlog/items',options),api<{weightSummary:SprintWeightSummary}>('/sprints/backlog/weights',options),api<any>('/field-definitions',options),api<any>('/requirement-statuses',options)])
    if(version!==loadVersion||!currentPage())return
    sprints.value=s.items||[];members.value=m.items||[];backlog.value=b.items||[];fieldDefinitions.value=(f.items||[]).filter((field:any)=>['requirement','defect'].includes(field.objectType)&&field.enabled)
    backlogWeightSummary.value=w.weightSummary
    statusDefinitions.value=states.items||[]
    const id=preferredId??selected.value?.sprint.id
    if(preferredId===undefined&&route.query.sprint==='backlog')openBacklog(false)
    else if(id){const detail=await api('/sprints/'+id,options);if(version!==loadVersion||!currentPage())return;selected.value=detail}
    else if(selected.value?.backlog)openBacklog(false)
    else{const first=sprints.value.find(x=>x.status==='进行中')||sprints.value[0];if(first){const detail=await api('/sprints/'+first.id,options);if(version!==loadVersion||!currentPage())return;selected.value=detail}else openBacklog(false)}
  }catch(cause){if(version===loadVersion&&currentPage()){error.value=errorMessage(cause);selected.value=null}}
  finally{if(version===loadVersion&&currentPage())loading.value=false}
}
async function open(id:number){
  if(!currentPage()||saving.value)return
  const version=++loadVersion
  loading.value=true;error.value='';selected.value=null
  try{if(!Number.isInteger(id)||id<=0)throw new Error('迭代链接无效，请从左侧选择迭代');const detail=await api('/sprints/'+id,{headers:projectHeaders()});if(version===loadVersion&&currentPage()){selected.value=detail;activeTab.value='工作项列表'}}
  catch(cause){if(version===loadVersion&&currentPage())error.value=errorMessage(cause)}
  finally{if(version===loadVersion&&currentPage())loading.value=false}
}
function openBacklog(cancelRequest=true){if(!currentPage()||(cancelRequest&&saving.value))return;if(cancelRequest){loadVersion++;loading.value=false;error.value=''}selected.value={backlog:true,sprint:{name:'待规划工作项',code:'BACKLOG',goal:'尚未纳入迭代的需求与缺陷'},items:backlog.value,summary:summary(backlog.value),weightSummary:backlogWeightSummary.value};if(cancelRequest)activeTab.value='工作项列表'}
function workItemLink(x:any){return x.objectType==='defect'?{path:'/defects',query:{bug:x.id}}:{path:'/requirements',query:{req:x.id}}}
async function openRequirement(x:any){
 if(!currentPage()||x.objectType!=='requirement'||!Number.isSafeInteger(Number(x.id))||Number(x.id)<1||!selected.value)return
 requirementTrigger=document.activeElement instanceof HTMLElement?document.activeElement:null
 try{await router.replace({path:route.path,query:{...route.query,sprint:selected.value.backlog?'backlog':String(selected.value.sprint.id),req:String(x.id)}})}catch(cause){if(currentPage())error.value=errorMessage(cause)}
}
async function navigateSprint(id:number|'backlog'){
 if(!currentPage()||saving.value)return
 const query={...route.query};query.sprint=String(id);delete query.req;delete query.createChild
 try{const failure=await router.replace({path:route.path,query});if(!failure)activeTab.value='工作项列表'}catch(cause){if(currentPage())error.value=errorMessage(cause)}
}
async function refreshRequirement(){
 if(!currentPage()||!selected.value)return
 const version=++refreshVersion,selection=selected.value.sprint.id,isBacklog=!!selected.value.backlog,loadAt=loadVersion,options={headers:projectHeaders()}
 try{
  const [all,detail]=await Promise.all([api<any>('/sprints',options),isBacklog?Promise.all([api<any>('/sprints/backlog/items',options),api<any>('/sprints/backlog/weights',options)]):api<any>('/sprints/'+selection,options)])
  if(!currentPage()||version!==refreshVersion||loadAt!==loadVersion||selection!==selected.value?.sprint.id||isBacklog!==!!selected.value?.backlog)return
  sprints.value=all.items||[]
  if(isBacklog){const [items,weights]=detail;backlog.value=items.items||[];backlogWeightSummary.value=weights.weightSummary;openBacklog(false)}else selected.value=detail
 }catch(cause){if(currentPage()&&version===refreshVersion&&loadAt===loadVersion)error.value=errorMessage(cause)}
}
function summary(xs:any[]){return{total:xs.length,done:xs.filter(done).length,unfinished:xs.filter(x=>!terminal(x)).length,estimatedHours:xs.reduce((n,x)=>n+Number(x.estimatedHours||0),0),actualHours:xs.reduce((n,x)=>n+Number(x.actualHours||0),0)}}
const metrics=computed(()=>summary(selected.value?.items||[]));const percent=computed(()=>metrics.value.total?Math.round(metrics.value.done/metrics.value.total*100):0)
const board=computed(()=>{const g:Record<string,any[]>={待处理:[],进行中:[],验证中:[],已完成:[],已终止:[]};for(const x of listRows.value){if(done(x))g.已完成.push(x);else if(terminal(x))g.已终止.push(x);else if(['测试中','待验证','已解决','冒烟测试完成','待上线'].includes(x.status))g.验证中.push(x);else if((x.objectType!=='defect'&&stateInfo(x,statusDefinitions.value).category==='doing')||['修复中','已确认'].includes(x.status))g.进行中.push(x);else g.待处理.push(x)}return g})
const statusStats=computed(()=>{const m=new Map<string,{name:string;color:string;value:number}>();for(const x of selected.value?.items||[]){const definitions=x.objectType==='defect'?[]:statusDefinitions.value,info=stateInfo(x,definitions),name=statusLabel(x,definitions,t),key=info.key+'|'+name+'|'+info.color;const row=m.get(key)||{name,color:info.color,value:0};row.value++;m.set(key,row)}return [...m.values()].map(row=>({...row,pct:metrics.value.total?Math.round(row.value/metrics.value.total*100):0}))})
const disciplineStats=computed(()=>Object.entries((selected.value?.items||[]).reduce((a:any,x:any)=>{const k=x.discipline||'未设置';a[k]||(a[k]={name:disciplineNames[k]||k,total:0,done:0,estimated:0,actual:0});a[k].total++;a[k].done+=done(x)?1:0;a[k].estimated+=Number(x.estimatedHours||0);a[k].actual+=Number(x.actualHours||0);return a},{})).map(([,v])=>v as any))
const team=computed(()=>sprintTeamParticipation(selected.value?.items||[],members.value,done))
function sprintPct(x:any){return x.total?Math.round(x.done/x.total*100):0}
async function createSprint(){if(saving.value||!currentPage()||!canManage.value)return;if(!sprintDateValidity.start||!sprintDateValidity.end||!isCalendarDate(sprintForm.startDate)||!isCalendarDate(sprintForm.endDate)||sprintForm.endDate<sprintForm.startDate){error.value='迭代日期不能为空或格式不正确';return}error.value='';saving.value=true;try{const s=await api<any>('/sprints',{method:'POST',body:JSON.stringify(sprintForm)});showSprint.value=false;activeTab.value='工作项列表';await load(s.id)}catch(cause){error.value=errorMessage(cause)}finally{saving.value=false}}
function prepareWork(){if(!canCreateWork.value)return;Object.assign(workForm,{objectType:'requirement',title:'',description:'',priority:'P2',assignee:'',assigneeUserId:'',assigneeUserIds:[],discipline:'product',progress:0,estimatedHours:8,actualHours:0,sprint:selected.value.backlog?'待规划':selected.value.sprint.name});error.value='';showWork.value=true}
async function createWork(){if(saving.value||!canCreateWork.value)return;error.value='';saving.value=true;try{const body=sprintWorkPayload(workForm);await api(workForm.objectType==='defect'?'/defects':'/requirements',{method:'POST',body:JSON.stringify(body)});showWork.value=false;await load()}catch(cause){error.value=errorMessage(cause)}finally{saving.value=false}}
async function patchSprint(p:any,id=selected.value?.sprint?.id){if(!currentPage()||saving.value||!canManage.value||!id||selected.value?.sprint?.id!==id)return false;error.value='';saving.value=true;try{await api('/sprints/'+id,{method:'PATCH',headers:projectHeaders(),body:JSON.stringify(p)});if(currentPage()&&selected.value?.sprint?.id===id)await load(id);return true}catch(cause){if(currentPage()&&selected.value?.sprint?.id===id)error.value=errorMessage(cause);return false}finally{if(currentPage())saving.value=false}}
async function patchItem(x:any,p:any,event:Event){if(saving.value||!canWrite.value)return;error.value='';saving.value=true;try{await api(`/${x.objectType==='defect'?'defects':'requirements'}/${x.id}`,{method:'PATCH',body:JSON.stringify(p)});await load()}catch(cause){error.value=errorMessage(cause);const key=Object.keys(p)[0];(event.target as HTMLInputElement).value=String(x[key]??'')}finally{saving.value=false}}
async function complete(){if(saving.value)return;error.value='';saving.value=true;try{await api('/sprints/'+selected.value.sprint.id+'/complete',{method:'POST',body:JSON.stringify({targetSprint:completionTarget.value})});showComplete.value=false;await load()}catch(cause){error.value=errorMessage(cause)}finally{saving.value=false}}
onMounted(async()=>{await load(route.query.sprint&&route.query.sprint!=='backlog'?Number(route.query.sprint):undefined);await loadColumns()})
watch([listMine,listRules,listSearch,listSort,listOrder,listStatuses,()=>selected.value?.sprint?.id,()=>selected.value?.backlog],()=>{listPage.value=1})
watch(listPages,pages=>{listPage.value=Math.min(pages,listPage.value)})
watch(()=>route.query.sprint,value=>{if(value==='backlog'){if(!selected.value?.backlog)void load()}else if(value){if(Number(value)!==selected.value?.sprint.id)void load(Number(value))}else if(!route.query.req)void load()})
watch(()=>route.query.req,async value=>{if(!value){await nextTick();if(requirementTrigger?.isConnected)requirementTrigger.focus({preventScroll:true});requirementTrigger=null}})
onBeforeUnmount(()=>{disposed=true;loadVersion++;columnsVersion++;refreshVersion++})
</script>

<template><div class="iteration-shell" :inert="!!route.query.req"><aside class="iteration-nav" :class="{'is-collapsed':!iterationSidebarExpanded}"><SidebarCollapseButton :expanded="iterationSidebarExpanded" :label="t('迭代侧边栏')" @toggle="iterationSidebarExpanded=!iterationSidebarExpanded"/><div v-show="iterationSidebarExpanded" class="iteration-nav-content"><header><div><span class="eyebrow">{{ t("交付节奏") }}</span><h2>{{ t("迭代") }}</h2></div><button v-if="canManage" type="button" class="create-sprint-button" :disabled="saving" @click="openSprint"><span aria-hidden="true">＋</span>{{t('创建迭代')}}</button></header><button class="backlog-link" :class="{active:selected?.backlog}" :disabled="saving||loading" @click="navigateSprint('backlog')"><span>☷</span><b>{{ t("待规划") }}</b><em>{{backlog.length}}</em></button><div class="iteration-label">{{ t("进行中 / 规划中") }}</div><p v-if="!loading&&!sprints.length" class="empty-mini">{{ t("暂无迭代，可先在待规划中整理工作项。") }}</p><button v-for="x in sprints.filter(x=>!['已完成','已取消'].includes(x.status))" :key="x.id" class="iteration-card" :disabled="saving" :class="{active:selected?.sprint?.id===x.id}" @click="navigateSprint(x.id)"><div><b>{{x.name}}</b><span :class="['status',x.status]">{{ t(x.status) }}</span></div><small>{{formatDate(x.startDate)}} — {{formatDate(x.endDate)}}</small><div class="nav-progress"><i :style="{width:sprintPct(x)+'%'}"></i></div><footer><span>{{x.done}} / {{x.total}} {{ t("项") }}</span><strong>{{sprintPct(x)}}%</strong></footer></button><div class="iteration-label">{{ t("历史迭代") }}</div><button v-for="x in sprints.filter(x=>['已完成','已取消'].includes(x.status))" :key="x.id" class="iteration-card compact-card" :disabled="saving" :class="{active:selected?.sprint?.id===x.id}" @click="navigateSprint(x.id)"><div><b>{{x.name}}</b><span :class="['status',x.status]">{{ t(x.status) }}</span></div><small>{{x.done}} / {{x.total}} {{ t("项 ·") }} {{sprintPct(x)}}%</small></button></div></aside>
<section class="iteration-main" v-if="selected&&!loading"><header class="iteration-head compact-page-heading"><div><div class="iteration-kicker"><span>{{selected.sprint.code}}</span><span :class="['status',selected.sprint.status]">{{t(selected.sprint.status||"待规划")}}</span></div><h1 :title="selected.backlog ? t(selected.sprint.name) : selected.sprint.name">{{selected.backlog ? t(selected.sprint.name) : selected.sprint.name}}</h1><details v-if="selected.sprint.goal" class="page-heading-note iteration-goal"><summary :aria-label="t('迭代目标')" :title="t('迭代目标')"><span aria-hidden="true">?</span></summary><p>{{selected.backlog ? t(selected.sprint.goal) : selected.sprint.goal}}</p></details></div><div class="iteration-actions"><button v-if="canManage&&selected.sprint.status==='规划中'" class="btn" :disabled="saving" @click="patchSprint({status:'进行中'})">{{ t("开始迭代") }}</button><button v-if="canManage&&selected.sprint.status==='进行中'" class="btn danger-outline" :disabled="saving" @click="patchSprint({status:'已取消'})">{{ t("取消迭代") }}</button><button v-if="canManage&&selected.sprint.status==='进行中'" class="btn" :disabled="saving" @click="error='';completionTarget='待规划';showComplete=true">{{ t("完成迭代") }}</button><button v-if="canWrite" class="btn primary" :disabled="!canCreateWork||saving" :title="t(canCreateWork?'创建需求或缺陷':'历史迭代不能新增工作项')" @click="prepareWork">{{ t("＋ 创建工作项") }}</button></div></header><div v-if="error&&!showSprint&&!showWork&&!showComplete" class="readonly-banner" role="alert">{{ t(error) }}</div><div v-if="!selected.backlog&&['已完成','已取消'].includes(selected.sprint.status)" class="readonly-banner">{{ t("此迭代已结束，保留历史工作项；新增需求或缺陷请放入待规划或开放迭代。") }}</div><SprintTabs v-model="activeTab" v-model:order="tabs"><template #工作项列表><small>{{metrics.total}}</small></template></SprintTabs>
<div v-if="['工作项列表','看板'].includes(activeTab)" class="sprint-shared-tools">
 <div class="list-toolbar configurable-sprint-toolbar"><div class="sprint-toolbar-primary"><button type="button" class="btn compact sprint-related-toggle" :class="{active:listMine,primary:listMine}" :aria-pressed="listMine" :title="t('包含人员字段、工程师角色、创建、评论参与及 @ 提及')" @click="listMine=!listMine">{{t('与我相关')}}</button><StatusMultiSelect v-model="listStatuses" :options="statusFilterOptions" :label="t('筛选迭代工作状态（多选）')" /><input class="sprint-search-input" v-model="listSearch" :aria-label="t('搜索编号或标题')" :placeholder="t('搜索编号或标题')"><WorkItemFilters v-model="listRules" :fields="listFields"/><button v-if="listMine||listRules.length||listSearch||listStatuses.length" class="link" @click="listMine=false;listRules=[];listSearch='';listStatuses=[]">{{t('清除筛选')}}</button></div><div class="sprint-toolbar-actions"><AppSelect class="sprint-sort-select" :model-value="listSort" :options="listSortOptions" :label="t('排序字段')" @update:model-value="selectListSort"/><button class="btn compact" :aria-label="listOrder==='asc'?t('切换降序'):t('切换升序')" @click="listOrder=listOrder==='asc'?'desc':'asc'">{{listOrder==='asc'?'↑':'↓'}}</button><RequirementListExport v-if="activeTab==='工作项列表'" class="sprint-list-export" :items="loading?[]:listRows.filter(item=>item.objectType==='requirement')" :columns="visibleColumns.map(field=>field.key)" :project-id="pageProject"/><WorkItemColumns v-if="activeTab==='工作项列表'" ref="columnPanel" :fields="listFields" :model-value="listColumns" :saving="columnsSaving" :error="columnsError" @save="saveColumns"/><span class="sprint-filter-count" :title="t('显示 {shown} / {total} 项；筛选不影响迭代概览和权重总计。',{shown:listRows.length,total:selected.items.length})">{{listRows.length}} / {{selected.items.length}}</span></div></div>
</div><div class="iteration-content"><template v-if="activeTab==='概览'"><div class="sprint-weight-preview"><div><span>{{ t("需求总权重") }}</span><b>{{ selected.weightSummary ? formatNumber(selected.weightSummary.totalWeight, { maximumFractionDigits: 6 }) : '—' }}</b><small v-if="selected.weightSummary">{{ t("{count} 条需求 · {estimated} 条已估算", { count: selected.weightSummary.requirementCount, estimated: selected.weightSummary.estimatedCount }) }}</small></div><button class="btn compact" @click="activeTab='权重统计'">{{ t("查看权重统计") }} →</button></div><div class="metric-grid"><article><span>{{ t("总体完成率") }}</span><b>{{percent}}%</b><div class="progress"><i :style="{width:percent+'%'}"></i></div></article><article><span>{{ t("工作项") }}</span><b>{{metrics.done}}<small> / {{metrics.total}}</small></b><p>{{metrics.unfinished}} {{ t("项尚未完成") }}</p></article><article><span>{{ t("工时消耗") }}</span><b>{{metrics.actualHours}}<small>h / {{metrics.estimatedHours}}h</small></b><p>{{ t("负载") }} {{metrics.estimatedHours?Math.round(metrics.actualHours/metrics.estimatedHours*100):0}}%</p></article><article><span>{{ t("迭代容量") }}</span><b>{{selected.sprint.capacity||0}}<small>h</small></b></article></div><div class="overview-grid"><article class="panel"><h3>{{ t("状态分布") }}</h3><div v-for="x in statusStats" class="stat-row"><span>{{x.name}}</span><div><i :style="{width:x.pct+'%',backgroundColor:x.color}"></i></div><b>{{x.value}}</b></div><div v-if="!statusStats.length" class="empty-mini">{{ t("暂无工作项") }}</div></article><article class="panel"><h3>{{ t("职能交付") }}</h3><div v-for="x in disciplineStats" class="discipline-row"><span class="discipline-icon">{{t(x.name).slice(0,1)}}</span><div><b>{{t(x.name)}}</b><small>{{x.done}} / {{x.total}} {{ t("完成 ·") }} {{x.actual}}h / {{x.estimated}}h</small></div><strong>{{x.total?Math.round(x.done/x.total*100):0}}%</strong></div></article></div></template>
<template v-else-if="activeTab==='工作项列表'">
 <p v-if="columnsError" class="field-error" role="alert">{{t(columnsError)}} <button class="link" @click="loadColumns">{{t('重试')}}</button></p>
 <div class="card table-card sprint-table configurable-sprint-table"><table :style="{width:listWidth+'px',tableLayout:'fixed'}"><colgroup><col v-for="field in visibleColumns" :key="field.key" :style="{width:sprintColumnWidth(field)+'px'}"></colgroup><thead><tr><th v-for="field in visibleColumns" :key="field.key" :aria-sort="listSort===field.key?(listOrder==='asc'?'ascending':'descending'):'none'"><button class="column-sort" :aria-label="t('按 {name} 排序',{name:listFieldLabel(field)})" @click="sortColumn(field.key)">{{listFieldLabel(field)}} <span>{{listSort===field.key?(listOrder==='asc'?'↑':'↓'):'↕'}}</span></button></th></tr></thead><tbody><tr v-for="x in pagedRows" :key="x.objectType+'-'+x.id" :class="{'released-requirement-row':isReleasedRequirement(x)}"><td v-for="field in visibleColumns" :key="field.key" :title="displayCell(x,field)"><RequirementCode v-if="field.key==='code'&&x.objectType==='requirement'" class="code" :requirement="x" :members="members" :project-id="pageProject" @open="openRequirement(x)"/><router-link v-else-if="field.key==='code'" class="code" :to="workItemLink(x)">{{x.code}}</router-link><button v-else-if="field.key==='title'&&x.objectType==='requirement'" type="button" class="work-title requirement-title" @click="openRequirement(x)"><b>{{x.title}}</b></button><router-link v-else-if="field.key==='title'" class="work-title" :to="workItemLink(x)"><b>{{x.title}}</b></router-link><span v-else-if="field.key==='status'" :class="['status','workflow-color',x.status]" :style="workflowStyle(x,x.objectType==='defect'?[]:statusDefinitions)">{{displayCell(x,field)}}</span><div v-else-if="field.kind==='person'" class="sprint-assignees"><span v-for="(name,index) in workItemPersonNames(x,field.key,members)" :key="index">{{name}}</span><small v-if="!workItemPersonNames(x,field.key,members).length">—</small></div><div v-else-if="field.key==='tags'" class="sprint-assignees"><span v-for="tag in splitTags(x.tags)" :key="tag" :style="tagStyle(x.tagColors?.[tag])">{{tag}}</span></div><template v-else-if="field.key==='progress'"><input class="inline-number" :aria-label="t('进度')" type="number" min="0" max="100" :disabled="!canWrite||saving" :value="x.progress" @change="patchItem(x,{progress:Number(($event.target as HTMLInputElement).value)},$event)">%</template><template v-else-if="field.key==='actualHours'"><input class="inline-number" :aria-label="t('实际工时')" type="number" min="0" :disabled="!canWrite||saving" :value="x.actualHours" @change="patchItem(x,{actualHours:Number(($event.target as HTMLInputElement).value)},$event)">h</template><span v-else>{{displayCell(x,field)}}</span></td></tr><tr v-if="!listRows.length"><td :colspan="visibleColumns.length"><div class="empty-mini">{{selected.items.length?t('没有符合筛选条件的工作项'):selected.backlog?t('待规划池暂无工作项。可创建需求或缺陷，稍后再安排迭代。'):t('当前迭代暂无工作项。可在需求池编辑需求的迭代归属。')}}</div></td></tr></tbody></table></div>
 <div class="pagination"><span>{{t('共 {count} 条 · 每页 {size} 条',{count:listRows.length,size:30})}}</span><button :disabled="listPage<=1" :aria-label="t('上一页')" @click="listPage--">‹</button><b>{{listPage}} / {{listPages}}</b><button :disabled="listPage>=listPages" :aria-label="t('下一页')" @click="listPage++">›</button></div>
</template>
<template v-else-if="activeTab==='权重统计'"><SprintWeightPanel  :project-id="pageProject" :members="members" @open-requirement="openRequirement" :summary="selected.weightSummary" :backlog="!!selected.backlog" :loading="loading" :error="error" @refresh="load()" /></template>
<template v-else-if="activeTab==='看板'"><div class="board iteration-board"><section v-for="(xs,k) in board" class="board-col"><h4 :style="{borderTop:'3px solid '+({待处理:'#64748B',进行中:'#2563EB',验证中:'#7C3AED',已完成:'#059669',已终止:'#DC2626'} as any)[k]}">{{t(k)}} <small>{{xs.length}}</small></h4><article v-for="x in xs" :key="x.objectType+'-'+x.id" class="work-card"><span><RequirementCode v-if="x.objectType==='requirement'" :requirement="x" :members="members" :project-id="pageProject" @open="openRequirement(x)"/><span v-else>{{x.code}}</span> · {{t(disciplineNames[x.discipline]||x.discipline)}}</span><button v-if="x.objectType==='requirement'" type="button" class="work-title requirement-title" @click="openRequirement(x)"><b>{{x.title}}</b></button><router-link v-else class="work-title" :to="workItemLink(x)"><b>{{x.title}}</b></router-link><span class="status workflow-color" :style="workflowStyle(x,x.objectType==='defect'?[]:statusDefinitions)">{{statusLabel(x,x.objectType==='defect'?[]:statusDefinitions,t)}}</span><div class="work-progress"><i :style="{width:(x.progress||0)+'%'}"></i></div><footer><span :class="['priority',x.priority]">{{x.priority}}</span><div class="sprint-assignees"><span v-for="person in sprintParticipants(x,members)" :key="person.id" :title="person.id">{{person.name}}</span><small v-if="!sprintParticipants(x,members).length">{{t("待指派")}}</small><small>{{x.actualHours}}/{{x.estimatedHours}}h</small></div></footer></article><p v-if="!xs.length" class="empty-mini">{{ t("暂无工作项") }}</p></section></div></template>
<template v-else-if="activeTab==='团队跟踪'"><p class="participation-note">{{t("多人需求分别展示在各参与者名下；迭代数量与总权重仍按工作项计一次。")}}<br>{{t("卡片工时为参与工作项工时，不是个人实际投入或平均分摊。")}}</p><div class="team-grid"><article v-for="x in team" :key="x.id" class="team-card"><header><span class="avatar">{{(x.unassigned ? t("待指派") : x.name).slice(0,1)}}</span><div><b :title="x.unassigned ? undefined : x.id">{{x.unassigned ? t("待指派") : x.name}}</b><small>{{t(roleNames[x.role]||disciplineNames[x.role]||x.role)}}</small></div><strong>{{x.progress}}%</strong></header><div class="progress"><i :style="{width:x.progress+'%'}"></i></div><footer><span>{{x.done}} / {{x.total}} {{ t("项完成") }}</span><span :title="t('参与工作项工时')">{{x.actual}}h / {{x.estimated}}h</span></footer></article><div v-if="!team.length" class="empty-mini">{{ t("暂无已指派工作项") }}</div></div></template>
<template v-else-if="activeTab==='进度图'"><div class="panel progress-panel"><h3>{{ t("工作项进度") }}</h3><p>{{ t("按职能汇总当前工作项的实时完成与工时数据。") }}</p><div class="chart-area"><div v-for="x in disciplineStats" class="chart-column"><div class="chart-bars"><i class="planned" :style="{height:(x.total?Math.max(6,x.done/x.total*100):0)+'%'}"></i><i class="actual" :style="{height:(x.estimated?Math.min(100,x.actual/x.estimated*100):0)+'%'}"></i></div><b>{{t(x.name)}}</b><small>{{ t("完成") }} {{x.done}}/{{x.total}}</small></div></div><div class="chart-legend"><span><i class="planned"></i>{{ t("完成率") }}</span><span><i class="actual"></i>{{ t("工时消耗率") }}</span></div></div></template>
<template v-else><div class="dashboard-grid"><article class="panel hero-metric"><span>{{ t("迭代健康度") }}</span><b>{{Math.max(0,Math.min(100,Math.round(percent-(metrics.estimatedHours&&metrics.actualHours>metrics.estimatedHours?(metrics.actualHours/metrics.estimatedHours-1)*30:0))))}}</b><small>{{ t("由完成率与工时偏差实时计算") }}</small></article><article class="panel"><h3>{{ t("交付风险") }}</h3><div class="risk-line"><span>{{ t("未完成工作项") }}</span><b>{{metrics.unfinished}}</b></div><div class="risk-line"><span>{{ t("超出预估工时") }}</span><b :class="{danger:metrics.actualHours>metrics.estimatedHours}">{{Math.max(0,metrics.actualHours-metrics.estimatedHours)}}h</b></div><div class="risk-line"><span>{{ t("未指派事项") }}</span><b>{{selected.items.filter((x:any)=>!sprintParticipants(x,members).length).length}}</b></div></article><article class="panel dashboard-wide"><h3>{{ t("职能负载") }}</h3><div v-for="x in disciplineStats" class="load-row"><b>{{t(x.name)}}</b><div><i :style="{width:(x.estimated?Math.min(100,x.actual/x.estimated*100):0)+'%'}"></i></div><span>{{x.actual}} / {{x.estimated}}h</span></div></article></div></template></div></section><div v-else class="state"><template v-if="loading"><span class="spinner"></span>{{ t("正在载入迭代…") }}</template><template v-else-if="error"><b>{{ t("迭代加载失败") }}</b><p role="alert">{{ t(error) }}</p><button class="btn" @click="load()">{{ t("重新加载") }}</button><button class="btn" @click="openBacklog()">{{ t("查看待规划") }}</button></template><template v-else><b>{{ t("暂无迭代") }}</b><p>{{ t("先在待规划池整理需求，再创建迭代安排交付。") }}</p><button class="btn" @click="openBacklog()">{{ t("查看待规划") }}</button></template></div>

<div v-if="showSprint" class="modal-shade" @click.self="!saving&&(showSprint=false)"><div class="modal"><header><h2>{{ t("创建迭代") }}</h2><button :disabled="saving" @click="showSprint=false" :aria-label="t('关闭')">×</button></header><div class="modal-body form-grid"><label>{{ t("迭代名称 *") }}</label><input :aria-label="t('迭代名称 *')" v-model="sprintForm.name" @input="sprintCustomized.name=true"><label>{{ t("迭代目标") }}</label><textarea :aria-label="t('迭代目标')" v-model="sprintForm.goal"></textarea><label>{{ t("开始日期 *") }}</label><DatePicker :label="t('开始日期 *')" :model-value="sprintForm.startDate" required :disabled="saving" @update:model-value="changeSprintStart" @validity-change="sprintDateValidity.start=$event" /><label>{{ t("结束日期 *") }}</label><div><DatePicker :label="t('结束日期 *')" v-model="sprintForm.endDate" required :min="sprintForm.startDate" :disabled="saving" @input="sprintCustomized.endDate=true" @change="sprintCustomized.endDate=true" @validity-change="sprintDateValidity.end=$event" /><p class="hint">{{t('本周五至下周四，名称和结束日期可手动调整')}}</p></div><label>{{ t("容量（小时）") }}</label><input :aria-label="t('容量（小时）')" type="number" min="0" v-model="sprintForm.capacity"><p v-if="error" class="field-error">{{ t(error) }}</p></div><footer><button class="btn" :disabled="saving" @click="showSprint=false">{{ t("取消") }}</button><button class="btn primary" :disabled="saving" @click="createSprint">{{saving?t("创建中…"):t("创建迭代")}}</button></footer></div></div>
<div v-if="showWork" class="modal-shade" @click.self="showWork=false"><div class="modal large"><header><div><span class="eyebrow">{{workForm.sprint === '待规划' ? t("待规划") : workForm.sprint}}</span><h2>{{ t("创建工作项") }}</h2></div><button @click="showWork=false" :aria-label="t('关闭')">×</button></header><div class="modal-body work-form"><section><label>{{ t("工作项类型") }}</label><div class="type-choice"><button :class="{active:workForm.objectType==='requirement'}" @click="workForm.objectType='requirement';workForm.discipline='product'">{{ t("需求") }}</button><button :class="{active:workForm.objectType==='defect'}" @click="workForm.objectType='defect';workForm.discipline='backend'">{{ t("缺陷") }}</button></div><label>{{ t("标题 *") }}</label><input :aria-label="t('标题 *')" v-model="workForm.title" :placeholder="t('一句话说明要交付的结果')"><label>{{ t("描述") }}</label><textarea :aria-label="t('描述')" v-model="workForm.description" :placeholder="t('补充背景、范围和验收说明')"></textarea></section><section class="form-grid"><label>{{ t("优先级") }}</label><select :aria-label="t('优先级')" v-model="workForm.priority"><option value="P0">P0</option><option value="P1">P1</option><option value="P2">P2</option><option value="P3">P3</option></select><label :for="workForm.objectType==='requirement'?'sprint-work-assignees':'sprint-work-defect-owner'">{{t(workForm.objectType==='requirement'?'处理人（多选）':'缺陷负责人（单选）')}}</label><MemberMultiSelect v-if="workForm.objectType==='requirement'" v-model="workForm.assigneeUserIds" :members="members" :disabled="saving" input-id="sprint-work-assignees" :label="t('处理人（多选）')"/><MemberMultiSelect v-else single input-id="sprint-work-defect-owner" :label="t('缺陷负责人（单选）')" :model-value="workForm.assigneeUserId?[workForm.assigneeUserId]:[]" :members="members" :show-lead="false" :disabled="saving" @update:model-value="workForm.assigneeUserId=$event[0]||'';workForm.assignee=members.find(x=>x.id===$event[0])?.name||''" /><label>{{ t("职能") }}</label><select :aria-label="t('职能')" v-model="workForm.discipline"><option v-for="(label,key) in disciplineNames" :value="key">{{t(label)}}</option></select><label>{{ t("初始进度") }}</label><input :aria-label="t('初始进度')" type="number" min="0" max="100" v-model="workForm.progress"><label>{{ t("预估工时") }}</label><input :aria-label="t('预估工时')" type="number" min="0" v-model="workForm.estimatedHours"><label>{{ t("实际工时") }}</label><input :aria-label="t('实际工时')" type="number" min="0" v-model="workForm.actualHours"></section><p v-if="error" class="field-error">{{ t(error) }}</p></div><footer><button class="btn" @click="showWork=false">{{ t("取消") }}</button><button class="btn primary" :disabled="saving" @click="createWork">{{ t("创建并加入迭代") }}</button></footer></div></div>
<div v-if="showComplete" class="modal-shade" @click.self="!saving&&(showComplete=false)"><div class="modal complete-modal"><header><h2>{{ t("完成迭代") }}</h2><button :disabled="saving" @click="showComplete=false" :aria-label="t('关闭')">×</button></header><div class="modal-body"><div class="completion-summary"><b>{{metrics.unfinished}}</b><span>{{ t("个未完成工作项需要迁移") }}</span></div><label>{{ t("迁移目标") }}</label><select :aria-label="t('迁移目标')" v-model="completionTarget"><option value="待规划">{{ t("待规划") }}</option><option v-for="x in sprints.filter(x=>x.id!==selected.sprint.id&&['规划中','进行中'].includes(x.status))" :value="x.name">{{x.name}}</option></select><p class="hint">{{ t("完成操作会在同一事务中迁移未完成需求和缺陷，并写入迭代活动与审计日志。") }}</p><p v-if="error" class="field-error">{{ t(error) }}</p></div><footer><button class="btn" :disabled="saving" @click="showComplete=false">{{ t("暂不完成") }}</button><button class="btn primary" :disabled="saving" @click="complete">{{saving?t("处理中…"):t("确认完成")}}</button></footer></div></div></div><Requirements v-if="route.query.req" detail-only @updated="refreshRequirement"/></template>
<style scoped>
.sprint-shared-tools{padding:10px 24px 0}.sprint-status-toolbar{display:flex;align-items:center;gap:10px;flex-wrap:wrap;padding:0 0 8px}.sprint-status-toolbar>span{font-size:11px;color:var(--muted)}.iteration-board{display:flex!important;overflow:auto;align-items:stretch}.iteration-board>.board-col{flex:0 0 250px;min-width:0}.iteration-board h4{padding-top:10px}.work-card>.workflow-color{display:inline-block;font-size:11px;margin:10px 0 0}
.create-sprint-button{display:inline-flex;align-items:center;justify-content:center;gap:5px;min-height:34px;padding:7px 10px;border:1px solid color-mix(in srgb,var(--primary) 28%,var(--line));border-radius:8px;background:var(--primary-soft);color:var(--primary);font-size:12px;font-weight:650;white-space:nowrap;transition:background-color .15s ease,border-color .15s ease,transform .15s ease}.create-sprint-button>span{font-size:17px;line-height:12px;font-weight:400}.create-sprint-button:hover:not(:disabled){border-color:var(--primary);background:color-mix(in srgb,var(--primary) 15%,var(--card));transform:translateY(-1px)}.create-sprint-button:disabled{cursor:not-allowed;opacity:.55}
.iteration-nav{position:relative;transition:width .16s ease,flex-basis .16s ease,padding .16s ease}.iteration-nav>:deep(.sidebar-collapse-toggle){position:absolute;right:10px;top:10px;z-index:2}.iteration-nav.is-collapsed{width:50px;min-width:50px;flex-basis:50px;padding:10px}.iteration-nav.is-collapsed>:deep(.sidebar-collapse-toggle){position:static;margin:auto}.iteration-nav-content{display:flex;flex-direction:column;min-height:100%}

.configurable-sprint-toolbar{justify-content:space-between;flex-wrap:wrap;gap:8px;overflow:visible;margin-bottom:10px;padding:8px 10px;border:1px solid var(--border,var(--line));border-radius:8px;background:var(--secondary,var(--surface-subtle))}.sprint-toolbar-primary,.sprint-toolbar-actions{display:flex;align-items:center;gap:8px;min-width:0;flex-wrap:wrap}.sprint-toolbar-primary{flex:1 1 460px}.sprint-toolbar-primary>b{font-size:14px;color:var(--foreground,var(--ink));white-space:nowrap}.sprint-toolbar-primary>input{flex:1 1 220px;min-width:180px;max-width:420px;padding:8px 10px;font-size:12px}.sprint-toolbar-actions{flex:0 1 auto;margin-left:auto}.configurable-sprint-toolbar .btn{margin-left:0}.configurable-sprint-toolbar :deep(.sprint-sort-select){min-width:128px;max-width:172px}.sprint-list-export{flex:none}.configurable-sprint-table{max-width:100%;overflow-x:auto}.configurable-sprint-table td{overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.configurable-sprint-table .sprint-assignees{max-width:none;white-space:normal}.column-sort{border:0;background:transparent;padding:0;text-align:left;color:inherit;font:inherit;cursor:pointer;display:flex;align-items:center;gap:7px;width:100%}.column-sort>span{color:#b1b7c5}.column-sort:hover{color:#625de0}
.sprint-assignees{display:flex;flex-wrap:wrap;align-items:center;gap:4px;max-width:240px}.sprint-assignees>span{padding:3px 6px;border-radius:4px;background:#f1f0fa;color:#696389;font-size:11px;white-space:nowrap}.sprint-assignees>small{font-size:10px;color:#949bad}.participation-note{font-size:12px;line-height:1.85;color:#858da0;padding:13px 16px;background:#f7f8fc;border:1px solid #e6e9f2;border-radius:8px;margin:0 0 18px}

.sprint-weight-preview{display:flex;align-items:center;justify-content:space-between;gap:16px;margin-bottom:18px;padding:17px 20px;border:1px solid #dedbf9;border-radius:10px;background:linear-gradient(110deg,#f6f5ff,#fff)}.sprint-weight-preview>div{display:flex;align-items:baseline;gap:15px;flex-wrap:wrap}.sprint-weight-preview span{font-size:12px;color:#726e91}.sprint-weight-preview b{font-size:25px;font-variant-numeric:tabular-nums;color:#6660d8}.sprint-weight-preview small{font-size:11px;color:#9490a8}

.work-title{color:inherit;text-decoration:none}.work-title:hover{color:var(--brand,#5B5CE2);text-decoration:underline}
.requirement-title{font:inherit;text-align:left;padding:0;background:none;border:0;cursor:pointer;max-width:100%}.work-card>.requirement-title{display:block;margin:10px 0}
.configurable-sprint-table .released-requirement-row{--sprint-released-bg:color-mix(in srgb,var(--surface) 88%,var(--muted));--sprint-released-ink:color-mix(in srgb,var(--ink) 55%,var(--muted))}
.configurable-sprint-table .released-requirement-row>td{background:var(--sprint-released-bg);color:var(--sprint-released-ink)}
.configurable-sprint-table .released-requirement-row .code{color:var(--sprint-released-ink)!important}
.configurable-sprint-table .released-requirement-row:hover>td,.configurable-sprint-table .released-requirement-row:focus-within>td{background:color-mix(in srgb,var(--sprint-released-bg) 92%,var(--primary))}
.configurable-sprint-table .released-requirement-row .work-title:is(:hover,:focus-visible){color:var(--primary)}
.configurable-sprint-table .released-requirement-row .requirement-title:focus-visible{outline:2px solid var(--primary);outline-offset:2px;border-radius:2px}
@media(max-width:820px){
 .iteration-shell{flex-direction:column;height:auto;min-height:100%;min-width:0}
 .iteration-nav{width:100%;max-height:230px;flex:none;border-right:0;border-bottom:1px solid var(--line);padding:12px;overflow:auto}
 .iteration-nav.is-collapsed{width:100%;min-width:0;max-height:none;padding:9px}.iteration-nav.is-collapsed>:deep(.sidebar-collapse-toggle){margin-left:auto}
 .iteration-card{display:inline-block;width:190px;vertical-align:top;margin:4px}
 .iteration-main{width:100%;min-width:0;overflow:visible}
 .iteration-head{flex-wrap:wrap;gap:14px;padding:16px;align-items:flex-start}
 .iteration-head>div{min-width:0;max-width:100%}.iteration-head h1,.iteration-head p{overflow-wrap:anywhere}
 .iteration-actions{flex-wrap:wrap;gap:8px}
 .sprint-shared-tools{padding:12px 16px 0}.configurable-sprint-toolbar{gap:8px;padding:10px}.sprint-toolbar-primary,.sprint-toolbar-actions{width:100%;margin-left:0}
 .sprint-toolbar-primary>input{flex:1 1 100%;width:100%;max-width:100%;min-width:0}
 .configurable-sprint-toolbar :deep(.sprint-sort-select){min-width:0;flex:1}
 .iteration-content{padding:16px;overflow:visible}
 .metric-grid{grid-template-columns:repeat(2,minmax(0,1fr));gap:9px}.overview-grid{grid-template-columns:minmax(0,1fr)}
 .sprint-weight-preview{flex-wrap:wrap;padding:14px}.sprint-weight-preview>div{min-width:0}
 .iteration-board{scroll-snap-type:x proximity}.iteration-board>.board-col{flex-basis:min(300px,calc(100vw - 60px));scroll-snap-align:start}
 .configurable-sprint-table{max-width:100%;overflow:auto}.sprint-assignees{max-width:100%}
 .iteration-content .pagination{flex-wrap:wrap;height:auto;min-height:50px}
}
</style>

<style scoped>
/* 迭代模块与需求池共享相同的表面、卡片与状态层级，避免深色皮肤留下固定白底。 */
.iteration-shell{background:var(--background);color:var(--foreground)}
.iteration-nav{background:var(--secondary);border-color:var(--border)}
.iteration-head,.iteration-tabs{background:var(--card);border-color:var(--border)}
.iteration-head :is(h1,h2){color:var(--foreground)}.iteration-head p,.iteration-kicker{color:var(--muted-foreground)}
.sprint-shared-tools{background:var(--secondary);border-top:1px solid var(--border);border-bottom:1px solid var(--border)}
.configurable-sprint-toolbar :is(input,select){background:var(--card);border-color:var(--input);color:var(--foreground)}
.configurable-sprint-table{background:var(--card);border-color:var(--border);box-shadow:none}
.configurable-sprint-table table{background:var(--card);color:var(--foreground)}
.configurable-sprint-table th{background:var(--secondary);border-color:var(--border);color:var(--muted-foreground);font-weight:600}
.configurable-sprint-table td{background:var(--card);border-color:color-mix(in srgb,var(--border) 78%,var(--background));color:var(--foreground)}
.configurable-sprint-table tbody tr:not(.released-requirement-row):hover td{background:color-mix(in srgb,var(--primary) 7%,var(--background))}
.sprint-assignees>span{background:var(--accent);border:1px solid color-mix(in srgb,var(--primary) 16%,var(--border));color:var(--accent-foreground)}

.sprint-weight-preview,.participation-note{background:var(--secondary);border-color:var(--border);color:var(--foreground)}
.sprint-weight-preview :is(span,small),.participation-note{color:var(--muted-foreground)}.sprint-weight-preview b{color:var(--primary)}
.iteration-board{gap:16px;padding:8px 2px 20px;color:var(--foreground)}
.iteration-board .board-col{background:var(--secondary);border:1px solid var(--border);border-radius:var(--radius);box-shadow:none}
.iteration-board h4{color:var(--foreground);border-bottom:1px solid color-mix(in srgb,var(--border) 78%,var(--background));padding-bottom:10px}
.iteration-board .work-card{background:var(--card);border-color:var(--border);color:var(--foreground);border-radius:calc(var(--radius) - 2px);box-shadow:none}
.iteration-board .work-card:is(:hover,:focus-within){border-color:var(--primary);background:var(--card);box-shadow:none}
.iteration-board .work-card>span:first-child,.iteration-board .work-card footer{color:var(--muted-foreground)}
.iteration-board .work-progress,.iteration-main .progress{background:color-mix(in srgb,var(--primary) 10%,var(--secondary))}.iteration-board .work-progress i,.iteration-main .progress i{background:var(--primary)}
.work-title{color:var(--foreground)}.work-title:is(:hover,:focus-visible){color:var(--primary)}
@media(max-width:820px){.iteration-board{gap:12px;padding-bottom:16px}.iteration-board .work-card{padding:12px}}
</style>

<script setup lang="ts">
import { t, categoryLabel, formatDate as formatCreatedAt } from '../i18n'
import { computed, nextTick, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { onBeforeRouteLeave, onBeforeRouteUpdate, useRoute, useRouter } from 'vue-router'
import { api } from '../api'
import TapdImport from '../components/TapdImport.vue'
import Icon from '../components/Icon.vue'
import CustomFieldInputs from '../components/CustomFieldInputs.vue'
import RequirementWeights from '../components/RequirementWeights.vue'
import RequirementTags from '../components/RequirementTags.vue'
import MentionComment from '../components/MentionComment.vue'
import CommentReplyContext from '../components/CommentReplyContext.vue'
import type { CommentReplyTarget } from '../commentReplies'
import MemberMultiSelect from '../components/MemberMultiSelect.vue'
import WorkItemFilters from '../components/WorkItemFilters.vue'
import WorkItemColumns from '../components/WorkItemColumns.vue'
import RequirementSavedViews from '../components/RequirementSavedViews.vue'
import type { RequirementViewConfig, SavedViewFilter } from '../savedViews'
import { workItemFields, workItemPersonNames, workOperators, workFilterValue, type WorkFilter } from '../workItemQuery'
import RequirementResources from '../components/RequirementResources.vue'
import RequirementDetailPreferences from '../components/RequirementDetailPreferences.vue'
import ResizableDrawer from '../components/ResizableDrawer.vue'
import ResizableSplit from '../components/ResizableSplit.vue'
import Editor from './Editor.vue'
import RichTextEditor from '../components/RichTextEditor.vue'
import RequirementFavorite from '../components/RequirementFavorite.vue'
import RequirementCode from '../components/RequirementCode.vue'
import RequirementBulkActions from '../components/RequirementBulkActions.vue'
import RequirementAITestCases from '../components/RequirementAITestCases.vue'
import RequirementTestCaseCoverage from '../components/RequirementTestCaseCoverage.vue'
import RequirementLinks from '../components/RequirementLinks.vue'
import RequirementDependencies from '../components/RequirementDependencies.vue'
import RequirementListExport from '../components/RequirementListExport.vue'
import WorkItemPdfExport from '../components/WorkItemPdfExport.vue'
import DefectComposer from '../components/DefectComposer.vue'
import { useLayoutBoolean } from '../layoutScope'
import SidebarCollapseButton from '../components/SidebarCollapseButton.vue'
import StatusMultiSelect from '../components/StatusMultiSelect.vue'
import AppSelect from '../components/AppSelect.vue'
import RequirementTransition from '../components/RequirementTransition.vue'
import { stateInfo, statusLabel, statusOptionLabel, workflowOptions, workflowStyle, type RequirementState } from '../requirementWorkflow'
import { normalizeMentionIds, retainMentionIds, unavailableNewMentionIds } from '../mentions'
import { normalizeRoleWeights, splitTags, tagStyle } from '../requirementFields'
import { requirementPage, requirementExportPages } from '../requirementPaging'

type Column = { key:string; label:string; group:string; width?:number; fixed?:boolean; default?:boolean }
const props=defineProps<{detailOnly?:boolean}>()
const emit=defineEmits<{(event:'updated',requirement:any):void}>()
const route=useRoute(), router=useRouter()
const items=ref<any[]>([]), fieldDefs=ref<any[]>([]), sprints=ref<any[]>([]), members=ref<any[]>([])
const checkedIds=ref<number[]>([]),bulkBusy=ref(false),aiBusy=ref(false),aiDirty=ref(false)
const aiTests=ref<{canLeave:()=>boolean|Promise<boolean>}|null>(null)
const checkedItems=computed(()=>items.value.filter(item=>checkedIds.value.includes(item.id)))
const allPageChecked=computed(()=>paged.value.length>0&&paged.value.every(item=>checkedIds.value.includes(item.id)))
function togglePage(checked:boolean){checkedIds.value=checked?[...new Set([...checkedIds.value,...paged.value.map(item=>item.id)])]:checkedIds.value.filter(id=>!paged.value.some(item=>item.id===id))}
function bulkChanged(){checkedIds.value=[];void load()}
watch(items,()=>{checkedIds.value=checkedIds.value.filter(id=>items.value.some(item=>item.id===id))})
const detailPreferences=ref<{basicFields:string[]|null;customFieldKeys:string[]|null}>({basicFields:null,customFieldKeys:null})
const detailFieldVisible=(key:string)=>detailPreferences.value.basicFields===null||detailPreferences.value.basicFields.includes(key)
const categoryRecords=ref<any[]>([]), categorySearch=ref(''), categoryMenu=ref<number|null>(null), canManageCategories=ref(false), categoryOrdering=ref(false), categoryOrderVersion=ref('')
const categoryModal=ref<'create'|'rename'|'delete'|null>(null), categoryTarget=ref<any>(null), categoryName=ref(''), categoryError=ref(''), categorySaving=ref(false)
const loading=ref(true), error=ref(''), metadataError=ref(''), toast=ref(''), session=ref<any>(null)
const toastParams=ref<Record<string,string|number>>({})
const initialAssigneeId=typeof route.query.assigneeUserId==='string'?route.query.assigneeUserId:''
const filters=reactive({mine:route.query.mine==='1'?'1':'',q:'',status:'',statusCategory:'',priority:'',assignee:initialAssigneeId?'':typeof route.query.assignee==='string'?route.query.assignee:'',assigneeUserId:initialAssigneeId,sprint:'',category:'',sort:'updatedAt',order:'desc'})
const cfFilters=reactive<Record<string,string>>({})
const advancedFilters=ref<WorkFilter[]>([])
let boundViewFilters:{signature:string;rules:SavedViewFilter[]}|null=null
const filterFields=computed(()=>workItemFields(fieldDefs.value,members.value).map(field=>field.key==='status'?{...field,options:statusFilterOptions.value.map(item=>({...item,label:statusOptionLabel(item,t)}))}:field))
const page=ref(1), size=ref(15), listTotal=ref(0), selected=ref<any>(null), detailTab=ref('详细信息'), detailLoading=ref(!!props.detailOnly&&!!route.query.req), saving=ref(false)
const pageSizeOptions=[{value:15,label:'15 条/页'},{value:30,label:'30 条/页'},{value:50,label:'50 条/页'}]
let lastListQuery=''
const comments=ref<any[]>([]), activities=ref<any[]>([]), checks=ref<any[]>([]), related=ref<any>({children:[],defects:[],cases:[]})
const testCaseCoverage=ref<{refresh:()=>Promise<void>;canLeave?:()=>boolean}|null>(null), testCaseTotal=ref<number|null>(null)
const parentSummary=ref<any>(null),parentLoading=ref(false),parentError=ref('')
let parentLoadVersion=0
const comment=ref(''), commentMentions=ref<string[]>([]), commentMentionNames=ref<Record<string,string>>({}), commentDoc=ref<any>(null), checkText=ref(''), detailError=ref(''), detailInputsVersion=ref(0)
const commentReply=ref<CommentReplyTarget|null>(null),commentEditor=ref<InstanceType<typeof RichTextEditor>|null>(null)
const commentMap=computed(()=>new Map<number,any>(comments.value.map(entry=>[entry.id,entry])))
const descriptionMediaBusy=ref(false),commentMediaBusy=ref(false),resourceVersion=ref(0)
const mediaBusy=computed(()=>descriptionMediaBusy.value||commentMediaBusy.value||aiBusy.value)
function documentHasContent(node:any):boolean{return !!node&&(typeof node.text==='string'&&!!node.text.trim()||['image','attachment','mention'].includes(node.type)||Array.isArray(node.content)&&node.content.some(documentHasContent))}
const commentHasContent=computed(()=>!!comment.value.trim()||documentHasContent(commentDoc.value))
const commentDraftDirty=computed(()=>commentHasContent.value||!!commentReply.value)
const detailDraft=reactive<any>({roleWeights:normalizeRoleWeights({}),tags:'',tagColors:{},remarks:'',remarksMentionUserIds:[],remarksMentionNames:{},customFields:{}})
const descriptionDraft=reactive({body:'',document:null as any,mentionUserIds:[] as string[],mentionNames:{} as Record<string,string>}), descriptionEditing=ref(false)
const assignmentDraft=ref<string[]>([])
const assignmentTouched=ref(false)
const ownerDraft=ref<string[]>([]), ownersTouched=ref(false), tagOptions=ref<any[]>([]), tagError=ref('')
const leavePrompt=ref(false), leaveSaving=ref(false), leaveError=ref('')
const resourceDraft=ref({url:'',title:'',opened:false})
const resourceDirty=computed(()=>!!(resourceDraft.value.url.trim()||resourceDraft.value.title.trim()))
const drawerWidth=ref(1320), childDrawerWidth=ref(1160), showProperties=useLayoutBoolean('requirements.detail.properties',true)
// 侧边栏属于个人阅读偏好，只保存在当前浏览器，不同步到项目或其他成员。
const requirementsSidebarExpanded=useLayoutBoolean('requirements.sidebar',true)
const collapsedRequirementParents=ref<number[]>([])
const showExtraFilters=ref(false)
const childParent=ref<{id:number;title:string}|null>(null)
const childEditor=ref<{requestClose:()=>Promise<boolean>|boolean;dirty:boolean;saving:boolean}|null>(null)
const childCreateButton=ref<HTMLButtonElement|null>(null)
const fullEditorID=ref<number|null>(null),fullEditor=ref<{requestClose:()=>Promise<boolean>|boolean;dirty:boolean;saving:boolean}|null>(null)
const fullEditorWidth=ref(1320)
const defectContext=ref<{id:number;title:string;sprint:string}|null>(null),defectComposer=ref<{requestClose:()=>Promise<boolean>;dirty:boolean;saving:boolean}|null>(null),requirementLinks=ref<{saving:boolean}|null>(null),requirementDependencies=ref<{saving:boolean}|null>(null),defectCreateButton=ref<HTMLButtonElement|null>(null)
let pendingLeave:((allowed:boolean)=>void)|null=null
const columns=ref<string[]>([]), columnsOpen=ref(false), columnsSaving=ref(false), columnsError=ref('')
const columnDraft=ref<{key:string;enabled:boolean}[]>([])
const columnPanel=ref<{close:()=>void;begin:()=>void}|null>(null)
let columnReadVersion=0
const statusDefinitions=ref<RequirementState[]>([]),selectedStatuses=ref<string[]>([])
const listView=ref<'list'|'board'>('list')
// 看板默认只保留编号和标题；展开状态只影响展示密度，不能改变已有的筛选、排序或详情路由。
const expandedBoardCardIds=ref<number[]>([])
const boardCardExpanded=(id:number)=>expandedBoardCardIds.value.includes(id)
function toggleBoardCard(id:number){expandedBoardCardIds.value=boardCardExpanded(id)?expandedBoardCardIds.value.filter(value=>value!==id):[...expandedBoardCardIds.value,id]}
const statusBoard=computed(()=>statusFilterOptions.value.filter(option=>!statusSelection.value.length||statusSelection.value.includes(option.value)).map(option=>({...option,items:items.value.filter(item=>item.status===option.value)})).filter(column=>column.items.length))
const statusFilterOptions=computed(()=>workflowOptions(statusDefinitions.value,[...items.value.map(x=>x.status),...selectedStatuses.value,filters.status]))
const statusSelection=computed({get:()=>filters.status?[filters.status]:filters.statusCategory==='done'?statusDefinitions.value.filter(item=>item.category==='done').map(item=>item.key):selectedStatuses.value,set:(values:string[])=>{filters.status='';filters.statusCategory='';selectedStatuses.value=values}})
const roles=[
 {key:'frontend',label:'前端开发难度',person:'前端工程师'}, {key:'backend',label:'后端难度',person:'后端工程师'},
 {key:'algorithm',label:'算法难度',person:'算法工程师'}, {key:'ui',label:'UI 难度',person:'UI 工程师'}, {key:'product',label:'产品难度',person:'产品'},
]
const baseColumns:Column[]=[
 {key:'code',label:'需求编号',group:'基础信息',fixed:true,width:112},
 {key:'title',label:'标题',group:'基础信息',fixed:true,width:320},
 {key:'type',label:'需求类型',group:'基础信息'}, {key:'category',label:'分类',group:'基础信息',width:180},
 {key:'sprint',label:'迭代',group:'基础信息',default:true,width:165},
 {key:'status',label:'状态',group:'基础信息',default:true}, {key:'priority',label:'优先级',group:'基础信息',default:true,width:86},
 {key:'owner',label:'产品负责人',group:'基础信息'}, {key:'assignee',label:'处理人',group:'基础信息',default:true},
 ...roles.flatMap(r=>[
  {key:'role.'+r.key+'.userId',label:r.person,group:'角色权重',width:125},
  {key:'role.'+r.key+'.value',label:r.label,group:'角色权重',width:130,default:true},
 ]),
 {key:'weightTotal',label:'总权重',group:'角色权重',default:true,width:100},
 {key:'tags',label:'标签',group:'补充信息',default:true,width:190}, {key:'remarks',label:'备注',group:'补充信息',width:240},
 {key:'createdAt',label:'创建时间',group:'时间',default:true,width:175}, {key:'updatedAt',label:'更新时间',group:'时间',width:175},
 {key:'startDate',label:'计划开始',group:'时间'}, {key:'endDate',label:'计划结束',group:'时间'},
 {key:'description',label:'需求描述',group:'补充信息',width:260}, {key:'acceptance',label:'验收标准',group:'补充信息',width:240},
 {key:'parentId',label:'父需求',group:'基础信息'}, {key:'discipline',label:'职能',group:'研发信息'},
 {key:'progress',label:'进度',group:'研发信息'}, {key:'estimatedHours',label:'预估工时',group:'研发信息'},
 {key:'actualHours',label:'实际工时',group:'研发信息'}, {key:'sensitive',label:'涉及敏感数据',group:'研发信息',width:145},
 {key:'authImpact',label:'涉及权限认证',group:'研发信息',width:145},
]
const catalog=computed<Column[]>(()=>[...baseColumns,...fieldDefs.value.map(d=>({key:'cf.'+d.key,label:d.name,group:'自定义字段',width:150,default:d.listVisible}))])
const defaults=computed(()=>catalog.value.filter(c=>c.fixed||c.default).map(c=>c.key))
const columnFields=computed(()=>catalog.value.map(column=>({...column,kind:filterFields.value.find(field=>field.key===column.key)?.kind||'text',custom:column.key.startsWith('cf.')})))
const visibleColumns=computed(()=>columns.value.map(key=>catalog.value.find(c=>c.key===key)).filter(Boolean) as Column[])
function columnLabel(column:Column|undefined){return !column?'':column.key.startsWith('cf.')?column.label:t(column.label)}
const tableWidth=computed(()=>visibleColumns.value.reduce((n,c)=>n+(c.width||115),canEdit.value?44:0))
const sortedItems=computed(()=>items.value)
// 后端筛选完成后再组成树，父项被筛掉时子项仍会作为顶层结果保留，避免搜索结果丢失。
const treeItems=computed(()=>{
 const source=paged.value, ids=new Set(source.map(item=>item.id)), children=new Map<number,any[]>()
 source.forEach(item=>{if(item.parentId&&ids.has(item.parentId))children.set(item.parentId,[...(children.get(item.parentId)||[]),item])})
 const visited=new Set<number>(), result:any[]=[]
 // 收起时仍遍历并标记后代，避免兜底循环把隐藏子项重新作为顶层需求展示。
 const append=(item:any,depth:number,visible=true)=>{if(visited.has(item.id))return;visited.add(item.id);const nested=children.get(item.id)||[],expanded=!collapsedRequirementParents.value.includes(item.id);if(visible)result.push({...item,_treeDepth:depth,_treeHasChildren:nested.length>0,_treeExpanded:expanded});nested.forEach(child=>append(child,depth+1,visible&&expanded))}
 source.filter(item=>!item.parentId||!ids.has(item.parentId)).forEach(item=>append(item,0))
 source.forEach(item=>append(item,0))
 return result
})
function toggleRequirementChildren(id:number){collapsedRequirementParents.value=collapsedRequirementParents.value.includes(id)?collapsedRequirementParents.value.filter(value=>value!==id):[...collapsedRequirementParents.value,id]}
function sortByColumn(key:string){if(filters.sort===key)filters.order=filters.order==='asc'?'desc':'asc';else{filters.sort=key;filters.order='asc'}}
const paged=computed(()=>sortedItems.value)
const pages=computed(()=>Math.max(1,Math.ceil(listTotal.value/size.value)))
function changePage(value:number){if(loading.value||bulkBusy.value)return;page.value=Math.max(1,Math.min(value,pages.value));void load()}
function changePageSize(){page.value=1;void load()}
function selectPageSize(value:string|number){const next=Number(value);if(![15,30,50].includes(next)||next===size.value)return;size.value=next;changePageSize()}
async function exportRecords(signal:AbortSignal,progress:(done:number,total:number)=>void){
 const query=lastListQuery,project=String(session.value?.project?.id||'')
 if(!project||loading.value||!listTotal.value)throw Error('请稍后重试')
 const params=new URLSearchParams(query),rules:SavedViewFilter[]=JSON.parse(params.get('filters')||'[]').map(captureFilterReference)
 const request=(path:string,signal:AbortSignal)=>api<any>(path,{signal,headers:{'X-DevFlow-Project':project}})
 // 保存视图的 neq 引用在导出前也重查，防止目录改名后扩大结果。
 if(rules.some(rule=>['category','sprint'].includes(rule.field)&&['eq','neq'].includes(rule.operator))){
  const [s,c]=await Promise.all([request('/sprints',signal),request('/requirement-categories',signal)])
  if(!Array.isArray(s.items)||!Array.isArray(c.items))throw Error('视图引用的迭代或分类已不可用，请更新视图；当前筛选未改变')
  params.set('filters',JSON.stringify(rules.map(rule=>resolveFilterReference(rule,s.items,c.items))))
 }
 return requirementExportPages(params.toString(),request,signal,progress)
}
const canEdit=computed(()=>!!session.value&&session.value.user?.role!=='viewer')
const canConfigure=computed(()=>['tenant_admin','project_admin'].includes(session.value?.user?.role))
const activeMembers=computed(()=>members.value.filter(m=>m.active!==false))
const assigneeFilterValue=computed({get:()=>filters.assigneeUserId||(filters.assignee?'__legacy_assignee__':''),set:(value:string)=>{filters.assignee='';filters.assigneeUserId=value}})
const assigneeFilterIds=computed({get:()=>assigneeFilterValue.value?[assigneeFilterValue.value]:[],set:(values:string[])=>{assigneeFilterValue.value=values[0]||''}})
const assigneeFilterSnapshots=computed(()=>filters.assignee?[{id:'__legacy_assignee__',name:t('旧链接按姓名筛选：{name}',{name:filters.assignee})}]:selectedHistoricalAssignee.value?[selectedHistoricalAssignee.value]:[])
const selectedHistoricalAssignee=computed(()=>filters.assigneeUserId&&!activeMembers.value.some(m=>m.id===filters.assigneeUserId)?members.value.find(m=>m.id===filters.assigneeUserId)||{id:filters.assigneeUserId,name:t('原筛选成员')}:null)
function assigneeOptionLabel(member:any){return members.value.filter(m=>m.name===member.name).length>1?member.name+' · '+(member.email||member.id):member.name}
function resolveLegacyAssignee(){
 if(!filters.assignee||filters.assigneeUserId)return
 const exactId=members.value.find(m=>m.id===filters.assignee),matches=members.value.filter(m=>m.name===filters.assignee)
 const match=exactId||(matches.length===1?matches[0]:null)
 if(match){filters.assigneeUserId=match.id;filters.assignee=''}
}
const categories=computed(()=>[...new Set([...categoryRecords.value.map(x=>x.name),selected.value?.category].filter(Boolean))])
const visibleCategories=computed(()=>categoryRecords.value.filter(x=>x.name.toLowerCase().includes(categorySearch.value.trim().toLowerCase())))
const totalRequirements=computed(()=>categoryRecords.value.reduce((total,x)=>total+Number(x.count||0),0))
const activeView=computed(()=>filters.category?'category':filters.mine==='1'?'mine':filters.status==='评审中'?'review':filters.statusCategory==='done'||statusSelection.value.length&&statusSelection.value.every(key=>stateInfo(key,statusDefinitions.value).category==='done')?'completed':'all')
const detailTabs=computed(()=>[{name:'详细信息',label:'需求描述',icon:'review'},{name:'子需求',label:'子需求',icon:'folder',count:related.value.children.length},{name:'测试用例',label:'测试用例',icon:'work',count:testCaseTotal.value??related.value.cases.length},{name:'缺陷',label:'缺陷',icon:'shield',count:related.value.defects.length},{name:'关联需求',label:'关联需求',icon:'link'},{name:'需求依赖',label:'需求依赖',icon:'link'},{name:'变更记录',label:'变更记录',icon:'list',count:activities.value.length}])
const tagTabs=computed(()=>[{name:'标签',icon:'tag',count:splitTags(detailDraft.tags).length},{name:'角色权重',icon:'weights',label:'权重',count:roles.filter(role=>detailDraft.roleWeights[role.key]?.value!==null&&detailDraft.roleWeights[role.key]?.value!==undefined).length}])
function selectDetailTab(name:string){if(name!==detailTab.value&&testCaseCoverage.value?.canLeave?.()===false)return;detailTab.value=name}
const assessmentDirty=computed(()=>selected.value&&(!detailCustomDatesValid.value||JSON.stringify(detailDraft)!==JSON.stringify(assessment(selected.value))))
const descriptionDirty=computed(()=>selected.value&&(descriptionDraft.body!==(selected.value.description||'')||JSON.stringify(descriptionDraft.document)!==JSON.stringify(selected.value.descriptionDoc??null)||JSON.stringify(descriptionDraft.mentionUserIds)!==JSON.stringify(normalizeMentionIds(selected.value.descriptionMentionUserIds))))
const assignmentDirty=computed(()=>selected.value&&(assignmentTouched.value||JSON.stringify(assignmentDraft.value)!==JSON.stringify(normalizeMentionIds(selected.value.assigneeUserIds))))
const ownerDirty=computed(()=>selected.value&&(ownersTouched.value||JSON.stringify(ownerDraft.value)!==JSON.stringify(normalizeMentionIds(selected.value.ownerUserIds??(selected.value.ownerUserId?[selected.value.ownerUserId]:[])))))
const detailDirty=computed(()=>selected.value&&(commentDraftDirty.value||mediaBusy.value||resourceDirty.value||assessmentDirty.value||descriptionDirty.value||assignmentDirty.value||ownerDirty.value))
const selectedSprintOptions=computed(()=>{
 const values=[{name:'待规划',status:''},...[...sprints.value].sort((a,b)=>Number(b.status==='进行中')-Number(a.status==='进行中'))]
 if(selected.value?.sprint&&!values.some(x=>x.name===selected.value.sprint))values.push({name:selected.value.sprint,status:'历史关联'})
 return values
})
let listVersion=0, detailVersion=0, relatedLoadVersion=0, detailDisposed=false, timer:ReturnType<typeof setTimeout>, toastTimer:ReturnType<typeof setTimeout>
function assessment(x:any){return{roleWeights:normalizeRoleWeights(x.roleWeights||{}),tags:x.tags||'',tagColors:{...(x.tagColors||{})},remarks:x.remarks||'',remarksMentionUserIds:normalizeMentionIds(x.remarksMentionUserIds),remarksMentionNames:{...(x.remarksMentionNames||{})},customFields:JSON.parse(JSON.stringify(x.customFields||{}))}}
const detailCustomDatesValid=ref(true)
function resetAssessment(){detailCustomDatesValid.value=true;if(selected.value)Object.assign(detailDraft,assessment(selected.value))}
function resetDescription(){if(selected.value)Object.assign(descriptionDraft,{body:selected.value.description||'',document:JSON.parse(JSON.stringify(selected.value.descriptionDoc??null)),mentionUserIds:normalizeMentionIds(selected.value.descriptionMentionUserIds),mentionNames:{...(selected.value.descriptionMentionNames||{})}})}
function resetAssignments(){assignmentDraft.value=normalizeMentionIds(selected.value?.assigneeUserIds);assignmentTouched.value=false}
function resetOwners(){ownerDraft.value=normalizeMentionIds(selected.value?.ownerUserIds??(selected.value?.ownerUserId?[selected.value.ownerUserId]:[]));ownersTouched.value=false}
async function saveOwners(){if(await patchFields({ownerUserIds:normalizeMentionIds(ownerDraft.value)}))resetOwners()}
async function loadTags(){try{const data=await api<any>('/requirement-tags');tagOptions.value=data.items||[];tagError.value=''}catch(cause:any){tagError.value=cause.message||'项目标签暂时无法载入，可重试或创建新标签'}}
function createChild(){if(canEdit.value&&selected.value&&!saving.value&&!mediaBusy.value&&!childParent.value)childParent.value={id:selected.value.id,title:selected.value.title}}
function createDefect(){if(canEdit.value&&selected.value&&!saving.value&&!mediaBusy.value&&!defectContext.value)defectContext.value={id:selected.value.id,title:selected.value.title,sprint:selected.value.sprint}}
async function defectCancelled(){defectContext.value=null;await nextTick();defectCreateButton.value?.focus({preventScroll:true})}
async function defectCreated(defect:any){
 const context=defectContext.value;if(!context||selected.value?.id!==context.id||Number(defect.requirementId)!==context.id)return
 if(!related.value.defects.some((item:any)=>item.id===defect.id))related.value.defects=[...related.value.defects,defect]
 await defectCancelled();show('缺陷已创建并关联当前需求');emit('updated',selected.value)
}
async function consumeChildEntry(){
 if(route.query.createChild!=='1'||!selected.value||Number(route.query.req)!==selected.value.id||detailLoading.value)return
 detailTab.value='子需求';createChild()
 const query={...route.query};delete query.createChild;await router.replace({query})
}
async function closeChild(){return childEditor.value?await childEditor.value.requestClose():false}
async function childCancelled(){childParent.value=null;await nextTick();childCreateButton.value?.focus()}
async function childCreated(requirement:any,again:boolean){
 const parent=childParent.value,version=detailVersion
 if(!parent)return
 emit('updated',requirement)
 if(selected.value?.id===parent.id&&Number(requirement.parentId)===parent.id&&!related.value.children.some((item:any)=>item.id===requirement.id))related.value.children=[...related.value.children,requirement]
 if(!again)await childCancelled()
 show('子需求已创建')
 // Refresh related records without re-opening the parent and discarding its drafts.
 const results=await Promise.allSettled([load(),loadCategories(),loadRelated(parent.id)])
 if(version===detailVersion&&selected.value?.id===parent.id&&results.some(result=>result.status==='rejected'))detailError.value='子需求已创建，部分关联信息刷新失败，请稍后重试'
}
const descriptionEditor=ref<InstanceType<typeof RichTextEditor>|null>(null)
// 双击与按钮共用入口；链接和附件仍保留原来的浏览行为。
async function beginDescriptionEdit(event?:MouseEvent){
 if(!canEdit.value||saving.value||mediaBusy.value||descriptionEditing.value||!selected.value)return
 if(event&&event.target instanceof Element&&event.target.closest('a,button,input,select,textarea'))return
 const id=selected.value.id
 descriptionEditing.value=true
 await nextTick()
 if(selected.value?.id===id&&descriptionEditing.value)descriptionEditor.value?.focus()
}
async function saveDescription(){
 if(mediaBusy.value)return
 const ids=retainMentionIds(descriptionDraft.body,descriptionDraft.mentionUserIds,members.value,descriptionDraft.mentionNames)
 if(unavailableNewMentionIds(ids,members.value,selected.value?.descriptionMentionUserIds).length){detailError.value='新增提及成员暂不可用，请刷新项目成员或移除后重选';return}
 const fields:any={description:descriptionDraft.body,descriptionMentionUserIds:ids}
 if(descriptionDraft.document!==null||selected.value?.descriptionDoc)fields.descriptionDoc=descriptionDraft.document
 const saved=await patchFields(fields)
 if(saved){resetDescription();descriptionEditing.value=false}
}
async function saveAssessment(){
 if(!detailCustomDatesValid.value){detailError.value='请先修正日期输入';return}
 const ids=retainMentionIds(detailDraft.remarks,detailDraft.remarksMentionUserIds,members.value,detailDraft.remarksMentionNames)
 if(unavailableNewMentionIds(ids,members.value,selected.value?.remarksMentionUserIds).length){detailError.value='新增提及成员暂不可用，请刷新项目成员或移除后重选';return}
 const {remarksMentionNames,...fields}=detailDraft
 await patchFields({...fields,remarksMentionUserIds:ids},true)
}
async function saveAssignments(){if(await patchFields({assigneeUserIds:normalizeMentionIds(assignmentDraft.value)}))resetAssignments()}
function displayValue(x:any,key:string):string{
 if(key.startsWith('role.')){
  const [,role,field]=key.split('.'),value=x.roleWeights?.[role]?.[field]
  return field==='userId'?workItemPersonNames(x,key,members.value).join('、')||t('未绑定'):value===null||value===undefined?'—':String(value)
 }
 const value=key.startsWith('cf.')?x.customFields?.[key.slice(3)]:x[key]
 if(['createdAt','updatedAt'].includes(key))return formatCreatedAt(value)
 if(key==='progress')return (value||0)+'%'
 if(key==='discipline')return t(({product:'产品',frontend:'前端',backend:'后端',algorithm:'算法',ui:'UI',qa:'测试'} as any)[value]||value||'—')
 if(key==='status')return statusLabel(x,statusDefinitions.value,t)||'—'
 if(key==='type')return t(value||'—')
 if(key==='sprint'&&value==='待规划')return t(value)
 if(key==='category')return categoryLabel(value||'未分类')
 if(key==='assignee')return x.assignees?.length?x.assignees.map((member:any)=>member.name).join('、'):x.assignee||'—'
 if(key==='owner')return workItemPersonNames(x,key,members.value).join('、')||'—'
 if(key==='parentId')return value?'REQ-'+String(value).padStart(4,'0'):'—'
 if(key.startsWith('cf.')&&['user','users'].includes(fieldDefs.value.find(d=>d.key===key.slice(3))?.type))return (Array.isArray(value)?value:[value]).filter(Boolean).map(id=>members.value.find(m=>m.id===id)?.name||id).join('、')||'—'
 if(typeof value==='boolean')return t(value?'是':'否')
 if(Array.isArray(value))return value.join('、')||'—'
 return value===null||value===undefined||value===''?'—':String(value)
}
async function load(){
 if(props.detailOnly)return
 const version=++listVersion; loading.value=true; error.value=''
 try{
  let queryFilters=advancedFilters.value
  if(boundViewFilters&&boundViewFilters.signature===JSON.stringify(advancedFilters.value)&&boundViewFilters.rules.some(rule=>['category','sprint'].includes(rule.field)&&['eq','neq'].includes(rule.operator))){
   const [s,c]=await Promise.all([api<any>('/sprints'),api<any>('/requirement-categories')])
   if(version!==listVersion||detailDisposed)return
   if(!Array.isArray(s.items)||!Array.isArray(c.items))throw Error('视图引用的迭代或分类已不可用，请更新视图；当前筛选未改变')
   try{queryFilters=boundViewFilters.rules.map(rule=>resolveFilterReference(rule,s.items,c.items))}catch(cause){items.value=[];throw cause}
  }
  const q=new URLSearchParams({...filters})
  if(selectedStatuses.value.length){q.delete('status');q.set('statuses',JSON.stringify(selectedStatuses.value))}
  if(queryFilters.length)q.set('filters',JSON.stringify(queryFilters))
  Object.entries(cfFilters).forEach(([key,value])=>{if(value)q.set('cf.'+key,value)})
  const snapshot=q.toString(),requestedSize=size.value
  q.set('page',String(page.value));q.set('pageSize',String(requestedSize));q.set('projection','list')
  const data=await api<any>('/requirements?'+q)
  if(version===listVersion){const result=requirementPage(data,requestedSize);items.value=result.items;listTotal.value=result.total;page.value=result.page;lastListQuery=snapshot}
 }catch(e:any){if(version===listVersion)error.value=e.message}
 finally{if(version===listVersion)loading.value=false}
}
function setCategoryDirectory(data:any){
 categoryRecords.value=Array.isArray(data?.items)?data.items:[]
 if(typeof data?.canManage==='boolean')canManageCategories.value=data.canManage
 // 版本号是服务端生成的指纹，必须原样回传，不能按数值处理或截断。
 categoryOrderVersion.value=typeof data?.orderVersion==='string'?data.orderVersion:''
}
async function loadOptions(){
 try{
  const [s,m,f,c,states]=await Promise.all([api<any>('/sprints'),api<any>('/members'),api<any>('/field-definitions?objectType=requirement'),api<any>('/requirement-categories'),api<any>('/requirement-statuses'),loadTags()])
  statusDefinitions.value=states.items||[]
  sprints.value=s.items||[];members.value=m.items||[];resolveLegacyAssignee();fieldDefs.value=(f.items||[]).filter((d:any)=>d.enabled);setCategoryDirectory(c);metadataError.value=''
 }catch(e:any){metadataError.value=e.message||'请稍后重试'}
}
async function loadCategories(){const data=await api<any>('/requirement-categories');setCategoryDirectory(data)}
function setView(view:string){
 let status=''
 if(view==='review'||view==='release'){const aliases=view==='review'?['评审中','待评审']:['待上线'];const definition=statusDefinitions.value.find(item=>item.enabled&&aliases.includes(item.key))||statusDefinitions.value.find(item=>item.enabled&&aliases.includes(item.name));if(!definition){show('当前项目没有此模板对应的可用状态，请使用状态筛选');return};status=definition.key}
 if(view==='mine'&&!session.value?.user?.id){show('账号信息尚未载入，请稍后重试');return}
 clearFilters();if(view==='mine')filters.mine='1';if(status)filters.status=status;if(view==='completed')filters.statusCategory='done'
}
function captureView():RequirementViewConfig{
 const myId=session.value?.user?.id,mode=filters.assigneeUserId?(filters.assigneeUserId===myId?'me':'member'):'any'
 const sprint=sprints.value.find(item=>item.name===filters.sprint),category=categoryRecords.value.find(item=>item.name===filters.category)
 return{schema:1,relatedToMe:filters.mine==='1',q:filters.q,statuses:filters.status?[filters.status]:[...selectedStatuses.value],statusCategory:filters.statusCategory,assigneeMode:mode,assigneeUserId:mode==='member'?filters.assigneeUserId:'',sprint:filters.sprint,...(sprint?.id?{sprintId:Number(sprint.id)}:{}),category:filters.category,...(category?.id?{categoryId:Number(category.id)}:{}),priority:filters.priority,filters:[...(boundViewFilters&&boundViewFilters.signature===JSON.stringify(advancedFilters.value)?boundViewFilters.rules.map(rule=>({...rule})):advancedFilters.value.map(captureFilterReference)),...Object.entries(cfFilters).filter(([,value])=>!!value).map(([key,value])=>({field:'cf.'+key,operator:'eq',value}))],columns:normalizeColumns(columns.value.length?columns.value:defaults.value),sort:filters.sort,order:filters.order==='asc'?'asc':'desc',view:listView.value}
}
function captureFilterReference(rule:WorkFilter):SavedViewFilter{
 const entry=['eq','neq'].includes(rule.operator)?(rule.field==='category'?categoryRecords.value:rule.field==='sprint'?sprints.value:[]).find(item=>item.name===rule.value):null
 return {...rule,...(entry?.id?{referenceId:Number(entry.id)}:{})}
}
function resolveFilterReference(rule:SavedViewFilter,currentSprints=sprints.value,currentCategories=categoryRecords.value):WorkFilter{
 const {referenceId,...filter}=rule
 if(!['category','sprint'].includes(rule.field)||!['eq','neq'].includes(rule.operator))return filter
 const entries=rule.field==='category'?currentCategories:currentSprints
 const entry=referenceId?entries.find(item=>Number(item.id)===referenceId):entries.find(item=>item.name===rule.value)
 if(entry)return {...filter,value:entry.name}
 // Unplanned is a reserved virtual iteration, not a directory record.
 if(!referenceId&&rule.field==='sprint'&&rule.value==='待规划')return filter
 throw Error('视图引用的迭代或分类已不可用，请更新视图；当前筛选未改变')
}
function applySavedView(config:RequirementViewConfig){
 if(loading.value||metadataError.value||bulkBusy.value)return
 const keys=new Set(catalog.value.map(column=>column.key))
 if(config.columns.some(key=>!keys.has(key))||!keys.has(config.sort)||config.filters.some(rule=>!filterFields.value.some(field=>field.key===rule.field))){show('视图引用的字段已不可用，请先更新视图；当前筛选未改变');return}
 try{for(const rule of config.filters){const field=filterFields.value.find(field=>field.key===rule.field)!;if(!workOperators(field).includes(rule.operator)||workFilterValue(field,rule.operator,rule.value)!==rule.value)throw Error()}}catch{show('视图筛选条件与当前字段类型不符，请更新视图；当前筛选未改变');return}
 let resolvedFilters:WorkFilter[]
 try{resolvedFilters=config.filters.map(rule=>resolveFilterReference(rule))}catch{show('视图引用的迭代或分类已不可用，请更新视图；当前筛选未改变');return}
 const sprint=config.sprintId?sprints.value.find(item=>Number(item.id)===config.sprintId):null,category=config.categoryId?categoryRecords.value.find(item=>Number(item.id)===config.categoryId):null
 if(config.sprintId&&!sprint||config.categoryId&&!category){show('视图引用的迭代或分类已不可用，请更新视图；当前筛选未改变');return}
 const user=config.assigneeMode==='me'?session.value?.user?.id:config.assigneeMode==='member'?config.assigneeUserId:''
 if(config.assigneeMode==='me'&&!user){show('账号信息尚未载入，请稍后重试');return}
 clearFilters();Object.assign(filters,{mine:config.relatedToMe?'1':'',q:config.q,statusCategory:config.statusCategory,priority:config.priority,assigneeUserId:user||'',sprint:sprint?.name??config.sprint,category:category?.name??config.category,sort:config.sort,order:config.order});selectedStatuses.value=[...config.statuses];advancedFilters.value=resolvedFilters;columns.value=normalizeColumns(config.columns);listView.value=config.view;page.value=1;++columnReadVersion
 boundViewFilters={signature:JSON.stringify(resolvedFilters),rules:resolvedFilters.map(captureFilterReference)}
}
function editCategory(mode:'create'|'rename'|'delete',category?:any){if(categorySaving.value||categoryOrdering.value)return;categoryModal.value=mode;categoryTarget.value=category||null;categoryName.value=category?.name||'';categoryError.value='';categoryMenu.value=null}
function categoryCanMove(category:any,direction:-1|1){
 if(!canManageCategories.value||categoryOrdering.value||categorySaving.value||category?.name==='未分类')return false
 const index=categoryRecords.value.findIndex(item=>Number(item.id)===Number(category.id)),target=categoryRecords.value[index+direction]
 return index>0&&!!target&&target.name!=='未分类'
}
async function moveCategory(category:any,direction:-1|1){
 if(!categoryCanMove(category,direction))return
 const current=[...categoryRecords.value],index=current.findIndex(item=>Number(item.id)===Number(category.id)),targetIndex=index+direction
 if(index<1||targetIndex<1||!current[targetIndex])return
 const reordered=[...current], [moved]=reordered.splice(index,1)
 reordered.splice(targetIndex,0,moved)
 categoryOrdering.value=true
 try{
  // 服务端要求提交当前项目的完整目录快照；失败时不乐观写入，避免并发下覆盖他人的排序。
  const result=await api<any>('/requirement-categories/order',{method:'PATCH',body:JSON.stringify({orderedIds:reordered.map(item=>Number(item.id)),...(categoryOrderVersion.value?{orderVersion:categoryOrderVersion.value}:{})})})
  if(!Array.isArray(result.items)||result.items.length!==reordered.length)throw Error('分类排序返回数据格式不正确，请刷新后重试')
  setCategoryDirectory(result)
  show(direction<0?'已将分类「{name}」上移':'已将分类「{name}」下移',{name:category.name})
 }catch(error:any){
  // 目录被其他管理员新增、删除或重排时，后端会拒绝过期快照；刷新后由用户决定新的顺序。
  try{await loadCategories()}catch{}
  show('分类顺序已发生变化，已刷新，请重试')
 }finally{categoryOrdering.value=false}
}
async function saveCategory(){
 if(categorySaving.value||categoryOrdering.value)return
 categorySaving.value=true;categoryError.value=''
 try{
  const mode=categoryModal.value,oldName=categoryTarget.value?.name
  if(mode==='delete'){
   const result=await api<any>('/requirement-categories/'+categoryTarget.value.id,{method:'DELETE'})
   if(filters.category===oldName)filters.category='未分类'
   show('分类已删除，{count} 条需求已移至未分类',{count:result.movedRequirements})
  }else{
   const name=categoryName.value.trim();if(!name)throw new Error('请输入分类名称')
   const result=await api<any>(mode==='create'?'/requirement-categories':'/requirement-categories/'+categoryTarget.value.id,{method:mode==='create'?'POST':'PATCH',body:JSON.stringify({name})})
   if(filters.category===oldName||mode==='create')filters.category=result.name
   show(mode==='create'?'分类已创建':'分类名称及关联需求已同步更新')
  }
  categoryModal.value=null;await Promise.all([loadCategories(),load()])
  if(selected.value){const id=selected.value.id,version=detailVersion;const updated=await api('/requirements/'+id);if(selected.value?.id===id&&version===detailVersion)selected.value=updated}
 }catch(e:any){categoryError.value=e.message}finally{categorySaving.value=false}
}
function normalizeColumns(keys:string[]){return ['code','title',...keys.filter((key,index)=>typeof key==='string'&&!['code','title'].includes(key)&&keys.indexOf(key)===index)]}
async function loadColumns(){
 const request=++columnReadVersion
 try{const data=await api<any>('/preferences/requirement-list');if(request===columnReadVersion&&!detailDisposed)columns.value=normalizeColumns(Array.isArray(data.columns)?data.columns:defaults.value)}
 catch{if(request===columnReadVersion&&!detailDisposed){columns.value=defaults.value;columnsError.value='列偏好暂时无法读取，当前使用默认列；可稍后重试保存。'}}
}
function openColumns(){if(metadataError.value){show('请先重新载入项目字段，再配置列表列');return}columnDraft.value=[...columns.value.filter(key=>catalog.value.some(c=>c.key===key)).map(key=>({key,enabled:true})),...catalog.value.filter(c=>!columns.value.includes(c.key)).map(c=>({key:c.key,enabled:false}))];columnsOpen.value=true}
function moveColumn(index:number,by:number){
 const next=index+by;if(index<2||next<2||next>=columnDraft.value.length)return
 const draft=[...columnDraft.value];[draft[index],draft[next]]=[draft[next],draft[index]];columnDraft.value=draft
}
function resetColumns(){columnDraft.value=catalog.value.map(c=>({key:c.key,enabled:!!(c.fixed||c.default)}))}
async function saveColumns(keys?:string[]){
 if(columnsSaving.value||metadataError.value)return
 const request=++columnReadVersion,project=session.value?.project?.id,user=session.value?.user?.id,panel=columnPanel.value
 columnsSaving.value=true;columnsError.value=''
 try{
  const next=normalizeColumns(keys||columnDraft.value.filter(c=>c.enabled).map(c=>c.key))
  await api('/preferences/requirement-list',{method:'PATCH',headers:project?{'X-DevFlow-Project':String(project)}:undefined,body:JSON.stringify({columns:next})})
  if(detailDisposed||request!==columnReadVersion||session.value?.project?.id!==project||session.value?.user?.id!==user)return
  columns.value=next;columnsOpen.value=false;if(panel===columnPanel.value)panel?.close();show('列配置已保存，仅影响你在当前项目的视图')
 }catch(e:any){if(!detailDisposed&&request===columnReadVersion)columnsError.value=e.message}finally{if(!detailDisposed)columnsSaving.value=false}
}
async function loadRelated(id:number){
 const version=detailVersion,read=++relatedLoadVersion
 const [c,a,ch]=await Promise.all([api<any>('/requirements/'+id+'/comments'),api<any>('/requirements/'+id+'/activities'),api<any>('/requirements/'+id+'/checklist')])
 if(!detailDisposed&&selected.value?.id===id&&version===detailVersion&&read===relatedLoadVersion){if(!Array.isArray(c.items))throw Error('评论数据格式不正确，请重试');comments.value=c.items;activities.value=a.items||[];checks.value=ch.items||[]}
}
async function refreshComments(){if(!selected.value||saving.value)return;const id=selected.value.id,version=detailVersion;try{await loadRelated(id)}catch(cause:any){if(!detailDisposed&&version===detailVersion&&selected.value?.id===id)detailError.value=cause.message}}
async function refreshTestCases(){
 const id=selected.value?.id,version=detailVersion
 if(!id)return
 try{await testCaseCoverage.value?.refresh();const data=await api<any>('/test-cases');if(selected.value?.id===id&&version===detailVersion)related.value={...related.value,cases:(data.items||[]).filter((item:any)=>item.requirementId===id)};await loadRelated(id)}
 catch(cause:any){if(selected.value?.id===id&&version===detailVersion)detailError.value=cause.message||'部分关联信息刷新失败，请稍后重试'}
}
async function loadParent(requirement:any){
 const version=++parentLoadVersion,detail=detailVersion,id=Number(requirement?.parentId),childId=requirement?.id
 parentSummary.value=null;parentError.value='';parentLoading.value=false
 if(!requirement?.parentId)return
 if(!Number.isSafeInteger(id)||id<1||id===childId){parentError.value='父需求暂时无法加载，可能已删除或没有访问权限。';return}
 const project=String(session.value?.project?.id||'')
 const current=()=>!detailDisposed&&version===parentLoadVersion&&detail===detailVersion&&String(session.value?.project?.id||'')===project&&selected.value?.id===childId&&Number(selected.value?.parentId)===id
 parentLoading.value=true
 try{const parent=await api<any>('/requirements/'+id,project?{headers:{'X-DevFlow-Project':project}}:undefined);if(!current())return;if(parent?.id!==id||typeof parent.title!=='string'||parent.projectId&&project&&String(parent.projectId)!==project)throw Error('Invalid parent response');parentSummary.value=parent}
 catch{if(current())parentError.value='父需求暂时无法加载，可能已删除或没有访问权限。'}
 finally{if(current())parentLoading.value=false}
}
async function openParent(){if(!parentSummary.value||parentLoading.value||saving.value||mediaBusy.value)return;await open(parentSummary.value.id)}
async function open(id:number,push=true){
 if(detailDisposed)return
 if(push){await router.replace({query:{...route.query,req:id}});return}
 const version=++detailVersion;detailLoading.value=true;detailError.value='';++parentLoadVersion;parentSummary.value=null;parentError.value='';parentLoading.value=false
 if(selected.value?.id!==id){selected.value=null;resetCommentDraft();comments.value=[];checkText.value='';resourceDraft.value={url:'',title:'',opened:false}}
 testCaseTotal.value=null
 try{
  const x=await api<any>('/requirements/'+id);if(version!==detailVersion)return
  selected.value=x;detailTab.value='详细信息';resetAssessment();resetDescription();resetAssignments();resetOwners();descriptionEditing.value=false;void loadParent(x)
  const [,all,d,c]=await Promise.all([loadRelated(id),api<any>('/requirements'),api<any>('/defects'),api<any>('/test-cases')])
  if(version===detailVersion)related.value={children:(all.items||[]).filter((v:any)=>v.parentId===id),defects:(d.items||[]).filter((v:any)=>v.requirementId===id),cases:(c.items||[]).filter((v:any)=>v.requirementId===id)}
 }catch(e:any){if(version===detailVersion){detailError.value=e.message;if(!selected.value)error.value=e.message}}
 finally{if(version===detailVersion){detailLoading.value=false;await consumeChildEntry()}}
}
function close(){
 if(defectContext.value){void defectComposer.value?.requestClose();return}
 if(fullEditorID.value){void fullEditor.value?.requestClose();return}
 if(childParent.value){void closeChild();return}
 const query={...route.query};delete query.req;delete query.createChild;router.replace({query})
}
async function patchFields(fields:any,resetDraft=false){
 if(!selected.value||saving.value||mediaBusy.value)return
 if(Object.hasOwn(fields,'customFields')&&!detailCustomDatesValid.value){detailError.value='请先修正日期输入';return}
 saving.value=true;detailError.value='';const id=selected.value.id,version=detailVersion
 let persisted:any=null
 try{
  const updated=await api('/requirements/'+id,{method:'PATCH',body:JSON.stringify(fields)})
  if(version===detailVersion&&selected.value?.id===id){selected.value=updated;persisted=updated;emit('updated',updated);if(resetDraft)resetAssessment();if(Object.hasOwn(fields,'descriptionDoc'))resourceVersion.value++;await loadRelated(id);show('已保存')}
  await Promise.all([load(),loadCategories(),loadTags()])
 }catch(e:any){if(version===detailVersion&&selected.value?.id===id)detailError.value=e.message}finally{saving.value=false;detailInputsVersion.value++}
 return persisted
}
function postComment(){void addComment({body:comment.value,mentionUserIds:commentMentions.value,contentDoc:commentDoc.value})}
function resetCommentDraft(){comment.value='';commentMentions.value=[];commentMentionNames.value={};commentDoc.value=null;commentReply.value=null}
function commentSnapshot(){return JSON.stringify({body:comment.value,ids:commentMentions.value,names:commentMentionNames.value,doc:commentDoc.value,reply:commentReply.value})}
async function replyComment(id:number){if(!canEdit.value||!selected.value||saving.value||mediaBusy.value||detailLoading.value||detailDisposed)return;const entry=commentMap.value.get(id);if(!entry||!Number.isSafeInteger(id)||id<1)return;commentReply.value={id,author:entry.author,body:entry.body};const requirementId=selected.value.id;await nextTick();if(!detailDisposed&&selected.value?.id===requirementId&&commentReply.value?.id===id)commentEditor.value?.focus()}
function cancelCommentReply(){if(!saving.value&&!mediaBusy.value)commentReply.value=null}
async function addComment(payload:{body:string;mentionUserIds:string[];contentDoc?:any}){
 if((!payload.body.trim()&&!documentHasContent(payload.contentDoc))||!selected.value||!canEdit.value||saving.value||mediaBusy.value||detailDisposed)return
 const id=selected.value.id,version=detailVersion,draft=commentSnapshot(),project=String(session.value?.project?.id||''),user=session.value?.user?.id,replyToId=commentReply.value?.id
 const current=()=>!detailDisposed&&version===detailVersion&&selected.value?.id===id&&String(session.value?.project?.id||'')===project&&session.value?.user?.id===user
 saving.value=true;detailError.value='';++relatedLoadVersion
 try{const saved=await api<any>('/requirements/'+id+'/comments',{method:'POST',body:JSON.stringify({...payload,...(replyToId?{replyToId}:{})}),...(project?{headers:{'X-DevFlow-Project':project}}:{})});if(!current())return;if(!saved||!Number.isSafeInteger(saved.id)||saved.id<1||replyToId&&saved.replyToId!==replyToId)throw Error('评论返回数据不完整，请刷新讨论后确认，勿重复提交。');comments.value=[saved,...comments.value.filter(entry=>entry.id!==saved.id)];if(draft===commentSnapshot())resetCommentDraft();resourceVersion.value++;show('评论已发布');window.dispatchEvent?.(new Event('devflow-notifications-changed'));try{await loadRelated(id)}catch{if(current())detailError.value='评论已发布，但讨论刷新失败，请重试刷新。'}}catch(e:any){if(current())detailError.value=e.message}finally{if(!detailDisposed&&version===detailVersion)saving.value=false}
}
async function addCheck(){
 if(!checkText.value.trim()||!selected.value)return
 try{await api('/requirements/'+selected.value.id+'/checklist',{method:'POST',body:JSON.stringify({text:checkText.value})});checkText.value='';await loadRelated(selected.value.id)}catch(e:any){detailError.value=e.message}
}
async function toggleCheck(x:any){
 try{await api('/requirements/'+selected.value.id+'/checklist',{method:'PATCH',body:JSON.stringify({id:x.id,done:!x.done})});await loadRelated(selected.value.id)}catch(e:any){detailError.value=e.message}
}
function show(message:string,params:Record<string,string|number>={}){toast.value=message;toastParams.value=params;clearTimeout(toastTimer);toastTimer=setTimeout(()=>toast.value='',2600)}
async function copy(){try{await navigator.clipboard.writeText(location.origin+'/requirements?req='+selected.value.id);show('需求链接已复制')}catch{detailError.value='复制失败，请复制浏览器地址栏链接。'}}
function clearFilters(){boundViewFilters=null;selectedStatuses.value=[];Object.assign(filters,{mine:'',q:'',status:'',statusCategory:'',priority:'',assignee:'',assigneeUserId:'',sprint:'',category:''});Object.keys(cfFilters).forEach(k=>delete cfFilters[k]);advancedFilters.value=[]}
function escape(event:KeyboardEvent){if(event.defaultPrevented)return;if(event.key==='Escape'){if(leavePrompt.value){if(!leaveSaving.value)finishLeave(false)}else if(defectContext.value){event.preventDefault();void defectComposer.value?.requestClose()}else if(fullEditorID.value){event.preventDefault();void fullEditor.value?.requestClose()}else if(childParent.value){event.preventDefault();void closeChild()}else if(categoryModal.value){if(!categorySaving.value)categoryModal.value=null}else if(categoryMenu.value!==null)categoryMenu.value=null;else if(columnsOpen.value){if(!columnsSaving.value)columnsOpen.value=false}else if(selected.value||props.detailOnly&&route.query.req)close()}}
watch([()=>({...filters}),()=>({...cfFilters}),advancedFilters,selectedStatuses],()=>{if(boundViewFilters&&boundViewFilters.signature!==JSON.stringify(advancedFilters.value))boundViewFilters=null;if(props.detailOnly)return;++listVersion;clearTimeout(timer);timer=setTimeout(()=>{page.value=1;void load()},180)})
watch(()=>route.query.req,value=>{childParent.value=null;fullEditorID.value=null;defectContext.value=null;if(value)void open(Number(value),false);else{selected.value=null;resetCommentDraft();resourceDraft.value={url:'',title:'',opened:false};++detailVersion}})
watch(()=>route.query.createChild,()=>{void consumeChildEntry()})
function confirmDetailLeave():boolean|Promise<boolean>{
 if(testCaseCoverage.value?.canLeave?.()===false)return false
 if(bulkBusy.value||saving.value||mediaBusy.value||childEditor.value?.saving||fullEditor.value?.saving||defectComposer.value?.saving||requirementLinks.value?.saving||requirementDependencies.value?.saving)return false
 if(aiDirty.value&&!aiTests.value?.canLeave())return false
 if(!detailDirty.value)return true
 if(pendingLeave)pendingLeave(false)
 leavePrompt.value=true;leaveError.value=''
 return new Promise(resolve=>{pendingLeave=resolve})
}
function finishLeave(allowed:boolean){if(leaveSaving.value)return;const resolve=pendingLeave;pendingLeave=null;leavePrompt.value=false;leaveError.value='';resolve?.(allowed)}
async function editFull(){
 if(!canEdit.value||!selected.value||fullEditorID.value||!await confirmDetailLeave())return
 resetAssessment();resetDescription();resetAssignments();resetOwners();resetCommentDraft();resourceDraft.value={url:'',title:'',opened:false}
 fullEditorID.value=selected.value.id
}
async function fullEditorSaved(requirement:any){
 if(fullEditorID.value!==requirement.id||selected.value?.id!==requirement.id)return
 selected.value=requirement;resetAssessment();resetDescription();resetAssignments();resetOwners();fullEditorID.value=null;resourceVersion.value++;emit('updated',requirement)
 try{await Promise.all([loadRelated(requirement.id),load(),loadCategories(),loadTags(),loadParent(requirement)])}catch(cause:any){detailError.value=cause.message||'部分关联信息刷新失败，请稍后重试'}
}
async function saveAndLeave(){
 if(saving.value||mediaBusy.value||leaveSaving.value||commentDraftDirty.value||resourceDirty.value||!canEdit.value)return
 if(!detailCustomDatesValid.value){leaveError.value='请先修正日期输入';return}
 leaveError.value='';const fields:any={}
 if(assessmentDirty.value){const {remarksMentionNames,...assessmentFields}=detailDraft;const ids=retainMentionIds(detailDraft.remarks,detailDraft.remarksMentionUserIds,members.value,detailDraft.remarksMentionNames);if(unavailableNewMentionIds(ids,members.value,selected.value?.remarksMentionUserIds).length){leaveError.value='新增提及成员暂不可用，请刷新项目成员或移除后重选';return}Object.assign(fields,assessmentFields,{remarksMentionUserIds:ids})}
 if(descriptionDirty.value){const ids=retainMentionIds(descriptionDraft.body,descriptionDraft.mentionUserIds,members.value,descriptionDraft.mentionNames);if(unavailableNewMentionIds(ids,members.value,selected.value?.descriptionMentionUserIds).length){leaveError.value='新增提及成员暂不可用，请刷新项目成员或移除后重选';return}Object.assign(fields,{description:descriptionDraft.body,descriptionMentionUserIds:ids});if(descriptionDraft.document!==null||selected.value?.descriptionDoc)fields.descriptionDoc=descriptionDraft.document}
 if(assignmentDirty.value)fields.assigneeUserIds=normalizeMentionIds(assignmentDraft.value)
 if(ownerDirty.value)fields.ownerUserIds=normalizeMentionIds(ownerDraft.value)
 leaveSaving.value=true
 const updated=Object.keys(fields).length?await patchFields(fields,true):selected.value
 leaveSaving.value=false
 if(updated){resetDescription();resetAssignments();resetOwners();finishLeave(true)}else leaveError.value=detailError.value||'保存失败，请重试'
}
onBeforeRouteLeave(confirmDetailLeave)
onBeforeRouteUpdate((to,from)=>to.query.req===from.query.req&&to.query.createChild===from.query.createChild||confirmDetailLeave())
function beforeUnload(event:BeforeUnloadEvent){if(detailDirty.value||aiDirty.value||bulkBusy.value){event.preventDefault();event.returnValue=''}}
onMounted(async()=>{
 window.addEventListener('focus',loadOptions);window.addEventListener('keydown',escape);window.addEventListener('beforeunload',beforeUnload)
 try{session.value=await api('/session')}catch(e:any){error.value=e.message}
 if(detailDisposed)return
 await loadOptions();if(detailDisposed)return;if(!props.detailOnly)await Promise.all([load(),loadColumns()]);if(route.query.req)await open(Number(route.query.req),false)
})
onBeforeUnmount(()=>{detailDisposed=true;pendingLeave?.(false);pendingLeave=null;++listVersion;++detailVersion;clearTimeout(timer);clearTimeout(toastTimer);window.removeEventListener('focus',loadOptions);window.removeEventListener('keydown',escape);window.removeEventListener('beforeunload',beforeUnload)})
</script>

<template>
 <div :class="props.detailOnly?'requirement-detail-host':'requirements requirements-v4'">
  <template v-if="!props.detailOnly">
  <aside id="requirements-sidebar" class="category-tree requirements-sidebar" :class="{'is-collapsed':!requirementsSidebarExpanded}">
   <SidebarCollapseButton :expanded="requirementsSidebarExpanded" :label="t('需求侧边栏')" @toggle="requirementsSidebarExpanded=!requirementsSidebarExpanded"/>
   <div v-show="requirementsSidebarExpanded" class="requirements-sidebar-content">
   <header class="pool-brand"><span><Icon name="list" :size="20"/></span><div><b>{{ t('需求池') }}</b><small>{{ t('从想法到交付') }}</small></div></header>
   <nav class="pool-views" :aria-label="t('需求视图')">
    <button :class="{active:activeView==='all'}" @click="setView('all')"><Icon name="list"/><span>{{ t('全部需求') }}</span><em>{{totalRequirements}}</em></button>
    <button :class="{active:activeView==='mine'}" @click="setView('mine')"><Icon name="user"/><span>{{ t('与我相关') }}</span></button>
    <button :class="{active:activeView==='review'}" @click="setView('review')"><Icon name="review"/><span>{{ t('待评审') }}</span></button>
    <button :class="{active:activeView==='completed'}" @click="setView('completed')"><Icon name="work"/><span>{{ t('已完成') }}</span></button>
   </nav>
   <div class="category-section-title"><span>{{ t('需求分类') }}</span><button v-if="canManageCategories" :title="t('创建分类')" :aria-label="t('创建需求分类')" :disabled="categoryOrdering||categorySaving" @click="editCategory('create')"><Icon name="plus"/></button></div>
   <div class="category-search"><Icon name="search" :size="13"/><input v-model="categorySearch" :aria-label="t('搜索需求分类')" :placeholder="t('查找分类')"></div>
   <div class="category-folders"><div v-for="c in visibleCategories" :key="c.id" class="category-folder" :class="{active:filters.category===c.name}"><div class="category-folder-row"><button class="category-select" :title="categoryLabel(c.name)" @click="filters.category=c.name"><Icon name="folder"/><span>{{categoryLabel(c.name)}}</span><em>{{c.count}}</em></button><button v-if="canManageCategories&&c.name!=='未分类'" class="category-menu-trigger" :aria-label="t('管理分类 {name}',{name:c.name})" :aria-expanded="categoryMenu===c.id" :disabled="categoryOrdering||categorySaving" @click="categoryMenu=categoryMenu===c.id?null:c.id"><Icon name="more"/></button></div><div v-if="categoryMenu===c.id" class="category-actions" :aria-busy="categoryOrdering"><div class="category-reorder-actions" role="group" :aria-label="t('调整分类顺序')"><button type="button" class="category-move" :title="t('上移 {name}',{name:c.name})" :aria-label="t('上移 {name}',{name:c.name})" :disabled="!categoryCanMove(c,-1)" @click="moveCategory(c,-1)">↑</button><button type="button" class="category-move" :title="t('下移 {name}',{name:c.name})" :aria-label="t('下移 {name}',{name:c.name})" :disabled="!categoryCanMove(c,1)" @click="moveCategory(c,1)">↓</button></div><button :disabled="categoryOrdering||categorySaving" @click="editCategory('rename',c)"><Icon name="edit" :size="13"/>{{ t('重命名') }}</button><button :disabled="categoryOrdering||categorySaving" @click="editCategory('delete',c)"><Icon name="trash" :size="13"/>{{ t('删除分类') }}</button></div></div><p v-if="!visibleCategories.length" class="no-categories">{{ t('没有匹配的分类') }}</p></div>
   <button v-if="canManageCategories" class="new-category-link" :disabled="categoryOrdering||categorySaving" @click="editCategory('create')"><Icon name="plus" :size="14"/>{{ t('新建分类') }}</button>
   <div class="pool-sidebar-footer"><Icon name="shield" :size="14"/><span>{{ t('分类与数据按当前项目隔离') }}</span></div>
   </div>
  </aside>
  <section class="list-pane">
<div class="list-heading pool-heading compact-page-heading"><div><h1>{{categoryLabel(filters.category)||({mine:t('与我相关的需求'),review:t('待评审需求'),completed:t('已完成需求')} as Record<string,string>)[activeView]||t('全部需求')}}<small>{{listTotal}}</small></h1><details class="page-heading-note"><summary :aria-label="t('页面说明')" :title="t('页面说明')"><span aria-hidden="true">?</span></summary><p>{{ t('统一管理需求、协同评估难度，让交付安排清晰可见。') }}</p></details></div><div class="compact-heading-actions"><TapdImport v-if="canManageCategories" @imported="load"/><router-link v-if="canEdit" class="btn primary" to="/requirements/new"><Icon name="plus"/>{{ t('创建需求') }}</router-link></div></div>
   <div v-if="metadataError" class="inline-notice" role="alert">{{t('项目分类、迭代或字段选项加载失败：{error}',{error:t(metadataError)})}} <button class="link" @click="loadOptions">{{ t('重试') }}</button></div>
   <div class="toolbar req-toolbar">
    <div class="search"><Icon name="search"/><input v-model="filters.q" :aria-label="t('搜索需求')" :placeholder="t('搜索标题、编号、负责人或部门')"></div>
    <StatusMultiSelect v-model="statusSelection" :options="statusFilterOptions" :label="t('筛选需求状态（多选）')" />
    <select v-if="showExtraFilters||filters.priority" v-model="filters.priority" :aria-label="t('筛选优先级')"><option value="">{{ t('全部优先级') }}</option><option v-for="p in ['P0','P1','P2','P3']" :key="p" :value="p">{{p}}</option></select>
    <MemberMultiSelect v-model="assigneeFilterIds" :members="members" :snapshots="assigneeFilterSnapshots" :current-user-id="session?.user?.id" :label="t('筛选处理人')" :hint="t('全部处理人')" :show-lead="false" single compact/>
    <button type="button" class="btn compact" :aria-expanded="showExtraFilters" @click="showExtraFilters=!showExtraFilters">{{t(showExtraFilters?'收起更多操作':'更多筛选与操作')}}</button>
    <select v-if="showExtraFilters||filters.sprint" v-model="filters.sprint" :aria-label="t('筛选迭代')" @focus="loadOptions"><option value="">{{ t('全部迭代') }}</option><option value="待规划">{{ t('待规划') }}</option><option v-for="s in sprints" :key="s.id" :value="s.name">{{s.name}}</option></select>
    <button type="button" class="btn compact" :class="{active:filters.mine==='1'}" :aria-pressed="filters.mine==='1'" :title="t('包含人员字段、工程师角色、创建、评论参与及 @ 提及')" @click="filters.mine=filters.mine==='1'?'':'1'">{{t('与我相关')}}</button><WorkItemFilters v-show="showExtraFilters||advancedFilters.length" v-model="advancedFilters" :fields="filterFields" :disabled="!!metadataError"/>
    <div v-show="showExtraFilters"><RequirementListExport v-if="session?.project?.id" :items="loading||error?[]:sortedItems" :total="loading||error?0:listTotal" :records-provider="exportRecords" :columns="visibleColumns.map(column=>column.key)" :project-id="session.project.id"/></div>
    <div v-show="showExtraFilters||checkedItems.length"><RequirementBulkActions v-if="canEdit" :items="checkedItems" :members="members" :sprints="sprints" :categories="categories" :statuses="statusDefinitions" :disabled="loading||!!metadataError" @busy="bulkBusy=$event" @changed="bulkChanged"/></div>
    <button v-if="filters.mine||filters.q||filters.status||filters.statusCategory||selectedStatuses.length||filters.priority||filters.assignee||filters.assigneeUserId||filters.sprint||filters.category||advancedFilters.length||Object.values(cfFilters).some(Boolean)" class="link" @click="clearFilters">{{ t('清除筛选') }}</button>
   </div>
   <div class="req-viewbar" :class="{'is-compact':!showExtraFilters}">
   <RequirementSavedViews v-if="session" :show-shortcuts="!requirementsSidebarExpanded" :config="captureView()" :disabled="loading||!!metadataError||bulkBusy||!!filters.assignee" @apply="applySavedView" @builtin="setView"/>
    <div class="req-display-tools"><div class="req-view-switch"><button type="button" :aria-pressed="listView==='list'" :class="{active:listView==='list'}" @click="listView='list'">{{t('列表')}}</button><button type="button" :aria-pressed="listView==='board'" :class="{active:listView==='board'}" @click="listView='board'">{{t('看板')}}</button></div><select v-model="filters.sort" :aria-label="t('排序字段')"><option v-for="column in catalog" :key="column.key" :value="column.key">{{columnLabel(column)}}</option></select><button class="btn compact" :aria-label="filters.order==='desc'?t('切换升序'):t('切换降序')" @click="filters.order=filters.order==='desc'?'asc':'desc'">{{filters.order==='desc'?t('↓ 降序'):t('↑ 升序')}}</button><WorkItemColumns ref="columnPanel" :fields="columnFields" :model-value="columns" :saving="columnsSaving" :error="columnsError" :disabled="!!metadataError||loading" @save="saveColumns"/><router-link v-if="canConfigure" class="link" to="/settings/fields">{{ t('管理自定义字段') }}</router-link></div></div>
   <div v-if="error" class="inline-notice" role="alert">{{t(error)}} <button class="link" @click="load">{{ t('重试') }}</button></div>
   <div v-if="loading" class="state"><span class="spinner"></span>{{ t('正在载入需求…') }}</div>
   <div v-else-if="!items.length" class="state"><b>{{ t('没有符合条件的需求') }}</b><p>{{ t('清除筛选或创建一条需求。') }}</p><button class="btn" @click="clearFilters">{{ t('清除筛选') }}</button></div>
   <div v-else-if="listView==='board'" class="requirement-status-board" :aria-label="t('需求状态看板')"><section v-for="column in statusBoard" :key="column.value"><h3 :style="{borderColor:column.color}">{{statusOptionLabel(column,t)}} <small>{{column.items.length}}</small></h3><article v-for="item in column.items" :key="item.id" role="link" tabindex="0" :class="['requirement-board-card',{expanded:boardCardExpanded(item.id)}]" @click="open(item.id)" @keydown.enter.self="open(item.id)" @keydown.space.self.prevent="open(item.id)"><div class="requirement-board-summary"><RequirementCode class="code" :requirement="item" :members="members" @open="open(item.id)"/><strong :title="item.title">{{item.title}}</strong><button type="button" class="requirement-board-disclosure" :aria-controls="'requirement-board-details-'+item.id" :aria-expanded="boardCardExpanded(item.id)" :aria-label="boardCardExpanded(item.id)?t('收起需求 {title} 的更多信息',{title:item.title}):t('展开需求 {title} 的更多信息',{title:item.title})" @click.stop="toggleBoardCard(item.id)"><span>{{boardCardExpanded(item.id)?t('收起更多信息'):t('展开更多信息')}}</span><span class="requirement-board-disclosure-icon" aria-hidden="true">⌄</span></button></div><div :id="'requirement-board-details-'+item.id" v-show="boardCardExpanded(item.id)" class="requirement-board-details"><span class="req-tags"><span v-for="tag in splitTags(item.tags)" :key="tag" class="colored-tag" :style="tagStyle(item.tagColors?.[tag])">{{tag}}</span><span v-if="!splitTags(item.tags).length">{{t('暂无标签')}}</span></span><span class="requirement-board-meta"><span>{{displayValue(item,'assignee')}}</span><span>{{t('总权重')}} {{item.weightTotal??0}}</span></span></div></article></section></div>
   <div v-else class="table-wrap">
    <table class="req-table configurable-table" :style="{width:tableWidth+'px'}">
     <colgroup><col v-if="canEdit" style="width:44px"><col v-for="c in visibleColumns" :key="c.key" :style="{width:(c.width||115)+'px'}"></colgroup>
     <thead><tr><th v-if="canEdit" class="bulk-selection"><input type="checkbox" :checked="allPageChecked" :disabled="bulkBusy" :aria-label="t('选择当前页需求')" @change="togglePage(($event.target as HTMLInputElement).checked)"></th><th v-for="c in visibleColumns" :key="c.key" :class="{'sticky-code':c.key==='code','sticky-title':c.key==='title'}" :aria-sort="filters.sort===c.key?(filters.order==='asc'?'ascending':'descending'):'none'"><button class="column-sort" :aria-label="t('按 {name} 排序',{name:columnLabel(c)})" @click="sortByColumn(c.key)">{{columnLabel(c)}} <span>{{filters.sort===c.key?(filters.order==='asc'?'↑':'↓'):'↕'}}</span></button></th></tr></thead>
     <tbody><tr v-for="x in treeItems" :key="x.id" :class="{selected:selected?.id===x.id,'requirement-child-row':x._treeDepth>0}" @click="open(x.id)"><td v-if="canEdit" class="bulk-selection" @click.stop><input v-model="checkedIds" type="checkbox" :value="x.id" :disabled="bulkBusy" :aria-label="t('选择需求 {code}',{code:x.code})"></td>
      <td v-for="c in visibleColumns" :key="c.key" :class="{'sticky-code code':c.key==='code','sticky-title':c.key==='title','numeric-cell':c.key.endsWith('.value')||c.key==='weightTotal'}" :title="c.key==='tags'?x.tags:displayValue(x,c.key)">
       <RequirementCode v-if="c.key==='code'" :requirement="x" :members="members" @open="open(x.id)"/>
       <div v-else-if="c.key==='title'" class="req-title-link" :style="{paddingLeft:(x._treeDepth*18)+'px'}"><button v-if="x._treeHasChildren" type="button" class="requirement-tree-toggle" :aria-label="x._treeExpanded?t('收起子需求'):t('展开子需求')" :aria-expanded="x._treeExpanded" @click.stop="toggleRequirementChildren(x.id)">{{x._treeExpanded?'⌄':'›'}}</button><span v-else-if="x._treeDepth" class="requirement-tree-branch" aria-hidden="true">└</span><button type="button" class="req-title-open" @click.stop="open(x.id)"><span class="type-icon">{{ t('需') }}</span><b>{{x.title}}</b></button></div>
       <span v-else-if="c.key==='status'" :class="['status','workflow-color',x.status]" :style="workflowStyle(x,statusDefinitions)">{{statusLabel(x,statusDefinitions,t)}}</span>
       <span v-else-if="c.key==='priority'" :class="['priority',x.priority]">{{x.priority}}</span>
       <div v-else-if="c.key==='tags'" class="req-tags"><span v-for="tag in splitTags(x.tags)" :key="tag" class="colored-tag" :style="tagStyle(x.tagColors?.[tag])">{{tag}}</span><span v-if="!splitTags(x.tags).length">—</span></div>
       <strong v-else-if="c.key==='weightTotal'" class="total-value">{{x.weightTotal??0}}</strong>
       <span v-else>{{displayValue(x,c.key)}}</span>
      </td>
     </tr></tbody>
    </table>
   </div>
   <div class="pagination"><span>{{t('共 {count} 条 · 每页 {size} 条',{count:listTotal,size})}}</span><span v-if="listView==='board'">{{t('看板仅展示当前页需求')}}</span><AppSelect class="page-size-select" :model-value="size" :options="pageSizeOptions" :label="t('每页条数')" :disabled="loading||bulkBusy" @update:model-value="selectPageSize"/><button :disabled="loading||bulkBusy||page<=1" :aria-label="t('上一页')" @click="changePage(page-1)">‹</button><b>{{page}} / {{pages}}</b><button :disabled="loading||bulkBusy||page>=pages" :aria-label="t('下一页')" @click="changePage(page+1)">›</button></div>
  </section>

  </template>
  <div v-if="props.detailOnly&&route.query.req&&!selected" class="drawer-shade" @click.self="close"><ResizableDrawer storage-key="requirements.detail-loading.width" :initial-width="1320" :label="t('需求详情')" class="requirement-drawer"><header class="drawer-head"><div class="drawer-title"><h2>{{t('需求详情')}}</h2><button :aria-label="t('关闭需求详情')" @click="close">×</button></div></header><div class="state"><template v-if="detailLoading">{{t('正在载入需求…')}}</template><template v-else><p role="alert">{{t(detailError||error||'需求暂时无法载入，请重试')}}</p><button class="btn" @click="open(Number(route.query.req),false)">{{t('重新加载')}}</button></template></div></ResizableDrawer></div>
  <div v-if="selected" class="drawer-shade" @click.self="close">
   <ResizableDrawer v-model:width="drawerWidth" storage-key="requirements.detail.width" :initial-width="1320" :label="selected.title" :inert="!!childParent||!!fullEditorID||!!defectContext" class="requirement-drawer">
    <header class="drawer-head"><div class="drawer-kicker"><span class="type-icon">{{ t('需') }}</span><RequirementCode :requirement="selected" :members="members" :open-on-click="false"/><span :class="['status','workflow-color',selected.status]" :style="workflowStyle(selected,statusDefinitions)">{{statusLabel(selected,statusDefinitions,t)}}</span><span class="drawer-width-hint">{{t('拖动左侧边缘调整宽度')}}</span></div><div class="drawer-title"><h2>{{selected.title}}</h2><div><RequirementFavorite :requirement-id="selected.id"/><WorkItemPdfExport v-if="session?.project?.id" object-type="requirement" :object-id="selected.id" :project-id="session.project.id"/><button :aria-pressed="showProperties" :aria-label="showProperties?t('收起基础信息'):t('展开基础信息')" @click="showProperties=!showProperties"><Icon name="fields"/>{{t('基础信息')}}</button><button :title="t('复制链接')" :aria-label="t('复制需求链接')" @click="copy"><Icon name="link"/></button><button v-if="canEdit&&props.detailOnly" @click="editFull">{{t('完整编辑')}}</button><router-link v-else-if="canEdit" :to="'/requirements/'+selected.id+'/edit'">{{ t('完整编辑') }}</router-link><button :aria-label="t('关闭需求详情')" @click="close">×</button></div></div><p class="detail-time">{{ t('创建于') }} {{formatCreatedAt(selected.createdAt)}} {{ t('· 更新于') }} {{formatCreatedAt(selected.updatedAt)}}</p></header>
    <div v-if="selected.parentId" class="requirement-parent-context"><span>{{t('父需求')}}</span><span v-if="parentLoading" role="status">{{t('正在载入父需求…')}}</span><button v-else-if="parentSummary" type="button" :disabled="saving||mediaBusy" :aria-label="t('查看父需求')+' · '+parentSummary.title" @click="openParent"><b>{{parentSummary.code||'#'+parentSummary.id}}</b><span>{{parentSummary.title}}</span><Icon name="link" :size="14"/></button><div v-else role="status">{{t(parentError||'父需求暂时无法加载，可能已删除或没有访问权限。')}} <button type="button" class="link" :disabled="saving" @click="loadParent(selected)">{{t('重试')}}</button></div></div>
    <div v-if="detailError" class="inline-notice detail-alert" role="alert">{{t(detailError)}}</div>
    <div v-if="detailLoading" class="state">{{ t('正在载入关联信息…') }}</div>
    <div v-else class="drawer-body detail-workspace" :class="{'properties-open':showProperties}">
     <ResizableSplit class="detail-split" scene="requirements.detail" :project-id="String(session?.project?.id||'')" :label="t('基础信息')" :show-aside="showProperties" :initial-width="360" :min-main-width="480" :min-aside-width="300" :breakpoint="820" :disabled="!!childParent||!!fullEditorID||!!defectContext">
      <template #main><div class="detail-reading">
     <nav class="detail-navigation" :aria-label="t('需求详情导航')">
      <span class="detail-nav-caption">{{t('内容与协作')}}</span>
      <button v-for="tab in detailTabs" :key="tab.name" :aria-pressed="detailTab===tab.name" :class="{active:detailTab===tab.name}" @click="selectDetailTab(tab.name)"><Icon :name="tab.icon"/><span>{{t(tab.label)}}</span><small v-if="tab.count!==undefined">{{tab.count}}</small></button>
      <div class="detail-tag-section"><span class="detail-nav-caption">{{t('Tag 区间')}}</span><button v-for="tab in tagTabs" :key="tab.name" :aria-pressed="detailTab===tab.name" :class="{active:detailTab===tab.name}" @click="selectDetailTab(tab.name)"><Icon :name="tab.icon"/><span>{{t(tab.label||tab.name)}}</span><small>{{tab.count}}</small></button></div>
      <div class="detail-nav-summary"><span>{{t('已保存总权重')}}</span><strong>{{selected.weightTotal??0}}</strong><small v-if="assessmentDirty">{{t('有未保存的修改')}}</small></div>
     </nav>
     <section class="detail-main">
      <div v-show="detailTab==='详细信息'" class="detail-overview">
       <div class="detail-block description-block">
        <div class="assessment-heading"><h3>{{t('需求描述')}}</h3><button v-if="canEdit&&!descriptionEditing" class="btn compact" :disabled="saving" @click="beginDescriptionEdit()">{{t('编辑正文')}}</button></div>
        <template v-if="descriptionEditing&&canEdit">
         <RichTextEditor ref="descriptionEditor" input-id="detail-description" v-model="descriptionDraft.body" v-model:document="descriptionDraft.document" v-model:mentionUserIds="descriptionDraft.mentionUserIds" v-model:mentionNames="descriptionDraft.mentionNames" :saved-mention-user-ids="selected.descriptionMentionUserIds" :members="members" :requirement-id="selected.id" :disabled="saving" :label="t('需求描述')" :placeholder="t('补充需求正文，输入 @ 选择协同成员')" @busy="descriptionMediaBusy=$event"/>
         <div class="assessment-actions"><span>{{descriptionDirty?t('有未保存的修改'):t('已与需求同步')}}</span><button class="btn" :disabled="saving||mediaBusy" @click="resetDescription();descriptionEditing=false">{{t('取消编辑')}}</button><button class="btn primary" :disabled="saving||mediaBusy||!descriptionDirty" @click="saveDescription">{{saving?t('保存中…'):t('保存正文')}}</button></div>
        </template>
        <div v-else class="description-read-surface" :class="{editable:canEdit}" :title="canEdit?t('双击编辑需求描述'):undefined" @dblclick="beginDescriptionEdit"><RichTextEditor readonly :model-value="selected.description||''" :document="selected.descriptionDoc" :mention-user-ids="selected.descriptionMentionUserIds" :mention-names="selected.descriptionMentionNames" :members="members" :requirement-id="selected.id" :label="t('需求描述')" :placeholder="t('尚未补充需求描述。')"/></div>
       </div>
       <div class="detail-block"><h3>{{ t('验收标准') }}</h3><p>{{selected.acceptance||t('尚未设置验收标准。')}}</p></div>
       <RequirementResources v-model="resourceDraft" :requirement-id="selected.id" :can-edit="canEdit" :refresh-token="resourceVersion" />
       <div class="detail-block assessment-block">
        <label class="remarks-label" for="detail-remarks">{{ t('备注') }}</label><MentionComment mode="field" input-id="detail-remarks" v-model="detailDraft.remarks" v-model:mentionUserIds="detailDraft.remarksMentionUserIds" v-model:mentionNames="detailDraft.remarksMentionNames" :saved-mention-user-ids="selected.remarksMentionUserIds" :members="members" :disabled="!canEdit||saving" :label="t('备注')" :rows="4" :maxlength="10000" :placeholder="t('补充依赖、风险或协作约定，输入 @ 选择成员')"/>
        <div v-if="canEdit" class="assessment-actions"><span>{{assessmentDirty?t('有未保存的修改'):t('已与需求同步')}}</span><button class="btn" :disabled="!assessmentDirty||saving||mediaBusy" @click="resetAssessment">{{ t('还原') }}</button><button class="btn primary" :disabled="!assessmentDirty||saving||mediaBusy" @click="saveAssessment">{{saving?t('保存中…'):t('保存评估与补充信息')}}</button></div>
       </div>
       <div class="detail-block"><h3>{{ t('检查清单') }} <small>{{checks.filter(x=>x.done).length}}/{{checks.length}}</small></h3><label v-for="x in checks" :key="x.id" class="check-row"><input type="checkbox" :checked="x.done" :disabled="!canEdit" @change="toggleCheck(x)"><span :class="{done:x.done}">{{x.text}}</span></label><div v-if="canEdit" class="inline-add"><input v-model="checkText" :aria-label="t('添加检查项')" @keyup.enter="addCheck" :placeholder="t('添加检查项，回车提交')"><button :aria-label="t('保存检查项')" @click="addCheck">＋</button></div></div>
       <div class="detail-block"><h3>{{t('评论')}} <small>{{comments.length}}</small><button type="button" class="link comment-refresh" :disabled="saving||detailLoading" @click="refreshComments">{{t('刷新评论')}}</button></h3>
        <template v-if="canEdit"><CommentReplyContext v-if="commentReply" :target="commentReply" composing :disabled="saving||mediaBusy" @cancel="cancelCommentReply"/><RichTextEditor ref="commentEditor" mode="comment" input-id="detail-comment" v-model="comment" v-model:document="commentDoc" v-model:mentionUserIds="commentMentions" v-model:mentionNames="commentMentionNames" :members="members" :requirement-id="selected.id" :disabled="saving" :label="t('需求评论')" @busy="commentMediaBusy=$event" @submit="postComment"/><div class="comment-post-actions"><span>{{t('支持表情、截图和文件，Ctrl / ⌘ + Enter 发布')}}</span><button class="btn primary compact" :disabled="saving||mediaBusy||!commentHasContent" @click="postComment">{{saving?t('保存中…'):t(commentReply?'发布回复':'发布评论')}}</button></div></template>
        <article v-for="c in comments" :key="c.id" class="comment"><span class="avatar small">{{c.author?.slice(0,1)}}</span><div><b>{{c.author}}</b><time>{{formatCreatedAt(c.createdAt)}}</time><CommentReplyContext v-if="c.replyToId" :target="commentMap.get(c.replyToId)||{id:c.replyToId,author:c.replyToAuthor,unavailable:true}"/><RichTextEditor readonly mode="comment" :model-value="c.body||''" :document="c.contentDoc" :mention-user-ids="c.mentionUserIds" :mention-names="c.mentionNames" :members="members" :requirement-id="selected.id" :label="t('评论内容')"/><button v-if="canEdit" type="button" class="link comment-reply-button" :disabled="saving||mediaBusy||detailLoading" :aria-label="t('回复 {name} 的评论',{name:c.author||t('未知用户')})" @click="replyComment(c.id)">{{t('回复')}}</button></div></article>
       </div>
      </div>
      <div v-if="detailTab==='角色权重'" class="detail-block detail-tag-pane">
       <div class="detail-pane-heading"><span class="eyebrow">{{t('Tag 区间')}}</span><h3>{{t('角色权重')}}</h3><p>{{t('按职能绑定人员并手动填写数值，自动汇总整条需求的难度。')}}</p></div>
       <RequirementWeights v-model="detailDraft.roleWeights" :members="members" :current-user-id="session?.user?.id" :disabled="!canEdit||saving"/>
       <div v-if="canEdit" class="assessment-actions"><span>{{assessmentDirty?t('有未保存的修改'):t('已与需求同步')}}</span><button class="btn" :disabled="!assessmentDirty||saving||mediaBusy" @click="resetAssessment">{{t('还原')}}</button><button class="btn primary" :disabled="!assessmentDirty||saving||mediaBusy" @click="saveAssessment">{{saving?t('保存中…'):t('保存评估与补充信息')}}</button></div>
      </div>
      <div v-else-if="detailTab==='标签'" class="detail-block detail-tag-pane">
       <div class="detail-pane-heading"><span class="eyebrow">{{t('Tag 区间')}}</span><h3>{{t('标签')}}</h3><p>{{t('选择已有标签或创建彩色标签，切换区间会保留未保存的修改。')}}</p></div>
       <RequirementTags v-model="detailDraft.tags" v-model:colors="detailDraft.tagColors" :options="tagOptions" :disabled="!canEdit||saving"/><p v-if="tagError" class="inline-notice">{{t(tagError)}} <button class="link" @click="loadTags">{{t('重试')}}</button></p>
       <div v-if="canEdit" class="assessment-actions"><span>{{assessmentDirty?t('有未保存的修改'):t('已与需求同步')}}</span><button class="btn" :disabled="!assessmentDirty||saving||mediaBusy" @click="resetAssessment">{{t('还原')}}</button><button class="btn primary" :disabled="!assessmentDirty||saving||mediaBusy" @click="saveAssessment">{{saving?t('保存中…'):t('保存评估与补充信息')}}</button></div>
      </div>
      <RequirementLinks v-else-if="detailTab==='关联需求'" ref="requirementLinks" :requirement-id="selected.id" :can-edit="canEdit" @open="open"/>
      <RequirementDependencies v-else-if="detailTab==='需求依赖'" ref="requirementDependencies" :requirement-id="selected.id" :can-edit="canEdit" @open="open"/>
      <div v-else-if="detailTab==='变更记录'" class="detail-block"><h3>{{ t('变更记录') }}</h3><div v-if="!activities.length" class="empty-mini">{{ t('暂无变更记录') }}</div><article v-for="a in activities" :key="a.id" class="activity"><span></span><div><b>{{a.actor}}</b> {{a.detail}}<time>{{formatCreatedAt(a.createdAt)}}</time></div></article></div>
      <div v-else-if="detailTab!=='详细信息'" :class="['relation-list',{ 'test-case-relation-list':detailTab==='测试用例' }]"><h3 v-if="detailTab!=='测试用例'">{{t(detailTab)}}</h3>
       <template v-if="detailTab==='子需求'"><div class="child-actions"><p>{{t('子需求继承父需求分类及可用迭代，可在创建时调整。')}}</p><button v-if="canEdit" ref="childCreateButton" class="btn primary compact" :disabled="saving" @click="createChild">{{t('创建子需求')}}</button></div><section class="child-requirement-tree" :aria-label="t('子需求层级')"><article class="child-requirement-parent"><span class="type-icon">{{t('需')}}</span><RequirementCode :requirement="selected" :members="members" @open="open(selected.id)"/><b>{{selected.title}}</b><small>{{t('{count} 条子需求',{count:related.children.length})}}</small></article><article v-for="x in related.children" :key="x.id" class="child-requirement-node"><span class="child-requirement-branch" aria-hidden="true">└</span><RequirementCode :requirement="x" :members="members" @open="open(x.id)"/><button class="link" @click="open(x.id)">{{x.title}}</button><span class="status workflow-color" :style="workflowStyle(x,statusDefinitions)">{{statusLabel(x,statusDefinitions,t)}}</span></article></section><p v-if="!related.children.length" class="empty-mini">{{ t('暂无子需求') }}</p></template>
       <template v-if="detailTab==='缺陷'"><div class="child-actions"><p>{{t('从需求发起缺陷，自动进入统一缺陷池并保留需求关联。')}}</p><button v-if="canEdit" ref="defectCreateButton" class="btn primary compact" :disabled="saving||mediaBusy" @click="createDefect">{{t('创建关联缺陷')}}</button></div><article v-for="x in related.defects" :key="x.id"><router-link :to="'/defects?bug='+x.id">{{x.code}} · {{x.title}}</router-link><span class="status">{{t(x.status)}}</span></article><p v-if="!related.defects.length" class="empty-mini">{{ t('暂无关联缺陷') }}</p></template>
       <RequirementTestCaseCoverage v-if="detailTab==='测试用例'" ref="testCaseCoverage" :requirement-id="selected.id" :disabled="!canEdit||saving" @loaded="testCaseTotal=$event.total" />
      </div>
      <RequirementAITestCases v-show="detailTab==='测试用例'" :key="selected.id" ref="aiTests" :requirement-id="selected.id" :requirement-updated-at="selected.updatedAt" :disabled="!canEdit||saving" :has-unsaved-changes="!!(commentHasContent||resourceDirty||assessmentDirty||descriptionDirty||assignmentDirty||ownerDirty)" @busy="aiBusy=$event" @dirty="aiDirty=$event" @imported="refreshTestCases"/>
     </section>
      </div></template>
      <template #aside><aside class="detail-props" :aria-label="t('基础信息')">
      <h3>{{ t('基础信息') }}</h3><RequirementDetailPreferences v-model="detailPreferences" :definitions="fieldDefs" :can-configure="canConfigure"/><fieldset :key="detailInputsVersion" :disabled="!canEdit||saving||mediaBusy">
       <div v-if="detailFieldVisible('status')" class="requirement-state-control"><span>{{t('状态')}}</span><RequirementTransition :requirement-id="selected.id" :refresh-key="detailInputsVersion" :status="selected.status" :definitions="statusDefinitions" :disabled="saving||mediaBusy||!canEdit" @change="patchFields({status:$event})" /></div>
       <label v-if="detailFieldVisible('category')">{{ t('分类') }}<select :value="selected.category" @change="patchFields({category:($event.target as HTMLSelectElement).value})"><option v-for="c in categories" :key="c" :value="c">{{categoryLabel(c)}}</option></select></label>
       <label v-if="detailFieldVisible('sprint')">{{ t('迭代') }}<select :value="selected.sprint" :aria-label="t('需求所属迭代')" @focus="loadOptions" @change="patchFields({sprint:($event.target as HTMLSelectElement).value})"><option v-for="s in selectedSprintOptions" :key="s.name" :value="s.name" :disabled="['已完成','已取消','历史关联'].includes(s.status)&&s.name!==selected.sprint">{{s.name==='待规划'?t('待规划'):s.name}}{{s.status?' · '+t(s.status==='进行中'?'当前进行中':s.status):''}}</option></select></label>
       <label v-if="detailFieldVisible('priority')">{{ t('优先级') }}<select :value="selected.priority" @change="patchFields({priority:($event.target as HTMLSelectElement).value})"><option v-for="p in ['P0','P1','P2','P3']" :key="p" :value="p">{{p}}</option></select></label>
       <div v-if="detailFieldVisible('owner')" class="detail-owners detail-assignees"><label for="detail-owners">{{ t('产品负责人') }}</label><MemberMultiSelect input-id="detail-owners" v-model="ownerDraft" @update:modelValue="ownersTouched=true" :members="members" :member-roles="['product']" :snapshots="selected.owners" :legacy-name="!ownersTouched&&!selected.ownerUserIds?.length?selected.owner:''" :current-user-id="session?.user?.id" :disabled="!canEdit||saving" :label="t('产品负责人')"/><div v-if="canEdit&&ownerDirty" class="assignment-actions"><button class="btn compact" :disabled="saving" @click="resetOwners">{{t('还原')}}</button><button class="btn primary compact" :disabled="saving" @click="saveOwners">{{t('保存产品负责人')}}</button></div></div>
       <div v-if="detailFieldVisible('assignee')" class="detail-assignees"><label for="detail-assignees">{{ t('处理人') }}</label><MemberMultiSelect input-id="detail-assignees" v-model="assignmentDraft" @update:modelValue="assignmentTouched=true" :members="members" :snapshots="selected.assignees" :legacy-name="!assignmentTouched&&!selected.assigneeUserIds?.length?selected.assignee:''" :disabled="!canEdit||saving" :label="t('处理人')"/><div v-if="canEdit&&assignmentDirty" class="assignment-actions"><button class="btn compact" :disabled="saving" @click="resetAssignments">{{ t('还原') }}</button><button class="btn primary compact" :disabled="saving" @click="saveAssignments">{{ t('保存处理人') }}</button></div></div>
       <h3 class="custom-title">{{ t('自定义字段') }}</h3><CustomFieldInputs object-type="requirement" v-model="detailDraft.customFields" :visible-keys="detailPreferences.customFieldKeys" inline @validity-change="detailCustomDatesValid=$event"/>
       <button v-if="canEdit" class="btn" :disabled="!assessmentDirty||saving||mediaBusy" @click="saveAssessment">{{ t('保存补充字段') }}</button>
      </fieldset>
      <dl class="detail-audit"><template v-if="detailFieldVisible('createdAt')"><dt>{{ t('创建时间') }}</dt><dd>{{formatCreatedAt(selected.createdAt)}}</dd></template><template v-if="detailFieldVisible('updatedAt')"><dt>{{ t('更新时间') }}</dt><dd>{{formatCreatedAt(selected.updatedAt)}}</dd></template><template v-if="detailFieldVisible('weightTotal')"><dt>{{ t('已保存总权重') }}</dt><dd class="total-value">{{selected.weightTotal??0}}</dd></template></dl>
     </aside></template>
     </ResizableSplit>
    </div>
   </ResizableDrawer>
  </div>

  <div v-if="childParent" class="drawer-shade child-drawer-shade" @click.self="closeChild">
   <ResizableDrawer v-model:width="childDrawerWidth" storage-key="requirements.child.width" :initial-width="1160" :label="t('创建子需求')" class="child-requirement-drawer">
    <Editor :key="childParent.id" ref="childEditor" embedded :parent-id="childParent.id" @cancel="childCancelled" @created="childCreated"/>
   </ResizableDrawer>
  </div>

  <div v-if="fullEditorID" class="drawer-shade child-drawer-shade" @click.self="fullEditor?.requestClose()"><ResizableDrawer v-model:width="fullEditorWidth" storage-key="requirements.full-editor.width" :initial-width="1320" :label="t('完整编辑')" class="child-requirement-drawer"><Editor :key="fullEditorID" ref="fullEditor" embedded :requirement-id="fullEditorID" @cancel="fullEditorID=null" @saved="fullEditorSaved"/></ResizableDrawer></div>
  <DefectComposer v-if="defectContext" :key="defectContext.id" ref="defectComposer" :requirement-id="defectContext.id" :requirement-title="defectContext.title" :initial-sprint="defectContext.sprint" @created="defectCreated" @cancel="defectCancelled"/>

  <div v-if="leavePrompt" class="modal-shade detail-leave-shade" @click.self="finishLeave(false)"><section class="modal detail-leave-dialog" role="dialog" aria-modal="true" aria-labelledby="detail-leave-title"><header><h2 id="detail-leave-title">{{t('保存修改后离开？')}}</h2><button :disabled="leaveSaving" :aria-label="t('继续编辑')" @click="finishLeave(false)">×</button></header><div class="modal-body"><p>{{t('需求有未保存的修改。你可以继续编辑、保存后离开，或明确放弃本次修改。')}}</p><p v-if="commentDraftDirty" class="inline-notice">{{t('评论尚未发表。请返回详情发表，或选择放弃；离开不会自动发表评论。')}}</p><p v-if="resourceDirty" class="inline-notice">{{t('Figma 链接尚未关联。请返回详情关联文件，或选择放弃；离开不会自动提交链接。')}}</p><p v-if="saving&&!leaveSaving" role="status">{{t('当前修改正在保存，请稍候。')}}</p><p v-if="leaveError" class="field-error" role="alert">{{t(leaveError)}}</p></div><footer><button class="btn" :disabled="leaveSaving" @click="finishLeave(false)">{{t('继续编辑')}}</button><button class="btn danger" :disabled="leaveSaving||saving" @click="finishLeave(true)">{{t('放弃修改并离开')}}</button><button v-if="!commentDraftDirty&&!resourceDirty&&canEdit" class="btn primary" :disabled="saving||mediaBusy||leaveSaving" @click="saveAndLeave">{{leaveSaving?t('保存中…'):t('保存并离开')}}</button></footer></section></div>
  <div v-if="categoryModal" class="modal-shade category-modal-shade" @click.self="!categorySaving&&(categoryModal=null)">
   <form class="modal category-modal" role="dialog" aria-modal="true" aria-labelledby="category-dialog-title" @submit.prevent="saveCategory"><header><div><span class="eyebrow">{{ t('需求分类') }}</span><h2 id="category-dialog-title">{{categoryModal==='create'?t('创建分类'):categoryModal==='rename'?t('重命名分类'):t('删除分类')}}</h2></div><button type="button" :aria-label="t('关闭分类弹窗')" :disabled="categorySaving" @click="categoryModal=null">×</button></header><div class="modal-body"><template v-if="categoryModal==='delete'"><div class="category-delete-icon"><Icon name="folder" :size="28"/></div><p>{{t('确定删除「{name}」？',{name:categoryTarget.name})}}</p><p class="category-helper">{{t('{count} 条需求将移动到“未分类”，需求内容、评论和权重全部保留。',{count:categoryTarget.count})}}</p></template><template v-else><label for="category-name">{{ t('分类名称') }} <b>*</b></label><input id="category-name" v-model="categoryName" :disabled="categorySaving" maxlength="100" required autofocus :placeholder="t('例如：客户端 / 体验优化')"><p class="category-helper">{{ t('用于当前项目的需求归类。重命名会同步更新所有关联需求。') }}</p></template><p v-if="categoryError" class="inline-notice" role="alert">{{t(categoryError)}}</p></div><footer><button class="btn" type="button" :disabled="categorySaving" @click="categoryModal=null">{{ t('取消') }}</button><button :class="['btn',categoryModal==='delete'?'danger-category':'primary']" :disabled="categorySaving" type="submit">{{categorySaving?t('处理中…'):categoryModal==='delete'?t('删除并移至未分类'):t('保存分类')}}</button></footer></form>
  </div>
  <transition name="toast"><div v-if="toast" class="toast" role="status">{{t(toast,toastParams)}}</div></transition>
 </div>
</template>

<style scoped>
.req-title-open{display:flex;align-items:center;gap:8px;min-width:0;max-width:100%;padding:0;border:0;border-radius:3px;background:transparent;color:inherit;font:inherit;text-align:left;box-shadow:none;cursor:pointer}.req-title-open b{overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.req-title-open:focus-visible{outline:2px solid var(--primary);outline-offset:3px}
.description-read-surface{min-height:260px;min-width:0}.description-read-surface.editable{cursor:text;border-radius:6px}.description-read-surface.editable:hover{background:var(--surface-soft)}
.requirement-parent-context{flex:none;display:flex;align-items:center;gap:12px;padding:11px 28px;border-bottom:1px solid var(--line);background:var(--surface-soft,var(--surface));font-size:12px;color:var(--muted)}.requirement-parent-context>span:first-child{flex:none;font-weight:600}.requirement-parent-context>button{display:flex;align-items:center;gap:8px;min-width:0;background:transparent;border:0;color:var(--primary);padding:3px 0;font:inherit;text-align:left;cursor:pointer}.requirement-parent-context>button>span{overflow-wrap:anywhere}.requirement-parent-context>button>b{flex:none;font-size:11px}.requirement-parent-context>button:disabled{opacity:.55;cursor:wait}.requirement-parent-context>button:focus-visible{outline:2px solid var(--primary);outline-offset:3px;border-radius:3px}.requirement-parent-context>div{line-height:1.6}.requirement-parent-context .link{font:inherit}@media(max-width:700px){.requirement-parent-context{padding:10px 16px;align-items:flex-start;gap:9px}.requirement-parent-context>button{flex-wrap:wrap}}
.requirement-detail-host{height:0;width:0;overflow:visible}
.column-sort{border:0;background:transparent;padding:0;color:inherit;font:inherit;cursor:pointer;display:flex;gap:7px;align-items:center;text-align:left;width:100%}.column-sort>span{color:#afb6c6}.column-sort:hover{color:#625de0}
.detail-leave-shade{z-index:120}.detail-leave-dialog{width:min(540px,92vw)}.detail-leave-dialog h2{font-size:18px;margin:0}.detail-leave-dialog .modal-body p{font-size:13px;line-height:1.8;color:#667085}.detail-leave-dialog footer{display:flex;gap:8px;flex-wrap:wrap}.detail-leave-dialog .danger{color:#b42318;border-color:#f0c4c1;background:#fff7f6}.child-actions{display:flex;align-items:center;justify-content:space-between;gap:12px;margin-bottom:15px}.child-actions p{font-size:11px;line-height:1.7;color:#98a2b3;margin:0}.child-actions button{flex:none}.child-requirement-tree{border:1px solid #e6eaf0;border-radius:8px;overflow:hidden}.child-requirement-tree article{display:flex;align-items:center;gap:9px;min-height:45px;padding:9px 12px;border-bottom:1px solid #eef0f4;font-size:12px}.child-requirement-tree article:last-child{border-bottom:0}.child-requirement-parent{background:#fafbfc;color:#344054}.child-requirement-parent b{overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.child-requirement-parent small{margin-left:auto;color:#98a2b3;white-space:nowrap}.child-requirement-node{padding-left:30px!important}.child-requirement-node .link{min-width:0;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;text-align:left}.child-requirement-node .status{margin-left:auto;flex:none}.child-requirement-branch{color:#98a2b3;font-size:15px}.child-requirement-node .code{flex:none}
.requirement-prose{white-space:pre-wrap;overflow-wrap:anywhere}.detail-assignees{padding:12px 0;border-top:1px solid #eef0f4;border-bottom:1px solid #eef0f4;margin:12px 0}.detail-assignees>label{display:block!important;margin-bottom:8px;font-size:12px;color:#667085}.assignment-actions{display:flex;justify-content:flex-end;gap:6px;margin-top:9px}
.requirements-v4{min-width:0}.tree-hint{margin:24px 16px;color:#667085;font-size:12px;line-height:1.7}.category-tree button{text-align:left;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
.requirements-v4 .list-pane{padding:22px 24px 0;overflow:hidden}.requirements-v4 .pool-heading{padding:0 0 22px!important}.requirements-v4 .pagination{flex:none;padding:0;margin-top:8px;border-top:0}.requirements-v4 .req-viewbar>div{flex-wrap:wrap;justify-content:flex-end}@media(max-width:1080px){.requirements-v4 .list-pane{padding:18px 16px 0}}
.requirements-sidebar-content{display:flex;flex:1;min-height:0;flex-direction:column}.requirements-sidebar-content>header,.requirements-sidebar-content>nav,.requirements-sidebar-content>.category-section-title,.requirements-sidebar-content>.category-search,.requirements-sidebar-content>.new-category-link,.requirements-sidebar-content>.pool-sidebar-footer{flex-shrink:0}.requirements-sidebar-content>.category-folders{display:block;min-height:80px;overflow:auto;flex:1}.requirements-sidebar-content>.new-category-link{min-height:32px}.requirements-sidebar-content>.pool-sidebar-footer{margin-top:0;padding-top:8px}
.req-toolbar{flex-wrap:wrap;gap:8px;padding-bottom:12px}.req-toolbar select{display:block!important;max-width:200px}.req-toolbar .search{min-width:190px;max-width:250px;flex:1}.req-toolbar .search input{min-width:0;width:100%}
.req-viewbar{display:flex;align-items:center;justify-content:space-between;gap:12px;padding:10px 0 14px;font-size:12px;color:#667085}.req-viewbar>div{display:flex;align-items:center;gap:8px}.req-viewbar select{border:1px solid var(--line);border-radius:6px;padding:7px;background:white;color:#475467}.req-viewbar .btn{display:inline-flex;align-items:center;gap:6px}.req-viewbar small{background:#efefff;color:#5b5ce2;border-radius:4px;padding:1px 5px}.list-heading .btn{display:inline-flex;align-items:center;gap:6px}
.configurable-table{table-layout:fixed;border-collapse:separate;border-spacing:0}.configurable-table th,.configurable-table td{height:46px;max-width:none;overflow:hidden;text-overflow:ellipsis;padding:10px 12px;border-bottom:1px solid #eaecf0}.configurable-table th{height:40px}.configurable-table .sticky-code,.configurable-table .sticky-title{position:sticky;background:#fff;z-index:2}.configurable-table .sticky-code{left:0}.configurable-table .sticky-title{left:112px;box-shadow:1px 0 #eaecf0}.configurable-table th.sticky-code,.configurable-table th.sticky-title{background:#fafbfc;z-index:4}.configurable-table tr:hover td,.configurable-table tr.selected td{background:#f5f4ff}
.req-title-link{display:flex;align-items:center;gap:8px;width:100%;padding:0;border:0;background:none;color:#273249;text-align:left;font-size:13px;cursor:pointer}.req-title-link b{overflow:hidden;text-overflow:ellipsis;white-space:nowrap;font-weight:500}.req-title-link .type-icon{flex:none}.requirement-tree-toggle{display:grid;place-items:center;flex:none;width:18px;height:18px;padding:0;border:0;border-radius:4px;background:transparent;color:#667085;cursor:pointer;font:inherit;font-size:15px;line-height:1}.requirement-tree-toggle:hover,.requirement-tree-toggle:focus-visible{background:#eeedff;color:#514cc7;outline:0}.requirement-tree-branch{flex:none;width:18px;color:#98a2b3;font-size:14px}.requirement-child-row td{background:#fcfcfd}.numeric-cell{font-variant-numeric:tabular-nums}.total-value{color:#514cc7;font-variant-numeric:tabular-nums}
.req-tags{display:flex;align-items:center;gap:5px;overflow:hidden}.colored-tag{display:inline-block;padding:3px 7px;border-radius:4px;border:1px solid;font-size:11px;line-height:16px;max-width:150px;overflow:hidden;text-overflow:ellipsis;flex-shrink:0}
.requirement-drawer{width:min(1220px,90vw)}.drawer-title h2{overflow-wrap:anywhere;font-size:19px}.drawer-title>div{display:flex;align-items:center;flex-shrink:0}.detail-time{font-size:11px;color:#667085;margin:9px 0 0}.detail-alert{margin:0 20px 10px}.detail-main{min-width:0;padding:24px}.detail-props{width:280px;flex:none;padding:20px}.detail-props fieldset{border:0;padding:0;margin:0;min-width:0}.detail-props label{grid-template-columns:80px minmax(0,1fr)}.detail-props select{min-width:0}.detail-audit{font-size:12px;line-height:1.8;border-top:1px solid var(--line);padding-top:16px}.detail-audit dt{color:#667085}.detail-audit dd{margin:0 0 10px;color:#344054}
.assessment-heading{display:flex;align-items:center;justify-content:space-between;margin-bottom:12px;gap:12px}.assessment-heading h3{margin:0}.assessment-heading span{font-size:11px;color:#667085}.subsection-title{margin-top:22px!important}.remarks-label{display:block;margin:20px 0 8px;font-size:13px;font-weight:600}.assessment-block>textarea{width:100%;resize:vertical;border:1px solid #d0d5dd;border-radius:6px;padding:10px;color:#344054;background:#fff;font:inherit;font-size:13px}.assessment-actions{display:flex;align-items:center;gap:8px;margin-top:12px;flex-wrap:wrap}.assessment-actions>span{font-size:11px;color:#667085;margin-right:auto}.detail-props :deep(fieldset:disabled){opacity:.8}
.columns-modal{width:min(600px,90vw);max-height:85vh;display:flex;flex-direction:column}.columns-modal header{align-items:flex-start}.columns-modal h2{margin:0}.columns-modal header p{font-size:12px;line-height:1.6;color:#667085;margin:8px 20px 0 0}.column-list{overflow:auto;padding:0 22px;min-height:0}.column-list-heading{position:sticky;top:0;display:flex;justify-content:space-between;align-items:center;background:white;padding:14px 0;border-bottom:1px solid var(--line);font-size:12px;z-index:1}.column-setting-row{display:flex;align-items:center;gap:12px;min-height:44px;border-bottom:1px solid #f2f4f7}.column-setting-row>label{display:flex;align-items:center;gap:10px;flex:1;cursor:pointer;font-size:13px}.column-setting-row input{accent-color:#5b5ce2}.column-setting-row small{font-size:10px;color:#98a2b3;margin-left:auto}.column-moves{display:flex;gap:4px}.column-moves button{border:1px solid #e4e7ec;border-radius:5px;background:white;color:#667085;width:26px;height:26px}.column-moves button:disabled{opacity:.25;cursor:not-allowed}.fixed-column-label{font-size:10px;color:#98a2b3;width:56px;text-align:center}.columns-modal footer{display:flex;align-items:center;gap:8px}.columns-modal footer>span{margin-right:auto;font-size:12px;color:#667085}.column-shade{z-index:40}.columns-modal .inline-notice{margin:8px 22px}.requirements-v4 .toast{z-index:50}
.relation-list article a{font-size:13px;color:#514cc7;text-decoration:none}.relation-list article{display:flex;justify-content:space-between;gap:10px}.tabs{overflow-x:auto;flex:none}.tabs button{white-space:nowrap}.req-toolbar .svg-icon{width:16px}.req-viewbar .svg-icon,.list-heading .svg-icon{width:16px;height:16px}
@media(max-width:1250px){.requirements-v4 .category-tree{width:170px;min-width:170px}.requirement-drawer{width:94vw}.detail-props{width:260px}.req-viewbar{flex-wrap:wrap}}
@media(max-width:1080px){.requirements-v4 .category-tree{display:none}.detail-main{padding:18px}.detail-props{width:235px}.assessment-heading{align-items:flex-start;flex-direction:column;gap:0}}
.requirements-v4 .requirements-sidebar{display:flex;flex-direction:column;width:224px;min-width:224px;padding:20px 12px 12px;background:#fbfcfe;overflow-y:auto;border-right:1px solid #e6eaf0;transition:width .16s ease,min-width .16s ease,padding .16s ease}.requirements-v4 .requirements-sidebar>:deep(.sidebar-collapse-toggle){align-self:flex-end;margin:-8px -2px 10px}.requirements-v4 .requirements-sidebar.is-collapsed{width:50px;min-width:50px;padding:12px}.requirements-v4 .requirements-sidebar.is-collapsed>:deep(.sidebar-collapse-toggle){align-self:center;margin:0}.pool-brand{display:flex;align-items:center;gap:10px;padding:0 8px 22px}.pool-brand>span{display:grid;place-items:center;width:34px;height:34px;background:#eeedff;color:#5b5ce2;border-radius:10px}.pool-brand b{font-size:15px;color:#1d2939;letter-spacing:.02em}.pool-brand small{display:block;margin-top:4px;font-size:10px;color:#98a2b3}.pool-views{display:grid;gap:4px;padding-bottom:18px;border-bottom:1px solid #e8ecf2}.requirements-sidebar .pool-views>button{display:flex;align-items:center;gap:10px;width:100%;padding:10px 12px;min-height:38px;border:0;border-radius:7px;background:none;color:#667085;font-size:13px;text-align:left}.pool-views button>span{flex:1}.pool-views button em{font-style:normal;font-size:10px;padding:2px 6px;border-radius:4px;background:#f2f4f7;color:#667085}.requirements-sidebar .pool-views>button:hover{background:#f0f2f7}.requirements-sidebar .pool-views>button.active{color:#514cc7;background:#eeedff;font-weight:600}.pool-views button.active em{background:#e1dfff;color:#514cc7}.category-section-title{display:flex;justify-content:space-between;align-items:center;padding:20px 10px 10px;font-size:11px;font-weight:600;color:#98a2b3;letter-spacing:.08em}.requirements-sidebar .category-section-title button{width:24px;height:24px;padding:4px;border:0;border-radius:5px;background:none;color:#98a2b3}.requirements-sidebar .category-section-title button:hover{color:#5b5ce2;background:#eeedff}.category-search{display:flex;align-items:center;gap:6px;margin:0 6px 10px;border:1px solid #e4e7ec;border-radius:6px;background:#fff;color:#98a2b3;padding:7px 8px}.category-search input{width:100%;min-width:0;border:0;background:none;outline:0;font-size:11px;color:#475467}.category-folders{display:grid;gap:3px}.category-folder{border-radius:6px}.category-folder-row{display:flex;align-items:center;min-width:0}.requirements-sidebar .category-select{min-width:0;flex:1;display:flex;align-items:center;gap:8px;margin:0;padding:10px 8px;border:0;border-radius:6px;background:none;text-align:left;font-size:12px;color:#667085}.category-select .svg-icon{color:#a1aabb;flex:none}.category-select>span{flex:1;min-width:0;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.category-select em{font-style:normal;font-size:10px;color:#98a2b3}.category-folder.active{background:#eeedff}.category-folder.active .category-select,.category-folder.active .category-select .svg-icon{color:#514cc7}.category-folder:hover{background:#f0f2f7}.requirements-sidebar .category-menu-trigger{flex:none;width:26px;padding:5px;border:0;border-radius:4px;background:none;color:#98a2b3}.category-menu-trigger:hover{color:#5b5ce2;background:white}.category-actions{display:flex;padding:3px 7px 8px;gap:4px}.requirements-sidebar .category-actions button{display:flex;justify-content:center;align-items:center;gap:4px;flex:1;width:auto;border:1px solid #e4e7ec;padding:6px 4px;border-radius:5px;background:white;font-size:10px;color:#667085}.category-actions button:last-child{color:#b54751}.requirements-sidebar .new-category-link{display:flex;align-items:center;gap:8px;width:auto;margin:12px 6px;padding:8px 10px;border:1px dashed #d9ddec;border-radius:6px;background:none;color:#7773b8;font-size:11px}.requirements-sidebar .new-category-link:hover{background:#eeedff;color:#514cc7}.pool-sidebar-footer{display:flex;align-items:center;gap:6px;margin-top:auto;padding:22px 8px 4px;color:#a3aab8;font-size:9px}.no-categories{font-size:11px;color:#98a2b3;padding:14px;text-align:center}.pool-heading{align-items:center;padding-bottom:22px!important;margin-bottom:0!important}.pool-breadcrumb{display:flex;align-items:center;gap:10px;color:#98a2b3;font-size:10px;margin-bottom:11px}.pool-heading h1{display:flex;align-items:center;gap:10px;font-size:24px;margin:0;line-height:1.4;color:#1d2939}.pool-heading h1 small{padding:2px 7px;border:1px solid #e7e9f1;border-radius:6px;font-size:11px;font-weight:500;color:#7775aa;background:#f8f8fe}.pool-heading p{font-size:11px;color:#98a2b3;margin:7px 0 0}.pool-heading>.btn{min-height:36px;padding:8px 14px;box-shadow:none!important;background:#5b5ce2!important}.requirements-v4 .req-toolbar{padding:12px;background:#fafbfe;border:1px solid #e8ecf2;border-radius:8px;flex-wrap:wrap}.requirements-v4 .req-toolbar .search{background:white;border:1px solid #e4e7ec;border-radius:6px;height:34px;min-width:180px}.requirements-v4 .req-toolbar select{background:white;height:34px;border-color:#e4e7ec;color:#667085;font-size:11px}.requirements-v4 .req-viewbar{padding:14px 0 12px}.requirements-v4 .table-wrap{border:1px solid #e6eaf0;border-radius:8px}.requirements-v4 .configurable-table th{font-size:11px;font-weight:500;color:#7a8498;background:#fafbfc}.requirements-v4 .configurable-table td{font-size:12px;height:49px}.requirements-v4 .req-viewbar .link{font-size:11px}.category-modal-shade{z-index:45}.category-modal{width:min(460px,90vw)}.category-modal h2{margin:7px 0 0;font-size:19px}.category-modal .modal-body{padding:24px}.category-modal label{display:block;font-size:12px;color:#475467;margin-bottom:8px}.category-modal label b{color:#d92d20}.category-modal input{width:100%;border:1px solid #d0d5dd;border-radius:7px;padding:10px;font-size:13px;color:#344054}.category-helper{font-size:12px;line-height:1.8;color:#667085}.category-delete-icon{display:grid;place-items:center;background:#fff1f0;color:#d95b64;width:52px;height:52px;border-radius:14px;margin-bottom:18px}.danger-category{background:#d64550;color:white;border-color:#d64550}.category-modal footer{gap:8px}.comment-mentions{display:flex;gap:5px;flex-wrap:wrap;font-size:10px;color:#5b5ce2}.comment-mentions span{background:#f1f0ff;border-radius:4px;padding:2px 5px}@media(max-width:1250px){.requirements-v4 .requirements-sidebar{width:208px;min-width:208px}.requirements-v4 .requirements-sidebar.is-collapsed{width:50px;min-width:50px}.pool-heading h1{font-size:22px}.pool-heading p{max-width:390px;line-height:1.6}}@media(max-width:1080px){.requirements-v4 .requirements-sidebar{display:flex;width:180px;min-width:180px;padding:16px 8px}.requirements-v4 .requirements-sidebar.is-collapsed{width:50px;min-width:50px;padding:12px}.category-actions{flex-wrap:wrap}.pool-heading p{max-width:320px}.pool-sidebar-footer{font-size:8px;gap:4px}}
</style>

<style scoped>
.description-read-surface{min-height:260px;min-width:0}.description-read-surface.editable{cursor:text;border-radius:6px}.description-read-surface.editable:hover{background:var(--surface-soft)}
.requirement-drawer { container: requirement-detail / inline-size; }
.requirement-drawer .drawer-head { flex: none; padding: 22px 28px 18px; border-bottom: 1px solid #e7eaf0; }
.drawer-width-hint { margin-left: auto; color: #a0a8b8; font-size: 10px; }
.requirement-drawer .drawer-title { gap: 20px; align-items: flex-start; }
.requirement-drawer .drawer-title h2 { flex: 1; min-width: 0; line-height: 1.5; font-size: 22px; }
.requirement-drawer .drawer-title > div { gap: 6px; padding-top: 9px; }
.requirement-drawer .drawer-title button { display: inline-flex; align-items: center; gap: 5px; border-radius: 6px; font-size: 12px; }
.requirement-drawer .drawer-title button:hover, .requirement-drawer .drawer-title button[aria-pressed=true] { color: #514cc7; background: #f0efff; }
.detail-workspace { position: relative; min-width: 0; overflow: hidden; }
.detail-split { flex: 1; min-width: 0; min-height: 0; width: 100%; }
.detail-split :deep(.split-main) { overflow: hidden; }
.detail-reading { display: flex; min-width: 0; min-height: 0; height: 100%; }
.detail-navigation { display: flex; flex-direction: column; flex: 0 0 166px; padding: 22px 12px 16px; border-right: 1px solid #e9edf3; background: #fafbfe; overflow-y: auto; }
.detail-nav-caption { display: block; padding: 0 10px 10px; color: #98a2b3; font-size: 10px; letter-spacing: .07em; }
.detail-navigation button { display: flex; align-items: center; gap: 9px; width: 100%; padding: 11px 10px; margin-bottom: 4px; border: 0; border-radius: 7px; background: transparent; color: #667085; text-align: left; font-size: 12px; white-space: nowrap; }
.detail-navigation button > span { flex: 1; }
.detail-navigation button small { color: #98a2b3; font-size: 10px; font-variant-numeric: tabular-nums; }
.detail-navigation button:hover { background: #f0f2f8; }
.detail-navigation button.active { background: #eeedff; color: #514cc7; font-weight: 600; }
.detail-navigation button.active small { color: #7770cc; }
.detail-navigation button:focus-visible { outline: 2px solid #7c75e8; outline-offset: 1px; }
.detail-tag-section { border-top: 1px solid #e5e8f0; margin-top: 17px; padding-top: 20px; }
.detail-nav-summary { margin: auto 10px 0; padding-top: 30px; display: grid; gap: 8px; color: #98a2b3; font-size: 10px; }
.detail-nav-summary strong { color: #625de0; font-size: 24px; font-weight: 600; font-variant-numeric: tabular-nums; }
.detail-nav-summary small { color: #aa7017; font-size: 10px; }
.requirement-drawer .detail-main { min-width: 0; min-height: 0; padding: 30px 38px; }
.detail-overview { width: 100%; }
.description-block { min-height: min(52vh, 480px); padding-bottom: 30px; }
.description-block .assessment-heading { align-items: center; flex-direction: row; margin-bottom: 22px; }
.description-block h3 { font-size: 18px; color: #263247; }
.description-block .requirement-prose { white-space: pre-wrap; font-size: 14px; line-height: 1.95; color: #475467; margin: 0; }
.description-block :deep(textarea) { min-height: 350px; line-height: 1.9; font-size: 14px; }
.detail-pane-heading { padding-bottom: 20px; }
.detail-pane-heading h3 { margin: 13px 0 8px; font-size: 20px; color: #263247; }
.detail-pane-heading p { color: #98a2b3; font-size: 12px; margin: 0; }
.detail-tag-pane { border-bottom: 0; }
.comment-post-actions { display: flex; justify-content: space-between; align-items: center; gap: 12px; padding: 12px 0; }
.comment-post-actions > span { color: #98a2b3; font-size: 11px; }
.comment > div { min-width: 0; flex: 1; }
.comment-reply-button,.comment-refresh{font-size:11px;min-height:32px;background:none;border:0;color:var(--primary,#665fe8);cursor:pointer;padding:6px 0}.comment-refresh{margin-left:12px;font-weight:400}.comment-reply-button:disabled,.comment-refresh:disabled{opacity:.5;cursor:not-allowed}.comment-reply-button:focus-visible,.comment-refresh:focus-visible{outline:2px solid var(--primary,#665fe8);outline-offset:2px}@media(max-width:640px){.comment-reply-button,.comment-refresh{min-height:40px}.comment>div>time{display:block;margin-left:0}}
.detail-tag-pane > .assessment-actions { padding-top: 20px; border-top: 1px solid #eef0f4; margin-top: 25px; }
/* 分隔组件负责滚动与宽度，字段本身只占本列；长姓名/选项不能扩大右栏。 */
.requirement-drawer .detail-props { width: 100%; min-width: 0; max-width: 100%; overflow: visible; border-left: 0; padding: 26px 22px; background: var(--surface-soft, #fcfdff); box-sizing: border-box; overflow-wrap: anywhere; }
.detail-props > fieldset, .detail-props :deep(.custom-fields > *) { min-width: 0; max-width: 100%; }
.detail-props > fieldset > label, .detail-props .requirement-state-control { display: grid; grid-template-columns: minmax(0, 1fr); gap: 7px; margin-bottom: 16px; }
.detail-props :deep(.custom-fields.inline) { display: grid; grid-template-columns: minmax(0, 1fr); gap: 8px; margin-bottom: 16px; }
.detail-props :deep(.custom-fields > label) { display: block; margin: 8px 0 0; overflow-wrap: anywhere; }
.detail-props :deep(input:not([type=checkbox])), .detail-props :deep(select), .detail-props :deep(textarea) { min-width: 0; max-width: 100%; width: 100%; box-sizing: border-box; }
.detail-props :deep(.option-checks) { display: grid; gap: 8px; }
.detail-props :deep(.option-checks label), .detail-props :deep(.custom-fields > label.check) { display: flex; align-items: flex-start; gap: 8px; margin: 0; }
.detail-props :deep(input[type=checkbox]) { width: auto; flex: none; }
.detail-props .assignment-actions { flex-wrap: wrap; }
.detail-props .assignment-actions > button { max-width: 100%; white-space: normal; }
.detail-audit { min-width: 0; }
.detail-audit dd { min-width: 0; overflow-wrap: anywhere; }
.child-drawer-shade { z-index: 60; background: #17243b1c; }
.child-requirement-drawer { border-left: 1px solid #dcdfea; }
.child-requirement-drawer :deep(.requirement-editor) { height: 100%; min-height: 0; }
@container requirement-detail (max-width: 1060px) {
 .requirement-drawer .detail-main { padding: 26px; }
 .detail-navigation { flex-basis: 148px; padding: 20px 8px; }
 .drawer-width-hint { display: none; }
}
@container requirement-detail (max-width: 760px) {
 .requirement-drawer .drawer-head { padding: 16px 18px; }
 .requirement-drawer .drawer-title { flex-direction: column; gap: 0; }
 .requirement-drawer .drawer-title h2 { font-size: 19px; }
 .requirement-drawer .drawer-title > div { padding-top: 6px; }
 .detail-workspace { flex-direction: column; }
 .detail-navigation { flex: none; flex-direction: row; gap: 5px; width: 100%; padding: 9px 12px; border-right: 0; border-bottom: 1px solid #e9edf3; overflow-x: auto; }
 .detail-navigation button { width: auto; flex: none; padding: 9px; margin: 0; }
 .detail-tag-section { display: flex; flex: none; gap: 5px; padding: 0 0 0 5px; margin: 0; border: 0; border-left: 1px solid #e5e8f0; }
 .detail-nav-caption, .detail-nav-summary { display: none; }
 .requirement-drawer .detail-main { padding: 22px 18px; }
 .description-block { min-height: 350px; }
 .description-block :deep(textarea) { min-height: 300px; }
 .child-actions { align-items: flex-start; flex-direction: column; }
}
/* 窄抽屉由同一个分隔组件切为单列，导航横排；字段在正文之后而非绝对定位覆盖。 */
@container requirement-detail (max-width: 820px) {
 .requirement-drawer .drawer-title{flex-wrap:wrap;gap:8px}
 .requirement-drawer .drawer-title>div{flex-wrap:wrap;max-width:100%;flex-shrink:1;gap:5px}
 .requirement-drawer .drawer-title>div>button,.requirement-drawer .drawer-title>div>a{min-height:38px}
 .requirement-drawer .drawer-kicker{flex-wrap:wrap}
 /* 抽屉宽度落入移动端时由 ResizableSplit 改为上下内容流。外层不能继续保留
    桌面 flex/hidden，否则正文与基础信息会被裁掉，也会让右栏看起来漂移。 */
 .detail-workspace{display:block;overflow:auto;overscroll-behavior:contain}
 .detail-split :deep(.split-main){overflow:visible}
 .requirement-drawer .detail-props{position:static;width:100%;max-width:100%;overflow:visible}
 .detail-reading{display:block;height:auto;min-height:0}
 .detail-navigation{position:sticky;top:0;z-index:2;display:flex;flex-direction:row;gap:5px;flex:none;max-width:100%;padding:9px 12px;overflow:auto;border-right:0;border-bottom:1px solid var(--line);background:var(--surface)}
 .detail-navigation button{width:auto;flex:none;padding:10px;margin:0}
 .detail-tag-section{display:flex;flex:none;gap:5px;padding:0 0 0 5px;margin:0;border:0}
 .detail-nav-caption,.detail-nav-summary{display:none}
 .requirement-drawer .detail-main{overflow:visible;width:100%;min-width:0;padding:20px 16px}
 .requirement-drawer .detail-props{width:100%;max-width:100%;padding:20px 16px}
 .description-block{min-height:240px}
 .assessment-heading,.comment-post-actions,.child-actions,.relation-list article{flex-wrap:wrap;gap:10px}
 .comment-post-actions>span{flex-basis:100%}
 .relation-list article>.link,.relation-list article>a{min-width:0;overflow-wrap:anywhere}
 .assessment-actions>span{flex-basis:100%}
 .inline-add input{min-width:0}
}
@media(max-width:820px){
 .requirements-v4{display:flex;flex-direction:column;height:auto;min-height:100%}
 .requirements-v4 .requirements-sidebar{width:100%;min-width:0;border-right:0;border-bottom:1px solid var(--line);padding:12px;overflow:visible}
 .requirements-sidebar>.category-folders{display:flex;max-width:100%;min-height:0;overflow:auto;flex:none;gap:7px}
 .category-folder{flex:0 0 170px}
 .requirements-sidebar .pool-views{display:flex;max-width:100%;overflow:auto;padding-bottom:10px}
 .requirements-sidebar .pool-views>button{width:auto;flex:none}
 .pool-brand,.pool-sidebar-footer{display:none}
 .requirements-v4 .list-pane{padding:16px;overflow:visible;width:100%}
 .requirements-v4 .pool-heading{flex-wrap:wrap;gap:12px}
 .requirements-v4 .req-viewbar>div{justify-content:flex-start}
 .requirements-v4 .table-wrap{max-width:100%;overflow:auto}
 .configurable-table .sticky-code,.configurable-table .sticky-title{position:static!important}
 .pagination{flex-wrap:wrap;min-height:50px;height:auto}
 .pagination>span{margin-right:auto;overflow-wrap:anywhere}
}
</style>

<style scoped>
.description-read-surface{min-height:260px;min-width:0}.description-read-surface.editable{cursor:text;border-radius:6px}.description-read-surface.editable:hover{background:var(--surface-soft)}
/*
 * 需求池局部视觉覆盖：列表、看板与详情使用同一组 shadcn 语义表面。
 * 这里不修改筛选、保存或抽屉的行为，仅在本组件最终样式层替换旧的固定浅色。
 */
.requirements-v4 .req-toolbar{background:var(--secondary);border-color:var(--border);box-shadow:none}
.requirements-v4 .req-toolbar .search,.requirements-v4 .req-toolbar select{background:var(--card);border-color:var(--input);color:var(--foreground)}
.requirements-v4 .req-viewbar select{background:var(--card);border-color:var(--input);color:var(--foreground)}
.requirements-v4 .req-viewbar small{background:var(--accent);color:var(--accent-foreground);border:1px solid color-mix(in srgb,var(--primary) 16%,var(--border))}
.requirements-v4 .table-wrap,.requirements-v4 .configurable-table{background:var(--card);border-color:var(--border);box-shadow:none}
.requirements-v4 .configurable-table th{background:var(--secondary);border-color:var(--border);color:var(--muted-foreground);font-weight:600}
.requirements-v4 .configurable-table td{background:var(--card);border-color:color-mix(in srgb,var(--border) 78%,var(--background));color:var(--foreground)}
.requirements-v4 .configurable-table :is(.sticky-code,.sticky-title){background:var(--card);box-shadow:1px 0 0 var(--border)}
.requirements-v4 .configurable-table th:is(.sticky-code,.sticky-title){background:var(--secondary)}
.requirements-v4 .configurable-table tr:not(.released-requirement-row):hover td,.requirements-v4 .configurable-table tr.selected td{background:color-mix(in srgb,var(--primary) 7%,var(--background))}
.requirements-v4 .configurable-table tr:not(.released-requirement-row):hover :is(.sticky-code,.sticky-title),.requirements-v4 .configurable-table tr.selected :is(.sticky-code,.sticky-title){background:color-mix(in srgb,var(--primary) 7%,var(--background))}
.req-title-link{color:var(--foreground)}.req-title-link:is(:hover,:focus-visible){color:var(--primary)}.total-value{color:var(--accent-foreground)}
.requirement-status-board{gap:16px;padding:8px 2px 20px;color:var(--foreground)}
.requirement-status-board>section{background:var(--secondary);border-color:var(--border);border-radius:var(--radius);box-shadow:none}
.requirement-status-board h3{color:var(--foreground);border-bottom:1px solid color-mix(in srgb,var(--border) 78%,var(--background));padding-bottom:10px}
/* 卡片的首行始终固定为编号、标题和显式展开动作，避免看板被标签和人员信息撑成多行。 */
.requirement-board-card{display:block;background:var(--card);border-color:var(--border);color:var(--foreground);border-radius:calc(var(--radius) - 2px);box-shadow:none;padding:0;overflow:hidden}
.requirement-board-card:is(:hover,:focus-within){background:var(--card);border-color:var(--primary);box-shadow:none}
.requirement-board-summary{display:grid;grid-template-columns:auto minmax(0,1fr) auto;align-items:center;gap:8px;min-width:0;min-height:46px;padding:8px 8px 8px 12px}
.requirement-board-summary>.requirement-code-control{min-width:0;color:var(--muted-foreground);font-size:11px}
.requirement-board-summary strong{min-width:0;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;color:var(--foreground);font-size:13px;line-height:1.5;font-weight:600}
.requirement-board-disclosure{display:inline-flex;align-items:center;gap:3px;min-height:30px;border:0;border-radius:calc(var(--radius) - 3px);padding:4px 6px;background:transparent;color:var(--muted-foreground);font-size:11px;white-space:nowrap}
.requirement-board-disclosure:hover{background:var(--accent);color:var(--accent-foreground)}
.requirement-board-disclosure:focus-visible{outline:2px solid var(--ring);outline-offset:1px}
.requirement-board-disclosure-icon{display:inline-block;font-size:15px;line-height:1;transition:transform .16s ease}
.requirement-board-card.expanded .requirement-board-disclosure-icon{transform:rotate(180deg)}
.requirement-board-details{display:grid;gap:9px;padding:0 12px 12px;border-top:1px solid color-mix(in srgb,var(--border) 78%,var(--background))}
.requirement-board-details .req-tags{padding-top:10px;min-height:0;flex-wrap:wrap;overflow:visible}
.requirement-board-meta{display:flex;justify-content:space-between;gap:10px;color:var(--muted-foreground);font-size:11px}
.requirement-board-meta>span{min-width:0;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
.requirement-drawer{background:var(--card);color:var(--card-foreground);box-shadow:none}
.requirement-drawer .drawer-head{background:var(--card);border-color:var(--border)}
.requirement-drawer .detail-navigation{background:var(--secondary);border-color:var(--border)}
.requirement-drawer .detail-navigation button{color:var(--muted-foreground)}
.requirement-drawer .detail-navigation button:hover{background:color-mix(in srgb,var(--primary) 7%,var(--background));color:var(--foreground)}
.requirement-drawer .detail-navigation button.active{background:var(--accent);color:var(--accent-foreground);box-shadow:inset 3px 0 0 var(--primary)}
.requirement-drawer :is(.detail-tag-section,.detail-block,.detail-audit){border-color:var(--border)}
.requirement-drawer :is(.detail-main,.detail-reading){background:var(--card);color:var(--card-foreground)}
.requirement-drawer .detail-props{background:var(--secondary);border-color:var(--border);color:var(--foreground)}
.requirement-drawer .detail-props :is(input,select,textarea){background:var(--background);border-color:var(--input);color:var(--foreground)}
.requirement-drawer :is(.detail-time,.detail-nav-caption,.detail-nav-summary,.detail-pane-heading p,.detail-assignees>label,.detail-audit dt,.detail-block p){color:var(--muted-foreground)}
.requirement-drawer :is(.detail-block h3,.drawer-title h2){color:var(--foreground)}.requirement-drawer .detail-nav-summary strong{color:var(--primary)}
@media(max-width:820px){.requirement-status-board{gap:12px;padding-bottom:16px}.requirement-board-summary{min-height:48px;padding:8px 8px 8px 12px}.requirement-board-disclosure{min-height:34px;padding:5px}.requirement-board-disclosure>span:first-child{position:absolute;width:1px;height:1px;padding:0;margin:-1px;overflow:hidden;clip:rect(0,0,0,0);white-space:nowrap;border:0}.requirement-board-disclosure-icon{font-size:18px}.requirement-board-details{padding:0 12px 12px}}
</style>

<style scoped>
.description-read-surface{min-height:260px;min-width:0}.description-read-surface.editable{cursor:text;border-radius:6px}.description-read-surface.editable:hover{background:var(--surface-soft)}
/* 分类排序不使用拖拽：提交完整目录快照前始终保持当前顺序，减少筛选或并发时的误操作。 */
.category-actions{align-items:center}
.category-reorder-actions{display:flex;flex:none;gap:2px}
.requirements-sidebar .category-actions .category-move{flex:none;width:24px;min-height:26px;padding:3px;font-size:13px;line-height:1}
.requirements-sidebar .category-actions .category-move:disabled{opacity:.38;cursor:not-allowed}
.requirements-sidebar .category-actions button:focus-visible{outline:2px solid var(--ring,#625de0);outline-offset:1px}
</style>

<script setup lang="ts">
import RequirementRefinement from '../components/RequirementRefinement.vue'
import {plainTextToDocument} from '../richText'
import { t, categoryLabel, formatDate as formatCreatedAt } from '../i18n'
import { computed, nextTick, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { onBeforeRouteLeave, onBeforeRouteUpdate, useRoute, useRouter } from 'vue-router'
import { api } from '../api'
import { useLayoutBoolean } from '../layoutScope'
import CustomFieldInputs from '../components/CustomFieldInputs.vue'
import AppSelect from '../components/AppSelect.vue'
import DatePicker from '../components/DatePicker.vue'
import RequirementTags from '../components/RequirementTags.vue'
import RequirementWeights from '../components/RequirementWeights.vue'
import MentionComment from '../components/MentionComment.vue'
import RichTextEditor from '../components/RichTextEditor.vue'
import RequirementTitleAssistant from '../components/RequirementTitleAssistant.vue'
import RequirementTemplates from '../components/RequirementTemplates.vue'
import RequirementSuggestions from '../components/RequirementSuggestions.vue'
import MemberMultiSelect from '../components/MemberMultiSelect.vue'
import ResizableSplit from '../components/ResizableSplit.vue'
import WorkItemDrafts from '../components/WorkItemDrafts.vue'
import WorkItemPdfExport from '../components/WorkItemPdfExport.vue'
import { normalizeMentionIds, retainMentionIds, unavailableNewMentionIds } from '../mentions'
import { emptyRoleWeights, normalizeRoleWeights, normalizeSprints, roleWeightDefinitions, splitTags, sprintSelectable, validTagColor, type RequirementMember, type RequirementSprint, type RequirementTagOption } from '../requirementFields'

const props = defineProps<{ embedded?: boolean; parentId?: number; requirementId?: number; restoreId?: string }>()
const emit = defineEmits<{ (event: 'created', requirement: any, again: boolean): void; (event: 'saved', requirement: any): void; (event: 'cancel'): void }>()
const refinementBusy=ref(false)
const route = useRoute(), router = useRouter()
const saving = ref(false), loading = ref(true), referencesLoading = ref(false), error = ref(''), referenceError = ref(''), notice = ref('')
const descriptionMediaBusy=ref(false)
const titleAssistant=ref<{generate:()=>Promise<boolean>;cancel:()=>void}|null>(null),titleGenerating=ref(false),canConfigureAI=ref(false),aiActorKey=ref('')
const descriptionDraftRevision=ref(0)
// 全屏只改变富文本编辑器的呈现层；表单草稿仍由同一个组件实例维护。
const descriptionFullscreen=ref(false)
const draftRecovery=ref<{saveNow:()=>Promise<boolean>;complete:()=>Promise<void|boolean>}|null>(null),draftSaving=ref(false)
const baseline = ref(''), initialized = ref(false)
const invalidWeightField = ref('')
const errorText = computed(() => t(error.value, {field:t(invalidWeightField.value)}))
const canEdit = ref(false)
const currentUserId=ref(''), tagOptions=ref<RequirementTagOption[]>([]), tagError=ref('')
const members = ref<RequirementMember[]>([]), sprints = ref<RequirementSprint[]>([]), parents = ref<any[]>([]), categories = ref<{ id: number; name: string; count?: number }[]>([])
const editingID = computed(() => props.embedded ? props.requirementId : route.params.id)
const edit = computed(() => !!editingID.value)
// 新建先收录诉求；切换展开状态只改变布局，保留所有字段及私有草稿。
const moreFields = useLayoutBoolean('requirement-editor.more-fields', false)
const expanded = computed(() => edit.value || moreFields.value)
const parentContext = ref<any>(null)
const formElement = ref<HTMLFormElement | null>(null)
const editorDatesValid=reactive({start:true,end:true,business:true,testers:true,custom:true})
const savedDescriptionMentionIds = ref<string[]>([]), savedRemarksMentionIds = ref<string[]>([]), savedAssigneeIds = ref<string[]>([])
const assigneesTouched = ref(false)
const savedOwnerIds=ref<string[]>([]), ownersTouched=ref(false)
let referenceRequest: Promise<void> | null = null
let loadSequence = 0, disposed = false, navigatingAfterSave = false, loadedProject = ''
function defaults() { return { type: '产品需求', title: '', description: '', descriptionDoc: null, descriptionMentionUserIds: [], descriptionMentionNames: {}, acceptance: '', parentId: null, category: '未分类', sprint: '待规划', status: '草稿', priority: 'P2', owner: '', ownerUserIds: [], owners: [], assignee: '', assigneeUserIds: [], assignees: [], tags: '', tagColors: {}, remarks: '', remarksMentionUserIds: [], remarksMentionNames: {}, roleWeights: emptyRoleWeights(), startDate: '', endDate: '', sensitive: false, authImpact: false, customFields: {}, createdAt: '' } }
const f = reactive<any>(defaults())
function applyRequirementTemplate(value:any,replace:boolean){
 if(saving.value||descriptionMediaBusy.value||!canEdit.value)return
 titleAssistant.value?.cancel()
 if(value.description&&(replace||(!f.description?.trim()&&!f.descriptionDoc?.content?.some((node:any)=>node.type==='image'||node.type==='attachment')))){
  f.description=value.description;f.descriptionDoc=value.descriptionDoc;f.descriptionMentionUserIds=[];f.descriptionMentionNames={};descriptionDraftRevision.value++
 }
 if(value.acceptance&&(replace||!f.acceptance?.trim()))f.acceptance=value.acceptance
 if(value.remarks&&(replace||!f.remarks?.trim())){f.remarks=value.remarks;f.remarksMentionUserIds=[];f.remarksMentionNames={}}
 if(value.roleWeights){
  const incoming=normalizeRoleWeights(value.roleWeights)
  for(const {key,roles} of roleWeightDefinitions){
   const next=incoming[key],current=f.roleWeights[key]
   if(next.value!==null&&(replace||current.value===null))current.value=next.value
   const ids=(next.userIds||[]).filter((id:string)=>members.value.some(m=>m.id===id&&m.active!==false&&roles.includes(m.projectRole||m.role||'')))
   if(ids.length&&(replace||!current.userIds?.length)){current.userIds=ids;current.userId=ids[0]}
  }
 }
 const owners=(value.ownerUserIds||[]).filter((id:string)=>members.value.some(m=>m.id===id&&m.active!==false&&(m.projectRole||m.role)==='product'))
 if(owners.length&&(replace||!f.ownerUserIds?.length)){f.ownerUserIds=owners;ownersTouched.value=true}
 const testers=(value.testerUserIds||[]).filter((id:string)=>members.value.some(m=>m.id===id&&m.active!==false&&(m.projectRole||m.role)==='qa'))
 if(testers.length&&(replace||!f.customFields.testers?.length))f.customFields.testers=testers
 if(value.acceptance||value.remarks||value.roleWeights||owners.length||testers.length)moreFields.value=true
 notice.value='模板已填入当前草稿，请检查后保存需求'
}
const sameTitleItems = computed(() => {
  const normalize = (value: string) => value.normalize('NFKC').trim().replace(/\s+/g,' ').toLocaleLowerCase()
  const title = normalize(f.title || '')
  return title ? parents.value.filter(item=>Number(item.id)!==Number(editingID.value)&&normalize(String(item.title||''))===title).slice(0,3) : []
})
// 私有草稿只保存可编辑字段；主键、服务器版本和状态不随恢复内容被覆盖。
const draftFields=Object.keys(defaults()).filter(key=>!['createdAt','status'].includes(key))
const draftPayload=computed(()=>Object.fromEntries(draftFields.map(key=>[key,f[key]])))
const draftContext=computed(()=>({parentId:f.parentId,baselineUpdatedAt:typeof f.updatedAt==='string'?f.updatedAt:''}))
const requestedDraftId=computed(()=>props.embedded?props.restoreId||'':typeof route.query.draft==='string'?route.query.draft:'')
function snapshot() { return JSON.stringify(f) }
const dirty = computed(() => initialized.value && (snapshot() !== baseline.value || Object.values(editorDatesValid).some(valid=>!valid) || assigneesTouched.value || ownersTouched.value || titleGenerating.value))
const availableSprints = computed(() => sprints.value.filter(sprint => sprintSelectable(sprint) || sprint.name === f.sprint).sort((a,b)=>Number(b.status==='进行中')-Number(a.status==='进行中')))
const missingSprint = computed(() => !!f.sprint && f.sprint !== '待规划' && !sprints.value.some(sprint => sprint.name === f.sprint))
const historicalSprint = computed(() => sprints.value.find(sprint => sprint.name === f.sprint && !sprintSelectable(sprint)))
const categoryOptions = computed(() => [...new Set([...categories.value.map(item => item.name), f.category].filter(Boolean))])
const parentOptions = computed(() => {
  const excluded = new Set<number>(edit.value ? [Number(editingID.value)] : [])
  let changed = true
  while (changed) {
    changed = false
    for (const parent of parents.value) if (excluded.has(Number(parent.parentId)) && !excluded.has(Number(parent.id))) { excluded.add(Number(parent.id)); changed = true }
  }
  return parents.value.filter(parent => !excluded.has(Number(parent.id)))
})

async function loadTags(){try{const data=await api<any>('/requirement-tags');if(disposed)return;tagOptions.value=data.items||[];tagError.value=''}catch(cause:any){if(!disposed)tagError.value=cause.message||'项目标签暂时无法载入，可重试或创建新标签'}}
function refreshReferences(): Promise<void> {
  if (disposed) return Promise.resolve()
  if (referenceRequest) return referenceRequest
  referencesLoading.value = true
  referenceRequest = (async () => {
    try {
      const [sprintData, memberData, parentData, categoryData] = await Promise.all([api<any>('/sprints'), api<any>('/members'), api<any>('/requirements?projection=reference'), api<any>('/requirement-categories'),loadTags()])
      if (disposed) return
      sprints.value = normalizeSprints(sprintData.items || []); members.value = memberData.items || []; parents.value = parentData.items || []; categories.value = categoryData.items || []; referenceError.value = ''
    } catch (cause: any) { if (!disposed) referenceError.value = cause.message || '无法载入当前项目的迭代和成员，请重试' }
    finally { if (!disposed) referencesLoading.value = false; referenceRequest = null }
  })()
  return referenceRequest
}
async function load() {
  if (disposed) return
  titleAssistant.value?.cancel();titleGenerating.value=false;canConfigureAI.value=false
  const sequence = ++loadSequence, embedded = !!props.embedded, requirementID = editingID.value
  const requestedParent = embedded ? props.parentId : typeof route.query.parentId === 'string' ? route.query.parentId : ''
  const requestedSprint = !embedded && typeof route.query.sprint === 'string' ? route.query.sprint : ''
  const current = () => !disposed && sequence === loadSequence && embedded === !!props.embedded && requirementID === editingID.value && (!embedded || requestedParent === props.parentId)
  initialized.value = false; loading.value = true; error.value = ''; notice.value = ''
  parentContext.value = null; loadedProject = ''
  for (const key of Object.keys(f)) delete f[key]
  Object.assign(f, defaults())
  savedDescriptionMentionIds.value = []; savedRemarksMentionIds.value = []; savedAssigneeIds.value = []; assigneesTouched.value = false; savedOwnerIds.value=[];ownersTouched.value=false
  try {
    const [session, workflow] = await Promise.all([api<any>('/session'),api<any>('/requirement-workflow'), refreshReferences()])
    if (!current()) return
    loadedProject = session.project?.id || ''
    const options = loadedProject ? { headers: { 'X-DevFlow-Project': loadedProject } } : undefined
    canEdit.value = !!session.user?.role && session.user.role !== 'viewer'
    currentUserId.value=session.user?.id||''
    canConfigureAI.value=session.canImpersonate===true&&!session.impersonation
    aiActorKey.value=JSON.stringify([session.user?.id||'',!!session.impersonation])
    if (!canEdit.value) { baseline.value = snapshot(); initialized.value = true; return }
    if (requirementID) {
      const value = await api<any>('/requirements/' + requirementID, options)
      if (!current()) return
      Object.assign(f, value, { roleWeights: normalizeRoleWeights(value.roleWeights), tagColors: value.tagColors || {}, remarks: value.remarks || '', descriptionMentionUserIds: normalizeMentionIds(value.descriptionMentionUserIds), remarksMentionUserIds: normalizeMentionIds(value.remarksMentionUserIds), descriptionMentionNames: value.descriptionMentionNames || {}, remarksMentionNames: value.remarksMentionNames || {}, assigneeUserIds: normalizeMentionIds(value.assigneeUserIds), assignees: value.assignees || [],ownerUserIds:normalizeMentionIds(value.ownerUserIds??(value.ownerUserId?[value.ownerUserId]:[])),owners:value.owners||[] })
      savedDescriptionMentionIds.value = [...f.descriptionMentionUserIds]; savedRemarksMentionIds.value = [...f.remarksMentionUserIds]; savedAssigneeIds.value = [...f.assigneeUserIds];savedOwnerIds.value=[...f.ownerUserIds]
    } else {
      f.status=workflow.initialStatus
      const parentId=Number(requestedParent)
      if(embedded || requestedParent){
        if(!Number.isSafeInteger(parentId)||parentId<1)throw new Error('父需求无效，请返回需求详情重新创建')
        const parent=await api<any>('/requirements/'+parentId, options)
        if(!current())return
        parentContext.value=parent
        f.parentId=parent.id;f.category=parent.category||'未分类'
        if(!parents.value.some(item=>item.id===parent.id))parents.value=[...parents.value,parent]
        const inherited=sprints.value.find(sprint=>sprint.name===parent.sprint&&sprintSelectable(sprint))
        if(inherited)f.sprint=inherited.name
        else if(parent.sprint&&parent.sprint!=='待规划')notice.value='父需求迭代已结束或不可用，子需求暂放待规划池'
      }
      const target = sprints.value.find(sprint => (sprint.name === requestedSprint || String(sprint.id) === requestedSprint) && sprintSelectable(sprint))
      if (target) f.sprint = target.name
    }
    baseline.value = snapshot(); initialized.value = true
  } catch (cause: any) { if (current()) error.value = cause.message || '需求载入失败' }
  finally { if (current()) loading.value = false }
}
const leaveDialog=ref<HTMLDialogElement|null>(null),leaveError=ref('')
let leavePending:Promise<boolean>|null=null,resolveLeave:((value:boolean)=>void)|null=null
function finishLeave(allowed:boolean){
 if(draftSaving.value)return
 if(allowed&&(saving.value||descriptionMediaBusy.value)){leaveError.value='正在保存或处理附件，请完成后再离开';return}
 leaveDialog.value?.close();const resolve=resolveLeave;resolveLeave=null;leavePending=null
 if(allowed)titleAssistant.value?.cancel()
 resolve?.(allowed)
}
function confirmLeave():boolean|Promise<boolean> {
 if(descriptionMediaBusy.value||draftSaving.value||saving.value&&!navigatingAfterSave){error.value='正在保存或处理附件，请完成后再离开';return false}
 if(!dirty.value){titleAssistant.value?.cancel();return true}
 if(leavePending)return leavePending
 leaveError.value='';leavePending=new Promise(resolve=>{resolveLeave=resolve})
 void nextTick(()=>{if(!disposed&&leavePending)leaveDialog.value?.showModal()})
 return leavePending
}
async function saveDraftAndLeave(){
 if(draftSaving.value||saving.value||descriptionMediaBusy.value)return
 titleAssistant.value?.cancel();draftSaving.value=true;leaveError.value=''
 let saved=false
 try{saved=await draftRecovery.value?.saveNow()===true;if(!saved)leaveError.value='草稿未保存成功，请重试或继续编辑'}catch{leaveError.value='草稿未保存成功，请重试或继续编辑'}
 finally{draftSaving.value=false}
 if(saved&&!disposed)finishLeave(true)
}
async function requestClose(): Promise<boolean> {
  if (saving.value || draftSaving.value || descriptionMediaBusy.value || disposed) return false
  if (props.embedded) {
    if (!await confirmLeave()) return false
    emit('cancel'); return true
  }
  return !(await router.push('/requirements'))
}
defineExpose({ requestClose, dirty, saving:computed(()=>saving.value||draftSaving.value||descriptionMediaBusy.value) })
onBeforeRouteLeave(confirmLeave); onBeforeRouteUpdate(confirmLeave)
const unload = (event: BeforeUnloadEvent) => { if (dirty.value||descriptionMediaBusy.value||refinementBusy.value||titleGenerating.value) { event.preventDefault(); event.returnValue = '' } }
const refreshOnFocus = () => { if (initialized.value && !saving.value) void refreshReferences() }
onMounted(() => { void load(); window.addEventListener('beforeunload', unload); window.addEventListener('focus', refreshOnFocus) })
onBeforeUnmount(() => { disposed = true; resolveLeave?.(false);resolveLeave=null;leavePending=null;leaveDialog.value?.close();descriptionFullscreen.value=false; loadSequence++; window.removeEventListener('beforeunload', unload); window.removeEventListener('focus', refreshOnFocus) })
watch([() => !!props.embedded, editingID, () => props.embedded ? props.parentId : route.query.parentId], () => { descriptionFullscreen.value=false; void load() })

function applyRefinement(value:{description:string;acceptance:string}){
 if(saving.value||loading.value||!canEdit.value)return
 if(value.description){const doc=f.descriptionDoc||plainTextToDocument(f.description,f.descriptionMentionUserIds,f.descriptionMentionNames,members.value);f.descriptionDoc={...doc,content:[...(doc.content||[]),...(plainTextToDocument(value.description).content||[])]};f.description=[f.description,value.description].filter(Boolean).join('\n\n');descriptionDraftRevision.value++}
 if(value.acceptance)f.acceptance=[f.acceptance,value.acceptance].filter(Boolean).join('\n\n')
 notice.value='已追加所选 AI 建议，请检查后保存需求'
}
function applyGeneratedTitle(title:string){if(disposed||saving.value||loading.value||!initialized.value||f.title.trim())return;f.title=title;error.value='';notice.value='标题已生成，请确认或编辑后再次保存需求'}

async function savePrivateDraft() {
  if(disposed||saving.value||draftSaving.value||refinementBusy.value||titleGenerating.value||descriptionMediaBusy.value||loading.value||!initialized.value||!canEdit.value)return
  error.value='';notice.value=''
  if(!draftRecovery.value){error.value='草稿箱尚未就绪，请稍后重试';return}
  draftSaving.value=true
  try{await draftRecovery.value.saveNow()}catch(cause:any){if(!disposed)error.value=cause.message||'草稿保存失败，编辑内容已保留'}finally{draftSaving.value=false}
}
function restoreDraft(payload:Record<string,unknown>,context:Record<string,unknown>={},draft?:{kind?:string;targetId?:string}) {
  if(disposed||saving.value||draftSaving.value||refinementBusy.value||titleGenerating.value||descriptionMediaBusy.value||loading.value||!initialized.value||!canEdit.value)return
  const fail=()=>{error.value='该草稿与当前需求不匹配或内容格式无效，未覆盖当前编辑内容'}
  if(!payload||typeof payload!=='object'||Array.isArray(payload)||draft?.kind&&draft.kind!=='requirement'||draft?.targetId!==undefined&&draft.targetId!==(edit.value?String(editingID.value):'')){fail();return}
  const parent=payload.parentId??null
  if(parent!==null&&(!Number.isSafeInteger(parent)||Number(parent)<1)){fail();return}
  if(props.embedded&&!edit.value&&(parent!==props.parentId||context.parentId!=null&&context.parentId!==props.parentId)){fail();return}
  const expected=defaults() as Record<string,unknown>,next:Record<string,unknown>={}
  // 先完整验证再一次性恢复，错误备份不能造成半份表单被覆盖。
  try{
    for(const key of draftFields){
      if(!Object.prototype.hasOwnProperty.call(payload,key))continue
      const value=payload[key],sample=expected[key]
      if(key==='parentId'){next[key]=parent;continue}
      if(key==='descriptionDoc'){if(value!==null&&(typeof value!=='object'||Array.isArray(value)||(value as any).type!=='doc'))throw Error();}
      else if(Array.isArray(sample)){if(!Array.isArray(value)||key.endsWith('UserIds')&&!value.every(item=>typeof item==='string'))throw Error();}
      else if(sample&&typeof sample==='object'){if(!value||typeof value!=='object'||Array.isArray(value))throw Error();}
      else if(typeof value!==typeof sample)throw Error()
      next[key]=JSON.parse(JSON.stringify(value))
    }
  }catch{fail();return}
  Object.assign(f,next)
  if('assigneeUserIds' in next)assigneesTouched.value=true
  if('ownerUserIds' in next)ownersTouched.value=true
  descriptionDraftRevision.value++;error.value=''
  // baseline 与已保存人员快照继续取本次服务器载入值，恢复绝不是已正式保存。
  notice.value=context.baselineUpdatedAt&&context.baselineUpdatedAt!==f.updatedAt?'草稿基于较早的需求版本，请核对当前内容后再正式保存':'草稿已恢复，尚未正式保存需求'
}

async function save(again = false, draft = false) {
  if(draft){await savePrivateDraft();return}
  if (saving.value || draftSaving.value || titleGenerating.value || descriptionMediaBusy.value || loading.value || !initialized.value || disposed) return
  error.value = ''; notice.value = ''
  if (!canEdit.value) { error.value = '当前账号为只读身份，不能创建或修改需求'; return }
  if (!f.title.trim()) { if(titleAssistant.value)await titleAssistant.value.generate();else error.value='请输入需求标题';return }
  if (!formElement.value?.checkValidity()) {
    // 必填字段可能位于折叠区，先展开再聚焦，避免浏览器报“不可聚焦”。
    moreFields.value = true
    await nextTick()
    formElement.value?.querySelectorAll?.('details').forEach(element => { element.open = true })
    formElement.value?.reportValidity()
    return
  }
  if (f.startDate && f.endDate && f.startDate > f.endDate) { error.value = '计划结束日期不能早于开始日期'; return }
  const descriptionIDs = retainMentionIds(f.description, f.descriptionMentionUserIds, members.value, f.descriptionMentionNames)
  const remarksIDs = retainMentionIds(f.remarks, f.remarksMentionUserIds, members.value, f.remarksMentionNames)
  if (unavailableNewMentionIds(descriptionIDs, members.value, savedDescriptionMentionIds.value).length || unavailableNewMentionIds(remarksIDs, members.value, savedRemarksMentionIds.value).length) { error.value = '新增提及成员暂不可用，请刷新项目成员或移除后重选'; return }
  for (const row of roleWeightDefinitions) {
    const value = f.roleWeights[row.key]?.value
    if (value !== null && value !== undefined && (!Number.isFinite(value) || value < 0 || value > 1000000)) { error.value = '{field}需为 0 至 1000000 的有效数值'; invalidWeightField.value = row.label; return }
  }
  const payload: Record<string, any> = {}
  for (const key of ['type', 'title', 'description', 'acceptance', 'parentId', 'category', 'sprint', 'status', 'priority', 'tags', 'tagColors', 'remarks', 'roleWeights', 'startDate', 'endDate', 'sensitive', 'authImpact', 'customFields']) payload[key] = f[key]
  payload.descriptionDoc = f.descriptionDoc
  payload.descriptionMentionUserIds = descriptionIDs; payload.remarksMentionUserIds = remarksIDs
  if (!edit.value || assigneesTouched.value || JSON.stringify(f.assigneeUserIds) !== JSON.stringify(savedAssigneeIds.value)) payload.assigneeUserIds = normalizeMentionIds(f.assigneeUserIds)
  if (!edit.value || ownersTouched.value || JSON.stringify(f.ownerUserIds)!==JSON.stringify(savedOwnerIds.value))payload.ownerUserIds=normalizeMentionIds(f.ownerUserIds)
  payload.title = f.title.trim(); payload.tags = splitTags(f.tags).join(',')
  payload.tagColors = Object.fromEntries(splitTags(f.tags).map(tag => [tag, validTagColor(f.tagColors[tag])]))
  payload.roleWeights = normalizeRoleWeights(f.roleWeights)
  if (props.embedded && !edit.value) payload.parentId = props.parentId
  if (!edit.value) delete payload.status // 初始状态由服务器在事务中决定；私有草稿不会走正式创建接口。
  const sequence = loadSequence, embedded = !!props.embedded, parentId = props.parentId, requirementID = editingID.value, projectId = loadedProject
  const current = () => !disposed && sequence === loadSequence && embedded === !!props.embedded && requirementID === editingID.value && (!embedded || parentId === props.parentId)
  saving.value = true
  try {
    const saved = await api<any>(requirementID ? '/requirements/' + requirementID : '/requirements', { method: requirementID ? 'PATCH' : 'POST', body: JSON.stringify(payload), headers: projectId ? { 'X-DevFlow-Project': projectId } : undefined })
    if (!current()) return
    if (saved.descriptionDoc !== undefined) f.descriptionDoc = saved.descriptionDoc
    // 只有正式接口成功后才清理对应草稿；清理失败不能被误报为需求保存失败而诱发重复创建。
    try{await draftRecovery.value?.complete()}catch{if(current())notice.value='需求已保存，但草稿清理失败，可稍后在草稿箱手动删除'}
    if(!current())return
    if (again && !requirementID) {
      descriptionDraftRevision.value++
      Object.assign(f, defaults(), { sprint: payload.sprint, category: payload.category,parentId:payload.parentId })
      savedDescriptionMentionIds.value=[];savedRemarksMentionIds.value=[];savedAssigneeIds.value=[];savedOwnerIds.value=[]
      baseline.value = snapshot(); assigneesTouched.value = false;ownersTouched.value=false; notice.value = '需求已创建，可以继续填写下一条需求'
      if (embedded) emit('created', saved, true)
      if (current()) await refreshReferences()
    } else {
      baseline.value = snapshot(); assigneesTouched.value = false;ownersTouched.value=false
      if (embedded) { if (requirementID) emit('saved', saved); else emit('created', saved, false) }
      else { navigatingAfterSave = true; await router.push('/requirements') }
    }
  } catch (cause: any) { if (current()) { error.value = cause.message || '保存失败，请重试'; moreFields.value = true; await nextTick(); if (current()) formElement.value?.querySelectorAll?.('details').forEach(element=>{element.open=true}) } }
  finally { navigatingAfterSave = false; if (!disposed) saving.value = false }
}
</script>

<template>
  <form ref="formElement" class="editor requirement-editor" :class="{ 'embedded-editor': props.embedded }" novalidate @submit.prevent="save()">
    <header class="editor-head"><div class="editor-titlebar"><h1>{{ props.embedded ? t('创建子需求') : edit ? t('编辑需求') : t('创建需求') }}</h1><span>{{ t('编辑内容会自动保存为私有草稿') }}</span></div><div class="editor-head-actions"><WorkItemPdfExport v-if="edit&&initialized&&!loading&&loadedProject" object-type="requirement" :object-id="Number(editingID)" :project-id="loadedProject" :disabled="saving||draftSaving"/><WorkItemDrafts ref="draftRecovery" compact kind="requirement" :target-id="edit?String(editingID):''" :context="draftContext" :payload="draftPayload" :ready="initialized&&canEdit&&!loading" :dirty="dirty" :busy="saving||descriptionMediaBusy||refinementBusy||titleGenerating" :restore-id="requestedDraftId" @restore="restoreDraft" /><button type="button" class="btn ghost" :disabled="saving" @click="requestClose">{{ t('取消') }}</button></div></header>
    <div v-if="loading" class="state"><span class="spinner"></span>{{ t('正在载入需求与当前项目…') }}</div>
    <div v-else-if="!initialized" class="state error"><p>{{ errorText }}</p><div><button type="button" class="btn" @click="load">{{ t('重新载入') }}</button> <button type="button" class="btn" @click="requestClose">{{ props.embedded ? t('取消') : t('返回需求列表') }}</button></div></div>
    <div v-else-if="!canEdit" class="state"><h2>{{ t('当前账号为只读身份') }}</h2><p>{{ t('可以浏览需求与评估信息，创建或编辑需要项目写入权限。') }}</p><div><button type="button" class="btn" @click="props.embedded ? requestClose() : router.push(edit ? '/requirements?req=' + route.params.id : '/requirements')">{{ props.embedded ? t('取消') : edit ? t('查看需求详情') : t('返回需求列表') }}</button></div></div>
    <template v-else>
      <div v-if="error" class="editor-alert error-alert" role="alert">{{ errorText }}</div><div v-if="notice" class="editor-alert success-alert" role="status">{{ t(notice) }}</div>
      <div v-if="referenceError" class="editor-alert error-alert" role="alert">{{ t(referenceError) }} <button type="button" @click="refreshReferences">{{ t('重试载入项目数据') }}</button></div>
      <div v-if="!edit" class="capture-toolbar"><span>{{ t('先记录要解决的问题，评估与排期可稍后补充') }}</span><label v-if="!expanded" for="quick-priority">{{ t('优先级') }}</label><select v-if="!expanded" id="quick-priority" v-model="f.priority" :disabled="saving"><option>P0</option><option>P1</option><option>P2</option><option>P3</option></select><button type="button" class="btn compact" :disabled="saving" :aria-expanded="expanded" @click="moreFields=!moreFields">{{ t(expanded?'收起更多信息':'展开更多信息') }}</button></div>
      <ResizableSplit class="editor-split" :show-aside="expanded" :label="t('调整字段区宽度')" :project-id="loadedProject" :scene="props.embedded ? (edit ? 'editor.embedded-edit' : 'editor.child') : edit ? 'editor.edit' : 'editor.new'" :initial-width="380" :min-main-width="360" :min-aside-width="300" :max-aside-width="640" :breakpoint="820" :disabled="saving">
       <template #main>
        <section class="editor-main">
          <RequirementTemplates :members="members" :current-user-id="currentUserId" :context-key="currentUserId+':'+loadedProject" :disabled="saving||descriptionMediaBusy||refinementBusy||titleGenerating" @apply="applyRequirementTemplate" />
          <div class="editor-content-intro">
            <div v-show="expanded" class="type-field"><span class="intro-field-label">{{ t('需求类型') }}</span><AppSelect id="requirement-type" :model-value="f.type" :label="t('需求类型')" :options="[...new Set(['产品需求','技术需求','体验优化',f.type])].map(value=>({value,label:['产品需求','技术需求','体验优化'].includes(value)?t(value):value}))" :disabled="saving" @update:model-value="f.type=String($event)" /></div>
            <div class="title-field"><label for="requirement-title" class="title-label">{{ t('需求标题') }}</label><input id="requirement-title" v-model="f.title" required maxlength="300" :disabled="saving" class="title-input" :placeholder="t('一句话说明要解决的问题')"></div>
            <RequirementTitleAssistant class="intro-assistant" ref="titleAssistant" :title="f.title" :description="f.description" :document="f.descriptionDoc" :requirement-id="edit?Number(editingID):undefined" :project-id="loadedProject" :user-id="currentUserId" :actor-key="aiActorKey" :can-configure="canConfigureAI" :disabled="saving||descriptionMediaBusy||loading||!canEdit" @busy="titleGenerating=$event" @generated="applyGeneratedTitle"/>
          </div>
          <div class="description-heading"><label for="requirement-description">{{ t('需求描述') }}</label><span>{{ t('正文优先展示；可使用右上角按钮全屏编辑') }}</span></div><RichTextEditor :key="descriptionDraftRevision" input-id="requirement-description" v-model="f.description" v-model:document="f.descriptionDoc" v-model:fullscreen="descriptionFullscreen" :allow-fullscreen="true" :requirement-id="edit?Number(editingID):undefined" @busy="descriptionMediaBusy=$event" v-model:mentionUserIds="f.descriptionMentionUserIds" v-model:mentionNames="f.descriptionMentionNames" :saved-mention-user-ids="savedDescriptionMentionIds" :members="members" :disabled="saving" :label="t('需求描述')" :placeholder="t('建议说明背景、目标用户、使用场景和解决方案，输入 @ 协同成员…')"/>
          <p v-if="sameTitleItems.length" class="title-duplicates" role="status">{{t('发现同名需求，创建前请核对：')}} <a v-for="item in sameTitleItems" :key="item.id" :href="'/requirements?req='+item.id" target="_blank" rel="noopener noreferrer">{{item.code}} · {{item.title}}</a></p>
          <div v-show="expanded" class="capture-details">
          <RequirementRefinement :description="f.description" :acceptance="f.acceptance" :requirement-id="edit?Number(editingID):undefined" :disabled="saving||descriptionMediaBusy||loading||!canEdit" @apply="applyRefinement" @busy="refinementBusy=$event"/><RequirementSuggestions :description="f.description" :acceptance="f.acceptance" />
          <label for="requirement-acceptance">{{ t('验收标准') }}</label><textarea id="requirement-acceptance" v-model="f.acceptance" :disabled="saving" rows="5" :placeholder="t('请写出可验证、可测试的完成条件')"></textarea>
          <div class="section-heading weights-heading"><span>02</span><div><h2>{{ t('职能权重评估') }}</h2><p>{{ t('按职能绑定人员并手动填写数值，自动汇总整条需求的难度。') }}</p></div></div>
          <RequirementWeights v-model="f.roleWeights" :members="members" :current-user-id="currentUserId" :disabled="saving" />
          <label for="requirement-remarks">{{ t('备注') }}</label><MentionComment mode="field" input-id="requirement-remarks" v-model="f.remarks" v-model:mentionUserIds="f.remarksMentionUserIds" v-model:mentionNames="f.remarksMentionNames" :saved-mention-user-ids="savedRemarksMentionIds" :members="members" :disabled="saving" :label="t('备注')" :rows="4" :maxlength="10000" :placeholder="t('补充依赖、风险或协作约定，输入 @ 选择成员')"/>
          </div>
        </section>
       </template>
       <template #aside>
        <aside class="property-panel">
          <h3>{{ t('计划与负责人') }}</h3>
          <label for="requirement-parent">{{ t('父需求') }}</label><select id="requirement-parent" v-model="f.parentId" :disabled="props.embedded || saving || referencesLoading"><option :value="null">{{ t('无父需求') }}</option><option v-for="parent in parentOptions" :key="parent.id" :value="parent.id">{{ parent.code }} · {{ parent.title }}</option></select>
          <label for="requirement-category">{{ t('分类') }}</label><select id="requirement-category" v-model="f.category" :disabled="saving"><option v-for="category in categoryOptions" :key="category" :value="category">{{ categoryLabel(category) }}</option></select>
          <div class="property-label"><label for="requirement-sprint">{{ t('所属迭代') }}</label><button type="button" :disabled="referencesLoading || saving" @click="refreshReferences">{{ referencesLoading ? t('刷新中…') : t('刷新迭代') }}</button></div><select id="requirement-sprint" v-model="f.sprint" :disabled="saving" @focus="refreshOnFocus"><option value="待规划">{{ t('待规划') }}</option><option v-if="missingSprint" :value="f.sprint" disabled>{{ f.sprint }} {{ t('· 原关联，当前不可用') }}</option><option v-for="sprint in availableSprints" :key="sprint.id" :value="sprint.name" :disabled="!sprintSelectable(sprint)">{{ sprint.name }} · {{ t(sprint.status==='进行中'?'当前进行中':sprint.status) }}</option></select>
          <p v-if="historicalSprint || missingSprint" class="property-hint warning-hint">{{ t('保留原迭代关系；若要调整，请选择规划中或进行中的迭代。') }}</p><p v-else class="property-hint">{{ t('与当前项目迭代池同步，切回页面时自动刷新。') }}</p>
          <div class="priority-value-row"><div><label for="requirement-priority">{{ t('优先级') }}</label><select id="requirement-priority" v-model="f.priority" :disabled="saving"><option>P0</option><option>P1</option><option>P2</option><option>P3</option></select></div><div class="business-value-field"><CustomFieldInputs object-type="requirement" v-model="f.customFields" :visible-keys="['business_value']" @validity-change="editorDatesValid.business=$event" /></div></div>
          <label for="requirement-assignee">{{ t('处理人') }}</label><MemberMultiSelect input-id="requirement-assignee" v-model="f.assigneeUserIds" @update:modelValue="assigneesTouched=true" :members="members" :snapshots="f.assignees" :legacy-name="!assigneesTouched&&!savedAssigneeIds.length?f.assignee:''" :current-user-id="currentUserId" :disabled="saving || referencesLoading" :label="t('处理人')"/>
          <div class="tester-field"><CustomFieldInputs object-type="requirement" v-model="f.customFields" :visible-keys="['testers']" @validity-change="editorDatesValid.testers=$event" /></div>
          <div class="two"><div><label for="requirement-start">{{ t('计划开始') }}</label><DatePicker id="requirement-start" v-model="f.startDate" @validity-change="editorDatesValid.start=$event" :label="t('计划开始')" :disabled="saving" /></div><div><label for="requirement-end">{{ t('计划结束') }}</label><DatePicker id="requirement-end" v-model="f.endDate" @validity-change="editorDatesValid.end=$event" :label="t('计划结束')" :min="f.startDate || undefined" :disabled="saving" /></div></div>
          <div class="property-section"><h3>{{ t('彩色标签') }}</h3><RequirementTags v-model="f.tags" v-model:colors="f.tagColors" :options="tagOptions" :disabled="saving" /><p v-if="tagError" class="property-hint warning-hint">{{t(tagError)}} <button type="button" class="link" @click="loadTags">{{t('重试')}}</button></p></div>
          <div class="property-section"><h3>{{ t('合规检查') }}</h3><label class="check"><input v-model="f.sensitive" type="checkbox" :disabled="saving">{{ t('涉及敏感数据') }}</label><label class="check"><input v-model="f.authImpact" type="checkbox" :disabled="saving">{{ t('涉及权限认证') }}</label></div>
          <details class="property-section custom-fields-section"><summary>{{ t('更多字段') }}</summary><fieldset class="custom-field-lock" :disabled="saving"><CustomFieldInputs object-type="requirement" v-model="f.customFields" :excluded-keys="['business_value','testers','customer_type']" :excluded-names="['客户类型','11']" @validity-change="editorDatesValid.custom=$event" /></fieldset></details>
          <div class="property-section creation-meta"><span>{{ t('创建时间') }}</span><time>{{ edit ? formatCreatedAt(f.createdAt) : t('创建成功后自动记录') }}</time></div>
        </aside>
       </template>
      </ResizableSplit>
      <footer class="editor-footer"><button type="submit" class="btn primary" :disabled="saving || draftSaving || titleGenerating || descriptionMediaBusy || referencesLoading">{{ saving ? t('保存中…') : edit ? t('保存修改') : t('创建需求') }}</button><button v-if="!edit" type="button" class="btn" :disabled="saving || draftSaving || titleGenerating || descriptionMediaBusy || referencesLoading" @click="save(true)">{{ t('提交并继续创建') }}</button><button type="button" class="btn" :disabled="saving || draftSaving || titleGenerating || descriptionMediaBusy" @click="savePrivateDraft">{{ t('保存草稿') }}</button><button v-if="props.embedded" type="button" class="btn ghost" :disabled="saving || draftSaving || descriptionMediaBusy" @click="requestClose">{{ t('取消') }}</button><span v-if="dirty" class="unsaved">{{ t('● 尚未保存') }}</span><span v-else class="save-hint">{{ edit ? t('修改后点击保存生效') : t('创建时间与权重总和由系统自动计算') }}</span></footer>
    </template>
  </form>
  <dialog ref="leaveDialog" class="editor-leave-dialog" :aria-label="t('离开需求编辑')" @cancel.prevent="finishLeave(false)" @click.self="finishLeave(false)">
   <h2>{{t('离开需求编辑')}}</h2><p>{{t('当前内容尚未正式保存。可以保存为本机私有草稿后离开，之后从草稿箱恢复。')}}</p>
   <p v-if="leaveError" role="alert">{{t(leaveError)}}</p>
   <div><button type="button" class="btn" :disabled="draftSaving" @click="finishLeave(false)">{{t('继续编辑')}}</button><button type="button" class="btn" :disabled="draftSaving" @click="finishLeave(true)">{{t('直接离开')}}</button><button type="button" class="btn primary" :disabled="draftSaving" @click="saveDraftAndLeave">{{t(draftSaving?'正在保存草稿…':'保存草稿后离开')}}</button></div>
  </dialog>
</template>

<style scoped>
.editor-leave-dialog{width:min(480px,calc(100vw - 32px));box-sizing:border-box;border:1px solid var(--line);border-radius:8px;padding:20px;color:var(--text);background:var(--surface)}.editor-leave-dialog::backdrop{background:#11182766}.editor-leave-dialog h2{font-size:16px}.editor-leave-dialog p{font-size:13px;line-height:1.7}.editor-leave-dialog>div{display:flex;flex-wrap:wrap;justify-content:flex-end;gap:8px}
.title-duplicates{font-size:var(--ui-font-caption,12px);color:var(--warning-text,#9b681b);line-height:1.7}.title-duplicates a{display:inline-block;margin-right:8px;color:var(--primary)}
/* 分栏及滚动由 ResizableSplit 按容器控制；字段本身必须可收缩，不能仅靠裁切掩盖溢出。 */
.requirement-editor{min-width:0;min-height:0;max-width:100%;overflow:hidden;background:var(--surface,#fff)}
.capture-toolbar{display:flex;align-items:center;flex-wrap:wrap;gap:8px;padding:10px 26px;border-bottom:1px solid var(--line);font-size:13px;flex:none}.capture-toolbar>span{flex:1 1 220px;color:var(--muted)}.capture-toolbar label{margin:0}.capture-toolbar select{width:auto;min-height:32px;margin:0;padding:4px 8px;border:1px solid var(--line);border-radius:6px;background:var(--surface);color:var(--ink);font:inherit}.capture-details{min-width:0}
.requirement-editor .editor-head{display:flex;align-items:center;justify-content:space-between;min-height:56px;padding:10px 26px;flex:none;gap:14px}
.editor-titlebar,.editor-head-actions{display:flex;align-items:center;gap:10px;min-width:0}.editor-titlebar h1{margin:0;font-size:22px;line-height:30px;white-space:nowrap}.editor-titlebar span{min-width:0;color:var(--muted);font-size:11px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}.editor-head-actions{margin-left:auto;flex:none}.editor-head-actions :deep(.work-item-drafts){margin:0;padding:0;border:0;background:transparent}.editor-head-actions :deep(.draft-error),.editor-head-actions :deep(.draft-message){display:none}
.editor-head>div{min-width:0}.editor-head>.btn{flex:none}
.editor-head-actions{flex-wrap:wrap;max-width:100%}
.editor-head h1,.editor-head p{overflow-wrap:anywhere}
.editor-head p{margin:6px 0 0;color:var(--muted);font-size:11px;line-height:1.7}
.requirement-editor .editor-main{min-width:0;max-width:100%;padding:20px 30px 40px;overflow:visible;background:var(--surface,#fff)}
.requirement-editor .property-panel{min-width:0;max-width:100%;width:100%;padding:26px 20px 35px;overflow:visible;border-left:0;background:var(--surface-subtle,#f7f9fc);overflow-wrap:anywhere}
.section-heading{display:flex;align-items:center;gap:10px}.section-heading>div{min-width:0}
.section-heading>span{display:grid;place-items:center;width:25px;height:25px;flex:none;border-radius:6px;background:var(--primary-soft);color:var(--primary);font-size:10px;font-weight:650}
.section-heading h2{margin:0;color:var(--ink);font-size:14px}.section-heading p{margin:5px 0 0;color:var(--muted);font-size:11px;line-height:1.6;overflow-wrap:anywhere}
.weights-heading{margin:29px 0 14px;padding-top:22px;border-top:1px solid var(--line)}
/* 按分栏后的可用宽度自然换行；不再把标签和 100% 宽的选择器挤在同一固定列。 */
.editor-content-intro{display:flex;flex-wrap:wrap;align-items:flex-end;gap:8px 12px;padding:0 0 12px;border-bottom:1px solid var(--line)}
.editor-content-intro .intro-heading{flex:0 0 auto;min-height:40px;align-self:flex-end;margin:0;white-space:nowrap}
.editor-content-intro .type-field{flex:0 1 120px;min-width:110px;max-width:100%;display:grid;gap:6px;margin:0}
.editor-content-intro .title-field{flex:1 1 210px;min-width:0;max-width:100%;display:grid;gap:6px}
.editor-content-intro .intro-assistant{flex:0 0 auto;align-self:end;min-width:0;max-width:100%;margin:0;padding:0;border:0;background:transparent}
.requirement-editor .editor-content-intro :is(.intro-field-label,.title-label){display:block;margin:0;color:var(--muted);font-size:12px;line-height:18px;white-space:nowrap}
.editor-content-intro .type-field :deep(.app-select-trigger){width:100%;min-width:0;min-height:40px}
.requirement-editor .editor-content-intro .title-input{display:block;width:100%;min-width:0;min-height:40px;height:40px;font-size:15px!important;line-height:22px;padding:8px 12px!important;margin:0;border:1px solid var(--line)!important;border-radius:8px!important;background:var(--surface,#fff)}
.requirement-editor .editor-content-intro .title-input:focus-visible{outline:2px solid color-mix(in srgb,var(--primary) 35%,transparent);outline-offset:2px;border-color:var(--primary)}
.description-heading{display:flex;align-items:baseline;justify-content:space-between;gap:12px;margin:12px 0 7px}.description-heading label{margin:0}.description-heading span{color:var(--muted);font-size:10px;line-height:1.5;text-align:right}.requirement-editor .editor-main :deep(.rich-editor:not(.rich-fullscreen)){display:flex;flex-direction:column;min-height:clamp(340px,50vh,660px)}.requirement-editor .editor-main :deep(.rich-editor:not(.rich-fullscreen) .rich-content){flex:1;min-height:0}.requirement-editor .editor-main :deep(.rich-editor:not(.rich-fullscreen) .rich-document){min-height:clamp(270px,42vh,570px)!important}
.requirement-editor textarea{font-size:12px;line-height:1.8;resize:vertical}
.requirement-editor :is(input,select,textarea){min-width:0;max-width:100%;box-sizing:border-box}
.requirement-editor .property-panel h3{font-size:13px;color:var(--ink);margin-bottom:13px;overflow-wrap:anywhere}
.property-panel label{white-space:normal;overflow-wrap:anywhere;text-align:start}
.property-label{display:flex;align-items:center;justify-content:space-between;gap:10px;flex-wrap:wrap}
.property-label button{border:0;background:transparent;color:var(--primary);font-size:10px;padding:3px 0;margin-top:10px}
.property-hint{font-size:10px;line-height:1.7;color:var(--muted);margin:6px 0 0;overflow-wrap:anywhere}.warning-hint{color:var(--warning-text,#9b681b)}
.property-section{min-width:0;border-top:1px solid var(--line);margin-top:22px;padding-top:20px}.custom-fields-section summary{cursor:pointer;color:var(--muted);font-size:12px;font-weight:600;list-style:none}.custom-fields-section summary::-webkit-details-marker{display:none}.custom-fields-section summary::before{content:'›';display:inline-block;margin-right:6px;color:var(--primary);transition:transform .16s ease}.custom-fields-section[open] summary::before{transform:rotate(90deg)}.custom-fields-section .custom-field-lock{margin-top:8px}
.property-section .check{margin:11px 0;white-space:normal}.property-section .check input{width:auto;flex:none}
/* 日期按字段区真实可用宽度换行；不增加 contain/transform，保留本地 fixed 日历的坐标系。 */
.property-panel .two{display:flex;flex-wrap:wrap;gap:10px;min-width:0}.property-panel .two>div{flex:1 1 170px;min-width:0;max-width:100%}.priority-value-row{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:10px;min-width:0}.priority-value-row>div{min-width:0}.priority-value-row :deep(.custom-fields){display:grid;grid-template-columns:minmax(0,1fr);gap:0}.priority-value-row :deep(.custom-fields>label){margin:0 0 7px}.tester-field :deep(.custom-fields){display:grid;grid-template-columns:minmax(0,1fr);gap:0}.tester-field :deep(.custom-fields>label){margin:14px 0 7px}
.creation-meta{display:grid;gap:8px;color:var(--muted);font-size:11px}.creation-meta time{font-variant-numeric:tabular-nums;color:var(--ink);overflow-wrap:anywhere}
.custom-field-lock{border:0;padding:0;margin:0;min-width:0;max-width:100%}
.property-panel :deep(.custom-fields){display:grid;grid-template-columns:minmax(0,1fr);gap:0;min-width:0;max-width:100%}
.property-panel :deep(.custom-fields>*){min-width:0;max-width:100%;box-sizing:border-box}
.property-panel :deep(.custom-fields>label){white-space:normal;overflow-wrap:anywhere;text-align:start;margin:16px 0 7px}
.property-panel :deep(.custom-fields input),.property-panel :deep(.custom-fields select),.property-panel :deep(.custom-fields textarea){min-width:0;max-width:100%;box-sizing:border-box}
.property-panel :deep(.member-chip){flex-wrap:wrap;min-width:0;max-width:100%}.property-panel :deep(.member-chip span){min-width:0;overflow-wrap:anywhere}
.property-panel :deep(.member-hint),.property-panel :deep(.option-checks label){white-space:normal;overflow-wrap:anywhere}
.property-panel :deep(.option-checks){min-width:0;max-width:100%}
.requirement-editor .editor-footer{height:auto;min-height:67px;padding:13px 26px;flex:none;flex-wrap:wrap}
.save-hint{margin-left:auto;color:var(--muted);font-size:11px}.unsaved{overflow-wrap:anywhere}
.editor-alert{flex:none;padding:11px 26px;font-size:12px;border-bottom:1px solid var(--line);overflow-wrap:anywhere}
.error-alert{background:var(--danger-soft,#fff5f5);color:var(--danger-text,#b42318)}.success-alert{background:var(--success-soft,#ecfdf3);color:var(--success-text,#027a48)}
.editor-alert button{border:0;background:transparent;color:inherit;text-decoration:underline;font-size:11px;margin-left:8px}
.requirement-editor input:disabled,.requirement-editor select:disabled,.requirement-editor textarea:disabled{background:var(--surface-subtle,#f9fafb);cursor:not-allowed}
.embedded-editor{height:100%;min-height:0;min-width:0}.embedded-editor .editor-head{padding:20px 24px}.embedded-editor .editor-head h1{font-size:20px}
.embedded-editor .parent-context{line-height:1.7;color:var(--muted);overflow-wrap:anywhere}.embedded-editor .editor-main{padding:24px}.embedded-editor .editor-main :deep(.rich-editor:not(.rich-fullscreen)){min-height:300px}.embedded-editor .editor-main :deep(.rich-editor:not(.rich-fullscreen) .rich-document){min-height:260px!important}
.embedded-editor :deep(#requirement-description){min-height:300px}.embedded-editor .editor-footer{padding:13px 24px;gap:8px}.embedded-editor .editor-footer .btn{white-space:nowrap}
.requirement-editor .editor-split.split-stacked .editor-main,.requirement-editor .editor-split.split-stacked .property-panel{padding:20px}@media(max-width:850px){.requirement-editor .editor-head{padding:12px 20px;align-items:flex-start;flex-wrap:wrap}.editor-head-actions{margin-left:0}.editor-titlebar{flex:1 1 100%}.editor-titlebar span{display:none}.requirement-editor .editor-footer{padding:12px 20px}.save-hint{display:none}.embedded-editor :deep(#requirement-description){min-height:260px}.requirement-editor .editor-main :deep(.rich-editor:not(.rich-fullscreen)){min-height:300px}.requirement-editor .editor-main :deep(.rich-editor:not(.rich-fullscreen) .rich-document){min-height:240px!important}}@media(max-width:620px){.editor-content-intro{gap:12px}.editor-content-intro .type-field{flex:1 1 140px}.editor-content-intro .title-field{flex-basis:100%}.editor-content-intro .intro-assistant{flex:0 0 auto}.editor-content-intro .intro-assistant :deep(.title-assistant-actions span){display:none}.priority-value-row{grid-template-columns:1fr}.description-heading{align-items:flex-start;flex-direction:column;gap:2px}.description-heading span{text-align:left}}
</style>

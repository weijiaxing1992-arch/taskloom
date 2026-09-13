<script setup lang="ts">
import CodeTextView from '../components/CodeTextView.vue'
import Icon from '../components/Icon.vue'
import { t, locale, formatDate } from '../i18n'
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter, onBeforeRouteLeave, onBeforeRouteUpdate } from 'vue-router'
import CustomFieldInputs from '../components/CustomFieldInputs.vue'
import MemberMultiSelect from '../components/MemberMultiSelect.vue'
import ResizableDrawer from '../components/ResizableDrawer.vue'
import StatusMultiSelect from '../components/StatusMultiSelect.vue'
import AppSelect from '../components/AppSelect.vue'
import DefectComposer from '../components/DefectComposer.vue'
import RequirementPicker from '../components/RequirementPicker.vue'
import WorkItemPdfExport from '../components/WorkItemPdfExport.vue'
import { useSettingsScope } from '../components/settingsScope'
import MentionComment from '../components/MentionComment.vue'
import CommentReplyContext from '../components/CommentReplyContext.vue'
import type { CommentReplyTarget } from '../commentReplies'
import { qaMembers, defectPersonValue } from '../defectPeople'
import { defectStatusOptions, workflowStyle } from '../requirementWorkflow'

const route = useRoute()
const router = useRouter()
const scope = useSettingsScope(), request = scope.request
const session = ref<any>(null), editingId = ref<number|null>(null), editingComposer = ref<InstanceType<typeof DefectComposer>|null>(null), rowSavingId = ref<number|null>(null)
const items = ref<any[]>([])
const defs = ref<any[]>([])
const selected = ref<any>(null)
const drawerWidth = ref(1040)
const show = ref(false)
const q = ref('')
const status = ref<string[]>([])
const view = ref<'list'|'board'>('list')
const board = computed(()=>defectStatusOptions.filter(option=>!status.value.length||status.value.includes(option.value)).map(option=>({...option,items:items.value.filter(item=>item.status===option.value)})))
const severity = ref('')
// 人员筛选只保存成员稳定 ID；姓名只用于展示，避免改名后筛选结果漂移。
const assigneeUserId = ref('')
const verifierUserId = ref('')
const mine = ref(false)
const comment = ref('')
const comments = ref<any[]>([])
const commentMentions=ref<string[]>([]),commentMentionNames=ref<Record<string,string>>({}),commentReply=ref<CommentReplyTarget|null>(null),commentComposer=ref<InstanceType<typeof MentionComment>|null>(null)
const commentMap=computed(()=>new Map<number,any>(comments.value.map(entry=>[entry.id,entry])))
const commentDirty=computed(()=>!!comment.value.trim()||!!commentReply.value)
const activities = ref<any[]>([])
const error = ref('')
const notice = ref('')
const saving = ref(false)
const loading = ref(false)
const members = ref<any[]>([])
const sprints = ref<any[]>([])
const activeMembers = computed(() => members.value.filter(x => x.active))
const canWrite = computed(() => { const role=session.value?.user?.role||members.value.find(member=>member.isCurrent)?.projectRole; return !!role&&role!=='viewer'&&!scope.locked.value })
const testers = computed(() => qaMembers(activeMembers.value))
const otherVerifiers = computed(() => activeMembers.value.filter(x => !testers.value.some(person => person.id === x.id)))
const composer = ref<InstanceType<typeof DefectComposer> | null>(null)
const availableSprints = computed(() => sprints.value.filter(x => ['规划中', '进行中'].includes(x.status)))
const severityOptions = computed(() => [
  { value: '', label: t('全部严重程度') },
  ...['致命', '严重', '一般', '轻微'].map(value => ({ value, label: t(value) })),
])
function memberFilterOption(member:any) {
  const department = member.department ? ` · ${member.department}` : ''
  return { value: String(member.id), label: `${member.name}${department}` }
}
const assigneeOptions = computed(() => [{ value: '', label: t('全部负责人') }, ...activeMembers.value.map(memberFilterOption)])
const verifierOptions = computed(() => [{ value: '', label: t('全部验证人') }, ...activeMembers.value.map(memberFilterOption)])
const severityTone = (value:string) => ({
  '致命': 'severity-critical',
  '严重': 'severity-major',
  '一般': 'severity-normal',
  '轻微': 'severity-minor',
} as Record<string,string>)[value] || 'severity-normal'
const severitySelectClass = (value:string) => `defect-severity-select ${severityTone(value)}`
function setAssigneeFilter(value:string|number) { assigneeUserId.value=String(value); if(assigneeUserId.value)mine.value=false }
function setVerifierFilter(value:string|number) { verifierUserId.value=String(value); if(verifierUserId.value)mine.value=false }
function toggleMine() { mine.value=!mine.value; if(mine.value){assigneeUserId.value='';verifierUserId.value=''} }
function clearFilters() { q.value='';status.value=[];severity.value='';assigneeUserId.value='';verifierUserId.value='';mine.value=false }
function customValue(item:any,definition:any):string {
  const value=item.customFields?.[definition.key]
  if(value==null||value==='')return '—'
  if(['user','users'].includes(definition.type))return (Array.isArray(value)?value:[value]).map(id=>members.value.find(member=>member.id===id)?.name||id).join('、')||'—'
  return typeof value==='boolean'?t(value?'是':'否'):Array.isArray(value)?value.join('、'):String(value)
}
let loadVersion = 0
let detailVersion = 0
let commentReadVersion=0,disposed=false,projectLeaveApproved=false
const message = (cause: unknown) => cause instanceof Error ? cause.message : '操作失败，请稍后重试'

const statusOptions = computed(() => selected.value ? [selected.value.status, ...(selected.value.allowedTransitions || [])] : [])
const canRegress = computed(() => ['已解决', '待验证'].includes(selected.value?.status))

async function load() {
  if(!scope.current())return
  const version = ++loadVersion
  loading.value = true
  const params = new URLSearchParams()
  if(q.value.trim())params.set('q',q.value.trim())
  if(severity.value)params.set('severity',severity.value)
  if(status.value.length)params.set('statuses',JSON.stringify(status.value))
  if(assigneeUserId.value)params.set('assigneeUserId',assigneeUserId.value)
  if(verifierUserId.value)params.set('verifierUserId',verifierUserId.value)
  if(mine.value)params.set('mine','1')
  try { const data = await request<any>(`/defects?${params}`); if (version === loadVersion&&scope.current()) items.value = data.items || [] }
  catch (cause) { if (version === loadVersion&&scope.current()) notice.value = message(cause) }
  finally { if (version === loadVersion&&scope.current()) loading.value = false }
}

async function loadDetails(id: number) {
  const version = ++detailVersion
  const commentVersion=++commentReadVersion
  const [detail, commentData, activityData] = await Promise.all([
    request<any>(`/defects/${id}`),
    request<any>(`/defects/${id}/comments`),
    request<any>(`/defects/${id}/activities`),
  ])
  if (version !== detailVersion||!scope.current()) return
  selected.value = detail
  if(commentVersion===commentReadVersion)comments.value = commentData.items || []
  activities.value = activityData.items || []
}

async function open(id: number, push = true) {
  if (push) { await router.replace({ query: { bug: id } }); return }
  if (selected.value?.id !== id) {selected.value=null;resetCommentDraft();comments.value=[]}
  notice.value = ''
  const version = detailVersion + 1
  try {
    await loadDetails(id)
  } catch (cause: any) {
    if (version === detailVersion) { selected.value = null; notice.value = cause.message || '缺陷详情加载失败' }
  }
}

async function close() {
  if(editingId.value){void editingComposer.value?.requestClose();return}
  if(show.value){void composer.value?.requestClose();return}
  if (!canLeave()) return
  detailVersion++
  selected.value = null
  resetCommentDraft()
  await router.replace({ query: {} })
}

async function onCreated() { show.value = false; await load() }
function openCreate(){if(canWrite.value&&!saving.value&&!rowSavingId.value&&scope.current())show.value=true}
function editSelected(){if(canWrite.value&&selected.value&&!saving.value&&!rowSavingId.value&&scope.current())editingId.value=selected.value.id}
async function onSaved(defect:any){if(!scope.current()||editingId.value!==defect.id)return;editingId.value=null;if(selected.value?.id===defect.id)selected.value=defect;notice.value='缺陷修改已保存';try{await Promise.all([load(),refreshActivities(defect.id)])}catch(cause){if(scope.current())notice.value=message(cause)}}
function rowStatusOptions(item:any){return [item.status,...(Array.isArray(item.allowedTransitions)?item.allowedTransitions:[])].filter((value,index,all)=>typeof value==='string'&&all.indexOf(value)===index)}
async function changeStatus(item:any,value:string,event?:Event){
  const input=event?.target as HTMLSelectElement|undefined,previous=item.status
  if(!canWrite.value||saving.value||rowSavingId.value||!scope.current()||value===previous||!rowStatusOptions(item).slice(1).includes(value)){if(input)input.value=previous;return}
  rowSavingId.value=item.id;notice.value=''
  try{const updated=await request<any>('/defects/'+item.id,{method:'PATCH',body:JSON.stringify({status:value})});if(!scope.current())return;if(updated?.id!==item.id)throw Error('缺陷保存响应无效，请刷新后确认');items.value=items.value.map(row=>row.id===item.id?updated:row);if(selected.value?.id===item.id)selected.value=updated;await load()}
  catch(cause){if(scope.current()){notice.value=message(cause);if(input)input.value=previous}}
  finally{if(scope.current())rowSavingId.value=null}
}

async function setDetailPerson(key:'assigneeUserId'|'verifierUserId',ids:string[]){
 if(!selected.value||saving.value||rowSavingId.value||!canWrite.value||!scope.current()||ids.length>1)return
 const value=ids[0]||''
 if(value&&(!activeMembers.value.some(member=>member.id===value)||value===selected.value[key]))return
 await patch(key,value)
}
async function patch(key: string, value: any) {
  if (!selected.value || saving.value || rowSavingId.value || !canWrite.value || !scope.current()) return
  if(key==='status'&&(value===selected.value.status||!statusOptions.value.slice(1).includes(value)))return
  const id = selected.value.id
  const previous = selected.value[key]
  notice.value = ''
  saving.value = true
  try {
    const payload = key === 'verifierUserId' ? defectPersonValue(activeMembers.value,value) : key === 'assigneeUserId' ? {assigneeUserId:value,assignee:activeMembers.value.find(member=>member.id===value)?.name||''} : { [key]: value }
    const updated = await request(`/defects/${id}`, { method: 'PATCH', body: JSON.stringify(payload) })
    if(!scope.current())return
    if (selected.value?.id === id) selected.value = updated
    await Promise.all([load(), refreshActivities(id)])
  } catch (cause: any) {
    if(scope.current()){if (selected.value?.id === id) selected.value[key] = previous;notice.value = cause.message || '更新失败，请稍后重试'}
  } finally {
    saving.value = false
  }
}

async function refreshActivities(id: number) {
  const data = await request<any>(`/defects/${id}/activities`)
  if (selected.value?.id === id) activities.value = data.items || []
}

async function addComment(value: {body:string;mentionUserIds:string[]}) {
  if (!value.body.trim() || !selected.value || saving.value || !canWrite.value || !scope.current()||disposed||!commentTargetCurrent()) return
  const id = selected.value.id
  const body = value.body
  const version=detailVersion,draft=commentSnapshot(),replyToId=commentReply.value?.id
  const current=()=>!disposed&&scope.current()&&version===detailVersion&&selected.value?.id===id&&commentTargetCurrent(id)
  saving.value = true
  ++commentReadVersion
  notice.value = ''
  try {
    const saved=await request<any>(`/defects/${id}/comments`, { method: 'POST', body: JSON.stringify({ body,mentionUserIds:value.mentionUserIds,...(replyToId?{replyToId}:{}) }) })
    if(!current())return
    if(!saved||!Number.isSafeInteger(saved.id)||saved.id<1||replyToId&&saved.replyToId!==replyToId)throw Error('评论返回数据不完整，请刷新讨论后确认，勿重复提交。')
    comments.value=[saved,...comments.value.filter(entry=>entry.id!==saved.id)]
    if(draft===commentSnapshot())resetCommentDraft()
    window.dispatchEvent?.(new Event('devflow-notifications-changed'))
    try{await Promise.all([refreshComments(),refreshActivities(id)])}catch{if(current())notice.value='评论已发布，但讨论刷新失败，请重试刷新。'}
  } catch (cause: any) {
    if(current())notice.value = cause.message || '评论发布失败'
  } finally { if(!disposed&&version===detailVersion)saving.value = false }
}
function resetCommentDraft(){comment.value='';commentMentions.value=[];commentMentionNames.value={};commentReply.value=null}
function commentSnapshot(){return JSON.stringify({body:comment.value,ids:commentMentions.value,names:commentMentionNames.value,reply:commentReply.value})}
function commentTargetCurrent(id=selected.value?.id){return !!id&&(!route.query.bug||String(route.query.bug)===String(id))}
async function replyComment(id:number){if(!canWrite.value||!selected.value||saving.value||!scope.current()||disposed||!commentTargetCurrent())return;const entry=commentMap.value.get(id);if(!entry||!Number.isSafeInteger(id)||id<1)return;commentReply.value={id,author:entry.author,body:entry.body};const defectId=selected.value.id;await nextTick();if(!disposed&&selected.value?.id===defectId&&commentReply.value?.id===id&&commentTargetCurrent(defectId))commentComposer.value?.focus()}
function cancelCommentReply(){if(!saving.value)commentReply.value=null}
async function refreshComments(){if(!selected.value||!scope.current()||disposed)return;const id=selected.value.id,version=detailVersion,read=++commentReadVersion;const data=await request<any>(`/defects/${id}/comments`);if(!disposed&&scope.current()&&selected.value?.id===id&&version===detailVersion&&read===commentReadVersion){if(!Array.isArray(data.items))throw Error('评论数据格式不正确，请重试');comments.value=data.items}}
async function reloadComments(){if(saving.value)return;try{await refreshComments()}catch(cause){if(scope.current())notice.value=message(cause)}}

let timer: any
function canLeave() { return !saving.value && !rowSavingId.value && !composer.value?.saving && !editingComposer.value?.saving && (projectLeaveApproved||!commentDirty.value || window.confirm(t('评论尚未发布，确定离开并放弃内容？'))) }
function beforeProjectChange(event:Event){projectLeaveApproved=false;if(!canLeave())event.preventDefault();else projectLeaveApproved=true}
function cancelProjectLeave(){projectLeaveApproved=false}
function beforeUnload(event:BeforeUnloadEvent){const switching=projectLeaveApproved&&(localStorage.getItem('devflow-project')||'prj_orbit')!==scope.project;if(!switching&&(commentDirty.value||saving.value)){event.preventDefault();event.returnValue=''}}
onBeforeRouteLeave(canLeave)
onBeforeRouteUpdate((to,from) => to.query.bug===from.query.bug || canLeave())
watch([q, status, severity, assigneeUserId, verifierUserId, mine], () => { ++loadVersion; clearTimeout(timer); timer = setTimeout(() => { void load() }, 150) })
watch(() => route.query.bug, async value => {
  editingId.value=null
  if (value) await open(Number(value), false)
  else { detailVersion++; selected.value = null;resetCommentDraft() }
})

onMounted(async () => {
  window.addEventListener('beforeunload',beforeUnload);window.addEventListener('devflow-before-project-change',beforeProjectChange);window.addEventListener('devflow-project-change-cancelled',cancelProjectLeave)
  await load()
  try {
    const [d, m, s, identity] = await Promise.all([request<any>('/field-definitions?objectType=defect'), request<any>('/members'), request<any>('/sprints'), request<any>('/session')])
    if(!scope.current())return
    defs.value = (d.items || []).filter((item: any) => item.enabled && item.listVisible)
    members.value = m.items || []; sprints.value = s.items || []
    session.value=identity
  } catch (cause) { notice.value = message(cause) }
  if (route.query.bug) await open(Number(route.query.bug), false)
})
onBeforeUnmount(() => {disposed=true;clearTimeout(timer); loadVersion++; detailVersion++;commentReadVersion++;window.removeEventListener('beforeunload',beforeUnload);window.removeEventListener('devflow-before-project-change',beforeProjectChange);window.removeEventListener('devflow-project-change-cancelled',cancelProjectLeave)})
watch(scope.locked,locked=>{if(locked){clearTimeout(timer);loadVersion++;detailVersion++;rowSavingId.value=null;loading.value=false;notice.value='项目或账号已变化，请刷新页面后继续'}},{flush:'sync'})
</script>

<template>
  <div class="module-page">
   <div class="defects-content" :inert="show||editingId!==null">
    <div class="page-heading compact-page-heading">
      <div><h1>{{ t("缺陷") }} <small class="compact-heading-count">{{items.length}}</small></h1><details class="page-heading-note"><summary :aria-label="t('页面说明')" :title="t('页面说明')"><span aria-hidden="true">?</span></summary><p>{{ t("记录、修复并验证产品问题，状态变化自动进入企微通知队列。") }}</p></details></div>
      <div class="compact-heading-actions"><div class="segmented"><button type="button" :class="{active:view==='list'}" :aria-pressed="view==='list'" @click="view='list'">{{t('列表')}}</button><button type="button" :class="{active:view==='board'}" :aria-pressed="view==='board'" @click="view='board'">{{t('看板')}}</button></div><button v-if="canWrite" class="btn primary" :disabled="saving||!!rowSavingId" @click="openCreate">{{ t("＋ 创建缺陷") }}</button></div>
    </div>

    <p v-if="notice" class="inline-notice" role="alert">{{ t(notice) }}</p>

    <div class="toolbar module-toolbar">
      <div class="search"><Icon name="search"/><input :aria-label="t('搜索标题、编号、负责人或部门')" v-model="q" :placeholder="t('搜索标题、编号、负责人或部门')"></div>
      <StatusMultiSelect v-model="status" :options="defectStatusOptions" :label="t('筛选缺陷状态（多选）')" />
      <AppSelect class="defect-filter-select" :model-value="severity" :options="severityOptions" :label="t('严重程度')" @update:model-value="severity=String($event)" />
      <AppSelect class="defect-filter-select defect-person-filter" :model-value="assigneeUserId" :options="assigneeOptions" :label="t('筛选负责人')" @update:model-value="setAssigneeFilter" />
      <AppSelect class="defect-filter-select defect-person-filter" :model-value="verifierUserId" :options="verifierOptions" :label="t('筛选验证人')" @update:model-value="setVerifierFilter" />
      <button type="button" class="defect-mine-filter" :class="{active:mine}" :aria-pressed="mine" :title="t('包含负责人、验证人、所有人员字段、创建、评论参与及 @ 提及')" @click="toggleMine">{{ t('与我相关') }}</button>
      <button v-if="q||status.length||severity||assigneeUserId||verifierUserId||mine" type="button" class="link defect-filter-clear" @click="clearFilters">{{ t('清除筛选') }}</button>
    </div>

    <p v-if="loading" role="status">{{ t("正在加载…") }}</p><p v-else-if="!items.length && !notice" class="empty-mini">{{ t("暂无符合条件的记录") }}</p><div v-if="view==='list'" class="card table-card">
      <table>
        <thead><tr><th>{{ t("缺陷编号") }}</th><th>{{ t("标题") }}</th><th>{{ t("状态") }}</th><th>{{ t("严重程度") }}</th><th>{{ t("优先级") }}</th><th>{{ t("负责人") }}</th><th>{{ t("验证人") }}</th><th>{{ t("迭代") }}</th><th v-for="definition in defs" :key="definition.id">{{ definition.name }}</th><th>{{ t("更新时间") }}</th></tr></thead>
        <tbody><tr v-for="item in items" :key="item.id" :class="['defect-row',severityTone(item.severity)]" tabindex="0" @keydown.enter.self="open(item.id)" @click="open(item.id)"><td class="code">{{ item.code }}</td><td><b>{{ item.title }}</b><small>{{ item.tags }}</small></td><td @click.stop @dblclick.stop @keydown.stop><select class="defect-inline-status workflow-color" :style="workflowStyle(item)" :value="item.status" :aria-label="t('更改缺陷 {code} 状态',{code:item.code})" :disabled="!canWrite||saving||!!rowSavingId||rowStatusOptions(item).length<2" @change="changeStatus(item,($event.target as HTMLSelectElement).value,$event)"><option v-for="value in rowStatusOptions(item)" :key="value" :value="value">{{t(value)}}</option></select></td><td><span :class="['severity','defect-severity',severityTone(item.severity)]"><i aria-hidden="true"></i>{{ t(item.severity) }}</span></td><td><span :class="['priority', item.priority]">{{ item.priority }}</span></td><td>{{ item.assignee || t("待指派") }}</td><td>{{ item.verifier || '—' }}</td><td>{{ item.sprint === '待规划' ? t("待规划") : item.sprint }}</td><td v-for="definition in defs" :key="definition.id">{{ customValue(item,definition) }}</td><td>{{ formatDate(item.updatedAt) }}</td></tr></tbody>
      </table>
    </div>

    <div v-else class="defect-status-board"><section v-for="column in board" :key="column.value"><header><span class="status workflow-color" :style="workflowStyle(column.value)">{{t(column.label)}}</span><b>{{column.items.length}}</b></header><button v-for="item in column.items" :key="item.id" type="button" :class="['defect-board-card',severityTone(item.severity)]" @click="open(item.id)"><small>{{item.code}}</small><strong>{{item.title}}</strong><div class="defect-board-meta"><span :class="['severity','defect-severity',severityTone(item.severity)]"><i aria-hidden="true"></i>{{t(item.severity)}}</span><span class="defect-board-assignee">{{item.assignee||t('待指派')}}</span></div></button><p v-if="!column.items.length">{{t('暂无符合条件的记录')}}</p></section></div>
    <div v-if="selected" class="drawer-shade" @click.self="close">
      <ResizableDrawer v-model:width="drawerWidth" storage-key="defects.detail.width" :initial-width="1040" :label="selected.title" class="module-detail-drawer">
        <header class="drawer-head">
          <div class="drawer-kicker">
            <select class="workflow-color" :style="workflowStyle(selected)" :value="selected.status" :disabled="saving || !!rowSavingId || !canWrite || statusOptions.length === 1" :aria-label="t('缺陷状态')" @change="patch('status', ($event.target as HTMLSelectElement).value)"><option v-for="value in statusOptions" :key="value" :value="value">{{ t(value) }}</option></select>
            <span>{{ selected.code }}</span>
          </div>
          <div class="drawer-title"><h2>{{ selected.title }}</h2><div class="defect-detail-actions"><!-- defect-detail-actions --><WorkItemPdfExport object-type="defect" :object-id="selected.id" :project-id="scope.project"/><button v-if="canWrite" class="btn" :disabled="saving||!!rowSavingId" @click="editSelected">{{t('完整编辑')}}</button><button :aria-label="t('关闭详情')" @click="close">×</button></div></div>
        </header>
        <div class="drawer-body">
          <section class="detail-main"><p v-if="notice" class="field-error" role="alert">{{ t(notice) }}</p>
            <div v-if="selected.sourceExecutionId" class="detail-block traceability-card">
              <div class="section-title"><h3>{{ t("测试来源与回归") }}</h3><span class="status">{{ t("双向追溯") }}</span></div>
              <p>{{ t("此缺陷由失败的测试执行生成，可返回原计划、用例或执行记录继续验证。") }}</p>
              <div class="trace-links">
                <router-link v-if="selected.sourcePlanId" :to="`/tests?tab=plans&plan=${selected.sourcePlanId}`">{{ t("计划：") }}{{ selected.sourcePlanName || `PLN-${selected.sourcePlanId}` }}</router-link>
                <router-link v-if="selected.sourceCaseId" :to="`/tests?tab=cases&case=${selected.sourceCaseId}`">{{ t("用例：") }}{{ selected.sourceCaseTitle || `CASE-${selected.sourceCaseId}` }}</router-link>
                <router-link :to="`/tests?tab=executions&execution=${selected.sourceExecutionId}`">{{ t("执行：EXE-") }}{{ String(selected.sourceExecutionId).padStart(4, '0') }} · {{ t(selected.sourceExecutionStatus||'未设置') }}</router-link>
              </div>
              <router-link v-if="canRegress" class="btn primary compact" :to="`/tests?tab=executions&execution=${selected.sourceExecutionId}`">{{ t("返回原执行做回归") }}</router-link>
            </div>
            <div class="detail-block"><h3>{{ t("问题描述") }}</h3><CodeTextView :text="selected.description||t('尚未填写')"/></div>
            <div class="detail-columns"><div><h3>{{ t("复现步骤") }}</h3><CodeTextView :text="selected.steps||'—'"/></div><div><h3>{{ t("实际 / 预期") }}</h3><CodeTextView :text="selected.actual||'—'"/><span>→</span><CodeTextView :text="selected.expected||'—'"/></div></div>
            <div class="detail-block">
              <h3>{{ t("评论") }}</h3>
              <button type="button" class="link comment-reply-button" :disabled="saving||scope.locked.value" @click="reloadComments">{{t('刷新评论')}}</button><CommentReplyContext v-if="canWrite&&commentReply" :target="commentReply" composing :disabled="saving" @cancel="cancelCommentReply"/>
              <MentionComment :key="selected.id" ref="commentComposer" v-model="comment" v-model:mention-user-ids="commentMentions" v-model:mention-names="commentMentionNames" :members="members" :disabled="saving||!canWrite" :label="t('缺陷评论')" @submit="addComment" />
              <article v-for="entry in comments" :key="entry.id" class="comment"><span class="avatar small">{{ entry.author?.slice(0, 1) }}</span><div><b>{{ entry.author }}</b><time>{{ formatDate(entry.createdAt) }}</time><CommentReplyContext v-if="entry.replyToId" :target="commentMap.get(entry.replyToId)||{id:entry.replyToId,author:entry.replyToAuthor,unavailable:true}"/><CodeTextView :text="entry.body"/><button v-if="canWrite" type="button" class="link comment-reply-button" :disabled="saving" :aria-label="t('回复 {name} 的评论',{name:entry.author||t('未知用户')})" @click="replyComment(entry.id)">{{t('回复')}}</button></div></article>
            </div>
            <div class="detail-block">
              <h3>{{ t("活动") }}</h3>
              <article v-for="entry in activities" :key="entry.id" class="activity"><span></span><div><b>{{ entry.actor }}</b> {{ entry.detail }} <router-link v-if="entry.event === 'created_from_execution' && selected.sourceExecutionId" :to="`/tests?tab=executions&execution=${selected.sourceExecutionId}`">{{ t("查看原执行") }}</router-link><time>{{ formatDate(entry.createdAt) }}</time></div></article>
            </div>
          </section>
          <aside class="detail-props">
            <h3>{{ t("基础信息") }}</h3>
            <fieldset class="defect-property-fields" :disabled="saving||!!rowSavingId||!canWrite">
            <label class="defect-severity-property"><span>{{ t("严重程度") }}</span><AppSelect :class="severitySelectClass(selected.severity)" :model-value="selected.severity" :options="severityOptions.slice(1)" :label="t('严重程度')" :disabled="saving||!!rowSavingId||!canWrite" @update:model-value="patch('severity', String($event))" /></label>
            <label>{{ t("优先级") }}<select :value="selected.priority" @change="patch('priority', ($event.target as HTMLSelectElement).value)"><option value="P0">P0</option><option value="P1">P1</option><option value="P2">P2</option><option value="P3">P3</option></select></label>
            <div class="defect-person-field"><label :for="'defect-assignee-'+selected.id">{{t('负责人')}}</label><MemberMultiSelect single :show-lead="false" :input-id="'defect-assignee-'+selected.id" :label="t('负责人')" :model-value="selected.assigneeUserId?[selected.assigneeUserId]:[]" :members="members" :snapshots="selected.assigneeUserId?[{id:selected.assigneeUserId,name:selected.assignee}]:[]" :legacy-name="!selected.assigneeUserId?selected.assignee:''" :disabled="saving||!!rowSavingId||!canWrite||scope.locked.value" @update:model-value="setDetailPerson('assigneeUserId',$event)"/></div>
            <div class="defect-person-field"><label :for="'defect-verifier-'+selected.id">{{t('验证人')}}</label><MemberMultiSelect single :show-lead="false" :input-id="'defect-verifier-'+selected.id" :label="t('验证人')" :model-value="selected.verifierUserId?[selected.verifierUserId]:[]" :members="[...testers,...otherVerifiers,...members.filter(member=>!member.active)]" :snapshots="selected.verifierUserId?[{id:selected.verifierUserId,name:selected.verifier}]:[]" :legacy-name="!selected.verifierUserId?selected.verifier:''" :disabled="saving||!!rowSavingId||!canWrite||scope.locked.value" @update:model-value="setDetailPerson('verifierUserId',$event)"/></div>
            <RequirementPicker :key="selected.id" :model-value="selected.requirementId??null" :disabled="saving||!!rowSavingId||!canWrite" @update:model-value="patch('requirementId',$event)"/>
            <label>{{ t("迭代") }}<select :value="selected.sprint" @change="patch('sprint', ($event.target as HTMLSelectElement).value)"><option value="待规划">{{ t("待规划") }}</option><option v-if="selected.sprint && selected.sprint !== '待规划' && !availableSprints.some(x => x.name === selected.sprint)" :value="selected.sprint">{{ selected.sprint }}</option><option v-for="x in availableSprints" :key="x.id" :value="x.name">{{ x.name }}</option></select></label>
            <CustomFieldInputs :disabled="!canWrite||saving||!!rowSavingId" object-type="defect" :model-value="selected.customFields || {}" inline @update:model-value="patch('customFields', $event)" />
            </fieldset>
          </aside>
        </div>
      </ResizableDrawer>
    </div>

   </div>
    <DefectComposer v-if="show" ref="composer" @created="onCreated" @cancel="show=false" />
    <DefectComposer v-if="editingId" :key="editingId" ref="editingComposer" :defect-id="editingId" @saved="onSaved" @cancel="editingId=null" />
  </div>
</template>
<style scoped>
.defect-person-field{min-width:0;margin:14px 0}.defect-person-field>label{display:block;margin-bottom:7px;color:var(--muted);font-size:12px}
.defect-detail-actions{display:flex;align-items:center;gap:8px;flex-shrink:0}.defect-property-fields{border:0;padding:0;margin:0;min-width:0}.defect-property-fields>.requirement-picker{margin:12px 0 18px}.defect-inline-status{font:inherit;border:1px solid var(--line);border-radius:7px;padding:5px 8px;max-width:150px;background:var(--surface);cursor:pointer}.defect-inline-status:disabled{cursor:not-allowed;opacity:.7}.defect-inline-status:focus-visible{outline:2px solid #1677ff;outline-offset:2px}
.defect-status-board{display:flex;align-items:stretch;gap:14px;overflow:auto;padding:8px 0 20px;min-height:300px}.defect-status-board>section{flex:0 0 255px;border:1px solid var(--line);border-radius:10px;background:var(--panel,#f8f9fc);padding:12px}.defect-status-board header{display:flex;justify-content:space-between;align-items:center;margin-bottom:12px;font-size:12px}.defect-status-board header>b{color:var(--muted)}.defect-board-card{display:grid;gap:10px;text-align:left;width:100%;padding:14px;margin:8px 0;border:1px solid var(--line);border-radius:8px;background:var(--surface,#fff);color:var(--ink);box-shadow:0 2px 6px #17203308}.defect-board-card:hover{border-color:var(--primary)}.defect-board-card small,.defect-board-card>span,.defect-status-board p{color:var(--muted);font-size:11px}.defect-board-card strong{font-size:13px;line-height:1.7}
@media(max-width:820px){
 .defects-content{min-width:0}.toolbar{flex-wrap:wrap;padding:10px 0;gap:8px}.toolbar .search{flex:1 1 100%;width:100%;min-width:0}
 .module-detail-drawer .drawer-head{padding:16px;flex:none}.drawer-title{flex-wrap:wrap;gap:10px}.drawer-title h2{width:100%;min-width:0;overflow-wrap:anywhere}
 .defect-detail-actions{flex-wrap:wrap;flex-shrink:1;max-width:100%;margin-left:auto}.defect-detail-actions>button{min-height:40px}
 .drawer-body{display:block;overflow:auto;overscroll-behavior:contain}.detail-main{width:100%;min-width:0;padding:18px 16px;overflow:visible}
 .detail-props{width:100%;max-width:100%;padding:18px 16px;overflow:visible;border-left:0;border-top:1px solid var(--line)}
 .detail-columns{grid-template-columns:minmax(0,1fr)}.detail-columns p,.detail-block p,.comment>div,.activity>div{min-width:0;overflow-wrap:anywhere}
 .trace-links{flex-wrap:wrap}.trace-links a{max-width:100%;overflow-wrap:anywhere}.section-title{flex-wrap:wrap;gap:10px}
 .table-card{max-width:100%;overflow:auto}.table-card table{min-width:760px}.defect-inline-status{min-height:40px}
 .defect-status-board{scroll-snap-type:x proximity}.defect-status-board>section{flex-basis:min(300px,calc(100vw - 62px));scroll-snap-align:start}.defect-board-card{overflow-wrap:anywhere}
}
</style>
<style scoped>.comment-reply-button{font-size:11px;min-height:32px;border:0;background:none;color:var(--primary,#665fe8);padding:6px 0;cursor:pointer}.comment-reply-button:disabled{opacity:.5;cursor:not-allowed}.comment-reply-button:focus-visible{outline:2px solid var(--primary,#665fe8);outline-offset:2px}@media(max-width:640px){.comment-reply-button{min-height:40px}.comment>div>time{display:block;margin-left:0}}</style>

<style scoped>
/* 缺陷池复用需求/迭代的层级：筛选区为次级表面，记录与详情正文为卡片表面。 */
.module-page{background:var(--background);color:var(--foreground)}
.defects-content .module-toolbar{background:var(--secondary);border:1px solid var(--border);border-radius:var(--radius);box-shadow:none}
.defects-content .module-toolbar .search{background:var(--card);border-color:var(--input);color:var(--foreground)}
.defects-content .module-toolbar{display:flex;align-items:center;flex-wrap:wrap;gap:8px;padding:10px 12px}
.defects-content .module-toolbar :deep(.defect-filter-select){min-width:142px;max-width:220px}
.defects-content .module-toolbar :deep(.defect-person-filter){min-width:154px;max-width:236px}
.defect-mine-filter{display:inline-flex;align-items:center;justify-content:center;min-height:36px;padding:7px 11px;border:1px solid var(--input);border-radius:calc(var(--radius) - 2px);background:var(--card);color:var(--muted-foreground);font:inherit;font-size:12px;white-space:nowrap;cursor:pointer;transition:background .15s ease,border-color .15s ease,color .15s ease}.defect-mine-filter:hover{border-color:color-mix(in srgb,var(--primary) 54%,var(--input));color:var(--foreground)}.defect-mine-filter.active{border-color:color-mix(in srgb,var(--primary) 64%,var(--input));background:color-mix(in srgb,var(--primary) 12%,var(--card));color:var(--primary);font-weight:650}.defect-mine-filter:focus-visible{outline:2px solid var(--ring,var(--primary));outline-offset:2px}.defect-filter-clear{min-height:32px;padding:5px 4px;font-size:12px;white-space:nowrap}
.defects-content .module-toolbar .result-count{margin-left:auto;white-space:nowrap}.defects-content .module-toolbar .segmented{flex:none}
.defects-content .table-card{background:var(--card);border-color:var(--border);box-shadow:none}
.defects-content .table-card table{background:var(--card);color:var(--foreground)}
.defects-content .table-card th{background:var(--secondary);border-color:var(--border);color:var(--muted-foreground);font-weight:600}
.defects-content .table-card td{background:var(--card);border-color:color-mix(in srgb,var(--border) 78%,var(--background));color:var(--foreground)}
.defects-content .table-card tbody tr:hover td{background:color-mix(in srgb,var(--primary) 7%,var(--background))}
.defect-inline-status{background:var(--card);border-color:var(--input);color:var(--foreground)}
.defect-inline-status:hover{background:color-mix(in srgb,var(--primary) 7%,var(--background));border-color:var(--primary)}
.severity-critical{--defect-severity-color:#c9354a;--defect-severity-surface:#fdecef}.severity-major{--defect-severity-color:#c55f25;--defect-severity-surface:#fff1e8}.severity-normal{--defect-severity-color:#3569c8;--defect-severity-surface:#edf3ff}.severity-minor{--defect-severity-color:#157258;--defect-severity-surface:#e9f8f1}
.defect-severity{display:inline-flex;align-items:center;gap:6px;min-width:0;padding:4px 8px;border:1px solid color-mix(in srgb,var(--defect-severity-color) 22%,transparent);border-radius:999px;background:color-mix(in srgb,var(--defect-severity-surface) 82%,var(--card));color:var(--defect-severity-color);font-size:12px;font-weight:650;line-height:1.15;white-space:nowrap}.defect-severity i{width:6px;height:6px;border-radius:50%;background:currentColor;box-shadow:0 0 0 2px color-mix(in srgb,currentColor 14%,transparent)}
.defect-row td:first-child{transition:box-shadow .15s ease}.defect-row.severity-critical td:first-child,.defect-row.severity-major td:first-child,.defect-row.severity-normal td:first-child,.defect-row.severity-minor td:first-child{box-shadow:inset 3px 0 0 var(--defect-severity-color)}
.defect-status-board{gap:16px;padding:8px 2px 20px;color:var(--foreground)}
.defect-status-board>section{background:var(--secondary);border-color:var(--border);border-radius:var(--radius);box-shadow:none}
.defect-status-board>section>header{border-bottom:1px solid color-mix(in srgb,var(--border) 78%,var(--background));padding-bottom:10px}
.defect-board-card{background:var(--card);border-color:var(--border);border-left:3px solid var(--defect-severity-color);color:var(--foreground);border-radius:calc(var(--radius) - 2px);box-shadow:none}
.defect-board-card:is(:hover,:focus-visible){background:var(--card);border-color:var(--primary);box-shadow:none}
.defect-board-card :is(small,span),.defect-status-board p{color:var(--muted-foreground)}
.defect-board-card:is(:hover,:focus-visible){border-left-color:var(--defect-severity-color)}.defect-board-meta{display:flex;align-items:center;justify-content:space-between;gap:8px;min-width:0}.defect-board-card .defect-severity{color:var(--defect-severity-color);background:color-mix(in srgb,var(--defect-severity-surface) 82%,var(--card))}.defect-board-assignee{overflow:hidden;text-overflow:ellipsis;white-space:nowrap;font-size:11px}
.defect-severity-property{gap:7px}.defect-severity-property>span{color:var(--muted-foreground);font-size:12px}.defect-severity-property :deep(.defect-severity-select){width:100%;min-width:0;border-left:3px solid var(--defect-severity-color);background:color-mix(in srgb,var(--defect-severity-surface) 42%,var(--background));color:var(--foreground)}.defect-severity-property :deep(.defect-severity-select:hover:not(:disabled)){border-left-color:var(--defect-severity-color);background:color-mix(in srgb,var(--defect-severity-surface) 60%,var(--background))}
.module-detail-drawer{background:var(--card);color:var(--card-foreground);box-shadow:none}
.module-detail-drawer .drawer-head{background:var(--card);border-color:var(--border)}
.module-detail-drawer :is(.detail-main,.detail-columns){background:var(--card);color:var(--card-foreground);border-color:var(--border)}
.module-detail-drawer .detail-props{background:var(--secondary);border-color:var(--border);color:var(--foreground)}
.module-detail-drawer .detail-props :is(input,select,textarea){background:var(--background);border-color:var(--input);color:var(--foreground)}
.module-detail-drawer .detail-block{border-color:var(--border)}.module-detail-drawer .detail-block p{color:var(--muted-foreground)}
.module-detail-drawer :is(.drawer-title h2,.detail-block h3,.detail-columns h3){color:var(--foreground)}
@media(max-width:820px){.defect-status-board{gap:12px;padding-bottom:16px}.defect-board-card{padding:12px}.defects-content .module-toolbar{border-radius:calc(var(--radius) - 2px)}.defects-content .module-toolbar :deep(.defect-filter-select),.defects-content .module-toolbar :deep(.defect-person-filter){flex:1 1 calc(50% - 6px);max-width:none}.defects-content .module-toolbar .result-count{margin-left:0}.defect-filter-clear{padding-inline:6px}}
@media(max-width:540px){.defects-content .module-toolbar :deep(.defect-filter-select),.defects-content .module-toolbar :deep(.defect-person-filter){flex-basis:100%}.defect-mine-filter{flex:1}.defects-content .module-toolbar .segmented{margin-left:auto}}
</style>

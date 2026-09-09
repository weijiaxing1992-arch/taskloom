<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { onBeforeRouteLeave, onBeforeRouteUpdate, useRoute, useRouter } from 'vue-router'
import { t, locale } from '../i18n'
import { useSettingsDialog, useSettingsScope } from '../components/settingsScope'
import RequirementStatusSettings from '../components/RequirementStatusSettings.vue'
import WorkflowSettings from '../components/WorkflowSettings.vue'
import AutomationRulesSettings from '../components/AutomationRulesSettings.vue'
import DatePicker from '../components/DatePicker.vue'
import MemberMultiSelect from '../components/MemberMultiSelect.vue'
import { isCalendarDate } from '../calendarDates'

type Field = { id?: number; objectType: string; key: string; name: string; type: string; description: string; required: boolean; searchable: boolean; filterable: boolean; listVisible: boolean; enabled: boolean; sortOrder: number; defaultValue: unknown; options: string[]; departmentId?: string; system?: boolean; installed?: boolean }
type Member = { id: string; name: string; email?: string; active: boolean; departmentIds?: string[]; departmentNames?: string[]; department?: string }
type Department = { id: string; name: string; parentId?: string; status: string }
type ChildSettings = { dirty: boolean; saving: boolean; externalChanged?: () => void }
const route = useRoute(), router = useRouter()
const settingsTabs = ['fields', 'statuses', 'workflow', 'automation'] as const
type SettingsTab = typeof settingsTabs[number]
function settingsTab(value: unknown): SettingsTab {
  return typeof value === 'string' && (settingsTabs as readonly string[]).includes(value) ? value as SettingsTab : 'fields'
}
const scope = useSettingsScope(), tab = ref<SettingsTab>(settingsTab(route.query.tab)), objectType = ref('requirement'), search = ref(''), memberSearch = ref('')
const items = ref<Field[]>([]), systemFields = ref<Field[]>([]), presets = ref<Field[]>([]), members = ref<Member[]>([]), departments = ref<Department[]>([])
const canManage = ref(false), loading = ref(false), saving = ref(false), directoryReady = ref(false), directoryError = ref(''), error = ref(''), notice = ref(''), opened = ref(false), editing = ref<Field | null>(null), baseline = ref('')
const statusSettings = ref<ChildSettings | null>(null), workflowSettings = ref<ChildSettings | null>(null), automationSettings = ref<ChildSettings | null>(null), noticeParams = ref<Record<string,string|number>>({})
const modal = useSettingsDialog(opened, () => close())
const deleteTarget = ref<Field | null>(null), deleteError = ref('')
const deleteOpened = computed(() => deleteTarget.value !== null)
const deleteModal = useSettingsDialog(deleteOpened, () => closeDelete())
const labels: Record<string,string> = { requirement:'需求', defect:'缺陷', test_case:'测试用例', sprint:'迭代' }
const fieldTypes: Record<string,string> = { text:'单行文本', textarea:'多行文本', number:'数字', single_select:'单选', multi_select:'多选', boolean:'布尔', date:'日期', datetime:'日期时间', user:'单用户', users:'多用户', richtext:'富文本', tags:'标签', attachments:'附件' }
const editableTypes = ['text','textarea','number','single_select','multi_select','boolean','date','user','users']
const emptyForm = (): Field => ({ objectType:objectType.value,key:'',name:'',type:'text',description:'',required:false,searchable:true,filterable:true,listVisible:true,enabled:true,sortOrder:10,defaultValue:null,options:[],departmentId:'' })
const form = reactive<Field>(emptyForm()), optionsText = ref(''), dateDefaultValid = ref(true)
const dirty = computed(() => opened.value && (snapshot() !== baseline.value || (form.type==='date'&&!dateDefaultValid.value)))
const anyDirty = computed(() => dirty.value || !!statusSettings.value?.dirty || !!workflowSettings.value?.dirty || !!automationSettings.value?.dirty)
const anySaving = computed(() => saving.value || !!statusSettings.value?.saving || !!workflowSettings.value?.saving || !!automationSettings.value?.saving)
const mutable = computed(() => canManage.value && !loading.value && !saving.value && !scope.locked.value)
const peopleField = computed(() => ['user','users'].includes(form.type))
const activeDepartments = computed(() => departments.value.filter(item => item.status === 'active'))
const eligibleMembers = computed(() => members.value.filter(member => member.active && (!form.departmentId || member.departmentIds?.includes(form.departmentId))))
const memberOptions = computed(() => eligibleMembers.value.filter(member => !memberSearch.value.trim() || [member.name,member.email||'',...(member.departmentNames||[]),member.department||''].join(' ').toLocaleLowerCase().includes(memberSearch.value.trim().toLocaleLowerCase())))
const selectedMemberIds = computed(() => Array.isArray(form.defaultValue) ? form.defaultValue.filter((id):id is string=>typeof id==='string') : typeof form.defaultValue==='string'&&form.defaultValue ? [form.defaultValue] : [])
const matching = (field:Field) => !search.value.trim() || [field.name,field.key,field.description,t(fieldTypes[field.type]||field.type)].join(' ').toLocaleLowerCase().includes(search.value.trim().toLocaleLowerCase())
const visibleSystemFields = computed(() => systemFields.value.filter(matching)), visibleItems = computed(() => items.value.filter(matching))
const missingCount = computed(() => presets.value.filter(item => !item.installed && !items.value.some(field => field.key === item.key)).length)
let loadVersion = 0, projectLeaveApproved = false
const message = (cause:unknown) => cause instanceof Error ? cause.message : '设置保存失败，请稍后重试'
function snapshot() { return JSON.stringify({ ...form, optionsText:optionsText.value }) }
function memberName(id:string) { return members.value.find(member=>member.id===id)?.name || id }
function memberLabel(member:Member) { return member.name+(member.email?' · '+member.email:' · '+member.id) }
function departmentName(id?:string) { return id ? departments.value.find(item=>item.id===id)?.name || id : t('不限部门') }
function displayDefault(field:Field):string {
  const value=field.defaultValue
  if(value==null||value===''||(Array.isArray(value)&&!value.length))return '—'
  if(typeof value==='boolean')return t(value?'是':'否')
  if(['user','users'].includes(field.type))return (Array.isArray(value)?value:[value]).map(value=>memberName(String(value))).join(locale.value==='en-US'?', ':'、')
  return Array.isArray(value)?value.join(locale.value==='en-US'?', ':'、'):String(value)
}
async function load() {
  if(saving.value||!scope.current())return
  const request=++loadVersion,target=objectType.value;loading.value=true;error.value='';directoryError.value=''
  const directory=Promise.all([scope.request<{items:Department[]}>('/departments'),scope.request<{items:Member[]}>('/members')]).then(([d,m])=>({departments:d.items,members:m.items})).catch(cause=>({error:message(cause)}))
  try {
    const [fields,catalog]=await Promise.all([scope.request<{items:Field[]}>('/field-definitions?objectType='+target),scope.request<{systemFields:Field[];presets:Field[];canManage:boolean}>('/field-presets?objectType='+target)])
    if(request!==loadVersion||target!==objectType.value)return
    items.value=fields.items;systemFields.value=catalog.systemFields;presets.value=catalog.presets;canManage.value=catalog.canManage
    const data=await directory
    if(request!==loadVersion||!scope.current())return
    if('error' in data){directoryError.value=data.error;directoryReady.value=false}else{departments.value=data.departments;members.value=data.members;directoryReady.value=true}
  }catch(cause){if(request===loadVersion&&scope.current())error.value=message(cause)}finally{if(request===loadVersion)loading.value=false}
}
function open(field?:Field) {
  if(!mutable.value||deleteTarget.value)return
  dateDefaultValid.value=true;editing.value=field||null;Object.assign(form,emptyForm(),field?JSON.parse(JSON.stringify(field)):{sortOrder:Math.max(0,...items.value.map(item=>item.sortOrder))+10})
  optionsText.value=(form.options||[]).join('\n');baseline.value=snapshot();memberSearch.value='';error.value='';opened.value=true
}
function close() { if(saving.value)return false;if(dirty.value&&!window.confirm(t('放弃尚未保存的设置修改？')))return false;opened.value=false;return true }
function selectObject(value:string) { if(value===objectType.value||saving.value)return;if(opened.value&&!close())return;closeDelete();objectType.value=value;items.value=[];systemFields.value=[];presets.value=[];canManage.value=false;search.value='';notice.value='';void load() }
function selectedOptions():string[] { return Array.isArray(form.defaultValue)?form.defaultValue.filter((value):value is string=>typeof value==='string'):[] }
function setOption(value:string,checked:boolean) { form.defaultValue=checked?[...new Set([...selectedOptions(),value])]:selectedOptions().filter(item=>item!==value) }
function toggleMember(id:string) { if(!mutable.value||!eligibleMembers.value.some(member=>member.id===id))return;form.defaultValue=selectedMemberIds.value.includes(id)?selectedMemberIds.value.filter(value=>value!==id):[...selectedMemberIds.value,id] }
function removeMember(id:string) { if(!mutable.value)return;form.defaultValue=form.type==='user'?null:selectedMemberIds.value.filter(value=>value!==id) }
function clearDefault() { if(mutable.value)form.defaultValue=form.type==='users'||form.type==='multi_select'?[]:null }
function validateAndBuild():Field {
  const body={...form,key:form.key.trim(),name:form.name.trim(),options:[...new Set(optionsText.value.split(/[\n,，]/).map(value=>value.trim()).filter(Boolean))],sortOrder:Number(form.sortOrder)}
  if(!body.name||!body.key)throw Error('字段名称和标识不能为空')
  if(!Number.isSafeInteger(body.sortOrder)||body.sortOrder<0)throw Error('排序须为非负整数')
  if(body.type==='number'){body.defaultValue=body.defaultValue===''||body.defaultValue==null?null:Number(body.defaultValue);if(body.defaultValue!==null&&!Number.isFinite(body.defaultValue))throw Error('默认值须为有效数字')}
  if(body.type==='boolean'&&body.defaultValue!==null&&typeof body.defaultValue!=='boolean')throw Error('请重新选择布尔默认值')
  if(['single_select','multi_select'].includes(body.type)&&!body.options.length)throw Error('选择字段必须配置选项')
  if(body.type==='multi_select'){if(body.defaultValue==null)body.defaultValue=[];if(!Array.isArray(body.defaultValue)||body.defaultValue.some(value=>!body.options.includes(value)))throw Error('默认值必须来自当前选项')}
  if(body.type==='single_select'&&body.defaultValue!=null&&body.defaultValue!==''&&!body.options.includes(String(body.defaultValue)))throw Error('默认值必须来自当前选项')
  if(peopleField.value){
    if(!directoryReady.value)throw Error('人员目录未加载完成，请重试后保存')
    if(body.type==='user'&&body.defaultValue!=null&&typeof body.defaultValue!=='string')throw Error('请从人员目录选择默认成员')
    const unchanged=editing.value&&JSON.stringify(body.defaultValue)===JSON.stringify(editing.value.defaultValue)&&(body.departmentId||'')===(editing.value.departmentId||'')
    if(!unchanged){if(body.type==='users'&&!Array.isArray(body.defaultValue)){if(body.defaultValue==null)body.defaultValue=[];else throw Error('请从人员目录选择默认成员')}
      if(selectedMemberIds.value.some(id=>!eligibleMembers.value.some(member=>member.id===id)))throw Error('默认成员必须是所选部门下的有效项目成员')}
  }
  if(body.type==='date'&&(!dateDefaultValid.value||(body.defaultValue&&!isCalendarDate(body.defaultValue))))throw Error('请填写有效的日期默认值')
  return body
}
async function save() {
  if(!mutable.value||!opened.value)return
  let body:Field;try{body=validateAndBuild()}catch(cause){error.value=message(cause);return}
  const request=loadVersion;error.value='';saving.value=true
  try{const result=await scope.request<Field>(editing.value?'/field-definitions/'+editing.value.id:'/field-definitions',{method:editing.value?'PATCH':'POST',body:JSON.stringify(body)});if(request!==loadVersion)return;items.value=[...items.value.filter(field=>field.id!==result.id),result].sort((a,b)=>a.sortOrder-b.sortOrder);opened.value=false;notice.value='字段配置已保存'}
  catch(cause){if(request===loadVersion&&scope.current())error.value=message(cause)}finally{if(request===loadVersion)saving.value=false}
}
async function toggle(field:Field) {
  if(!mutable.value||opened.value||deleteTarget.value)return
  const request=loadVersion;saving.value=true;error.value=''
  try{const result=await scope.request<Field>('/field-definitions/'+field.id,{method:'PATCH',body:JSON.stringify({...field,enabled:!field.enabled})});if(request===loadVersion)items.value=items.value.map(item=>item.id===result.id?result:item)}catch(cause){if(request===loadVersion&&scope.current())error.value=message(cause)}finally{if(request===loadVersion)saving.value=false}
}
async function applyPresets() {
  if(!mutable.value||opened.value||deleteTarget.value)return
  const request=loadVersion;saving.value=true;error.value=''
  try{const result=await scope.request<{createdCount:number;skippedCount:number;items:Field[]}>('/field-presets/apply',{method:'POST',body:JSON.stringify({objectType:objectType.value})});if(request===loadVersion){const mapped=new Map(items.value.map(item=>[item.key,item]));for(const item of result.items)mapped.set(item.key,item);items.value=[...mapped.values()].sort((a,b)=>a.sortOrder-b.sortOrder);notice.value='已补齐 {created} 个字段，保留 {skipped} 个已有配置';noticeParams.value={created:result.createdCount,skipped:result.skippedCount}}}catch(cause){if(request===loadVersion&&scope.current())error.value=message(cause)}finally{if(request===loadVersion)saving.value=false}
}
function beginDelete(field:Field) {
  if(!mutable.value||opened.value||deleteTarget.value||field.system||!field.id||!scope.current())return
  const current=items.value.find(item=>item.id===field.id&&item.key===field.key&&item.objectType===objectType.value)
  if(!current)return
  deleteTarget.value=JSON.parse(JSON.stringify(current));deleteError.value='';notice.value=''
}
function closeDelete() { if(saving.value)return false;deleteTarget.value=null;deleteError.value='';return true }
async function confirmDelete() {
  if(!mutable.value||!deleteTarget.value||!scope.current())return
  const target={...deleteTarget.value},request=loadVersion
  saving.value=true;deleteError.value=''
  try {
    const result=await scope.request<{deleted:boolean;id:number;preservedValueCount:number}>('/field-definitions/'+target.id,{method:'DELETE',body:JSON.stringify({confirmKey:target.key,objectType:target.objectType})})
    if(request!==loadVersion||!scope.current())return
    if(result.deleted!==true||result.id!==target.id||!Number.isSafeInteger(result.preservedValueCount)||result.preservedValueCount<0)throw Error('删除结果未确认，请刷新字段列表核实后再操作。')
    items.value=items.value.filter(field=>field.id!==target.id)
    presets.value=presets.value.map(field=>field.key===target.key||field.name===target.name?{...field,installed:true}:field)
    deleteTarget.value=null;notice.value='字段已删除，已保留 {count} 条历史字段值。';noticeParams.value={count:result.preservedValueCount}
  } catch(cause) { if(request===loadVersion&&scope.current())deleteError.value=message(cause) }
  finally { if(request===loadVersion)saving.value=false }
}
function confirmLeave() { if(anySaving.value)return false;return projectLeaveApproved||!anyDirty.value||window.confirm(t('应用设置尚未保存，确定离开并放弃修改？')) }
function beforeProjectChange(event:Event) { projectLeaveApproved=false;if(!confirmLeave())event.preventDefault();else projectLeaveApproved=true }
function beforeUnload(event:BeforeUnloadEvent) { const switching=projectLeaveApproved&&(localStorage.getItem('devflow-project')||'prj_orbit')!==scope.project;if(!switching&&(anyDirty.value||anySaving.value)){event.preventDefault();event.returnValue=''} }
function cancelProjectLeave() { projectLeaveApproved=false }
function keydown(event:KeyboardEvent) { if(opened.value&&event.key==='Escape'){event.preventDefault();event.stopImmediatePropagation();close()} }
function selectTab(value: string) {
  const next = settingsTab(value)
  if (next === tab.value) return
  const query = { ...route.query } as Record<string, string | string[] | undefined>
  if (next === 'fields') delete query.tab
  else query.tab = next
  void router.replace({ query })
}
onBeforeRouteLeave(confirmLeave);onBeforeRouteUpdate(confirmLeave)
watch(() => route.query.tab, value => { tab.value = settingsTab(value) })
onMounted(()=>{void load();window.addEventListener('devflow-before-project-change',beforeProjectChange);window.addEventListener('devflow-project-change-cancelled',cancelProjectLeave);window.addEventListener('beforeunload',beforeUnload)})
onBeforeUnmount(()=>{++loadVersion;window.removeEventListener('devflow-before-project-change',beforeProjectChange);window.removeEventListener('devflow-project-change-cancelled',cancelProjectLeave);window.removeEventListener('beforeunload',beforeUnload)})
</script>

<template>
  <div class="application-settings page-pad">
    <header class="page-heading"><div><span class="eyebrow">{{ t('项目设置') }}</span><h1>{{ t('应用设置') }}</h1><p>{{ t('统一管理项目字段、需求状态与角色流转规则。配置仅作用于当前项目。') }}</p></div><span class="settings-context-label">{{ t('项目级配置') }}</span></header>
    <nav class="settings-tabs" role="tablist" :aria-label="t('应用设置')"><button v-for="item in [['fields','字段设置'],['statuses','状态设置'],['workflow','工作流设置'],['automation','自动化规则']]" :key="item[0]" type="button" role="tab" :aria-selected="tab===item[0]" :class="{active:tab===item[0]}" @click="selectTab(item[0]!)">{{ t(item[1]!) }}</button></nav>
    <section v-show="tab==='fields'" role="tabpanel" :aria-label="t('字段设置')" :aria-busy="loading||saving">
      <header class="settings-section-head"><div><h2>{{ t('工作项字段') }}</h2><p>{{ t('系统字段支撑核心协作；自定义字段可按部门与业务流程扩展。') }}</p></div><div class="settings-buttons"><button type="button" class="btn" :disabled="!mutable||opened||!presets.length" @click="applyPresets">{{ t('一键补齐默认配置') }}<span v-if="missingCount"> · {{ missingCount }}</span></button><button type="button" class="btn primary" :disabled="!mutable" @click="open()">＋ {{ t('创建字段') }}</button></div></header>
      <p v-if="scope.locked.value" class="settings-error" role="alert">{{ t('项目或账号已变化，请刷新页面后继续') }}</p><p v-if="!loading&&!canManage" class="settings-note">{{ t('仅企业管理员和项目管理员可以修改配置。') }}</p>
      <div class="field-toolbar"><div class="settings-object-tabs"><button v-for="(label,key) in labels" :key="key" type="button" :class="{active:objectType===key}" :disabled="saving" @click="selectObject(key)">{{ t(label) }}</button></div><input v-model="search" type="search" :aria-label="t('搜索字段')" :placeholder="t('搜索名称、标识或类型')"></div>
      <p v-if="error&&!opened" class="settings-error" role="alert">{{ t(error) }} <button type="button" class="link" :disabled="loading||saving" @click="load">{{ t('重新加载') }}</button></p><p v-if="directoryError" class="settings-warning" role="alert">{{ t('人员目录暂不可用：{error}',{error:t(directoryError)}) }} <button type="button" class="link" :disabled="loading||saving" @click="load">{{ t('重试') }}</button></p><p v-if="notice" class="settings-success" role="status">{{ t(notice,noticeParams) }}</p>
      <div class="settings-field-group"><h3>{{ t('系统内置字段') }}<span>{{ systemFields.length }}</span></h3><p>{{ t('内置字段由核心模块统一维护，已存在的人员和权重字段不会重复创建。') }}</p></div>
      <div class="settings-table-wrap"><table class="settings-table"><thead><tr><th>{{ t('字段名称') }}</th><th>{{ t('稳定标识') }}</th><th>{{ t('类型') }}</th><th>{{ t('默认值') }}</th><th>{{ t('所属部门') }}</th><th>{{ t('必填') }}</th><th>{{ t('列表') }}</th><th>{{ t('筛选') }}</th><th>{{ t('启停') }}</th></tr></thead><tbody><tr v-if="loading&&!systemFields.length"><td colspan="9">{{ t('正在加载…') }}</td></tr><tr v-for="field in visibleSystemFields" :key="field.key"><td><b>{{ t(field.name) }}</b><small>{{ t(field.description||'系统内置字段') }}</small></td><td><code>{{ field.key }}</code></td><td>{{ t(fieldTypes[field.type]||field.type) }}</td><td>{{ displayDefault(field) }}</td><td>{{ departmentName(field.departmentId) }}</td><td>{{ field.required?t('是'):t('否') }}</td><td>{{ field.listVisible==null?'—':field.listVisible?t('是'):t('否') }}</td><td>{{ field.filterable==null?'—':field.filterable?t('是'):t('否') }}</td><td><span class="system-field-label">{{ t('内置') }}</span></td></tr><tr v-if="!loading&&!visibleSystemFields.length"><td colspan="9" class="settings-empty">{{ t('暂无匹配的内置字段') }}</td></tr></tbody></table></div>
      <div class="settings-field-group"><h3>{{ t('自定义字段') }}<span>{{ items.length }}</span></h3><p>{{ t('默认配置补齐仅新增缺失字段，不修改已有选项、默认值或启停设置。') }}</p></div>
      <div class="settings-table-wrap"><table class="settings-table"><thead><tr><th>{{ t('字段名称') }}</th><th>{{ t('稳定标识') }}</th><th>{{ t('类型') }}</th><th>{{ t('默认值') }}</th><th>{{ t('所属部门') }}</th><th>{{ t('必填') }}</th><th>{{ t('列表') }}</th><th>{{ t('筛选') }}</th><th>{{ t('启停') }}</th><th>{{ t('操作') }}</th></tr></thead><tbody><tr v-for="field in visibleItems" :key="field.id"><td><b>{{ field.name }}</b><small>{{ field.description||'—' }}</small></td><td><code>{{ field.key }}</code></td><td>{{ t(fieldTypes[field.type]||field.type) }}</td><td class="field-default-cell">{{ displayDefault(field) }}</td><td>{{ departmentName(field.departmentId) }}</td><td>{{ field.required?t('是'):t('否') }}</td><td>{{ field.listVisible?t('是'):t('否') }}</td><td>{{ field.filterable?t('是'):t('否') }}</td><td><span :class="field.enabled?'enabled-label':'disabled-label'">{{ field.enabled?t('已启用'):t('已停用') }}</span></td><td class="field-row-actions"><button type="button" class="link" :disabled="!mutable" @click="open(field)">{{ t('编辑') }}</button><button type="button" class="link" :disabled="!mutable||opened" @click="toggle(field)">{{ field.enabled?t('停用'):t('启用') }}</button><button type="button" class="link field-delete-action" :disabled="!mutable||opened||!!deleteTarget" :aria-label="t('删除字段 {name}',{name:field.name})" @click="beginDelete(field)">{{ t('删除') }}</button></td></tr><tr v-if="!loading&&!visibleItems.length"><td colspan="10" class="settings-empty">{{ t('暂无自定义字段，可创建字段或补齐默认配置。') }}</td></tr><tr v-if="loading&&!items.length"><td colspan="10">{{ t('正在加载…') }}</td></tr></tbody></table></div>
    </section>
    <RequirementStatusSettings v-show="tab==='statuses'" ref="statusSettings" @changed="workflowSettings?.externalChanged?.()" />
    <WorkflowSettings v-show="tab==='workflow'" ref="workflowSettings" />
    <AutomationRulesSettings v-show="tab==='automation'" ref="automationSettings" />
    <div v-if="deleteTarget" class="settings-modal-shade" @click.self="closeDelete"><section ref="deleteModal" tabindex="-1" class="settings-modal field-delete-modal" role="alertdialog" aria-modal="true" :aria-label="t('删除自定义字段')" :aria-busy="saving"><header><div><h2>{{ t('确认删除字段「{name}」？',{name:deleteTarget.name}) }}</h2><p>{{ t(labels[deleteTarget.objectType]!) }} · {{ deleteTarget.key }}</p></div><button type="button" :disabled="saving" :aria-label="t('关闭')" @click="closeDelete">×</button></header><div class="field-delete-explanation"><p>{{ t('删除后，字段将从表单、列表与筛选配置中移除。已有字段值和删除审计会保留，不会删除需求、缺陷或测试数据。') }}</p><p>{{ t('已保存视图若引用此字段，将提示配置失效，不会自动放宽筛选。') }}</p><p>{{ t('默认配置补齐与应用重启不会恢复已删除字段。') }}</p><p v-if="deleteError" class="settings-error" role="alert">{{ t(deleteError) }}</p></div><footer><button type="button" class="btn" :disabled="saving" @click="closeDelete">{{ t('取消') }}</button><button type="button" class="btn field-delete-confirm" :disabled="!mutable" @click="confirmDelete">{{ t(saving?'删除中…':'确认删除字段') }}</button></footer></section></div>
    <div v-if="opened" class="settings-modal-shade" @click.self="close" @keydown="keydown"><form ref="modal" tabindex="-1" class="settings-modal field-settings-modal" role="dialog" aria-modal="true" :aria-label="t(editing?'编辑字段':'创建字段')" @submit.prevent="save"><header><div><h2>{{ t(editing?'编辑字段':'创建字段') }}</h2><p>{{ t(labels[objectType]!) }} · {{ t('字段标识与类型创建后不可修改') }}</p></div><button type="button" :disabled="saving" :aria-label="t('关闭')" @click="close">×</button></header><fieldset :disabled="saving||scope.locked.value"><div class="field-form-grid"><label>{{ t('字段名称') }} *<input v-model="form.name" required maxlength="100" :aria-label="t('字段名称')"></label><label>{{ t('字段标识') }} *<input v-model="form.key" required :disabled="!!editing" maxlength="100" :aria-label="t('字段标识')" placeholder="risk_level"></label><label>{{ t('字段类型') }}<select v-model="form.type" :disabled="!!editing" :aria-label="t('字段类型')"><option v-for="type in editableTypes" :key="type" :value="type">{{ t(fieldTypes[type]!) }}</option></select></label><label>{{ t('排序') }}<input v-model.number="form.sortOrder" type="number" min="0" step="1" :aria-label="t('排序')"></label></div>
      <label v-if="peopleField">{{ t('所属部门') }}<select v-model="form.departmentId" :disabled="!directoryReady" :aria-label="t('所属部门')"><option value="">{{ t('不限部门') }}</option><option v-if="form.departmentId&&!activeDepartments.some(item=>item.id===form.departmentId)" :value="form.departmentId">{{ departmentName(form.departmentId) }} · {{ t('历史部门') }}</option><option v-for="department in activeDepartments" :key="department.id" :value="department.id">{{ department.name }}</option></select><small>{{ t('仅可新选该部门下的有效项目成员；既有默认成员不会被静默清空。') }}</small></label>
      <label v-if="['single_select','multi_select'].includes(form.type)">{{ t('选项') }}<textarea v-model="optionsText" rows="3" :aria-label="t('选项')" :placeholder="t('每行一个选项，也可用逗号分隔')"></textarea></label>
      <div class="field-default-head"><b>{{ t('默认值') }}</b><button type="button" class="link" :disabled="saving" @click="clearDefault">{{ t('清空默认值') }}</button></div>
      <template v-if="peopleField"><div v-if="selectedMemberIds.length" class="default-member-chips"><span v-for="id in selectedMemberIds" :key="id">{{ memberName(id) }}<small v-if="!eligibleMembers.some(member=>member.id===id)">{{ t('原默认成员') }}</small><button type="button" :disabled="saving" :aria-label="t('移除默认成员 {name}',{name:memberName(id)})" @click="removeMember(id)">×</button></span></div><input v-model="memberSearch" type="search" :disabled="!directoryReady" :aria-label="t('搜索默认成员')" :placeholder="t('搜索姓名、邮箱或部门')"><MemberMultiSelect v-if="form.type==='user'" single :model-value="selectedMemberIds" :members="members" :department-id="form.departmentId" :disabled="!directoryReady||!mutable" :label="t('默认成员')" :show-lead="false" @update:model-value="form.defaultValue=$event[0]||null" /><div v-else class="field-member-options" role="group" :aria-label="t('默认成员')"><label v-for="member in memberOptions" :key="member.id"><input type="checkbox" :disabled="!directoryReady" :checked="selectedMemberIds.includes(member.id)" @change="toggleMember(member.id)"><span>{{ member.name }}<small>{{ member.email||member.id }}<template v-if="member.departmentNames?.length"> · {{ member.departmentNames.join(', ') }}</template></small></span></label><p v-if="!memberOptions.length">{{ t('没有匹配的可用成员') }}</p></div><p v-if="directoryError" class="settings-warning">{{ t(directoryError) }}</p></template>
      <select v-else-if="form.type==='boolean'" :value="form.defaultValue==null?'':String(form.defaultValue)" :aria-label="t('默认值')" @change="form.defaultValue=($event.target as HTMLSelectElement).value===''?null:($event.target as HTMLSelectElement).value==='true'"><option value="">{{ t('未设置默认值') }}</option><option value="true">{{ t('是') }}</option><option value="false">{{ t('否') }}</option></select>
      <select v-else-if="form.type==='single_select'" v-model="form.defaultValue" :aria-label="t('默认值')"><option :value="null">{{ t('未设置默认值') }}</option><option v-for="option in optionsText.split(/[\n,，]/).map(value=>value.trim()).filter(Boolean)" :key="option" :value="option">{{ option }}</option></select>
      <div v-else-if="form.type==='multi_select'" class="field-default-options"><label v-for="option in optionsText.split(/[\n,，]/).map(value=>value.trim()).filter(Boolean)" :key="option"><input type="checkbox" :checked="selectedOptions().includes(option)" @change="setOption(option,($event.target as HTMLInputElement).checked)">{{ option }}</label></div>
      <textarea v-else-if="form.type==='textarea'" :value="String(form.defaultValue??'')" :aria-label="t('默认值')" @input="form.defaultValue=($event.target as HTMLTextAreaElement).value"></textarea>
      <DatePicker v-else-if="form.type==='date'" :model-value="String(form.defaultValue??'')" :label="t('默认值')" :disabled="!mutable" @update:model-value="form.defaultValue=$event" @validity-change="dateDefaultValid=$event" />
      <input v-else :type="form.type==='number'?'number':'text'" :step="form.type==='number'?'any':undefined" :value="form.defaultValue??''" :aria-label="t('默认值')" @input="form.defaultValue=($event.target as HTMLInputElement).value">
      <label>{{ t('说明') }}<textarea v-model="form.description" maxlength="2000" :aria-label="t('说明')"></textarea></label><div class="field-toggle-grid"><label><input v-model="form.required" type="checkbox">{{ t('必填') }}</label><label><input v-model="form.searchable" type="checkbox">{{ t('可搜索') }}</label><label><input v-model="form.filterable" type="checkbox">{{ t('可筛选') }}</label><label><input v-model="form.listVisible" type="checkbox">{{ t('列表显示') }}</label><label><input v-model="form.enabled" type="checkbox">{{ t('启用字段') }}</label></div></fieldset><p v-if="error" class="settings-error" role="alert">{{ t(error) }}</p><footer><button type="button" class="btn" :disabled="saving" @click="close">{{ t('取消') }}</button><button type="submit" class="btn primary" :disabled="!mutable">{{ saving?t('保存中…'):t('保存字段') }}</button></footer></form></div>
  </div>
</template>

<style>
.application-settings .field-delete-action{color:var(--danger,#ba3548)}.application-settings .field-delete-confirm{background:var(--danger,#ba3548);border-color:var(--danger,#ba3548);color:#fff}.application-settings .field-delete-explanation{padding:16px 24px;color:var(--ink,#334155);font-size:13px;line-height:1.8;overflow-wrap:anywhere}.application-settings .field-delete-modal h2{overflow-wrap:anywhere}.application-settings .field-delete-modal footer .btn{min-height:42px}@media(max-width:820px){.application-settings .field-delete-explanation{padding:12px 16px}.application-settings .field-delete-modal footer{flex-wrap:wrap}}
 .application-settings{width:100%;height:100%;min-height:0;min-width:0;overflow:auto;overscroll-behavior:contain;scrollbar-gutter:stable;scrollbar-width:auto;scrollbar-color:#94a3b8 var(--surface-soft,#f1f5f9);max-width:1600px;margin:auto;color:var(--text,#27334d)}.application-settings .page-heading{margin-bottom:24px}.settings-context-label{font-size:11px;color:var(--muted,#8691a5);border:1px solid var(--line,#e4e8f0);border-radius:20px;padding:6px 11px}.settings-tabs{display:flex;gap:30px;border-bottom:1px solid var(--line,#e4e8f0);margin-bottom:25px}.settings-tabs button{border:0;border-bottom:2px solid transparent;padding:13px 0;background:transparent;color:var(--muted,#8390a4);font-size:13px;font-weight:550}.settings-tabs button.active{color:var(--primary,#665fe8);border-bottom-color:var(--primary,#665fe8)}.application-settings .settings-section-head{display:flex;align-items:center;justify-content:space-between;gap:20px;flex-wrap:wrap}.application-settings .settings-section-head h2{margin:0 0 8px;font-size:17px;font-weight:650;letter-spacing:-.3px}.application-settings .settings-section-head p{margin:0;color:var(--muted,#8590a3);font-size:12px;line-height:1.7}.application-settings .settings-buttons{display:flex;gap:9px;align-items:center}.application-settings .settings-buttons .btn{font-size:12px;white-space:nowrap}.field-toolbar{display:flex;align-items:center;justify-content:space-between;gap:14px;margin:24px 0}.settings-object-tabs{display:flex;gap:4px;background:var(--surface-soft,#f0f2f7);padding:4px;border-radius:8px}.settings-object-tabs button{padding:7px 16px;font-size:12px;border:0;border-radius:5px;background:transparent;color:var(--muted,#8993a5)}.settings-object-tabs button.active{background:var(--surface,#fff);color:var(--text,#35415a);box-shadow:0 2px 5px #172d5009}.field-toolbar>input{max-width:260px;width:100%;border:1px solid var(--line,#e4e8f0);border-radius:7px;padding:9px 12px;font-size:12px;background:var(--surface,#fff)}.settings-field-group{margin:22px 0 12px}.settings-field-group h3{display:flex;align-items:center;gap:8px;font-size:13px;margin:0}.settings-field-group h3 span{font-size:10px;font-weight:400;color:#909bad;border:1px solid var(--line,#e7eaf1);border-radius:5px;padding:2px 5px}.settings-field-group p{color:var(--muted,#8b96a8);font-size:11px;margin:8px 0}.application-settings .settings-table-wrap{border:1px solid var(--line,#e6e9f1);border-radius:9px;overflow:auto;background:var(--surface,#fff)}.application-settings .settings-table{width:100%;border-collapse:collapse;font-size:12px;white-space:nowrap}.application-settings .settings-table th{padding:12px 15px;background:var(--surface-soft,#f8f9fc);text-align:left;font-size:11px;font-weight:500;color:var(--muted,#8c97aa);border-bottom:1px solid var(--line,#e9edf3)}.application-settings .settings-table td{padding:13px 15px;border-bottom:1px solid var(--line,#eef0f5);color:var(--text,#54617a)}.application-settings .settings-table tr:last-child td{border-bottom:0}.application-settings .settings-table b{font-size:12px;font-weight:550}.application-settings .settings-table small{display:block;font-size:10px;color:var(--muted,#98a2b4);margin-top:5px;white-space:normal;max-width:250px;line-height:1.6}.application-settings code{font-size:10px;color:var(--muted,#919cad);background:none}.application-settings .settings-table .settings-empty{padding:32px;color:var(--muted,#909bad);text-align:center}.field-default-cell{max-width:220px;overflow-wrap:anywhere;white-space:normal}.field-row-actions .link+.link{margin-left:11px}.system-field-label{border:1px solid var(--line,#e7eaf1);padding:3px 6px;border-radius:5px;font-size:10px;color:var(--muted,#99a2b1)}.application-settings .enabled-label{color:#199c82;font-size:11px}.application-settings .disabled-label{color:var(--muted,#99a2b1);font-size:11px}.application-settings .settings-note{color:var(--muted,#8995a8);font-size:11px;line-height:1.8}.application-settings .settings-error,.application-settings .settings-warning,.application-settings .settings-success{font-size:12px;line-height:1.7;margin:12px 0}.application-settings .settings-error{color:#c14c58}.application-settings .settings-warning{color:#b5802a}.application-settings .settings-success{color:#1b9a81}.application-settings .link:disabled{opacity:.4;cursor:not-allowed}.application-settings .settings-modal-shade{position:fixed;inset:0;z-index:250;background:#16233c62;backdrop-filter:blur(3px);display:flex;justify-content:center;align-items:center;padding:28px}.application-settings .settings-modal{width:min(570px,100%);max-height:calc(100dvh - 56px);overflow:auto;background:var(--surface,#fff);border:1px solid var(--line,#e4e8f0);border-radius:12px;box-shadow:0 28px 80px #0c173638}.application-settings .settings-modal header{display:flex;align-items:center;justify-content:space-between;padding:20px 23px;border-bottom:1px solid var(--line,#edf0f5)}.application-settings .settings-modal h2{font-size:16px;margin:0}.application-settings .settings-modal header p{font-size:11px;color:var(--muted,#919bad);margin:6px 0 0}.application-settings .settings-modal header>button{border:0;background:none;font-size:24px;color:var(--muted,#99a4b4);padding:0 5px}.application-settings .settings-modal>fieldset{display:grid;gap:17px;border:0;margin:0;padding:22px 24px;min-width:0}.application-settings .settings-modal label{display:grid;gap:7px;font-size:12px;color:var(--text,#56637b)}.application-settings .settings-modal input:not([type=checkbox]),.application-settings .settings-modal select,.application-settings .settings-modal textarea{border:1px solid var(--line,#dfe4ed);border-radius:6px;padding:9px 10px;font:inherit;color:var(--text,#46546f);background:var(--surface,#fff);width:100%;min-width:0;font-size:12px}.application-settings .settings-modal input:disabled,.application-settings .settings-modal select:disabled{opacity:.65;background:var(--surface-soft,#f6f8fb)}.application-settings .settings-modal textarea{min-height:75px;resize:vertical;line-height:1.6}.application-settings .settings-modal small{font-size:10px;color:var(--muted,#929caf);line-height:1.7}.application-settings .settings-modal footer{display:flex;justify-content:flex-end;gap:10px;padding:17px 23px;border-top:1px solid var(--line,#edf0f5);position:sticky;bottom:0;background:var(--surface,#fff)}.application-settings .settings-modal>.settings-error{padding:0 24px}.application-settings .settings-modal .settings-check{display:flex;align-items:center;gap:7px}.application-settings input[type=checkbox]{accent-color:#7068e8;flex:none;width:14px;height:14px}.application-settings .field-settings-modal{width:min(680px,100%)}.field-form-grid{display:grid;grid-template-columns:1fr 1fr;gap:16px}.field-default-head{display:flex;align-items:center;justify-content:space-between;font-size:12px}.field-default-head b{font-weight:500}.field-default-head .link{font-size:11px}.field-toggle-grid,.field-default-options{display:flex;align-items:center;flex-wrap:wrap;gap:15px}.application-settings .field-toggle-grid label,.application-settings .field-default-options label{display:flex;align-items:center;gap:6px}.default-member-chips{display:flex;gap:6px;flex-wrap:wrap}.default-member-chips>span{display:inline-flex;align-items:center;gap:5px;padding:5px 7px;font-size:11px;background:#7169e80e;border:1px solid #7169e825;border-radius:6px}.default-member-chips button{padding:0 3px;background:none;border:0;color:inherit;font-size:16px}.field-member-options{max-height:190px;overflow:auto;border:1px solid var(--line,#e5e9f2);border-radius:7px;padding:4px}.application-settings .field-member-options label{display:flex;align-items:center;gap:9px;padding:8px;border-radius:4px}.application-settings .field-member-options label:hover{background:var(--surface-soft,#f6f7fb)}.field-member-options small{display:block}.field-member-options p{font-size:11px;color:var(--muted,#909aae);padding:10px;text-align:center}@media(max-width:900px){.field-toolbar{flex-wrap:wrap}.application-settings .settings-section-head{align-items:flex-start}.application-settings .settings-modal-shade{padding:12px}.application-settings .settings-modal{max-height:calc(100dvh - 24px)}.field-form-grid{grid-template-columns:1fr}.settings-tabs{gap:22px}}
.application-settings::-webkit-scrollbar,.application-settings .settings-table-wrap::-webkit-scrollbar{width:12px;height:12px}.application-settings::-webkit-scrollbar-track,.application-settings .settings-table-wrap::-webkit-scrollbar-track{background:var(--surface-soft,#f1f5f9);border-radius:8px}.application-settings::-webkit-scrollbar-thumb,.application-settings .settings-table-wrap::-webkit-scrollbar-thumb{background:#94a3b8;border:3px solid var(--surface-soft,#f1f5f9);border-radius:8px}.application-settings::-webkit-scrollbar-thumb:hover,.application-settings .settings-table-wrap::-webkit-scrollbar-thumb:hover{background:#3b82f6}.application-settings .settings-table-wrap{scrollbar-gutter:stable;max-width:100%;overflow:auto}.application-settings .settings-table-wrap:focus-visible{outline:2px solid var(--primary);outline-offset:2px}
@media(max-width:820px){
 .application-settings{padding:16px;max-width:100%;height:auto;min-height:100%}.application-settings .page-heading{flex-wrap:wrap;gap:12px}.application-settings .settings-tabs{overflow:auto;max-width:100%;gap:20px}.application-settings .settings-tabs button{flex:none;white-space:nowrap;min-height:42px}
 .application-settings .settings-buttons{flex-wrap:wrap;max-width:100%;gap:8px}.application-settings .settings-buttons .btn{white-space:normal;text-align:center;min-height:40px}.application-settings .settings-object-tabs{max-width:100%;overflow:auto}.application-settings .settings-object-tabs button{flex:none;white-space:nowrap;min-height:40px}.application-settings .field-toolbar>input{max-width:100%;min-width:0;flex-basis:100%}
 .application-settings .settings-modal header{gap:12px;padding:16px}.application-settings .settings-modal header h2{overflow-wrap:anywhere}.application-settings .settings-modal>fieldset{padding:16px}.application-settings .settings-modal footer{flex-wrap:wrap;padding:14px 16px}.application-settings .field-form-grid{grid-template-columns:minmax(0,1fr)}.application-settings .field-default-head{flex-wrap:wrap;gap:8px}.application-settings .default-member-chips>span{max-width:100%;overflow-wrap:anywhere}.application-settings .field-member-options label>span{min-width:0;overflow-wrap:anywhere}
}

/*
 * 设置页以前保留了一套独立的灰蓝色常量，在暗色皮肤里会造成表头、弹层和输入
 * 框彼此不在同一层级。这里仅覆盖设置作用域，统一复用 shadcn 语义令牌，不改变
 * 字段数据、弹层焦点或滚动行为。
 */
.application-settings{
  color:var(--foreground);
  scrollbar-color:var(--scrollbar-thumb) var(--scrollbar-track);
}
.application-settings .settings-context-label,
.application-settings .system-field-label,
.application-settings .settings-field-group h3 span{
  color:var(--muted-foreground);
  border-color:var(--border);
  background:var(--secondary);
}
.application-settings :is(.settings-tabs,.settings-modal header,.settings-modal footer){
  border-color:var(--border);
}
.application-settings .settings-tabs button,
.application-settings .settings-object-tabs button,
.application-settings .settings-section-head p,
.application-settings .settings-field-group p,
.application-settings .settings-note,
.application-settings .settings-table small,
.application-settings .settings-modal :is(header p,small),
.application-settings code{
  color:var(--muted-foreground);
}
.application-settings .settings-tabs button.active{
  color:var(--primary);
  border-bottom-color:var(--primary);
}
.application-settings .settings-object-tabs{
  background:var(--secondary);
}
.application-settings .settings-object-tabs button.active{
  color:var(--foreground);
  background:var(--card);
  box-shadow:0 1px 2px color-mix(in srgb,var(--foreground) 10%,transparent);
}
.application-settings :is(.field-toolbar>input,.settings-modal input:not([type=checkbox]),.settings-modal select,.settings-modal textarea){
  color:var(--foreground);
  background:var(--background);
  border-color:var(--input);
}
.application-settings :is(.field-toolbar>input,.settings-modal input:not([type=checkbox]),.settings-modal select,.settings-modal textarea):focus-visible{
  outline:2px solid var(--ring);
  outline-offset:2px;
  border-color:var(--ring);
  box-shadow:0 0 0 3px color-mix(in srgb,var(--ring) 18%,transparent);
}
.application-settings :is(.settings-table-wrap,.settings-modal){
  background:var(--card);
  border-color:var(--border);
}
.application-settings .settings-table th{
  color:var(--muted-foreground);
  background:var(--secondary);
  border-color:var(--border);
}
.application-settings .settings-table td{
  color:var(--foreground);
  border-color:var(--border);
}
.application-settings .settings-table tbody tr:not(:has(.settings-empty)):hover{
  background:color-mix(in srgb,var(--accent) 55%,var(--card));
}
.application-settings .settings-modal-shade{
  background:color-mix(in srgb,#060c19 66%,transparent);
}
.application-settings .settings-modal{
  box-shadow:0 24px 72px color-mix(in srgb,#060c19 38%,transparent);
}
.application-settings .settings-modal input:disabled,
.application-settings .settings-modal select:disabled{
  background:var(--secondary);
  color:var(--muted-foreground);
}
.application-settings .default-member-chips>span{
  color:var(--accent-foreground);
  background:var(--accent);
  border-color:color-mix(in srgb,var(--primary) 25%,var(--border));
}
.application-settings .field-member-options{
  background:var(--background);
  border-color:var(--border);
}
.application-settings .field-member-options label:hover{
  background:var(--accent);
}
.application-settings :is(.enabled-label,.settings-success){color:var(--success)}
.application-settings .settings-warning{color:var(--warning)}
.application-settings .settings-error{color:var(--danger)}
.application-settings .field-delete-confirm{
  color:var(--destructive-foreground);
  background:var(--destructive);
  border-color:var(--destructive);
}
.application-settings ::-webkit-scrollbar-track{background:var(--scrollbar-track)}
.application-settings ::-webkit-scrollbar-thumb{
  background:var(--scrollbar-thumb);
  border-color:var(--scrollbar-track);
}
.application-settings ::-webkit-scrollbar-thumb:hover{background:color-mix(in srgb,var(--scrollbar-thumb) 74%,var(--foreground))}
</style>

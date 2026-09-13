<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { onBeforeRouteLeave, onBeforeRouteUpdate } from 'vue-router'
import { apiDownload } from '../api'
import { t, locale } from '../i18n'
import { departmentPath, orderedDepartments, orderedDepartmentIds, memberPrimaryDepartment, sortOrganizationMembers, permits, type Department, type OrganizationContext, type OrganizationMember, type ProjectMembership } from '../organization'
import { useSettingsScope } from './settingsScope'
import OrganizationModal from './OrganizationModal.vue'
import OrganizationMemberBulkDialog from './OrganizationMemberBulkDialog.vue'
import UserWecomWebhook from './UserWecomWebhook.vue'
import { Button } from './ui/button'
const props=defineProps<{context:OrganizationContext}>(),emit=defineEmits<{(event:'changed'):void}>(),scope=useSettingsScope()
const items=ref<OrganizationMember[]>([]),departments=ref<Department[]>([]),session=ref<any>(null),query=ref(''),department=ref(''),status=ref(''),page=ref(1)
const loading=ref(true),saving=ref(false),error=ref(''),notice=ref(''),editing=ref<OrganizationMember|null>(null),opened=ref(false),baseline=ref('')
const discardMemberOpen=ref(false),discardMemberPrompt=ref<HTMLElement|null>(null)
const canManage=computed(()=>permits(props.context,'members.manage')&&!scope.locked.value)
const showPassword=ref(false)
const form=reactive({name:'',email:'',employeeNo:'',initialPassword:'',tenantRole:'member',active:true,departmentIds:[] as string[],primaryDepartmentId:'',projectMemberships:[] as ProjectMembership[]})
const importOpen=ref(false),csv=ref(''),preview=ref<any>(null),importConfirmed=ref(false),previewing=ref(false),fileReading=ref(false)
let fileReadVersion=0
function invalidateFileRead(){fileReadVersion++;fileReading.value=false}
watch(importOpen,()=>invalidateFileRead(),{flush:'sync'})
// 代访问是一项受审计的只读辅助入口。默认理由降低日常查看工作的录入成本，
// 但仍允许管理员在提交前补充真实的访问背景。
const defaultImpersonationReason='查看工作'
const impersonating=ref<OrganizationMember|null>(null),reason=ref('')
const webhookMember=ref<OrganizationMember|null>(null),webhookEditor=ref<InstanceType<typeof UserWecomWebhook>|null>(null),webhookBusy=ref(false)
const deleteTarget=ref<OrganizationMember|null>(null),deleteConfirmed=ref(false)
type BulkAction='activate'|'deactivate'|'delete'|'wecom-config'
type BulkWebhook={enabled?:boolean;url?:string}
const selectedIds=ref<string[]>([]),selectionNotice=ref(''),bulkAction=ref<BulkAction|null>(null),bulkTargets=ref<OrganizationMember[]>([]),bulkUncertain=ref(false)
const bulkDialog=ref<InstanceType<typeof OrganizationMemberBulkDialog>|null>(null)
const canBulk=computed(()=>canManage.value&&!!session.value?.user?.id&&!session.value?.impersonation)
const selectionBusy=computed(()=>loading.value||saving.value||previewing.value||webhookBusy.value||scope.locked.value||opened.value||importOpen.value||!!impersonating.value||!!webhookMember.value||!!deleteTarget.value||!!bulkAction.value)
const selectedMembers=computed(()=>items.value.filter(member=>selectedIds.value.includes(member.id)))
const departmentOptions=computed(()=>orderedDepartments(departments.value,locale.value))
const primaryDepartmentOptions=computed(()=>orderedDepartmentIds(form.departmentIds,departments.value,locale.value))
const filtered=computed(()=>sortOrganizationMembers(items.value.filter(item=>(!query.value.trim()||[item.name,item.email,item.employeeNo].join(' ').toLowerCase().includes(query.value.trim().toLowerCase()))&&(!department.value||item.departmentIds.includes(department.value))&&(!status.value||memberStatus(item)===status.value)),departments.value,locale.value))
const paged=computed(()=>filtered.value.slice((page.value-1)*30,page.value*30))
const allPageSelected=computed(()=>!!paged.value.length&&paged.value.every(member=>selectedIds.value.includes(member.id)))
const somePageSelected=computed(()=>!allPageSelected.value&&paged.value.some(member=>selectedIds.value.includes(member.id)))
const pageGroups=computed(()=>{const groups=new Map<string,{id:string;name:string;members:OrganizationMember[]}>();for(const member of paged.value){const item=memberPrimaryDepartment(member,departmentOptions.value),id=item?.id||'';if(!groups.has(id))groups.set(id,{id,name:item?departmentPath(item,departments.value):'',members:[]});groups.get(id)!.members.push(member)}return [...groups.values()]})
// 成员弹窗只比较自己的草稿；全页离开保护仍汇总导入、代访问等其他编辑状态。
const memberDirty=computed(()=>opened.value&&JSON.stringify(form)!==baseline.value)
const dirty=computed(()=>memberDirty.value||importOpen.value&&!!csv.value||!!impersonating.value&&reason.value.trim()!==defaultImpersonationReason||!!bulkDialog.value?.dirty)
let projectLeaveApproved=false,loadVersion=0,disposed=false
function clearSelection(text=''){selectedIds.value=[];selectionNotice.value=text}
function clearBulk(){bulkAction.value=null;bulkTargets.value=[];bulkUncertain.value=false}
// 筛选变化不保留隐形目标，避免用户在另一组结果上误提交旧选择；翻页则保留明确勾选。
watch([query,department,status],()=>{page.value=1;if(selectedIds.value.length)clearSelection('筛选已变化，已清空之前的选择');if(!saving.value)clearBulk()},{flush:'sync'})
watch(scope.locked,value=>{if(value){loadVersion++;clearSelection();clearBulk();form.initialPassword='';invalidateFileRead();csv.value='';preview.value=null;importConfirmed.value=false}},{flush:'sync'})
// 主部门回填同步完成后再建立快照，避免打开旧成员时被延迟 watcher 误判为有修改。
watch(()=>form.departmentIds,ids=>{if(!ids.includes(form.primaryDepartmentId))form.primaryDepartmentId=ids[0]||''},{deep:true,flush:'sync'})
watch(memberDirty,value=>{if(!value)discardMemberOpen.value=false},{flush:'sync'})
const message=(cause:unknown)=>cause instanceof Error?cause.message:'操作失败，请稍后重试'
async function load(){if(disposed||!scope.current())return;const request=++loadVersion;if(selectedIds.value.length)clearSelection('成员列表已重新加载，请重新选择');if(!saving.value)clearBulk();loading.value=true;error.value='';try{const [members,directory,auth]=await Promise.all([scope.request<{items:OrganizationMember[]}>('/organization/members'),scope.request<{items:Department[]}>('/organization/departments'),scope.request('/session')]);if(disposed||request!==loadVersion||!scope.current())return;items.value=members.items;departments.value=directory.items;session.value=auth;page.value=Math.min(page.value,Math.max(1,Math.ceil(filtered.value.length/30)))}catch(cause){if(!disposed&&request===loadVersion&&scope.current())error.value=message(cause)}finally{if(!disposed&&request===loadVersion)loading.value=false}}
function toggleMemberSelection(id:string){
  if(!canBulk.value||selectionBusy.value||!items.value.some(member=>member.id===id))return
  if(selectedIds.value.includes(id))selectedIds.value=selectedIds.value.filter(value=>value!==id)
  else if(selectedIds.value.length>=200){selectionNotice.value='每次最多选择 200 位成员，请缩小筛选范围或分批操作';return}
  else selectedIds.value=[...selectedIds.value,id]
  selectionNotice.value=''
}
function togglePageSelection(){
  if(!canBulk.value||selectionBusy.value||!paged.value.length)return
  const ids=paged.value.map(member=>member.id)
  if(allPageSelected.value)selectedIds.value=selectedIds.value.filter(id=>!ids.includes(id))
  else{const next=[...new Set([...selectedIds.value,...ids])];if(next.length>200){selectionNotice.value='每次最多选择 200 位成员，请缩小筛选范围或分批操作';return}selectedIds.value=next}
  selectionNotice.value=''
}
function selectAllFiltered(){
  if(!canBulk.value||selectionBusy.value)return
  if(filtered.value.length>200){selectionNotice.value='每次最多选择 200 位成员，请缩小筛选范围或分批操作';return}
  selectedIds.value=filtered.value.map(member=>member.id);selectionNotice.value='已选择全部筛选结果，包含其他分页中的成员'
}
function bulkReason(action:BulkAction,targets=selectedMembers.value){
  if(!canBulk.value)return '当前身份不能批量管理成员'
  if(!targets.length||targets.length!==selectedIds.value.length||targets.some(member=>!selectedIds.value.includes(member.id)))return '请重新选择有效成员'
  if(targets.length>200)return '每次最多选择 200 位成员，请缩小筛选范围或分批操作'
  if(action==='delete'&&!props.context.isTenantAdmin)return '仅企业管理员可以批量删除成员'
  if(['deactivate','delete'].includes(action)&&targets.some(member=>member.id===session.value.user.id||member.tenantRole==='tenant_admin'))return '批量停用和删除不能包含当前账号或企业管理员，请取消勾选这些成员'
  if(!props.context.isTenantAdmin&&targets.some(member=>(action!=='wecom-config'||member.id!==session.value.user.id)&&(member.tenantRole==='tenant_admin'||member.groupIds.length>0)))return '所选包含管理员或权限组成员，需要企业管理员操作'
  return ''
}
function prepareBulk(action:BulkAction){
  if(selectionBusy.value||!scope.current())return
  const issue=bulkReason(action);if(issue){error.value=issue;return}
  // 确认窗口固定保存当时的完整名单，不以动态查询重新推断目标，也不静默剔除管理员。
  bulkTargets.value=selectedMembers.value.map(member=>({...member,groupIds:[...member.groupIds],departmentIds:[...member.departmentIds],projectMemberships:member.projectMemberships.map(item=>({...item}))}))
  bulkUncertain.value=false;error.value='';bulkAction.value=action;projectLeaveApproved=false
}
function closeBulk(){if(!saving.value)clearBulk()}
async function confirmBulk(webhook?:BulkWebhook){
  if(!bulkAction.value||saving.value||bulkUncertain.value||disposed||!scope.current())return
  const action=bulkAction.value,targets=bulkTargets.value,issue=bulkReason(action,targets)
  if(issue){error.value=issue;return}
  const userIds=targets.map(member=>member.id)
  if(action==='wecom-config'&&(!webhook||Object.keys(webhook).some(key=>!['url','enabled'].includes(key))||webhook.url===undefined&&typeof webhook.enabled!=='boolean')){error.value='请确认机器人操作和地址';return}
  saving.value=true;error.value='';notice.value=''
  try{
    const result=await scope.request<{affected:number;userIds:string[]}>('/organization/members/bulk',{method:'POST',body:JSON.stringify({action,userIds,...(action==='wecom-config'?{webhook}:{})})})
    if(disposed||!scope.current())return
    if(!result||result.affected!==userIds.length||!Array.isArray(result.userIds)||result.userIds.length!==userIds.length||new Set(result.userIds).size!==userIds.length||result.userIds.some(id=>!userIds.includes(id))){bulkUncertain.value=true;error.value='批量操作结果未能完整确认，请先刷新核实，勿重复提交';return}
    clearBulk();clearSelection();notice.value=t('批量操作已完成，共处理 {count} 位成员',{count:result.affected})
    await load();if(!disposed&&scope.current())emit('changed')
  }catch(cause){if(!disposed&&scope.current()){if(cause&&typeof cause==='object'&&'status' in cause&&cause.status===0)bulkUncertain.value=true;error.value=message(cause)}}
  finally{if(!disposed)saving.value=false}
}
function open(member?:OrganizationMember){
  if(!canManage.value||saving.value||webhookMember.value||bulkAction.value)return
  const departmentIds=[...(member?.departmentIds||[])]
  const primaryDepartmentId=member?.primaryDepartmentId&&departmentIds.includes(member.primaryDepartmentId)?member.primaryDepartmentId:departmentIds[0]||''
  editing.value=member||null;showPassword.value=false
  Object.assign(form,{name:member?.name||'',email:member?.email||'',employeeNo:member?.employeeNo||'',initialPassword:'',tenantRole:member?.tenantRole||'member',active:member?.active??true,departmentIds,primaryDepartmentId,projectMemberships:member?.projectMemberships.map(item=>({...item,roles:[...(item.roles??[item.role])]}))||[]})
  baseline.value=JSON.stringify(form);discardMemberOpen.value=false;projectLeaveApproved=false;error.value='';opened.value=true
}
function discardMemberChanges(){if(saving.value)return;opened.value=false;discardMemberOpen.value=false;editing.value=null;form.initialPassword='';error.value=''}
function keepMemberEditing(){discardMemberOpen.value=false}
function close(){
  if(saving.value||!opened.value)return
  if(!memberDirty.value){discardMemberChanges();return}
  // 嵌入式浏览器可能不显示原生 confirm。把丢弃选择放在当前弹窗内，点击取消必有可见反馈。
  discardMemberOpen.value=true
  void nextTick(()=>{if(discardMemberOpen.value)discardMemberPrompt.value?.focus()})
}
async function save(){if(!opened.value||discardMemberOpen.value||!canManage.value||saving.value)return;error.value='';saving.value=true;try{const body={...form};if(!body.initialPassword)delete (body as Partial<typeof body>).initialPassword;await scope.request(editing.value?'/organization/members/'+editing.value.id:'/organization/members',{method:editing.value?'PATCH':'POST',body:JSON.stringify(body)});opened.value=false;discardMemberOpen.value=false;form.initialPassword='';notice.value='成员信息已保存';await load();emit('changed')}catch(cause){error.value=message(cause)}finally{saving.value=false}}
function departmentNames(ids:string[]){return orderedDepartmentIds(ids,departments.value,locale.value).map(id=>{const item=departments.value.find(item=>item.id===id);return item?departmentPath(item,departments.value):id}).join('、')||'—'}
function memberStatus(member:OrganizationMember){return !member.active?'inactive':member.operationDisabled?'disabled':'enabled'}
function canToggleStatus(member:OrganizationMember){return canManage.value&&!session.value?.impersonation&&member.id!==session.value?.user.id&&(props.context.isTenantAdmin||(member.tenantRole!=='tenant_admin'&&!member.groupIds.length))}
async function toggleStatus(member:OrganizationMember){
  if(!member.active||saving.value||loading.value||bulkAction.value||!canToggleStatus(member))return
  saving.value=true;error.value='';notice.value=''
  try{await scope.request('/organization/members/'+encodeURIComponent(member.id),{method:'PATCH',body:JSON.stringify({operationDisabled:!member.operationDisabled})});notice.value=member.operationDisabled?'已恢复该成员的业务操作权限':'已禁用该成员的业务操作；账号仍可登录';await load();emit('changed')}
  catch(cause){error.value=message(cause)}finally{saving.value=false}
}
async function activateMember(member:OrganizationMember){
  if(member.active||selectionBusy.value||!canToggleStatus(member)||disposed||!scope.current())return
  saving.value=true;error.value='';notice.value=''
  try{await scope.request('/organization/members/'+encodeURIComponent(member.id),{method:'PATCH',body:JSON.stringify({active:true,operationDisabled:false})});if(disposed||!scope.current())return;notice.value=t('成员 {name} 已激活，请确认登录凭据和项目权限',{name:member.name});await load();if(!disposed&&scope.current())emit('changed')}
  catch(cause){if(!disposed&&scope.current())error.value=message(cause)}finally{if(!disposed)saving.value=false}
}
// 删除企业成员只允许当前租户管理员发起；服务端仍会在写事务内再次校验，前端仅负责减少误操作入口。
function canDeleteMember(member:OrganizationMember|null|undefined){return !!member&&props.context.isTenantAdmin&&!scope.locked.value&&!session.value?.impersonation&&member.id!==session.value?.user?.id}
function prepareDelete(member:OrganizationMember){if(!canDeleteMember(member)||saving.value||opened.value||importOpen.value||impersonating.value||webhookMember.value||bulkAction.value)return;deleteTarget.value=member;deleteConfirmed.value=false;error.value=''}
function closeDelete(){if(saving.value)return;deleteTarget.value=null;deleteConfirmed.value=false;error.value=''}
async function removeMember(){
  if(!deleteTarget.value||!deleteConfirmed.value||!canDeleteMember(deleteTarget.value)||saving.value)return
  saving.value=true;error.value='';notice.value=''
  try{await scope.request('/organization/members/'+encodeURIComponent(deleteTarget.value.id),{method:'DELETE'});const name=deleteTarget.value.name;deleteTarget.value=null;deleteConfirmed.value=false;notice.value=t('成员 {name} 已删除',{name});await load();emit('changed')}
  catch(cause){error.value=message(cause)}finally{saving.value=false}
}
function projectName(id:string){return props.context.projects.find(item=>item.id===id)?.name||id}
function roleName(key:string){return key==='tenant_admin'?'企业管理员':props.context.roles.find(item=>item.key===key)?.name||key}
function projectRoleOptions(membership:ProjectMembership){const options=props.context.roles.filter(role=>props.context.isTenantAdmin||role.key!=='project_admin');const previous=editing.value?.projectMemberships.find(item=>item.projectId===membership.projectId);if(previous&&['tenant_admin','project_admin'].includes(previous.role)&&!options.some(item=>item.key===previous.role))return [{key:previous.role,name:roleName(previous.role)},...options];return options}
function download(blob:Blob,name:string){const url=URL.createObjectURL(blob),a=document.createElement('a');a.href=url;a.download=name;a.click();setTimeout(()=>URL.revokeObjectURL(url),1000)}
async function exportMembers(){if(saving.value||!scope.current()||!permits(props.context,'members.export'))return;saving.value=true;error.value='';try{const data=await apiDownload('/organization/members/export');if(scope.current())download(data,'devflow-members.csv')}catch(cause){error.value=message(cause)}finally{saving.value=false}}
function template(){download(new Blob(['\uFEFFname,email,employeeNo,departmentCode,projectCode,projectRole\r\n'],{type:'text/csv;charset=utf-8'}),'devflow-member-template.csv')}
async function chooseFile(event:Event){
  const input=event.target as HTMLInputElement,file=input.files?.[0]
  // 输入框立即复位；后续异步读取不得再触碰已关闭或已改选文件的输入框。
  input.value=''
  if(disposed||!importOpen.value||saving.value||previewing.value||!scope.current())return
  invalidateFileRead();preview.value=null;importConfirmed.value=false;error.value=''
  if(!file)return
  if(file.size>1048576){error.value='CSV 文件不能超过 1 MB';return}
  const version=fileReadVersion
  const current=()=>!disposed&&importOpen.value&&version===fileReadVersion&&scope.current()
  fileReading.value=true
  try{const contents=await file.text();if(current())csv.value=contents}
  catch{if(current())error.value='无法读取 CSV 文件'}
  finally{if(current())fileReading.value=false}
}
watch(csv,()=>{preview.value=null;importConfirmed.value=false})
async function previewImport(){if(fileReading.value||previewing.value||saving.value||!csv.value.trim())return;previewing.value=true;error.value='';try{preview.value=await scope.request('/organization/members/import/preview',{method:'POST',body:JSON.stringify({csv:csv.value})});importConfirmed.value=false}catch(cause){error.value=message(cause)}finally{previewing.value=false}}
async function commitImport(){if(fileReading.value||!preview.value?.canCommit||!importConfirmed.value||saving.value)return;saving.value=true;error.value='';try{await scope.request('/organization/members/import/commit',{method:'POST',body:JSON.stringify({previewId:preview.value.previewId})});csv.value='';preview.value=null;importOpen.value=false;notice.value='成员已导入，默认未激活，请逐一审核启用';await load();emit('changed')}catch(cause){error.value=message(cause)}finally{saving.value=false}}
function closeImport(){if(saving.value||previewing.value)return;if(csv.value&&!window.confirm(t('放弃尚未保存的设置修改？')))return;importOpen.value=false;csv.value='';preview.value=null}
// 首次改密账号不能由管理员代改密码，但企业管理员仍可通过受限代看核对
// 该成员实际能看到的工作范围。服务端会将这类会话永久标为只读并拦截所有写请求。
function canImpersonateMember(member:OrganizationMember|null|undefined){return !!member&&session.value?.canImpersonate===true&&!session.value?.impersonation&&!scope.locked.value&&member.id!==session.value?.user?.id&&member.active}
function validImpersonationReason(value:string){const length=Array.from(value.trim()).length;return length>=4&&length<=500}
function prepareImpersonation(member:OrganizationMember){if(!canImpersonateMember(member)||selectionBusy.value||webhookMember.value||bulkAction.value)return;impersonating.value=member;reason.value=defaultImpersonationReason;error.value=''}
function closeImpersonation(){if(saving.value)return;impersonating.value=null;reason.value='';error.value=''}
function canConfigureWebhook(member:OrganizationMember){return canManage.value&&!session.value?.impersonation&&(props.context.isTenantAdmin||member.tenantRole!=='tenant_admin'||member.id===session.value?.user?.id)}
function openWebhook(member:OrganizationMember){if(!canConfigureWebhook(member)||saving.value||opened.value||importOpen.value||impersonating.value||webhookMember.value||bulkAction.value)return;webhookMember.value=member;webhookBusy.value=false}
function closeWebhook(){if(webhookBusy.value||!(webhookEditor.value?.requestClose()??true))return;webhookMember.value=null}
async function startImpersonation(){if(saving.value||!canImpersonateMember(impersonating.value)||!validImpersonationReason(reason.value))return;saving.value=true;error.value='';try{const result=await scope.request<{projectId:string}>('/auth/impersonation',{method:'POST',body:JSON.stringify({userId:impersonating.value!.id,reason:reason.value.trim()})});localStorage.setItem('devflow-project',result.projectId);location.href='/my-work'}catch(cause){error.value=message(cause)}finally{saving.value=false}}
function canLeave(){return !saving.value&&!previewing.value&&(bulkDialog.value?.canLeave()??true)&&(projectLeaveApproved||((webhookEditor.value?.canLeave()??true)&&(!dirty.value||window.confirm(t('放弃尚未保存的设置修改？')))))}
function beforeProjectChange(event:Event){projectLeaveApproved=false;const mayLeave=!saving.value&&!previewing.value&&!webhookBusy.value&&(bulkDialog.value?.canLeave()??true)&&(!dirty.value||window.confirm(t('放弃尚未保存的设置修改？')));if(!mayLeave)event.preventDefault();else projectLeaveApproved=true}
function cancelProjectLeave(){projectLeaveApproved=false}
function beforeUnload(event:BeforeUnloadEvent){const switching=projectLeaveApproved&&(localStorage.getItem('devflow-project')||'prj_orbit')!==scope.project;if(!switching&&(dirty.value||saving.value)){event.preventDefault();event.returnValue=''}}
onBeforeRouteLeave(canLeave);onBeforeRouteUpdate(canLeave)
onMounted(()=>{void load();window.addEventListener('beforeunload',beforeUnload);window.addEventListener('devflow-before-project-change',beforeProjectChange);window.addEventListener('devflow-project-change-cancelled',cancelProjectLeave)})
onBeforeUnmount(()=>{disposed=true;loadVersion++;clearSelection();clearBulk();form.initialPassword='';invalidateFileRead();csv.value='';preview.value=null;importConfirmed.value=false;window.removeEventListener('beforeunload',beforeUnload);window.removeEventListener('devflow-before-project-change',beforeProjectChange);window.removeEventListener('devflow-project-change-cancelled',cancelProjectLeave)})
</script>
<template>
  <section :aria-busy="loading||saving"><div class="org-tools"><p class="org-note">{{t('按部门管理成员，企业权限与项目角色分别授权。')}}</p><div class="org-actions"><Button v-if="permits(context,'members.export')" type="button" variant="outline" :disabled="selectionBusy" @click="exportMembers">{{t('导出成员')}}</Button><Button v-if="permits(context,'members.import')" type="button" variant="outline" :disabled="selectionBusy" @click="importOpen=true;error=''">{{t('导入成员')}}</Button><Button v-if="canManage" type="button" :disabled="selectionBusy" @click="open()">＋ {{t('添加成员')}}</Button></div></div>
  <p v-if="notice" class="org-success" role="status">{{t(notice)}}</p><p v-if="error&&!opened&&!importOpen&&!impersonating&&!deleteTarget&&!bulkAction" class="org-error" role="alert">{{t(error)}} <Button type="button" variant="ghost" :disabled="saving||loading" @click="load">{{t('重试')}}</Button></p>
  <div class="org-filters"><input v-model="query" :disabled="selectionBusy" :placeholder="t('搜索姓名、邮箱或工号')" :aria-label="t('搜索姓名、邮箱或工号')"><select v-model="department" :disabled="selectionBusy" :aria-label="t('部门筛选')"><option value="">{{t('全部部门')}}</option><option v-for="item in departmentOptions" :key="item.id" :value="item.id">{{departmentPath(item,departments)}}</option></select><select v-model="status" :disabled="selectionBusy" :aria-label="t('账号状态')"><option value="">{{t('全部状态')}}</option><option value="enabled">{{t('已启用')}}</option><option value="disabled">{{t('已禁用')}}</option><option value="inactive">{{t('未激活 / 已停用')}}</option></select></div>
  <div v-if="canBulk" class="member-bulk-toolbar" :aria-label="t('成员批量操作')">
    <div class="org-actions member-selection-controls"><strong>{{t('已选 {count} 位成员',{count:selectedIds.length})}}</strong><Button type="button" size="sm" variant="outline" :disabled="selectionBusy||!paged.length" @click="togglePageSelection">{{t(allPageSelected?'取消本页选择':'全选当前页')}}</Button><Button type="button" size="sm" variant="outline" :disabled="selectionBusy||!filtered.length||filtered.length>200" @click="selectAllFiltered">{{t('全选全部筛选结果（{count} 人，跨页）',{count:filtered.length})}}</Button><Button v-if="selectedIds.length" type="button" size="sm" variant="ghost" :disabled="selectionBusy" @click="clearSelection()">{{t('取消选择')}}</Button></div>
    <div v-if="selectedIds.length" class="org-actions member-bulk-actions"><Button type="button" size="sm" variant="outline" :disabled="selectionBusy||!!bulkReason('activate')" :title="t(bulkReason('activate'))" @click="prepareBulk('activate')">{{t('批量激活')}}</Button><Button type="button" size="sm" variant="outline" :disabled="selectionBusy||!!bulkReason('deactivate')" :title="t(bulkReason('deactivate'))" @click="prepareBulk('deactivate')">{{t('停用账号')}}</Button><Button type="button" size="sm" variant="outline" :disabled="selectionBusy||!!bulkReason('wecom-config')" :title="t(bulkReason('wecom-config'))" @click="prepareBulk('wecom-config')">{{t('配置机器人')}}</Button><Button v-if="context.isTenantAdmin" type="button" size="sm" variant="destructive" :disabled="selectionBusy||!!bulkReason('delete')" :title="t(bulkReason('delete'))" @click="prepareBulk('delete')">{{t('批量删除')}}</Button></div>
  </div>
  <p v-if="selectionNotice" class="org-note member-selection-notice" role="status">{{t(selectionNotice)}}</p><p v-if="canBulk&&filtered.length>200" class="org-note member-selection-notice">{{t('每次最多选择 200 位成员，请缩小筛选范围或分批操作')}}</p><p v-if="selectedIds.length&&bulkReason('deactivate')" class="org-note member-selection-notice">{{t(bulkReason('deactivate'))}}</p>
  <p class="org-note member-sort-note">{{t('按主部门树顺序归类，同部门按姓名排序；无主部门时取所属部门树序首项，未分配部门置后。')}}</p>
  <div class="org-table-wrap" tabindex="0" :aria-label="t('企业成员')"><table class="org-table"><thead><tr><th v-if="canBulk" class="member-check-cell"><input type="checkbox" :checked="allPageSelected" :indeterminate="somePageSelected" :disabled="selectionBusy||!paged.length" :aria-label="t('选择当前页全部成员')" @change="togglePageSelection"></th><th>{{t('成员')}}</th><th>{{t('部门 / 工号')}}</th><th>{{t('企业角色')}}</th><th>{{t('项目角色')}}</th><th>{{t('状态')}}</th><th>{{t('操作')}}</th></tr></thead><tbody><tr v-if="loading"><td :colspan="canBulk?7:6" class="org-empty">{{t('正在加载…')}}</td></tr><tr v-else-if="!paged.length"><td :colspan="canBulk?7:6" class="org-empty">{{t('暂无符合条件的记录')}}</td></tr>
    <template v-for="group in pageGroups" v-else :key="group.id"><tr class="member-department-heading"><th :colspan="canBulk?7:6" scope="colgroup"><span>{{group.name||t('未分配部门')}}</span><small>{{t('本页 {count} 位成员',{count:group.members.length})}}</small></th></tr>
      <tr v-for="member in group.members" :key="member.id" :class="{'member-is-selected':selectedIds.includes(member.id)}" @dblclick="open(member)"><td v-if="canBulk" class="member-check-cell" @click.stop @dblclick.stop><input type="checkbox" :checked="selectedIds.includes(member.id)" :disabled="selectionBusy" :aria-label="t('选择成员 {name}',{name:member.name})" @change="toggleMemberSelection(member.id)"></td><td><div class="org-person"><span class="org-avatar">{{member.name.slice(0,1)}}</span><div><b>{{member.name}}</b><small>{{member.email}}</small></div></div></td><td>{{departmentNames(member.departmentIds)}}<small>{{member.employeeNo||'—'}}</small></td><td><span class="org-pill">{{t(member.tenantRole==='tenant_admin'?'企业管理员':'普通成员')}}</span></td><td><div v-for="membership in member.projectMemberships" :key="membership.projectId">{{projectName(membership.projectId)}}<small>{{(membership.roles??[membership.role]).map(role=>t(roleName(role))).join(' / ')}}</small></div><span v-if="!member.projectMemberships.length">—</span></td>
        <td><div class="member-status-cell"><button v-if="member.active&&canToggleStatus(member)" type="button" class="org-pill member-status-button" :class="{active:!member.operationDisabled,disabled:member.operationDisabled}" :disabled="selectionBusy" :title="t(member.operationDisabled?'点击启用业务操作':'点击禁用业务操作（仍可登录）')" @click.stop="toggleStatus(member)" @dblclick.stop><i></i>{{t(member.operationDisabled?'已禁用':'已启用')}} ⌄</button><template v-else><span class="org-pill" :class="{active:member.active&&!member.operationDisabled}"><i></i>{{t(!member.active?'未激活 / 已停用':member.operationDisabled?'已禁用':'已启用')}}</span><Button v-if="!member.active&&canToggleStatus(member)" type="button" size="sm" variant="outline" :disabled="selectionBusy" :aria-label="t('激活成员 {name}',{name:member.name})" @click.stop="activateMember(member)" @dblclick.stop>{{t('激活')}}</Button></template></div></td>
        <td><div class="org-actions"><Button v-if="canManage" type="button" size="sm" variant="ghost" :disabled="selectionBusy" @click="open(member)">{{t('编辑')}}</Button><Button v-if="canConfigureWebhook(member)" type="button" size="sm" variant="ghost" :disabled="selectionBusy" @click.stop="openWebhook(member)">{{t('机器人')}}</Button><Button v-if="session?.canImpersonate&&member.id!==session.user?.id" type="button" size="sm" variant="ghost" :disabled="selectionBusy||!canImpersonateMember(member)" :title="t(member.mustChangePassword?'受限代看（待首次改密账号）：仅查看，完成首次改密后可常规代访问':'按成员实际权限代访问')" @click="prepareImpersonation(member)">{{t(member.mustChangePassword?'受限代看':'代访问账号')}}</Button><Button v-if="canDeleteMember(member)" type="button" size="sm" variant="ghost" class="member-delete-button" :disabled="selectionBusy" @click.stop="prepareDelete(member)">{{t('删除成员')}}</Button></div></td>
      </tr>
    </template>
  </tbody></table></div>
  <div class="org-tools" style="margin-top:16px"><span class="org-note">{{t('共 {count} 位成员',{count:filtered.length})}}</span><div class="org-actions"><Button type="button" size="sm" variant="outline" :disabled="selectionBusy||page===1" @click="page--">{{t('上一页')}}</Button><span class="org-note">{{page}} / {{Math.max(1,Math.ceil(filtered.length/30))}}</span><Button type="button" size="sm" variant="outline" :disabled="selectionBusy||page*30>=filtered.length" @click="page++">{{t('下一页')}}</Button></div></div>
  <OrganizationMemberBulkDialog v-if="bulkAction" ref="bulkDialog" :action="bulkAction" :members="bulkTargets" :current-user-id="session?.user?.id||''" :busy="saving" :error="error" :uncertain="bulkUncertain" @close="closeBulk" @confirm="confirmBulk"/>
  <OrganizationModal v-if="opened" :title="t(editing?'编辑成员':'添加成员')" :busy="saving" wide @close="close"><form id="org-member-form" class="org-form member-form" :inert="saving" @submit.prevent="save"><div class="org-form-two"><label>{{t('姓名')}} *<input v-model="form.name" required maxlength="80"></label><label>{{t('登录邮箱')}} *<input v-model="form.email" required type="email" maxlength="254"></label><label>{{t('工号')}}<input :value="form.employeeNo" readonly :placeholder="t('保存后自动生成')"><small>{{t('工号由系统按现有编号规则生成。')}}</small></label><label>{{t(editing?'设置新密码（留空不修改）':'初始密码')}}<span class="member-password-input"><input v-model="form.initialPassword" :required="!editing" :type="showPassword?'text':'password'" autocomplete="new-password" minlength="6" maxlength="72"><button type="button" :aria-label="t(showPassword?'隐藏密码':'显示密码')" :aria-pressed="showPassword" @click="showPassword=!showPassword">{{t(showPassword?'隐藏':'显示')}}</button></span><small v-if="!editing">{{t('请为新成员设置独立临时密码，首次登录必须修改。')}}</small></label></div><div class="member-departments-field"><span class="member-field-label">{{t('所属部门')}}</span><div class="org-multi member-department-options"><label v-for="item in departmentOptions.filter(item=>item.status==='active'||form.departmentIds.includes(item.id))" :key="item.id" class="org-check"><input v-model="form.departmentIds" type="checkbox" :value="item.id"><span>{{departmentPath(item,departments)}}</span></label><small v-if="!departments.length">{{t('请先创建部门')}}</small></div></div><label v-if="form.departmentIds.length">{{t('主部门')}}<select v-model="form.primaryDepartmentId" required><option v-for="id in primaryDepartmentOptions" :key="id" :value="id">{{departmentNames([id])}}</option></select></label><div class="org-form-two member-account-controls"><label v-if="context.isTenantAdmin">{{t('企业角色')}}<select v-model="form.tenantRole"><option value="member">{{t('普通成员')}}</option><option value="tenant_admin">{{t('企业管理员')}}</option></select></label><label class="org-check member-account-status"><input v-model="form.active" type="checkbox" :disabled="editing?.id===session?.user?.id">{{t('激活此账号')}}</label></div><fieldset class="member-project-permissions"><legend>{{t('项目角色')}}</legend><div v-for="(membership,index) in form.projectMemberships" :key="index" class="org-project-role"><div class="member-project-space"><span>{{t('项目空间')}}</span><select v-model="membership.projectId" required :aria-label="t('项目空间')"><option value="">{{t('选择项目')}}</option><option v-for="project in context.projects" :key="project.id" :value="project.id" :disabled="form.projectMemberships.some((item,i)=>i!==index&&item.projectId===project.id)">{{project.name}}</option></select></div><div class="member-project-role-options" role="group" :aria-label="t('项目角色（可多选）')"><span class="member-project-role-label">{{t('项目角色（可多选）')}}</span><div class="member-project-role-grid"><label v-for="role in projectRoleOptions(membership)" :key="role.key" class="org-check"><input v-model="membership.roles" type="checkbox" :value="role.key"><span>{{t(role.name)}}</span></label></div></div><button type="button" :aria-label="t('移除项目权限')" @click="form.projectMemberships.splice(index,1)">×</button></div><Button type="button" size="sm" variant="outline" @click="form.projectMemberships.push({projectId:'',role:'viewer',roles:['viewer']})">＋ {{t('添加项目权限')}}</Button><p class="org-note">{{t('不选择项目时，普通成员无法访问项目内的需求与缺陷。')}}</p></fieldset><p v-if="error" class="org-error" role="alert">{{t(error)}}</p></form><template #footer>
    <div v-if="discardMemberOpen" ref="discardMemberPrompt" class="member-discard-prompt" tabindex="-1" role="alert">
      <p>{{t('放弃尚未保存的设置修改？')}}</p><div class="org-actions"><Button type="button" variant="outline" :disabled="saving" @click="keepMemberEditing">{{t('继续编辑')}}</Button><Button type="button" variant="destructive" :disabled="saving" @click="discardMemberChanges">{{t('放弃修改')}}</Button></div>
    </div>
    <template v-else><Button type="button" variant="outline" :disabled="saving" @click="close">{{t('取消')}}</Button><Button form="org-member-form" type="submit" :disabled="saving||!canManage">{{t(saving?'保存中…':'保存成员')}}</Button></template>
  </template></OrganizationModal>
  <OrganizationModal v-if="importOpen" :title="t('导入成员')" :busy="saving||previewing" wide @close="closeImport"><div class="org-form"><p class="org-note">{{t('支持 UTF-8 CSV，最多 500 行、1 MB。导入仅创建未激活成员，不覆盖已有账号。')}}</p><div class="org-actions"><Button size="sm" variant="outline" @click="template">{{t('下载 CSV 模板')}}</Button><input type="file" accept=".csv,text/csv" :aria-label="t('选择 CSV 文件')" :disabled="saving||previewing" @change="chooseFile"></div><p class="org-note">{{t('departmentCode 填部门代码，projectCode 填项目代号；projectRole 填角色标识，多角色用 | 分隔。可在编辑页查看。')}}</p><p v-if="fileReading" class="org-note" role="status">{{t('正在加载…')}}</p><textarea v-model="csv" :disabled="saving||previewing||fileReading" :aria-label="t('CSV 内容')" :placeholder="t('也可以粘贴 CSV 内容')" rows="7"></textarea><Button variant="outline" :disabled="saving||previewing||fileReading||!csv.trim()" @click="previewImport">{{t(previewing?'正在校验…':'预览并校验')}}</Button><div v-if="preview"><p class="org-note">{{t('本次预览 {count} 位成员',{count:preview.rows?.length||0})}}</p><ul v-if="preview.errors?.length" class="org-error"><li v-for="(item,i) in preview.errors" :key="i">{{t('第 {row} 行',{row:item.row})}} · {{item.field}} · {{t(item.message)}}</li></ul><div class="org-table-wrap"><table class="org-table"><thead><tr><th>{{t('姓名')}}</th><th>{{t('邮箱')}}</th><th>{{t('部门代码')}}</th><th>{{t('项目角色')}}</th></tr></thead><tbody><tr v-for="(row,i) in preview.rows" :key="i"><td>{{row.name}}</td><td>{{row.email}}</td><td>{{row.departmentCode}}</td><td>{{row.projectCode}} / {{row.projectRole}}</td></tr></tbody></table></div><label v-if="preview.canCommit" class="org-check"><input v-model="importConfirmed" type="checkbox">{{t('我已核对预览，将创建未激活成员')}}</label></div><p v-if="error" class="org-error" role="alert">{{t(error)}}</p></div><template #footer><Button variant="outline" :disabled="saving||previewing" @click="closeImport">{{t('取消')}}</Button><Button :disabled="saving||previewing||fileReading||!preview?.canCommit||!importConfirmed" @click="commitImport">{{t(saving?'导入中…':'确认导入')}}</Button></template></OrganizationModal>
  <OrganizationModal v-if="impersonating" presentation="confirmation" :title="t(impersonating.mustChangePassword?'受限代看（待首次改密账号）':'代访问账号')" :busy="saving" @close="closeImpersonation"><form id="org-impersonate-form" class="org-form" @submit.prevent="startImpersonation"><p>{{impersonating.name}} · {{impersonating.email}}</p><p class="org-note">{{t('按成员实际权限查看页面，操作全程记录管理员与成员的双身份审计。')}}</p><p v-if="impersonating.mustChangePassword" class="org-readonly-note" role="status">{{t('受限代看（待首次改密账号）：只能查看工作与通知，不能修改业务、通知已读状态、显示偏好、密码或权限。成员完成首次改密后才能使用常规代访问。')}}</p><label>{{t('访问原因')}}<textarea v-model="reason" required minlength="4" maxlength="500"></textarea></label><p v-if="error" class="org-error" role="alert">{{t(error)}}</p></form><template #footer><Button variant="outline" :disabled="saving" @click="closeImpersonation">{{t('取消')}}</Button><Button form="org-impersonate-form" type="submit" :disabled="saving||!validImpersonationReason(reason)">{{t(impersonating.mustChangePassword?'确认受限代看':'确认代访问')}}</Button></template></OrganizationModal>
  <OrganizationModal v-if="deleteTarget" presentation="confirmation" :title="t('删除成员')" :busy="saving" @close="closeDelete"><div class="org-form member-delete-confirm"><p>{{t('确定删除成员「{name}」？',{name:deleteTarget.name})}}</p><p class="org-note">{{t('将立即撤销其组织、部门、项目和权限组访问，并终止登录及代访问会话；需求、缺陷、评论和审计历史会被保留。')}}</p><label class="org-check"><input v-model="deleteConfirmed" type="checkbox" :disabled="saving">{{t('我已确认这是不可恢复的成员移除操作')}}</label><p v-if="error" class="org-error" role="alert">{{t(error)}}</p></div><template #footer><Button variant="outline" :disabled="saving" @click="closeDelete">{{t('取消')}}</Button><Button variant="destructive" :disabled="saving||!deleteConfirmed||!canDeleteMember(deleteTarget)" @click="removeMember">{{t(saving?'删除中…':'确认删除成员')}}</Button></template></OrganizationModal>
  <OrganizationModal v-if="webhookMember" :title="t('配置 {name} 的企业微信机器人',{name:webhookMember.name})" :busy="webhookBusy" wide @close="closeWebhook"><UserWecomWebhook :key="webhookMember.id" ref="webhookEditor" :user-id="webhookMember.id" @busy="webhookBusy=$event"/><template #footer><Button variant="outline" :disabled="webhookBusy" @click="closeWebhook">{{t('关闭')}}</Button></template></OrganizationModal>
  </section>
</template>
<style scoped>
.member-form{gap:16px}.member-field-label,.member-project-space>span,.member-project-role-label{display:block;color:var(--muted);font-size:12px;font-weight:550;line-height:1.4}.member-departments-field{display:grid;gap:7px;min-width:0}.member-department-options{display:grid;grid-template-columns:repeat(auto-fit,minmax(190px,1fr));gap:2px 14px;max-height:196px;padding:8px 12px}.member-department-options .org-check{min-width:0;margin:0;padding:7px 3px}.member-department-options .org-check span{min-width:0;overflow-wrap:anywhere}.member-password-input{display:flex;gap:8px;align-items:center;min-width:0}.member-password-input input{min-width:0;flex:1}.member-password-input button{flex:none;padding:8px 10px;min-height:36px;border:1px solid var(--border);border-radius:6px;background:var(--surface);color:var(--ink)}.member-account-controls{align-items:end}.member-account-status{align-self:end;min-height:38px;margin:0;padding:0 11px!important;border:1px solid var(--border);border-radius:7px;background:var(--surface-soft)}.member-project-permissions{display:grid;gap:12px}.org-project-role{grid-template-columns:minmax(210px,.85fr) minmax(0,1.65fr) 32px;gap:14px;align-items:start;margin:0;padding:4px 0 14px;border-bottom:1px solid var(--border)}.org-project-role+.org-project-role{padding-top:14px}.member-project-space{display:grid;gap:7px;min-width:0}.member-project-space select{width:100%}.member-project-role-options{display:grid;gap:7px;min-width:0}.member-project-role-grid{display:grid;grid-template-columns:repeat(auto-fit,minmax(112px,1fr));gap:4px 10px;min-width:0}.member-project-role-options .org-check{display:flex;gap:6px;align-items:center;min-width:0;margin:0;padding:6px 3px;font-size:12px;line-height:1.35}.member-project-role-options .org-check span{min-width:0;overflow-wrap:anywhere}.member-project-role-options input{width:16px;height:16px;flex:none}.org-project-role>button{display:grid;place-items:center;width:32px;min-height:32px;margin-top:21px;border:1px solid transparent;border-radius:6px}.org-project-role>button:hover{border-color:var(--border);background:var(--surface-soft)}
@media(max-width:720px){.member-department-options{grid-template-columns:minmax(0,1fr);max-height:220px}.org-project-role{grid-template-columns:minmax(0,1fr) 36px;gap:10px;padding-bottom:12px}.member-project-role-options{grid-column:1 / -1}.member-project-role-grid{grid-template-columns:repeat(auto-fit,minmax(132px,1fr))}.org-project-role>button{grid-column:2;grid-row:1;margin-top:20px;min-width:36px;min-height:36px}}
.member-bulk-toolbar{display:flex;align-items:center;justify-content:space-between;gap:8px 16px;flex-wrap:wrap;padding:10px 0;border-bottom:1px solid var(--border)}.member-selection-controls,.member-bulk-actions{display:flex;align-items:center;gap:6px;flex-wrap:wrap}.member-selection-controls strong{font-size:var(--ui-font-caption);font-weight:600;white-space:nowrap}.member-selection-notice{margin:8px 0;font-size:var(--ui-font-caption);line-height:1.7}.member-check-cell{width:36px;min-width:36px;text-align:center!important;padding-left:8px!important;padding-right:8px!important}.member-check-cell input{width:16px;height:16px;accent-color:var(--primary);cursor:pointer}.member-check-cell input:disabled{cursor:not-allowed}.member-status-cell{display:flex;align-items:center;gap:6px;white-space:nowrap}.member-is-selected{background:var(--accent)}@media(max-width:820px){.member-bulk-toolbar{align-items:flex-start}.member-selection-controls,.member-bulk-actions{gap:6px}.member-check-cell input{width:18px;height:18px}}
.member-discard-prompt{display:flex;align-items:center;justify-content:space-between;gap:12px;width:100%;min-width:0}.member-discard-prompt p{margin:0;color:var(--ink);font-size:var(--ui-font-body);line-height:1.7}.member-discard-prompt:focus-visible{outline:2px solid var(--ring);outline-offset:4px}.member-discard-prompt .org-actions{flex:none}@media(max-width:640px){.member-discard-prompt{align-items:flex-start;flex-direction:column}.member-discard-prompt .org-actions{width:100%;justify-content:flex-end}}
.member-sort-note{margin:12px 0;font-size:11px;line-height:1.7}.member-department-heading th{background:var(--surface-subtle,#f4f7fb);color:var(--ink,#26334a);padding:10px 14px;font-size:12px;text-align:left;border-top:1px solid var(--line,#d9e0ea)}.member-department-heading th>span{overflow-wrap:anywhere}.member-department-heading th>small{display:inline-block;margin-left:12px;font-size:10px;font-weight:400;color:var(--muted,#758298)}@media(max-width:700px){.member-department-heading th>small{display:block;margin:4px 0 0}.member-sort-note{font-size:12px}}
.member-status-button{display:inline-flex;align-items:center;gap:6px;cursor:pointer;white-space:nowrap;min-height:var(--ui-control-height);padding:4px var(--ui-control-padding);border:1px solid var(--success-border,#9be7c6);border-radius:var(--ui-control-radius);background:var(--success-background,#ecfdf5);color:var(--success,#15803d);font-size:var(--ui-font-body);font-weight:550;line-height:1.25;transition:background .16s ease,border-color .16s ease,color .16s ease}.member-status-button i{width:6px;height:6px;flex:none}.member-status-button:hover:not(:disabled){background:color-mix(in srgb,currentColor 10%,var(--background))}.member-status-button:focus-visible{outline:2px solid var(--ring);outline-offset:2px}.member-status-button.disabled{background:var(--danger-background,#fff1ee);color:var(--danger,#c2413b);border-color:var(--danger-border,#fecaca)}.member-status-button:disabled{cursor:wait;opacity:.6}
#org-impersonate-form{gap:12px}#org-impersonate-form>p:first-child{display:flex;align-items:center;min-height:46px;margin:0;padding:0 12px;border:1px solid var(--border,#d9e0ea);border-radius:10px;background:var(--secondary,#f4f7fb);color:var(--foreground,#26334a);font-weight:650;overflow-wrap:anywhere}#org-impersonate-form>p:nth-child(2){margin:0;font-size:12px;line-height:1.7}#org-impersonate-form .org-readonly-note{margin:0;padding:10px 12px;border:1px solid #f2d28b;border-radius:8px;background:#fff8e8;color:#80580f;font-size:12px;line-height:1.7}#org-impersonate-form textarea{min-height:78px;line-height:1.65;resize:vertical}
.member-delete-button{color:var(--danger,#c2413b)}.member-delete-button:hover{color:var(--danger,#c2413b);background:color-mix(in srgb,var(--danger,#c2413b) 10%,transparent)}.member-delete-confirm>p:first-child{margin:0;font-weight:650;color:var(--ink)}
</style>

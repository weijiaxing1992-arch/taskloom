<script setup lang="ts">
import CodeTextEditor from './CodeTextEditor.vue'
import RequirementRefinement from './RequirementRefinement.vue'
import { computed, onBeforeUnmount, onMounted, reactive, ref, Teleport, useId, watch } from 'vue'
import { onBeforeRouteLeave, onBeforeRouteUpdate } from 'vue-router'
import { t } from '../i18n'
import { defaultVerifier, defectPersonValue, qaMembers, type DefectMember } from '../defectPeople'
import ResizableDrawer from './ResizableDrawer.vue'
import CustomFieldInputs from './CustomFieldInputs.vue'
import MemberMultiSelect from './MemberMultiSelect.vue'
import RequirementPicker, { type RequirementChoice } from './RequirementPicker.vue'
import WorkItemDrafts from './WorkItemDrafts.vue'
import { useSettingsDialog, useSettingsScope } from './settingsScope'
const props=defineProps<{defectId?:number;requirementId?:number;requirementTitle?:string;initialSprint?:string}>()
const emit=defineEmits<{(event:'created',defect:any):void;(event:'saved',defect:any):void;(event:'cancel'):void}>()
const scope=useSettingsScope(),loading=ref(true),saving=ref(false),error=ref(''),notice=ref(''),canWrite=ref(false),initialized=ref(false),baseline=ref(''),open=ref(false),width=ref(1040),uid=useId()
const form=reactive<any>({title:'',description:'',steps:'',actual:'',expected:'',environment:'',foundVersion:'',fixVersion:'',severity:'一般',priority:'P2',status:'新建',assignee:'',assigneeUserId:'',verifier:'',verifierUserId:'',sprint:'待规划',requirementId:props.requirementId??null,tags:'',customFields:{}})
const members=ref<DefectMember[]>([]),sprints=ref<{id:number;name:string;status:string}[]>([]),original=ref<any>(null),selectedRequirement=ref<RequirementChoice|null>(null)
const draftRecovery=ref<InstanceType<typeof WorkItemDrafts>|null>(null)
const aiSource=computed(()=>['标题：'+form.title,'描述：'+form.description,'复现步骤：'+form.steps,'实际结果：'+form.actual,'环境：'+form.environment].join('\n\n'))
function applyAI(value:{description:string;acceptance:string}){if(saving.value||!canWrite.value||!current())return;if(value.description)form.description=[form.description,value.description].filter(Boolean).join('\n\n');if(value.acceptance)form.expected=[form.expected,value.acceptance].filter(Boolean).join('\n\n')}
const activeMembers=computed(()=>members.value.filter(member=>member.active===true)),availableSprints=computed(()=>sprints.value.filter(sprint=>['规划中','进行中'].includes(sprint.status)))
const dirty=computed(()=>initialized.value&&JSON.stringify(form)!==baseline.value),formElement=ref<HTMLFormElement|null>(null)
const initialRequirement=props.requirementId,initialDefect=props.defectId,editing=initialDefect!==undefined
// 迭代页会把底层工作区设为 inert。创建缺陷抽屉必须脱离该工作区，
// 才能始终覆盖应用顶栏并保持可操作。非浏览器渲染器保留原树，方便静态/宿主测试。
const canTeleport=typeof document!=='undefined'&&!!document.body
const OverlayHost=canTeleport?Teleport:'div'
const snapshot=computed<any>(()=>baseline.value?JSON.parse(baseline.value):{})
let disposed=false,version=0
const current=()=>!disposed&&scope.current()&&props.requirementId===initialRequirement&&props.defectId===initialDefect
const id=(name:string)=>'defect-composer-'+uid+'-'+name
const verifierMembers=computed(()=>{const qa=qaMembers(activeMembers.value);return [...qa,...members.value.filter(member=>!qa.some(person=>person.id===member.id))]})
function setPerson(role:'assignee'|'verifier',userId:string){if(saving.value||loading.value||!canWrite.value||!current()||(userId&&!activeMembers.value.some(member=>member.id===userId))||(userId&&userId===form[role+'UserId']))return;form[role+'UserId']=userId;form[role]=activeMembers.value.find(member=>member.id===userId)?.name||''}
function personChanged(role:'assignee'|'verifier'){return !editing||form[role+'UserId']!==snapshot.value[role+'UserId']||form[role]!==snapshot.value[role]}
async function load(){
 if(!current())return
 const sequence=++version;loading.value=true;error.value=''
 try{
  if(initialRequirement!==undefined&&(!Number.isSafeInteger(initialRequirement)||initialRequirement<1))throw Error('关联需求无效，请关闭后重新打开')
  if(editing&&(!Number.isSafeInteger(initialDefect)||initialDefect!<1))throw Error('缺陷编号无效，请关闭后重新打开')
  const [session,people,iterations,detail]=await Promise.all([scope.request<any>('/session'),scope.request<any>('/members'),scope.request<any>('/sprints'),editing?scope.request<any>('/defects/'+initialDefect):Promise.resolve(null)])
  if(!current()||sequence!==version)return
  if(!Array.isArray(people.items)||!Array.isArray(iterations.items))throw Error('缺陷创建选项加载失败，请重试')
  if(editing&&(!detail||detail.id!==initialDefect||typeof detail.title!=='string'))throw Error('缺陷详情数据不完整，请重试')
  canWrite.value=!!session.user?.role&&session.user.role!=='viewer';members.value=people.items;sprints.value=iterations.items
  if(!initialized.value){
   if(editing){original.value=detail;for(const key of Object.keys(form))form[key]=key==='customFields'?JSON.parse(JSON.stringify(detail.customFields||{})):key==='requirementId'?detail.requirementId??null:detail[key]??form[key]}
   else{Object.assign(form,defectPersonValue(activeMembers.value,defaultVerifier(activeMembers.value,session.user?.id)?.id||''));const sprint=availableSprints.value.find(item=>item.name===props.initialSprint);form.sprint=sprint?.name||'待规划';if(props.initialSprint&&props.initialSprint!=='待规划'&&!sprint)notice.value='关联需求迭代已结束或不可用，新缺陷暂放待规划池'}
   baseline.value=JSON.stringify(form);initialized.value=true
  }
 }catch(cause){if(current()&&sequence===version)error.value=cause instanceof Error?cause.message:'缺陷创建选项加载失败，请重试'}finally{if(current()&&sequence===version)loading.value=false}
}
// 本地/私有草稿会保留编辑内容；路由守卫仍提示，因为“未正式保存”不会产生或更新缺陷。
function confirmLeave(){return !saving.value&&(!dirty.value||window.confirm(t('缺陷内容尚未正式保存，自动草稿可稍后恢复，确定离开吗？')))}
function restoreDraft(payload:Record<string,unknown>){
 if(!initialized.value||saving.value||loading.value||!current())return
 // 只恢复该表单本来支持编辑的字段，固定关联需求不能由草稿跨上下文改写。
 for(const key of ['title','description','steps','actual','expected','environment','foundVersion','fixVersion','severity','priority','assignee','assigneeUserId','verifier','verifierUserId','sprint','tags'] as const){
  if(Object.prototype.hasOwnProperty.call(payload,key)&&typeof payload[key]==='string')form[key]=payload[key]
 }
 if(initialRequirement===undefined&&Object.prototype.hasOwnProperty.call(payload,'requirementId')){
  const value=payload.requirementId
  if(value===null||typeof value==='number'&&Number.isSafeInteger(value)&&value>0)form.requirementId=value
 }
 if(Object.prototype.hasOwnProperty.call(payload,'customFields')&&payload.customFields&&typeof payload.customFields==='object'&&!Array.isArray(payload.customFields))form.customFields=JSON.parse(JSON.stringify(payload.customFields))
 selectedRequirement.value=null;error.value='';notice.value='已恢复草稿，请确认内容后正式保存'
}
async function requestClose(){if(saving.value||disposed||!confirmLeave())return false;open.value=false;emit('cancel');return true}
const dialog=useSettingsDialog(open,()=>{void requestClose()})
async function save(){
 if(saving.value||loading.value||!initialized.value||!current())return
 error.value=''
 if(!canWrite.value){error.value=editing?'当前账号为只读身份，不能编辑缺陷':'当前账号为只读身份，不能创建缺陷';return}
 if(!form.title.trim()||[...form.title.trim()].length>200){error.value='缺陷标题须为 1–200 字';return}
 if(!formElement.value?.reportValidity())return
 const assignee=activeMembers.value.find(member=>member.id===form.assigneeUserId),verifier=activeMembers.value.find(member=>member.id===form.verifierUserId)
 if(personChanged('assignee')&&form.assigneeUserId&&!assignee||personChanged('verifier')&&form.verifierUserId&&!verifier){error.value='所选处理人或验证人已不可用，请重新选择';return}
 if((!editing||form.sprint!==snapshot.value.sprint)&&form.sprint!=='待规划'&&!availableSprints.value.some(sprint=>sprint.name===form.sprint)){error.value='请选择待规划或可用的完整迭代名称';return}
 if(form.requirementId!==null&&(!Number.isSafeInteger(form.requirementId)||form.requirementId<1)){error.value='关联需求无效，请关闭后重新打开';return}
 const sequence=version,requirementId=initialRequirement??form.requirementId,payload:Record<string,any>={}
 if(editing){for(const key of ['title','description','steps','actual','expected','environment','foundVersion','fixVersion','severity','priority','sprint','requirementId','tags','customFields'])if(JSON.stringify(form[key])!==JSON.stringify(snapshot.value[key]))payload[key]=key==='title'?form.title.trim():JSON.parse(JSON.stringify(form[key]))}
 else Object.assign(payload,form,{title:form.title.trim(),requirementId})
 if(personChanged('assignee'))Object.assign(payload,{assignee:assignee?.name||'',assigneeUserId:assignee?.id||''})
 if(personChanged('verifier'))Object.assign(payload,defectPersonValue(activeMembers.value,form.verifierUserId))
 if(editing&&!Object.keys(payload).length)return
 saving.value=true
 try{
  if(initialRequirement===undefined&&requirementId!==null&&(!editing||requirementId!==snapshot.value.requirementId)&&selectedRequirement.value?.id!==requirementId){const requirement=await scope.request<RequirementChoice>('/requirements/'+requirementId);if(!current()||sequence!==version)return;if(requirement.id!==requirementId)throw Error('关联需求无效，请关闭后重新打开')}
  const saved=await scope.request<any>(editing?'/defects/'+initialDefect:'/defects',{method:editing?'PATCH':'POST',body:JSON.stringify(payload)});if(!current()||sequence!==version)return
  if(!saved||!Number.isSafeInteger(saved.id)||saved.id<1||editing&&saved.id!==initialDefect)throw Error('缺陷保存响应无效，请刷新后确认')
  baseline.value=JSON.stringify(form)
  // 正式接口成功后再清理对应草稿。抽屉切换期间草稿组件可能尚未暴露 complete，
  // 清理失败也只能保留草稿，绝不能把已成功创建的缺陷误报为失败或阻断 created 事件。
  try{await draftRecovery.value?.complete?.()}catch{if(current())notice.value='缺陷已创建，但草稿清理失败，可稍后在草稿箱手动删除'}
  if(editing)emit('saved',saved);else emit('created',saved)
 }catch(cause){if(current()&&sequence===version)error.value=cause instanceof Error?cause.message:editing?'保存缺陷失败，请重试':'创建缺陷失败，请重试'}finally{if(current()&&sequence===version)saving.value=false}
}
onBeforeRouteLeave(confirmLeave);onBeforeRouteUpdate(confirmLeave)
const beforeUnload=(event:BeforeUnloadEvent)=>{if(dirty.value||saving.value){event.preventDefault();event.returnValue=''}}
watch(()=>[props.requirementId,props.defectId],()=>{version++;loading.value=false;saving.value=false;error.value='编辑对象已变化，请关闭后重新打开'},{flush:'sync'})
watch(scope.locked,value=>{if(value){version++;loading.value=false;saving.value=false;error.value='项目或账号已变化，请刷新页面后继续'}},{flush:'sync'})
onMounted(()=>{open.value=true;void load();window.addEventListener('beforeunload',beforeUnload)})
onBeforeUnmount(()=>{disposed=true;version++;window.removeEventListener('beforeunload',beforeUnload)})
defineExpose({requestClose,dirty,saving})
</script>
<template>
 <component :is="OverlayHost" v-bind="canTeleport?{to:'body'}:{}">
  <div class="drawer-shade defect-composer-shade" @click.self="requestClose"><ResizableDrawer v-model:width="width" :storage-key="editing?'defects.edit.width':'defects.create.width'" :initial-width="1040" :label="t(editing?'编辑缺陷':'创建缺陷')" class="defect-composer-drawer"><div ref="dialog" class="defect-composer" tabindex="-1"><header><div><span class="eyebrow">{{t('质量协作')}}</span><h2>{{t(editing?'编辑缺陷':'创建缺陷')}}</h2><p v-if="editing&&original">{{original.code}} · {{t(original.status)}}<span v-if="original.sourceExecutionId"> · {{t('测试执行来源')}} #{{original.sourceExecutionId}}</span></p><p v-if="requirementId">{{t('自动关联当前需求')}} · {{requirementTitle||requirementId}}</p></div><button type="button" :disabled="saving" :aria-label="t(editing?'关闭缺陷编辑':'关闭创建缺陷')" @click="requestClose">×</button></header>
  <div v-if="loading" class="state" role="status">{{t('正在载入缺陷创建选项…')}}</div><div v-else-if="!initialized" class="state"><p role="alert">{{t(error)}}</p><button class="btn" type="button" @click="load">{{t('重新加载')}}</button></div>
  <form v-else ref="formElement" class="defect-composer-form" @submit.prevent="save"><div v-if="error" class="field-error" role="alert">{{t(error)}}</div><p v-if="notice" class="composer-notice">{{t(notice)}}</p><p v-if="!canWrite" role="alert">{{t(editing?'当前账号为只读身份，不能编辑缺陷':'当前账号为只读身份，不能创建缺陷')}}</p><WorkItemDrafts ref="draftRecovery" kind="defect" :target-id="editing?String(initialDefect):''" :context="{requirementId:initialRequirement??form.requirementId??null,initialSprint:props.initialSprint||''}" :payload="form" :ready="initialized&&canWrite&&!scope.locked.value" :dirty="dirty" :busy="saving||loading" @restore="restoreDraft($event)"/><fieldset :disabled="saving||!canWrite||scope.locked.value||requirementId!==initialRequirement||defectId!==initialDefect"><div class="composer-full"><label :for="id('title')">{{t('缺陷标题')}} *</label><input :id="id('title')" v-model="form.title" required maxlength="200" :placeholder="t('一句话描述缺陷现象')"/></div><div v-for="field in [{key:'description',label:'缺陷描述'},{key:'steps',label:'复现步骤'},{key:'actual',label:'实际结果'},{key:'expected',label:'预期结果'}]" :key="field.key" :class="{'composer-full':field.key==='description'||field.key==='steps'}"><label :for="id(field.key)">{{t(field.label)}}</label><CodeTextEditor :input-id="id(field.key)" v-model="form[field.key]" :rows="field.key==='description'?4:3"/></div>
   <div class="composer-full"><RequirementRefinement kind="defect" :description="aiSource" :acceptance="form.expected" :disabled="saving||!canWrite||scope.locked.value" @apply="applyAI"/></div>
   <div><label :for="id('severity')">{{t('严重程度')}}</label><select :id="id('severity')" v-model="form.severity"><option v-for="value in ['致命','严重','一般','轻微']" :key="value" :value="value">{{t(value)}}</option></select></div><div><label :for="id('priority')">{{t('优先级')}}</label><select :id="id('priority')" v-model="form.priority"><option v-for="value in ['P0','P1','P2','P3']" :key="value" :value="value">{{value}}</option></select></div>
   <div v-for="role in (['assignee','verifier'] as const)" :key="role"><label :for="id(role)">{{t(role==='assignee'?'处理人':'验证人')}}</label><MemberMultiSelect single :show-lead="false" :input-id="id(role)" :label="t(role==='assignee'?'处理人':'验证人')" :model-value="form[role+'UserId']?[form[role+'UserId']]:[]" :members="role==='verifier'?verifierMembers:members" :snapshots="form[role+'UserId']?[{id:form[role+'UserId'],name:form[role]}]:[]" :legacy-name="!form[role+'UserId']?form[role]:''" :disabled="saving||loading||!canWrite||scope.locked.value||requirementId!==initialRequirement||defectId!==initialDefect" @update:model-value="setPerson(role,$event[0]||'')"/><small v-if="role==='verifier'&&!editing">{{t('验证人默认选当前测试成员或唯一测试成员；多人时请手动选择。')}}</small></div>
   <div><label :for="id('sprint')">{{t('迭代')}}</label><select :id="id('sprint')" v-model="form.sprint"><option value="待规划">{{t('待规划')}}</option><option v-if="editing&&form.sprint!=='待规划'&&!availableSprints.some(sprint=>sprint.name===form.sprint)" :value="form.sprint">{{form.sprint||t('未设置')}} · {{t('历史迭代（保持不变）')}}</option><option v-for="sprint in availableSprints" :key="sprint.id" :value="sprint.name">{{sprint.name}}</option></select></div><div><template v-if="requirementId"><label :for="id('requirement')">{{t('关联需求')}}</label><input :id="id('requirement')" :value="requirementTitle||String(requirementId)" readonly/></template><RequirementPicker v-else v-model="form.requirementId" :input-id="id('requirement')" :selected-requirement="selectedRequirement" :disabled="saving||!canWrite||scope.locked.value||defectId!==initialDefect" @change="selectedRequirement=$event"/></div>
   <div class="composer-full"><label :for="id('environment')">{{t('测试环境')}}</label><input :id="id('environment')" v-model="form.environment"/></div><div v-for="field in [{key:'foundVersion',label:'发现版本'},{key:'fixVersion',label:'修复版本'},{key:'tags',label:'标签'}]" :key="field.key" :class="{'composer-full':field.key==='tags'}"><label :for="id(field.key)">{{t(field.label)}}</label><input :id="id(field.key)" v-model="form[field.key]"/></div><div class="composer-full composer-custom"><h3>{{t('自定义字段')}}</h3><CustomFieldInputs :disabled="saving||!canWrite||scope.locked.value||requirementId!==initialRequirement||defectId!==initialDefect" object-type="defect" v-model="form.customFields"/></div>
  </fieldset><footer><span>{{t(editing?'仅保存改动字段，保留当前状态、测试执行来源和未修改的历史关联。':'缺陷进入统一缺陷池，不会修改关联需求的正文或状态。')}}</span><button class="btn" type="button" :disabled="saving" @click="requestClose">{{t('取消')}}</button><button class="btn primary" type="submit" :disabled="saving||!canWrite||scope.locked.value||requirementId!==initialRequirement||defectId!==initialDefect||(editing&&!dirty)">{{saving?t(editing?'保存中…':'创建中…'):t(editing?'保存修改':'创建缺陷')}}</button></footer></form>
  </div></ResizableDrawer></div>
 </component>
</template>
<style scoped>
.defect-composer-shade{position:fixed;inset:0;z-index:1200;display:flex;align-items:stretch;justify-content:flex-end;min-height:100dvh;overflow:hidden;isolation:isolate;background:color-mix(in srgb,#0f172a 34%,transparent);-webkit-backdrop-filter:blur(2px);backdrop-filter:blur(2px)}.defect-composer-drawer{height:100dvh;max-height:100dvh;position:relative;z-index:1}.defect-composer{display:flex;flex-direction:column;min-height:0;height:100%;color:var(--ink);background:var(--surface);outline:0}.defect-composer>header{display:flex;justify-content:space-between;align-items:flex-start;gap:20px;padding:22px 28px;border-bottom:1px solid var(--line)}.defect-composer h2{margin:5px 0;font-size:22px}.defect-composer p,.defect-composer small{font-size:12px;line-height:1.7;color:var(--muted)}.defect-composer>header>button{border:0;background:none;color:var(--muted);font-size:24px;cursor:pointer}.defect-composer-form{overflow:auto;min-height:0;flex:1;padding:22px 28px}.defect-composer fieldset{display:grid;grid-template-columns:1fr 1fr;gap:18px 22px;border:0;padding:0;margin:0}.composer-full{grid-column:1/-1}.defect-composer label{display:block;font-size:12px;margin-bottom:7px;color:var(--muted)}.defect-composer input,.defect-composer select,.defect-composer textarea{width:100%;border:1px solid var(--line);border-radius:7px;padding:9px 11px;background:var(--surface);color:var(--ink);font:inherit;font-size:13px;box-sizing:border-box}.defect-composer textarea{resize:vertical;min-height:96px}.defect-composer input:focus,.defect-composer select:focus,.defect-composer textarea:focus{outline:2px solid #1677ff;outline-offset:1px}.defect-composer input[readonly]{background:var(--surface-soft);color:var(--muted)}.defect-composer footer{display:flex;gap:10px;align-items:center;position:sticky;bottom:-22px;background:var(--surface);padding:18px 0 0;margin-top:25px;border-top:1px solid var(--line)}.defect-composer footer>span{flex:1;font-size:11px;line-height:1.7;color:var(--muted)}.composer-custom h3{font-size:14px;margin:8px 0 16px}.composer-custom :deep(.custom-fields){display:grid;grid-template-columns:160px 1fr;align-items:center;gap:10px}.composer-notice{padding:10px 14px;border-radius:7px;background:var(--surface-soft)}@media(max-width:760px){.defect-composer fieldset{grid-template-columns:1fr}.defect-composer-form,.defect-composer>header{padding:16px}.defect-composer footer{bottom:-16px;flex-wrap:wrap}.defect-composer footer>span{flex-basis:100%}.composer-custom :deep(.custom-fields){grid-template-columns:1fr}}
.defect-composer>header>div,.defect-composer fieldset>div{min-width:0}.defect-composer>header>button{flex:none;min-width:36px;min-height:36px}.defect-composer>header p,.defect-composer small,.defect-composer footer>span{overflow-wrap:anywhere}.defect-composer fieldset{grid-template-columns:repeat(2,minmax(0,1fr))}.defect-composer footer>.btn{flex:none;white-space:normal}
@media(max-width:760px){.defect-composer fieldset{grid-template-columns:minmax(0,1fr)}.defect-composer input,.defect-composer select,.defect-composer textarea{font-size:16px}.defect-composer footer>.btn{flex:1;min-height:42px}.defect-composer footer>span{flex-basis:100%}}
</style>

<script setup lang="ts">
import { onMounted, onBeforeUnmount, ref, reactive, watch } from 'vue'
import { api } from '../api'
import { t } from '../i18n'
import RequirementWeights from './RequirementWeights.vue'
import MemberMultiSelect from './MemberMultiSelect.vue'
import { emptyRoleWeights, normalizeRoleWeights, type RequirementMember } from '../requirementFields'
import AppSelect from './AppSelect.vue'
import { importMarkdown } from '../markdownImport'
import { memberCandidates } from '../mentions'
import { roleWeightDefinitions } from '../requirementFields'
import { richTextPlain } from '../richText'
const props=defineProps<{contextKey:string;disabled?:boolean;members:RequirementMember[];currentUserId?:string}>()
const emit=defineEmits<{(event:'apply',value:any,replace:boolean):void}>()
const items=ref<any[]>([]),selected=ref(''),allowed=ref(false),busy=ref(false),error=ref(''),dialog=ref<HTMLDialogElement|null>(null)
const testerField=ref<any>(null)
const draft=reactive({id:'',name:'',description:'',acceptance:'',remarks:'',roleWeights:emptyRoleWeights(),ownerUserIds:[] as string[],testerUserIds:[] as string[],baseVersion:0})
let sequence=0,disposed=false,baseline=''
async function load(){const version=++sequence;error.value='';try{const [result,fields]=await Promise.all([api<any>('/requirement-templates'),api<any>('/field-definitions?objectType=requirement')]);if(disposed||version!==sequence)return;testerField.value=(fields.items||[]).find((x:any)=>x.key==='testers'&&x.enabled);allowed.value=result.canManage===true;items.value=result.items||[]}catch(cause:any){if(!disposed&&version===sequence)error.value=cause.message}}
async function detail(){return (await api<any>('/requirement-templates/'+selected.value)).template}
function fresh(){if(props.disabled||busy.value)return;Object.assign(draft,{id:crypto.randomUUID(),name:'',description:'## 背景与目标\n\n## 使用场景\n\n## 功能说明\n\n## 边界与异常\n',acceptance:'',remarks:'',roleWeights:emptyRoleWeights(),ownerUserIds:[] as string[],testerUserIds:[] as string[],baseVersion:0});baseline=JSON.stringify(draft);dialog.value?.showModal()}
async function edit(){if(!selected.value||busy.value||props.disabled)return;busy.value=true;error.value='';const version=sequence;try{const item=await detail();if(disposed||version!==sequence)return;Object.assign(draft,{...item,roleWeights:normalizeRoleWeights(item.roleWeights),ownerUserIds:item.ownerUserIds||[],testerUserIds:item.testerUserIds||[],baseVersion:item.version});baseline=JSON.stringify(draft);dialog.value?.showModal()}catch(cause:any){if(!disposed&&version===sequence)error.value=cause.message}finally{busy.value=false}}
function close(){if(busy.value)return;if(JSON.stringify(draft)!==baseline&&!window.confirm(t('模板尚未保存，确定关闭？')))return;dialog.value?.close()}
async function save(){if(busy.value||props.disabled)return;busy.value=true;error.value='';const version=sequence;try{await api('/requirement-templates/'+draft.id,{method:'PUT',body:JSON.stringify({name:draft.name,description:draft.description,acceptance:draft.acceptance,remarks:draft.remarks,roleWeights:draft.roleWeights,ownerUserIds:draft.ownerUserIds,testerUserIds:draft.testerUserIds,baseVersion:draft.baseVersion})});if(disposed||version!==sequence)return;selected.value=draft.id;dialog.value?.close();await load()}catch(cause:any){if(!disposed&&version===sequence)error.value=cause.message}finally{busy.value=false}}
async function remove(){if(!selected.value||busy.value||props.disabled||!window.confirm(t('删除此个人模板？已经填入需求的内容不受影响。')))return;const item=items.value.find(x=>x.id===selected.value),version=sequence;busy.value=true;error.value='';try{await api('/requirement-templates/'+selected.value,{method:'DELETE',body:JSON.stringify({baseVersion:item.version})});if(disposed||version!==sequence)return;selected.value='';await load()}catch(cause:any){if(!disposed&&version===sequence)error.value=cause.message}finally{busy.value=false}}
async function apply(replace=false){if(!selected.value||busy.value||props.disabled)return;if(replace&&!window.confirm(t('替换模板中已配置的文本、难度和人员？标题及迭代保持不变。')))return;busy.value=true;error.value='';const version=sequence;try{const item=await detail();if(disposed||version!==sequence)return;const missing:string[]=[]
 const keep=(ids:string[],roles:string[],department='')=>(ids||[]).filter(id=>{const ok=memberCandidates(props.members,roles,department).some(m=>m.id===id);if(!ok)missing.push(id);return ok})
 item.ownerUserIds=keep(item.ownerUserIds,['product'])
 item.testerUserIds=testerField.value?keep(item.testerUserIds,['qa'],testerField.value.departmentId):(missing.push(...(item.testerUserIds||[])),[])
 item.roleWeights=normalizeRoleWeights(item.roleWeights)
 for(const {key,roles} of roleWeightDefinitions){const weight=item.roleWeights[key];weight.userIds=keep(weight.userIds,roles);weight.userId=weight.userIds[0]||''}
 if(missing.length&&!window.confirm(t('模板中部分人员在当前项目不可用，将跳过这些人员，是否继续？')))return
 const parsed=importMarkdown(item.description);emit('apply',{...item,description:richTextPlain(parsed.document),descriptionDoc:parsed.document},replace);if(parsed.warnings.length)error.value=parsed.warnings.join('；')}catch(cause:any){if(!disposed&&version===sequence)error.value=cause.message}finally{busy.value=false}}
function reset(){sequence++;dialog.value?.close();items.value=[];selected.value='';allowed.value=false;Object.assign(draft,{id:'',name:'',description:'',acceptance:'',remarks:'',roleWeights:emptyRoleWeights(),ownerUserIds:[] as string[],testerUserIds:[] as string[],baseVersion:0});void load()}
watch(()=>props.contextKey,reset)
onMounted(load);onBeforeUnmount(()=>{disposed=true;sequence++;dialog.value?.close()})
</script>
<template>
 <section v-if="allowed||error" class="requirement-templates">
  <template v-if="allowed"><span>{{t('我的需求模板')}}</span><AppSelect :model-value="selected" :options="[{value:'',label:t('选择个人模板')},...items.map(x=>({value:x.id,label:x.name}))]" :label="t('选择个人模板')" :disabled="busy||disabled" @update:model-value="selected=String($event)"/><button type="button" class="btn compact" :disabled="!selected||busy||disabled" @click="apply(false)">{{t('填入空白')}}</button><button type="button" class="btn compact" :disabled="!selected||busy||disabled" @click="apply(true)">{{t('替换内容')}}</button><button type="button" class="btn compact" :disabled="busy||disabled" @click="fresh">{{t('新建模板')}}</button><button type="button" class="btn compact" :disabled="!selected||busy||disabled" @click="edit">{{t('编辑模板')}}</button><button type="button" class="btn compact" :disabled="!selected||busy||disabled" @click="remove">{{t('删除模板')}}</button></template>
  <p v-if="error" role="alert">{{t(error)}} <button type="button" class="link" :disabled="busy" @click="load">{{t('刷新')}}</button></p>
  <dialog ref="dialog" class="template-dialog" @cancel.prevent="close" @click.self="close">
   <div class="template-dialog-body"><header><h2>{{t(draft.baseVersion?'编辑模板':'新建模板')}}</h2><button type="button" class="btn compact" :disabled="busy" @click="close">{{t('关闭')}}</button></header><p>{{t('模板仅自己可见，可保存文本、难度和人员；填入后保存需求才会生效。')}}</p><label>{{t('模板名称')}}<input v-model="draft.name" maxlength="80" :disabled="busy" /></label><label>{{t('需求描述')}}<textarea v-model="draft.description" rows="10" :disabled="busy" /></label><div class="template-extra"><label>{{t('验收标准')}}<textarea v-model="draft.acceptance" rows="4" :disabled="busy" /></label><label>{{t('备注')}}<textarea v-model="draft.remarks" rows="4" :disabled="busy" /></label></div><div class="template-extra"><label>{{t('负责人')}}<MemberMultiSelect v-model="draft.ownerUserIds" :members="members" :member-roles="['product']" :disabled="busy" :current-user-id="currentUserId" :label="t('负责人')" /></label><label>{{t('测试负责人')}}<MemberMultiSelect v-model="draft.testerUserIds" :members="members" :member-roles="['qa']" :department-id="testerField?.departmentId" :disabled="busy||!testerField" :current-user-id="currentUserId" :label="t('测试负责人')" /></label></div><RequirementWeights v-model="draft.roleWeights" :members="members" :disabled="busy" :current-user-id="currentUserId" /><p v-if="error" role="alert">{{t(error)}}</p><footer><button type="button" class="btn" :disabled="busy" @click="close">{{t('取消')}}</button><button type="button" class="btn primary" :disabled="busy||!draft.name.trim()" @click="save">{{t('保存模板')}}</button></footer></div>
  </dialog>
 </section>
</template>
<style scoped>
.requirement-templates{display:flex;flex-wrap:wrap;align-items:center;gap:6px;margin:8px 0 12px;padding:8px 0;border-bottom:1px solid var(--line);font-size:12px}.requirement-templates :deep(.app-select){min-width:160px;max-width:260px}.requirement-templates>p{flex-basis:100%;color:var(--danger,#b42318)}.template-dialog{width:min(760px,calc(100vw - 28px));max-height:85dvh;border:1px solid var(--line);border-radius:10px;padding:0;color:var(--text);background:var(--panel,#fff)}.template-dialog::backdrop{background:#11182766}.template-dialog-body{padding:20px}.template-dialog header,.template-dialog footer{display:flex;align-items:center;justify-content:space-between;gap:12px}.template-dialog h2{font-size:18px;margin:0}.template-dialog p{font-size:12px;line-height:1.6;color:var(--muted)}.template-dialog label{display:grid;gap:6px;margin:12px 0;font-size:13px}.template-dialog textarea,.template-dialog input{box-sizing:border-box;width:100%;min-width:0;padding:8px;border:1px solid var(--line);border-radius:6px;background:var(--panel,#fff);color:inherit;font:inherit}.template-extra{display:grid;grid-template-columns:1fr 1fr;gap:12px}.template-dialog footer{justify-content:flex-end}@media(max-width:600px){.template-extra{grid-template-columns:1fr}.template-dialog-body{padding:14px}}
</style>

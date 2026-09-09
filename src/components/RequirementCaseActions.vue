<script setup lang="ts">
import { computed, onBeforeUnmount, ref } from 'vue'
import { useSettingsScope } from './settingsScope'
import { t } from '../i18n'
import type { TestCaseRecord, TestWorkspace } from '../testingWorkspace'
import TestCaseLibrary from './testing/TestCaseLibrary.vue'
import OrganizationModal from './OrganizationModal.vue'
import '../testing-workspace.css'

const props=defineProps<{requirementId:number;disabled?:boolean}>()
const emit=defineEmits<{saved:[]}>()
const {request:api}=useSettingsScope()
const workspace=ref<TestWorkspace|null>(null),members=ref<any[]>([]),executions=ref<any[]>([])
const editor=ref<InstanceType<typeof TestCaseLibrary>|null>(null),opened=ref(false),caseId=ref<number|null>(null),linking=ref(false)
const preparing=ref(false),error=ref(''),picker=ref(false),query=ref(''),page=ref(1),total=ref(0),items=ref<TestCaseRecord[]>([]),searching=ref(false)
let version=0,searchVersion=0
const pages=computed(()=>Math.max(1,Math.ceil(total.value/20)))
function message(cause:unknown){return cause instanceof Error?cause.message:'用例加载失败，请重试'}
function canLeave(){return !preparing.value&&editor.value?.canLeave()!==false}
async function open(id:number|null=null,link=false){
 if(preparing.value||(!id&&props.disabled)||(link&&props.disabled)||!canLeave())return
 const ticket=++version,requirement=props.requirementId
 preparing.value=true;error.value=''
 try{
  const [work,people,runs]=await Promise.all([api<TestWorkspace>('/testing/workspace'),api<{items:any[]}>('/members'),api<{items:any[]}>('/test-executions')])
  if(ticket!==version||requirement!==props.requirementId)return
  workspace.value={...work,canEdit:work.canEdit&&!props.disabled};members.value=people.items;executions.value=runs.items
  caseId.value=id;linking.value=link;opened.value=true;picker.value=false
 }catch(cause){if(ticket===version)error.value=message(cause)}finally{if(ticket===version)preparing.value=false}
}
async function search(){
 const ticket=++searchVersion;searching.value=true;error.value='';items.value=[]
 try{const data=await api<{items:TestCaseRecord[];total:number}>(`/test-cases?${new URLSearchParams({q:query.value.trim(),page:String(page.value),pageSize:'20'})}`);if(ticket!==searchVersion)return;items.value=data.items;total.value=data.total}
 catch(cause){if(ticket===searchVersion)error.value=message(cause)}finally{if(ticket===searchVersion)searching.value=false}
}
function associate(){if(props.disabled||preparing.value||!canLeave())return;picker.value=true;query.value='';page.value=1;void search()}
function closePicker(){if(preparing.value)return;picker.value=false;searchVersion++;searching.value=false;error.value=''}
function saved(){emit('saved')}
onBeforeUnmount(()=>{version++;searchVersion++})
defineExpose({open,associate,canLeave})
</script>
<template>
 <p v-if="preparing" class="case-action-feedback" role="status">{{t('正在加载用例编辑器…')}}</p>
 <p v-if="error&&!picker" class="case-action-feedback" role="alert">{{error}}</p>
 <TestCaseLibrary v-if="opened&&workspace" ref="editor" embedded :workspace="{...workspace,canEdit:workspace.canEdit&&!disabled}" :members="members" :executions="executions" :requirement-id="requirementId" :initial-case-id="caseId" :link-existing="linking" @refresh="saved" @close="opened=false"/>
 <OrganizationModal v-if="picker" :title="t('关联已有用例')" :busy="preparing" wide @close="closePicker">
  <form class="case-picker-search" @submit.prevent="page=1;search()"><input v-model="query" type="search" :aria-label="t('搜索已有用例')" :placeholder="t('搜索编号、标题或标签')"/><button type="submit" class="btn" :disabled="searching||preparing">{{t('搜索')}}</button></form>
  <p class="case-action-feedback">{{t('选择用例后可直接编辑，点击保存用例完成关联。已关联其他需求的用例不会被覆盖。')}}</p>
  <p v-if="error" role="alert">{{error}}</p>
  <div class="case-picker-list" :aria-busy="searching"><div v-for="item in items" :key="item.id" class="case-picker-row"><div><strong>{{item.code}} · {{item.title}}</strong><small>{{item.requirementId===requirementId?t('已关联当前需求'):item.requirementId?t('已关联其他需求'):t('未关联需求')}}</small></div><button class="btn" :disabled="disabled||preparing||!!item.requirementId" @click="open(item.id,true)">{{t('选择并编辑')}}</button></div><p v-if="!items.length">{{t(searching?'正在加载…':'没有符合条件的用例')}}</p></div>
  <template #footer><span>{{page}} / {{pages}}</span><button class="btn" :disabled="page<=1||searching||preparing" @click="page--;search()">{{t('上一页')}}</button><button class="btn" :disabled="page>=pages||searching||preparing" @click="page++;search()">{{t('下一页')}}</button><button class="btn" :disabled="preparing" @click="closePicker">{{t('取消')}}</button></template>
 </OrganizationModal>
</template>
<style scoped>
.case-action-feedback{font-size:12px;color:var(--muted);margin:10px 0}.case-picker-search{display:flex;gap:8px;min-width:0}.case-picker-search input{min-width:0;flex:1;height:32px;border:1px solid var(--line);border-radius:6px;padding:0 10px;box-shadow:none}.case-picker-list{max-height:48vh;overflow:auto}.case-picker-row{display:flex;align-items:center;gap:12px;padding:12px 0;border-bottom:1px solid var(--line)}.case-picker-row>div{flex:1;min-width:0}.case-picker-row strong{display:block;overflow-wrap:anywhere;font-size:13px}.case-picker-row small{display:block;color:var(--muted);margin-top:4px}.case-picker-row button{flex:none}
</style>

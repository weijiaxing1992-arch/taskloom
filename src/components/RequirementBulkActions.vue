<script setup lang="ts">
import { computed,ref,onBeforeUnmount } from 'vue'
import { t } from '../i18n'
import { useSettingsScope } from './settingsScope'
import OrganizationModal from './OrganizationModal.vue'
import MemberMultiSelect from './MemberMultiSelect.vue'
import { Button } from './ui/button'
const props=defineProps<{items:any[];members:any[];sprints:any[];categories:string[];statuses:any[];disabled?:boolean}>()
const emit=defineEmits<{changed:[];busy:[boolean]}>(),scope=useSettingsScope()
const opened=ref(false),busy=ref(false),action=ref('edit'),field=ref('priority'),value=ref('P2'),people=ref<string[]>([]),confirmed=ref(false),error=ref(''),progress=ref(0),results=ref<{id:number;title:string;ok:boolean;message:string}[]>([]),targets=ref<any[]>([]),allowed=ref<string[]>([]),checking=ref(false)
const choices=[['edit','批量编辑'],['status','批量状态流转'],['checklist','批量添加检查项'],['copy','批量复制'],['move','批量移动'],['links','批量复制链接'],['summary','批量复制标题与链接']]
let actionVersion=0,disposed=false
onBeforeUnmount(()=>{disposed=true;++actionVersion})
const editingFields=[['priority','优先级'],['assigneeUserIds','处理人'],['ownerUserIds','产品负责人'],['tags','标签'],['remarks','备注']]
const valid=computed(()=>confirmed.value&&!busy.value&&!checking.value&&!results.value.length&&(action.value==='status'?allowed.value.includes(value.value):action.value==='copy'||['links','summary'].includes(action.value)||field.value.endsWith('UserIds')&&action.value==='edit'||!!value.value.trim()))
function begin(){if(props.disabled||busy.value||checking.value||!props.items.length||!scope.current())return;++actionVersion;targets.value=[...props.items];opened.value=true;results.value=[];error.value='';confirmed.value=false;progress.value=0;action.value='edit';field.value='priority';value.value='P2';people.value=[];allowed.value=[]}
function close(){if(busy.value||checking.value)return;++actionVersion;opened.value=false}
async function changeAction(){if(busy.value)return;const version=++actionVersion;confirmed.value=false;results.value=[];error.value='';value.value=action.value==='move'?'待规划':action.value==='edit'?'P2':'';field.value=action.value==='move'?'sprint':'priority';allowed.value=[]
 if(action.value!=='status')return;checking.value=true
 try{let intersection:string[]|null=null;for(const item of [...targets.value]){const data=await scope.request<any>(`/requirements/${item.id}/transitions`);if(disposed||version!==actionVersion||!scope.current())return;const options=Array.isArray(data.allowedTransitions)?data.allowedTransitions.filter((key:unknown)=>typeof key==='string'):[];intersection=intersection===null?options:intersection.filter(key=>options.includes(key))};allowed.value=intersection||[];if(!allowed.value.length)error.value='所选需求没有共同可用的流转状态，请缩小选择范围'}catch(cause){if(!disposed&&version===actionVersion)error.value=cause instanceof Error?cause.message:'流转权限加载失败，请重试'}finally{if(version===actionVersion)checking.value=false}}
function changeField(){people.value=[];value.value=field.value==='priority'?'P2':field.value==='sprint'?'待规划':'';confirmed.value=false}
async function execute(){
 if(!valid.value||!scope.current()||props.disabled)return
 const chosenAction=action.value,chosenField=field.value,chosenValue=value.value,chosenPeople=[...people.value],chosenTargets=[...targets.value]
 busy.value=true;emit('busy',true);results.value=[];error.value='';progress.value=0
 try{
  if(['links','summary'].includes(chosenAction)){
   const lines=chosenTargets.map(item=>{const url=new URL('/requirements',location.origin);url.searchParams.set('req',String(item.id));url.searchParams.set('project',scope.project);return chosenAction==='links'?url.href:`${item.code||'#'+item.id} ${item.title}\n${url.href}`})
   if(!navigator.clipboard?.writeText)throw new Error(t('浏览器暂不支持剪贴板，请使用需求编号复制功能'))
   await navigator.clipboard.writeText(lines.join('\n'));results.value=targets.value.map(item=>({id:item.id,title:item.title,ok:true,message:'已复制'}));progress.value=targets.value.length;return
  }
  for(const item of chosenTargets){
   if(!scope.current())break
   try{
    if(chosenAction==='copy'){
     const source=await scope.request<any>(`/requirements/${item.id}`)
     // A copied requirement is a new planning item: do not copy identities,
     // audit history, comments or attachment references owned by its source.
     const body={title:source.title+'（副本）',description:source.description,acceptance:source.acceptance,category:source.category,priority:source.priority,tags:source.tags,tagColors:source.tagColors,remarks:source.remarks,roleWeights:source.roleWeights,assigneeUserIds:source.assigneeUserIds,ownerUserIds:source.ownerUserIds,customFields:source.customFields}
     await scope.request('/requirements',{method:'POST',body:JSON.stringify(body)})
    }else if(chosenAction==='checklist'){
     await scope.request(`/requirements/${item.id}/checklist`,{method:'POST',body:JSON.stringify({text:chosenValue.trim()})})
    }else{
     const body=chosenAction==='status'?{status:chosenValue}:{[chosenField]:chosenField.endsWith('UserIds')?chosenPeople:chosenValue}
     await scope.request(`/requirements/${item.id}`,{method:'PATCH',body:JSON.stringify(body)})
    }
    results.value.push({id:item.id,title:item.title,ok:true,message:'成功'})
   }catch(cause){results.value.push({id:item.id,title:item.title,ok:false,message:cause instanceof Error?cause.message:t('操作失败，请稍后重试')})}
   progress.value++
  }
  emit('changed')
 }catch(cause){error.value=cause instanceof Error?cause.message:'操作失败，请稍后重试'}finally{busy.value=false;emit('busy',false);confirmed.value=false}
}
</script>
<template><Button type="button" variant="outline" size="toolbar" :disabled="disabled||!items.length||scope.locked.value" @click="begin">{{t('批量操作')}} <small>{{items.length}}</small></Button><OrganizationModal v-if="opened" :title="t('批量处理 {count} 条需求',{count:targets.length})" :busy="busy||checking" wide @close="close"><div class="bulk-form"><label>{{t('操作类型')}}<select v-model="action" :disabled="busy||checking" @change="changeAction"><option v-for="choice in choices" :key="choice[0]" :value="choice[0]">{{t(choice[1]!)}}</option></select></label><template v-if="action==='edit'||action==='move'"><label>{{t('修改字段')}}<select v-model="field" :disabled="busy" @change="changeField"><template v-if="action==='edit'"><option v-for="choice in editingFields" :key="choice[0]" :value="choice[0]">{{t(choice[1]!)}}</option></template><template v-else><option value="sprint">{{t('迭代')}}</option><option value="category">{{t('分类')}}</option></template></select></label><MemberMultiSelect v-if="field.endsWith('UserIds')" v-model="people" :members="members" :member-roles="field==='ownerUserIds'?['product']:[]" :disabled="busy" :label="t('选择成员')"/><select v-else-if="field==='priority'" v-model="value" :disabled="busy" :aria-label="t('优先级')"><option v-for="priority in ['P0','P1','P2','P3']" :key="priority">{{priority}}</option></select><select v-else-if="field==='sprint'" v-model="value" :disabled="busy" :aria-label="t('迭代')"><option value="待规划">{{t('待规划')}}</option><option v-for="sprint in sprints.filter(s=>['规划中','进行中'].includes(s.status))" :key="sprint.id" :value="sprint.name">{{sprint.name}}</option></select><select v-else-if="field==='category'" v-model="value" :disabled="busy" :aria-label="t('分类')"><option value="">{{t('请选择')}}</option><option v-for="category in categories" :key="category">{{category}}</option></select><textarea v-else v-model="value" :disabled="busy" :aria-label="t('新字段值')" rows="3"></textarea></template><select v-else-if="action==='status'" v-model="value" :disabled="checking||busy" :aria-label="t('目标状态')"><option value="">{{t(checking?'正在检查流转权限…':'请选择')}}</option><option v-for="key in allowed" :key="key" :value="key">{{statuses.find(s=>s.key===key)?.name||key}}</option></select><label v-else-if="action==='checklist'">{{t('检查项内容')}}<textarea v-model="value" :disabled="busy" maxlength="2000" rows="3"></textarea></label><p v-if="action==='copy'" class="bulk-note">{{t('副本使用新的编号和初始状态，不复制子需求、评论、附件及历史记录。')}}</p><p class="bulk-note">{{t('只处理已选择的需求；字段值会替换原值。操作逐条执行，失败项单独列出，不自动重试。')}}</p><label class="bulk-confirm"><input v-model="confirmed" type="checkbox" :disabled="busy||checking">{{t('已核对操作与选中需求，确认执行')}}</label><p v-if="error" role="alert" class="bulk-error">{{t(error)}}</p><p v-if="busy" role="status">{{t('已处理 {done}/{total}',{done:progress,total:targets.length})}}</p><ul v-if="results.length" class="bulk-results"><li v-for="result in results" :key="result.id" :class="{failed:!result.ok}"><b>{{result.ok?'✓':'!'}} #{{result.id}}</b><span>{{result.title}}</span><small>{{t(result.message)}}</small></li></ul></div><template #footer><Button variant="outline" :disabled="busy||checking" @click="close">{{t('关闭')}}</Button><Button :disabled="!valid" @click="execute">{{t(busy?'正在处理…':'执行批量操作')}}</Button></template></OrganizationModal></template>
<style scoped>.bulk-form{display:grid;gap:16px}.bulk-form label{display:grid;gap:8px;font-size:13px}.bulk-form select,.bulk-form textarea{padding:10px;border:1px solid var(--line);border-radius:8px;background:var(--surface);color:var(--ink);max-width:100%}.bulk-form .bulk-confirm{display:flex;align-items:center;gap:10px}.bulk-note{margin:0;font-size:12px;line-height:1.7;color:var(--muted)}.bulk-error,.bulk-results .failed{color:var(--danger,#d05858)}.bulk-results{padding:0;margin:0;list-style:none;max-height:280px;overflow:auto}.bulk-results li{display:grid;grid-template-columns:70px 1fr;gap:6px;padding:12px 0;border-bottom:1px solid var(--line);font-size:12px}.bulk-results li small{grid-column:2;overflow-wrap:anywhere}</style>

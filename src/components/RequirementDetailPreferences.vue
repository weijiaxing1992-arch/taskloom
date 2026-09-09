<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { t } from '../i18n'
import FieldSelectionPanel from './FieldSelectionPanel.vue'
import { useSettingsDialog, useSettingsScope } from './settingsScope'
const props=defineProps<{modelValue:{basicFields:string[]|null;customFieldKeys:string[]|null};definitions:{key:string;name:string}[];canConfigure?:boolean}>()
const emit=defineEmits<{(event:'update:modelValue',value:{basicFields:string[]|null;customFieldKeys:string[]|null}):void}>()
const fields=[{key:'status',label:'状态'},{key:'category',label:'分类'},{key:'sprint',label:'迭代'},{key:'priority',label:'优先级'},{key:'owner',label:'产品负责人'},{key:'assignee',label:'处理人'},{key:'createdAt',label:'创建时间'},{key:'updatedAt',label:'更新时间'},{key:'weightTotal',label:'已保存总权重'}]
const scope=useSettingsScope(),opened=ref(false),loading=ref(false),saving=ref(false),error=ref(''),basic=ref<string[]>([]),custom=ref<string[]>([]),resetToDefault=ref(false)
const available=computed(()=>[...fields,...props.definitions.map(field=>({key:'cf.'+field.key,label:field.name,custom:true}))])
const selected=computed({get:()=>[...basic.value,...custom.value.map(key=>'cf.'+key)],set:(keys:string[])=>{resetToDefault.value=false;basic.value=keys.filter(key=>fields.some(field=>field.key===key));custom.value=keys.filter(key=>key.startsWith('cf.')).map(key=>key.slice(3))}})
let version=0,disposed=false
function valid(value:any){return !!value&&['basicFields','customFieldKeys'].every(key=>value[key]===null||Array.isArray(value[key])&&value[key].every((entry:any)=>typeof entry==='string'))}
async function load(){if(saving.value||disposed||!scope.current())return;const request=++version;loading.value=true;error.value='';try{const value=await scope.request<any>('/preferences/requirement-detail');if(request===version&&!disposed&&scope.current()){if(!valid(value))throw Error('显示配置格式不正确，请重试');emit('update:modelValue',value)}}catch(cause){if(request===version&&!disposed)error.value=cause instanceof Error?cause.message:t('请求失败')}finally{if(request===version&&!disposed)loading.value=false}}
function open(){if(loading.value||saving.value||error.value||!scope.current())return;basic.value=[...(props.modelValue.basicFields??fields.map(x=>x.key))];custom.value=[...(props.modelValue.customFieldKeys??props.definitions.map(x=>x.key))];resetToDefault.value=false;opened.value=true}
function close(){if(!saving.value)opened.value=false}
function reset(){if(saving.value)return;basic.value=fields.map(field=>field.key);custom.value=props.definitions.map(field=>field.key);resetToDefault.value=true}
async function save(reset=false){
 if(saving.value||loading.value||disposed||!scope.current())return
 const request=++version;saving.value=true;error.value=''
 const value=reset||resetToDefault.value?{basicFields:null,customFieldKeys:null}:{basicFields:basic.value.filter(key=>fields.some(x=>x.key===key)),customFieldKeys:custom.value.filter(key=>props.definitions.some(x=>x.key===key))}
 try{const result=await scope.request<any>('/preferences/requirement-detail',{method:'PATCH',body:JSON.stringify(value)});if(request===version&&!disposed&&scope.current()){if(!valid(result))throw Error('显示配置格式不正确，请重试');emit('update:modelValue',result);opened.value=false}}
 catch(cause){if(request===version&&!disposed)error.value=cause instanceof Error?cause.message:t('请求失败')}
 finally{if(request===version&&!disposed)saving.value=false}
}
const dialog=useSettingsDialog(opened,close)
watch(scope.locked,locked=>{if(locked){version++;opened.value=false;loading.value=false;saving.value=false;basic.value=[];custom.value=[]}},{flush:'sync'})
onMounted(load);onBeforeUnmount(()=>{disposed=true;version++})
</script>
<template>
 <div class="detail-preference-trigger"><button type="button" class="link" :disabled="loading||saving||scope.locked.value" @click="open">{{t('配置详情字段')}}</button><p v-if="error&&!opened" role="alert">{{t(error)}} <button type="button" class="link" :disabled="loading" @click="load">{{t('重试')}}</button></p></div>
 <div v-if="opened" class="detail-preference-shade" @click.self="close">
  <section ref="dialog" tabindex="-1" class="detail-preference-dialog" role="dialog" aria-modal="true" :aria-label="t('配置详情字段')">
   <header><div><h2>{{t('配置详情字段')}}</h2><p>{{t('仅调整当前账号、当前项目的显示，不删除字段及已填写数据。')}}</p></div><button type="button" :disabled="saving" :aria-label="t('关闭字段配置')" @click="close">×</button></header>
   <FieldSelectionPanel v-model="selected" :fields="available" :disabled="saving" :sortable="false"/>
   <p v-if="error" class="inline-notice" role="alert">{{t(error)}}</p>
   <footer><button type="button" class="link" :disabled="saving" @click="reset">{{t('恢复默认')}}</button><router-link v-if="canConfigure" to="/settings/fields">{{t('管理字段定义')}}</router-link><span></span><button type="button" class="btn" :disabled="saving" @click="close">{{t('取消')}}</button><button type="button" class="btn primary" :disabled="saving" @click="save()">{{t(saving?'保存中…':'保存显示设置')}}</button></footer>
  </section>
 </div>
</template>
<style scoped>
.detail-preference-trigger{font-size:12px;margin-bottom:16px}.detail-preference-trigger p{color:var(--danger);line-height:1.6}.detail-preference-shade{position:fixed;inset:0;background:#17223966;z-index:170;display:flex;align-items:center;justify-content:center;padding:20px}.detail-preference-dialog{background:var(--surface);color:var(--ink);border-radius:12px;width:min(860px,95vw);max-height:90dvh;display:flex;flex-direction:column;overflow:hidden;box-shadow:0 20px 80px #10182833}.detail-preference-dialog>header,.detail-preference-dialog>footer{display:flex;align-items:center;gap:12px;padding:20px;flex:none}.detail-preference-dialog header>div{flex:1;min-width:0}.detail-preference-dialog h2{margin:0;font-size:18px}.detail-preference-dialog p{font-size:12px;line-height:1.7;color:var(--muted)}.detail-preference-dialog header>button{background:none;border:0;font-size:22px;color:var(--muted);min-height:34px;min-width:34px}.detail-preference-dialog footer span{flex:1}.detail-preference-dialog footer>a{font-size:11px;color:var(--primary)}.detail-preference-dialog .inline-notice{margin:10px 20px}@media(max-width:700px){.detail-preference-shade{padding:12px}.detail-preference-dialog{width:100%;max-width:100%;max-height:calc(100dvh - 24px)}.detail-preference-dialog>header,.detail-preference-dialog>footer{padding:14px;flex-wrap:wrap}.detail-preference-dialog footer button{min-height:40px}}
</style>

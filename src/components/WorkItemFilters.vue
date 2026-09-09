<script setup lang="ts">
import { computed, reactive, ref, useId } from 'vue'
import { t } from '../i18n'
import DatePicker from './DatePicker.vue'
import MemberMultiSelect from './MemberMultiSelect.vue'
import { operatorLabels, workFilterValue, workOperators, type WorkField, type WorkFilter } from '../workItemQuery'
const props=defineProps<{fields:WorkField[];modelValue:WorkFilter[];disabled?:boolean}>()
const emit=defineEmits<{(event:'update:modelValue',value:WorkFilter[]):void}>()
const open=ref(false),draft=ref<WorkFilter[]>([]),error=ref(''),uid=useId()
const invalidDates=reactive(new Set<WorkFilter>())
const groups=computed(()=>[...new Set(props.fields.map(field=>field.group||'基础信息'))])
const label=(field:WorkField)=>field.custom?field.label:t(field.label)
const definition=(rule:WorkFilter)=>props.fields.find(field=>field.key===rule.field)
function begin(){invalidDates.clear();draft.value=props.modelValue.map(rule=>({...rule}));error.value='';open.value=true}
function add(){const field=props.fields[0];if(field&&draft.value.length<30)draft.value.push({field:field.key,operator:workOperators(field)[0]!,value:''})}
function changed(rule:WorkFilter){const field=definition(rule);if(field){rule.operator=workOperators(field)[0]!;rule.value=''}}
function apply(){try{if(draft.value.some(rule=>definition(rule)?.kind==='date'&&!['is_empty','not_empty'].includes(rule.operator)&&invalidDates.has(rule)))throw new Error('请先修正日期输入');const rules=draft.value.map(rule=>{const field=definition(rule);if(!field||!workOperators(field).includes(rule.operator))throw new Error('筛选字段已变更，请重新选择');return{field:rule.field,operator:rule.operator,value:workFilterValue(field,rule.operator,rule.value)}});emit('update:modelValue',rules);open.value=false;error.value=''}catch(cause){error.value=cause instanceof Error?cause.message:'筛选条件无效'}}
</script>
<template>
 <div class="work-filter-control"><button type="button" class="btn compact" :disabled="disabled" :aria-expanded="open" @click="open?open=false:begin()">{{t('高级筛选')}} <small v-if="modelValue.length">{{modelValue.length}}</small></button>
 <section v-if="open" class="work-filter-panel" :aria-label="t('全字段筛选')" @keydown.esc.stop="open=false">
  <header><div><b>{{t('全字段筛选')}}</b><p>{{t('所有条件同时满足；空值不等同于 0 或否。')}}</p></div><button type="button" :aria-label="t('关闭筛选')" @click="open=false">×</button></header>
  <div v-for="(rule,index) in draft" :key="index" class="work-filter-row">
   <select v-model="rule.field" :aria-label="t('筛选字段')" @change="changed(rule)"><optgroup v-for="group in groups" :key="group" :label="t(group)"><option v-for="field in fields.filter(field=>(field.group||'基础信息')===group)" :key="field.key" :value="field.key">{{label(field)}}</option></optgroup></select>
   <select v-model="rule.operator" :aria-label="t('筛选条件')"><option v-for="operator in definition(rule)?workOperators(definition(rule)!):[]" :key="operator" :value="operator">{{t(operatorLabels[operator]||operator)}}</option></select>
   <template v-if="!['is_empty','not_empty'].includes(rule.operator)"><select v-if="definition(rule)?.kind==='boolean'" v-model="rule.value" :aria-label="t('筛选值')"><option value="">{{t('请选择')}}</option><option :value="true">{{t('是')}}</option><option :value="false">{{t('否')}}</option></select><MemberMultiSelect v-else-if="definition(rule)?.kind==='person'" single :model-value="rule.value?[String(rule.value)]:[]" :members="(definition(rule)?.options||[]).map(option=>({id:option.value,name:option.label,active:true}))" :label="t('筛选值')" :show-lead="false" :disabled="disabled" @update:model-value="rule.value=$event[0]||''" /><select v-else-if="definition(rule)?.options?.length" v-model="rule.value" :aria-label="t('筛选值')"><option value="">{{t('请选择')}}</option><option v-for="option in definition(rule)?.options" :key="option.value" :value="option.value">{{definition(rule)?.systemOptions?t(option.label):option.label}}</option></select><DatePicker v-else-if="definition(rule)?.kind==='date'" :model-value="String(rule.value??'')" :id="uid+'-value-'+index" :label="t('筛选值')" :disabled="disabled" @update:model-value="rule.value=$event" @validity-change="$event?invalidDates.delete(rule):invalidDates.add(rule)" /><input v-else v-model="rule.value" :id="uid+'-value-'+index" :aria-label="t('筛选值')" :type="definition(rule)?.kind==='number'?'number':'text'" step="any" :placeholder="t('输入筛选值')"></template><span v-else class="no-filter-value">—</span>
   <button type="button" :aria-label="t('删除筛选条件')" @click="draft.splice(index,1)">×</button>
  </div>
  <p v-if="!draft.length" class="filter-empty">{{t('尚未添加条件，将展示所有工作项。')}}</p><p v-if="error" class="field-error" role="alert">{{t(error)}}</p>
  <footer><button type="button" class="link" :disabled="draft.length>=30" @click="add">{{t('添加条件')}}</button><span>{{t('最多 30 个条件')}}</span><button type="button" class="btn compact" @click="draft=[]">{{t('清空')}}</button><button type="button" class="btn primary compact" @click="apply">{{t('应用筛选')}}</button></footer>
 </section></div>
</template>
<style scoped>
.work-filter-control .work-filter-panel{position:fixed;left:50%;right:auto;top:145px;transform:translateX(-50%);width:min(800px,calc(100vw - 48px));max-height:calc(100vh - 180px);overflow:auto}
.work-filter-control{position:relative;flex:none}.work-filter-panel{position:absolute;z-index:50;top:calc(100% + 8px);right:0;width:min(760px,calc(100vw - 60px));padding:16px;border:1px solid var(--line,#dedfeb);border-radius:8px;background:var(--surface,#fff);box-shadow:none;text-align:left}.work-filter-panel header{display:flex;justify-content:space-between;gap:20px;margin-bottom:14px}.work-filter-panel header b{font-size:14px}.work-filter-panel p{font-size:12px;color:#7a8398;margin:6px 0;line-height:1.6}.work-filter-panel button{cursor:pointer}.work-filter-panel header>button,.work-filter-row>button{border:0;background:transparent;color:#8590a4;font-size:21px}.work-filter-row{display:grid;grid-template-columns:minmax(140px,1.2fr) minmax(120px,1fr) minmax(120px,1.2fr) 24px;gap:8px;margin:9px 0}.work-filter-row select,.work-filter-row input{width:100%;min-width:0;max-width:none;padding:8px;font-size:12px}.work-filter-panel footer{display:flex;align-items:center;gap:10px;margin-top:18px;border-top:1px solid #eef0f5;padding-top:14px}.work-filter-panel footer>span{margin-right:auto;font-size:11px;color:#929aae}.no-filter-value{padding:7px;color:#a0a6b6}.filter-empty{padding:16px 0}.work-filter-panel .field-error{color:#b42318}@media(max-width:760px){.work-filter-panel{position:fixed;left:15px;right:15px;top:100px;width:auto;max-height:75vh;overflow:auto}.work-filter-row{grid-template-columns:1fr 1fr 24px}.work-filter-row>select:first-child{grid-column:1/-1}.work-filter-panel footer{flex-wrap:wrap}}
</style>
<style scoped>
@media(max-width:820px){
 .work-filter-control .work-filter-panel{left:12px;right:12px;top:max(12px,env(safe-area-inset-top));transform:none;width:auto;max-height:calc(100dvh - 24px - env(safe-area-inset-top) - env(safe-area-inset-bottom));padding:16px;overscroll-behavior:contain}
 .work-filter-row{grid-template-columns:minmax(0,1fr) minmax(0,1fr) 36px}
 .work-filter-row>select:first-child{grid-column:1/-1}.work-filter-row select,.work-filter-row input{font-size:16px;min-height:44px}
 .work-filter-panel header>button,.work-filter-row>button{min-width:36px;min-height:44px}
 .work-filter-panel footer{flex-wrap:wrap}.work-filter-panel footer>button{min-height:44px}
}
</style>

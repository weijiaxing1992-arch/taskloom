<script setup lang="ts">
import {ref,watch,onBeforeUnmount} from 'vue'
import {t} from '../i18n'
import {requirementSuggestions,type RequirementSuggestion} from '../requirementSuggestions'
const props=defineProps<{description:string;acceptance:string}>()
const items=ref<RequirementSuggestion[]>([]),hasContent=ref(false),pending=ref(false)
let timer:ReturnType<typeof setTimeout>|undefined
// 停止输入 800ms 后检查；清空立即移除旧结果，避免展示与当前内容不符的建议。
watch(()=>[props.description,props.acceptance],()=>{
 clearTimeout(timer);items.value=[];hasContent.value=!!props.description.trim();pending.value=hasContent.value
 if(hasContent.value)timer=setTimeout(()=>{items.value=requirementSuggestions(props.description,props.acceptance);pending.value=false},800)
},{immediate:true})
onBeforeUnmount(()=>clearTimeout(timer))
</script>
<template>
 <details v-if="hasContent" class="requirement-suggestions" open>
  <summary>{{t('需求优化意见')}} <span>{{pending?t('检查中…'):t('建议 {count} 项',{count:items.length})}}</span></summary>
  <p>{{t('本机规则检查，非 AI 分析；仅供补充参考，不修改正文，也不外发内容。')}}</p>
  <div v-if="!pending" aria-live="polite"><ul v-if="items.length"><li v-for="item in items" :key="item.key"><b>{{t(item.title)}}</b><span>{{t(item.detail)}}</span></li></ul><p v-else>{{t('未发现上述规则覆盖的明显遗漏，仍需人工确认业务逻辑与验收标准。')}}</p></div>
 </details>
</template>
<style scoped>
.requirement-suggestions{margin:12px 0;padding:10px 12px;border:1px solid var(--line);border-radius:6px;background:var(--surface);font-size:13px;overflow-wrap:anywhere}.requirement-suggestions summary{cursor:pointer;font-weight:600}.requirement-suggestions summary span{margin-left:8px;color:var(--muted);font-size:12px;font-weight:400}.requirement-suggestions p{color:var(--muted);font-size:12px;line-height:1.6;margin:8px 0}.requirement-suggestions ul{list-style:none;margin:0;padding:0;display:grid;gap:10px}.requirement-suggestions li{display:grid;gap:3px;line-height:1.6}.requirement-suggestions li span{color:var(--muted)}
</style>

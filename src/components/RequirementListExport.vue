<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { api } from '../api'
import { t } from '../i18n'
import { downloadFile,requirementCSV } from '../workItemExport'
import { useSettingsScope } from './settingsScope'
import { Button } from './ui/button'
import { requirementExportLimit } from '../requirementPaging'
const props=defineProps<{items:Array<{id:number;projectId?:string|number}>;columns?:string[];projectId:string|number;label?:string;total?:number;recordsProvider?:(signal:AbortSignal,progress:(done:number,total:number)=>void)=>Promise<any[]>}>()
const busy=ref(false),error=ref(''),done=ref(0),exportTotal=ref(0),scope=useSettingsScope()
const available=computed(()=>props.recordsProvider?(props.total??props.items.length):props.items.length)
let controller:AbortController|null=null
function cancel(){controller?.abort()}
watch(()=>scope.locked.value,locked=>{if(locked)cancel()})
onBeforeUnmount(cancel)
async function exportList(){
  if(busy.value||!available.value||!scope.current())return
  const seen=new Set<string>(),items=props.items.filter(item=>{const key=(item.projectId||props.projectId)+':'+item.id;if(seen.has(key))return false;seen.add(key);return true}),columns=props.columns?[...props.columns]:undefined,projectId=String(props.projectId),provider=props.recordsProvider
  if(available.value>requirementExportLimit){error.value=t('最多导出 20000 条需求，请缩小筛选范围后分批导出');return}
  const operation=new AbortController();controller=operation
  const current=()=>scope.current()&&!operation.signal.aborted
  busy.value=true;done.value=0;exportTotal.value=available.value;error.value=''
  try{
    const contexts=new Map<string,{definitions:any[];members:any[]}>(),results:any[]=[]
    // 分页提供器返回完整字段数据，避免导出再逐条 GET 造成 2 万次请求。
    // 旧调用仍逐条读取，但同样支持取消、作用域失效及数量上限保护。
    const records=provider?await provider(operation.signal,(doneCount,total)=>{if(current()){done.value=doneCount;exportTotal.value=total}}):items
    if(!current())return
    if(records.length>requirementExportLimit)throw Error('最多导出 20000 条需求，请缩小筛选范围后分批导出')
    for(const item of records){
      if(!current())return
      const project=String(item.projectId||projectId),headers={'X-TaskLoom-Project':project},signal=operation.signal
      if(!contexts.has(project)){
        const [defs,members]=await Promise.all([api<any>('/field-definitions?objectType=requirement',{headers,signal}),api<any>('/members',{headers,signal})])
        if(!current())return
        contexts.set(project,{definitions:defs.items||[],members:members.items||[]})
      }
      const result=provider?item:await api<any>(`/requirements/${item.id}`,{headers,signal})
      if(!current())return
      results.push({...result,projectId:project});if(!provider)done.value++
    }
    if(!current())return
    const definitions=[...contexts.values()].flatMap(c=>c.definitions),members=[...contexts.values()].flatMap(c=>c.members)
    downloadFile(new Blob([requirementCSV(results,definitions,members,columns,contexts)],{type:'text/csv;charset=utf-8'}),`requirements-${new Date().toISOString().slice(0,10)}.csv`)
  }catch(cause){if(current())error.value=cause instanceof Error?t(cause.message):t('导出失败，请重试')}
  finally{if(controller===operation){controller=null;busy.value=false}}
}
</script>
<template><div class="list-export"><Button type="button" variant="outline" size="toolbar" :disabled="busy||!available||scope.locked.value" :title="t('导出全部筛选结果，保留当前排序')" @click.stop="exportList">{{busy?t('正在导出 {done}/{total}',{done,total:exportTotal}):label||t('导出需求列表')}}</Button><Button v-if="busy" type="button" variant="ghost" size="toolbar" @click.stop="cancel">{{t('取消导出')}}</Button><small v-if="error" role="alert">{{error}}</small></div></template>
<style scoped>.list-export{display:inline-flex;align-items:center;gap:8px;flex-wrap:wrap}.list-export small{color:var(--danger,#c95050);max-width:280px;font-size:11px}</style>

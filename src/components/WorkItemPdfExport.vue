<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { apiDownload } from '../api'
import { t } from '../i18n'
import { downloadFile } from '../workItemExport'
import { useSettingsScope } from './settingsScope'
import { Button } from './ui/button'
import { Popover, PopoverContent, PopoverTrigger } from './ui/popover'
type ExportFormat = 'pdf' | 'json' | 'markdown'
const props=defineProps<{objectType:'requirement'|'defect';objectId:number;projectId:string|number;disabled?:boolean}>()
const busy=ref(false),error=ref(''),opened=ref(false),activeFormat=ref<ExportFormat>('pdf'),invalidated=ref(false),scope=useSettingsScope()
let controller:AbortController|null=null,generation=0,disposed=false
const validObject=computed(()=>['requirement','defect'].includes(props.objectType)&&Number.isSafeInteger(props.objectId)&&props.objectId>0&&String(props.projectId).trim()!=='')
const unavailable=computed(()=>busy.value||props.disabled||scope.locked.value||invalidated.value||!validObject.value)
const progressLabel=computed(()=>activeFormat.value==='pdf'?t('正在生成 PDF…'):activeFormat.value==='json'?t('正在导出 JSON…'):t('正在导出 Markdown…'))
// 需求导出文件名与页面编号完全一致：固定六位纯数字。超过分配上限的
// 历史数据不会被截断，避免下载时映射到另一条需求；新建接口已阻止该情况。
function requirementSerial(id:number){return id>0&&id<=999999?String(id).padStart(6,'0'):String(id)}
function cancel(){generation++;controller?.abort();controller=null;busy.value=false;opened.value=false}
function invalidate(){invalidated.value=true;cancel();error.value=''}
async function exportFile(format:ExportFormat){
  if(unavailable.value||disposed||!scope.current()||!['pdf','json','markdown'].includes(format)||format!=='pdf'&&props.objectType!=='requirement')return
  const id=props.objectId,type=props.objectType,project=String(props.projectId),version=++generation,operation=new AbortController()
  controller=operation;busy.value=true;error.value='';opened.value=false;activeFormat.value=format
  const current=()=>!disposed&&!invalidated.value&&generation===version&&controller===operation&&!operation.signal.aborted&&scope.current()&&id===props.objectId&&type===props.objectType&&project===String(props.projectId)
  try{
    const path=format==='pdf'?`/exports/${type}/${id}.pdf`:`/requirements/${id}/export?format=${format}`
    const blob=await apiDownload(path,{headers:{'X-TaskLoom-Project':project},signal:operation.signal})
    if(!current())return
    const mime=blob.type.split(';')[0]?.trim().toLowerCase(),expected=format==='pdf'?'application/pdf':format==='json'?'application/json':'text/markdown'
    if(mime!==expected||!blob.size)throw new Error(t(format==='pdf'?'PDF 生成失败，请重试':'导出文件格式异常，请重试'))
    // 后端完整快照按原始字节下载，不从列表/编辑草稿重建，不丢弃未知字段或关联内容。
    const code=type==='requirement'?requirementSerial(id):`BUG-${String(id).padStart(4,'0')}`
    const filename=format==='pdf'?`${code}.pdf`:`${code}-complete.${format==='json'?'json':'md'}`
    downloadFile(blob,filename)
  }catch(cause){if(current())error.value=cause instanceof Error?cause.message:t('导出失败，请重试')}
  finally{if(controller===operation){controller=null;busy.value=false}}
}
function exportPDF(){return exportFile('pdf')}
watch(()=>[props.objectId,props.objectType,String(props.projectId)],()=>{cancel();error.value=''},{flush:'sync'})
watch(()=>props.disabled,disabled=>{if(disabled)cancel()},{flush:'sync'})
watch(()=>scope.locked.value,locked=>{if(locked)invalidate()},{flush:'sync'})
onMounted(()=>window.addEventListener('devflow-password-change-required',invalidate))
onBeforeUnmount(()=>{disposed=true;cancel();window.removeEventListener('devflow-password-change-required',invalidate)})
</script>
<template>
  <div class="export-control" :aria-busy="busy" @click.stop>
    <Popover v-if="objectType==='requirement'" v-model:open="opened">
      <PopoverTrigger as-child><Button type="button" variant="outline" size="sm" :disabled="unavailable" :title="t('导出已保存版本，不包含未保存的修改')" :aria-label="t('选择需求导出格式')">{{busy?progressLabel:t('导出需求')}}<span aria-hidden="true">⌄</span></Button></PopoverTrigger>
      <PopoverContent v-if="opened&&!unavailable" class="work-item-export-menu w-[280px] max-w-[calc(100vw-24px)] p-1" align="end" :side-offset="6" :collision-padding="12" :aria-label="t('选择需求导出格式')" @escape-key-down="$event.stopPropagation()" @click.stop>
        <div class="work-item-export-options">
          <button type="button" @click.stop="exportPDF"><span>{{t('导出 PDF')}}</span><small>{{t('便于阅读和打印')}}</small></button>
          <button type="button" @click.stop="exportFile('json')"><span>{{t('导出完整 JSON')}}</span><small>{{t('完整字段和关联内容，适合程序处理')}}</small></button>
          <button type="button" @click.stop="exportFile('markdown')"><span>{{t('导出完整 Markdown')}}</span><small>{{t('完整需求内容，适合阅读和文本协作')}}</small></button>
        </div>
        <p class="work-item-export-note">{{t('导出已保存版本，不包含未保存的修改')}}</p>
      </PopoverContent>
    </Popover>
    <Button v-else type="button" variant="outline" size="sm" :disabled="unavailable" :title="t('导出已保存版本，不包含未保存的修改')" @click.stop="exportPDF">{{t(busy?'正在生成 PDF…':'导出 PDF')}}</Button>
    <Button v-if="busy" type="button" variant="ghost" size="sm" @click.stop="cancel">{{t('取消导出')}}</Button>
    <small v-if="error" class="export-error" role="alert">{{t(error)}}</small>
  </div>
</template>
<style scoped>
.export-control{display:inline-flex;flex-wrap:wrap;align-items:center;gap:6px;min-width:0;max-width:100%}.export-control .export-error{color:var(--danger,#c95050);max-width:280px;font-size:11px;line-height:1.6;overflow-wrap:anywhere}.work-item-export-options{display:grid;gap:2px}.work-item-export-options>button{display:grid!important;gap:4px!important;width:100%;min-height:54px;padding:9px 11px!important;border:0!important;border-radius:6px;background:transparent;color:var(--foreground,var(--ink));font:inherit;text-align:left;cursor:pointer;white-space:normal}.work-item-export-options>button:hover,.work-item-export-options>button:focus-visible{background:var(--accent,var(--primary-soft));color:var(--accent-foreground,var(--ink));outline:2px solid transparent}.work-item-export-options>button:focus-visible{outline:2px solid var(--ring,var(--primary));outline-offset:-2px}.work-item-export-options>button>span{font-size:12px;font-weight:600}.work-item-export-options>button>small{font-size:11px;line-height:1.5;color:var(--muted-foreground,var(--muted))}.work-item-export-note{border-top:1px solid var(--border,var(--line));margin:4px 0 0;padding:10px 11px 7px;color:var(--muted-foreground,var(--muted));font-size:10px;line-height:1.6;white-space:normal}@media(max-width:640px){.export-control :deep(button){min-height:38px}.work-item-export-options>button{min-height:58px}.export-control .export-error{flex-basis:100%;max-width:100%}}
</style>

<script setup lang="ts">
import {computed,ref,watch,onBeforeUnmount} from 'vue'
import {t} from '../i18n'
import AIButton from './AIButton.vue'
import {useSettingsScope} from './settingsScope'
const props=defineProps<{description:string;acceptance:string;requirementId?:number;disabled?:boolean;kind?:'requirement'|'defect'}>()
const emit=defineEmits<{apply:[value:{description:string;acceptance:string}];busy:[value:boolean]}>()
const scope=useSettingsScope(),open=ref(false),busy=ref(false),consent=ref(false),error=ref(''),reused=ref(false),preview=ref<Record<string,string>|null>(null),selected=ref<string[]>([])
const sections=computed(()=>props.kind==='defect'?[['background','缺陷描述与影响'],['rules','复现步骤'],['exceptions','实际结果'],['acceptance','预期结果与回归检查'],['questions','待确认问题']]:[['background','背景与目标'],['rules','功能规则'],['exceptions','异常与边界'],['acceptance','验收标准'],['questions','待确认问题']])
// This is exactly the text sent after consent; never silently fetch comments or attachments.
const sourceText=computed(()=>props.description+'\n\n已有预期或验收标准：\n'+props.acceptance)
const sourceLength=computed(()=>Array.from(sourceText.value).length)
// Preview the same append-only payload that the parent editor receives on adoption.
const adoption=computed(()=>{
 const chosen=sections.value.filter(([key])=>selected.value.includes(key!)&&preview.value?.[key!]?.trim())
 return {
  description:chosen.filter(([key])=>key!=='acceptance').map(([key,label])=>label+'\n'+preview.value![key!]).join('\n\n'),
  acceptance:chosen.some(([key])=>key==='acceptance')?preview.value?.acceptance||'':'',
  count:chosen.length,
 }
})
let revision=0,disposed=false
watch(()=>[props.description,props.acceptance,props.requirementId,props.kind],()=>{revision++;preview.value=null;selected.value=[];consent.value=false;error.value='';reused.value=false})
watch(scope.locked,()=>{revision++;preview.value=null;selected.value=[];consent.value=false})
onBeforeUnmount(()=>{disposed=true;revision++})
async function generate(){
 if(busy.value||props.disabled||!consent.value||!scope.current())return
 const version=revision;busy.value=true;emit('busy',true);error.value=''
 try{const result=await scope.request<{preview:Record<string,string>;model:string;reused?:boolean}>(props.kind==='defect'?'/ai/defect-refine':'/ai/requirement-refine',{method:'POST',body:JSON.stringify({description:sourceText.value,confirmed:true,...(props.requirementId&&props.kind!=='defect'?{requirementId:props.requirementId}:{}),...(preview.value?{forceNew:true}:{})})});if(disposed||version!==revision||!scope.current())return
 if(!result.preview||sections.value.some(([key])=>typeof result.preview[key!]!=='string'))throw Error('返回内容格式不正确，请重试')
 preview.value=result.preview;selected.value=[];consent.value=false;reused.value=result.reused===true
 }catch(e){if(!disposed&&version===revision)error.value=e instanceof Error?e.message:'生成失败，请重试'}finally{if(!disposed){busy.value=false;emit('busy',false)}}
}
function apply(){if(!preview.value||props.disabled||busy.value||!scope.current()||!adoption.value.count)return;emit('apply',{description:adoption.value.description,acceptance:adoption.value.acceptance});preview.value=null;selected.value=[]}
</script>
<template>
 <section class="requirement-refinement">
  <AIButton :disabled="disabled||scope.locked.value" :aria-expanded="open" @click="open=!open">{{t(kind==='defect'?'AI 优化缺陷描述':'AI 优化需求描述')}}</AIButton>
  <div v-if="open" class="refinement-panel">
   <p>{{t(kind==='defect'?'以专业缺陷分析视角优化复现步骤、实际与预期结果；不虚构根因或测试结论。仅追加确认的段落。':'以专业 PRD 视角完善背景、规则、异常场景与验收标准；缺失事实标记为待确认。仅追加所选段落，保留原文和图片。')}}</p>
   <details class="refinement-source">
    <summary>{{t('查看本次发送材料')}} <span>{{t('{count} 字',{count:sourceLength})}}</span></summary>
    <p>{{t('以下是本次发送的完整文字；不自动读取其他需求、评论或附件。原文中填写的敏感信息也会发送，请先检查。')}}</p>
    <pre>{{sourceText}}</pre>
   </details>
   <label class="refinement-consent"><input v-model="consent" type="checkbox" :disabled="busy||disabled||scope.locked.value">{{t('同意将当前描述及验收标准的文字发送给企业配置的 AI 服务（可能收费，不发送附件文件）。')}}</label>
   <AIButton :busy="busy" :disabled="disabled||!consent||!description.trim()||scope.locked.value" @click="generate">{{t(busy?'正在生成…':preview?'重新生成预览':'生成完善预览')}}</AIButton>
   <p>{{t('相同内容与配置会优先恢复近期预览；点击重新生成会再次调用 AI。')}}</p>
   <p v-if="error" role="alert">{{t(error)}}</p>
   <p v-if="preview&&reused" role="status">{{t('已恢复近期相同输入的预览，本次未再次调用外部 AI 服务。')}}</p>
   <div v-if="preview" aria-live="polite">
    <div class="refinement-provenance">
     <strong>{{t('先核对来源，再选择采纳')}}</strong>
     <p>{{t('原文明确：来自输入，不代表已验证；优化建议：供选择的改进；待确认：缺少信息或存在冲突。标注由 AI 提供，仍需核对原文。')}}</p>
    </div>
    <article v-for="[key,label] in sections" :key="key"><label><input v-model="selected" type="checkbox" :value="key" :disabled="busy||disabled||!preview[key!]">{{t(label!)}}<span v-if="key==='questions'" class="refinement-question-note">{{t('不作为已确定规则')}}</span></label><pre>{{preview[key!]||t('暂无补充')}}</pre></article>
    <section v-if="adoption.count" class="refinement-changes" :aria-label="t('采纳变更预览')">
     <div class="refinement-changes-heading"><strong>{{t('采纳变更预览')}}</strong><span>{{t('新增 {count} 段',{count:adoption.count})}}</span></div>
     <p>{{t('原文保留，不删除、不替换；以下仅为本次新增内容。')}}</p>
     <div v-if="adoption.description" class="refinement-addition"><strong>{{t('正文新增')}}</strong><pre>{{adoption.description}}</pre></div>
     <div v-if="adoption.acceptance" class="refinement-addition"><strong>{{t(kind==='defect'?'预期结果新增':'验收标准新增')}}</strong><pre>{{adoption.acceptance}}</pre></div>
    </section>
    <div class="refinement-actions"><button type="button" class="btn primary" :disabled="busy||disabled||!adoption.count||scope.locked.value" @click="apply">{{t('确认追加所选段落')}}</button><button type="button" class="btn" :disabled="busy" @click="preview=null;selected=[]">{{t('放弃预览')}}</button></div>
    <p>{{t(kind==='defect'?'追加后仍需保存缺陷。修改输入后旧预览会清除；生成结果不会自动提交。':'追加后仍需保存需求。修改输入后旧预览会清除；生成结果不会自动提交。')}}</p>
   </div>
  </div>
 </section>
</template>
<style scoped>
.requirement-refinement{margin:12px 0;min-width:0}.refinement-panel{margin-top:8px;border:1px solid var(--line);border-radius:8px;padding:12px;background:var(--surface)}.refinement-panel p{font-size:12px;line-height:1.6;color:var(--muted)}.refinement-consent{display:flex;align-items:flex-start;gap:8px;font-size:12px;line-height:1.6;margin:12px 0}.refinement-panel article{border-bottom:1px solid var(--line);padding:12px 0}.refinement-panel article label{display:flex;align-items:center;gap:8px;font-weight:600}.refinement-panel pre{white-space:pre-wrap;overflow-wrap:anywhere;font:inherit;font-size:13px;line-height:1.7;margin:8px 0}.refinement-panel .btn{margin:10px 6px 0 0}
.refinement-source{margin:12px 0;padding:10px 12px;border:1px solid var(--line);border-radius:8px}.refinement-source summary{cursor:pointer;font-size:13px}.refinement-source summary span{margin-left:8px;color:var(--muted);font-variant-numeric:tabular-nums}.refinement-source pre{max-height:240px;overflow:auto}.refinement-provenance{margin-top:16px;padding:10px 12px;border-left:3px solid var(--primary,#336df4);background:var(--surface);font-size:13px}.refinement-provenance p{margin-bottom:0}.refinement-question-note{font-size:12px;font-weight:400;color:var(--muted);margin-left:auto}.refinement-changes{margin-top:16px;padding:12px;border:1px solid var(--line);border-radius:8px;min-width:0}.refinement-changes-heading{display:flex;flex-wrap:wrap;justify-content:space-between;gap:8px;font-size:13px}.refinement-changes-heading span{color:var(--muted);font-variant-numeric:tabular-nums}.refinement-addition{padding-left:12px;border-left:3px solid #2a9272;margin-top:12px;font-size:12px}.refinement-addition pre{max-height:240px;overflow:auto}.refinement-actions{display:flex;flex-wrap:wrap;gap:8px;margin-top:12px}.refinement-actions .btn{margin:0}.refinement-consent input,.refinement-panel article input{flex:0 0 auto}.refinement-panel article label{flex-wrap:wrap}
@media(max-width:600px){.refinement-panel{padding:10px}.refinement-question-note{margin-left:0}.refinement-actions .btn{flex:1 1 auto;white-space:normal}}
</style>

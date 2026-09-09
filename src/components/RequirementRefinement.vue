<script setup lang="ts">
import {ref,watch,onBeforeUnmount} from 'vue'
import AIButton from './AIButton.vue'
import {useSettingsScope} from './settingsScope'
const props=defineProps<{description:string;acceptance:string;requirementId?:number;disabled?:boolean}>()
const emit=defineEmits<{apply:[value:{description:string;acceptance:string}];busy:[value:boolean]}>()
const scope=useSettingsScope(),open=ref(false),busy=ref(false),consent=ref(false),error=ref(''),preview=ref<Record<string,string>|null>(null),selected=ref<string[]>([])
const sections=[['background','背景与目标'],['rules','功能规则'],['exceptions','异常与边界'],['acceptance','验收标准'],['questions','待确认问题']]
let revision=0,disposed=false
watch(()=>[props.description,props.acceptance,props.requirementId],()=>{revision++;preview.value=null;selected.value=[];consent.value=false})
watch(scope.locked,()=>{revision++;preview.value=null;consent.value=false})
onBeforeUnmount(()=>{disposed=true;revision++})
async function generate(){
 if(busy.value||props.disabled||!consent.value||!scope.current())return
 const version=revision;busy.value=true;emit('busy',true);error.value=''
 try{const result=await scope.request<{preview:Record<string,string>;model:string}>('/ai/requirement-refine',{method:'POST',body:JSON.stringify({description:props.description+'\n\n已有验收标准：\n'+props.acceptance,confirmed:true,...(props.requirementId?{requirementId:props.requirementId}:{})})});if(disposed||version!==revision||!scope.current())return
 if(!result.preview||sections.some(([key])=>typeof result.preview[key!]!=='string'))throw Error('返回内容格式不正确，请重试')
 preview.value=result.preview;selected.value=[];consent.value=false
 }catch(e){if(!disposed&&version===revision)error.value=e instanceof Error?e.message:'生成失败，请重试'}finally{if(!disposed){busy.value=false;emit('busy',false)}}
}
function apply(){if(!preview.value||props.disabled||busy.value||!scope.current())return;const description=sections.filter(([k])=>k!=='acceptance'&&selected.value.includes(k!)).map(([k,label])=>label+'\n'+preview.value![k!]).join('\n\n');const acceptance=selected.value.includes('acceptance')?preview.value.acceptance||'':'';emit('apply',{description,acceptance});preview.value=null;selected.value=[]}
</script>
<template>
 <section class="requirement-refinement">
  <AIButton :disabled="disabled||scope.locked.value" :aria-expanded="open" @click="open=!open">AI 完善需求</AIButton>
  <div v-if="open" class="refinement-panel">
   <p>完善背景、规则、异常场景与验收标准；缺失事实标记为待确认。仅追加你选中的段落，不替换原文、图片或人员字段。</p>
   <label class="refinement-consent"><input v-model="consent" type="checkbox" :disabled="busy||disabled||scope.locked.value">同意将当前描述及验收标准的文字发送给已配置的 OpenAI 模型（可能收费，不发送附件文件）。</label>
   <AIButton :busy="busy" :disabled="disabled||!consent||!description.trim()||scope.locked.value" @click="generate">{{busy?'正在生成…':preview?'重新生成预览':'生成完善预览'}}</AIButton>
   <p v-if="error" role="alert">{{error}}</p>
   <div v-if="preview" aria-live="polite"><article v-for="[key,label] in sections" :key="key"><label><input v-model="selected" type="checkbox" :value="key" :disabled="busy||disabled||!preview[key!]">{{label}}</label><pre>{{preview[key!]||'暂无补充'}}</pre></article><button type="button" class="btn primary" :disabled="busy||disabled||!selected.length||scope.locked.value" @click="apply">确认追加所选段落</button><button type="button" class="btn" :disabled="busy" @click="preview=null;selected=[]">放弃预览</button><p>追加后仍需保存需求。修改输入后旧预览会清除；生成结果不会自动提交。</p></div>
  </div>
 </section>
</template>
<style scoped>
.requirement-refinement{margin:12px 0;min-width:0}.refinement-panel{margin-top:8px;border:1px solid var(--line);border-radius:8px;padding:12px;background:var(--surface)}.refinement-panel p{font-size:12px;line-height:1.6;color:var(--muted)}.refinement-consent{display:flex;align-items:flex-start;gap:8px;font-size:12px;line-height:1.6;margin:12px 0}.refinement-panel article{border-bottom:1px solid var(--line);padding:12px 0}.refinement-panel article label{display:flex;align-items:center;gap:8px;font-weight:600}.refinement-panel pre{white-space:pre-wrap;overflow-wrap:anywhere;font:inherit;font-size:13px;line-height:1.7;margin:8px 0}.refinement-panel .btn{margin:10px 6px 0 0}
</style>

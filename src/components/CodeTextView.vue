<script setup lang="ts">
import { t } from '../i18n'
import { computed, defineComponent, h, ref } from 'vue'
import { codeHighlighter, detectCodeLanguage, languageLabel, splitCodeFences } from '../codeHighlight'
import '../code-reading.css'
const props=defineProps<{text:string}>()
const parts=computed(()=>splitCodeFences(props.text)),message=ref(''),wrap=ref<Record<number,boolean>>({})
const Highlight=defineComponent({props:{code:{type:String,required:true},language:{type:String,required:true}},setup(p){
  const render=(node:any):any=>node.type==='text'?node.value:h('span',{class:Array.isArray(node.properties?.className)?node.properties.className:[]},(node.children||[]).map(render))
  return ()=>h('code',{},codeHighlighter.highlight(p.language,p.code).children.map(render))
}})
function label(language:string,code:string) { return languageLabel(language==='auto'?detectCodeLanguage(code):language) }
async function copy(code:string) { try{await navigator.clipboard.writeText(code);message.value='代码已复制'}catch{message.value='复制失败，请选中代码后使用系统复制'} }
</script>
<template><div class="code-text-view"><template v-for="(part,index) in parts" :key="index"><section v-if="part.kind==='code'" class="code-reading" :class="{'code-wrap':wrap[index]}"><div class="code-reading-tools"><span>{{t(label(part.language,part.text))}}</span><span>{{t('{count} 行',{count:part.text.split('\n').length})}}</span><button type="button" :aria-pressed="!!wrap[index]" @click="wrap[index]=!wrap[index]">{{t('自动换行')}}</button><button type="button" @click="copy(part.text)">{{t('复制代码')}}</button></div><pre><Highlight :code="part.text" :language="part.language"/></pre></section><div v-else class="code-plain-text">{{part.text}}</div></template><span v-if="message" class="code-copy-status" role="status">{{t(message)}}</span></div></template>

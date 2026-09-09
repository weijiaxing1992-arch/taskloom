<script setup lang="ts">
import { t } from '../i18n'
import { computed, ref } from 'vue'
import { NodeViewWrapper, NodeViewContent, nodeViewProps } from '@tiptap/vue-3'
import { codeLanguages, detectCodeLanguage, languageLabel } from '../codeHighlight'
import '../code-reading.css'
const props=defineProps(nodeViewProps),wrap=ref(false),message=ref('')
const language=computed(()=>props.node.attrs.language||'auto')
const detected=computed(()=>language.value==='auto'?detectCodeLanguage(props.node.textContent):language.value)
function change(event:Event){if(props.editor.isEditable)props.updateAttributes({language:(event.target as HTMLSelectElement).value})}
async function copy(){try{await navigator.clipboard.writeText(props.node.textContent);message.value='代码已复制'}catch{message.value='复制失败，请选中代码后使用系统复制'}}
</script>
<template><NodeViewWrapper class="code-reading" :class="{'code-wrap':wrap}"><div class="code-reading-tools" contenteditable="false"><select v-if="editor.isEditable" :value="language" :aria-label="t('代码语言')" @change="change"><option v-if="!codeLanguages.includes(language)" :value="language">{{language}}</option><option v-for="value in codeLanguages" :key="value" :value="value">{{t(languageLabel(value))}}</option></select><span v-else>{{t(languageLabel(detected))}}</span><span v-if="editor.isEditable&&language==='auto'&&detected!=='auto'">{{t('已识别代码语言：{language}',{language:languageLabel(detected)})}}</span><span>{{t('{count} 行',{count:node.textContent.split('\n').length})}}</span><button type="button" :aria-pressed="wrap" @click="wrap=!wrap">{{t('自动换行')}}</button><button type="button" @click="copy">{{t('复制代码')}}</button><span v-if="message" role="status">{{t(message)}}</span></div><pre><NodeViewContent as="code"/></pre></NodeViewWrapper></template>

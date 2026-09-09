<script setup lang="ts">
import { t } from '../i18n'
import { ref } from 'vue'
import { codeLanguages, languageLabel } from '../codeHighlight'
import MarkdownReadView from './MarkdownReadView.vue'
defineProps<{text:string;disabled?:boolean}>()
defineEmits<{insert:[language:string]}>()
const language=ref('auto'),preview=ref(false)
</script>
<template><div class="code-insert-tools"><div class="code-reading-tools"><select v-model="language" :disabled="disabled" :aria-label="t('插入代码语言')"><option v-for="value in codeLanguages" :key="value" :value="value">{{t(languageLabel(value))}}</option></select><button type="button" :disabled="disabled" @mousedown.prevent @click="$emit('insert',language)">{{t('插入代码块')}}</button><button type="button" :aria-expanded="preview" @click="preview=!preview">{{t(preview?'收起预览':'Markdown 与代码预览')}}</button></div><MarkdownReadView v-if="preview" :text="text"/></div></template>

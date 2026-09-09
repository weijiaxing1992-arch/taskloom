<script setup lang="ts">
import { computed } from 'vue'
import RichTextEditor from './RichTextEditor.vue'
import { importMarkdown } from '../markdownImport'
import { t } from '../i18n'
const props=defineProps<{text:string}>()
const parsed=computed(()=>{try{return importMarkdown(props.text)}catch{return null}})
</script>
<template><RichTextEditor v-if="parsed" :model-value="text" :document="parsed.document" :members="[]" readonly :label="t('Markdown 预览')"/><div v-else class="code-plain-text">{{text}}</div></template>

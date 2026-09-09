<script setup lang="ts">
import { nextTick, ref } from 'vue'
import CodeInsertTools from './CodeInsertTools.vue'
import { clipboardCodeText, insideCodeFence } from '../editorPaste'
import { fencedCode } from '../codeHighlight'
const props=defineProps<{modelValue:string;inputId?:string;rows?:number;disabled?:boolean;label?:string}>()
const emit=defineEmits<{'update:modelValue':[value:string]}>(),input=ref<HTMLTextAreaElement|null>(null)
async function insert(language:string){
  if(props.disabled||input.value?.closest('fieldset[disabled], [inert]'))return
  const start=input.value?.selectionStart??props.modelValue.length,end=input.value?.selectionEnd??start
  const code=props.modelValue.slice(start,end),block='\n'+fencedCode(code,language)+'\n'
  emit('update:modelValue',props.modelValue.slice(0,start)+block+props.modelValue.slice(end))
  await nextTick();input.value?.focus();const caret=start+block.indexOf('\n',1)+1;input.value?.setSelectionRange(caret,caret+code.length)
}
async function pasteCode(event:ClipboardEvent){
  if(props.disabled||input.value?.closest('fieldset[disabled], [inert]'))return
  const start=input.value?.selectionStart??props.modelValue.length,end=input.value?.selectionEnd??start
  if(insideCodeFence(props.modelValue.slice(0,start)))return
  const text=event.clipboardData?.getData('text/plain')||'',converted=clipboardCodeText(text)
  if(converted===text)return
  const insertion='\n'+converted+'\n',body=props.modelValue.slice(0,start)+insertion+props.modelValue.slice(end)

  event.preventDefault();emit('update:modelValue',body)
  await nextTick();input.value?.focus();input.value?.setSelectionRange(start+insertion.length,start+insertion.length)
}
</script>
<template><div class="code-text-editor"><textarea :id="inputId" ref="input" :value="modelValue" :rows="rows||5" :disabled="disabled" :aria-label="label" @paste="pasteCode" @input="emit('update:modelValue',($event.target as HTMLTextAreaElement).value)"/><CodeInsertTools :text="modelValue" :disabled="disabled" @insert="insert"/></div></template>

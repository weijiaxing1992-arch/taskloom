<script setup lang="ts">
import {t} from '../i18n'
import {computed,onBeforeUnmount,ref,watch} from 'vue'
import {apiDownload} from '../api'
import {assetLanguage,decodeAssetText} from '../attachmentAssets'
import {base64ToBytes} from '../richText'
import {fencedCode} from '../codeHighlight'
import CodeTextView from './CodeTextView.vue'
const props=defineProps<{name:string;requirementId?:number;attachmentId?:number;data?:string}>()
const loading=ref(false),error=ref(''),source=ref(''),page=ref(0)
const emit=defineEmits<{close:[]}>()
let sequence=0
const language=computed(()=>assetLanguage(props.name,source.value)||'plaintext')
// 大文件分段显示，避免一次高亮上万行阻塞；复制和下载始终使用完整文件。
const pageSize=20000,pages=computed(()=>Math.max(1,Math.ceil(source.value.length/pageSize)))
const visible=computed(()=>source.value.slice(page.value*pageSize,(page.value+1)*pageSize))
async function load(){const version=++sequence;loading.value=true;error.value='';source.value='';page.value=0;try{let bytes:Uint8Array;if(props.data)bytes=base64ToBytes(props.data);else{if(!props.requirementId||!props.attachmentId)throw Error('请先保存附件');const blob=await apiDownload(`/requirements/${props.requirementId}/attachments/${props.attachmentId}`);bytes=new Uint8Array(await blob.arrayBuffer())}const text=decodeAssetText(bytes);if(version===sequence)source.value=text}catch(cause){if(version===sequence)error.value=cause instanceof Error?cause.message:'代码加载失败'}finally{if(version===sequence)loading.value=false}}
async function copy(){try{await navigator.clipboard.writeText(source.value);error.value='完整代码已复制'}catch{error.value='复制失败，请下载原文件'}}
watch(()=>[props.name,props.requirementId,props.attachmentId,props.data],load,{immediate:true})
onBeforeUnmount(()=>{sequence++})
</script>
<template><section class="asset-code-preview" :aria-label="t('代码附件预览')"><header><strong>{{name}}</strong><span>{{language}}</span><button type="button" :disabled="loading||!source" @click="copy">{{t('复制完整代码')}}</button><button type="button" @click="emit('close')" :aria-label="t('关闭代码预览')">{{t('关闭')}}</button></header><p v-if="loading" role="status">{{t('正在读取完整文件…')}}</p><p v-if="error" role="status">{{t(error)}}</p><template v-if="!loading"><nav v-if="pages>1" :aria-label="t('代码分段')"><button type="button" :disabled="page===0" @click="page--">{{t('上一段')}}</button><span>{{t('{page} / {pages} 段 · 共 {count} 字符，未截断；分段边界可能跨行',{page:page+1,pages,count:source.length})}}</span><button type="button" :disabled="page+1>=pages" @click="page++">{{t('下一段')}}</button></nav><CodeTextView v-if="source" :text="fencedCode(visible,language)"/><small v-else-if="!error">{{t('文件为空')}}</small></template></section></template>
<style scoped>.asset-code-preview{min-width:0;border:1px solid var(--line,#e4e7ec);border-radius:6px;margin:10px 0;padding:10px;background:var(--panel,#fff);user-select:text}.asset-code-preview header,.asset-code-preview nav{display:flex;align-items:center;gap:10px;flex-wrap:wrap;font-size:12px}.asset-code-preview strong{flex:1;min-width:100px;overflow-wrap:anywhere}.asset-code-preview button{font:inherit;padding:4px 8px;border:1px solid var(--line,#e4e7ec);border-radius:4px;background:transparent;color:var(--primary,#4268df)}.asset-code-preview p,.asset-code-preview small{font-size:12px}.asset-code-preview nav{padding-top:10px}.asset-code-preview :deep(pre){max-height:460px}</style>

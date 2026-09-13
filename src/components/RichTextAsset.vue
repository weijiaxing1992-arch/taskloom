<script setup lang="ts">
import { computed, inject, nextTick, onMounted, onBeforeUnmount, ref, shallowRef, watch, type ComputedRef } from 'vue'
import { NodeViewWrapper, nodeViewProps } from '@tiptap/vue-3'
import AssetIcon from './AssetIcon.vue'
import AssetCodePreview from './AssetCodePreview.vue'
import {assetCategories,assetCategory,assetLanguage,assetLabel} from '../attachmentAssets'
import { api, apiDownload } from '../api'
import { observeVisibleAsset } from '../visibleAsset'
import { t } from '../i18n'
import ImagePreview from './ImagePreview.vue'
import { base64ToBytes, richAssetContextKey, richAssetName, richImageType, type RichAssetContext } from '../richText'

const props = defineProps(nodeViewProps)
const context = inject<ComputedRef<RichAssetContext>>(richAssetContextKey, computed(() => ({ requirementId: undefined, readonly: true, disabled: true })))
const loading = ref(false), error = ref(''), imageURL = ref('')
const visibilityAnchor = ref<HTMLElement | null>(null)
let stopVisibility: (() => void) | undefined
const blob = shallowRef<Blob | null>(null)
const image = computed(() => props.node.type.name === 'image')
const pending = computed(() => !props.node.attrs.attachmentId && !!props.node.attrs.data)
const name = computed(() => richAssetName(String(props.node.attrs.name || 'attachment')))
const previewOpen = ref(false), previewIndex = ref(0)
const previewItems = ref<{ key: string; name: string; attachmentId?: number | null; data?: string | null; blob?: Blob | null }[]>([])
const metadata=ref<{category?:string;language?:string}>({}),codeOpen=ref(false)
const category=computed(()=>metadata.value.category||assetCategory(name.value,String(props.node.attrs.category||'auto')))
const language=computed(()=>metadata.value.language||assetLanguage(name.value))
let metadataVersion=0
async function loadMetadata(){const token=++metadataVersion,id=props.node.attrs.attachmentId,req=context.value.requirementId;if(!id||!req)return;try{const value=await api<{category:string;language?:string}>(`/requirements/${req}/attachments/${id}?metadata=1`);if(!disposed&&token===metadataVersion&&id===props.node.attrs.attachmentId&&req===context.value.requirementId)metadata.value=value}catch{/* 文件下载仍会展示权限或缺失错误。 */}}
function changeCategory(event:Event){if(context.value.readonly||context.value.disabled)return;const value=(event.target as HTMLSelectElement).value;props.updateAttributes({category:value});metadata.value={}}
let version = 0, disposed = false
const downloadURLs = new Set<string>()
function releasePreview() { if (imageURL.value) URL.revokeObjectURL(imageURL.value); imageURL.value = ''; blob.value = null }
async function load() {
  const sequence = ++version, id = props.node.attrs.attachmentId, data = props.node.attrs.data, requirementId = context.value.requirementId
  releasePreview(); loading.value = true; error.value = ''
  try {
    let result: Blob
    if (!id && typeof data === 'string' && data) result = new Blob([base64ToBytes(data) as BlobPart])
    else {
      if (!Number.isSafeInteger(id) || id < 1 || !requirementId) throw new Error('附件暂不可用，请保存后重新打开需求')
      result = await apiDownload('/requirements/' + requirementId + '/attachments/' + id)
    }
    if (disposed || sequence !== version) return
    if (image.value) {
      const type = richImageType(new Uint8Array(await result.slice(0, 16).arrayBuffer()))
      if (disposed || sequence !== version) return
      if (!type) throw new Error('图片仅支持 PNG、JPEG 或 GIF，不支持 SVG')
      result = new Blob([result], { type }); imageURL.value = URL.createObjectURL(result)
    }
    blob.value = result
  } catch (cause) { if (!disposed && sequence === version) error.value = cause instanceof Error ? cause.message : '附件载入失败，请重试' }
  finally { if (!disposed && sequence === version) loading.value = false }
}
async function download() {
  if (loading.value || disposed) return
  const id = props.node.attrs.attachmentId, data = props.node.attrs.data, requirementId = context.value.requirementId
  if (!blob.value) await load()
  if (!blob.value || disposed || id !== props.node.attrs.attachmentId || data !== props.node.attrs.data || requirementId !== context.value.requirementId) return
  const url = URL.createObjectURL(blob.value); downloadURLs.add(url)
  const link = document.createElement('a'); link.href = url; link.download = name.value; link.click()
  setTimeout(() => { URL.revokeObjectURL(url); downloadURLs.delete(url) }, 1000)
}
function remove() { if (!context.value.readonly && !context.value.disabled) props.deleteNode() }
function openPreview() {
  if (!image.value || !imageURL.value || disposed) return
  let position: number | undefined
  try { position = typeof props.getPos === 'function' ? props.getPos() : undefined } catch { return }
  const images: typeof previewItems.value = []
  props.editor.state.doc.descendants((node, offset) => {
    if (node.type.name !== 'image') return
    images.push({ key: 'image-' + offset, name: richAssetName(String(node.attrs.name || 'attachment')), attachmentId: node.attrs.attachmentId, data: node.attrs.data, blob: offset === position ? blob.value : undefined })
  })
  const index = images.findIndex(item => item.key === 'image-' + position)
  previewItems.value = index >= 0 ? images : [{ key: 'current', name: name.value, blob: blob.value }]
  previewIndex.value = index >= 0 ? index : 0; previewOpen.value = true
}
watch([() => props.node.attrs.attachmentId, () => props.node.attrs.data, () => context.value.requirementId], async () => {
  stopVisibility?.(); previewOpen.value = false; codeOpen.value=false;metadata.value={}; ++metadataVersion; ++version; releasePreview(); error.value = ''; loading.value = false
  const current = version
  await nextTick()
  if (disposed || current !== version || !visibilityAnchor.value) return
  stopVisibility = observeVisibleAsset(visibilityAnchor.value, () => {
    void loadMetadata()
    if ((image.value || pending.value) && !loading.value && !blob.value) void load()
  })
}, { immediate: true })
function metadataChanged(event:Event){const detail=(event as CustomEvent).detail;if(detail?.requirementId===context.value.requirementId&&detail?.attachmentId===props.node.attrs.attachmentId)void loadMetadata()}
onMounted(()=>window.addEventListener('devflow-asset-classified',metadataChanged))
onBeforeUnmount(() => { stopVisibility?.(); if(typeof window!=='undefined')window.removeEventListener('devflow-asset-classified',metadataChanged);disposed = true; version++; releasePreview(); for (const url of downloadURLs) URL.revokeObjectURL(url); downloadURLs.clear() })
</script>

<template>
  <NodeViewWrapper class="rich-asset" :class="{ 'rich-asset-image': image, 'is-selected': selected }" contenteditable="false">
    <span ref="visibilityAnchor" class="rich-asset-visibility" aria-hidden="true"></span>
    <button v-if="imageURL" type="button" class="rich-image-frame" :aria-label="t('放大预览 {name}',{name})" @click="openPreview"><img :src="imageURL" :alt="String(node.attrs.alt || name)" draggable="false" loading="lazy"></button>
    <div v-else-if="image && loading" class="rich-image-placeholder" role="status">{{ t('正在载入图片…') }}</div>
    <button v-else-if="image && !error" type="button" class="rich-image-placeholder" @click="load">{{ t('查看图片') }}</button>
    <div class="rich-asset-caption"><AssetIcon :category="category" :language="language"/><span class="rich-asset-name">{{ name }}<small v-if="pending">{{ t('待保存') }}</small></span><select v-if="pending&&!context.readonly" :disabled="context.disabled" :value="category" :aria-label="t('附件资产分类')" @change="changeCategory"><option v-for="item in assetCategories" :key="item.value" :value="item.value">{{t(item.label)}}</option></select><small v-else>{{t(assetLabel(category))}}</small><button v-if="!image&&(language||['code','api'].includes(category))" type="button" @click="codeOpen=!codeOpen">{{t(codeOpen?'收起代码':language?'代码预览':'文本预览')}}</button><button type="button" :disabled="loading" @click="download">{{ t('下载') }}</button><button v-if="!context.readonly" type="button" :disabled="context.disabled" :aria-label="t('从正文移除附件')" @click="remove">{{ t('移除') }}</button></div>
    <p v-if="error" class="rich-asset-error" role="alert">{{ t(error) }} <button type="button" :disabled="loading" @click="load">{{ t('重试') }}</button></p>
    <AssetCodePreview v-if="codeOpen" :name="name" :requirement-id="context.requirementId" :attachment-id="node.attrs.attachmentId" :data="node.attrs.data" @close="codeOpen=false"/>
    <ImagePreview v-model:open="previewOpen" :items="previewItems" :initial-index="previewIndex" :requirement-id="context.requirementId" />
  </NodeViewWrapper>
</template>

<style scoped>
.rich-asset{margin:16px 0;border:1px solid #e1e5ed;border-radius:9px;overflow:hidden;background:#fbfcfe;max-width:100%;user-select:none}.rich-asset.is-selected{outline:2px solid #7b73e9;outline-offset:2px}.rich-image-frame{display:flex;justify-content:center;background:#f5f7fb;border-bottom:1px solid #e7eaf0}.rich-image-frame img{display:block;max-width:100%;max-height:640px;width:auto;height:auto;object-fit:contain}.rich-image-placeholder{padding:32px;text-align:center;color:#8b94a5;font-size:12px}.rich-asset-caption{display:flex;flex-wrap:wrap;align-items:center;gap:10px;padding:10px 12px;font-size:12px}.rich-asset-icon{font-size:20px;color:#7068dc}.rich-asset-name{flex:1;min-width:0;overflow-wrap:anywhere;color:#475467}.rich-asset-name small{margin-left:7px;font-size:10px;color:#a66c13;background:#fff2d7;border-radius:4px;padding:2px 5px}.rich-asset button{border:0;background:transparent;color:#655edb;padding:4px;font-size:11px;flex:none}.rich-asset button:disabled{opacity:.5;cursor:not-allowed}.rich-asset-error{padding:0 12px 10px;margin:0;color:#b42318;font-size:11px;line-height:1.6}
</style>

<style scoped>
.rich-asset .rich-image-frame{display:flex;width:100%;padding:0;border:0;border-bottom:1px solid #e7eaf0;border-radius:0;cursor:zoom-in}
.rich-asset-visibility{display:block;width:100%;height:1px}.rich-asset>button.rich-image-placeholder{display:block;width:100%;min-height:100px;padding:28px 12px;background:var(--surface-muted,#f5f7fb);font-size:12px}
</style>

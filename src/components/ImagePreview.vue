<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { apiDownload } from '../api'
import { t } from '../i18n'
import { base64ToBytes, MAX_RICH_FILE_BYTES, richImageType } from '../richText'

type PreviewImage = { key: string; name: string; attachmentId?: number | null; data?: string | null; blob?: Blob | null }
const props = defineProps<{ open: boolean; items: PreviewImage[]; requirementId?: number; initialIndex?: number }>()
const emit = defineEmits<{ (event: 'update:open', value: boolean): void }>()
const dialog = ref<HTMLDialogElement | null>(null), closeButton = ref<HTMLButtonElement | null>(null)
const index = ref(0), shown = ref(false), loading = ref(false), error = ref(''), imageURL = ref(''), actualSize = ref(false)
const current = computed(() => props.items[index.value])
let version = 0, disposed = false, observer: MutationObserver | null = null, previousFocus: HTMLElement | null = null
function release() { ++version; if (imageURL.value) URL.revokeObjectURL(imageURL.value); imageURL.value = ''; loading.value = false }
function blocked() { return !!dialog.value?.parentElement?.closest('[inert], [hidden]') }
function hide() {
  shown.value = false; release(); error.value = ''; actualSize.value = false
  if (dialog.value?.open) dialog.value.close()
  if (previousFocus?.isConnected && !previousFocus.closest('[inert], [hidden]')) previousFocus.focus({ preventScroll: true })
  previousFocus = null
}
function close() { if (!shown.value && !props.open) return; hide(); emit('update:open', false) }
async function loadCurrent() {
  release(); error.value = ''; actualSize.value = false
  const item = current.value, requirementId = props.requirementId, request = version
  if (!shown.value || !item || blocked()) return
  loading.value = true
  try {
    let blob: Blob
    if (item.blob instanceof Blob) blob = item.blob
    else if (typeof item.data === 'string' && item.data) {
      if (item.data.length * 3 / 4 > MAX_RICH_FILE_BYTES + 2) throw new Error('单个文件不能超过 10 MiB')
      blob = new Blob([base64ToBytes(item.data) as BlobPart])
    } else {
      if (!requirementId || !Number.isSafeInteger(item.attachmentId) || Number(item.attachmentId) < 1) throw new Error('附件暂不可用，请保存后重新打开需求')
      blob = await apiDownload('/requirements/' + requirementId + '/attachments/' + item.attachmentId)
    }
    if (disposed || request !== version || !shown.value || requirementId !== props.requirementId || current.value?.key !== item.key || blocked()) return
    if (blob.size > MAX_RICH_FILE_BYTES) throw new Error('单个文件不能超过 10 MiB')
    const mime = richImageType(new Uint8Array(await blob.slice(0, 16).arrayBuffer()))
    if (disposed || request !== version || !shown.value || requirementId !== props.requirementId || blocked()) return
    if (!mime) throw new Error('此附件不是可预览的 PNG、JPEG 或 GIF 图片')
    imageURL.value = URL.createObjectURL(new Blob([blob], { type: mime }))
  } catch (cause) { if (!disposed && request === version && shown.value) error.value = cause instanceof Error ? cause.message : '附件载入失败，请重试' }
  finally { if (!disposed && request === version) loading.value = false }
}
async function show() {
  await nextTick()
  if (disposed || !props.open || !dialog.value || shown.value) return
  if (blocked() || !props.items.length) { emit('update:open', false); return }
  index.value = Math.max(0, Math.min(props.items.length - 1, props.initialIndex || 0))
  previousFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null
  try { dialog.value.showModal(); shown.value = true; closeButton.value?.focus({ preventScroll: true }); void loadCurrent() }
  catch { hide(); emit('update:open', false) }
}
function step(change: number) {
  if (!shown.value || blocked()) return
  const next = Math.max(0, Math.min(props.items.length - 1, index.value + change))
  if (next !== index.value) { index.value = next; void loadCurrent() }
}
function onKeydown(event: KeyboardEvent) {
  if (!shown.value) return
  event.stopPropagation()
  if (['Escape', 'ArrowLeft', 'ArrowRight'].includes(event.key)) {
    event.preventDefault(); event.stopImmediatePropagation()
    if (event.key === 'Escape') close()
    else step(event.key === 'ArrowLeft' ? -1 : 1)
  }
}
function onCancel(event: Event) { event.preventDefault(); event.stopImmediatePropagation(); close() }
function onNativeClose() { if (shown.value) close() }
function checkProtection() { if (shown.value && blocked()) close() }
function imageFailed() { error.value = '图片无法解码，请下载文件检查'; release() }
watch(() => props.open, value => { if (value) void show(); else hide() }, { flush: 'post' })
watch(() => props.requirementId, () => { if (shown.value || props.open) close() })
watch(() => props.items, (items, previous) => {
  if (!shown.value) return
  const key = previous[index.value]?.key, next = items.findIndex(item => item.key === key)
  if (next < 0) close(); else { index.value = next; void loadCurrent() }
})
onMounted(() => {
  observer = new MutationObserver(checkProtection)
  observer.observe(document.documentElement, { subtree: true, attributes: true, attributeFilter: ['inert', 'hidden'] })
  window.addEventListener('devflow-identity-changed', close); window.addEventListener('devflow-auth-expired', close)
  if (props.open) void show()
})
onBeforeUnmount(() => { disposed = true; observer?.disconnect(); window.removeEventListener('devflow-identity-changed', close); window.removeEventListener('devflow-auth-expired', close); hide() })
</script>

<template>
  <dialog ref="dialog" class="image-preview" :aria-label="t('图片预览')" @keydown="onKeydown" @cancel="onCancel" @close="onNativeClose" @click.self="close">
    <header class="preview-header"><div><h2>{{ current?.name || t('图片预览') }}</h2><span aria-live="polite">{{ index + 1 }} / {{ items.length }}</span></div><div class="preview-actions"><button type="button" :disabled="!imageURL || !!error" :aria-pressed="actualSize" @click="actualSize = !actualSize">{{ actualSize ? t('适应窗口') : t('实际大小') }}</button><button ref="closeButton" type="button" class="preview-close" :aria-label="t('关闭图片预览')" @click="close">×</button></div></header>
    <section class="preview-stage" :class="{ 'actual-size': actualSize }" @click.self="close"><div v-if="loading" class="preview-state" role="status">{{ t('正在载入图片…') }}</div><div v-else-if="error" class="preview-state" role="alert"><p>{{ t(error) }}</p><button type="button" @click="loadCurrent">{{ t('重试') }}</button></div><div v-else-if="imageURL" class="preview-image-wrap" @click.self="close"><img :src="imageURL" :alt="current?.name || t('图片预览')" draggable="false" @error="imageFailed"></div></section>
    <footer class="preview-footer"><button type="button" :disabled="index === 0" @click="step(-1)">← {{ t('上一张') }}</button><span>{{ t('← → 切换图片 · Esc 关闭预览') }}</span><button type="button" :disabled="index >= items.length - 1" @click="step(1)">{{ t('下一张') }} →</button></footer>
  </dialog>
</template>

<style scoped>
.image-preview{position:fixed;inset:0;z-index:1000;width:calc(100vw - 48px);height:calc(100dvh - 48px);max-width:1600px;max-height:none;margin:auto;padding:0;border:1px solid #ffffff24;border-radius:14px;background:#111827;color:#f4f6fb;overflow:hidden;box-shadow:0 30px 100px #0008}.image-preview[open]{display:flex;flex-direction:column}.image-preview::backdrop{background:#0a1125d9;backdrop-filter:blur(5px)}.preview-header,.preview-footer{display:flex;align-items:center;justify-content:space-between;gap:20px;flex:none;padding:15px 22px;background:#182133}.preview-header>div:first-child{display:flex;align-items:center;gap:15px;min-width:0}.preview-header h2{margin:0;font-size:14px;line-height:1.5;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;color:#f4f6fb}.preview-header span,.preview-footer span{color:#a8b1c3;font-size:11px;white-space:nowrap}.preview-actions{display:flex;align-items:center;gap:12px;flex:none}.image-preview button{border:1px solid #ffffff28;background:#ffffff0b;color:#f4f6fb;border-radius:6px;padding:7px 11px;font-size:12px;cursor:pointer}.image-preview button:hover{background:#ffffff19}.image-preview button:focus-visible{outline:2px solid #beb5ff;outline-offset:3px}.image-preview button:disabled{opacity:.35;cursor:not-allowed}.image-preview .preview-close{font-size:25px;line-height:24px;width:36px;height:36px;padding:0;border-color:transparent}.preview-stage{flex:1;min-height:0;min-width:0;overflow:auto;position:relative;padding:16px}.preview-image-wrap{display:flex;align-items:center;justify-content:center;width:100%;height:100%}.preview-image-wrap img{display:block;max-width:100%;max-height:100%;object-fit:contain;box-shadow:0 8px 28px #0004}.actual-size .preview-image-wrap{display:block;height:auto;min-height:100%}.actual-size .preview-image-wrap img{max-width:none;max-height:none;margin:auto;width:auto;height:auto}.preview-state{height:100%;display:flex;flex-direction:column;align-items:center;justify-content:center;gap:12px;font-size:13px;color:#c0c8d8;text-align:center}.preview-state p{color:inherit;margin:0;line-height:1.7}.preview-footer{padding:12px 22px;border-top:1px solid #ffffff0d}@media(max-width:700px){.image-preview{width:100vw;height:100dvh;border:0;border-radius:0}.preview-header,.preview-footer{padding:12px}.preview-footer span{display:none}.preview-header h2{font-size:12px}.preview-header>div:first-child{gap:9px}.preview-stage{padding:8px}}
</style>
<style scoped>
@media(max-width:700px){
 .preview-header{gap:10px;padding-top:max(12px,env(safe-area-inset-top))}
 .preview-header>div:first-child{flex:1}.preview-header>div:first-child>span{flex:none}
 .preview-actions{gap:6px}.image-preview button{min-height:44px}.image-preview .preview-close{width:44px;height:44px;flex:none}
 .preview-footer{padding-bottom:max(12px,env(safe-area-inset-bottom))}
}
</style>

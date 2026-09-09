<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, onUpdated, ref, useId, watch } from 'vue'
import { t } from '../i18n'
import { layoutScope } from '../layoutScope'
import { splitBounds, splitKeyboardWidth, splitPointerWidth, splitStorageKey, splitWidth } from '../splitLayout'

const props = withDefaults(defineProps<{ label: string; projectId: string; scene: string; initialWidth?: number; minMainWidth?: number; minAsideWidth?: number; maxAsideWidth?: number; breakpoint?: number; disabled?: boolean; showAside?: boolean }>(), { initialWidth: 360, minMainWidth: 360, minAsideWidth: 300, maxAsideWidth: 640, breakpoint: 820, disabled: false, showAside: true })
const emit = defineEmits<{ (event: 'resize', value: { asideWidth: number; mobile: boolean }): void }>()
const root = ref<HTMLElement | null>(null), containerWidth = ref(0), preferred = ref(props.initialWidth), dragging = ref(false), invalidated = ref(false)
const instance = useId(), asideId = 'split-aside-' + instance
const bounds = computed(() => splitBounds(containerWidth.value, props.minMainWidth, props.minAsideWidth, props.maxAsideWidth, props.breakpoint))
const asideWidth = computed(() => splitWidth(preferred.value, bounds.value, props.initialWidth))
const mobile = computed(() => bounds.value.mobile)
const cacheKey = computed(() => splitStorageKey(layoutScope.value, props.projectId, props.scene))
const gridStyle = computed(() => ({ gridTemplateColumns: !props.showAside || mobile.value ? 'minmax(0,1fr)' : `minmax(0,1fr) ${bounds.value.separator}px minmax(0,${asideWidth.value}px)` }))
type Drag = { id: number; startX: number; width: number; preference: number; handle: HTMLElement; scope: string | null; observer: MutationObserver | null }
let drag: Drag | null = null, resizeObserver: ResizeObserver | null = null, disposed = false
const identityEvents = ['devflow-auth-expired', 'devflow-identity-changed', 'devflow-account-disabled', 'devflow-password-change-required', 'devflow-project-changed']

function restorePreference() {
  endDrag(); invalidated.value = false
  let next = props.initialWidth
  try { const stored = cacheKey.value ? JSON.parse(localStorage.getItem(cacheKey.value) || 'null') : null; if (typeof stored === 'number' && Number.isFinite(stored) && stored > 0) next = stored } catch { /* 非敏感布局缓存损坏时仅恢复默认，不影响正文草稿。 */ }
  preferred.value = next
}
function setWidth(value: number) {
  preferred.value = splitWidth(value, bounds.value, props.initialWidth)
  const key = cacheKey.value
  if (key && !invalidated.value) { try { localStorage.setItem(key, JSON.stringify(preferred.value)) } catch { /* 隐私模式/配额不足只放弃记忆。 */ } }
}
function available() {
  const element = root.value
  return !disposed && !props.disabled && !invalidated.value && props.showAside && bounds.value.resizable && !!element && !element.closest('[inert],[hidden]') && element.getClientRects().length > 0
}
function endDrag() {
  const active = drag; if (!active) return
  drag = null; dragging.value = false; active.observer?.disconnect()
  window.removeEventListener('pointermove', pointerMove); window.removeEventListener('pointerup', pointerEnd); window.removeEventListener('pointercancel', pointerEnd); window.removeEventListener('blur', endDrag); window.removeEventListener('keydown', cancelEscape, true)
  active.handle.removeEventListener('lostpointercapture', pointerEnd)
  try { if (active.handle.hasPointerCapture?.(active.id)) active.handle.releasePointerCapture(active.id) } catch { /* pointercancel 可能已释放捕获。 */ }
}
function cancelEscape(event: KeyboardEvent) {
  if (event.key !== 'Escape' || !drag) return
  event.preventDefault(); event.stopImmediatePropagation()
  const original = drag.preference; endDrag(); setWidth(original)
}
function pointerEnd(event: PointerEvent) { if (event.pointerId === drag?.id) endDrag() }
function pointerMove(event: PointerEvent) {
  if (!drag || event.pointerId !== drag.id) return
  if (!available() || event.buttons === 0 || drag.scope !== cacheKey.value) { endDrag(); return }
  event.preventDefault(); setWidth(splitPointerWidth(drag.width, drag.startX, event.clientX, bounds.value))
}
function checkAvailability() { if (drag && !available()) endDrag() }
function startPointer(event: PointerEvent) {
  if (!available() || event.button !== 0 || event.isPrimary === false || !Number.isFinite(event.clientX)) return
  const handle = event.currentTarget as HTMLElement | null; if (!handle) return
  event.preventDefault(); event.stopPropagation(); endDrag()
  drag = { id: event.pointerId, startX: event.clientX, width: asideWidth.value, preference: preferred.value, handle, scope: cacheKey.value, observer: null }; dragging.value = true
  window.addEventListener('pointermove', pointerMove, { passive: false }); window.addEventListener('pointerup', pointerEnd); window.addEventListener('pointercancel', pointerEnd); window.addEventListener('blur', endDrag); window.addEventListener('keydown', cancelEscape, true)
  handle.addEventListener('lostpointercapture', pointerEnd)
  try { handle.setPointerCapture(event.pointerId) } catch { /* 窗口级监听器作为浏览器捕获不可用时的兜底。 */ }
  handle.focus({ preventScroll: true })
  if (drag && typeof MutationObserver !== 'undefined') {
    const observer = new MutationObserver(checkAvailability); drag.observer = observer
    for (let node: HTMLElement | null = root.value; node; node = node.parentElement) observer.observe(node, { attributes: true, attributeFilter: ['inert', 'hidden', 'style', 'class'] })
  }
}
function keydown(event: KeyboardEvent) {
  if (!available() || event.altKey || event.ctrlKey || event.metaKey) return
  const next = splitKeyboardWidth(asideWidth.value, event.key, bounds.value, event.shiftKey); if (next === null) return
  event.preventDefault(); event.stopPropagation(); endDrag(); setWidth(next)
}
function resetWidth() { if (available()) { endDrag(); setWidth(props.initialWidth) } }
function measure() {
  if (disposed || !root.value) return
  const next = Math.max(0, Math.floor(root.value.getBoundingClientRect().width))
  if (next !== containerWidth.value) { endDrag(); containerWidth.value = next }
  // 只重算展示限幅，不回写桌面偏好。缩小后再展开容器可恢复用户原先宽度。
}
function invalidate() { invalidated.value = true; endDrag() }
watch(cacheKey, restorePreference, { flush: 'sync', immediate: true })
watch(() => [props.showAside, props.disabled, props.minMainWidth, props.minAsideWidth, props.maxAsideWidth, props.breakpoint], checkAvailability, { flush: 'sync' })
watch([asideWidth, mobile], () => emit('resize', { asideWidth: asideWidth.value, mobile: mobile.value }), { immediate: true })
onMounted(() => {
  measure(); if (typeof ResizeObserver !== 'undefined' && root.value) { resizeObserver = new ResizeObserver(measure); resizeObserver.observe(root.value) }
  window.addEventListener('resize', measure)
  for (const event of identityEvents) window.addEventListener(event, invalidate)
})
onUpdated(() => { measure(); checkAvailability() })
onBeforeUnmount(() => { disposed = true; endDrag(); resizeObserver?.disconnect(); window.removeEventListener('resize', measure); for (const event of identityEvents) window.removeEventListener(event, invalidate) })
defineExpose({ asideWidth, mobile, dragging, resetWidth, cancelResize: endDrag })
</script>

<template>
  <div ref="root" class="resizable-split" :class="{ 'split-stacked': mobile, 'split-dragging': dragging, 'split-without-aside': !showAside }" :style="gridStyle">
    <div class="split-main"><slot name="main" /></div>
    <div v-if="showAside && bounds.resizable" class="split-separator" role="separator" tabindex="0" aria-orientation="vertical" :aria-label="label" :aria-controls="asideId" :aria-disabled="disabled || invalidated" :aria-valuemin="bounds.min" :aria-valuemax="bounds.max" :aria-valuenow="asideWidth" :aria-valuetext="asideWidth+' px'" :title="t('拖动调整字段区宽度；双击恢复默认')" @pointerdown="startPointer" @keydown="keydown" @dblclick.stop.prevent="resetWidth"><span aria-hidden="true"></span></div>
    <div v-show="showAside" :id="asideId" class="split-aside"><slot name="aside" /></div>
  </div>
</template>

<style scoped>
.resizable-split{display:grid;flex:1;width:100%;max-width:100%;min-width:0;min-height:0;overflow:hidden;position:relative;background:var(--surface,#fff)}
.split-main,.split-aside{min-width:0;min-height:0;max-width:100%;overflow-y:auto;overflow-x:hidden;overscroll-behavior:contain;overflow-wrap:anywhere;scrollbar-gutter:stable}
.split-main{grid-column:1}.split-aside{grid-column:3;background:var(--surface-subtle,#f7f9fc)}
.split-main :slotted(*),.split-aside :slotted(*){min-width:0;max-width:100%;box-sizing:border-box}
.split-separator{grid-column:2;grid-row:1;position:relative;z-index:2;width:9px;min-width:0;cursor:col-resize;touch-action:none;user-select:none;background:var(--surface-subtle,#f7f9fc);outline:none}
.split-separator:before{content:'';position:absolute;top:0;bottom:0;left:4px;width:1px;background:var(--line,#d9e0ea);transition:background .12s,width .12s,left .12s}
.split-separator span{position:absolute;top:45%;left:2px;width:5px;height:38px;border-radius:5px;background:var(--muted,#8b98ae);opacity:.4}
.split-separator:hover:before,.split-separator:focus-visible:before,.split-dragging .split-separator:before{left:2px;width:5px;background:var(--control-selected,#3370eb)}
.split-separator:hover span,.split-separator:focus-visible span,.split-dragging .split-separator span{background:var(--control-selected,#3370eb);opacity:1}
.split-separator:focus-visible{box-shadow:inset 0 0 0 1px var(--control-selected,#3370eb)}
.split-separator[aria-disabled=true]{cursor:default;opacity:.45}.split-dragging{user-select:none;cursor:col-resize}
.split-stacked{display:block;overflow-y:auto;overflow-x:hidden}.split-stacked>.split-main,.split-stacked>.split-aside{overflow:visible;height:auto;max-height:none;scrollbar-gutter:auto}.split-stacked>.split-aside{border-top:1px solid var(--line,#d9e0ea)}
@media(prefers-reduced-motion:reduce){.split-separator:before{transition:none}}
</style>

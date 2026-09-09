<script lang="ts">
// 全站同一时刻仅一个抽屉拥有拖拽状态；接管前先恢复上一抽屉修改的全局样式。
// 此组件只处理布局，账号隔离缓存由 layoutScope 提供，不能存入需求正文等业务数据。
let cancelActiveDrawerDrag: (() => void) | null = null
</script>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, onUpdated, ref, useId, watch } from 'vue'
import { t } from '../i18n'
import { clampDrawerWidth, drawerBounds, drawerWidthFromKey, drawerWidthFromPointer, preferredDrawerWidth } from '../drawerLayout'
import { layoutScope, readLayoutWidth, writeLayoutWidth } from '../layoutScope'

const props = withDefaults(defineProps<{
  label: string
  width?: number
  initialWidth?: number
  minWidth?: number
  maxViewportRatio?: number
  storageKey?: string
}>(), { initialWidth: 1240, minWidth: 860, maxViewportRatio: .96 })
const emit = defineEmits<{ (event: 'update:width', width: number): void }>()
const drawer = ref<HTMLElement | null>(null)
const instanceID = useId()
const viewportWidth = ref(typeof window === 'undefined' ? 1280 : window.innerWidth)
const preferredWidth = ref(readLayoutWidth(props.storageKey, preferredDrawerWidth(props.width, props.initialWidth)))
const bounds = computed(() => drawerBounds(viewportWidth.value, props.minWidth, props.maxViewportRatio))
// preferredWidth 记住桌面偏好，actualWidth 仅按当前视口限幅。
// 手机全宽展示不会覆盖用户原来的桌面宽度；移动端是否可拖由 drawerBounds 决定。
const actualWidth = computed(() => clampDrawerWidth(preferredWidth.value, bounds.value, props.initialWidth))
const dragging = ref(false)
type SavedStyle = { value: string; priority: string }
type Drag = {
  id: number; startX: number; startWidth: number; initialPreference: number
  handle: HTMLElement; body: HTMLElement; cursor: SavedStyle; selection: SavedStyle
  observer: MutationObserver | null
}
let drag: Drag | null = null

function setWidth(width: number) {
  const next = clampDrawerWidth(width, bounds.value, props.initialWidth)
  if (preferredWidth.value === next) return
  preferredWidth.value = next
  writeLayoutWidth(props.storageKey, next)
  emit('update:width', next)
}
function resetWidth() {
  if (!available()) return
  endDrag()
  setWidth(props.initialWidth)
}
function available() {
  // 隐藏或因身份冲突变为 inert 的抽屉不可继续拖拽，包括 v-show 未卸载的情形。
  const element = drawer.value
  return bounds.value.resizable && !!element && !element.closest('[inert], [hidden]') && element.getClientRects().length > 0
}
function savedStyle(style: CSSStyleDeclaration, property: string): SavedStyle {
  return { value: style.getPropertyValue(property), priority: style.getPropertyPriority(property) }
}
function restoreStyle(style: CSSStyleDeclaration, property: string, saved: SavedStyle, ownedValue: string) {
  // 仅恢复本次拖拽仍拥有的样式，不能覆盖期间其他弹窗设置的新样式。
  if (style.getPropertyValue(property) !== ownedValue || style.getPropertyPriority(property) !== 'important') return
  if (saved.value) style.setProperty(property, saved.value, saved.priority)
  else style.removeProperty(property)
}
function endDrag() {
  // pointerup/cancel、失焦、Esc、窗口缩放和组件卸载共用同一清理路径。
  // 若遗漏监听器或 body 光标恢复，会导致关闭面板后整页仍不可正常选字。
  const previous = drag
  if (!previous) return
  drag = null
  dragging.value = false
  previous.observer?.disconnect()
  window.removeEventListener('pointermove', movePointer)
  window.removeEventListener('pointerup', finishPointer)
  window.removeEventListener('pointercancel', finishPointer)
  window.removeEventListener('blur', endDrag)
  window.removeEventListener('keydown', cancelWithEscape, true)
  previous.handle.removeEventListener('lostpointercapture', finishPointer)
  try {
    if (previous.handle.hasPointerCapture?.(previous.id)) previous.handle.releasePointerCapture(previous.id)
  } catch { /* The browser may already have cancelled capture. */ }
  restoreStyle(previous.body.style, 'cursor', previous.cursor, 'col-resize')
  restoreStyle(previous.body.style, 'user-select', previous.selection, 'none')
  if (cancelActiveDrawerDrag === endDrag) cancelActiveDrawerDrag = null
}
function cancelWithEscape(event: KeyboardEvent) {
  if (event.key !== 'Escape' || !drag) return
  const original = drag.initialPreference
  event.preventDefault()
  event.stopImmediatePropagation()
  endDrag()
  setWidth(original)
}
function finishPointer(event: PointerEvent) { if (drag && event.pointerId === drag.id) endDrag() }
function checkAvailability() { if (drag && !available()) endDrag() }
function movePointer(event: PointerEvent) {
  if (!drag || event.pointerId !== drag.id) return
  if (!available() || event.buttons === 0) { endDrag(); return }
  event.preventDefault()
  setWidth(drawerWidthFromPointer(drag.startWidth, drag.startX, event.clientX, bounds.value))
}
function startPointer(event: PointerEvent) {
  if (event.button !== 0 || event.isPrimary === false || !available()) return
  const handle = event.currentTarget as HTMLElement | null
  if (!handle || !Number.isFinite(event.clientX)) return
  event.preventDefault()
  event.stopPropagation()
  cancelActiveDrawerDrag?.()
  const body = handle.ownerDocument.body
  drag = {
    id: event.pointerId, startX: event.clientX, startWidth: actualWidth.value, initialPreference: preferredWidth.value,
    handle, body, cursor: savedStyle(body.style, 'cursor'), selection: savedStyle(body.style, 'user-select'), observer: null,
  }
  dragging.value = true
  cancelActiveDrawerDrag = endDrag
  body.style.setProperty('cursor', 'col-resize', 'important')
  body.style.setProperty('user-select', 'none', 'important')
  window.addEventListener('pointermove', movePointer, { passive: false })
  window.addEventListener('pointerup', finishPointer)
  window.addEventListener('pointercancel', finishPointer)
  window.addEventListener('blur', endDrag)
  window.addEventListener('keydown', cancelWithEscape, true)
  handle.addEventListener('lostpointercapture', finishPointer)
  const startedDrag = drag
  try { handle.setPointerCapture(event.pointerId) } catch { /* Window listeners provide a safe fallback. */ }
  if (drag !== startedDrag) return
  handle.focus({ preventScroll: true })
  if (drag !== startedDrag) return
  // 只在拖拽期间观察祖先：v-show、inert 或外部遮罩可能在未卸载时改变可用性。
  if (typeof MutationObserver !== 'undefined') {
    const observer = new MutationObserver(checkAvailability)
    drag.observer = observer
    for (let element: HTMLElement | null = drawer.value; element; element = element.parentElement) {
      observer.observe(element, { attributes: true, attributeFilter: ['inert', 'hidden', 'style', 'class'] })
    }
  }
}
function keydown(event: KeyboardEvent) {
  if (!available() || event.altKey || event.ctrlKey || event.metaKey) return
  const next = drawerWidthFromKey(actualWidth.value, event.key, bounds.value, event.shiftKey)
  if (next === null) return
  event.preventDefault()
  event.stopPropagation()
  endDrag()
  setWidth(next)
}
function resizeViewport() {
  // 视口改变后旧拖拽起点已不属于同一坐标系，先结束拖拽再重新限幅。
  endDrag()
  viewportWidth.value = window.innerWidth
}
function restoreLayout() {
  endDrag()
  // 切换账号时只读新 scope 的缓存/默认值，不能把父组件旧 v-model 写入新账号。
  preferredWidth.value = readLayoutWidth(props.storageKey, preferredDrawerWidth(undefined, props.initialWidth))
  emit('update:width', preferredWidth.value)
}
watch([layoutScope, () => props.storageKey], restoreLayout, { flush: 'sync' })
watch(() => props.width, value => { if (value !== undefined) { const next=preferredDrawerWidth(value,props.initialWidth); if(next!==preferredWidth.value){endDrag();preferredWidth.value=next} } })
watch(() => [props.minWidth, props.maxViewportRatio], () => endDrag())
onMounted(() => { resizeViewport(); if (props.storageKey) emit('update:width', preferredWidth.value); window.addEventListener('resize', resizeViewport) })
onUpdated(checkAvailability)
onBeforeUnmount(() => { endDrag(); window.removeEventListener('resize', resizeViewport) })
defineExpose({ actualWidth, resetWidth, cancelResize: endDrag })
</script>

<template>
  <aside :id="'resizable-drawer-'+instanceID" ref="drawer" class="resizable-drawer" :class="{'resizable-drawer--dragging':dragging,'resizable-drawer--mobile':bounds.mobile}" :style="{width:bounds.mobile?'100vw':actualWidth+'px'}" role="dialog" aria-modal="true" :aria-label="label">
    <div v-if="bounds.resizable" class="resizable-drawer-handle" role="separator" tabindex="0" aria-orientation="vertical" :aria-label="t('调整面板宽度')" :aria-valuenow="actualWidth" :aria-valuemin="bounds.min" :aria-valuemax="bounds.max" :aria-valuetext="actualWidth+' px'" :aria-controls="'resizable-drawer-'+instanceID" :aria-describedby="'drawer-resize-help-'+instanceID" :title="t('拖动调整宽度；双击恢复默认')" @pointerdown="startPointer" @keydown="keydown" @dblclick.stop.prevent="resetWidth"><span aria-hidden="true"></span></div>
    <span :id="'drawer-resize-help-'+instanceID" class="drawer-resize-help">{{t('使用左右方向键调整宽度，Home 最窄，End 最宽')}}</span>
    <slot />
  </aside>
</template>

<style scoped>
.resizable-drawer{position:relative;flex:0 0 auto;display:flex;flex-direction:column;min-width:0;max-width:100vw;height:100%;max-height:100%;box-sizing:border-box;background:#fff;box-shadow:-16px 0 46px #18213c20;isolation:isolate}
.resizable-drawer-handle{position:absolute;z-index:20;left:-7px;top:0;bottom:0;width:15px;cursor:col-resize;touch-action:none;user-select:none;outline:none}
.resizable-drawer-handle::before{content:'';position:absolute;left:6px;top:0;bottom:0;width:3px;background:transparent;transition:background-color .12s}
.resizable-drawer-handle>span{position:absolute;left:4px;top:calc(50% - 22px);width:7px;height:44px;border:1px solid #cddbeb;border-radius:5px;background:#f3f8ff;box-shadow:0 2px 7px #16365c0f}
.resizable-drawer-handle:hover::before,.resizable-drawer-handle:focus-visible::before,.resizable-drawer--dragging>.resizable-drawer-handle::before{background:#1677ff}
.resizable-drawer-handle:hover>span,.resizable-drawer-handle:focus-visible>span,.resizable-drawer--dragging>.resizable-drawer-handle>span{border-color:#1677ff;background:#e6f4ff;box-shadow:0 0 0 3px #1677ff25}
.drawer-resize-help{position:absolute;width:1px;height:1px;padding:0;margin:-1px;overflow:hidden;clip-path:inset(50%);white-space:nowrap;border:0}
.resizable-drawer--mobile{width:100vw;max-width:100vw;box-shadow:none}
.resizable-drawer.module-detail-drawer :deep(.detail-main){min-width:0}
.resizable-drawer.module-detail-drawer :deep(.detail-props){flex:0 0 265px}
.resizable-drawer.module-detail-drawer :deep(.drawer-title h2){overflow-wrap:anywhere;min-width:0}
@media(max-width:760px){.resizable-drawer.module-detail-drawer :deep(.drawer-body){flex-direction:column;overflow:auto}.resizable-drawer.module-detail-drawer :deep(.detail-main){overflow:visible;flex:none;padding:18px}.resizable-drawer.module-detail-drawer :deep(.detail-props){width:100%;flex:none;overflow:visible;border-left:0;border-top:1px solid #e7eaf0;padding:18px}.resizable-drawer.module-detail-drawer :deep(.drawer-head){padding:14px 18px}.resizable-drawer.module-detail-drawer :deep(.drawer-title){gap:10px}.resizable-drawer.module-detail-drawer :deep(.drawer-title h2){font-size:18px}.resizable-drawer.module-detail-drawer :deep(.drawer-title button){flex:none}}
@media(prefers-reduced-motion:reduce){.resizable-drawer-handle::before{transition:none}}
</style>

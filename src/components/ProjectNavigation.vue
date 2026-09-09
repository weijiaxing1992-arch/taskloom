<script setup lang="ts">
import { nextTick, ref } from 'vue'
import { t } from '../i18n'
import { moveProjectNavigation, projectNavigationItems, type ProjectNavigationKey, useProjectNavigation } from '../projectNavigation'

const order = useProjectNavigation()
const dragging = ref<ProjectNavigationKey | null>(null)
const over = ref<ProjectNavigationKey | null>(null)
const announcement = ref('')
const buttons = new Map<ProjectNavigationKey, HTMLElement>()
let suppressNavigationUntil = 0
let pointer: { id: number; key: ProjectNavigationKey; startX: number; startY: number; moved: boolean } | null = null

function refItem(key: ProjectNavigationKey, element: unknown) {
  if (element) buttons.set(key, element as HTMLElement)
  else buttons.delete(key)
}
function finishDrag() { dragging.value = null; over.value = null }
function afterReorder(key: ProjectNavigationKey) {
  suppressNavigationUntil = Date.now() + 350
  announcement.value = '项目导航顺序已调整'
  void nextTick(() => buttons.get(key)?.focus({ preventScroll: true }))
}
function reorder(key: ProjectNavigationKey, targetKey: ProjectNavigationKey) {
  const target = order.value.indexOf(targetKey)
  const next = moveProjectNavigation(order.value, key, target)
  if (next.join(',') === order.value.join(',')) return
  order.value = next
  afterReorder(key)
}
function startDrag(event: DragEvent, key: ProjectNavigationKey) {
  // 触控设备由 pointer 逻辑处理，避免浏览器合成 mouse drag 后产生双重排序。
  if (pointer) { event.preventDefault(); return }
  dragging.value = key
  event.dataTransfer?.setData('text/plain', key)
  if (event.dataTransfer) event.dataTransfer.effectAllowed = 'move'
}
function dragOver(event: DragEvent, key: ProjectNavigationKey) {
  if (!dragging.value) return
  event.preventDefault()
  over.value = key
  if (event.dataTransfer) event.dataTransfer.dropEffect = 'move'
}
function drop(event: DragEvent, key: ProjectNavigationKey) {
  const source = dragging.value
  if (!source) return
  event.preventDefault()
  reorder(source, key)
  finishDrag()
}
function finishNativeDrag() { finishDrag() }
function navigate(event: MouseEvent, handler: (event?: MouseEvent) => void) {
  // 拖拽的 mouseup 会在部分浏览器合成 click；排序后短暂吞掉该 click，
  // 取消拖拽没有重排也不会进入此分支，正常链接行为不受影响。
  if (dragging.value || Date.now() < suppressNavigationUntil) {
    event.preventDefault()
    event.stopImmediatePropagation()
    return
  }
  handler(event)
}
function keydown(event: KeyboardEvent, key: ProjectNavigationKey) {
  if (event.key === 'Escape') { finishDrag(); return }
  if (!event.altKey || !['ArrowLeft', 'ArrowRight'].includes(event.key)) return
  event.preventDefault()
  event.stopPropagation()
  const index = order.value.indexOf(key)
  const target = index + (event.key === 'ArrowLeft' ? -1 : 1)
  if (target < 0 || target >= order.value.length) return
  reorder(key, order.value[target]!)
}
function pointerDown(event: PointerEvent, key: ProjectNavigationKey) {
  if (event.pointerType === 'mouse') return
  pointer = { id: event.pointerId, key, startX: event.clientX, startY: event.clientY, moved: false }
  ;(event.currentTarget as HTMLElement).setPointerCapture?.(event.pointerId)
}
function keyAtPointer(event: PointerEvent): ProjectNavigationKey | null {
  const element = document.elementFromPoint(event.clientX, event.clientY)?.closest<HTMLElement>('[data-project-navigation-key]')
  const key = element?.dataset.projectNavigationKey
  return key && Object.hasOwn(projectNavigationItems, key) ? key as ProjectNavigationKey : null
}
function pointerMove(event: PointerEvent) {
  if (!pointer || pointer.id !== event.pointerId) return
  if (!pointer.moved && Math.hypot(event.clientX - pointer.startX, event.clientY - pointer.startY) < 9) return
  pointer.moved = true
  dragging.value = pointer.key
  const target = keyAtPointer(event)
  if (target) over.value = target
  event.preventDefault()
}
function pointerFinish(event: PointerEvent, cancelled = false) {
  const active = pointer
  if (!active || active.id !== event.pointerId) return
  pointer = null
  const target = over.value
  if (!cancelled && active.moved && target) reorder(active.key, target)
  finishDrag()
}
</script>

<template>
  <nav class="project-nav project-navigation" :aria-label="t('项目协作')" :title="t('拖动项目导航调整顺序，或按 Alt + ← / →')">
    <span class="project-nav-label">{{ t('项目协作') }}</span>
    <span id="project-navigation-help" class="sr-only">{{ t('拖动项目导航调整顺序，或按 Alt + ← / →') }}</span>
    <RouterLink v-for="key in order" :key="key" :to="projectNavigationItems[key].path" custom v-slot="{ href, navigate: routeNavigate, isActive }">
      <a
        :ref="element => refItem(key, element)"
        :href="href"
        :class="{ 'router-link-active': isActive, dragging: dragging === key, 'drop-target': over === key && dragging !== key }"
        :data-project-navigation-key="key"
        :aria-label="t(projectNavigationItems[key].label)"
        aria-describedby="project-navigation-help"
        aria-keyshortcuts="Alt+ArrowLeft Alt+ArrowRight"
        :aria-grabbed="dragging === key ? 'true' : 'false'"
        draggable="true"
        @click="navigate($event, routeNavigate)"
        @dragstart="startDrag($event, key)"
        @dragover="dragOver($event, key)"
        @drop="drop($event, key)"
        @dragend="finishNativeDrag"
        @keydown="keydown($event, key)"
        @pointerdown="pointerDown($event, key)"
        @pointermove="pointerMove"
        @pointerup="pointerFinish"
        @pointercancel="pointerFinish($event, true)"
      ><span class="project-navigation-grip" aria-hidden="true">⠿</span>{{ t(projectNavigationItems[key].label) }}</a>
    </RouterLink>
    <span class="sr-only" role="status" aria-live="polite">{{ t(announcement) }}</span>
  </nav>
</template>

<style scoped>
.project-navigation{min-width:0;overflow-x:auto;scrollbar-width:thin;overscroll-behavior-x:contain}
.project-navigation a{flex:none;user-select:none;touch-action:pan-y;cursor:grab}
.project-navigation a:active{cursor:grabbing}
.project-navigation a.dragging{opacity:.48}
.project-navigation a.drop-target{box-shadow:inset 3px 0 0 var(--primary);background:var(--primary-soft)}
.project-navigation-grip{margin-right:5px;font-size:12px;line-height:1;opacity:.42}
.sr-only{position:absolute;width:1px;height:1px;padding:0;overflow:hidden;clip:rect(0,0,0,0);white-space:nowrap;border:0}
@media (max-width:1024px){.project-navigation-grip{display:none}.project-navigation a{min-height:40px}}
</style>

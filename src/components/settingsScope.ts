import { nextTick, onBeforeUnmount, onMounted, ref, watch, type Ref } from 'vue'
import { api } from '../api'

/**
 * 设置草稿固定属于打开时的项目和账号上下文，不随全局项目选择器“搬家”。
 * 身份/项目/禁用事件会永久锁定本实例并中止请求；切换后应重新挂载页面。
 * 这是前端防串稿措施，不代替服务端的项目、身份与写权限校验。
 */
export function useSettingsScope() {
  const project = localStorage.getItem('devflow-project') || 'prj_orbit'
  const locked = ref(false), controller = new AbortController()
  let disposed = false
  function invalidate() { locked.value = true; controller.abort() }
  function current() {
    if ((localStorage.getItem('devflow-project') || 'prj_orbit') !== project) invalidate()
    return !disposed && !locked.value
  }
  function storage(event: StorageEvent) { if (event.key === 'devflow-project') current() }
  window.addEventListener('devflow-identity-changed', invalidate)
  window.addEventListener('devflow-auth-expired', invalidate)
  window.addEventListener('devflow-account-disabled', invalidate)
  window.addEventListener('devflow-project-changed', invalidate)
  window.addEventListener('storage', storage)
  onBeforeUnmount(() => {
    disposed = true; controller.abort()
    window.removeEventListener('devflow-identity-changed', invalidate)
    window.removeEventListener('devflow-auth-expired', invalidate)
    window.removeEventListener('devflow-account-disabled', invalidate)
    window.removeEventListener('devflow-project-changed', invalidate)
    window.removeEventListener('storage', storage)
  })
  async function request<T>(path: string, options: RequestInit = {}): Promise<T> {
    // 请求前后都检查上下文：AbortController 不能保证已到达服务端的操作撤销，
    // 但旧响应绝不能覆盖新账号/新项目界面；请求头始终绑定原项目。
    if (!current()) throw new Error('项目或账号已变化，请刷新页面后继续')
    const headers = new Headers(options.headers); headers.set('X-DevFlow-Project', project)
    const result = await api<T>(path, { ...options, headers, signal: controller.signal })
    if (!current()) throw new Error('项目或账号已变化，请刷新页面后继续')
    return result
  }
  return { locked, current, request, project }
}

/**
 * 设置弹窗保留在 App 的 inert 保护树内，并管理 Esc、Tab 循环和焦点归还。
 * close 由业务页面提供：保存中禁止关闭、脏草稿确认均不能在此绕过。
 */
export function useSettingsDialog(open: Readonly<Ref<boolean>>, close: () => unknown) {
  const element = ref<HTMLElement | null>(null)
  let previous: HTMLElement | null = null, disposed = false
  const controls = () => [...(element.value?.querySelectorAll<HTMLElement>('button:not(:disabled), input:not(:disabled), select:not(:disabled), textarea:not(:disabled), [tabindex="0"]') || [])].filter(item => !item.closest('[hidden]') && item.getClientRects().length)
  watch(open, async value => {
    if (value) { previous = document.activeElement instanceof HTMLElement ? document.activeElement : null; await nextTick(); if (!disposed && open.value) (controls()[0] || element.value)?.focus() }
    else if (previous?.isConnected && !previous.closest('[inert],[hidden]')) { previous.focus({ preventScroll: true }); previous = null }
  })
  function keydown(event: KeyboardEvent) {
    if (!open.value || !element.value || element.value.closest('[inert],[hidden]')) return
    if (event.key === 'Escape') { event.preventDefault(); event.stopImmediatePropagation(); close(); return }
    if (event.key !== 'Tab') return
    const items = controls(), first = items[0], last = items[items.length - 1]
    if (!first) { event.preventDefault(); element.value.focus(); return }
    const active = document.activeElement
    if (event.shiftKey && (active === first || !element.value.contains(active))) { event.preventDefault(); last?.focus() }
    else if (!event.shiftKey && (active === last || !element.value.contains(active))) { event.preventDefault(); first.focus() }
  }
  onMounted(() => window.addEventListener('keydown', keydown, true))
  onBeforeUnmount(() => { disposed = true; window.removeEventListener('keydown', keydown, true) })
  return element
}

/**
 * 为共享控件提供一个不拦截原生交互的按压反馈。
 *
 * 它只写入视觉用的数据属性和波纹起点，不会阻止 click、键盘事件、路由跳转
 * 或弹层的焦点管理。CSS 伪元素本身也不接收指针事件，因此不会盖住实际按钮。
 */
type RippleTarget = HTMLElement

const clearTimers = new WeakMap<RippleTarget, ReturnType<typeof setTimeout>>()

function targetOf(value: EventTarget | null): RippleTarget | null {
  // 不依赖 instanceof HTMLElement，便于在非浏览器的组件测试环境中安全降级。
  if (!value || typeof (value as RippleTarget).getBoundingClientRect !== 'function') return null
  const target = value as RippleTarget
  if (!target.style || typeof target.style.setProperty !== 'function' || !target.dataset || target.matches?.(':disabled,[aria-disabled="true"]')) return null
  return target
}

function percent(value: number) {
  return `${Math.max(0, Math.min(100, value)).toFixed(2)}%`
}

function clearLater(target: RippleTarget) {
  const previous = clearTimers.get(target)
  if (previous) clearTimeout(previous)
  clearTimers.set(target, setTimeout(() => {
    delete target.dataset.motionPress
    clearTimers.delete(target)
  }, 520))
}

function activate(target: RippleTarget, x: number, y: number) {
  target.style.setProperty('--df-motion-origin-x', percent(x))
  target.style.setProperty('--df-motion-origin-y', percent(y))
  target.dataset.motionPress = 'active'
  clearLater(target)
}

/**
 * 供 Vue 组件事件绑定使用的轻量处理器。
 * 返回对象可被 Button、下拉触发器和列表选项复用，而不是各页面自行处理波纹。
 */
export function usePressRipple() {
  function pointerdown(event: PointerEvent) {
    if (!event.isPrimary || event.button !== 0) return
    const target = targetOf(event.currentTarget)
    if (!target) return
    const rect = target.getBoundingClientRect()
    const x = rect.width ? ((event.clientX - rect.left) / rect.width) * 100 : 50
    const y = rect.height ? ((event.clientY - rect.top) / rect.height) * 100 : 50
    activate(target, x, y)
  }

  function keydown(event: KeyboardEvent) {
    if (event.key !== 'Enter' && event.key !== ' ') return
    const target = targetOf(event.currentTarget)
    if (target) activate(target, 50, 50)
  }

  return { pointerdown, keydown }
}

import { ref, watch } from 'vue'
import { layoutScope } from './layoutScope'

/**
 * 顶部项目导航只保存稳定的内部 key，而不是 URL、标题或任意用户输入。
 * 这样即使本地缓存被修改、语言切换或后续新增页签，也不会把未知链接带入导航。
 */
export const defaultProjectNavigation = [
  'requirements', 'iterations', 'defects', 'testing', 'roadmap', 'dashboard',
] as const

export type ProjectNavigationKey = typeof defaultProjectNavigation[number]

export const projectNavigationItems: Record<ProjectNavigationKey, { path: string; label: string }> = {
  requirements: { path: '/requirements', label: '需求' },
  iterations: { path: '/iterations', label: '迭代' },
  defects: { path: '/defects', label: '缺陷' },
  testing: { path: '/tests', label: '测试协作' },
  roadmap: { path: '/roadmap', label: '交付路线图' },
  dashboard: { path: '/dashboard', label: '项目仪表盘' },
}

function isProjectNavigationKey(value: unknown): value is ProjectNavigationKey {
  return typeof value === 'string' && Object.hasOwn(projectNavigationItems, value)
}

/**
 * 将损坏、旧版本或手工篡改的缓存收敛为允许的页签集合；未知 key 丢弃，
 * 新版本增加的页签按默认顺序补到末尾，保证升级后仍能访问全部功能。
 */
export function normalizeProjectNavigation(value: unknown): ProjectNavigationKey[] {
  const values = Array.isArray(value) ? value.slice(0, defaultProjectNavigation.length * 2) : []
  const saved: ProjectNavigationKey[] = []
  for (const value of values) {
    if (isProjectNavigationKey(value) && !saved.includes(value)) saved.push(value)
  }
  return [...saved, ...defaultProjectNavigation.filter(key => !saved.includes(key))]
}

export function moveProjectNavigation(items: readonly ProjectNavigationKey[], key: ProjectNavigationKey, target: number): ProjectNavigationKey[] {
  const next = normalizeProjectNavigation(items)
  const from = next.indexOf(key)
  if (from < 0 || !Number.isInteger(target) || target < 0 || target >= next.length) return next
  next.splice(from, 1)
  next.splice(target, 0, key)
  return next
}

function storageKey(): string | null {
  // layoutScope 仅由已经验证的 tenant/user ID 构造，防止不同账号复用导航偏好。
  return layoutScope.value ? `devflow-layout:v1:${layoutScope.value}:project-navigation.order` : null
}

function readProjectNavigation(): ProjectNavigationKey[] {
  const key = storageKey()
  if (!key) return normalizeProjectNavigation(null)
  try { return normalizeProjectNavigation(JSON.parse(localStorage.getItem(key) || 'null')) }
  catch { return normalizeProjectNavigation(null) }
}

/** 使用当前已验证账号范围内的导航顺序；浏览器拒绝存储时仍可正常导航。 */
export function useProjectNavigation() {
  const order = ref<ProjectNavigationKey[]>(readProjectNavigation())
  let restoring = false
  watch(layoutScope, () => {
    restoring = true
    order.value = readProjectNavigation()
    restoring = false
  }, { flush: 'sync' })
  watch(order, value => {
    const key = storageKey()
    if (restoring || !key) return
    try { localStorage.setItem(key, JSON.stringify(normalizeProjectNavigation(value))) }
    catch { /* 私密模式或存储配额不足时不阻断只读导航。 */ }
  }, { flush: 'sync' })
  return order
}

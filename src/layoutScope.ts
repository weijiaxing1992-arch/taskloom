import { readonly, ref, watch } from 'vue'

// 仅使用已验证会话中的租户与有效用户 ID 构造缓存范围，不能依据显示名或旧项目缓存。
// 代访问采用被代访人的有效 ID，避免管理员与成员共用抽屉宽度、折叠偏好等设置。
const scope = ref('')
export const layoutScope = readonly(scope)
export function applyLayoutScope(tenantId: unknown, userId: unknown) {
  scope.value = typeof tenantId === 'string' && !!tenantId.trim() && typeof userId === 'string' && !!userId.trim()
    ? `${encodeURIComponent(tenantId)}:${encodeURIComponent(userId)}` : ''
}
// 退出后保留各账号的非敏感布局供下次使用，但立即停止当前页面对它们的读写。
export function clearLayoutScope() { scope.value = '' }
function storageKey(name: string | undefined): string | null {
  return scope.value && name && /^[a-z0-9][a-z0-9.-]{0,79}$/.test(name) ? `devflow-layout:v1:${scope.value}:${name}` : null
}
function read(name: string | undefined): unknown {
  const key = storageKey(name)
  if (!key) return undefined
  try { const raw = localStorage.getItem(key); return raw === null ? undefined : JSON.parse(raw) }
  catch { return undefined }
}
function write(name: string | undefined, value: boolean | number) {
  const key = storageKey(name)
  if (!key) return
  try { localStorage.setItem(key, JSON.stringify(value)) } catch { /* 隐私模式或配额不足时放弃记忆，不阻塞页面操作。 */ }
}
export function readLayoutWidth(name: string | undefined, fallback: number): number {
  const value = read(name)
  return typeof value === 'number' && Number.isFinite(value) && value > 0 ? Math.round(value) : fallback
}
export function writeLayoutWidth(name: string | undefined, value: number) {
  if (Number.isFinite(value) && value > 0) write(name, Math.round(value))
}
export function useLayoutBoolean(name: string, fallback: boolean) {
  const readValue = () => { const value = read(name); return typeof value === 'boolean' ? value : fallback }
  const value = ref(readValue())
  let restoring = false
  // 同步切换作用域；恢复新账号偏好时禁止触发回写，防止把旧账号值写到新账号名下。
  watch(scope, () => { restoring = true; value.value = readValue(); restoring = false }, { flush: 'sync' })
  watch(value, next => { if (!restoring) write(name, next) }, { flush: 'sync' })
  return value
}

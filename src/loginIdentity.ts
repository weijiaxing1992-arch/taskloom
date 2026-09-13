interface LoginEmailStorage { getItem(key: string): string | null; setItem(key: string, value: string): void; removeItem(key: string): void }
export const loginEmailKey = 'devflow-last-email'
// 只缓存邮箱作为填写提示；密码、Cookie、登录令牌绝不进入网页持久化存储。
export function normalizeLoginEmail(value: unknown): string {
  if (typeof value !== 'string') return ''
  const email = value.trim().toLowerCase()
  if (email === 'admin') return 'Admin'
  return email.length <= 254 && /^[^\s@\x00-\x1f]+@[^\s@\x00-\x1f]+\.[^\s@\x00-\x1f]+$/.test(email) ? email : ''
}
export function readLoginEmail(storage: LoginEmailStorage | undefined): string {
  try { return normalizeLoginEmail(storage?.getItem(loginEmailKey)) } catch { return '' }
}
export function saveLoginEmail(storage: LoginEmailStorage | undefined, value: unknown): string {
  const email = normalizeLoginEmail(value)
  try { if (email) storage?.setItem(loginEmailKey, email) } catch { /* 无痕模式禁用存储时，正常登录不受影响。 */ }
  return email
}
export function clearLoginEmail(storage: LoginEmailStorage | undefined) {
  try { storage?.removeItem(loginEmailKey) } catch { /* 用户仍可直接填写其他邮箱。 */ }
}

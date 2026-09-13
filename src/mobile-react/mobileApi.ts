/**
 * 移动端 React 壳层只复用同源会话和现有 REST API，不保存令牌，也不复制业务权限。
 * 请求项目仍由服务端核验，浏览器缓存仅作为当前项目选择提示。
 */
export class MobileAPIError extends Error {
  constructor(message: string, public readonly status: number, public readonly code = '') {
    super(message)
  }
}

export async function mobileAPI<T>(path: string, options: RequestInit = {}): Promise<T> {
  const headers = new Headers(options.headers)
  if (!headers.has('Content-Type') && !(options.body instanceof FormData)) headers.set('Content-Type', 'application/json')
  if (!headers.has('X-TaskLoom-Project')) headers.set('X-TaskLoom-Project', localStorage.getItem('devflow-project') || 'prj_orbit')
  if (!headers.has('Accept-Language')) headers.set('Accept-Language', 'zh-CN')
  let response: Response
  try {
    response = await fetch(`/api${path}`, { ...options, credentials: 'same-origin', headers })
  } catch (cause) {
    if (options.signal?.aborted) throw cause
    throw new MobileAPIError('网络连接失败，请检查服务后重试', 0, 'network_unavailable')
  }
  const data = await response.json().catch(() => ({}))
  if (!response.ok) throw new MobileAPIError(data?.error?.message || '请求失败，请稍后重试', response.status, data?.error?.code || '')
  return data as T
}

export function relativeTime(value: unknown): string {
  const date = value ? new Date(String(value)) : null
  if (!date || Number.isNaN(date.getTime())) return '时间待定'
  const delta = Date.now() - date.getTime()
  if (delta < 60_000) return '刚刚'
  if (delta < 3_600_000) return `${Math.floor(delta / 60_000)} 分钟前`
  if (delta < 86_400_000) return `${Math.floor(delta / 3_600_000)} 小时前`
  return `${date.getMonth() + 1} 月 ${date.getDate()} 日`
}

export async function mobileBlob(path: string, projectId: string, signal?: AbortSignal): Promise<Blob> {
  const response = await fetch(`/api${path}`, { credentials: 'same-origin', signal, headers: { 'X-TaskLoom-Project': projectId } })
  if (!response.ok) throw new Error('附件暂时无法读取')
  return response.blob()
}

export function localDay(value: unknown): string {
  const date = value ? new Date(String(value)) : null
  if (!date || Number.isNaN(date.getTime())) return ''
  return `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, '0')}-${String(date.getDate()).padStart(2, '0')}`
}

export function sameOriginPath(value: unknown): string | null {
  if (typeof value !== 'string' || !value.trim()) return null
  try {
    const target = new URL(value, window.location.origin)
    if (target.origin !== window.location.origin || !target.pathname.startsWith('/')) return null
    return `${target.pathname}${target.search}${target.hash}`
  } catch {
    return null
  }
}

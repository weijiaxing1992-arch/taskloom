import { locale, t } from './i18n'
// 记录本标签页已验证的有效账号；代访问/其他标签页切换账号时，阻止旧表单以新身份提交。
// 它只是并发身份校验提示，不是凭据。真实身份及权限始终由服务端会话校验。
let expectedUser = ''
let identityGeneration = 0

export class APIError extends Error {
  constructor(message: string, public status: number, public code = '') { super(message) }
}

async function request(path: string, options: RequestInit = {}): Promise<Response> {
  const requestUser = expectedUser, requestGeneration = identityGeneration
  const headers = new Headers(options.headers)
  // 上传附件时让浏览器生成 multipart boundary，不能强行写成 JSON Content-Type。
  if (!headers.has('Content-Type') && !(options.body instanceof FormData)) headers.set('Content-Type', 'application/json')
  // 本地项目 ID 仅用于选择请求范围；缓存可被修改，后端必须再次检查项目成员权限。
  if (!headers.has('X-DevFlow-Project')) headers.set('X-DevFlow-Project', localStorage.getItem('devflow-project') || 'prj_orbit')
  if (!headers.has('Accept-Language')) headers.set('Accept-Language', locale.value)
  if (expectedUser && path !== '/session' && (!path.startsWith('/auth/') || path === '/auth/initial-password')) headers.set('X-DevFlow-Expected-User', expectedUser)
  let res: Response
  try {
    // 会话使用同源 HttpOnly Cookie，前端不读取或持久化会话密钥，也不自动重试写请求。
    res = await fetch('/api' + path, { ...options, credentials: 'same-origin', headers })
  } catch (cause) {
    if (options.signal?.aborted) throw cause
    throw new APIError(t('网络连接失败，请检查服务后重试'), 0, 'network_unavailable')
  }
  if (!res.ok) {
    const data = await res.json().catch(() => ({}))
    // 统一通知应用外壳冻结过期页面，具体错误继续向调用方抛出，避免界面假成功。
    // 旧账号请求可能在重新登录后才失败；仅当前身份世代的响应可冻结外壳，
    // 否则 A 的迟到 403/401 会把已登录的 B 误送首登页、禁用页或登录页。
    if (requestGeneration === identityGeneration && requestUser === expectedUser) {
      if (data?.error?.code === 'account_disabled') window.dispatchEvent(new CustomEvent('devflow-account-disabled'))
      if (data?.error?.code === 'password_change_required') window.dispatchEvent(new CustomEvent('devflow-password-change-required', { detail: { userId: requestUser } }))
      if (data?.error?.code === 'identity_changed' || data?.error?.code === 'impersonation_expired') window.dispatchEvent(new CustomEvent('devflow-identity-changed', { detail: data.error.code }))
      if (res.status === 401 && path !== '/auth/login') window.dispatchEvent(new CustomEvent('devflow-auth-expired'))
    }
    throw new APIError(data?.error?.message || t('请求失败'), res.status, data?.error?.code || '')
  }
  return res
}

export async function apiDownload(path: string, options: RequestInit = {}): Promise<Blob> {
  const generation = identityGeneration
  const blob = await (await request(path, options)).blob()
  assertRequestGeneration(generation)
  return blob
}

export async function api<T>(path: string, options: RequestInit = {}): Promise<T> {
  const generation = identityGeneration
  const res = await request(path, options)
  const data = await res.json().catch(() => ({}))
  // 成功响应同样可能迟到，必须先判定世代，再建立 /session 的身份基线。
  // 否则旧 A 会话的 200 会把已经重新登录的 B 误判为身份冲突。
  if (path !== '/auth/logout' && path !== '/auth/login') assertRequestGeneration(generation)
  if (path === '/session' && data?.user?.id) {
    // 首次验证建立身份基线；后续身份漂移交由应用外壳处理，不默默覆盖编辑中的账号。
    if (expectedUser && expectedUser !== data.user.id) {
      window.dispatchEvent(new CustomEvent('devflow-identity-changed', { detail: 'identity_changed' }))
      throw new APIError(t('账号身份已在其他页面切换，请刷新后继续'), 409, 'identity_changed')
    }
    expectedUser = data.user.id
  }
  if (path === '/auth/logout' || path === '/auth/login') { expectedUser = ''; identityGeneration++; window.dispatchEvent(new Event('devflow-auth-session-ended')) }
  return data
}

function assertRequestGeneration(generation: number) {
  if (generation !== identityGeneration) throw new APIError(t('账号身份已在其他页面切换，请刷新后继续'), 409, 'request_superseded')
}

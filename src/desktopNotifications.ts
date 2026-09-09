/** 浏览器与 Mac 网页容器共用的通知协议；缓存仅存去重游标和偏好，不存正文或凭据。 */
export interface DesktopNotice { id: number; title: string; body: string; eventType: string; projectName: string; projectId: string; url: string; createdAt: string; readAt?: string }
export interface DesktopSession { tenant?: { id: string }; user?: { id: string; operationDisabled?: boolean; mustChangePassword?: boolean }; impersonation?: unknown }
type API = (path: string, options?: RequestInit) => Promise<any>
type Checkpoint = { time: number; ids: number[] }
type Bridge = { postMessage(message: unknown): Promise<any> }
declare global { interface Window { webkit?: { messageHandlers?: { devflowDesktop?: Bridge } }; devflowDesktopAgent?: boolean } }
export const nativeBridge = () => window.webkit?.messageHandlers?.devflowDesktop
export function desktopScope(session: DesktopSession | null): string {
  return session?.user?.id && session.tenant?.id && !session.impersonation && !session.user.operationDisabled && !session.user.mustChangePassword ? `${location.origin}:${session.tenant.id}:${session.user.id}` : ''
}
export function noticeCategory(event: string): string {
  if (/mentioned|replied/.test(event)) return '提及与回复'
  if (/assigned|assignee|owner_changed|executor_changed|verifier_changed|completed/.test(event)) return '分配与交接'
  if (/updated|status_changed/.test(event)) return '需求与状态变更'
  if (/^test[._]/.test(event)) return '测试协作'
  return '其他动态'
}
export function safeNoticeURL(raw: string, origin: string): string | null {
  try {
    const url = new URL(raw, origin)
    if (url.origin !== origin || url.username || url.password || !['/requirements', '/defects', '/iterations', '/tests', '/projects', '/profile', '/notifications'].includes(url.pathname)) return null
    return url.pathname + url.search + url.hash
  } catch { return null }
}
function validNotice(item: any): item is DesktopNotice {
  return Number.isSafeInteger(item?.id) && item.id > 0 && typeof item.createdAt === 'string' && Number.isFinite(Date.parse(item.createdAt))
}
export async function collectNewNotices(api: API, checkpoint: Checkpoint | null): Promise<{ items: DesktopNotice[]; checkpoint: Checkpoint; unread: number }> {
  const seen = new Map<number, DesktopNotice>()
  let unread = 0, baselineTime = 0
  for (let offset = 0; offset < 100_000; offset += 100) {
    const page = await api(`/notifications?limit=100&offset=${offset}`)
    if (!Array.isArray(page.items) || !page.items.every(validNotice)) throw Error('通知数据格式异常，请稍后重试')
    if (!offset) { unread = Number(page.unread) || 0; baselineTime = Math.max(0, ...page.items.map((item: DesktopNotice) => Date.parse(item.createdAt))) }
    for (const item of page.items as DesktopNotice[]) seen.set(item.id, item)
    // 首次只记录基线；之后扫描到时间边界，覆盖同一秒的消息和超过一页的积压。
    if (!page.hasMore || page.items.some((item: DesktopNotice) => Date.parse(item.createdAt) < (checkpoint?.time ?? baselineTime))) break
    if (!page.items.length || offset === 99_900) throw Error('通知积压尚未读取完成，保留进度稍后重试')
  }
  const rows = [...seen.values()]
  const time = Math.max(checkpoint?.time || 0, ...rows.map(item => Date.parse(item.createdAt)))
  const ids = [...new Set([...(checkpoint?.time === time ? checkpoint.ids : []), ...rows.filter(item => Date.parse(item.createdAt) === time).map(item => item.id)])]
  const oldIDs = new Set(checkpoint?.ids || [])
  const items = checkpoint ? rows.filter(item => !item.readAt && (Date.parse(item.createdAt) > checkpoint.time || (Date.parse(item.createdAt) === checkpoint.time && !oldIDs.has(item.id)))).sort((a, b) => Date.parse(a.createdAt) - Date.parse(b.createdAt) || a.id - b.id) : []
  return { items, checkpoint: { time, ids }, unread }
}
function read<T>(key: string, fallback: T): T { try { return JSON.parse(localStorage.getItem(key) || 'null') ?? fallback } catch { return fallback } }
function write(key: string, value: unknown) { localStorage.setItem(key, JSON.stringify(value)) }
const prefix = (scope: string) => 'devflow-desktop-v1:' + scope
export const desktopPreferences = (scope: string): { enabled: boolean; sound: boolean } => read(prefix(scope) + ':settings', { enabled: false, sound: true })
export function saveDesktopPreferences(scope: string, change: Partial<{ enabled: boolean; sound: boolean }>) {
  write(prefix(scope) + ':settings', { ...desktopPreferences(scope), ...change })
  window.dispatchEvent(new Event('devflow-desktop-settings'))
}
let audio: AudioContext | undefined
export async function unlockNotificationSound() {
  if (nativeBridge()) return
  audio ||= new AudioContext()
  await audio.resume()
}
export function ringNotification() {
  if (!audio || audio.state !== 'running') return false
  const start = audio.currentTime
  for (const [delay, frequency] of [[0, 880], [0.17, 1174]]) {
    const tone = audio.createOscillator(), gain = audio.createGain()
    tone.frequency.value = frequency!; tone.type = 'sine'
    gain.gain.setValueAtTime(0, start + delay!)
    gain.gain.linearRampToValueAtTime(0.09, start + delay! + 0.02)
    gain.gain.exponentialRampToValueAtTime(0.001, start + delay! + 0.22)
    tone.connect(gain); gain.connect(audio.destination); tone.start(start + delay!); tone.stop(start + delay! + 0.24)
  }
  return true
}
export async function desktopPermission(request = false): Promise<string> {
  const bridge = nativeBridge()
  if (bridge) return String(await bridge.postMessage({ action: request ? 'requestPermission' : 'permission' }))
  if (!window.isSecureContext || !('Notification' in window)) return 'unsupported'
  return request ? Notification.requestPermission() : Notification.permission
}
function state(message: string) { window.dispatchEvent(new CustomEvent('devflow-desktop-status', { detail: message })) }
export async function openDesktopNotice(api: API, scope: string, item: Pick<DesktopNotice, 'id' | 'url' | 'projectId'>) {
  const target = safeNoticeURL(item.url, location.origin)
  if (!target || !item.projectId || !Number.isSafeInteger(item.id) || item.id < 1) throw Error('通知链接无效')
  const popup = nativeBridge() ? null : window.open('', '_blank')
  try {
  if (desktopScope(await api('/session')) !== scope) throw Error('请使用收到此通知的账号登录后再打开')
  // 点击时重新验证通知和项目权限；不相信系统通知中缓存的旧权限。
  await api(`/notifications/${item.id}`, { method: 'PATCH', body: JSON.stringify({ read: true }) })
  await api(`/projects/${encodeURIComponent(item.projectId)}/visit`, { method: 'POST', headers: { 'X-DevFlow-Project': item.projectId } })
  if (desktopScope(await api('/session')) !== scope) throw Error('账号已切换，请重新打开通知')
  const url = new URL(target, location.origin)
  url.searchParams.set('project', item.projectId)
  // 新窗口保留原页面未提交的编辑；Mac 由容器处理同源窗口请求。
  if (nativeBridge()) await nativeBridge()!.postMessage({ action: 'openURL', url: url.href })
  else if (popup) { popup.opener = null; popup.location.href = url.href }
  else { if (window.confirm('即将打开通知详情，请先确认本页编辑已保存。是否继续？')) location.assign(url.href) }
  } catch (error) { popup?.close(); throw error }
}
export function startDesktopNotifications(api: API, session: () => DesktopSession | null) {
  let stopped = false, busy = false, currentScope = '', generation = 0
  const delivered = new Set<Notification>()
  const channel = typeof BroadcastChannel === 'undefined' ? null : new BroadcastChannel('devflow-desktop-sound-v1')
  async function playShared(scope: string, id: number) {
    if (stopped || nativeBridge() || desktopScope(session()) !== scope || !desktopPreferences(scope).enabled || !desktopPreferences(scope).sound || !audio || audio.state !== 'running') return
    const play = () => {
      const key = prefix(scope) + ':sounds', played = read<number[]>(key, [])
      if (!played.includes(id) && ringNotification()) write(key, [...played, id].slice(-1000))
    }
    if (navigator.locks) await navigator.locks.request(prefix(scope) + ':sound', { ifAvailable: true }, lock => { if (lock) play() })
    else play()
  }
  if (channel) channel.onmessage = event => { const item = event.data; if (typeof item?.scope === 'string' && Number.isSafeInteger(item?.id)) void playShared(item.scope, item.id) }
  const gesture = () => { const prefs = desktopPreferences(desktopScope(session())); if (prefs.enabled && prefs.sound && !nativeBridge()) void unlockNotificationSound().catch(() => {}) }
  const alive = (scope: string, version: number) => !stopped && version === generation && desktopScope(session()) === scope
  async function clear() {
    delivered.forEach(item => item.close()); delivered.clear()
    await nativeBridge()?.postMessage({ action: 'clear' }).catch(() => {})
  }
  function invalidate() { generation++; currentScope = ''; void clear() }
  async function show(item: DesktopNotice, scope: string, sound: boolean) {
    const title = `[${noticeCategory(item.eventType || '')}] ${item.title || 'TaskLoom 新通知'}`.slice(0, 160)
    const body = `${item.projectName || 'TaskLoom'}\n${item.body || '点击查看完整详情'}`.slice(0, 2000)
    const bridge = nativeBridge()
    if (bridge) {
      if (await bridge.postMessage({ action: 'show', id: item.id, scope, title, body, sound, url: item.url, projectId: item.projectId }) !== true) throw Error('系统尚未接受通知，请检查通知权限')
    } else {
      const notice = new Notification(title, { body, tag: `${scope}:${item.id}`, silent: true })
      delivered.add(notice)
      notice.onclose = () => delivered.delete(notice)
      notice.onclick = () => { window.focus(); void openDesktopNotice(api, scope, item).catch(error => state(error.message)); notice.close() }
    }
  }
  async function tick() {
    if (busy || stopped) return
    const scope = desktopScope(session())
    if (scope !== currentScope) { generation++; if (currentScope) await clear(); currentScope = scope }
    if (!scope) return
    const preferences = desktopPreferences(scope)
    if (!preferences.enabled) return
    busy = true
    const version = generation
    try {
      if (await desktopPermission() !== 'granted' || !alive(scope, version)) return
      const work = async () => {
        if (!alive(scope, version)) return
        if (desktopScope(await api('/session')) !== scope) { invalidate(); return }
        const key = prefix(scope) + ':cursor'
        const saved = read<Checkpoint | null>(key, null)
        const checkpoint = saved && Number.isFinite(saved.time) && Array.isArray(saved.ids) ? saved : null
        const result = await collectNewNotices(api, checkpoint)
        if (!alive(scope, version) || !desktopPreferences(scope).enabled) return
        window.dispatchEvent(new CustomEvent('devflow-unread', { detail: result.unread }))
        // 已展示 ID 单独记录，避免后面的系统发送失败时重放之前已成功的声音/横幅。
        const ackKey = prefix(scope) + ':ack', ack = new Set(read<number[]>(ackKey, []))
        let sounded = false
        for (const item of result.items) {
          if (!alive(scope, version) || !desktopPreferences(scope).enabled) return
          if (ack.has(item.id)) continue
          await show(item, scope, preferences.sound && !sounded)
          if (!alive(scope, version)) { await clear(); return }
          ack.add(item.id); write(ackKey, [...ack].slice(-10000))
          if (preferences.sound && !sounded) {
            if (!nativeBridge()) { await playShared(scope, item.id); channel?.postMessage({scope,id:item.id}) }
            sounded = true
          }
        }
        write(key, result.checkpoint)
      }
      // Web Locks 跨标签页串行处理同一账号的游标；不支持时采用短租约和通知 tag 降级。
      if (navigator.locks) await navigator.locks.request(prefix(scope), { ifAvailable: true }, lock => lock ? work() : undefined)
      else {
        const key = prefix(scope) + ':lease', lease = read<{ until: number }>(key, { until: 0 })
        if (lease.until < Date.now()) { write(key, { until: Date.now() + 30000 }); try { await work() } finally { localStorage.removeItem(key) } }
      }
    } catch (error: any) {
      if ([401, 403, 409].includes(error?.status)) invalidate()
      if (alive(scope, version)) state(error?.message || '系统通知暂时无法同步，将自动重试')
    } finally { busy = false }
  }
  const opened = (event: Event) => {
    const payload = (event as CustomEvent).detail
    if (payload?.scope && payload?.id) void openDesktopNotice(api, payload.scope, payload).catch(error => state(error.message))
  }
  const settings = () => { if (!desktopPreferences(desktopScope(session())).enabled) void clear(); void tick() }
  const revoked = ['devflow-auth-session-ended', 'devflow-auth-expired', 'devflow-identity-changed', 'devflow-account-disabled', 'devflow-password-change-required']
  revoked.forEach(name => window.addEventListener(name, invalidate))
  window.addEventListener('devflow-desktop-open', opened)
  window.addEventListener('devflow-desktop-settings', settings)
  window.addEventListener('storage', settings)
  window.addEventListener('focus', tick)
  window.addEventListener('devflow-notifications-changed', tick)
  window.addEventListener('pointerdown', gesture)
  window.addEventListener('keydown', gesture)
  const timer = setInterval(tick, 5000)
  void tick()
  return () => { stopped = true; invalidate(); channel?.close(); clearInterval(timer); revoked.forEach(name => window.removeEventListener(name, invalidate)); window.removeEventListener('devflow-desktop-open', opened); window.removeEventListener('devflow-desktop-settings', settings); window.removeEventListener('storage', settings); window.removeEventListener('focus', tick); window.removeEventListener('devflow-notifications-changed', tick); window.removeEventListener('pointerdown', gesture); window.removeEventListener('keydown', gesture) }
}

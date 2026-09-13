import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import ts from 'typescript'
import * as Vue from 'vue'
import { renderToString } from 'vue/server-renderer'
import { parse, compileStyle } from 'vue/compiler-sfc'
import { createServer, loadConfigFromFile } from 'vite'
import vue from '@vitejs/plugin-vue'
import { createWorkspaceHarness } from './helpers/workspace-harness.mjs'

const root = fileURLToPath(new URL('../', import.meta.url))
const read = file => readFileSync(new URL('../' + file, import.meta.url), 'utf8')
const evaluate = (source, imports, globals = {}) => {
  const exports = {}, code = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText
  new Function('require', 'exports', ...Object.keys(globals), code)(id => imports[id] || {}, exports, ...Object.values(globals))
  return exports
}
const deferred = () => { let resolve, reject; const promise = new Promise((yes, no) => { resolve = yes; reject = no }); return { promise, resolve, reject } }
const session = (id = 'a', project = 'project-a', role = 'tenant_admin') => ({ tenant: { id: 'tenant', name: 'Organization' }, user: { id, name: 'Name ' + id, role }, project: { id: project, name: project, code: 'APP' } })
class APIError extends Error { constructor(message, status, code = '') { super(message); this.status = status; this.code = code } }
function appHarness({ publicPage = false, handler, ready = Promise.resolve(),viewportWidth=1400 } = {}) {
  const storage = new Map(), writes = [], localStorage = { getItem: key => storage.get(key) ?? null, setItem: (key, value) => { writes.push({ key, value }); storage.set(key, value) }, removeItem: key => storage.delete(key) }
  const store = createWorkspaceHarness(localStorage), scope = Vue.effectScope(), mounted = [], unmount = [], intervals = [], events = new Map(), calls = []
  const route = Vue.reactive({ meta: { public: publicPage }, query: {}, path: publicPage ? '/join/token' : '/requirements', fullPath: '/requirements' })
  // 保留真实 router.replace 的 query 效果，项目切换测试才能覆盖跨项目深链的清理逻辑。
  const routeHref = target => {
    const query = new URLSearchParams()
    for (const [key, value] of Object.entries(target.query || {})) {
      if (value !== undefined && value !== null) query.set(key, String(value))
    }
    const suffix = query.toString()
    return target.path + (suffix ? '?' + suffix : '')
  }
  const router = { isReady: () => ready, resolve: target => ({ fullPath: routeHref(target), href: routeHref(target) }), replace: async target => { route.path = target.path; route.query = { ...(target.query || {}) }; route.fullPath = routeHref(target); return undefined }, push: () => {} }
  let width=viewportWidth,mediaMax=0
  const mediaQueries=[],mediaListeners=new Map(),media={matches:false,addEventListener:(name,fn)=>mediaListeners.set(name,fn),removeEventListener:name=>mediaListeners.delete(name)},doc={visibilityState:'visible',activeElement:null,addEventListener:()=>{},removeEventListener:()=>{}}
  const win = { matchMedia:query=>{mediaQueries.push(query);mediaMax=Number(query.match(/max-width:\s*(\d+)px/)?.[1]);media.matches=width<=mediaMax;return media},addEventListener: (key, callback) => events.set(key, callback), removeEventListener: key => events.delete(key), dispatchEvent: event => { events.get(event.type)?.(event); return !event.defaultPrevented } }
  const imports = { ...store.imports, vue: { ...Vue, onMounted: fn => mounted.push(fn), onBeforeUnmount: fn => unmount.push(fn) }, 'vue-router': { useRoute: () => route, useRouter: () => router }, './api': { APIError, api: async (path, options) => { calls.push({ path, options }); return handler ? handler(path, options) : path === '/session' ? session() : { items: [], unread: 3 } } }, './layoutScope': { applyLayoutScope: () => {}, clearLayoutScope: () => {} }, './displayPreferences': { applyDisplayPreferences: () => {} }, './theme': { applyThemePreferences: () => {}, startThemeClock: () => () => {} }, './i18n': { t: x => x, applyLanguagePreferences: () => {} } }
  imports['./layoutScope'].useLayoutBoolean = (_key, fallback) => Vue.ref(fallback)
  const source = read('src/App.vue').match(/<script setup[^>]*>([\s\S]*?)<\/script>/)[1]
  const location = { href: '', reload: () => {} }
  const app = scope.run(() => evaluate(source + '\nexport {load,refreshUnread,unreadChanged,profileChanged,switchProject,isPublicRoute,identityChanged,authExpired,accountDisabled,mobileMenu,mobileViewport,mobileRail,mobileTrigger,mobileNavigationKeys}', imports, { localStorage, window: win, document:doc, location, setInterval: fn => { intervals.push(fn); return intervals.length }, clearInterval: () => {}, CustomEvent: class extends Event { constructor(type, options) { super(type, options); this.detail = options?.detail } } }))
  return { ...app, ...store, route, router, calls, storage, writes, win, location, intervals,doc,media,mediaQueries,mediaListeners,events,resize:value=>{width=value;media.matches=width<=mediaMax;mediaListeners.get('change')?.()},mount: () => mounted.forEach(fn => fn()), stop: () => { unmount.forEach(fn => fn()); scope.stop(); store.stop() } }
}
const flush = async () => { await Vue.nextTick(); for (let i = 0; i < 10; i++) await Promise.resolve() }
let count = 0
async function test(name, run) { await run(); count++; console.log('✓ ' + name) }

await test('Vue and the foundation dependencies are real, exact installed releases', () => {
  const pkg = JSON.parse(read('package.json'))
  assert.equal(pkg.dependencies.vue, '3.5.42'); assert.equal(Vue.version, '3.5.42')
  for (const name of ['pinia', 'reka-ui', 'class-variance-authority', 'clsx', 'tailwind-merge']) assert.match(pkg.dependencies[name], /^\d+\.\d+\.\d+$/)
  for (const name of ['tailwindcss', '@tailwindcss/vite', 'shadcn-vue', 'typescript', 'vite']) assert.match(pkg.devDependencies[name], /^\d+\.\d+\.\d+$/)
})
await test('notification badge synchronizes promptly and the redundant language decoration stays absent', () => {
  const appSource = read('src/App.vue'), notificationsSource = read('src/views/Notifications.vue'), localeSource = read('src/components/LocaleSwitcher.vue')
  assert.match(appSource, /const unreadRefreshInterval = 10_000/)
  assert.match(appSource, /addEventListener\('devflow-notifications-changed', refreshUnread\)/)
  assert.match(notificationsSource, /const notificationRefreshInterval = 10_000/)
  assert.match(notificationsSource, /addEventListener\('devflow-notifications-changed', refreshVisible\)/)
  assert.doesNotMatch(localeSource, /aria-hidden="true">◎/)
})
await test('Pinia owns verified context, while an unconfirmed project selection is never authoritative', () => {
  const m = createWorkspaceHarness({ getItem: () => 'cached-project' }), s = m.workspace
  assert.equal(s.project, 'cached-project'); assert.equal(s.currentProject, null)
  s.acceptContext({ session: session(), projects: [{ id: 'project-a' }], unread: 4.9 }); assert.equal(s.currentUser.id, 'a'); assert.equal(s.currentProject.id, 'project-a'); assert.equal(s.unread, 4)
  s.project = 'unconfirmed-project'; assert.equal(s.currentProject.id, 'project-a')
  s.switchingProject = true; s.acceptContext({ session: session(), projects: [], unread: -2 }); assert.equal(s.project, 'unconfirmed-project'); assert.equal(s.unread, 0)
  s.setUnread(Infinity); assert.equal(s.unread, 0); s.setUnread('7'); assert.equal(s.unread, 7)
  m.stop()
})
await test('separate application Pinia instances do not share sessions or permissions', () => {
  const a = createWorkspaceHarness(), b = createWorkspaceHarness()
  a.workspace.acceptContext({ session: session(), projects: [], unread: 4 }); assert(a.workspace.canManageOrganization); assert.equal(b.workspace.session, null)
  a.workspace.markIdentityConflict(true, 'Expired'); assert.equal(a.workspace.canManageOrganization, false); assert.equal(a.workspace.canManageProject, false); assert.equal(a.workspace.impersonationRecovery, true)
  a.workspace.clearSession(); assert.equal(a.workspace.currentUser, null); assert.equal(a.workspace.unread, 0); assert.equal(a.workspace.authError, '')
  a.stop(); b.stop()
})
await test('authenticated sessions remain memory-only; successful load caches no account or token', async () => {
  const m = appHarness(); assert.equal(await m.load(), true); assert.equal(m.workspace.currentUser.id, 'a'); assert.deepEqual(m.writes, []); m.stop()
})
await test('a direct public invitation waits for router readiness and sends no private requests', async () => {
  const ready = deferred(), m = appHarness({ ready: ready.promise }); m.mount(); m.route.meta.public = true; m.route.path = '/join/token'; ready.resolve(); await flush()
  await m.load(); await m.refreshUnread(); m.profileChanged(); m.unreadChanged({ detail: 99 }); m.intervals.forEach(fn => fn()); await flush()
  assert.deepEqual(m.calls, []); assert.equal(m.workspace.unread, 0); assert.equal(m.isPublicRoute.value, true); m.stop()
})
await test('visiting a public invitation preserves an existing identity and returning reloads verified context', async () => {
  const m = appHarness(); await m.load(); const calls = m.calls.length; m.route.meta.public = true; await flush(); await m.refreshUnread()
  assert.equal(m.workspace.currentUser.id, 'a'); assert.equal(m.calls.length, calls)
  m.route.meta.public = false; await flush(); assert.equal(m.calls.filter(call => call.path === '/session').length, 2); assert.equal(m.workspace.authRefreshing, false); m.stop()
})
await test('a session response arriving after entering a public page cannot start private follow-up requests', async () => {
  const late = deferred(), m = appHarness({ handler: () => late.promise }), loading = m.load(); m.route.meta.public = true; await flush(); late.resolve(session()); assert.equal(await loading, false)
  assert.deepEqual(m.calls.map(call => call.path), ['/session']); assert.equal(m.workspace.session, null); m.stop()
})
await test('transient service failures keep a verified session; first-load failures show retry without false login', async () => {
  let fail = false; const m = appHarness({ handler: async path => { if (fail) throw new APIError('Offline', 503); return path === '/session' ? session() : { items: [], unread: 2 } } })
  await m.load(); fail = true; assert.equal(await m.load(), false); assert.equal(m.workspace.currentUser.id, 'a'); assert.equal(m.workspace.authError, 'Offline'); assert.equal(m.workspace.authChecking, false); m.stop()
  const first = appHarness({ handler: async () => { throw new APIError('Offline', 503) } }); await first.load(); assert.equal(first.workspace.session, null); assert.equal(first.workspace.authError, 'Offline'); assert.equal(first.calls.length, 1); first.stop()
})
await test('a real 401 clears private context, and stale pre-conflict requests cannot re-enable writes', async () => {
  let failure; const m = appHarness({ handler: async path => { if (failure) throw failure; return path === '/session' ? session() : { items: [] } } }); await m.load(); failure = new APIError('Expired', 401); await m.load(); assert.equal(m.workspace.session, null); assert.equal(m.workspace.authError, ''); m.stop()
  const late = deferred(), pending = appHarness({ handler: () => late.promise }), load = pending.load(); pending.identityChanged({ detail: 'identity_changed' }); late.resolve(session()); assert.equal(await load, false); assert.equal(pending.workspace.identityConflict, true); assert.equal(pending.workspace.session, null); pending.stop()
})
await test('a late business 401 rechecks a refreshed valid session once without replaying the write or asking for a password',async()=>{
  const m=appHarness();await m.load();const before=m.calls.length
  m.authExpired({detail:{path:'/requirements/7'}});assert.equal(m.workspace.session,null);assert.equal(m.workspace.authChecking,true);await flush()
  assert.equal(m.workspace.currentUser.id,'a');assert.equal(m.calls.slice(before).filter(call=>call.path==='/session').length,1);assert(m.calls.slice(before).every(call=>!call.options?.method));assert(!m.calls.some(call=>call.path==='/auth/login'))
  const restored=m.calls.length;m.authExpired();await flush();assert.equal(m.calls.length,restored);assert.equal(m.workspace.session,null);m.stop()
})
await test('a failed session recheck stops at the login boundary instead of recursively restoring an expired cookie',async()=>{
  let m;m=appHarness({handler:async path=>{assert.equal(path,'/session');m.authExpired({detail:{path:'/session'}});throw new APIError('Expired',401)}})
  m.authExpired({detail:{path:'/requirements/7'}});await flush();assert.equal(m.calls.length,1);assert.equal(m.workspace.session,null);assert.equal(m.workspace.authChecking,false);assert.equal(m.workspace.authRefreshing,false);m.stop()
})
await test('cancelled project guards keep both the persisted and verified scope unchanged', async () => {
  const m = appHarness(); await m.load(); const calls = m.calls.length; m.storage.set('devflow-project', 'project-a'); m.workspace.project = 'project-b'; m.win.addEventListener('devflow-before-project-change', event => event.preventDefault()); await m.switchProject()
  assert.equal(m.workspace.project, 'project-a'); assert.equal(m.workspace.currentProject.id, 'project-a'); assert.equal(m.storage.get('devflow-project'), 'project-a'); assert.equal(m.calls.length, calls); assert.equal(m.workspace.switchingProject, false); m.stop()
})
await test('project switching commits cache only after visit and rejects stale old-project unread responses', async () => {
  const visit = deferred(), oldUnread = deferred(); let delayUnread = false
  const m = appHarness({ handler: async (path, options) => {
    if (path === '/session') return session()
    if (path === '/projects/project-b/visit') { assert.equal(options?.headers?.['X-TaskLoom-Project'], 'project-b'); return visit.promise }
    if (path === '/notifications/unread-count') return delayUnread ? oldUnread.promise : { unread: 3 }
    return { items: [], unread: 3 }
  } })
  await m.load(); m.storage.set('devflow-project', 'project-a'); delayUnread = true
  const oldRequest = m.refreshUnread(); await flush()
  assert.equal(m.calls.at(-1).options?.headers?.['X-TaskLoom-Project'], 'project-a')
  m.workspace.project = 'project-b'
  const switching = m.switchProject(); await flush()
  assert.equal(m.workspace.switchingProject, true); assert.equal(m.storage.get('devflow-project'), 'project-a')
  const callCount = m.calls.length; await m.refreshUnread(); assert.equal(m.calls.length, callCount)
  oldUnread.resolve({ unread: 99 }); await oldRequest; assert.equal(m.workspace.unread, 3)
  visit.resolve({}); await switching
  assert.equal(m.storage.get('devflow-project'), 'project-b'); assert.equal(m.location.href, '/requirements'); m.stop()
})
await test('a late unread poll cannot overwrite the authoritative count from a read-state update', async () => {
  const pending=deferred(); let delayed=false
  const m=appHarness({handler:async path=>path==='/session'?session():path==='/notifications/unread-count'&&delayed?pending.promise:{items:[],unread:3}})
  await m.load(); delayed=true
  const old=m.refreshUnread(); await flush()
  m.unreadChanged({detail:1}); assert.equal(m.workspace.unread,1)
  pending.resolve({unread:3}); await old; assert.equal(m.workspace.unread,1)
  delayed=false; const requestCount=m.calls.length; await m.refreshUnread(); assert.equal(m.workspace.unread,1); assert.equal(m.calls.length,requestCount)
  await m.refreshUnread({type:'devflow-notifications-changed'}); assert.equal(m.workspace.unread,3)
  m.authExpired(); m.unreadChanged({detail:99}); assert.equal(m.workspace.unread,0)
  m.stop()
})
await test('same-user context reload also preserves a newer read-state event without crossing identities', async () => {
  const pending=deferred(); let delayed=false
  const m=appHarness({handler:async path=>path==='/session'?session():path==='/notifications/unread-count'&&delayed?pending.promise:{items:[],unread:3}})
  await m.load(); delayed=true
  const loading=m.load(); await flush(); m.unreadChanged({detail:0})
  pending.resolve({unread:3}); await loading; assert.equal(m.workspace.unread,0); m.stop()
})
await test('manual project switches discard cross-project deep-link query before reloading the destination', async () => {
  const m = appHarness(); await m.load(); m.storage.set('devflow-project', 'project-a')
  m.route.query = { project: 'project-b', req: '44', create: 'case' }; m.route.fullPath = '/requirements?project=project-b&req=44&create=case'; m.workspace.project = 'project-b'
  await m.switchProject()
  assert.deepEqual(m.route.query, {}); assert.equal(m.route.fullPath, '/requirements'); assert.equal(m.location.href, '/requirements'); assert.equal(m.storage.get('devflow-project'), 'project-b'); m.stop()
})
await test('notification project links validate accessible projects and commit only a verified target', async () => {
  const target = deferred(), m = appHarness({ handler: async (path, options) => { const project = options?.headers?.['X-TaskLoom-Project']; if (path === '/session') return project === 'project-b' ? target.promise : session(); return { items: [{ id: 'project-b', status: 'active' }], unread: 3 } } })
  m.route.query.project = 'project-b'; m.storage.set('devflow-project', 'project-a'); const loading = m.load(); await flush(); assert.equal(m.storage.get('devflow-project'), 'project-a'); assert.equal(m.workspace.session, null)
  target.resolve(session('a', 'project-b')); assert.equal(await loading, true); assert.equal(m.workspace.currentProject.id, 'project-b'); assert.equal(m.storage.get('devflow-project'), 'project-b')
  assert.equal(m.calls.filter(call => call.options?.headers?.['X-TaskLoom-Project'] === 'project-b').length, 4); m.stop()
})
await test('inaccessible or unavailable notification targets never change the original project', async () => {
  for (const accessible of [false, true]) {
    const m = appHarness({ handler: async (path, options) => { if (options?.headers?.['X-TaskLoom-Project']) throw new APIError('Offline', 503); return path === '/session' ? session() : { items: accessible ? [{ id: 'project-b', status: 'active' }] : [], unread: 3 } } })
    m.route.query.project = 'project-b'; m.storage.set('devflow-project', 'project-a'); m.workspace.acceptContext({ session: session(), projects: [], unread: 2 }); assert.equal(await m.load(), false)
    assert.equal(m.storage.get('devflow-project'), 'project-a'); assert.equal(m.workspace.currentProject.id, 'project-a'); assert(m.workspace.authError); assert.equal(m.calls.filter(call => call.options?.headers?.['X-TaskLoom-Project']).length, accessible ? 1 : 0); m.stop()
  }
})
await test('login retries preserve the initial notification project but later query changes never auto-switch scope', async () => {
  let loggedIn = false
  const m = appHarness({ handler: async (path, options) => { if (!loggedIn) throw new APIError('Login', 401); return path === '/session' ? session('a', options?.headers?.['X-TaskLoom-Project'] || 'project-a') : { items: [{ id: 'project-b', status: 'active' }], unread: 0 } } })
  m.route.query.project = 'project-b'; await m.load(); assert.equal(m.workspace.session, null); loggedIn = true; assert.equal(await m.load(), true); assert.equal(m.workspace.currentProject.id, 'project-b')
  m.route.query.project = 'arbitrary-new-query'; const before = m.calls.length; await m.load(); assert.equal(m.calls.length - before, 4); assert(m.calls.slice(before).every(call => !call.options?.headers?.['X-TaskLoom-Project'])); m.stop()
})
await test('delegated report and organization navigation is derived from verified grants, never a project-admin role', () => {
  const m = createWorkspaceHarness(), context = session('a', 'project-a', 'project_admin'); m.workspace.acceptContext({ session: context, projects: [], unread: 0 }); assert(m.workspace.canManageProject); assert(m.workspace.canAccessWorkload); assert.equal(m.workspace.canViewReports, false); assert.equal(m.workspace.canOpenOrganization, false)
  m.workspace.acceptContext({ session: { ...context, organizationPermissions: ['reports.view', 'organization.read'] }, projects: [], unread: 0 }); assert(m.workspace.canViewReports); assert(m.workspace.canOpenOrganization); m.workspace.markIdentityConflict(false, 'Changed'); assert.equal(m.workspace.canViewReports, false); assert.equal(m.workspace.canAccessWorkload, false); m.stop()
})
await test('disabled account loads only its session, retains cached project and exposes no privileged capabilities',async()=>{
 const account={...session('a',''),canImpersonate:true,organizationPermissions:['reports.view','organization.read']};account.user.operationDisabled=true
 const m=appHarness({handler:async path=>{assert.equal(path,'/session');return account}});m.storage.set('devflow-project','cached-project');m.workspace.project='cached-project';m.route.query.project='forbidden-target';assert.equal(await m.load(),true)
 assert.deepEqual(m.calls.map(call=>call.path),['/session']);assert.equal(m.workspace.currentUser.id,'a');assert.equal(m.workspace.project,'cached-project');assert.equal(m.storage.get('devflow-project'),'cached-project');assert.deepEqual(m.workspace.projects,[]);assert.equal(m.workspace.unread,0)
 for(const key of ['canManageProject','canManageOrganization','canOpenOrganization','canViewReports','canAccessWorkload'])assert.equal(m.workspace[key],false,key)
 await m.refreshUnread();assert.equal(m.calls.length,1);m.stop()
})
await test('projectless administrator can open archived-project management without reading a scoped inbox', async () => {
  const account = session('admin', ''), m = appHarness({ handler: async path => {
    if (path === '/session') return account
    if (path === '/projects') return { items: [{ id: 'archived-p', status: 'archived', canRestore: true }] }
    assert.equal(path, '/preferences/display'); return { themeMode: 'dark' }
  } })
  m.route.path = '/projects'; m.storage.set('devflow-project', 'archived-p'); m.workspace.project = 'archived-p'
  assert.equal(await m.load(), true); assert.equal(m.workspace.currentProject.id, ''); assert.equal(m.workspace.currentUser.id, 'admin'); assert(m.workspace.canManageOrganization)
  assert.deepEqual(m.calls.map(c => c.path).sort(), ['/session', '/projects', '/preferences/display'].sort()); assert.equal(m.workspace.projects[0].status, 'archived'); assert.equal(m.workspace.unread, 0)
  assert.equal(m.storage.get('devflow-project'), 'archived-p'); assert.deepEqual(m.writes, []); await m.refreshUnread(); assert.equal(m.calls.length, 3); m.stop()
})
await test('management pages recover archived cached projects only after an active target session is verified', async () => {
  for (const page of ['/projects', '/organization/members']) {
    const target = deferred(), m = appHarness({ handler: async (path, options) => {
      const selected = options?.headers?.['X-TaskLoom-Project']
      if (path === '/session') { if (selected === 'active-p') return target.promise; throw new APIError('Archived project', 403, 'project_forbidden') }
      if (path === '/projects') return { items: [{ id: 'archived-p', status: 'archived' }, { id: 'deleted-p', status: 'deleted' }, { id: 'active-p', status: 'active' }] }
      return path === '/notifications/unread-count' ? { unread: 4 } : {}
    } })
    m.route.path = page; m.storage.set('devflow-project', 'archived-p'); m.workspace.project = 'archived-p'
    const loading = m.load(); await flush(); assert.equal(m.storage.get('devflow-project'), 'archived-p'); assert.equal(m.workspace.session, null); assert.deepEqual(m.writes, [])
    assert.deepEqual(m.calls.map(c => c.path), ['/session', '/projects', '/session']); assert.equal(m.calls[2].options.headers['X-TaskLoom-Project'], 'active-p')
    target.resolve(session('admin', 'active-p')); assert.equal(await loading, true); assert.equal(m.workspace.currentProject.id, 'active-p'); assert.equal(m.storage.get('devflow-project'), 'active-p'); assert.equal(m.workspace.unread, 4)
    assert.deepEqual(m.writes, [{ key: 'devflow-project', value: 'active-p' }]); assert(m.calls.slice(2).every(c => c.options?.headers?.['X-TaskLoom-Project'] === 'active-p')); m.stop()
  }
})
await test('management recovery does not rewrite cache on no active project, failed/mismatched verification, or explicit notification deep links', async () => {
  for (const scenario of ['no-active', 'unavailable', 'mismatch', 'notification-link']) {
    const m = appHarness({ handler: async (path, options) => {
      const selected = options?.headers?.['X-TaskLoom-Project']
      if (path === '/session') {
        if (!selected) throw new APIError('Archived project', 403, 'project_forbidden')
        if (scenario === 'unavailable') throw new APIError('Offline', 503, 'database_unavailable')
        return session('admin', 'unexpected-p')
      }
      if (path === '/projects') return { items: scenario === 'no-active' ? [{ id: 'archived-p', status: 'archived' }] : [{ id: 'active-p', status: 'active' }] }
      return { unread: 0 }
    } })
    m.route.path = '/organization'; m.storage.set('devflow-project', 'archived-p'); m.workspace.acceptContext({ session: session('admin', 'archived-p'), projects: [], unread: 3 })
    if (scenario === 'notification-link') m.route.query.project = 'explicit-target'
    assert.equal(await m.load(), false, scenario); assert.equal(m.workspace.currentProject.id, 'archived-p'); assert.equal(m.storage.get('devflow-project'), 'archived-p'); assert.deepEqual(m.writes, []); assert(m.workspace.authError)
    if (scenario === 'notification-link') assert.deepEqual(m.calls.map(c => c.path), ['/session'])
    if (scenario === 'no-active') assert.deepEqual(m.calls.map(c => c.path), ['/session', '/projects'])
    m.stop()
  }
})
await test('disable events revoke UI operations without signing out or accepting an older unread response',async()=>{
 const pending=deferred();let delay=false;const m=appHarness({handler:async path=>path==='/session'?session():delay?pending.promise:{items:[],unread:3}});await m.load();m.storage.set('devflow-project','project-a');delay=true;const request=m.refreshUnread();m.accountDisabled();assert.equal(m.workspace.currentUser.id,'a');assert.equal(m.workspace.operationDisabled,true);assert.equal(m.workspace.unread,0);assert.deepEqual(m.workspace.projects,[]);assert.equal(m.storage.get('devflow-project'),'project-a')
 pending.resolve({unread:999});await request;const calls=m.calls.length;await m.refreshUnread();assert.equal(m.calls.length,calls);assert.equal(m.workspace.unread,0);m.stop()
})
await test('mobile navigation uses the real 1024px media boundary and clears state on desktop or navigation',async()=>{
 const m=appHarness({viewportWidth:390});m.mount();await flush();assert.deepEqual(m.mediaQueries,['(max-width:1024px)']);assert.equal(m.mobileViewport.value,true)
 for(const width of [375,390,768,1024]){m.resize(width);assert.equal(m.mobileViewport.value,true,'width '+width)}
 m.mobileMenu.value=true;m.resize(1025);assert.equal(m.mobileViewport.value,false);assert.equal(m.mobileMenu.value,false);m.resize(768);m.mobileMenu.value=true;m.route.fullPath='/my-work';await flush();assert.equal(m.mobileMenu.value,false);assert.equal(m.workspace.currentUser.id,'a')
 assert.equal(m.mediaListeners.size,1);m.stop();assert.equal(m.mediaListeners.size,0);assert.equal(m.events.size,0)
})
await test('mobile menu focuses its first control, traps keyboard focus and returns focus to the trigger on Escape',async()=>{
 const m=appHarness({viewportWidth:390});m.mount();await flush();const control=visible=>Vue.markRaw({getClientRects:()=>visible?[{}]:[],focus(){m.doc.activeElement=this}}),first=control(true),hidden=control(false),last=control(true),trigger=control(true)
 m.mobileRail.value={querySelector:()=>first,querySelectorAll:()=>[first,hidden,last]};m.mobileTrigger.value=trigger;m.mobileMenu.value=true;await flush();assert.equal(m.doc.activeElement,first)
 const key=(key,shiftKey=false)=>({key,shiftKey,prevented:false,preventDefault(){this.prevented=true}});const back=key('Tab',true);m.mobileNavigationKeys(back);assert(back.prevented);assert.equal(m.doc.activeElement,last);const forward=key('Tab');m.mobileNavigationKeys(forward);assert(forward.prevented);assert.equal(m.doc.activeElement,first)
 const escape=key('Escape');m.mobileNavigationKeys(escape);await flush();assert(escape.prevented);assert.equal(m.mobileMenu.value,false);assert.equal(m.doc.activeElement,trigger);const closed=key('Tab');m.mobileNavigationKeys(closed);assert.equal(closed.prevented,false);m.stop()
 const source=read('src/App.vue');assert.match(source,/:inert="mobileViewport&&!mobileMenu"/);assert.match(source,/:inert="mobileViewport&&mobileMenu"/);assert.match(source,/@media\(max-width:1024px\)/)
})
await test('public route and old member URLs have explicit compatible routing', () => {
  const source = read('src/main.ts'); assert.match(source, /path:'\/join\/:token',component:JoinOrganization,meta:\{public:true\}/); assert.match(source, /path:'\/members',redirect:'\/organization\/members'/); assert.match(source, /use\(createPinia\(\)\)\.use\(router\)/)
  assert.match(read('src/App.vue'), /<RouterView v-if="isPublicRoute"\s*\/>/)
})
await test('Tailwind is incremental: no global preflight, semantic account theme, legacy CSS retained', () => {
  const css = read('src/tailwind.css'), main = read('src/main.ts'); assert(!css.includes('tailwindcss/preflight.css')); assert.match(css, /tailwindcss\/utilities.css/); assert.match(css, /data-theme="dark"/); assert.match(css, /--color-primary: var\(--primary/)
  for (const file of ['style.css', 'v2.css', 'v3.css', 'tailwind.css']) assert(main.includes("import './" + file + "'"))
  const popover = read('src/components/ui/popover/PopoverContent.vue'); assert(!popover.includes('<PopoverPortal')); assert(!popover.includes('<Teleport')); assert.match(popover, /stopImmediatePropagation/); assert(!popover.includes('preventDefault()'))
})

await test('custom select menu CSS reaches the nested popover and gives long event lists real scrolling', () => {
  const { descriptor } = parse(read('src/components/AppSelect.vue'))
  const output = compileStyle({ source: descriptor.styles[0].content, filename: 'AppSelect.vue', id: 'data-v-test', scoped: true })
  assert.equal(output.errors.length, 0)
  const menu = output.code.match(/\.app-select-menu\[data-slot="popover-content"\]\s*\{([^}]+)\}/)
  assert(menu, 'menu selector must not depend on an ancestor component scoped attribute')
  assert.match(menu[1], /overflow-y:\s*auto/)
  assert.match(menu[1], /max-height:\s*min\(280px/)
  assert.match(menu[1], /overscroll-behavior:\s*contain/)
  // 通过 cn 合并原语的宽度/间距 utility；不能依赖普通 CSS 盖住 important layer。
  assert.match(descriptor.template.content, /w-\[max\(172px,var\(--reka-popover-trigger-width\)\)\] p-1/)
})

// Vite 7 keeps its WebSocket transport separate from HMR; SSR tests need neither listener.
const developmentConfig=await loadConfigFromFile({command:'serve',mode:'test'},root+'vite.config.ts',root,'silent',undefined,'runner')
assert(developmentConfig,'the real Vite configuration must load')
const server = await createServer({ configFile: false, root, plugins: [vue()], resolve: { alias: { '@': root + 'src' } }, optimizeDeps: { noDiscovery: true, include: [] }, server: { middlewareMode: true, watch: null, hmr: false, ws: false }, appType: 'custom' })
try {
  assert.equal(server.httpServer, null)
  assert.equal(server.config.server.hmr, false)
  assert.equal(server.config.server.ws, false)
  await test('development scans only the application entry and ignores archives without excluding source HMR',async()=>{
    const config=developmentConfig.config,ignored=config.server.watch.ignored
    assert.deepEqual(config.optimizeDeps.entries,['index.html'])
    assert.equal(typeof ignored,'function')
    for(const path of ['work','work/old-build/web/index.html','outputs/report.html','交付文件夹/release/index.html','开源交付/TaskLoom/source/index.html','clients/devflow_macos/build/app','data/devflow.db','dist/assets/old.js','dist-developer/index.html','build/index.html','archives/old/index.html','backups/snapshot.json','release/index.html','cmd/server/assets/index.html','snapshot.db','snapshot.db-wal','snapshot.sqlite3-shm','snapshot.sqlite-journal','release.tar.gz','backup.zip','installer.dmg'])assert.equal(ignored(root+path),true,'must ignore '+path)
    for(const path of ['', 'index.html','src','src/main.ts','src/App.vue','src/views/Requirements.vue','src/components/RequirementHistory.vue','src/tailwind.css','src/work/helper.ts','src/assets/icon.svg','scripts/theme-palette.mjs','portal/web-public/portal/index.html'])assert.equal(ignored(root+path),false,'must preserve '+path)
    assert.notEqual(config.server.hmr,false,'application HMR must remain enabled')
    assert.notEqual(config.server.watch,null,'source watcher must remain enabled')
    const html=await server.transformIndexHtml('/requirements',read('index.html'))
    assert.match(html,/src="\/@vite\/client"/)
    // Vite extracts the real inline bootstrap into an HTML proxy module.
    const bootstrap=html.match(/src="([^"]+\?html-proxy&index=0\.js)"/)?.[1]
    assert(bootstrap,'the browser must receive its inline bootstrap module')
    const inlineEntry=await server.transformRequest(bootstrap.replace('/@id/__x00__','\0'))
    assert.match(inlineEntry?.code||'',/import\(['"]\/src\/main\.ts['"]\)/)
    const entry=await server.transformRequest('/src/main.ts')
    assert(entry?.code,'Vite must transform the real browser entry')
    assert.match(entry.code,/createApp\(/);assert.match(entry.code,/import\.meta\.hot/)
    const app=await server.transformRequest('/src/App.vue')
    assert(app?.code,'Vite must compile the source Vue application');assert.match(app.code,/_sfc_main|defineComponent/)
  })
  await test('real shadcn/Reka Button defaults to a non-submitting native button and supports explicit submit', async () => {
    const { default: Button } = await server.ssrLoadModule('/src/components/ui/button/Button.vue')
    const ordinary = await renderToString(Vue.createSSRApp({ render: () => Vue.h(Button, { variant: 'outline', disabled: true }, () => 'Open') }))
    assert.match(ordinary, /<button[^>]*type="button"/); assert.match(ordinary, /disabled/); assert.match(ordinary, /data-slot="button"/); assert.match(ordinary, /border-border/)
    const submit = await renderToString(Vue.createSSRApp({ render: () => Vue.h(Button, { type: 'submit' }, () => 'Save') })); assert.match(submit, /type="submit"/)
    const asChild = await renderToString(Vue.createSSRApp({ render: () => Vue.h(Button, { asChild: true }, () => Vue.h('a', { href: '/requirements' }, 'View')) })); assert.match(asChild, /<a[^>]*href="\/requirements"/); assert(!asChild.includes('type="button"'))
  })
  await test('class merging preserves caller overrides and ScrollArea renders an accessible native viewport', async () => {
    const { cn } = await server.ssrLoadModule('/src/lib/utils.ts'); assert.equal(cn('px-4 text-sm', { 'px-2': true }, false, 'text-lg'), 'px-2 text-lg')
    const { default: ScrollArea } = await server.ssrLoadModule('/src/components/ui/scroll-area/ScrollArea.vue')
    const html = await renderToString(Vue.createSSRApp({ render: () => Vue.h(ScrollArea, { class: 'h-40', horizontal: true }, () => 'Workflow') })); assert.match(html, /data-slot="scroll-area-viewport"/); assert.match(html, /tabindex="0"/); assert.match(html, /Workflow/)
  })
} finally { await server.close() }
console.log(`Passed ${count} frontend foundation and public-route regression tests.`)

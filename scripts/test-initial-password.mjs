import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import ts from 'typescript'
import * as Vue from 'vue'
import { parse, compileScript, compileTemplate } from 'vue/compiler-sfc'
import { createWorkspaceHarness } from './helpers/workspace-harness.mjs'

const read = path => readFileSync(new URL('../' + path, import.meta.url), 'utf8')
const js = source => ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText
function evaluate(source, imports = {}, globals = {}) {
  const exports = {}
  new Function('require', 'exports', ...Object.keys(globals), js(source))(id => { assert(id in imports, `Missing import: ${id}`); return imports[id] }, exports, ...Object.values(globals))
  return exports
}
const policy = evaluate(read('src/initialPassword.ts'))
const deferred = () => { let resolve, reject; const promise = new Promise((a, b) => { resolve = a; reject = b }); return { promise, resolve, reject } }
const flush = async () => { for (let i = 0; i < 6; i++) { await Promise.resolve(); await Vue.nextTick() } }
class LocalEvent extends Event { constructor(type, options = {}) { super(type); this.detail = options.detail } }
const node = (tag, text = '') => ({ tag, text, props: {}, children: [], parent: null })
const renderer = Vue.createRenderer({
  createElement: tag => node(tag), createText: text => node('#text', text), createComment: () => node('#comment'),
  insert(child, parent, anchor) { if (child.parent) child.parent.children.splice(child.parent.children.indexOf(child), 1); child.parent = parent; const at = anchor ? parent.children.indexOf(anchor) : -1; if (at < 0) parent.children.push(child); else parent.children.splice(at, 0, child) },
  remove(child) { if (child.parent) child.parent.children.splice(child.parent.children.indexOf(child), 1) },
  setText(child, text) { child.text = text }, setElementText(child, text) { child.text = text; child.children = [] }, parentNode: child => child.parent,
  nextSibling: child => child.parent?.children[child.parent.children.indexOf(child) + 1] || null, patchProp(child, key, _old, value) { child.props[key] = value },
})
const descendants = root => [root, ...root.children.flatMap(descendants)]
const text = root => root.text + root.children.map(text).join('')
const source = read('src/components/InitialPasswordChange.vue'), descriptor = parse(source).descriptor
const script = compileScript(descriptor, { id: 'initial-password-test' })
const template = compileTemplate({ source: descriptor.template.content, filename: 'InitialPasswordChange.vue', id: 'initial-password-test', compilerOptions: { bindingMetadata: script.bindings } })
assert.deepEqual(template.errors, [])
function mountForm(initialProps = {}, handler = async () => ({ changed: true, requiresLogin: true })) {
  const props = Vue.reactive({ user: { id: 'u-a', name: '原姓名' }, ...initialProps }), calls = [], events = []
  const imports = { vue: { ...Vue, withDirectives: vnode => vnode }, '../api': { api: async (path, options) => { calls.push({ path, options }); return handler(path, options) } }, '../i18n': { t: value => value }, '../initialPassword': policy, './LocaleSwitcher.vue': { default: Vue.defineComponent({ render: () => Vue.h('span', 'Locale') }) } }
  const forbiddenStorage = new Proxy({}, { get() { throw Error('Password form must not access browser storage') } })
  const component = evaluate(script.content, imports, { localStorage: forbiddenStorage, sessionStorage: forbiddenStorage }).default
  component.render = evaluate(template.code, imports).render
  const root = Vue.h(Vue.defineComponent({ setup: () => () => Vue.h(component, { ...props, onCompleted: () => events.push('completed'), onLogout: () => events.push('logout'), onRefresh: () => events.push('refresh'), onReturnAdministrator: () => events.push('returnAdministrator') }) }))
  const container = node('root'); renderer.render(root, container)
  return { props, calls, events, container, c: root.component.subTree.component.setupState, stop() { renderer.render(null, container) } }
}
function fill(c) { Object.assign(c.form, { currentPassword: 'Temporary-fixture', newPassword: 'Personal2027!', confirmPassword: 'Personal2027!' }) }
function assertEmpty(c) { assert.deepEqual({ ...c.form }, { currentPassword: '', newPassword: '', confirmPassword: '' }) }

const appSource = read('src/App.vue')
function appHarness(handler, initialRoute = {}) {
  const storage = new Map(), calls = [], events = [], localStorage = { getItem: key => storage.get(key) ?? null, setItem: (key, value) => storage.set(key, value), removeItem: key => storage.delete(key) }
  const store = createWorkspaceHarness(localStorage), scope = Vue.effectScope(), window = new EventTarget()
  window.dispatchEvent = event => { events.push(event); return true }
  const route = { path: '/requirements', query: {}, meta: {}, ...initialRoute }
  const imports = { ...store.imports, vue: { ...Vue, onMounted() {}, onBeforeUnmount() {} }, 'vue-router': { useRoute: () => route, useRouter: () => ({}) }, './api': { api: async (path, options) => { calls.push({ path, options }); return handler(path, options) }, APIError: class APIError extends Error {} }, './i18n': { t: value => value, applyLanguagePreferences() {} }, './displayPreferences': { applyDisplayPreferences() {} }, './theme': { applyThemePreferences() {}, startThemeClock() {} }, './layoutScope': { applyLayoutScope() {}, clearLayoutScope() {}, useLayoutBoolean: (_key, fallback) => Vue.ref(fallback) }, './components/ui/button': {} }
  for (const match of appSource.matchAll(/import .*? from '([^']+\.vue)'/g)) imports[match[1]] = {}
  const body = appSource.match(/<script setup[^>]*>([\s\S]*?)<\/script>/)[1]
  const exposed = 'load,session,workspace,authChecking,authRefreshing,initialPasswordRequired,passwordChangeComplete,loginNotice,refreshUnread,identityConflict'
  const c = scope.run(() => evaluate(body + `\nexport {${exposed}}`, imports, { localStorage, window, document: { visibilityState: 'visible' }, location: { reload() {} }, CustomEvent: LocalEvent }))
  return { ...c, calls, storage, events, stop() { scope.stop(); store.stop() } }
}
const session = (id = 'u-a', flags = {}) => ({ tenant: { id: 't', name: 'Team' }, project: { id: 'p', name: 'Project', code: 'P' }, user: { id, name: id, role: 'tenant_admin', ...flags }, canImpersonate: true, organizationPermissions: ['organization.read', 'reports.view'] })
function apiHarness() {
  const calls = [], events = [], pending = new Map(), state = { user: 'u-a' }
  const imports = { './i18n': { locale: Vue.ref('en-US'), t: value => value } }
  const fetch = async (path, options) => { calls.push({ path, options }); if (pending.has(path)) return pending.get(path).promise; return { ok: true, status: 200, json: async () => path === '/api/session' ? session(state.user) : {} } }
  const api = evaluate(read('src/api.ts'), imports, { fetch, localStorage: { getItem: () => 'p-current' }, window: { dispatchEvent: event => events.push(event) }, CustomEvent: LocalEvent })
  return { ...api, calls, events, state, pending }
}
const failResponse = (status = 403, code = 'password_change_required') => ({ ok: false, status, json: async () => ({ error: { code, message: 'First password change required' } }) })
let count = 0
async function test(name, run) { await run(); count++; console.log('✓ ' + name) }

await test('new-password policy counts Unicode code points and UTF-8 bytes, not UTF-16 units', () => {
  assert.equal(policy.initialPasswordError('old', '中文密码测试A1', '中文密码测试A1'), '')
  const exact = '界'.repeat(23) + 'ab1'; assert.equal(new TextEncoder().encode(exact).length, 72); assert.equal(policy.initialPasswordError('old', exact, exact), '')
  assert(policy.initialPasswordError('old', exact + 'a', exact + 'a'))
  assert(policy.initialPasswordError('old', 'A1😀😀😀😀😀', 'A1😀😀😀😀😀'))
  assert.equal(policy.initialPasswordError('old', 'A1😀😀😀😀😀😀', 'A1😀😀😀😀😀😀'), '')
})
await test('new-password policy rejects missing current password, weak, unchanged, control, and mismatched values', () => {
  for (const args of [['', 'Personal2027!', 'Personal2027!'], ['old', 'OnlyLetters', 'OnlyLetters'], ['old', '12345678', '12345678'], ['old', 'shortA1', 'shortA1'], ['old', 'Password1\n', 'Password1\n'], ['old', 'Password1\u007f', 'Password1\u007f'], ['Personal2027!', 'Personal2027!', 'Personal2027!'], ['old', 'Personal2027!', 'Personal2028!']]) assert(policy.initialPasswordError(...args), JSON.stringify(args))
})
await test('mounted form uses explicit account name, native password managers, and empty memory-only password fields', () => {
  const m = mountForm(); assertEmpty(m.c); assert.match(text(m.container), /原姓名/)
  const inputs = descendants(m.container).filter(n => n.tag === 'input'); assert.equal(inputs.length, 3)
  assert(inputs.every(n => n.props.type === 'password' && n.props.required !== undefined)); assert.equal(inputs[0].props.autocomplete, 'current-password'); assert.equal(inputs[1].props.autocomplete, 'new-password')
  assert.doesNotMatch(source, /localStorage|sessionStorage|document\.cookie/); m.stop()
})
await test('invalid form never submits; valid pending form disables repeated submission and logout', async () => {
  const hold = deferred(), m = mountForm({}, () => hold.promise); await m.c.submit(); assert.equal(m.calls.length, 0); assert(m.c.error)
  fill(m.c); const promise = m.c.submit(); await m.c.submit(); await flush(); assert.equal(m.calls.length, 1); assert(m.c.submitting)
  assert.equal(m.calls[0].path, '/auth/initial-password'); assert.equal(m.calls[0].options.method, 'POST'); assert.deepEqual(JSON.parse(m.calls[0].options.body), { ...m.c.form })
  assert(m.calls[0].options.signal instanceof AbortSignal); assert(descendants(m.container).filter(n => n.tag === 'input').every(n => n.props.disabled)); assert(descendants(m.container).find(n => n.props.class === 'initial-password-logout').props.disabled)
  hold.resolve({ changed: true, requiresLogin: true }); await promise; assert.deepEqual(m.events, ['completed']); assertEmpty(m.c); assert.equal(m.c.submitting, false); m.stop()
})
await test('success requires both explicit confirmation flags and otherwise retains the form for retry', async () => {
  for (const response of [{}, { changed: true }, { requiresLogin: true }, { changed: false, requiresLogin: true }, { changed: 'true', requiresLogin: true }]) {
    const m = mountForm({}, async () => response); fill(m.c); await m.c.submit(); assert.deepEqual(m.events, []); assert.equal(m.c.form.newPassword, 'Personal2027!'); assert.match(m.c.error, /改密结果未确认/); assert.equal(m.c.submitting, false); m.stop()
  }
})
await test('API failure is visible, retains passwords only in the live form, and never emits completion', async () => {
  const m = mountForm({}, async () => { throw Error('Current password is incorrect') }); fill(m.c); await m.c.submit(); await flush()
  assert.equal(m.c.error, 'Current password is incorrect'); assert.match(text(m.container), /Current password is incorrect/); assert.equal(m.c.form.currentPassword, 'Temporary-fixture'); assert.deepEqual(m.events, []); assert.equal(m.c.submitting, false); m.stop(); assertEmpty(m.c)
})
await test('blocked and impersonated props fail closed; impersonation renders only an explicit return-to-admin path', async () => {
  for (const props of [{ blocked: true, blockReason: 'Identity changed' }, { impersonated: true }]) {
    const m = mountForm(props); fill(m.c); await m.c.submit(); assert.equal(m.calls.length, 0); assert.equal(descendants(m.container).filter(n => n.tag === 'form').length, 0)
    if (props.impersonated) { const back = descendants(m.container).find(n => n.tag === 'button' && text(n) === '返回管理员'); assert(back); back.props.onClick(); assert.deepEqual(m.events, ['returnAdministrator']); m.props.returning = true; await flush(); assert(descendants(m.container).find(n => n.tag === 'button' && text(n) === '返回管理员').props.disabled) }
    else { descendants(m.container).find(n => n.tag === 'button' && text(n) === '刷新').props.onClick(); assert.deepEqual(m.events, ['refresh']) }
    m.stop()
  }
})
await test('account change or a newly blocked/impersonated identity aborts in-flight work and clears secret input', async () => {
  for (const change of [m => { m.props.user = { id: 'u-b', name: 'New account' } }, m => { m.props.blocked = true }, m => { m.props.impersonated = true }]) {
    const hold = deferred(), m = mountForm({}, () => hold.promise); fill(m.c); const promise = m.c.submit(); change(m); await flush(); assert.equal(m.calls[0].options.signal.aborted, true); assertEmpty(m.c)
    hold.resolve({ changed: true, requiresLogin: true }); await promise; assert.deepEqual(m.events, []); assertEmpty(m.c); m.stop()
  }
})
await test('unmount aborts the request and a late success cannot emit or restore disposed passwords', async () => {
  const hold = deferred(), m = mountForm({}, () => hold.promise); fill(m.c); const promise = m.c.submit(); m.stop(); assert(m.calls[0].options.signal.aborted); assertEmpty(m.c)
  hold.resolve({ changed: true, requiresLogin: true }); await promise; await m.c.submit(); assert.deepEqual(m.events, []); assert.equal(m.calls.length, 1); assertEmpty(m.c)
})
await test('App fetches only minimal session while required or disabled, including a project deep link', async () => {
  for (const flags of [{ mustChangePassword: true }, { operationDisabled: true, mustChangePassword: true }]) {
    const value = session('u-a', flags); value.project = { id: '', name: '', code: '' }
    const m = appHarness(async path => { assert.equal(path, '/session'); return value }, { query: { project: 'not-authorized-yet' } }); assert.equal(await m.load(), true)
    assert.deepEqual(m.calls.map(call => call.path), ['/session']); assert.equal(m.workspace.mustChangePassword, true); assert.deepEqual(m.workspace.projects, []); assert.equal(m.workspace.unread, 0)
    await m.refreshUnread(); assert.equal(m.calls.length, 1); for (const permission of ['canManageOrganization', 'canManageProject', 'canOpenOrganization', 'canViewReports']) assert.equal(m.workspace[permission], false, permission)
    assert.equal(m.storage.get('devflow-project'), undefined); m.stop()
  }
})
await test('normal App load still retrieves business context and permission gates recover only from verified session', async () => {
  const m = appHarness(async path => path === '/session' ? session() : path === '/projects' ? { items: [{ id: 'p', status: 'active' }] } : path === '/notifications/unread-count' ? { unread: 2 } : {})
  assert.equal(await m.load(), true); assert.deepEqual(m.calls.map(c => c.path).sort(), ['/session', '/projects', '/notifications/unread-count', '/preferences/display'].sort()); assert(m.workspace.canManageOrganization)
  m.initialPasswordRequired(new LocalEvent('devflow-password-change-required', { detail: { userId: 'u-other' } })); assert.equal(m.workspace.mustChangePassword, false)
  m.initialPasswordRequired(new LocalEvent('devflow-password-change-required', { detail: { userId: 'u-a' } })); assert.equal(m.workspace.mustChangePassword, true); assert.equal(m.workspace.canManageOrganization, false)
  m.passwordChangeComplete(); assert.equal(m.session.value, null); assert.equal(m.loginNotice.value, '密码已修改，请使用新密码重新登录'); m.stop()
})
await test('App renders disabled gate before forced-password gate, and both before any business RouterView', () => {
  const d = parse(appSource).descriptor, s = compileScript(d, { id: 'initial-app' }); assert.deepEqual(compileTemplate({ source: d.template.content, filename: 'App.vue', id: 'initial-app', compilerOptions: { bindingMetadata: s.bindings } }).errors, [])
  assert(d.template.content.indexOf('v-else-if="workspace.operationDisabled"') < d.template.content.indexOf('v-else-if="workspace.mustChangePassword"'))
  assert(d.template.content.indexOf('v-else-if="workspace.mustChangePassword"') < d.template.content.indexOf('class="[\'app-shell'))
  assert.match(d.template.content, /:impersonated="!!session\.impersonation"/); assert.match(d.template.content, /@return-administrator="stopImpersonation"/)
})
await test('real api sends verified expected-user header for initial password even though other auth routes omit it', async () => {
  const a = apiHarness(); await a.api('/session'); await a.api('/auth/initial-password', { method: 'POST', body: '{}' }); const req = a.calls.at(-1)
  assert.equal(req.options.headers.get('X-DevFlow-Expected-User'), 'u-a'); assert.equal(req.options.headers.get('X-DevFlow-Project'), 'p-current'); assert.equal(req.options.headers.get('Content-Type'), 'application/json'); assert.equal(req.options.credentials, 'same-origin')
  await a.api('/auth/impersonation/stop', { method: 'POST', body: '{}' }); assert.equal(a.calls.at(-1).options.headers.get('X-DevFlow-Expected-User'), null)
})
await test('current initial-password-required response emits account-scoped event and preserves API error', async () => {
  const a = apiHarness(); await a.api('/session'); const hold = deferred(); a.pending.set('/api/requirements', hold)
  const promise = assert.rejects(a.api('/requirements'), error => error instanceof a.APIError && error.status === 403 && error.code === 'password_change_required'); hold.resolve(failResponse()); await promise
  assert.equal(a.events.length, 1); assert.equal(a.events[0].type, 'devflow-password-change-required'); assert.deepEqual(a.events[0].detail, { userId: 'u-a' })
})
await test('late 403 from another account never freezes newly logged-in account; its current 403 still does', async () => {
  const a = apiHarness(); await a.api('/session'); const old = deferred(); a.pending.set('/api/requirements', old)
  const promise = assert.rejects(a.api('/requirements'), error => error.code === 'password_change_required')
  await a.api('/auth/logout', { method: 'POST', body: '{}' }); a.state.user = 'u-b'; await a.api('/auth/login', { method: 'POST', body: '{}' }); await a.api('/session')
  assert.deepEqual(a.events.map(x=>x.type), ['devflow-auth-session-ended','devflow-auth-session-ended']); a.events.length=0
  old.resolve(failResponse()); await promise; assert.equal(a.events.length, 0)
  const current = deferred(); a.pending.set('/api/my-work', current); const next = assert.rejects(a.api('/my-work')); current.resolve(failResponse()); await next; assert.deepEqual(a.events[0].detail, { userId: 'u-b' })
})
await test('same-account re-login increments identity generation so previous session 403 and 401 do not affect it', async () => {
  for (const [status, code] of [[403, 'password_change_required'], [403, 'account_disabled'], [401, 'unauthorized']]) {
    const a = apiHarness(); await a.api('/session'); const old = deferred(); a.pending.set('/api/requirements', old); const promise = assert.rejects(a.api('/requirements'))
    await a.api('/auth/logout', { method: 'POST', body: '{}' }); await a.api('/auth/login', { method: 'POST', body: '{}' }); await a.api('/session')
    assert.deepEqual(a.events.map(x=>x.type), ['devflow-auth-session-ended','devflow-auth-session-ended']); a.events.length=0
    old.resolve(failResponse(status, code)); await promise; assert.equal(a.events.length, 0, code)
    await a.api('/auth/initial-password', { method: 'POST', body: '{}' }); assert.equal(a.calls.at(-1).options.headers.get('X-DevFlow-Expected-User'), 'u-a')
  }
})
await test('late successful session response or JSON body cannot replace or freeze a newer verified identity', async () => {
  for (const delay of ['response', 'body']) {
    const a = apiHarness(); await a.api('/session'); const old = deferred()
    a.pending.set('/api/session', delay === 'response' ? old : { promise: Promise.resolve({ ok: true, status: 200, json: () => old.promise }) })
    const promise = assert.rejects(a.api('/session'), error => error instanceof a.APIError && error.status === 409 && error.code === 'request_superseded')
    await flush(); a.pending.delete('/api/session')
    await a.api('/auth/logout', { method: 'POST', body: '{}' }); a.state.user = 'u-b'; await a.api('/auth/login', { method: 'POST', body: '{}' }); await a.api('/session')
    assert.deepEqual(a.events.map(x=>x.type), ['devflow-auth-session-ended','devflow-auth-session-ended']); a.events.length=0
    old.resolve(delay === 'response' ? { ok: true, status: 200, json: async () => session('u-a') } : session('u-a'))
    await promise; assert.equal(a.events.length, 0, delay)
    await a.api('/auth/initial-password', { method: 'POST', body: '{}' }); assert.equal(a.calls.at(-1).options.headers.get('X-DevFlow-Expected-User'), 'u-b')
    assert.equal((await a.api('/session')).user.id, 'u-b')
  }
})
await test('late successful download Blob is discarded after cross-account or same-account re-login without a freeze event', async () => {
  for (const nextUser of ['u-b', 'u-a']) {
    const a = apiHarness(); await a.api('/session'); const oldBlob = deferred(), path = '/requirements/1/export/pdf'
    a.pending.set('/api' + path, { promise: Promise.resolve({ ok: true, status: 200, blob: () => oldBlob.promise }) })
    const promise = assert.rejects(a.apiDownload(path), error => error instanceof a.APIError && error.code === 'request_superseded')
    await flush(); await a.api('/auth/logout', { method: 'POST', body: '{}' }); a.state.user = nextUser; await a.api('/auth/login', { method: 'POST', body: '{}' }); await a.api('/session')
    assert.deepEqual(a.events.map(x=>x.type), ['devflow-auth-session-ended','devflow-auth-session-ended']); a.events.length=0
    oldBlob.resolve(new Blob(['old-session-private-report'])); await promise; assert.equal(a.events.length, 0, nextUser)
    a.pending.set('/api' + path, { promise: Promise.resolve({ ok: true, status: 200, blob: async () => new Blob(['current-authorized-report']) }) })
    assert.equal(await (await a.apiDownload(path)).text(), 'current-authorized-report'); assert.equal(a.calls.at(-1).options.headers.get('X-DevFlow-Expected-User'), nextUser)
  }
})
console.log(`Passed ${count} first-password form, workspace gate, and request-identity regressions.`)

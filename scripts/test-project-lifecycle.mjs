import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import ts from 'typescript'
import * as Vue from 'vue'
import { parse, compileScript, compileTemplate, compileStyle } from 'vue/compiler-sfc'

const read = path => readFileSync(new URL('../' + path, import.meta.url), 'utf8')
const source = read('src/views/Projects.vue'), { descriptor } = parse(source)
const active = { id: 'prj_one', code: 'ONE', name: '同名项目', status: 'active', canManage: true, canRestore: false, canDelete: false }
const archived = { ...active, id: 'prj_two', code: 'TWO', status: 'archived', canManage: false, canRestore: true, canDelete: true, requirements: 20, defects: 3, sprints: 2 }
const deferred = () => { let resolve, reject; const promise = new Promise((a, b) => { resolve = a; reject = b }); return { promise, resolve, reject } }
function mount(handler = async () => ({ items: [active, archived], canCreate: true, canViewArchived: true }), query = {}, options = {}) {
  const calls = [], events = [], mounts = [], unmounts = [], scope = Vue.effectScope(), storage = new Map([['devflow-project', 'old-project'], ...(options.storage || [])]), listeners = new Map()
  const layoutScope = Vue.ref(options.layoutScope || 'tenant:user-a')
  const route = Vue.reactive({ query }), location = { href: '' }, exports = {}
  const imports = {
    vue: { ...Vue, onMounted: fn => mounts.push(fn), onBeforeUnmount: fn => unmounts.push(fn) },
    'vue-router': { useRoute: () => route, useRouter: () => ({ replace: async value => { route.query = value.query } }) },
    '../api': { api: async (path, options) => { calls.push({ path, options }); return handler(path, options) } },
    '../i18n': { t: text => text, locale: Vue.ref('zh-CN'), formatDate: text => text },
    '../layoutScope': { layoutScope },
  }
  const window = { addEventListener: (name, fn) => listeners.set(name, fn), removeEventListener: name => listeners.delete(name), dispatchEvent: event => events.push(event) }
  const code = ts.transpileModule(descriptor.scriptSetup.content + '\nexport { items, status, canCreate, canViewArchived, filtered, editing, openEdit, patch, load, enter, action, beginAction, closeAction, performAction, confirmation, saving, error, notice, locked, selectStatus, favorites, toggleFav }', { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText
  scope.run(() => new Function('require', 'exports', 'window', 'localStorage', 'location', 'CustomEvent', code)(id => imports[id] || {}, exports, window, { getItem: key => storage.get(key) ?? null, setItem: (key, value) => storage.set(key, value), removeItem: key => storage.delete(key) }, location, class { constructor(type) { this.type = type } }))
  return { ...exports, calls, events, storage, route, location, listeners, layoutScope, async start() { for (const fn of mounts) await fn() }, stop() { for (const fn of unmounts) fn(); scope.stop() } }
}
let count = 0
async function test(name, run) { await run(); count++; console.log('✓ ' + name) }

await test('only active projects appear for ordinary members; admins can browse archived projects but never deleted records', async () => {
  const admin = mount(); await admin.load(); assert.deepEqual(admin.filtered.value.map(x => x.id), ['prj_one']); admin.selectStatus('archived'); await Vue.nextTick(); assert.deepEqual(admin.filtered.value.map(x => x.id), ['prj_two']); admin.stop()
  const member = mount(async () => ({ items: [active, archived, { ...active, status: 'deleted' }], canCreate: false, canViewArchived: false })); await member.load(); member.selectStatus('archived'); assert.equal(member.status.value, 'active'); assert.equal(member.items.value.length, 1); assert.equal(member.canCreate.value, false); member.stop()
})
await test('enterprise shortcut opens the archived tab without entering the archived business project', async () => {
  const m = mount(undefined, { status: 'archived' }); await m.start(); assert.equal(m.status.value, 'archived'); assert.equal(m.filtered.value[0].id, archived.id); assert.equal(m.location.href, ''); assert.equal(m.storage.get('devflow-project'), 'old-project'); m.stop()
})
await test('archive and restore use explicit target IDs and refresh the shell only after confirmed success', async () => {
  for (const [project, kind, expected] of [[active, 'archive', 'archived'], [archived, 'restore', 'active']]) {
    const m = mount(async (path, options) => options?.method === 'POST' ? { status: expected } : { items: [], canCreate: true, canViewArchived: true })
    m.beginAction(project, kind); assert.equal(m.calls.length, 0); await m.performAction(); assert.equal(m.calls[0].path, `/projects/${project.id}/${kind}`); assert.equal(m.calls[0].options.method, 'POST'); assert.deepEqual(JSON.parse(m.calls[0].options.body), {}); assert.equal(m.action.value, null); assert(m.notice.value); assert(m.events.some(event => event.type === 'devflow-project-list-changed')); m.stop()
  }
})
await test('deletion requires exact code confirmation and never selects a same-name project by name or table position', async () => {
  const m = mount(async (path, options) => options?.method === 'DELETE' ? { status: 'deleted', deleted: true } : { items: [], canViewArchived: true })
  m.beginAction(archived, 'delete'); for (const code of ['', 'two', ' TWO ', 'ONE']) { m.confirmation.value = code; await m.performAction(); assert.equal(m.calls.length, 0); assert(m.error.value) }
  m.items.value = [active]; m.confirmation.value = 'TWO'; await m.performAction(); assert.equal(m.calls[0].path, '/projects/prj_two'); assert.equal(m.calls[0].options.method, 'DELETE'); assert.deepEqual(JSON.parse(m.calls[0].options.body), { confirmCode: 'TWO' }); assert.equal(m.confirmation.value, ''); assert.equal(m.action.value, null); m.stop()
})
await test('UI guards prevent deletion of active projects and restore/delete without server-issued capabilities', async () => {
  const m = mount(); for (const [project, kind] of [[active, 'delete'], [active, 'restore'], [archived, 'archive'], [{ ...archived, canDelete: false }, 'delete'], [{ ...archived, canRestore: false }, 'restore']]) { m.beginAction(project, kind); assert.equal(m.action.value, null); await m.performAction(); assert.equal(m.calls.length, 0) }; m.stop()
})
await test('failed or unconfirmed writes keep the confirmation open and do not report success', async () => {
  for (const handler of [async () => { throw Error('denied') }, async () => ({ deleted: false })]) {
    const m = mount(handler); m.beginAction(archived, 'delete'); m.confirmation.value = 'TWO'; await m.performAction(); assert.equal(m.action.value.project.id, archived.id); assert.equal(m.confirmation.value, 'TWO'); assert.equal(m.saving.value, false); assert(m.error.value); assert.equal(m.notice.value, ''); assert.equal(m.events.length, 0); m.stop()
  }
})
await test('pending deletion prevents duplicate requests, dialog closure and switching targets', async () => {
  const pending = deferred(), m = mount(async (path, options) => options?.method === 'DELETE' ? pending.promise : { items: [], canViewArchived: true })
  m.beginAction(archived, 'delete'); m.confirmation.value = 'TWO'; const first = m.performAction(); await m.performAction(); m.closeAction(); m.beginAction(active, 'archive'); assert.equal(m.calls.length, 1); assert.equal(m.action.value.kind, 'delete'); pending.resolve({ status: 'deleted' }); await first; assert.equal(m.action.value, null); m.stop()
})
await test('identity changes clear confirmation and abort in-flight writes without reporting a late success', async () => {
  const pending = deferred(), m = mount(async (path, options) => options?.method === 'DELETE' ? pending.promise : { items: [], canViewArchived: true }); await m.start()
  m.beginAction(archived, 'delete'); m.confirmation.value = 'TWO'; const first = m.performAction(); const request = m.calls.at(-1); m.listeners.get('devflow-identity-changed')(); assert(request.options.signal.aborted); assert.equal(m.action.value, null); assert.equal(m.confirmation.value, ''); assert(m.locked.value); pending.resolve({ status: 'deleted' }); await first; assert.equal(m.notice.value, ''); assert.equal(m.events.length, 0); m.beginAction(archived, 'restore'); assert.equal(m.action.value, null); m.stop()
})
await test('unmount cancels old response effects and a later list request wins over an older one', async () => {
  const pending = deferred(), m = mount(() => pending.promise); const first = m.load(); m.stop(); pending.resolve({ items: [active], canCreate: true }); await first; assert.equal(m.items.value.length, 0)
  const slow = deferred(); let n = 0; const next = mount(() => ++n === 1 ? slow.promise : Promise.resolve({ items: [active], canCreate: true })); const stale = next.load(); await next.load(); slow.resolve({ items: [archived], canCreate: false, canViewArchived: true }); await stale; assert.equal(next.items.value[0].id, active.id); assert(next.canCreate.value); next.stop()
})
await test('archived or deleted project cards never visit or switch the cached project', async () => {
  const m = mount(); for (const project of [archived, { ...archived, status: 'deleted' }]) await m.enter(project); assert.equal(m.calls.length, 0); assert.equal(m.location.href, ''); assert.equal(m.storage.get('devflow-project'), 'old-project'); m.stop()
})
await test('project favorites are isolated by verified tenant/account scope and never adopt legacy shared browser data', () => {
  const legacy = JSON.stringify(['prj_two']), keyA = 'devflow-layout:v1:tenant:user-a:projects.favorites', keyB = 'devflow-layout:v1:tenant:user-b:projects.favorites'
  const m = mount(undefined, {}, { storage: [['devflow-favorites', legacy], [keyA, JSON.stringify(['prj_one'])]] })
  assert.deepEqual(m.favorites.value, ['prj_one']); assert.equal(m.storage.get('devflow-favorites'), legacy)
  m.toggleFav('prj_two'); assert.deepEqual(JSON.parse(m.storage.get(keyA)), ['prj_one', 'prj_two'])
  m.layoutScope.value = 'tenant:user-b'; assert.deepEqual(m.favorites.value, [])
  m.toggleFav('prj_two'); assert.deepEqual(JSON.parse(m.storage.get(keyB)), ['prj_two'])
  m.layoutScope.value = 'tenant:user-a'; assert.deepEqual(m.favorites.value, ['prj_one', 'prj_two']); assert.equal(m.storage.get('devflow-favorites'), legacy); m.stop()
})
await test('editing active projects uses a copy and cannot edit an archived project', () => {
  const m = mount(); m.openEdit(archived); assert.equal(m.editing.value, null); m.openEdit(active); m.editing.value.name = 'draft'; assert.equal(active.name, '同名项目'); m.stop()
})
await test('actual templates and theme-aware confirmation styles compile with mobile and keyboard controls', () => {
  const script = compileScript(descriptor, { id: 'projects-life' }); assert.deepEqual(compileTemplate({ id: 'projects-life', filename: 'Projects.vue', source: descriptor.template.content, compilerOptions: { bindingMetadata: script.bindings } }).errors, [])
  assert.deepEqual(compileStyle({ id: 'projects-life', filename: 'Projects.vue', source: descriptor.styles.map(x => x.content).join('\n'), scoped: true }).errors, [])
  assert.match(source, /<OrganizationModal v-if="action"/); assert.match(source, /form="project-lifecycle-confirm"/); assert.match(source, /confirmation !== action.project.code/); assert.match(source, /min-height:44px/); assert.match(source, /var\(--danger/)
  const organization = read('src/views/Organization.vue'); assert.match(organization, /key==='archived-projects'\?'\/projects\?status=archived'/); assert.match(organization, /context.value.isTenantAdmin\?\[\{key:'archived-projects'/)
})
console.log(`Passed ${count} project archive/restore/delete UI regression tests.`)

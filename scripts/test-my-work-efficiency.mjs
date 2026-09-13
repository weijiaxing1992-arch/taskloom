import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import ts from 'typescript'
import * as Vue from 'vue'
import { parse, compileScript } from 'vue/compiler-sfc'
import { workflow } from './workflow-test-support.mjs'

const read = path => readFileSync(new URL('../' + path, import.meta.url), 'utf8')
const transpile = source => ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText
const efficiency = {}
new Function('exports', transpile(read('src/myWorkEfficiency.ts')))(efficiency)
const compiled = transpile(compileScript(parse(read('src/views/MyWork.vue')).descriptor, { id: 'my-work-efficiency' }).content)
const { compareWorkAttention, personalWorkKey, readMyWorkPreferences, saveMyWorkPreferences, clearMyWorkPreferences } = efficiency
const memory = () => { const values = new Map(); return { values, getItem: key => values.get(key) ?? null, setItem: (key, value) => values.set(key, value), removeItem: key => values.delete(key) } }
const deferred = () => { let resolve, reject; const promise = new Promise((a, b) => { resolve = a; reject = b }); return { promise, resolve, reject } }
const settle = async () => { for (let i = 0; i < 8; i++) await Promise.resolve(); await Vue.nextTick() }
const preferences = { category: 'overdue', q: '客户配置', type: '需求', project: 'p-two', view: 'assigned', status: '', sprintId: '12', sort: 'attention', order: 'desc', filtersExpanded: true, assignedCategory: 'active', selectedKey: '["p-two","需求",9]', scrollTop: 720 }
function setup({ storage = memory(), scope = 'tenant:person', handler = async () => ({ items: [], counts: {} }) } = {}) {
  const mounted = [], unmounts = [], calls = [], navigations = [], listeners = new Map(), timers = new Map(), runtime = Vue.effectScope()
  let serial = 0
  const identity = Vue.ref(scope), local = memory(), win = { sessionStorage: storage,
    addEventListener(name, fn) { if (!listeners.has(name)) listeners.set(name, new Set()); listeners.get(name).add(fn) },
    removeEventListener(name, fn) { listeners.get(name)?.delete(fn) },
    dispatch(name) { for (const fn of [...(listeners.get(name) || [])]) fn() } }
  local.setItem('devflow-project', 'p-current')
  const component = Vue.defineComponent({ setup: () => () => Vue.h('span') })
  const imports = {
    vue: { ...Vue, onMounted: fn => mounted.push(fn), onBeforeUnmount: fn => unmounts.push(fn) },
    'vue-router': { useRouter: () => ({ push: async url => navigations.push(url) }) },
    '../i18n': { t: value => value, locale: Vue.ref('zh-CN'), formatDate: value => value },
    '../api': { api: (path, options) => { calls.push({ path, options }); return handler(path, options) } },
    '../layoutScope': { layoutScope: identity }, '../myWorkEfficiency': efficiency, '../requirementWorkflow': workflow,
    '../components/Icon.vue': { default: component }, '../components/RequirementCode.vue': { default: component },
    '../components/RequirementListExport.vue': { default: component },
  }
  const exported = {}, location = { href: '' }
  new Function('require', 'exports', 'localStorage', 'window', 'location', 'setTimeout', 'clearTimeout', compiled)(
    id => imports[id], exported, local, win, location, fn => { timers.set(++serial, fn); return serial }, id => timers.delete(id))
  const state = runtime.run(() => exported.default.setup({}, { expose() {} }))
  return { ...state, calls, navigations, win, local, location, identity, storage, timers,
    mount: async () => { for (const fn of mounted) await fn(); await settle() },
    flush: async () => { const queued = [...timers.values()]; timers.clear(); queued.forEach(fn => fn()); await settle() },
    stop: () => { unmounts.forEach(fn => fn()); runtime.stop() } }
}
let count = 0
async function test(name, fn) { await fn(); count++; console.log('✓ ' + name) }

await test('attention order uses existing categories, then priority and real deadlines; never mutates records', () => {
  const items = [
    { id: 1, category: 'completed', priority: 'P0' }, { id: 2, category: 'doing', priority: 'P0' },
    { id: 3, category: 'overdue', priority: 'P2' }, { id: 4, category: 'due', priority: 'P0' },
    { id: 5, category: 'overdue', priority: 'P0', dueDate: '2026-09-08' },
    { id: 6, category: 'overdue', priority: 'P0', dueDate: '' }, { id: 7, category: 'todo', priority: 'P1' },
    { id: 8, category: 'unknown', priority: 'P0' }, { id: 9, category: 'cancelled', priority: 'P0' },
  ].map(item => ({ ...item, type: '需求', projectId: 'p' }))
  const before = JSON.stringify(items)
  assert.deepEqual([...items].sort(compareWorkAttention).map(item => item.id), [5, 6, 3, 4, 7, 2, 8, 1, 9])
  assert.equal(JSON.stringify(items), before)
  assert.notEqual(personalWorkKey({ id: 1, projectId: 'p', type: '需求' }), personalWorkKey({ id: 1, projectId: 'p', type: '缺陷' }))
})
await test('view memory is scoped to verified identity, bounded, expiring, and tolerant of blocked storage', () => {
  const storage = memory(), now = 20000
  saveMyWorkPreferences(storage, 'org:one', preferences, now)
  assert.deepEqual(readMyWorkPreferences(storage, 'org:one', now), preferences)
  assert.equal(readMyWorkPreferences(storage, 'org:two', now), null)
  assert.equal(readMyWorkPreferences(storage, '', now), null)
  assert.equal(readMyWorkPreferences(storage, 'org:one', now + 8 * 60 * 60 * 1000 + 1), null)
  assert.equal(readMyWorkPreferences(storage, 'org:one', now - 1), null)
  for (const patch of [{ scrollTop: -1 }, { sprintId: '../other' }, { sort: 'unknown' }, { q: 'x'.repeat(501) }, { project: null }]) {
    saveMyWorkPreferences(storage, 'org:bad', { ...preferences, ...patch }, now)
    assert.equal(readMyWorkPreferences(storage, 'org:bad', now), null)
  }
  assert.doesNotThrow(() => saveMyWorkPreferences({ setItem() { throw Error('privacy') } }, 'org:one', preferences))
  assert.equal(readMyWorkPreferences({ getItem() { throw Error('privacy') } }, 'org:one'), null)
  clearMyWorkPreferences(storage, 'org:one'); assert.equal(readMyWorkPreferences(storage, 'org:one', now), null)
})
await test('returning restores filters and scroll after fresh permission-scoped data, with one initial summary request', async () => {
  const storage = memory(); saveMyWorkPreferences(storage, 'tenant:person', preferences)
  const m = setup({ storage }), container = { scrollTop: 0 }
  m.workRoot.value = container; await m.mount()
  assert.equal(m.project.value, 'p-two'); assert.equal(m.sprintId.value, '12'); assert.equal(m.q.value, '客户配置')
  assert.equal(m.sort.value, 'attention'); assert.equal(container.scrollTop, 720); assert.equal(m.selectedKey.value, preferences.selectedKey)
  assert.equal(m.calls.filter(call => call.path.startsWith('/my-work?')).length, 1)
  assert.equal(m.timers.size, 0)
  assert.equal(m.calls[0].options.headers['X-TaskLoom-Project'], 'p-current')
  assert.equal(new URLSearchParams(m.calls[0].path.split('?')[1]).get('sprintId'), '12')
  m.stop()
})
await test('opening records selection and scroll, prevents duplicate navigation, and checks cross-project access first', async () => {
  const pending = deferred(), m = setup({ handler: () => pending.promise })
  m.workRoot.value = { scrollTop: 540 }
  const item = { id: 3, type: '需求', projectId: 'p-other', url: '/requirements?req=3', favorited: true }
  const opening = m.go(item); await m.go(item)
  assert.equal(m.calls.length, 1); assert.equal(m.navigationKey.value, personalWorkKey(item))
  assert.equal(readMyWorkPreferences(m.storage, 'tenant:person'), null)
  pending.resolve({ favorited: true }); await opening
  assert.equal(m.location.href, item.url); assert.equal(m.local.getItem('devflow-project'), 'p-other')
  assert.equal(readMyWorkPreferences(m.storage, 'tenant:person').selectedKey, personalWorkKey(item))
  assert.equal(readMyWorkPreferences(m.storage, 'tenant:person').scrollTop, 540)
  m.stop()
})
await test('IME input does not send partial searches; Enter cancels debounce and stale requests are aborted', async () => {
  const pending = deferred(), m = setup({ handler: () => pending.promise })
  const first = m.load(); m.compositionStart(); m.q.value = 'chan'; await m.flush()
  assert.equal(m.calls.length, 1); assert.equal(m.calls[0].options.signal.aborted, true)
  m.q.value = '产品'; m.compositionEnd(); m.submitSearch({ isComposing: false }); await m.flush()
  assert.equal(m.calls.length, 2)
  assert.equal(new URLSearchParams(m.calls[1].path.split('?')[1]).get('q'), '产品')
  pending.resolve({ items: [], counts: {} }); await first; await settle(); assert.equal(m.loading.value, false); m.stop()
})
await test('personal sorting is immediate without repeat API reads; favorites preserve server ordering and fetch semantics', async () => {
  const m = setup(); await m.mount(); m.calls.length = 0
  m.items.value = [{ id: 1, projectId: 'p', type: '需求', category: 'doing', priority: 'P0' }, { id: 2, projectId: 'p', type: '需求', category: 'overdue', priority: 'P2' }]
  assert.deepEqual(m.sortedItems.value.map(item => item.id), [2, 1])
  m.sort.value = 'code'; m.order.value = 'asc'; await m.flush()
  assert.deepEqual(m.sortedItems.value.map(item => item.id), [1, 2]); assert.equal(m.calls.length, 0)
  m.selectView('favorites'); await m.flush(); m.calls.length = 0
  m.items.value = [{ id: 9 }, { id: 2 }]; assert.deepEqual(m.sortedItems.value.map(item => item.id), [9, 2])
  m.sort.value = 'favoritedAt'; await m.flush(); assert.equal(m.calls.length, 1); m.stop()
})
await test('identity changes erase navigation memory, abort old reads, and never show late results', async () => {
  const storage = memory(); saveMyWorkPreferences(storage, 'tenant:person', preferences)
  const pending = deferred(), m = setup({ storage, handler: () => pending.promise }), loading = m.load()
  m.identity.value = 'tenant:someone-else'
  assert.equal(m.calls[0].options.signal.aborted, true)
  pending.resolve({ items: [{ id: 999, title: 'old account' }], counts: { all: 1 } }); await loading
  assert.deepEqual(m.items.value, []); assert.equal(readMyWorkPreferences(storage, 'tenant:person'), null)
  await m.go({ id: 1, projectId: 'p-current', url: '/requirements?req=1' }); assert.deepEqual(m.navigations, [])
  m.stop(); assert.equal(readMyWorkPreferences(storage, 'tenant:person'), null)
})
await test('display labels distinguish fallback update times from deadlines and do not invent missing ownership', () => {
  const source = read('src/views/MyWork.vue')
  assert(source.includes("x.dueDate?'截止日期':'更新时间'"))
  assert(source.includes('<template v-if="x.role">'))
  assert(source.includes('@compositionstart="compositionStart"'))
  assert(source.includes(':data-work-key="personalWorkKey(x)"'))
  assert(source.includes('minmax(0,1fr)')); assert(!source.includes('behavior: \'smooth\''))
})
console.log(`Passed ${count} personal-work efficiency and scope regressions.`)

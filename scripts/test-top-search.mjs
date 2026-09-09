import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import ts from 'typescript'
import * as Vue from 'vue'
import * as Pinia from 'pinia'
const read = path => readFileSync(new URL('../' + path, import.meta.url), 'utf8')
function evaluate(source, imports = {}, globals = {}) { const exports = {}; new Function('require', 'exports', ...Object.keys(globals), ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText)(id => imports[id] || {}, exports, ...Object.values(globals)); return exports }
const helpers = evaluate(read('src/topSearch.ts')), storeModule = evaluate(read('src/stores/workspace.ts'), { vue: Vue, pinia: Pinia }, { localStorage: { getItem: () => 'p' } })
const result = (id = 7, extra = {}) => ({ id, type: '需求', title: 'Original user title ' + id, code: 'REQ-' + id, projectId: 'p', projectName: 'Product project', status: '规划中', snippet: 'Untranslated user content', ...extra })
const deferred = () => { let resolve, reject; const promise = new Promise((a, b) => { resolve = a; reject = b }); return { promise, resolve, reject } }
const flush = async () => { for (let i = 0; i < 8; i++) { await Promise.resolve(); await Vue.nextTick() } }
function fixture(handler = async () => ({ items: [result()], total: 1 }), navigation = async () => undefined) {
 const scope = Vue.effectScope(), store = storeModule.useWorkspaceStore(Pinia.createPinia()), props = Vue.reactive({ disabled: false }), route = Vue.reactive({ path: '/iterations', fullPath: '/iterations?sprint=22', query: { sprint: '22', tab: 'list' } }), calls = [], navigations = [], unmount = [], mounted = [], timers = new Map(), events = new EventTarget(), document = new EventTarget(); let timerID = 0
 store.acceptContext({ session: { tenant: { id: 't', name: 'Team' }, user: { id: 'me', name: 'Me', role: 'product' }, project: { id: 'p', name: 'Product project', code: 'P' } }, projects: [{ id: 'p', name: 'Product project' }], unread: 0 })
 const imports = { vue: { ...Vue, useId: () => 'test', onMounted: fn => mounted.push(fn), onBeforeUnmount: fn => unmount.push(fn) }, 'vue-router': { useRoute: () => route, useRouter: () => ({ push: async target => { navigations.push(target); return navigation(target) } }) }, '../stores/workspace': { useWorkspaceStore: () => store }, '../topSearch': helpers, '../api': { api: async (path, options) => { calls.push({ path, options }); return handler(path, options) } }, '../i18n': { t: value => value } }
 const source = read('src/components/TopSearch.vue').match(/<script setup[^>]*>([\s\S]*?)<\/script>/)[1], exposed = 'root,input,query,opened,items,total,loading,error,searched,activeIndex,navigating,composing,focus,openPopup,search,schedule,close,clear,choose,more,keydown,pointerdown,available'
 const m = scope.run(() => evaluate(source + '\nexport {' + exposed + '}', imports, { defineProps: () => props, defineExpose() {}, window: events, document, setTimeout: fn => { timers.set(++timerID, fn); return timerID }, clearTimeout: id => timers.delete(id) }))
 mounted.forEach(fn => fn())
 return { ...m, props, store, route, calls, navigations, events, timers, async runTimers() { const callbacks = [...timers.values()]; timers.clear(); callbacks.forEach(fn => fn()); await flush() }, stop() { unmount.forEach(fn => fn()); scope.stop(); store.$dispose() } }
}
const keyboard = key => ({ key, prevented: false, stopped: false, preventDefault() { this.prevented = true }, stopImmediatePropagation() { this.stopped = true } })
let count = 0; async function test(name, run) { await run(); count++; console.log('✓ ' + name) }
await test('focusing opens a dropdown without navigation or an empty broad search; typing is debounced and scope is explicit', async () => {
 const m = fixture(); m.input.value = { focus() {}, closest: () => null }; assert.equal(await m.focus(), true); assert.equal(m.opened.value, true); assert.equal(m.calls.length, 0); assert.equal(m.navigations.length, 0)
 m.query.value = 'alpha'; m.query.value = 'beta'; assert.equal(m.calls.length, 0); assert.equal(m.timers.size, 1); await m.runTimers()
 const params = new URL(m.calls[0].path, 'https://local.test').searchParams; assert.equal(params.get('q'), 'beta'); assert.equal(params.get('project'), 'p'); assert.equal(params.get('limit'), '12'); assert.equal(m.calls[0].options.headers['X-DevFlow-Project'], 'p'); assert.equal(m.items.value.length, 1); m.stop()
})
await test('superseded searches abort their request and a late old response never replaces newer results', async () => {
 const old = deferred(), m = fixture(path => path.includes('alpha') ? old.promise : Promise.resolve({ items: [result(8)], total: 1 }))
 m.openPopup(); m.query.value = 'alpha'; await m.runTimers(); const oldSignal = m.calls[0].options.signal; m.query.value = 'beta'; assert.equal(oldSignal.aborted, true); await m.runTimers(); assert.equal(m.items.value[0].id, 8)
 old.resolve({ items: [result(99)], total: 1 }); await flush(); assert.equal(m.items.value[0].id, 8); m.stop()
})
await test('IME composing and whitespace inputs do not send incomplete queries', async () => {
 const m = fixture(); m.openPopup(); m.composing.value = true; m.query.value = '拼'; await m.runTimers(); assert.equal(m.calls.length, 0)
 m.composing.value = false; m.schedule(); await m.runTimers(); assert.equal(m.calls.length, 1)
 m.query.value = '   '; await m.runTimers(); assert.equal(m.calls.length, 1); assert.equal(m.items.value.length, 0); assert.equal(m.loading.value, false); m.stop()
})
await test('all result types use fixed internal routes and requirement selection preserves the active iteration backdrop', async () => {
 const m = fixture(async () => ({ items: [result(7, { url: 'https://evil.test' })], total: 1 })); m.openPopup(); m.query.value = 'title'; await m.runTimers(); await m.choose(m.items.value[0])
 assert.deepEqual(m.navigations[0], { path: '/iterations', query: { sprint: '22', tab: 'list', req: 7 } }); assert.equal(m.opened.value, false)
 for (const [type, path, key] of [['缺陷', '/defects', 'bug'], ['迭代', '/iterations', 'sprint'], ['测试用例', '/tests', 'case'], ['测试计划', '/tests', 'plan']]) { const target = helpers.topSearchTarget(result(8, { type }), '/requirements/new'); assert.equal(target.path, path); assert.equal(target.query[key], 8) }
 assert.deepEqual(helpers.topSearchTarget(result('p', { type: '项目' }), '/'), { path: '/projects', query: { project: 'p' } }); m.stop()
})
await test('cancelled route guards leave the dropdown and search query intact, and cross-project results are never selectable', async () => {
 const m = fixture(undefined, async () => ({ type: 4 })); m.openPopup(); m.query.value = 'draft'; await m.runTimers(); await m.choose(m.items.value[0]); assert.equal(m.opened.value, true); assert.equal(m.query.value, 'draft'); assert.equal(m.navigating.value, false)
 await m.choose(result(9, { projectId: 'other' })); assert.equal(m.navigations.length, 1); m.stop()
})
await test('keyboard navigation wraps, Enter opens only the highlighted result, and Escape stops outer drawer handlers', async () => {
 const m = fixture(async () => ({ items: [result(7), result(8)], total: 2 })); m.openPopup(); m.query.value = 'two'; await m.runTimers()
 const up = keyboard('ArrowUp'); m.keydown(up); assert.equal(m.activeIndex.value, 1); assert.equal(up.prevented, true); m.keydown(keyboard('Enter')); await flush(); assert.equal(m.navigations[0].query.req, 8)
 m.openPopup(); const escape = keyboard('Escape'); m.keydown(escape); assert.equal(escape.prevented, true); assert.equal(escape.stopped, true); assert.equal(m.opened.value, false); m.stop()
})
await test('outside clicks close only the popup while more-results navigation is an explicit cross-project action', async () => {
 const m = fixture(); m.root.value = { contains: node => node === 'inside', querySelector: () => null }; m.openPopup(); m.query.value = 'term'; await m.runTimers(); m.pointerdown({ target: 'inside' }); assert.equal(m.opened.value, true)
 m.pointerdown({ target: 'outside' }); assert.equal(m.opened.value, false); assert.equal(m.query.value, 'term'); assert.equal(m.navigations.length, 0); m.openPopup(); await m.more(); assert.deepEqual(m.navigations[0], { path: '/search', query: { q: 'term' } }); m.stop()
})
await test('errors and malformed/foreign result payloads are retryable and cannot expose another project title', async () => {
 let fail = true; const m = fixture(async () => fail ? { items: [result(7, { projectId: 'other', title: 'Secret title' })], total: 1 } : { items: [], total: 0 })
 m.openPopup(); m.query.value = 'title'; await m.runTimers(); assert.equal(m.items.value.length, 0); assert.match(m.error.value, /搜索结果格式/); assert(!m.error.value.includes('Secret title')); assert.equal(m.loading.value, false)
 fail = false; await m.search(); assert.equal(m.error.value, ''); assert.equal(m.searched.value, true); assert.equal(m.items.value.length, 0); m.stop()
})
await test('project, identity, disabled-account and unmount transitions erase text/results and prevent late private responses', async () => {
 for (const transition of ['project', 'identity', 'disabled', 'unmount']) {
   const pending = deferred(), m = fixture(() => pending.promise); m.openPopup(); m.query.value = 'private query'; await m.runTimers(); const signal = m.calls[0].options.signal
   if (transition === 'project') m.store.session.project.id = 'q'; else if (transition === 'identity') m.events.dispatchEvent(new Event('devflow-identity-changed')); else if (transition === 'disabled') m.store.session.user.operationDisabled = true; else m.stop()
   assert.equal(m.query.value, ''); assert.equal(m.items.value.length, 0); assert.equal(m.opened.value, false); assert.equal(signal.aborted, true)
   pending.resolve({ items: [result()], total: 1 }); await flush(); assert.equal(m.items.value.length, 0); if (transition !== 'unmount') m.stop()
 }
})
await test('the widget stays inside the App inert boundary, exposes focus and does not inherit the retired link styling', () => {
 const source = read('src/components/TopSearch.vue'); assert(!source.includes('Teleport')); assert.match(source, /class="top-search-widget"/); assert(!source.includes('class="top-search"')); assert.match(source, /closest\('\[inert\],\[hidden\]'\)/); assert.match(source, /defineExpose\(\{focus\}\)/); assert.match(source, /:has\(\.top-search-widget\[data-open="true"\]\)/)
})
console.log(`Passed ${count} top search regressions.`)

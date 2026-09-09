import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import ts from 'typescript'
import * as Vue from 'vue'
import { parse, compileScript, compileTemplate } from 'vue/compiler-sfc'
import { workflow } from './workflow-test-support.mjs'
const read = path => readFileSync(new URL('../' + path, import.meta.url), 'utf8')
function evaluate(source, imports = {}, globals = {}) { const exports = {}; new Function('require', 'exports', ...Object.keys(globals), ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText)(name => imports[name] || {}, exports, ...Object.values(globals)); return exports }
const paging = evaluate(read('src/requirementPaging.ts'))
const row = id => ({ id, title: 'Original ' + id, description: 'Body ' + id, customFields: { score: 0 } })
const response = (total, page, size = 200) => ({ total, page, pageSize: size, items: Array.from({ length: Math.min(size, Math.max(0, total - (page - 1) * size)) }, (_, i) => row((page - 1) * size + i + 1)) })
const deferred = () => { let resolve; const promise = new Promise(yes => resolve = yes); return { resolve, promise } }
let count = 0; async function test(name, run) { await run(); count++; console.log('✓ ' + name) }
await test('page response requires explicit total, bounded page size and valid metadata', () => {
  assert.equal(paging.requirementPage(response(20000, 1, 30), 30).items.length, 30)
  for (const invalid of [{ items: [] }, response(1, 0), { ...response(1, 1), total: -1 }, { ...response(1, 1), pageSize: 201 }, { ...response(1, 1), items: [row(1), row(2)] }]) assert.throws(() => paging.requirementPage(invalid, 200))
})
await test('CSV page provider freezes all filters and preserves full cross-page sorted order', async () => {
  const calls = [], signal = new AbortController().signal
  const items = await paging.requirementExportPages('category=客户端&sort=title&order=desc&filters=%5B%5D', async (path, passed) => { calls.push(path); assert.equal(passed, signal); const params = new URLSearchParams(path.split('?')[1]); assert.equal(params.get('category'), '客户端'); assert.equal(params.get('order'), 'desc'); assert.equal(params.get('projection'), 'list'); return response(201, Number(params.get('page'))) }, signal)
  assert.equal(calls.length, 2); assert.deepEqual(items.map(x => x.id), Array.from({ length: 201 }, (_, i) => i + 1)); assert.equal(items.at(-1).description, 'Body 201')
})
await test('20,000 rows are allowed inclusively and larger exports fail before downloading', async () => {
  let calls = 0; const signal = new AbortController().signal
  const items = await paging.requirementExportPages('', async path => { calls++; return response(20000, Number(new URLSearchParams(path.split('?')[1]).get('page'))) }, signal)
  assert.equal(items.length, 20000); assert.equal(calls, 100)
  await assert.rejects(() => paging.requirementExportPages('', async () => response(20001, 1), signal), /20000/)
})
await test('concurrent totals, duplicates, missing rows and wrong pages fail closed', async () => {
  for (const second of [{ ...response(201, 2), total: 202 }, { ...response(201, 2), items: [row(1)] }, { ...response(201, 2), items: [] }, response(201, 1)]) {
    let call = 0; await assert.rejects(() => paging.requirementExportPages('', async () => ++call === 1 ? response(201, 1) : second, new AbortController().signal), /数据发生变化/)
  }
})
await test('cancel before or during page reads prevents continuation and any partial result', async () => {
  const before = new AbortController(); before.abort(); let calls = 0
  await assert.rejects(() => paging.requirementExportPages('', async () => { calls++; return response(1, 1) }, before.signal), { name: 'AbortError' }); assert.equal(calls, 0)
  const during = new AbortController(); await assert.rejects(() => paging.requirementExportPages('', async () => { calls++; during.abort(); return response(201, 1) }, during.signal), { name: 'AbortError' }); assert.equal(calls, 1)
})
function exportFixture(handler, provider) {
  const props = Vue.reactive({ items: [row(1)], total: 201, projectId: 'p', columns: ['title'], recordsProvider: provider }), scope = Vue.effectScope(), locked = Vue.ref(false), calls = [], downloads = [], unmounts = []
  const source = read('src/components/RequirementListExport.vue').match(/<script setup[^>]*>([\s\S]*?)<\/script>/)[1]
  const exports = scope.run(() => evaluate(source + '\nexport {exportList,cancel,busy,error,done}', { vue: { ...Vue, onBeforeUnmount: fn => unmounts.push(fn) }, '../api': { api: async (path, options) => { calls.push({ path, options }); return handler(path, options) } }, '../i18n': { t: value => value }, '../workItemExport': { requirementCSV: items => JSON.stringify(items), downloadFile: (...args) => downloads.push(args) }, '../requirementPaging': paging, './settingsScope': { useSettingsScope: () => ({ locked, current: () => !locked.value }) } }, { defineProps: () => props }))
  return { ...exports, props, locked, calls, downloads, stop() { unmounts.forEach(fn => fn()); scope.stop() } }
}
await test('full-record export avoids per-requirement detail reads and preserves captured project and columns', async () => {
  const pending = deferred(), m = exportFixture(async () => ({ items: [] }), () => pending.promise)
  const operation = m.exportList(); m.props.projectId = 'later'; pending.resolve([row(8), row(2)]); await operation
  assert.equal(m.calls.length, 2); assert(m.calls.every(call => call.options.headers['X-DevFlow-Project'] === 'p')); assert(!m.calls.some(call => /requirements\/\d/.test(call.path))); assert.equal(m.downloads.length, 1); const exported = JSON.parse(await m.downloads[0][0].text()); assert.deepEqual(exported.map(x => x.id), [8, 2]); assert.equal(m.busy.value, false); m.stop()
})
await test('cancel, unmount and identity invalidation discard late export results', async () => {
  for (const action of ['cancel', 'unmount', 'identity']) {
    const pending = deferred(), m = exportFixture(async () => ({ items: [] }), () => pending.promise), operation = m.exportList()
    if (action === 'cancel') m.cancel(); else if (action === 'unmount') m.stop(); else { m.locked.value = true; await Vue.nextTick() }
    pending.resolve([row(1)]); await operation; assert.equal(m.downloads.length, 0); assert.equal(m.calls.length, 0); if (action !== 'unmount') m.stop()
  }
})
function listFixture(handler) {
  const source = read('src/views/Requirements.vue').match(/<script setup[^>]*>([\s\S]*?)<\/script>/)[1], scope = Vue.effectScope(), unmounts = [], calls = []
  const m = scope.run(() => evaluate(source + '\nexport {load,items,listTotal,page,size,paged,pages,loading,error,changePage,filters,session,categoryRecords,advancedFilters,exportRecords}', { vue: { ...Vue, onMounted() {}, onBeforeUnmount: fn => unmounts.push(fn) }, 'vue-router': { useRoute: () => ({ query: {} }), useRouter: () => ({}), onBeforeRouteLeave() {}, onBeforeRouteUpdate() {} }, '../api': { api: async path => { calls.push(path); return handler(path) } }, '../i18n': { t: x => x, categoryLabel: x => x }, '../requirementPaging': paging, '../requirementFields': evaluate(read('src/requirementFields.ts')), '../workItemQuery': evaluate(read('src/workItemQuery.ts')), '../mentions': evaluate(read('src/mentions.ts')), '../requirementWorkflow': workflow, '../layoutScope': { useLayoutBoolean: (_, fallback) => Vue.ref(fallback) } }, { defineProps: () => ({}), defineEmits: () => () => {}, window: { addEventListener() {}, removeEventListener() {} } }))
  return { ...m, calls, stop() { unmounts.forEach(fn => fn()); scope.stop() } }
}
await test('list uses server totals and pages without slicing a server page twice', async () => {
  const m = listFixture(path => { const params = new URLSearchParams(path.split('?')[1]); return response(20000, Number(params.get('page')), Number(params.get('pageSize'))) })
  m.page.value = 2; await m.load(); assert.equal(m.listTotal.value, 20000); assert.equal(m.items.value.length, 15); assert.equal(m.paged.value[0].id, 16); assert.equal(m.pages.value, 1334); assert.equal(m.loading.value, false); m.stop()
})
await test('late page responses cannot overwrite a newer filter/page result', async () => {
  const pending = deferred(); let calls = 0; const m = listFixture(() => ++calls === 1 ? pending.promise : response(201, 2, 15))
  const first = m.load(); m.page.value = 2; await m.load(); pending.resolve(response(20000, 1, 15)); await first; assert.equal(m.listTotal.value, 201); assert.equal(m.page.value, 2); assert.equal(m.items.value[0].id, 16); m.stop()
})
await test('export revalidates saved-view exact directory references and refuses deleted neq targets', async () => {
  let categories=[{id:8,name:'客户端新版'}]
  const m=listFixture(path=>{if(path==='/sprints')return{items:[]};if(path==='/requirement-categories')return{items:categories};const params=new URLSearchParams(path.split('?')[1]);return response(201,Number(params.get('page')),Number(params.get('pageSize')))})
  m.session.value={project:{id:'p'},user:{id:'u'}};m.categoryRecords.value=[{id:8,name:'客户端'}];m.advancedFilters.value=[{field:'category',operator:'neq',value:'客户端'}];await Vue.nextTick();await m.load()
  const rows=await m.exportRecords(new AbortController().signal,()=>{});assert.equal(rows.length,201)
  const request=m.calls.filter(path=>path.startsWith('/requirements?')).at(-1),params=new URLSearchParams(request.split('?')[1]);assert.deepEqual(JSON.parse(params.get('filters')),[{field:'category',operator:'neq',value:'客户端新版'}])
  categories=[];const count=m.calls.filter(path=>path.startsWith('/requirements?')).length;await assert.rejects(()=>m.exportRecords(new AbortController().signal,()=>{}),/当前筛选未改变/);assert.equal(m.calls.filter(path=>path.startsWith('/requirements?')).length,count);m.stop()
})
await test('pagination controls remain on boards and rich editor references use the lightweight API', () => {
  const source = read('src/views/Requirements.vue'); assert.match(source, /看板仅展示当前页需求/); assert.match(source, /count:listTotal/); assert.match(source, /:records-provider="exportRecords"/); assert.match(read('src/views/Editor.vue'), /requirements\?projection=reference/)
  for (const file of ['src/views/Requirements.vue', 'src/components/RequirementListExport.vue']) { const { descriptor } = parse(read(file)); const compiled = compileScript(descriptor, { id: file }); assert.deepEqual(compileTemplate({ source: descriptor.template.content, filename: file, id: file, compilerOptions: { bindingMetadata: compiled.bindings } }).errors, []) }
})
console.log(`Passed ${count} requirement pagination and full-result export tests.`)

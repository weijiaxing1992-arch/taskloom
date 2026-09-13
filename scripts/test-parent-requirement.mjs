import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import ts from 'typescript'
import * as Vue from 'vue'
import { workflow } from './workflow-test-support.mjs'
const read = path => readFileSync(new URL('../' + path, import.meta.url), 'utf8')
function evaluate(source, imports = {}, globals = {}) { const exports = {}; new Function('require', 'exports', ...Object.keys(globals), ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText)(id => imports[id] || {}, exports, ...Object.values(globals)); return exports }
const fields = evaluate(read('src/requirementFields.ts')), mentions = evaluate(read('src/mentions.ts')), queries = evaluate(read('src/workItemQuery.ts'))
const child = () => ({ id: 12, parentId: 7, title: 'Child requirement', code: 'REQ-12', category: '客户端', sprint: '123', description: 'Saved child description', roleWeights: fields.emptyRoleWeights(), assigneeUserIds: [], ownerUserIds: [], customFields: {} })
const parent = () => ({ ...child(), id: 7, parentId: null, code: 'REQ-7', title: 'Original parent title' })
const deferred = () => { let resolve, reject; const promise = new Promise((a, b) => { resolve = a; reject = b }); return { promise, resolve, reject } }
const flush = async () => { for (let i = 0; i < 8; i++) { await Promise.resolve(); await Vue.nextTick() } }
function fixture(handler = async path => path === '/requirements/7' ? parent() : { items: [] }) {
 const source = read('src/views/Requirements.vue').match(/<script setup[^>]*>([\s\S]*?)<\/script>/)[1], scope = Vue.effectScope(), route = Vue.reactive({ path: '/iterations', params: {}, query: { sprint: '123', tab: 'list', req: '12' } }), guards = [], unmount = [], navigations = [], calls = [], events = new EventTarget()
 const router = { replace: async target => { const to = { path: route.path, query: target.query }, from = { path: route.path, query: { ...route.query } }; for (const guard of guards) if (!(await guard(to, from))) return { type: 4 }; navigations.push(target); route.query = target.query; await flush() }, push: async target => navigations.push(target) }
 const imports = { vue: { ...Vue, onMounted() {}, onBeforeUnmount: fn => unmount.push(fn) }, 'vue-router': { useRoute: () => route, useRouter: () => router, onBeforeRouteLeave() {}, onBeforeRouteUpdate: fn => guards.push(fn) }, '../api': { api: async (path, options) => { calls.push({ path, options }); return handler(path, options) } }, '../layoutScope': { useLayoutBoolean: (_key, fallback) => Vue.ref(fallback) }, '../i18n': { t: value => value, categoryLabel: value => value, formatDate: value => value }, '../requirementFields': fields, '../mentions': mentions, '../workItemQuery': queries, '../requirementWorkflow': workflow }
 const names = 'selected,session,loadParent,openParent,parentSummary,parentLoading,parentError,descriptionDraft,detailDraft,resetDescription,resetAssessment,resetAssignments,resetOwners,leavePrompt,finishLeave,comment,saving,mediaBusy,detailLoading'
 const m = scope.run(() => evaluate(source + '\nexport {' + names + '}', imports, { defineProps: () => ({ detailOnly: true }), defineEmits: () => () => {}, window: events }))
 m.selected.value = child(); m.session.value = { user: { id: 'me', role: 'product' }, project: { id: 'p' } }; m.resetDescription(); m.resetAssessment(); m.resetAssignments(); m.resetOwners()
 return { ...m, route, calls, navigations, async navigate() { await m.loadParent(m.selected.value); return m.openParent() }, stop() { unmount.forEach(fn => fn()); scope.stop() } }
}
let count = 0; async function test(name, run) { await run(); count++; console.log('✓ ' + name) }
await test('saved child loads its exact parent by stable ID and verified project header, never from a truncated list', async () => {
 const m = fixture(); await m.loadParent(m.selected.value)
 assert.equal(m.calls[0].path, '/requirements/7'); assert.equal(m.calls[0].options.headers['X-TaskLoom-Project'], 'p'); assert.equal(m.calls.length, 1)
 assert.equal(m.parentSummary.value.id, 7); assert.equal(m.parentSummary.value.title, 'Original parent title'); assert.equal(m.parentLoading.value, false); m.stop()
})
await test('parent access keeps the iteration path and all unrelated query context', async () => {
 const m = fixture(); await m.navigate(); assert.equal(m.route.path, '/iterations'); assert.deepEqual(m.navigations[0], { query: { sprint: '123', tab: 'list', req: 7 } }); await flush(); assert.equal(m.selected.value.id, 7); m.stop()
})
await test('cancelled parent navigation retains child field and comment drafts with no parent-route transition', async () => {
 const m = fixture(); await m.loadParent(m.selected.value); m.descriptionDraft.body = 'Unsaved text'; m.detailDraft.remarks = 'Unpublished note'; m.comment.value = 'Unpublished comment'
 const navigation = m.openParent(); await flush(); assert.equal(m.leavePrompt.value, true); m.finishLeave(false); await navigation
 assert.equal(m.navigations.length, 0); assert.equal(m.selected.value.id, 12); assert.equal(m.descriptionDraft.body, 'Unsaved text'); assert.equal(m.detailDraft.remarks, 'Unpublished note'); assert.equal(m.comment.value, 'Unpublished comment'); assert(!m.calls.some(call => call.options?.method)); m.stop()
})
await test('explicit discard allows parent navigation without silently saving fields or publishing comments', async () => {
 const m = fixture(); await m.loadParent(m.selected.value); m.comment.value = 'Not sent'; const navigation = m.openParent(); await flush(); m.finishLeave(true); await navigation
 assert.equal(m.navigations.length, 1); assert(!m.calls.some(call => call.options?.method)); assert.equal(m.route.query.req, 7); m.stop()
})
await test('missing, malformed or cross-project parent responses fail locally and leave the child readable', async () => {
 for (const result of [Error('Forbidden'), { id: 9, title: 'Wrong parent' }, { ...parent(), projectId: 'another-project' }]) {
   const m = fixture(async () => { if (result instanceof Error) throw result; return result }); await m.loadParent(m.selected.value); assert.equal(m.parentSummary.value, null); assert.match(m.parentError.value, /父需求暂时无法加载/); assert.equal(m.selected.value.id, 12); assert.equal(m.parentLoading.value, false); await m.openParent(); assert.equal(m.navigations.length, 0); m.stop()
 }
})
await test('late parent responses cannot overwrite another child, another project or an unmounted detail', async () => {
 for (const change of ['child', 'project', 'unmount']) {
   const pending = deferred(), m = fixture(() => pending.promise), loading = m.loadParent(m.selected.value)
   if (change === 'child') m.selected.value = { ...child(), id: 13, parentId: 8 }; else if (change === 'project') m.session.value.project.id = 'q'; else m.stop()
   pending.resolve(parent()); await loading; assert.equal(m.parentSummary.value, null, change); if (change !== 'unmount') m.stop()
 }
})
await test('retry is explicit, invalid self-parent IDs never call the API, and saving blocks navigation', async () => {
 let failed = true; const m = fixture(async () => { if (failed) throw Error('offline'); return parent() }); await m.loadParent(m.selected.value); assert(m.parentError.value)
 failed = false; await m.loadParent(m.selected.value); assert.equal(m.parentError.value, ''); m.saving.value = true; await m.openParent(); assert.equal(m.navigations.length, 0); m.saving.value = false
 m.selected.value = { ...child(), parentId: 12 }; const count = m.calls.length; await m.loadParent(m.selected.value); assert.equal(m.calls.length, count); assert.equal(m.parentSummary.value, null); m.stop()
})
await test('parent entry is available outside the body tab, and export controls use the full filtered list and exact detail scope', () => {
 const source = read('src/views/Requirements.vue'), template = source.slice(source.indexOf('<template>'))
 assert(template.indexOf('class="requirement-parent-context"') < template.indexOf('class="drawer-body detail-workspace"'))
 assert.match(template, /@click="openParent"/); assert.match(template, /@click="loadParent\(selected\)"/)
 const listExport = template.split('\n').find(line => line.includes('<RequirementListExport')); assert(listExport.includes(':records-provider="exportRecords"')); assert(listExport.includes(':total="loading||error?0:listTotal"')); assert(listExport.includes(':project-id="session.project.id"')); assert(listExport.includes(':columns="visibleColumns.map(column=>column.key)"'))
 assert.match(template, /<WorkItemPdfExport[^>]+object-type="requirement"[^>]+:object-id="selected.id"[^>]+:project-id="session.project.id"/)
})
console.log(`Passed ${count} parent requirement regressions.`)

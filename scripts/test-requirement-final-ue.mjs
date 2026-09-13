import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import ts from 'typescript'
import * as Vue from 'vue'
import { parse, compileScript, compileTemplate } from 'vue/compiler-sfc'
import { workflow } from './workflow-test-support.mjs'

const read = path => readFileSync(new URL('../' + path, import.meta.url), 'utf8')
function evaluate(source, imports = {}, globals = {}) {
  const exports = {}
  new Function('require', 'exports', ...Object.keys(globals), ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText)(id => imports[id] || {}, exports, ...Object.values(globals))
  return exports
}
const source = read('src/views/Requirements.vue'), script = source.match(/<script setup[^>]*>([\s\S]*?)<\/script>/)[1]
const fields = evaluate(read('src/requirementFields.ts')), mentions = evaluate(read('src/mentions.ts')), queries = evaluate(read('src/workItemQuery.ts'))
const deferred = () => { let resolve, reject; const promise = new Promise((a, b) => { resolve = a; reject = b }); return { promise, resolve, reject } }
function fixture(handler = async () => ({ items: [] })) {
  const scope = Vue.effectScope(), unmounts = [], calls = [], events = new EventTarget()
  const imports = {
    vue: { ...Vue, onMounted() {}, onBeforeUnmount: fn => unmounts.push(fn) },
    'vue-router': { useRoute: () => ({ query: {} }), useRouter: () => ({}), onBeforeRouteLeave() {}, onBeforeRouteUpdate() {} },
    '../api': { api: async (path, options) => { calls.push({ path, options }); return handler(path, options) } },
    '../layoutScope': { useLayoutBoolean: (_key, fallback) => Vue.ref(fallback) },
    '../i18n': { t: value => value, categoryLabel: value => value },
    '../requirementFields': fields, '../mentions': mentions, '../workItemQuery': queries, '../requirementWorkflow': workflow,
  }
  const names = 'session,loadOptions,refreshSprintOptions,loadTags,sprints,members,fieldDefs,tagOptions,metadataError,tagError,filters,toast,selected,detailDraft,detailCustomDatesValid,supplementaryExpanded,supplementaryDirty,showProperties'
  const m = scope.run(() => evaluate(script + '\nexport {' + names + '}', imports, { defineProps: () => ({}), defineEmits: () => () => {}, window: events }))
  m.session.value = { tenant: { id: 't' }, user: { id: 'u' }, project: { id: 'p' } }
  return { ...m, calls, stop() { unmounts.forEach(fn => fn()); scope.stop() } }
}
let count = 0
async function test(name, run) { await run(); count++; console.log('✓ ' + name) }

await test('concurrent directory refreshes share six in-flight reads, scoped to the selected project', async () => {
  const pending = deferred(), m = fixture(() => pending.promise)
  const first = m.loadOptions(), second = m.loadOptions(), sprintFocus = m.refreshSprintOptions(), tags = m.loadTags()
  assert.equal(m.calls.length, 6)
  assert.equal(new Set(m.calls.map(call => call.path)).size, 6)
  assert(m.calls.every(call => call.options.headers['X-TaskLoom-Project'] === 'p' && !call.options.method))
  pending.resolve({ items: [] }); await Promise.all([first, second, sprintFocus, tags]); m.stop()
})
await test('focusing an iteration selector refreshes only iterations and preserves the chosen filter', async () => {
  const m = fixture(async () => ({ items: [{ id: 4, name: 'Current iteration' }] }))
  m.filters.sprint = 'Saved iteration'; await m.refreshSprintOptions()
  assert.deepEqual(m.calls.map(call => call.path), ['/sprints'])
  assert.equal(m.sprints.value[0].id, 4); assert.equal(m.filters.sprint, 'Saved iteration')
  await m.refreshSprintOptions(); assert.equal(m.calls.length, 2, 'settled results must not stay cached'); m.stop()
})
await test('failed refreshes retain current data and allow an immediate explicit retry', async () => {
  let fail = true; const m = fixture(async () => { if (fail) throw Error('offline'); return { items: [{ id: 5, name: 'Updated iteration' }] } })
  m.sprints.value = [{ id: 4, name: 'Saved iteration' }]; await m.refreshSprintOptions()
  assert.equal(m.sprints.value[0].id, 4); assert.match(m.toast.value, /已保留当前选择/)
  fail = false; await m.refreshSprintOptions(); assert.equal(m.sprints.value[0].id, 5); assert.equal(m.calls.length, 2); m.stop()
})
await test('old directory results cannot overwrite a new project or account', async () => {
  for (const change of ['project', 'user']) {
    const pending = deferred(); const m = fixture((_path, options) => options.headers['X-TaskLoom-Project'] === 'p' && m.session.value.user.id === 'u' ? pending.promise : Promise.resolve({ items: [{ id: 'new', name: 'New scope', enabled: true }] }))
    const old = m.loadOptions()
    if (change === 'project') m.session.value.project.id = 'q'; else m.session.value.user.id = 'another-user'
    await m.loadOptions(); pending.resolve({ items: [{ id: 'old', enabled: true }] }); await old
    assert.equal(m.members.value[0].id, 'new', change); assert.equal(m.sprints.value[0].id, 'new', change); assert.equal(m.tagOptions.value[0].id, 'new', change); m.stop()
  }
})
await test('late failures and unmounted responses do not replace current metadata', async () => {
  const pending = deferred(), m = fixture(() => pending.promise), old = m.loadOptions()
  m.session.value.project.id = 'q'; pending.reject(Error('old failure')); await old
  assert.equal(m.metadataError.value, ''); assert.equal(m.tagError.value, ''); m.stop()
  const later = deferred(), disposed = fixture(() => later.promise), loading = disposed.loadOptions()
  disposed.stop(); later.resolve({ items: [{ id: 'late', enabled: true }] }); await loading
  assert.deepEqual(disposed.members.value, []); assert.deepEqual(disposed.tagOptions.value, [])
})
await test('secondary fields are initially folded, retain drafts while folded and expose validation errors', async () => {
  const m = fixture(); m.fieldDefs.value = [{ key: 'business_value', type: 'text' }]
  m.selected.value = { id: 1, customFields: { business_value: 'saved' } }; m.detailDraft.customFields = { business_value: 'saved' }
  assert.equal(m.supplementaryExpanded.value, false); assert.equal(m.supplementaryDirty.value, false)
  m.detailDraft.customFields.business_value = 'draft'; m.supplementaryExpanded.value = true; m.supplementaryExpanded.value = false
  assert.equal(m.detailDraft.customFields.business_value, 'draft'); assert.equal(m.supplementaryDirty.value, true)
  m.showProperties.value = false; m.detailCustomDatesValid.value = false
  assert.equal(m.supplementaryExpanded.value, true); assert.equal(m.showProperties.value, true)
  assert.equal(m.detailDraft.customFields.business_value, 'draft'); assert.equal(m.calls.length, 0)
  m.selected.value = { id: 2, customFields: {} }; assert.equal(m.supplementaryExpanded.value, false); m.stop()
})
await test('detail relation lookup excludes full rich documents and native disclosure keeps field components mounted', () => {
  assert.match(script, /api<any>\('\/requirements\?projection=reference'\)/)
  const { descriptor } = parse(source), compiled = compileScript(descriptor, { id: 'final-ue' })
  assert.deepEqual(compileTemplate({ source: descriptor.template.content, filename: 'Requirements.vue', id: 'final-ue', compilerOptions: { bindingMetadata: compiled.bindings } }).errors, [])
  const panel = source.match(/<details class="detail-supplementary"[\s\S]*?<\/details>/)[0]
  assert.match(panel, /<summary>/); assert.match(panel, /@toggle=/); assert.match(panel, /@validity-change="detailCustomDatesValid=\$event"/)
  assert.doesNotMatch(panel, /v-if="supplementaryExpanded"|v-show="supplementaryExpanded"/)
  assert.match(panel, /<CustomFieldInputs/); assert.match(panel, /<dl class="detail-audit"/)
  assert.equal((source.match(/@focus="refreshSprintOptions"/g) || []).length, 2)
})
console.log(`Passed ${count} final requirement UE/performance regressions.`)

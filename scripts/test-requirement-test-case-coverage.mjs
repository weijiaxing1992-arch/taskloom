import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import ts from 'typescript'
import * as Vue from 'vue'
import { compileScript, compileTemplate, parse } from 'vue/compiler-sfc'

const read = path => readFile(new URL('../' + path, import.meta.url), 'utf8')
const source = await read('src/components/RequirementTestCaseCoverage.vue')
const setupSource = source.match(/<script setup[^>]*>([\s\S]*?)<\/script>/)?.[1]
assert(setupSource, 'RequirementTestCaseCoverage.vue must have script setup')
const flush = async () => { for (let index = 0; index < 24; index++) { await Promise.resolve(); await Vue.nextTick() } }
const deferred = () => { let resolve, reject; const promise = new Promise((yes, no) => { resolve = yes; reject = no }); return { promise, resolve, reject } }

const stats = (extra = {}) => ({ total: 1, enabled: 1, draft: 1, pendingReview: 0, approved: 0, deprecated: 0, ...extra })
const testCase = (id = 101, extra = {}) => ({
  id,
  code: `CASE-${id}`,
  title: `测试用例 ${id}`,
  status: '草稿',
  priority: 'P1',
  caseType: '功能测试',
  category: '回归',
  owner: '测试成员',
  enabled: true,
  updatedAt: '2026-09-04T08:00:00Z',
  ...extra,
})
const response = (id = 101, extra = {}) => ({ items: [testCase(id)], stats: stats(), hasMore: true, ...extra })

function fixture(handler, initial = {}) {
  const scope = Vue.effectScope()
  const props = Vue.reactive({ requirementId: 7, disabled: false, limit: 6, ...initial })
  const calls = [], emitted = [], unmounts = []
  const api = async (path, options) => { calls.push({ path, options }); return handler(path, options) }
  const imports = {
    vue: { ...Vue, onBeforeUnmount: callback => unmounts.push(callback) },
    '../api': { api },
    '../i18n': { t: value => value, formatDate: value => String(value) },
  }
  const output = ts.transpileModule(setupSource + '\nexport { load, items, stats, loading, error, hasMore, libraryPath }', {
    compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
  }).outputText
  const exports = {}
  scope.run(() => new Function('require', 'exports', 'defineProps', 'withDefaults', 'defineEmits', 'defineExpose', output)(
    id => { assert(id in imports, 'Unexpected import ' + id); return imports[id] },
    exports,
    () => props,
    () => props,
    () => (event, value) => emitted.push({ event, value }),
    () => {},
  ))
  return { ...exports, props, calls, emitted, stop: () => { for (const callback of unmounts) callback(); scope.stop() } }
}

let count = 0
async function test(name, run) { await run(); count++; console.log('✓ ' + name) }

await test('coverage rejects malformed responses and preserves the last verified snapshot', async () => {
  let next = response(101)
  const component = fixture(async () => next)
  await flush()
  assert.equal(component.items.value[0].id, 101)
  assert.equal(component.stats.value.total, 1)
  assert.equal(component.hasMore.value, true)
  assert.equal(component.emitted.length, 1)

  // 缺字段、非法 ID 和负统计值都不能覆盖已验证的覆盖数据。
  next = { items: [testCase(0)], stats: stats() }
  await component.load()
  assert.match(component.error.value, /关联用例列表返回格式不正确/)
  assert.equal(component.items.value[0].id, 101)
  assert.equal(component.stats.value.total, 1)
  assert.equal(component.emitted.length, 1)

  next = { items: [testCase(102)], stats: stats({ approved: -1 }) }
  await component.load()
  assert.match(component.error.value, /关联用例列表返回格式不正确/)
  assert.equal(component.items.value[0].id, 101)
  assert.equal(component.emitted.length, 1)
  component.stop()
})

await test('coverage requirement switches discard late responses from the previous requirement', async () => {
  const first = deferred(), second = deferred()
  const component = fixture(path => path.includes('/requirements/7/') ? first.promise : second.promise)
  await flush()
  assert.equal(component.calls.length, 1)
  assert.match(component.calls[0].path, /^\/requirements\/7\/test-cases\?limit=6$/)

  component.props.requirementId = 8
  await flush()
  assert.equal(component.calls.length, 2)
  assert.match(component.calls[1].path, /^\/requirements\/8\/test-cases\?limit=6$/)
  second.resolve(response(802))
  await flush()
  first.resolve(response(701))
  await flush()

  assert.deepEqual(component.items.value.map(item => item.id), [802])
  assert.equal(component.stats.value.total, 1)
  assert.deepEqual(component.emitted.map(event => event.value.items[0].id), [802])
  component.stop()
})

await test('coverage failures are retryable and a bounded request is rebuilt for the current requirement', async () => {
  let attempts = 0
  const component = fixture(async () => {
    attempts++
    if (attempts === 1) throw Error('temporary offline')
    return response(303, { hasMore: 'not-a-boolean' })
  }, { requirementId: 23, limit: 999 })
  await flush()
  assert.equal(component.loading.value, false)
  assert.equal(component.error.value, 'temporary offline')
  assert.equal(component.items.value.length, 0)
  assert.equal(component.calls[0].path, '/requirements/23/test-cases?limit=20')

  await component.load()
  assert.equal(component.error.value, '')
  assert.equal(component.items.value[0].id, 303)
  assert.equal(component.hasMore.value, false, 'only literal true enables the more-results affordance')
  assert.equal(component.calls[1].path, '/requirements/23/test-cases?limit=20')
  component.stop()
})

await test('inline actions stay in the requirement and library-view links carry its ID', async () => {
  const component = fixture(async () => response(404), { requirementId: 44 })
  await flush()
  assert.equal(component.libraryPath.value, '/tests?tab=cases&requirement=44')
  component.props.requirementId = 45
  await flush()
  assert.equal(component.libraryPath.value, '/tests?tab=cases&requirement=45')
  assert.match(source, /:to="libraryPath"/)
  assert.match(source, /@click="actions\?\.open\(\)"/)
  assert.match(source, /@click="actions\?\.associate\(\)"/)
  assert.match(source, /@click="actions\?\.open\(item.id\)"/)
  component.stop()
})

await test('coverage template compiles and keeps link/query behaviour declarative', () => {
  const descriptor = parse(source, { filename: 'RequirementTestCaseCoverage.vue' }).descriptor
  const script = compileScript(descriptor, { id: 'requirement-test-case-coverage' })
  const template = compileTemplate({ source: descriptor.template.content, filename: 'RequirementTestCaseCoverage.vue', id: 'requirement-test-case-coverage', compilerOptions: { bindingMetadata: script.bindings } })
  assert.deepEqual(template.errors, [])
  assert.match(source, /Math\.min\(20, Math\.max\(1, props\.limit\)\)/)
  assert.match(source, /关联用例列表返回格式不正确，请重试/)
  assert(!source.includes('v-html'))
})

console.log(`Passed ${count} requirement test-case coverage frontend regressions.`)

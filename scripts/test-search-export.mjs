import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import ts from 'typescript'
import * as Vue from 'vue'

const read = path => readFileSync(new URL('../' + path, import.meta.url), 'utf8')
const source = read('src/views/Search.vue').match(/<script setup[^>]*>([\s\S]*?)<\/script>/)[1]

function evaluate(handler, query = {}) {
  const exports = {}, scope = Vue.effectScope(), calls = [], unmounts = []
  const api = async (path, options) => { calls.push({ path, options }); return handler(path, options) }
  const code = ts.transpileModule(source + '\nexport {exportRequirements,matchingRequirements,canExportRequirements,requirementExportTotal,items,total,loading,type,q,project}', { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText
  scope.run(() => new Function('require', 'exports', 'window', code)(id => ({
    vue: { ...Vue, onMounted() {}, onBeforeUnmount: fn => unmounts.push(fn) },
    'vue-router': { useRoute: () => ({ query }) },
    '../api': { api },
    '../i18n': { t: value => value, locale: Vue.ref('zh-CN'), formatDate: value => String(value) },
    '../requirementWorkflow': {},
    '../requirementPaging': { requirementExportLimit: 20000 },
  })[id] || {}, exports, { addEventListener() {}, removeEventListener() {} }))
  return { ...exports, calls, stop() { unmounts.forEach(fn => fn()); scope.stop() } }
}

const requirement = (id, projectId) => ({ id, type: '需求', code: `REQ-${id}`, title: `需求 ${id}`, projectId, projectName: `项目 ${projectId}` })
let count = 0
async function test(name, run) { await run(); count++; console.log('✓ ' + name) }

await test('global search export traverses all requirement pages and reads each record with its own project scope', async () => {
  const m = evaluate((path, options) => {
    if (path.startsWith('/search?')) {
      const params = new URLSearchParams(path.split('?')[1])
      assert.equal(params.get('q'), '发布')
      assert.equal(params.get('type'), '需求')
      assert.equal(params.get('limit'), '100')
      return params.get('offset') === '0'
        ? { total: 2, items: [requirement(11, 'project-a')] }
        : { total: 2, items: [requirement(12, 'project-b')] }
    }
    if (path === '/requirements/11') { assert.equal(options.headers['X-TaskLoom-Project'], 'project-a'); return { id: 11, title: '完整需求 A', customFields: { priority: 'P1' } }
    }
    if (path === '/requirements/12') { assert.equal(options.headers['X-TaskLoom-Project'], 'project-b'); return { id: 12, title: '完整需求 B', customFields: { priority: 'P2' } }
    }
    throw Error('unexpected request ' + path)
  }, { q: '发布' })
  m.items.value = [requirement(11, 'project-a')]
  m.total.value = 2
  assert.equal(m.canExportRequirements.value, true)
  assert.equal(m.requirementExportTotal.value, 1)
  const progress = []
  const records = await m.exportRequirements(new AbortController().signal, (done, total) => progress.push([done, total]))
  assert.deepEqual(records.map(item => [item.id, item.projectId, item.title]), [[11, 'project-a', '完整需求 A'], [12, 'project-b', '完整需求 B']])
  assert.deepEqual(progress, [[1, 2], [2, 2]])
  assert.equal(m.calls.filter(call => call.path.startsWith('/search?')).length, 2)
  m.stop()
})

await test('global search export rejects malformed rows and does not issue a cross-project detail request', async () => {
  const m = evaluate(async path => path.startsWith('/search?') ? { total: 1, items: [{ ...requirement(7, 'project-a'), type: '缺陷' }] } : (() => { throw Error('detail request must not be made') })(), { q: '安全' })
  await assert.rejects(() => m.exportRequirements(new AbortController().signal, () => {}), /数据格式/)
  assert.equal(m.calls.length, 1)
  m.stop()
})

await test('search header keeps a dedicated flat query row and wires the export provider instead of only exporting the visible page', () => {
  const page = read('src/views/Search.vue'), shell = read('src/sidebar.css'), topSearch = read('src/components/TopSearch.vue'), glass = read('src/glass-system.css')
  assert.match(page, /class="search-query-row"/)
  assert.match(page, /:records-provider="exportRequirements"/)
  assert.match(page, /:total="requirementExportTotal"/)
  assert.match(page, /导出匹配需求/)
  assert.match(shell, /grid-template-columns: minmax\(168px, 320px\) minmax\(260px, 1fr\) max-content/)
  assert.match(shell, /\.project-switcher \{[\s\S]*?width: fit-content/)
  assert.match(topSearch, /width:clamp\(260px,36vw,520px\)/)
  assert.doesNotMatch(glass, /\.app-shell \.project-switcher,\n\.app-shell \.top-actions/)
})

console.log(`Passed ${count} global search export and header regressions.`)

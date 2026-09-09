import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import ts from 'typescript'
import * as Vue from 'vue'
import { parse, compileScript, compileTemplate } from 'vue/compiler-sfc'

const root = new URL('../', import.meta.url)
const read = path => readFile(new URL(path, root), 'utf8')
function evaluate(source, imports = {}, globals = {}) {
  const output = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText
  const exports = {}
  new Function('require', 'exports', ...Object.keys(globals), output)(id => imports[id], exports, ...Object.values(globals))
  return exports
}
const helpers = evaluate(await read('src/sprintWeights.ts'))
const roles = helpers.sprintWeightRoles.map(x => x.key)
function summary(overrides = {}) {
  return { totalWeight: 0, requirementCount: 0, estimatedCount: 0, unestimatedCount: 0, precision: 6, roleTotals: Object.fromEntries(roles.map(role => [role, 0])), roleEstimatedCounts: Object.fromEntries(roles.map(role => [role, 0])), items: [], ...overrides }
}
let count = 0
async function test(name, run) { await run(); count++; console.log('✓ ' + name) }

await test('weight dimensions are exactly frontend, backend, algorithm, UI and product', () => {
  assert.deepEqual(roles, ['frontend', 'backend', 'algorithm', 'ui', 'product'])
  assert.equal(helpers.sprintWeightRoles.length, new Set(roles).size)
})
await test('empty and deliberately zero-estimated pools never produce NaN charts', () => {
  assert.equal(helpers.sprintWeightCoverage(summary()), 0)
  const enteredZero = summary({ requirementCount: 1, estimatedCount: 1, roleEstimatedCounts: { ...summary().roleEstimatedCounts, frontend: 1 } })
  assert.equal(helpers.sprintWeightCoverage(enteredZero), 100)
  for (const role of helpers.sprintWeightBreakdown(enteredZero)) assert.equal(role.percent, 0)
  assert.equal(helpers.sprintWeightBreakdown(enteredZero)[0].estimatedCount, 1)
})
await test('summary totals are authoritative, unaffected by list size, completion status or hours', () => {
  const actual = summary({ totalWeight: 452.1, requirementCount: 1507, estimatedCount: 1507, roleTotals: { ...summary().roleTotals, frontend: 150.7, backend: 301.4 }, items: [{ id: 1, status: '已完成', totalWeight: .3, estimatedHours: 999999 }] })
  const data = helpers.sprintWeightBreakdown(actual)
  assert.equal(data[0].total, 150.7)
  assert.equal(data[1].total, 301.4)
  assert.equal(helpers.sprintWeightCoverage(actual), 100)
  assert.ok(Math.abs(data[0].percent - 100 / 3) < 1e-9)
})
await test('small decimal and large total shares remain finite and within 0–100', () => {
  for (const total of [.000001, 5000000000]) {
    const actual = summary({ totalWeight: total, roleTotals: { ...summary().roleTotals, frontend: total } })
    const data = helpers.sprintWeightBreakdown(actual)
    assert.equal(data[0].percent, 100)
    assert(data.every(x => Number.isFinite(x.percent) && x.percent >= 0 && x.percent <= 100))
  }
})
const source = await read('src/components/SprintWeightPanel.vue')
await test('weight panel compiles with all three visible states and accessible meters', () => {
  const { descriptor, errors } = parse(source)
  assert.deepEqual(errors, [])
  const script = compileScript(descriptor, { id: 'sprint-weight-panel' })
  const template = compileTemplate({ source: descriptor.template.content, filename: 'SprintWeightPanel.vue', id: 'sprint-weight-panel', compilerOptions: { bindingMetadata: script.bindings } })
  assert.deepEqual(template.errors, [])
  assert.match(source, /role="meter"/)
  assert.match(source, /role="status"/)
  assert.match(source, /role="alert"/)
})
await test('detail pagination and search cannot alter the complete weight summary', async () => {
  const actual = summary({ totalWeight: 1000, requirementCount: 75, estimatedCount: 74, unestimatedCount: 1, roleTotals: { ...summary().roleTotals, frontend: 1000 }, items: Array.from({ length: 75 }, (_, i) => ({ id: i + 1, code: `REQ-${i + 1}`, title: i === 31 ? '用户原文 / Keep title' : '普通原文', totalWeight: 1 })) })
  const props = Vue.reactive({ summary: actual })
  const scope = Vue.effectScope()
  const body = source.match(/<script setup[^>]*>([\s\S]*?)<\/script>/)[1]
  let panel
  scope.run(() => { panel = evaluate(body + '\nexport { query, page, pages, visible, breakdown, coverage }', { vue: Vue, '../i18n': { t: x => x, formatNumber: n => String(n) }, '../sprintWeights': helpers }, { defineProps: () => props, defineEmits: () => () => {} }) })
  assert.equal(panel.visible.value.length, 30)
  assert.equal(panel.pages.value, 3)
  panel.page.value = 2
  assert.equal(panel.visible.value[1].title, '用户原文 / Keep title')
  panel.query.value = 'Keep title'; await Vue.nextTick()
  assert.equal(panel.page.value, 1)
  assert.equal(panel.visible.value.length, 1)
  assert.equal(panel.breakdown.value[0].total, 1000)
  assert.equal(props.summary.requirementCount, 75)
  assert.equal(panel.coverage.value, 99)
  scope.stop()
})
console.log(`Passed ${count} sprint weight UI tests.`)

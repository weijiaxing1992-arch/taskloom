import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import ts from 'typescript'
import * as Vue from 'vue'
import { parse, compileScript, compileTemplate } from 'vue/compiler-sfc'

const read = file => readFileSync(new URL('../' + file, import.meta.url), 'utf8')
const source = read('src/components/LocaleSwitcher.vue')
const deferred = () => { let resolve, reject; const promise = new Promise((yes, no) => { resolve = yes; reject = no }); return { promise, resolve, reject } }
function harness(authenticated, handler = async () => ({})) {
  const locale = Vue.ref('zh-CN'), calls = [], events = []
  const code = ts.transpileModule(source.match(/<script setup[^>]*>([\s\S]*?)<\/script>/)[1] + '\nexport {change,pending,error,options}', { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText
  const exports = {}, imports = { vue: Vue, '../api': { api: async (...args) => { calls.push(args); return handler(...args) } }, '../i18n': { locale, setLocale: value => { locale.value = value }, t: value => value } }
  new Function('require', 'exports', 'defineProps', 'window', 'CustomEvent', code)(name => imports[name] || {}, exports, () => ({ authenticated }), { dispatchEvent: event => events.push(event) }, class { constructor(type, init) { this.type = type; this.detail = init.detail } })
  return { ...exports, locale, calls, events }
}
let count = 0
async function test(name, run) { await run(); count++; console.log('✓ ' + name) }

await test('language dropdown uses the shared flat component with an accessible value contract', () => {
  const { descriptor } = parse(source), script = compileScript(descriptor, { id: 'locale' })
  assert.deepEqual(compileTemplate({ source: descriptor.template.content, filename: 'LocaleSwitcher.vue', id: 'locale', compilerOptions: { bindingMetadata: script.bindings } }).errors, [])
  assert.match(source, /<AppSelect[^>]+:label="t\('界面语言'\)"/)
  assert.match(source, /@update:model-value="change"/)
  assert(!source.includes('<select'))
})
await test('public language selection stays local and authenticated selection saves one preference', async () => {
  const publicPage = harness(false); await publicPage.change('en-US')
  assert.equal(publicPage.locale.value, 'en-US'); assert.equal(publicPage.calls.length, 0)
  const app = harness(true); await app.change('en-US')
  assert.equal(app.locale.value, 'en-US'); assert.equal(app.pending.value, false)
  assert.deepEqual(app.calls, [['/preferences/locale', { method: 'PATCH', body: JSON.stringify({ locale: 'en-US' }) }]])
  assert.equal(app.events[0].detail, 'en-US')
})
await test('invalid, unchanged and overlapping selections never produce extra writes', async () => {
  const wait = deferred(), app = harness(true, () => wait.promise)
  await app.change('fr'); await app.change(1); await app.change('zh-CN')
  assert.equal(app.calls.length, 0)
  const pending = app.change('en-US'); assert.equal(app.pending.value, true)
  await app.change('zh-CN'); assert.equal(app.calls.length, 1)
  wait.resolve(); await pending; assert.equal(app.locale.value, 'en-US')
})
await test('failed preference writes restore the visible selection with a retryable error', async () => {
  let fail = true; const app = harness(true, async () => { if (fail) throw Error('保存失败') })
  await app.change('en-US'); assert.equal(app.locale.value, 'zh-CN'); assert.equal(app.error.value, '保存失败'); assert.equal(app.events.length, 0)
  fail = false; await app.change('en-US'); assert.equal(app.locale.value, 'en-US'); assert.equal(app.error.value, '')
})
await test('pagination excludes text dropdowns from icon sizing and retains a single-line selector', () => {
  const contract = read('src/ui-standards.css'), pages = read('src/app-page-controls.css')
  assert.match(contract, /\.pagination>button:not\(\.app-select-trigger\)/)
  assert(!/\.pagination>button\)/.test(contract))
  assert.match(pages, /\.app-shell \.pagination button:not\(\.app-select-trigger\),/)
  const rule = pages.match(/\.app-shell \.pagination \.page-size-select\s*\{([^}]+)\}/)[1]
  assert.match(rule, /display: inline-flex/); assert.match(rule, /flex: 0 0 auto/); assert.match(rule, /width: max-content/)
  assert.match(pages, /\.pagination > span:first-child\s*\{[^}]+white-space: nowrap/)
})
console.log(`Passed ${count} locale and pagination control regressions.`)

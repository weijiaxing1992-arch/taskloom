import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import ts from 'typescript'
import * as Vue from 'vue'
import { parse, compileScript, compileTemplate } from 'vue/compiler-sfc'
const read = path => readFileSync(new URL('../' + path, import.meta.url), 'utf8')
const js = code => ts.transpileModule(code, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText
const helper = {}; new Function('exports', js(read('src/loginIdentity.ts')))(helper)
function storage(email = '') { const values = new Map(email ? [[helper.loginEmailKey, email]] : []); return { values, getItem: key => values.get(key) || null, setItem: (key, value) => values.set(key, value), removeItem: key => values.delete(key) } }
const source = read('src/views/Login.vue'), { descriptor } = parse(source), script = compileScript(descriptor, { id: 'login-memory' })
function mount(store, api = async () => ({})) {
  const exports = {}, events = [], calls = []
  const imports = { vue: Vue, '../api': { api: async (...args) => { calls.push(args); return api(...args) } }, '../i18n': { t: x => x }, '../components/LocaleSwitcher.vue': {}, '../components/WechatLoginButton.vue': {}, '../loginIdentity': helper }
  new Function('require', 'exports', 'localStorage', js(script.content))(id => imports[id], exports, store)
  const c = exports.default.setup({}, { expose() {}, emit: name => events.push(name) })
  return { c, calls, events }
}
let count = 0
async function test(name, fn) { await fn(); count++; console.log('✓ ' + name) }
await test('cache is email-only and malformed or unavailable storage does not block login', () => {
  for (const input of [null, '{}', 'a@', 'a @b.test', 'a\n@b.test', 'x'.repeat(250) + '@b.test']) assert.equal(helper.normalizeLoginEmail(input), '')
  assert.equal(helper.readLoginEmail(storage(' User@Example.test ')), 'user@example.test')
  assert.equal(helper.readLoginEmail({ getItem() { throw Error('denied') } }), '')
  assert.doesNotThrow(() => helper.saveLoginEmail({ setItem() { throw Error('denied') } }, 'user@example.test'))
})
await test('initial email restores while password remains empty; quick-fill focuses password', async () => {
  const s = storage('last@example.test'), m = mount(s); assert.equal(m.c.form.email, 'last@example.test'); assert.equal(m.c.form.password, '')
  let focused = 0; m.c.passwordInput.value = { focus() { focused++ } }; m.c.form.email = 'another@example.test'
  await m.c.useLastEmail(); assert.equal(m.c.form.email, 'last@example.test'); assert.equal(focused, 1); assert.equal(m.events.length, 0)
})
await test('only successful login changes history; no password, cookie or token is persisted', async () => {
  const s = storage('last@example.test'), m = mount(s); m.c.form.email = 'next@example.test'; m.c.form.password = 'secret-memory-only'; await m.c.login()
  assert.equal(m.c.form.password, ''); assert.deepEqual(m.events, ['authenticated']); assert.equal(s.values.get(helper.loginEmailKey), 'next@example.test')
  assert(!JSON.stringify([...s.values]).includes('secret')); assert.equal(s.values.size, 1)
  const fail = mount(s, async () => { throw Error('Incorrect credentials') }); fail.c.form.email = 'typo@example.test'; fail.c.form.password = 'private'; await fail.c.login()
  assert.equal(s.values.get(helper.loginEmailKey), 'next@example.test'); assert.equal(fail.events.length, 0); assert.equal(fail.c.submitting.value, false)
})
await test('forget clears remembered email but never erases a different typed address', () => {
  const s = storage('last@example.test'), m = mount(s); m.c.forgetEmail(); assert.equal(s.values.size, 0); assert.equal(m.c.form.email, ''); assert.equal(m.c.lastEmail.value, '')
  const other = mount(storage('last@example.test')); other.c.form.email = 'different@example.test'; other.c.forgetEmail(); assert.equal(other.c.form.email, 'different@example.test')
})
await test('pending sign-in prevents duplicate submission or quick-fill changes', async () => {
  let finish; const s = storage('last@example.test'), m = mount(s, () => new Promise(resolve => { finish = resolve }))
  m.c.form.email = 'next@example.test'; const pending = m.c.login(); await m.c.login(); await m.c.useLastEmail(); m.c.forgetEmail()
  assert.equal(m.calls.length, 1); assert.equal(m.c.form.email, 'next@example.test'); assert.equal(s.values.size, 1); finish({}); await pending
})
await test('login markup supports native password managers; profile WeCom has its own padding and table scrolling', () => {
  assert.deepEqual(compileTemplate({ id: 'login-memory', filename: 'Login.vue', source: descriptor.template.content, compilerOptions: { bindingMetadata: script.bindings } }).errors, [])
  assert.match(source, /name="username"/); assert.match(source, /autocomplete="current-password"/); assert.match(source, /type="button" class="login-email-chip"/)
  assert.match(read('src/views/Profile.vue'), /profile-wecom-panel\{padding:24px/)
  assert.match(read('src/components/UserWecomWebhook.vue'), /\.wecom-table-wrap\{overflow:auto/)
})
console.log(`Passed ${count} sign-in memory and profile layout regressions.`)

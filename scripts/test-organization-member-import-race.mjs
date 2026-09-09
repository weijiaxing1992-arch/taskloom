import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import ts from 'typescript'
import * as Vue from 'vue'
import { parse, compileScript, compileTemplate } from 'vue/compiler-sfc'

// 仅执行组件逻辑、内存文件及请求替身，不读取账号存储或连接业务数据库。
const read = path => readFileSync(new URL('../' + path, import.meta.url), 'utf8')
function evaluate(source, imports, globals = {}) {
  const exports = {}, code = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText
  new Function('require', 'exports', ...Object.keys(globals), code)(id => imports[id] || {}, exports, ...Object.values(globals))
  return exports
}
const organization = evaluate(read('src/organization.ts'), {})
const deferred = () => { let resolve, reject; const promise = new Promise((yes, no) => { resolve = yes; reject = no }); return { promise, resolve, reject } }
const flush = async () => { for (let i = 0; i < 4; i++) await Vue.nextTick() }
const source = read('src/components/OrganizationMembers.vue')
function fixture() {
  const effects = Vue.effectScope(), unmounts = [], events = new Map(), calls = []
  const window = { confirm: () => true, addEventListener: (key, fn) => events.set(key, fn), removeEventListener: key => events.delete(key) }
  let project = 'p'
  const lifecycle = { ...Vue, onMounted: () => {}, onBeforeUnmount: fn => unmounts.push(fn) }
  const scopeModule = effects.run(() => evaluate(read('src/components/settingsScope.ts'), { vue: lifecycle, '../api': { api: async (path, options) => {
    calls.push({ path, options })
    if (path.endsWith('/preview')) return { previewId: 'preview-test', canCommit: true, rows: [] }
    return path === '/session' ? { user: { id: 'test-user' } } : { items: [] }
  } } }, { window, localStorage: { getItem: () => project } }))
  let scope
  const script = parse(source).descriptor.scriptSetup.content
  const names = 'importOpen,csv,preview,importConfirmed,previewing,fileReading,chooseFile,previewImport,commitImport,closeImport,error,saving'
  const imports = { vue: lifecycle, 'vue-router': { onBeforeRouteLeave() {}, onBeforeRouteUpdate() {} }, '../organization': organization, '../i18n': { t: text => text, locale: Vue.ref('zh-CN') }, './settingsScope': { useSettingsScope: () => (scope = scopeModule.useSettingsScope()) } }
  const m = effects.run(() => evaluate(script + '\nexport {' + names + '}', imports, { defineProps: () => ({ context: { isTenantAdmin: true, permissions: [], projects: [], roles: [] } }), defineEmits: () => () => {}, window, localStorage: { getItem: () => project } }))
  m.importOpen.value = true
  return { ...m, calls, events, scope, setProject(value) { project = value }, stop() { unmounts.forEach(fn => fn()); effects.stop() } }
}
function select(m, pending, size = 10) {
  const target = { files: [{ size, text: () => pending.promise }], value: 'fixture.csv' }
  return { target, run: m.chooseFile({ target }) }
}
let count = 0
async function test(name, run) { await run(); count++; console.log('✓ ' + name) }

await test('最新选择的文件胜出，较早成功或失败都不能覆盖文本、错误和读取状态', async () => {
  for (const failOld of [false, true]) {
    const m = fixture(), a = deferred(), b = deferred(), first = select(m, a)
    assert.equal(first.target.value, '')
    const second = select(m, b)
    b.resolve('LATEST_B'); await second.run
    if (failOld) a.reject(Error('STALE_ERROR')); else a.resolve('OLD_A')
    await first.run; await flush()
    assert.equal(m.csv.value, 'LATEST_B'); assert.equal(m.error.value, ''); assert.equal(m.fileReading.value, false)
    m.stop()
  }
})
await test('旧读取先完成也不能提前解除新文件的读取锁', async () => {
  const m = fixture(), a = deferred(), b = deferred(), first = select(m, a), second = select(m, b)
  a.resolve('OLD_A'); await first.run
  assert.equal(m.csv.value, ''); assert.equal(m.fileReading.value, true)
  b.resolve('LATEST_B'); await second.run
  assert.equal(m.csv.value, 'LATEST_B'); assert.equal(m.fileReading.value, false); m.stop()
})
await test('读取中取消并立即重开，旧文本或旧异常均不能污染新草稿', async () => {
  for (const failOld of [false, true]) {
    const m = fixture(), a = deferred(), first = select(m, a)
    m.closeImport(); assert.equal(m.importOpen.value, false); assert.equal(m.fileReading.value, false)
    m.importOpen.value = true; m.csv.value = 'REOPENED_DRAFT'; m.error.value = 'NEW_MESSAGE'
    if (failOld) a.reject(Error('OLD_ERROR')); else a.resolve('CANCELLED_FILE')
    await first.run
    assert.equal(m.csv.value, 'REOPENED_DRAFT'); assert.equal(m.error.value, 'NEW_MESSAGE'); assert.equal(m.fileReading.value, false); m.stop()
  }
})
await test('读取中方法守卫阻止预览和提交，取消仍然可用', async () => {
  const m = fixture(), pending = deferred()
  m.csv.value = 'PREVIOUS_CSV'; await flush()
  const file = select(m, pending)
  m.preview.value = { previewId: 'stale', canCommit: true }; m.importConfirmed.value = true
  await m.previewImport(); await m.commitImport()
  assert.equal(m.calls.length, 0); assert.equal(m.fileReading.value, true)
  m.closeImport(); assert.equal(m.importOpen.value, false)
  pending.resolve('CANCELLED'); await file.run
  assert.equal(m.csv.value, ''); assert.equal(m.preview.value, null); assert.equal(m.fileReading.value, false); m.stop()
})
await test('卸载清理读取和导入内存，迟到结果不会恢复草稿或错误', async () => {
  for (const failed of [false, true]) {
    const m = fixture(), pending = deferred(), file = select(m, pending)
    m.stop()
    if (failed) pending.reject(Error('OLD')); else pending.resolve('PRIVATE_CSV')
    await file.run
    assert.equal(m.csv.value, ''); assert.equal(m.preview.value, null); assert.equal(m.fileReading.value, false); assert.equal(m.error.value, '')
  }
})
await test('真实 settingsScope 的身份、退出、禁用和项目事件作废文件读取', async () => {
  for (const event of ['devflow-identity-changed', 'devflow-auth-expired', 'devflow-account-disabled', 'devflow-project-changed']) {
    const m = fixture(), pending = deferred(), file = select(m, pending)
    m.events.get(event)(); assert.equal(m.scope.locked.value, true); assert.equal(m.fileReading.value, false)
    pending.resolve('OLD_SCOPE'); await file.run
    assert.equal(m.csv.value, ''); assert.equal(m.preview.value, null); assert.equal(m.calls.length, 0); m.stop()
  }
})
await test('未发事件的项目存储变化在文件返回时复核并清理', async () => {
  const m = fixture(), pending = deferred(), file = select(m, pending)
  m.setProject('another-project'); pending.resolve('OLD_PROJECT'); await file.run
  assert.equal(m.scope.locked.value, true); assert.equal(m.csv.value, ''); assert.equal(m.fileReading.value, false); m.stop()
})
await test('取消选择或过大文件会废弃旧读取，当前错误不被旧响应覆盖', async () => {
  const m = fixture(), old = deferred(), first = select(m, old)
  let readLarge = false
  await m.chooseFile({ target: { files: [{ size: 1048577, text: () => { readLarge = true } }], value: 'large.csv' } })
  assert.equal(readLarge, false); assert.match(m.error.value, /1 MB/)
  old.resolve('OLD_FILE'); await first.run
  assert.equal(m.csv.value, ''); assert.match(m.error.value, /1 MB/); assert.equal(m.fileReading.value, false)
  const next = deferred(), second = select(m, next)
  await m.chooseFile({ target: { files: [], value: '' } })
  next.resolve('CANCELLED_SELECTION'); await second.run
  assert.equal(m.csv.value, ''); assert.equal(m.fileReading.value, false); m.stop()
})
await test('当前文件失败可重选，成功读取后仍需真实预览及明确确认', async () => {
  const m = fixture(), failed = deferred(), first = select(m, failed)
  failed.reject(Error('READ_FAILED')); await first.run
  assert.equal(m.fileReading.value, false); assert.equal(m.error.value, '无法读取 CSV 文件')
  const valid = deferred(), second = select(m, valid)
  valid.resolve('name,email\nExample,example@example.test'); await second.run; await flush()
  assert.equal(m.error.value, ''); await m.previewImport(); await m.commitImport()
  assert.equal(m.calls.length, 1); assert.equal(m.calls[0].path, '/organization/members/import/preview')
  assert.equal(JSON.parse(m.calls[0].options.body).csv, m.csv.value)
  m.importConfirmed.value = true; await m.commitImport()
  const commit = m.calls.find(call => call.path.endsWith('/commit'))
  assert.deepEqual(JSON.parse(commit.options.body), { previewId: 'preview-test' }); assert.equal(m.importOpen.value, false); m.stop()
})
await test('模板禁止读取中的文本编辑与两类提交，但取消和文件替换不被读取锁禁用', () => {
  const { descriptor } = parse(source), script = compileScript(descriptor, { id: 'member-import-race' })
  assert.deepEqual(compileTemplate({ source: descriptor.template.content, filename: 'OrganizationMembers.vue', id: 'member-import-race', compilerOptions: { bindingMetadata: script.bindings } }).errors, [])
  const modal = descriptor.template.content.match(/<OrganizationModal v-if="importOpen"[\s\S]*?<\/OrganizationModal>/)[0]
  assert.match(modal, /<textarea v-model="csv" :disabled="saving\|\|previewing\|\|fileReading"/)
  assert.match(modal, /:disabled="saving\|\|previewing\|\|fileReading\|\|!csv\.trim\(\)"/)
  assert.match(modal, /:disabled="saving\|\|previewing\|\|fileReading\|\|!preview\?\.canCommit\|\|!importConfirmed"/)
  assert.match(modal, /:disabled="saving\|\|previewing" @click="closeImport"/)
  assert.match(modal, /type="file"[^>]*:disabled="saving\|\|previewing"/)
})
console.log(`Passed ${count} isolated member CSV file-read race regressions.`)

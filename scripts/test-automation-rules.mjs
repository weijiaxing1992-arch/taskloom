import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import ts from 'typescript'
import * as Vue from 'vue'

const read = path => readFileSync(new URL('../' + path, import.meta.url), 'utf8')
function evaluate(source, imports = {}, extras = {}) {
  const exports = {}
  const js = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText
  new Function('require', 'exports', ...Object.keys(extras), js)(id => imports[id] || {}, exports, ...Object.values(extras))
  return exports
}
const flush = async () => { for (let i = 0; i < 12; i++) await Vue.nextTick() }
const statuses = [{ key: '规划中', name: '规划中', color: '#059669', enabled: true }, { key: '开发中', name: '开发中', color: '#2563EB', enabled: true }]
const members = [{ id: 'u_front', name: '前端同学' }, { id: 'u_back', name: '后端同学' }]
const source = read('src/components/AutomationRulesSettings.vue').match(/<script setup[^>]*>([\s\S]*?)<\/script>/)[1]
const exposed = ['items', 'statuses', 'members', 'canManage', 'loading', 'saving', 'error', 'notice', 'preview', 'opened', 'editing', 'baseline', 'memberSearch', 'form', 'dirty', 'mutable', 'recipientModes', 'selectableMembers', 'filteredMembers', 'currentPayload', 'validate', 'load', 'open', 'close', 'toggleMode', 'toggleMember', 'previewCurrent', 'previewSaved', 'save', 'toggle', 'remove']

async function component(handler, { canManage = true } = {}) {
  const mounts = [], calls = [], scope = Vue.effectScope()
  const settings = { locked: Vue.ref(false), current: () => true, request: async (path, options = {}) => { calls.push({ path, options }); return handler(path, options) } }
  const imports = {
    vue: { ...Vue, onMounted: fn => mounts.push(fn) },
    '../i18n': { t: (text, params = {}) => String(text).replace(/\{(\w+)\}/g, (all, key) => String(params[key] ?? all)) },
    './AppSelect.vue': {},
    './settingsScope': { useSettingsScope: () => settings, useSettingsDialog: () => Vue.ref(null) },
  }
  const globals = { window: { confirm: () => true }, defineExpose: () => {} }
  let value
  scope.run(() => { value = evaluate(source + '\nexport {' + exposed.join(',') + '}', imports, globals) })
  for (const mount of mounts) await mount()
  await flush()
  assert.equal(value.canManage.value, canManage)
  return { ...value, calls, settings, stop: () => scope.stop() }
}

let count = 0
async function test(name, fn) { await fn(); count++; console.log('✓ ' + name) }

await test('automation rules load through the scoped project request and new forms start disabled', async () => {
  const m = await component(async path => {
    if (path === '/automation-rules/executions') return { items: [] }
    assert.equal(path, '/automation-rules')
    return { items: [], statuses, members, canManage: true }
  })
  assert.deepEqual(m.calls.map(call => call.path), ['/automation-rules', '/automation-rules/executions'])
  m.open()
  assert.equal(m.opened.value, true)
  assert.equal(m.form.enabled, false)
  assert.deepEqual(m.currentPayload(), { name: '', enabled: false, trigger: 'requirement.status_changed', fromStatus: '', toStatus: '', action: 'notify', recipientModes: [], recipientUserIds: [] })
  m.stop()
})

await test('trial is non-mutating in the client contract and keeps stable recipient IDs', async () => {
  const m = await component(async (path, options) => {
    if (path === '/automation-rules') return { items: [], statuses, members, canManage: true }
    if (path === '/automation-rules/preview') return { dryRun: true, matchingSampleCount: 2, fromStatusEvaluatedAtRuntime: true, sampleItems: [] }
    throw new Error('unexpected path ' + path)
  })
  m.open()
  m.form.name = '研发交接'
  m.form.fromStatus = '规划中'; m.form.toStatus = '开发中'
  m.toggleMode('assignee'); m.toggleMember('u_back')
  await m.previewCurrent()
  const call = m.calls.at(-1)
  assert.equal(call.path, '/automation-rules/preview')
  assert.equal(call.options.method, 'POST')
  assert.deepEqual(JSON.parse(call.options.body), { name: '研发交接', enabled: false, trigger: 'requirement.status_changed', fromStatus: '规划中', toStatus: '开发中', action: 'notify', recipientModes: ['assignee'], recipientUserIds: ['u_back'] })
  assert.equal(m.preview.value.dryRun, true)
  assert.match(m.notice.value, /未产生任何通知/)
  m.stop()
})

await test('recipient choices target only bound requirement roles and typed lead/tester fields', async () => {
  const m = await component(async path => ({ items: [], statuses, members, canManage: true }))
  const modes = m.recipientModes.map(mode => mode.value)
  assert.deepEqual(modes, ['assignee', 'owner', 'frontend', 'backend', 'algorithm', 'ui', 'product', 'frontend_lead', 'backend_lead', 'tester'])
  assert.equal(modes.includes('qa'), false, 'a project-wide QA cohort must never be a recipient mode')
  m.open(); m.form.name = '绑定前端后提醒'; m.toggleMode('frontend'); m.toggleMode('frontend_lead'); m.toggleMode('tester')
  assert.deepEqual(m.currentPayload().recipientModes, ['frontend', 'frontend_lead', 'tester'])
  const template = read('src/components/AutomationRulesSettings.vue')
  assert.match(template, /需求职能成员及已绑定的前后端负责人\/测试人员/)
  assert.match(template, /模式只表达“从当前需求的哪一项绑定取人”/)
  m.stop()
})

await test('save, toggle, and delete preserve optimistic versions in their requests', async () => {
  const saved = { id: 31, name: '交接提醒', enabled: false, trigger: 'requirement.status_changed', fromStatus: '', toStatus: '开发中', action: 'notify', recipientModes: ['owner'], recipientUserIds: [], version: 1, createdAt: 'now', updatedAt: 'now' }
  const m = await component(async (path, options) => {
    if (path === '/automation-rules' && !options.method) return { items: [], statuses, members, canManage: true }
    if (path === '/automation-rules' && options.method === 'POST') return saved
    if (path === '/automation-rules/31' && options.method === 'PATCH') return { ...saved, enabled: true, version: 2 }
    if (path === '/automation-rules/31?version=2' && options.method === 'DELETE') return { id: 31, deleted: true }
    throw new Error('unexpected path ' + path)
  })
  m.open(); m.form.name = saved.name; m.form.toStatus = saved.toStatus; m.toggleMode('owner')
  await m.save()
  const create = m.calls.find(call => call.path === '/automation-rules' && call.options.method === 'POST')
  assert.equal(JSON.parse(create.options.body).enabled, false)
  assert.equal(m.opened.value, false)
  await m.toggle(m.items.value[0])
  const toggle = m.calls.find(call => call.path === '/automation-rules/31' && call.options.method === 'PATCH')
  assert.deepEqual(JSON.parse(toggle.options.body), { enabled: true, version: 1 })
  await m.remove(m.items.value[0])
  assert.equal(m.items.value.length, 0)
  assert.ok(m.calls.some(call => call.path === '/automation-rules/31?version=2' && call.options.method === 'DELETE'))
  m.stop()
})

await test('read-only automation settings never open a CRUD form or issue mutation requests', async () => {
  const m = await component(async path => ({ items: [], statuses, members, canManage: false }), { canManage: false })
  m.open(); await m.previewCurrent()
  assert.equal(m.opened.value, false)
  assert.equal(m.calls.length, 1)
  m.stop()
})

await test('automation rule UI keeps the fixed trigger/action, accessible controls, and no external channel language', () => {
  const template = read('src/components/AutomationRulesSettings.vue')
  assert.match(template, /requirement\.status_changed/)
  assert.match(template, /action:\s*'notify'/)
  assert.match(template, /aria-modal="true"/)
  assert.match(template, /自动化规则已保存；默认保持关闭/)
  assert.doesNotMatch(template, /webhook.*request|fetch\(/i)
})

console.log(`Passed ${count} automation rule frontend tests.`)

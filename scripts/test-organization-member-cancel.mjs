import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import ts from 'typescript'
import * as Vue from 'vue'
import { parse, compileScript, compileTemplate } from 'vue/compiler-sfc'

const read = path => readFileSync(new URL('../' + path, import.meta.url), 'utf8')
function evaluate(source, imports = {}, globals = {}) {
  const exports = {}, code = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText
  new Function('require', 'exports', ...Object.keys(globals), code)(id => imports[id] || {}, exports, ...Object.values(globals))
  return exports
}
const organization = evaluate(read('src/organization.ts'))
const member = extra => ({ id: 'u_1', name: '成员原文', email: 'member@example.test', employeeNo: '001', tenantRole: 'member', active: true, departmentIds: ['dep_1'], primaryDepartmentId: 'dep_1', projectMemberships: [{ projectId: 'p', role: 'viewer' }], groupIds: [], ...extra })
const flush = async () => { for (let i = 0; i < 5; i++) await Vue.nextTick() }
const deferred = () => { let resolve, reject; const promise = new Promise((yes, no) => { resolve = yes; reject = no }); return { promise, resolve, reject } }
function fixture(handler = async () => ({ items: [] })) {
  const effects = Vue.effectScope(), calls = [], unmounts = [], events = new Map(), emitted = []
  const props = { context: { isTenantAdmin: true, permissions: [], roles: [{ key: 'viewer', name: '只读' }], projects: [], initialPasswordConfigured: true } }
  let confirmations = 0
  const window = { confirm() { confirmations++; throw Error('Native dialogs are unavailable in this embedded browser') }, addEventListener: (event, fn) => events.set(event, fn), removeEventListener: event => events.delete(event) }
  const imports = { vue: { ...Vue, onMounted: () => {}, onBeforeUnmount: fn => unmounts.push(fn) }, 'vue-router': { onBeforeRouteLeave: () => {}, onBeforeRouteUpdate: () => {} }, '../organization': organization, '../i18n': { t: text => text, locale: Vue.ref('zh-CN') }, './settingsScope': { useSettingsScope: () => ({ locked: Vue.ref(false), project: 'p', current: () => true, request: async (path, options) => { calls.push({ path, options }); return handler(path, options) } }) } }
  const source = read('src/components/OrganizationMembers.vue').match(/<script setup[^>]*>([\s\S]*?)<\/script>/)[1]
  const names = 'open,close,save,opened,editing,form,baseline,dirty,memberDirty,discardMemberOpen,discardMemberPrompt,discardMemberChanges,keepMemberEditing,saving,importOpen,csv,impersonating,reason,error,showPassword'
  const value = effects.run(() => evaluate(source + '\nexport {' + names + '}', imports, { defineProps: () => props, defineEmits: () => (...args) => emitted.push(args), window, localStorage: { getItem: () => 'p' } }))
  return { ...value, calls, emitted, props, get confirmations() { return confirmations }, stop() { unmounts.forEach(fn => fn()); effects.stop() } }
}
let count = 0
async function test(name, run) { await run(); count++; console.log('✓ ' + name) }

await test('untouched new-member dialog cancels before and after watchers settle without native confirmation or a request', async () => {
  const m = fixture()
  for (const settled of [false, true]) {
    m.open(); if (settled) await flush()
    assert.equal(m.form.initialPassword,'');assert.equal(m.form.employeeNo,'');assert.equal(m.showPassword.value,false)
    const markup=read('src/components/OrganizationMembers.vue')
    assert.match(markup,/请为新成员设置独立临时密码，首次登录必须修改。/)
    assert.match(markup,/:required="!editing"/)
    assert.equal(m.memberDirty.value, false); assert.equal(m.form.active, true); assert.deepEqual(m.form.departmentIds, [])
    m.close(); assert.equal(m.opened.value, false); assert.equal(m.discardMemberOpen.value, false)
  }
  assert.equal(m.confirmations, 0); assert.deepEqual(m.calls, []); m.stop()
})
await test('legacy empty or invalid primary departments normalize before the baseline and do not mark untouched edits dirty', async () => {
  for (const primaryDepartmentId of [undefined, null, '', 'departed-department']) {
    const original = member({ primaryDepartmentId }), before = JSON.stringify(original), m = fixture()
    m.open(original); assert.equal(m.form.primaryDepartmentId, 'dep_1'); await flush()
    assert.equal(m.memberDirty.value, false); assert.equal(JSON.stringify(original), before)
    m.close(); assert.equal(m.opened.value, false); assert.equal(m.confirmations, 0); m.stop()
  }
})
await test('blank member cancellation ignores unrelated import and impersonation drafts without discarding them', () => {
  const m = fixture(); m.importOpen.value = true; m.csv.value = 'Pending CSV'; m.impersonating.value = member(); m.reason.value = '另一个待处理理由'; m.open()
  assert.equal(m.dirty.value, true); assert.equal(m.memberDirty.value, false)
  m.close(); assert.equal(m.opened.value, false); assert.equal(m.importOpen.value, true); assert.equal(m.csv.value, 'Pending CSV'); assert.equal(m.reason.value, '另一个待处理理由'); assert.equal(m.confirmations, 0); m.stop()
})
await test('real edits open a visible discard choice even when native confirmation is unavailable', async () => {
  const m = fixture(); m.open(); m.form.name = '未保存成员'; m.form.initialPassword = 'temporary-draft'; let focused = 0; m.discardMemberPrompt.value = { focus: () => focused++ }
  m.close(); await flush(); assert.equal(m.opened.value, true); assert.equal(m.discardMemberOpen.value, true); assert.equal(focused, 1); assert.equal(m.confirmations, 0)
  await m.save(); assert.deepEqual(m.calls, []); assert.equal(m.form.initialPassword, 'temporary-draft')
  m.keepMemberEditing(); assert.equal(m.discardMemberOpen.value, false); assert.equal(m.form.name, '未保存成员'); assert.equal(m.form.initialPassword, 'temporary-draft')
  m.close(); m.discardMemberChanges(); assert.equal(m.opened.value, false); assert.equal(m.discardMemberOpen.value, false); assert.equal(m.form.initialPassword, ''); assert.deepEqual(m.calls, [])
  m.open(); assert.equal(m.form.name, ''); assert.equal(m.memberDirty.value, false); m.stop()
})
await test('department removal is a real edit, while restoring the draft removes the discard choice', async () => {
  const m = fixture(), original = member({ departmentIds: ['dep_1', 'dep_2'] }); m.open(original)
  m.form.departmentIds = ['dep_2']; assert.equal(m.form.primaryDepartmentId, 'dep_2'); assert.equal(m.memberDirty.value, true); m.close(); assert(m.discardMemberOpen.value)
  m.form.departmentIds = ['dep_1', 'dep_2']; m.form.primaryDepartmentId = 'dep_1'; await flush(); assert.equal(m.memberDirty.value, false); assert.equal(m.discardMemberOpen.value, false)
  m.close(); assert.equal(m.opened.value, false); assert.deepEqual(original.departmentIds, ['dep_1', 'dep_2']); m.stop()
})
await test('an empty project-role row remains a real draft but never forces invalid form submission when cancelling', async () => {
  const m = fixture(); m.open(); m.form.projectMemberships.push({ projectId: '', role: 'viewer' })
  assert.equal(m.memberDirty.value, true); m.close(); assert.equal(m.discardMemberOpen.value, true)
  await m.save(); assert.deepEqual(m.calls, []); m.discardMemberChanges(); assert.equal(m.opened.value, false)
  m.open(); assert.deepEqual(m.form.projectMemberships, []); assert.equal(m.memberDirty.value, false); m.stop()
})
await test('saving blocks both close and discard, and a failed save preserves the editable draft', async () => {
  const pending = deferred(), m = fixture(async (path, options) => options?.method ? pending.promise : { items: [] })
  m.open(); m.form.name = '待保存'; m.form.email = 'valid@example.test'; const saving = m.save()
  m.close(); m.discardMemberChanges(); assert.equal(m.opened.value, true); assert.equal(m.discardMemberOpen.value, false); assert.equal(m.calls.length, 1)
  pending.reject(Error('Save failed')); await saving; assert.equal(m.saving.value, false); assert.equal(m.form.name, '待保存')
  m.close(); assert.equal(m.discardMemberOpen.value, true); m.discardMemberChanges(); assert.equal(m.opened.value, false); assert.equal(m.calls.length, 1); m.stop()
})
await test('cancel and discard actions are non-submit buttons; only Save is associated with the member form', () => {
  const filename = 'src/components/OrganizationMembers.vue', { descriptor } = parse(read(filename)), script = compileScript(descriptor, { id: 'member-cancel' })
  assert.deepEqual(compileTemplate({ source: descriptor.template.content, filename, id: 'member-cancel', compilerOptions: { bindingMetadata: script.bindings } }).errors, [])
  const seen = new Set()
  function walk(node) {
    if (node.type === 1 && node.tag === 'Button') {
      const handler = node.props.find(prop => prop.type === 7 && prop.name === 'on' && prop.arg?.content === 'click')?.exp?.content
      if (['close', 'keepMemberEditing', 'discardMemberChanges'].includes(handler)) {
        assert.equal(node.props.find(prop => prop.type === 6 && prop.name === 'type')?.value?.content, 'button')
        assert(!node.props.some(prop => prop.name === 'form')); seen.add(handler)
      }
    }
    for (const child of node.children || []) walk(child)
  }
  walk(descriptor.template.ast); assert.deepEqual([...seen].sort(), ['close', 'discardMemberChanges', 'keepMemberEditing'])
  assert.match(descriptor.template.content, /form="org-member-form" type="submit"/)
  assert.match(descriptor.template.content, /class="member-discard-prompt" tabindex="-1" role="alert"/)
})
await test('modal close button, backdrop and Escape delegate the same parent guard and respect an active save', () => {
  const filename = 'src/components/OrganizationModal.vue', source = read(filename), body = source.match(/<script setup[^>]*>([\s\S]*?)<\/script>/)[1], props = Vue.reactive({ title: '成员', busy: false }), emitted = []
  let escapeClose
  const modal = evaluate(body + '\nexport {close}', { vue: { ...Vue, onMounted: () => {} }, '../i18n': { t: text => text }, './settingsScope': { useSettingsDialog: (open, close) => { escapeClose = close; return Vue.ref(null) } } }, { defineProps: () => props, defineEmits: () => name => emitted.push(name) })
  props.busy = true; modal.close(); escapeClose(); assert.deepEqual(emitted, [])
  props.busy = false; modal.close(); escapeClose(); assert.deepEqual(emitted, ['close', 'close'])
  assert.match(source, /@click.self="close"/); assert.match(source, /<Button type="button"[^>]*@click="close"/)
})
console.log(`Passed ${count} member dialog cancellation regressions.`)

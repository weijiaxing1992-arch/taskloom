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
const wecom = evaluate(read('src/wecomWebhook.ts'))
const translate = (source, params = {}) => source.replace(/\{(\w+)\}/g, (token, key) => Object.hasOwn(params, key) ? String(params[key]) : token)
const member = (id, extra = {}) => ({ id, name: '测试成员 ' + id, email: id + '@example.test', employeeNo: id, active: false, operationDisabled: false, tenantRole: 'member', groupIds: [], departmentIds: [], primaryDepartmentId: '', projectMemberships: [], ...extra })
const deferred = () => { let resolve, reject; const promise = new Promise((yes, no) => { resolve = yes; reject = no }); return { promise, resolve, reject } }
const flush = async () => { for (let i = 0; i < 5; i++) await Vue.nextTick() }

function fixture(rows = [member('u_1')], options = {}) {
  const effects = Vue.effectScope(), calls = [], emitted = [], unmounts = [], locked = Vue.ref(false), events = new Map()
  const props = Vue.reactive({ context: { isTenantAdmin: true, permissions: [], roles: [], projects: [], initialPasswordConfigured: true, ...options.context } })
  const auth = { user: { id: 'me' }, impersonation: null, ...options.auth }
  let handler = options.handler || (async (path, init) => {
    if (path === '/organization/members/bulk') { const { userIds } = JSON.parse(init.body); return { affected: userIds.length, userIds } }
    if (init) return {}
    return path === '/session' ? auth : { items: path === '/organization/members' ? rows : [] }
  })
  let project = 'p', nativeConfirmations = 0
  const scope = { locked, project: 'p', current: () => { if (project !== 'p') locked.value = true; return !locked.value }, request: async (path, init) => { calls.push({ path, init }); return handler(path, init) } }
  const imports = { vue: { ...Vue, onMounted: () => {}, onBeforeUnmount: fn => unmounts.push(fn) }, 'vue-router': { onBeforeRouteLeave: () => {}, onBeforeRouteUpdate: () => {} }, '../organization': organization, '../i18n': { t: translate, locale: Vue.ref('zh-CN') }, './settingsScope': { useSettingsScope: () => scope } }
  const source = read('src/components/OrganizationMembers.vue').match(/<script setup[^>]*>([\s\S]*?)<\/script>/)[1]
  const names = 'items,departments,session,loading,saving,query,department,status,page,filtered,paged,selectedIds,selectedMembers,selectionBusy,selectionNotice,allPageSelected,somePageSelected,canBulk,bulkAction,bulkTargets,bulkUncertain,bulkDialog,bulkReason,prepareBulk,confirmBulk,closeBulk,toggleMemberSelection,togglePageSelection,selectAllFiltered,clearSelection,activateMember,toggleStatus,canToggleStatus,load,notice,error,canLeave,beforeProjectChange,beforeUnload,opened,form'
  const m = effects.run(() => evaluate(source + '\nexport {' + names + '}', imports, { defineProps: () => props, defineEmits: () => (...args) => emitted.push(args), window: { confirm: () => { nativeConfirmations++; throw Error('Native confirmation unavailable') }, addEventListener: (key, fn) => events.set(key, fn), removeEventListener: key => events.delete(key) }, localStorage: { getItem: () => project } }))
  m.items.value = rows; m.session.value = auth; m.loading.value = false
  return { ...m, props, calls, emitted, locked, events, get nativeConfirmations() { return nativeConfirmations }, setProject(value) { project = value }, setHandler(fn) { handler = fn }, stop() { unmounts.forEach(fn => fn()); effects.stop() } }
}

function dialog(options = {}) {
  const props = Vue.reactive({ action: 'activate', members: [member('u_1')], currentUserId: 'me', busy: false, error: '', uncertain: false, ...options })
  const effects = Vue.effectScope(), emitted = [], unmounts = []
  const source = read('src/components/OrganizationMemberBulkDialog.vue').match(/<script setup[^>]*>([\s\S]*?)<\/script>/)[1]
  const names = 'confirmed,protectedConfirmed,sharedConfirmed,mode,replacementState,url,localError,discardOpen,discardPrompt,dirty,maySubmit,protectedMembers,submit,discard,requestClose,canLeave'
  const m = effects.run(() => evaluate(source + '\nexport {' + names + '}', { vue: { ...Vue, onBeforeUnmount: fn => unmounts.push(fn) }, '../i18n': { t: translate }, '../wecomWebhook': wecom }, { defineProps: () => props, defineEmits: () => (...args) => emitted.push(args), defineExpose: () => {} }))
  return { ...m, props, emitted, stop() { unmounts.forEach(fn => fn()); effects.stop() } }
}

let count = 0
async function test(name, run) { await run(); count++; console.log('✓ ' + name) }

await test('page selection, cross-page selection and deselection have explicit stable target sets', () => {
  const rows = Array.from({ length: 65 }, (_, i) => member('u_' + String(i).padStart(3, '0'))), m = fixture(rows)
  m.togglePageSelection(); assert.equal(m.selectedIds.value.length, 30); assert(m.allPageSelected.value)
  m.page.value = 2; assert.equal(m.allPageSelected.value, false); m.toggleMemberSelection(m.paged.value[0].id); assert(m.somePageSelected.value); assert.equal(m.selectedIds.value.length, 31)
  m.selectAllFiltered(); assert.equal(m.selectedIds.value.length, 65); assert.match(m.selectionNotice.value, /其他分页/)
  m.togglePageSelection(); assert.equal(m.selectedIds.value.length, 35); assert.equal(m.somePageSelected.value, false)
  assert.deepEqual(m.calls, []); m.stop()
})

await test('filter changes synchronously clear hidden selections and any unopened confirmation target snapshot', () => {
  for (const field of ['query', 'department', 'status']) {
    const m = fixture([member('u_1')]); m.selectAllFiltered(); m.prepareBulk('activate'); assert(m.bulkAction.value)
    m[field].value = field === 'status' ? 'inactive' : 'changed'
    assert.deepEqual(m.selectedIds.value, []); assert.equal(m.bulkAction.value, null); assert.deepEqual(m.bulkTargets.value, []); assert.equal(m.page.value, 1); assert.match(m.selectionNotice.value, /筛选已变化/); assert.deepEqual(m.calls, []); m.stop()
  }
})

await test('selection limit never silently truncates all filtered results or partially selects a page', () => {
  const rows = Array.from({ length: 201 }, (_, i) => member('u_' + String(i).padStart(3, '0'))), m = fixture(rows)
  m.selectAllFiltered(); assert.equal(m.selectedIds.value.length, 0); assert.match(m.selectionNotice.value, /200/)
  for (const row of rows) m.toggleMemberSelection(row.id)
  assert.equal(m.selectedIds.value.length, 200); assert(!m.selectedIds.value.includes(rows[200].id))
  m.page.value = 7; const before = [...m.selectedIds.value]; m.togglePageSelection(); assert.deepEqual(m.selectedIds.value, before)
  assert.deepEqual(m.calls, []); m.stop()
})

await test('deactivation and deletion reject protected selections without silently removing admins or self', async () => {
  const rows = [member('me'), member('admin', { tenantRole: 'tenant_admin' }), member('normal')], m = fixture(rows)
  m.selectAllFiltered()
  for (const action of ['deactivate', 'delete']) { assert.match(m.bulkReason(action), /当前账号或企业管理员/); m.prepareBulk(action); await m.confirmBulk(); assert.equal(m.bulkAction.value, null) }
  assert.equal(m.selectedIds.value.length, 3); assert.deepEqual(m.calls, [])
  m.toggleMemberSelection('me'); m.toggleMemberSelection('admin'); m.prepareBulk('delete'); assert.deepEqual(m.bulkTargets.value.map(x => x.id), ['normal']); m.stop()
})

await test('delegated managers cannot activate protected members or delete, but may configure their own webhook', () => {
  const rows = [member('me', { groupIds: ['permission-group'] }), member('manager', { groupIds: ['another-group'] }), member('admin', { tenantRole: 'tenant_admin' }), member('normal')]
  const m = fixture(rows, { context: { isTenantAdmin: false, permissions: ['members.manage'] } })
  m.toggleMemberSelection('normal'); assert.equal(m.bulkReason('activate'), ''); assert.match(m.bulkReason('delete'), /仅企业管理员/)
  m.clearSelection(); m.toggleMemberSelection('manager'); assert.match(m.bulkReason('activate'), /权限组成员/); assert.match(m.bulkReason('wecom-config'), /权限组成员/)
  m.clearSelection(); m.toggleMemberSelection('admin'); assert.match(m.bulkReason('activate'), /管理员/)
  m.clearSelection(); m.toggleMemberSelection('me'); assert.equal(m.bulkReason('wecom-config'), ''); assert.match(m.bulkReason('activate'), /权限组成员/)
  m.session.value.impersonation = {}; assert.equal(m.canBulk.value, false); m.clearSelection(); m.toggleMemberSelection('normal'); assert.equal(m.selectedIds.value.length, 0); m.stop()
})

await test('single quick activation sends both activation fields and keeps business-disable toggling distinct', async () => {
  const row = member('inactive', { operationDisabled: true }), m = fixture([row])
  await m.activateMember(row)
  const activation = m.calls.find(x => x.init?.method === 'PATCH'); assert.equal(activation.path, '/organization/members/inactive'); assert.deepEqual(JSON.parse(activation.init.body), { active: true, operationDisabled: false }); assert.match(m.notice.value, /已激活，请确认登录凭据和项目权限/)
  m.calls.length = 0; row.active = true; await m.activateMember(row); assert.equal(m.calls.length, 0)
  await m.toggleStatus(row); assert.deepEqual(JSON.parse(m.calls.find(x => x.init?.method === 'PATCH').init.body), { operationDisabled: false }); m.stop()
})

await test('failed quick activation keeps the account unchanged and exposes credential errors', async () => {
  const row = member('no_password'), m = fixture([row], { handler: async () => { throw Error('请先设置初始密码') } })
  await m.activateMember(row); assert.equal(row.active, false); assert.equal(m.saving.value, false); assert.match(m.error.value, /初始密码/); assert.equal(m.notice.value, ''); assert.deepEqual(m.emitted, []); m.stop()
})

await test('bulk confirmation snapshots names and IDs, prevents duplicate requests and clears selection only after complete success', async () => {
  const pending = deferred(), rows = [member('a'), member('b')], m = fixture(rows)
  m.setHandler(async (path, init) => init ? pending.promise : path === '/session' ? { user: { id: 'me' } } : { items: path === '/organization/members' ? rows : [] })
  m.selectAllFiltered(); m.prepareBulk('activate'); const originalName = m.bulkTargets.value[0].name; rows[0].name = 'Changed after confirmation opened'; assert.equal(m.bulkTargets.value[0].name, originalName); assert.equal(m.calls.length, 0)
  const run = m.confirmBulk(); await m.confirmBulk(); assert.equal(m.calls.length, 1); assert.deepEqual(JSON.parse(m.calls[0].init.body), { action: 'activate', userIds: ['a', 'b'] }); assert.equal(m.calls[0].init.method, 'POST')
  m.closeBulk(); m.toggleMemberSelection('a'); assert.equal(m.bulkAction.value, 'activate'); assert.equal(m.selectedIds.value.length, 2); assert.equal(m.canLeave(), false)
  pending.resolve({ affected: 2, userIds: ['b', 'a'] }); await run; assert.equal(m.bulkAction.value, null); assert.deepEqual(m.selectedIds.value, []); assert.match(m.notice.value, /2/); assert.equal(m.emitted.length, 1); m.stop()
})

await test('bulk failures keep the exact targets, while ambiguous responses disable retries until the user verifies', async () => {
  const m = fixture([member('a'), member('b')]); m.selectAllFiltered(); m.prepareBulk('deactivate')
  m.setHandler(async () => { throw Error('Atomic validation failed') }); await m.confirmBulk(); assert.deepEqual(m.selectedIds.value, ['a', 'b']); assert.equal(m.bulkAction.value, 'deactivate'); assert.equal(m.bulkUncertain.value, false); assert.match(m.error.value, /Atomic/); assert.deepEqual(m.emitted, [])
  m.setHandler(async () => ({ affected: 1, userIds: ['a'] })); await m.confirmBulk(); assert(m.bulkUncertain.value); assert.match(m.error.value, /勿重复提交/); const calls = m.calls.length; await m.confirmBulk(); assert.equal(m.calls.length, calls); m.closeBulk(); assert.equal(m.bulkAction.value, null); m.stop()
  const n = fixture(); n.selectAllFiltered(); n.prepareBulk('activate'); n.setHandler(async () => { throw Object.assign(Error('Network disconnected'), { status: 0 }) }); await n.confirmBulk(); assert(n.bulkUncertain.value); const attempts = n.calls.length; await n.confirmBulk(); assert.equal(n.calls.length, attempts); n.stop()
})

await test('webhook request sends only the chosen batch fields and does not call test-send or member mutation endpoints', async () => {
  const m = fixture([member('a')]); m.selectAllFiltered(); m.prepareBulk('wecom-config'); await m.confirmBulk({ enabled: false })
  const writes = m.calls.filter(x => x.init?.method); assert.equal(writes.length, 1); assert.equal(writes[0].path, '/organization/members/bulk'); assert.deepEqual(JSON.parse(writes[0].init.body), { action: 'wecom-config', userIds: ['a'], webhook: { enabled: false } }); m.stop()
})

await test('scope changes and unmounts erase snapshots and ignore late successful mutations', async () => {
  for (const dispose of [false, true]) {
    const pending = deferred(), m = fixture([member('a')], { handler: async () => pending.promise }); m.selectAllFiltered(); m.prepareBulk('activate'); const run = m.confirmBulk()
    if (dispose) m.stop(); else m.locked.value = true
    assert.deepEqual(m.selectedIds.value, []); assert.equal(m.bulkAction.value, null); assert.deepEqual(m.bulkTargets.value, [])
    pending.resolve({ affected: 1, userIds: ['a'] }); await run; assert.equal(m.notice.value, ''); assert.equal(m.emitted.length, 0); if (!dispose) m.stop()
  }
})

await test('reload invalidates selections and stale read responses cannot restore a previous directory', async () => {
  const first = deferred(), second = deferred(), m = fixture([member('a')]); let reads = 0
  m.setHandler(async path => path === '/organization/members' ? (++reads === 1 ? first.promise : second.promise) : path === '/session' ? { user: { id: 'me' } } : { items: [] })
  m.selectAllFiltered(); const old = m.load(); assert.deepEqual(m.selectedIds.value, []); const newer = m.load(); second.resolve({ items: [member('new')] }); await newer; first.resolve({ items: [member('old')] }); await old; assert.deepEqual(m.items.value.map(x => x.id), ['new']); m.stop()
})

await test('bulk webhook drafts participate in route, project-switch and unload guards without native dialogs', () => {
  const m = fixture(); m.bulkDialog.value = { dirty: true, canLeave: () => false }; assert.equal(m.canLeave(), false)
  let blocked = 0; m.beforeProjectChange({ preventDefault: () => blocked++ }); m.beforeUnload({ preventDefault: () => blocked++ }); assert.equal(blocked, 2); assert.equal(m.nativeConfirmations, 0)
  m.bulkDialog.value = { dirty: false, canLeave: () => true }; assert.equal(m.canLeave(), true); m.stop()
})

await test('dialog requires the full-list confirmation plus explicit protected-target acknowledgment', () => {
  const d = dialog({ members: [member('admin', { tenantRole: 'tenant_admin' }), member('me')] }); d.submit(); assert.equal(d.emitted.length, 0)
  d.confirmed.value = true; d.submit(); assert.equal(d.emitted.length, 0); d.protectedConfirmed.value = true; d.submit(); assert.deepEqual(d.emitted, [['confirm']]); d.stop()
  for (const action of ['deactivate', 'delete']) { const p = dialog({ action, members: [member('me')] }); p.confirmed.value = true; p.protectedConfirmed.value = true; p.submit(); assert.equal(p.maySubmit.value, false); assert.equal(p.emitted.length, 0); p.stop() }
})

await test('existing webhook enable and disable preserve individual addresses; URL replacement requires explicit shared consent', () => {
  const d = dialog({ action: 'wecom-config' }); d.confirmed.value = true; d.submit(); assert.deepEqual(d.emitted.pop(), ['confirm', { enabled: true }])
  d.mode.value = 'disable-existing'; assert.equal(d.confirmed.value, false); d.confirmed.value = true; d.submit(); assert.deepEqual(d.emitted.pop(), ['confirm', { enabled: false }])
  d.mode.value = 'replace'; d.url.value = 'https://example.test/not-a-webhook'; d.confirmed.value = true; d.sharedConfirmed.value = true; d.submit(); assert.equal(d.emitted.length, 0); assert.match(d.localError.value, /有效/)
  const address = 'https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=' + 'x'.repeat(32); d.url.value = address; assert.equal(d.confirmed.value, false); assert.equal(d.sharedConfirmed.value, false)
  d.confirmed.value = true; d.submit(); assert.equal(d.emitted.length, 0); d.sharedConfirmed.value = true; d.submit(); assert.deepEqual(d.emitted.pop(), ['confirm', { url: address }])
  d.replacementState.value = 'enable'; assert.equal(d.confirmed.value, false); d.confirmed.value = true; d.sharedConfirmed.value = true; d.submit(); assert.deepEqual(d.emitted.pop(), ['confirm', { url: address, enabled: true }]); d.stop(); assert.equal(d.url.value, '')
})

await test('closing dirty webhook drafts offers an inline discard choice and saving or uncertain results block submission', async () => {
  const d = dialog({ action: 'wecom-config' }); d.mode.value = 'replace'; d.url.value = 'draft-secret'; let focus = 0; d.discardPrompt.value = { focus: () => focus++ }
  assert.equal(d.requestClose(), false); await flush(); assert(d.discardOpen.value); assert.equal(focus, 1); assert.equal(d.emitted.length, 0); assert.equal(d.url.value, 'draft-secret')
  d.props.busy = true; d.discard(); assert.equal(d.url.value, 'draft-secret'); assert.equal(d.canLeave(), false)
  d.props.busy = false; d.discard(); assert.equal(d.url.value, ''); assert.deepEqual(d.emitted.pop(), ['close']); d.stop()
  const uncertain = dialog({ uncertain: true }); uncertain.confirmed.value = true; uncertain.submit(); assert.equal(uncertain.emitted.length, 0); uncertain.stop()
})

await test('both templates compile and selection/activation controls cannot accidentally submit or open member editors', () => {
  for (const filename of ['src/components/OrganizationMembers.vue', 'src/components/OrganizationMemberBulkDialog.vue']) {
    const { descriptor, errors } = parse(read(filename)); assert.deepEqual(errors, []); const script = compileScript(descriptor, { id: filename }); assert.deepEqual(compileTemplate({ source: descriptor.template.content, filename, id: filename, compilerOptions: { bindingMetadata: script.bindings } }).errors, [])
  }
  const main = read('src/components/OrganizationMembers.vue'), bulk = read('src/components/OrganizationMemberBulkDialog.vue')
  assert.match(main, /:indeterminate="somePageSelected"/); assert.match(main, /class="member-check-cell" @click.stop @dblclick.stop/); assert.match(main, /@click.stop="activateMember\(member\)" @dblclick.stop/)
  // 状态文字必须来自真实模板，避免伪元素覆盖翻译或隐藏快速激活入口。
  assert.match(main, /@click.stop="activateMember\(member\)" @dblclick.stop>\{\{t\('激活'\)\}\}/)
  const statusRules = [...main.matchAll(/\.member-status-button[^{}]*\{([^}]*)\}/g)]
  assert(statusRules.length); assert(statusRules.some(rule => /font-size:var\(--ui-font-body\)/.test(rule[1])))
  for (const rule of statusRules) { assert(!/::before|::after/.test(rule[0])); assert(!/font-size:0|box-shadow|transform/.test(rule[1])) }
  assert.match(bulk, /type="password" autocomplete="off"/); assert(!/localStorage|sessionStorage|clipboard|fetch\(|scope.request/.test(bulk)); assert.match(bulk, /form="org-member-bulk-form" type="submit"/)
})

console.log(`Passed ${count} organization member bulk regressions (isolated, no business writes).`)

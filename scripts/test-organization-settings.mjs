import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import ts from 'typescript'
import * as Vue from 'vue'
import { parse, compileTemplate } from 'vue/compiler-sfc'

const read = path => readFileSync(new URL('../' + path, import.meta.url), 'utf8')
function evaluate(source, imports = {}, globals = {}) {
  const exports = {}, code = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText
  new Function('require', 'exports', ...Object.keys(globals), code)(id => id==='../layoutScope'?{useLayoutBoolean:(_key,value)=>Vue.ref(value)}:imports[id] || {}, exports, ...Object.values(globals))
  return exports
}
const helpers = evaluate(read('src/organization.ts'))
const context = (extra = {}) => ({ organization: { id: 'tenant', name: 'Test organization' }, isTenantAdmin: true, permissions: [], roles: [{ key: 'viewer', name: '只读成员' }, { key: 'developer', name: '开发工程师' }, { key: 'project_admin', name: '项目管理员' }], projects: [{ id: 'p', name: 'Project one', code: 'P', status: 'active' }, { id: 'q', name: 'Project two', code: 'Q', status: 'active' }], ...extra })
const member = (extra = {}) => ({ id: 'u', name: '同名成员', email: 'first@example.test', employeeNo: '001', active: true, tenantRole: 'member', departmentIds: ['frontend'], primaryDepartmentId: 'frontend', projectMemberships: [{ projectId: 'p', role: 'developer' }], groupIds: [], ...extra })
const department = (id, parentId = null) => ({ id, name: id, code: id, parentId, status: 'active', sortOrder: 0, memberCount: 0 })
const deferred = () => { let resolve, reject; const promise = new Promise((a, b) => { resolve = a; reject = b }); return { promise, resolve, reject } }
const common = 'loading,saving,error,notice,opened,dirty,form,load,close,canLeave,beforeProjectChange,cancelProjectLeave,beforeUnload'
const exposed = {
  Members: common + ',items,departments,session,filtered,department,query,editing,open,save,roleName,projectRoleOptions,importOpen,csv,preview,importConfirmed,previewImport,commitImport,webhookMember,webhookEditor,webhookBusy,openWebhook,closeWebhook,canConfigureWebhook,deleteTarget,deleteConfirmed,canDeleteMember,prepareDelete,removeMember,closeDelete,impersonating,reason,canImpersonateMember,validImpersonationReason,prepareImpersonation,startImpersonation,closeImpersonation',
  Directory: common + ',departments,members,permissions,selected,editing,open,save,eligibleParents,hasAncestor,selectAll,canManage',
  Invitations: common + ',allowed,createDialog,create,createdLink,review,decision,approval,reviewConfirmed,startReview,submitReview',
}
function setup(kind, options = {}) {
  const source = read('src/components/Organization' + kind + '.vue').match(/<script setup[^>]*>([\s\S]*?)<\/script>/)[1]
  const props = Vue.reactive({ context: context(options.context), section: options.section || (kind === 'Directory' ? 'departments' : 'invitations') })
  const effects = Vue.effectScope(), calls = [], emits = [], mounted = [], unmount = [], routes = [], events = new Map(), confirmations = [], locked = Vue.ref(false)
  let confirm = true, project = 'p', handler = options.handler || (async path => path === '/session' ? { user: { id: 'me' } } : { items: [], id: 'new' })
  const scope = { locked, project: 'p', current: () => !locked.value, request: async (path, init) => { calls.push({ path, init }); return handler(path, init) } }
  const imports = { vue: { ...Vue, onMounted: fn => mounted.push(fn), onBeforeUnmount: fn => unmount.push(fn) }, 'vue-router': { onBeforeRouteLeave: fn => routes.push(fn), onBeforeRouteUpdate: fn => routes.push(fn) }, '../organization': helpers, './settingsScope': { useSettingsScope: () => scope }, '../i18n': { t: (x, params = {}) => x.replace(/\{(\w+)\}/g, (token, key) => Object.hasOwn(params, key) ? String(params[key]) : token), formatDate: x => x, locale:Vue.ref('zh-CN') } }
  const m = effects.run(() => evaluate(source + '\nexport {' + exposed[kind] + '}', imports, { defineProps: () => props, defineEmits: () => (...args) => emits.push(args), localStorage: { getItem: () => project, setItem: (_key, value) => { project = String(value) } }, window: { confirm: message => { confirmations.push(message); return confirm }, addEventListener: (key, fn) => events.set(key, fn), removeEventListener: key => events.delete(key) }, location: { origin: 'http://127.0.0.1:19084', href: '' } }))
  return { ...m, props, calls, emits, locked, events, routes, confirmations, setConfirm(value) { confirm = value }, setProject(value) { project = value }, setHandler(value) { handler = value }, mount() { mounted.forEach(fn => fn()) }, stop() { unmount.forEach(fn => fn()); effects.stop() } }
}
function setupLegacyMembers() {
  const source = read('src/views/Members.vue').match(/<script setup[^>]*>([\s\S]*?)<\/script>/)[1]
  const effects = Vue.effectScope(), calls = [], storage = new Map(), location = { href: '' }
  const imports = {
    vue: { ...Vue, onMounted: () => {} },
    '../i18n': { t: value => value, formatDate: value => value, locale: Vue.ref('zh-CN') },
    '../api': { api: async (path, init) => { calls.push({ path, init }); return { projectId: 'target-project' } } },
  }
  const instance = effects.run(() => evaluate(source + '\nexport { context,impersonating,reason,canImpersonateMember,validImpersonationReason,prepareImpersonation,startImpersonation,closeImpersonation }', imports, {
    localStorage: { setItem: (key, value) => storage.set(key, String(value)) },
    location,
  }))
  return { ...instance, calls, storage, location, stop: () => effects.stop() }
}
const flush = async () => { await Vue.nextTick(); for (let i = 0; i < 6; i++) await Promise.resolve() }
let count = 0
async function test(name, fn) { await fn(); count++; console.log('✓ ' + name) }

await test('single and multiple departments use stable IDs; primary department follows removals without changing the source member', async () => {
  const m = setup('Members'), original = member()
  m.open(original); m.form.departmentIds.push('backend'); await flush()
  assert.deepEqual(original.departmentIds, ['frontend']); assert.equal(m.form.primaryDepartmentId, 'frontend')
  m.form.departmentIds = ['backend']; await flush(); assert.equal(m.form.primaryDepartmentId, 'backend')
  m.form.departmentIds = []; await flush(); assert.equal(m.form.primaryDepartmentId, '')
  assert.equal(m.dirty.value, true); m.stop()
})
await test('same-name member filtering is department-ID based and legacy project roles are offered only where already assigned', () => {
  const m = setup('Members'), first = member(), second = member({ id: 'second', email: 'second@example.test', departmentIds: ['backend'] })
  m.items.value = [first, second]; m.query.value = '同名'; m.department.value = 'backend'
  assert.deepEqual(m.filtered.value.map(item => item.id), ['second'])
  m.open(member({ tenantRole: 'tenant_admin', projectMemberships: [{ projectId: 'p', role: 'tenant_admin' }] }))
  assert.equal(m.roleName('tenant_admin'), '企业管理员')
  assert(m.projectRoleOptions({ projectId: 'p', role: 'tenant_admin' }).some(role => role.key === 'tenant_admin'))
  assert(!m.projectRoleOptions({ projectId: 'q', role: 'viewer' }).some(role => role.key === 'tenant_admin'))
  m.open(); assert(!m.projectRoleOptions({ projectId: 'p', role: 'viewer' }).some(role => role.key === 'tenant_admin')); m.stop()
})
await test('member save prevents duplicate requests, retains rejected drafts and omits an unchanged password', async () => {
  const pending = deferred(), m = setup('Members', { handler: async (path, init) => init ? pending.promise : { items: [] } })
  m.open(member()); m.form.name = 'Changed'; const saving = m.save(); await m.save()
  assert.equal(m.calls.length, 1); assert.equal(m.calls[0].path, '/organization/members/u'); assert.equal(m.calls[0].init.method, 'PATCH')
  assert(!('initialPassword' in JSON.parse(m.calls[0].init.body))); m.close(); assert.equal(m.opened.value, true); assert.equal(m.canLeave(), false)
  pending.reject(Error('Duplicate email')); await saving
  assert.equal(m.form.name, 'Changed'); assert.equal(m.opened.value, true); assert.equal(m.dirty.value, true); assert.equal(m.saving.value, false); assert.equal(m.emits.length, 0)
  m.locked.value = true; await m.save(); assert.equal(m.calls.length, 1); m.stop()
})
await test('CSV import requires a validated and explicitly confirmed preview; editing CSV invalidates it and failure preserves retry data', async () => {
  const m = setup('Members', { handler: async path => { if (path.endsWith('/commit')) throw Error('Duplicate import'); return { previewId: 'preview-1', canCommit: true, rows: [] } } })
  m.importOpen.value = true; m.csv.value = 'name,email\nOne,one@example.test'; await flush(); await m.previewImport(); await m.commitImport()
  assert.equal(m.calls.length, 1); assert.equal(m.importConfirmed.value, false)
  m.importConfirmed.value = true; await m.commitImport(); assert.equal(m.calls.length, 2); assert.equal(m.importOpen.value, true); assert.equal(m.preview.value.previewId, 'preview-1'); assert(m.csv.value)
  m.csv.value += '\nTwo,two@example.test'; await flush(); assert.equal(m.preview.value, null); assert.equal(m.importConfirmed.value, false); await m.commitImport(); assert.equal(m.calls.length, 2); m.stop()
})
await test('department parent choices exclude self and descendants and cyclic historical paths terminate safely', () => {
  const m = setup('Directory'), root = department('root'), child = department('child', 'root'), leaf = department('leaf', 'child'), sibling = department('sibling')
  m.departments.value = [root, child, leaf, sibling]; m.open(root)
  assert.deepEqual(m.eligibleParents.value.map(item => item.id), ['sibling'])
  const a = department('a', 'b'), b = department('b', 'a'); m.departments.value = [a, b]
  assert.equal(m.hasAncestor(a, 'absent'), false); assert.equal(helpers.departmentPath(a, [a, b]), 'b / a'); m.stop()
})
await test('group select-all uses the server permission catalog and never delegates non-delegable administrative powers', () => {
  const m = setup('Directory', { section: 'groups' })
  m.permissions.value = [{ key: 'members.manage', delegable: true }, { key: 'reports.view', delegable: true }, { key: 'groups.manage', delegable: false }]
  m.open(); m.selectAll(true); assert.deepEqual(m.form.permissions, ['members.manage', 'reports.view']); m.selectAll(false); assert.deepEqual(m.form.permissions, [])
  m.props.context.isTenantAdmin = false; m.props.context.permissions = ['groups.manage']; assert.equal(m.canManage.value, false); m.stop()
})
await test('failed department or group saves preserve the full draft and never claim success', async () => {
  for (const section of ['departments', 'groups']) {
    const m = setup('Directory', { section, handler: async () => { throw Error('Duplicate code') } })
    m.open(); Object.assign(m.form, { name: 'Preserve name', code: 'same', permissions: ['reports.view'], memberIds: ['u'] }); const snapshot = JSON.stringify(m.form)
    await m.save(); assert.equal(JSON.stringify(m.form), snapshot); assert.equal(m.opened.value, true); assert.equal(m.saving.value, false); assert.equal(m.emits.length, 0)
    const body = JSON.parse(m.calls[0].init.body)
    if (section === 'departments') { assert.equal(body.parentId, null); assert(!('permissions' in body)); assert(!('status' in body)) } else assert.deepEqual(body.permissions, ['reports.view'])
    m.stop()
  }
})
await test('invitation creation has no implicit department or role grants and rejects an unexpected foreign link without clearing the draft', async () => {
  const m = setup('Invitations', { handler: async () => ({ url: 'https://foreign.example/join/token' }) })
  m.createDialog(); assert.deepEqual(m.form.departmentIds, []); assert.deepEqual(m.form.allowedRoles, [])
  Object.assign(m.form, { name: 'Team invitation', departmentIds: ['frontend'], allowedRoles: ['developer'] }); await m.create()
  assert.equal(m.createdLink.value, ''); assert.equal(m.opened.value, true); assert.equal(m.form.name, 'Team invitation'); assert.match(m.error.value, /邀请链接/)
  m.props.context.isTenantAdmin = false; m.props.context.permissions = ['applications.review']; assert.equal(m.allowed.value, false); await m.create(); assert.equal(m.calls.length, 1); m.stop()
})
await test('approval never infers project grants from a requested role; explicit confirmation is required and rejection does not send credentials', async () => {
  const m = setup('Invitations', { section: 'applications', handler: async () => { throw Error('Duplicate account') } })
  m.startReview({ id: 'application-1', requestedRole: 'project_admin' }, 'approve')
  assert.deepEqual(m.approval.projectMemberships, []); assert.equal(m.reviewConfirmed.value, false)
  Object.assign(m.approval, { email: 'approved@example.test', initialPassword: 'temporary-password', projectMemberships: [{ projectId: 'p', role: 'developer' }] })
  await m.submitReview(); assert.equal(m.calls.length, 0)
  m.reviewConfirmed.value = true; await m.submitReview(); assert.equal(m.calls.length, 1)
  assert.deepEqual(JSON.parse(m.calls[0].init.body).projectMemberships, [{ projectId: 'p', role: 'developer' }])
  assert.equal(m.review.value.id, 'application-1'); assert.equal(m.approval.initialPassword, 'temporary-password'); assert.equal(m.saving.value, false)
  m.startReview({ id: 'application-2' }, 'reject'); m.approval.reason = 'Wrong team'; await m.submitReview(); assert.deepEqual(JSON.parse(m.calls[1].init.body), { reason: 'Wrong team' }); m.stop(); assert.equal(m.approval.initialPassword, '')
})
await test('all organization editors reject cancelled project switches before scope changes, preserve drafts and only reuse a successful leave confirmation once', () => {
  for (const kind of Object.keys(exposed)) {
    const m = setup(kind); if (kind === 'Invitations') m.createDialog(); else m.open(); m.form.name = 'Unsaved value'
    m.setConfirm(false); const event = new Event('change', { cancelable: true }); m.beforeProjectChange(event); assert(event.defaultPrevented, kind)
    assert.equal(m.form.name, 'Unsaved value'); assert.equal(m.opened.value, true)
    m.setConfirm(true); const accepted = new Event('change', { cancelable: true }); m.beforeProjectChange(accepted); assert.equal(accepted.defaultPrevented, false)
    const count = m.confirmations.length; assert.equal(m.canLeave(), true); assert.equal(m.confirmations.length, count)
    m.setProject('q'); const unload = new Event('beforeunload', { cancelable: true }); m.beforeUnload(unload); assert.equal(unload.defaultPrevented, false)
    m.cancelProjectLeave(); m.setConfirm(false); assert.equal(m.canLeave(), false); m.saving.value = true
    m.setConfirm(true); const busy = new Event('change', { cancelable: true }); m.beforeProjectChange(busy); assert(busy.defaultPrevented); assert.equal(m.form.name, 'Unsaved value'); m.stop()
  }
})
await test('robot configuration uses member IDs, prevents overlapping edit modals and delegates draft-aware closing', () => {
  const m = setup('Members'); m.session.value = { user: { id: 'me' } }; const target = member({ id: 'same-name-second' })
  m.open(); m.openWebhook(target); assert.equal(m.webhookMember.value, null); m.close(); m.openWebhook(target); assert.equal(m.webhookMember.value.id, 'same-name-second')
  m.open(); assert.equal(m.opened.value, false); m.webhookEditor.value = { requestClose: () => false, canLeave: () => false }; m.closeWebhook(); assert(m.webhookMember.value); assert.equal(m.canLeave(), false)
  // The child has its own project-change listener. The parent must not ask its
  // canLeave confirmation a second time during the same global dispatch.
  let robotConfirmations = 0; m.webhookEditor.value = { requestClose: () => false, canLeave: () => { robotConfirmations++; return false } }
  m.beforeProjectChange(new Event('change', { cancelable: true })); assert.equal(robotConfirmations, 0); m.cancelProjectLeave()
  m.webhookEditor.value = { requestClose: () => true, canLeave: () => true }; m.webhookBusy.value = true; m.closeWebhook(); assert(m.webhookMember.value)
  m.webhookBusy.value = false; m.closeWebhook(); assert.equal(m.webhookMember.value, null)
  m.props.context.isTenantAdmin = false; m.props.context.permissions = ['members.manage']; assert.equal(m.canConfigureWebhook(member({ tenantRole: 'tenant_admin' })), false); m.stop()
})
await test('member deletion is tenant-admin-only, excludes self, requires an explicit second confirmation, and refreshes after success', async () => {
  const m = setup('Members')
  m.session.value = { user: { id: 'me' } }
  const self = member({ id: 'me' }), target = member({ id: 'remove-me', name: '待移除成员' })
  assert.equal(m.canDeleteMember(self), false)
  assert.equal(m.canDeleteMember(target), true)
  m.prepareDelete(target); assert.equal(m.deleteTarget.value.id, 'remove-me')
  await m.removeMember(); assert.equal(m.calls.filter(call => call.init?.method === 'DELETE').length, 0)
  m.deleteConfirmed.value = true; await m.removeMember()
  const deletion = m.calls.find(call => call.init?.method === 'DELETE')
  assert.equal(deletion.path, '/organization/members/remove-me')
  assert.equal(m.deleteTarget.value, null); assert.match(m.notice.value, /待移除成员/); assert.equal(m.emits.length, 1)
  m.props.context.isTenantAdmin = false; assert.equal(m.canDeleteMember(target), false); m.stop()
})
await test('impersonation pre-fills a clear work-viewing audit reason without flagging an untouched modal as a draft', () => {
  const m = setup('Members'), target = member({ id: 'inspect-me', name: '待查看成员' })
  m.loading.value = false; m.session.value = { user: { id: 'me' }, canImpersonate: true }
  m.prepareImpersonation(target)
  assert.equal(m.impersonating.value.id, 'inspect-me')
  assert.equal(m.reason.value, '查看工作')
  assert.equal(m.dirty.value, false)
  m.reason.value = '协助排查权限问题'
  assert.equal(m.dirty.value, true)
  m.closeImpersonation()
  assert.equal(m.impersonating.value, null)
  assert.equal(m.reason.value, '')
  m.stop()
})
await test('a pending first-password member can only enter the clearly marked read-only review from an authorized administrator', async () => {
  const m = setup('Members', { handler: async path => path === '/auth/impersonation' ? { projectId: 'target-project', readOnly: true } : { items: [] } })
  const target = member({ id: 'pending-review', mustChangePassword: true })
  m.loading.value = false; m.session.value = { user: { id: 'me' }, canImpersonate: true }
  assert.equal(m.canImpersonateMember(target), true)
  m.prepareImpersonation(target)
  assert.equal(m.impersonating.value.id, 'pending-review')
  assert.equal(m.validImpersonationReason(m.reason.value), true)
  await m.startImpersonation()
  const request = m.calls.find(call => call.path === '/auth/impersonation')
  assert.deepEqual(JSON.parse(request.init.body), { userId: 'pending-review', reason: '查看工作' })
  m.session.value = { user: { id: 'me' }, canImpersonate: false }
  m.impersonating.value = null
  m.prepareImpersonation(target)
  assert.equal(m.impersonating.value, null)
  assert.equal(m.validImpersonationReason('短'), false)
  m.stop()
})
await test('legacy Members entry applies the same pending-password review guard, wording, and reason validation', async () => {
  const m = setupLegacyMembers(), target = { id: 'legacy-pending', active: true, mustChangePassword: true }
  m.context.value = { user: { id: 'admin' }, canImpersonate: true }
  assert.equal(m.canImpersonateMember(target), true)
  m.prepareImpersonation(target)
  assert.equal(m.impersonating.value.id, 'legacy-pending')
  assert.equal(m.reason.value, '查看工作')
  m.reason.value = '短'; await m.startImpersonation(); assert.equal(m.calls.length, 0)
  m.reason.value = '核查待改密成员可见范围'; await m.startImpersonation()
  assert.deepEqual(JSON.parse(m.calls[0].init.body), { userId: 'legacy-pending', reason: '核查待改密成员可见范围' })
  assert.equal(m.storage.get('devflow-project'), 'target-project'); assert.equal(m.location.href, '/my-work')
  m.closeImpersonation(); assert.equal(m.impersonating.value, null); assert.equal(m.reason.value, '')
  m.context.value = { user: { id: 'admin' }, canImpersonate: false }; m.prepareImpersonation(target); assert.equal(m.impersonating.value, null)
  assert.match(read('src/views/Members.vue'), /受限代看（待首次改密账号）/)
  m.stop()
})
await test('organization templates compile and route/event listeners are registered and removed as pairs', async () => {
  for (const kind of Object.keys(exposed)) {
    const filename = 'src/components/Organization' + kind + '.vue', { descriptor, errors } = parse(read(filename), { filename })
    assert.deepEqual(errors, []); const compiled = compileTemplate({ source: descriptor.template.content, filename, id: kind, compilerOptions: { expressionPlugins: ['typescript'] } }); assert.deepEqual(compiled.errors, [], kind)
    if (kind === 'Members') assert.match(descriptor.template.content, /id="org-member-form"[^>]*:inert="saving"/)
    if (kind === 'Members') {
      assert.match(descriptor.template.content, /受限代看（待首次改密账号）/)
      assert.match(descriptor.template.content, /不能修改业务、通知已读状态、显示偏好、密码或权限/)
    }
    const m = setup(kind); m.mount(); await flush(); assert.equal(m.routes.length, 2); assert(m.events.has('devflow-before-project-change')); assert(m.events.has('devflow-project-change-cancelled')); m.stop(); assert.equal(m.events.size, 0)
  }
})
console.log(`Passed ${count} organization settings regressions.`)

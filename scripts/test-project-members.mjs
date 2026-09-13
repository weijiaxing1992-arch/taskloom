import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import ts from 'typescript'
import * as Vue from 'vue'
import { parse, compileScript, compileTemplate, compileStyle } from 'vue/compiler-sfc'

const read = path => readFileSync(new URL('../' + path, import.meta.url), 'utf8')
const source = read('src/components/ProjectMembers.vue'), { descriptor } = parse(source)
const person = (id, role = null, extra = {}) => ({ id, name: id, email: `${id}@example.test`, role, active: true, tenantRole: 'member', ...extra })
const members = [person('self', 'frontend'), person('admin', 'tenant_admin', { tenantRole: 'tenant_admin' }), person('old', 'qa'), person('inactive', null, { active: false }), person('new')]
const response = (items = members, extra = {}) => ({ items: structuredClone(items), canManage: true, currentUserId: 'self', isTenantAdmin: true, ...extra })
const deferred = () => { let resolve, reject; const promise = new Promise((yes, no) => { resolve = yes; reject = no }); return { promise, resolve, reject } }
const flush = async () => { for (let i = 0; i < 5; i++) await Vue.nextTick() }
function fixture(handler = async () => response()) {
  const scope = Vue.effectScope(), calls = [], emitted = [], mounts = [], unmounts = [], listeners = new Map(), guards = []
  const props = Vue.reactive({ project: { id: 'p_one', name: 'One', code: 'ONE' } }), exports = {}
  const imports = {
    vue: { ...Vue, onMounted: fn => mounts.push(fn), onBeforeUnmount: fn => unmounts.push(fn) },
    'vue-router': { onBeforeRouteLeave: fn => guards.push(fn), onBeforeRouteUpdate: fn => guards.push(fn) },
    '../api': { api: async (path, options) => { calls.push({ path, options }); return handler(path, options) } },
    '../i18n': { t: (text, params = {}) => text.replace(/\{(\w+)\}/g, (_, key) => String(params[key] ?? key)) },
  }
  const names = 'items,selected,baseline,query,loading,saving,loaded,canManage,locked,error,notice,discardOpen,discardPrompt,filtered,added,removed,dirty,mutable,allFilteredSelected,someFilteredSelected,load,toggleMember,toggleFiltered,save,requestClose,keepEditing,discardChanges,canLeave,protectedMember,roleDrafts,roleUpdates'
  const code = ts.transpileModule(descriptor.scriptSetup.content + '\nexport {' + names + '}', { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText
  const window = { addEventListener: (name, fn) => listeners.set(name, fn), removeEventListener: name => listeners.delete(name) }
  scope.run(() => new Function('require', 'exports', 'defineProps', 'defineEmits', 'defineExpose', 'window', code)(id => imports[id] || {}, exports, () => props, () => (...args) => emitted.push(args), () => {}, window))
  mounts.forEach(fn => fn())
  return { ...exports, props, calls, emitted, listeners, guards, stop() { unmounts.forEach(fn => fn()); scope.stop() } }
}
function projectFixture(handler) {
  const scope = Vue.effectScope(), calls = [], emitted = [], unmounts = [], exports = {}, location = { href: '' }
  const imports = {
    vue: { ...Vue, onMounted: () => {}, onBeforeUnmount: fn => unmounts.push(fn) },
    'vue-router': { useRoute: () => Vue.reactive({ query: {} }), useRouter: () => ({ replace: async () => {} }) },
    '../api': { api: async (path, options) => { calls.push({ path, options }); return handler(path, options) } },
    '../i18n': { t: text => text, formatDate: text => text }, '../layoutScope': { layoutScope: Vue.ref('tenant:self') },
  }
  const source = parse(read('src/views/Projects.vue')).descriptor.scriptSetup.content
  const code = ts.transpileModule(source + '\nexport { canCreate, canManageMembers, managingMembers, createdProject, openMembers, membersChanged, load, create, form, saving, locked, enter }', { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText
  scope.run(() => new Function('require', 'exports', 'window', 'localStorage', 'location', 'CustomEvent', code)(id => imports[id] || {}, exports, { addEventListener() {}, removeEventListener() {}, dispatchEvent: event => emitted.push(event.type) }, { getItem: () => null, setItem() {} }, location, class { constructor(type) { this.type = type } }))
  return { ...exports, calls, emitted, location, stop() { unmounts.forEach(fn => fn()); scope.stop() } }
}
let count = 0
async function test(name, run) { await run(); count++; console.log('✓ ' + name) }

await test('candidate loading uses explicit target and opt-in route; inactive candidates remain visible and baseline includes only current members', async () => {
  const m = fixture(); await flush()
  assert.equal(m.calls[0].path, '/projects/p_one/members?candidates=1'); assert.equal(m.calls[0].options.headers['X-TaskLoom-Project'], 'p_one')
  assert.deepEqual(m.baseline.value, ['self', 'admin', 'old']); assert.deepEqual(m.selected.value, m.baseline.value)
  assert.equal(m.items.value.find(x => x.id === 'inactive').active, false); assert.equal(m.dirty.value, false); await m.save(); assert.equal(m.calls.length, 1); m.stop()
})
await test('filtered select-all preserves hidden selections and never emits an accidental hidden removal', async () => {
  const m = fixture(); await flush(); m.query.value = 'inactive'; m.toggleFiltered(true)
  assert.deepEqual(m.added.value, ['inactive']); assert.deepEqual(m.removed.value, []); assert(m.selected.value.includes('old'))
  m.query.value = 'new'; m.toggleFiltered(true); m.toggleFiltered(false)
  assert.deepEqual(m.added.value, ['inactive']); assert.deepEqual(m.removed.value, []); m.query.value = ''; assert.equal(m.someFilteredSelected.value, true); m.stop()
})
await test('bulk deselection and direct toggles cannot remove self or organization admins, and delegated admins cannot remove project admins', async () => {
  const m = fixture(async () => response([...members, person('project-admin', 'project_admin')], { isTenantAdmin: false })); await flush()
  m.toggleFiltered(false); assert.deepEqual(m.selected.value, ['self', 'admin', 'project-admin']); assert.deepEqual(m.removed.value, ['old'])
  for (const id of ['self', 'admin', 'project-admin']) m.toggleMember(id, false)
  assert.deepEqual(m.selected.value, ['self', 'admin', 'project-admin']); m.stop()
})
await test('save writes only incremental IDs and viewer default, then uses canonical response without modifying inactive accounts or existing roles', async () => {
  const updated = members.map(x => x.id === 'inactive' ? { ...x, role: 'viewer' } : x.id === 'old' ? { ...x, role: null } : x)
  const m = fixture(async (path, options) => response(options.method === 'PATCH' ? updated : members)); await flush()
  m.toggleMember('inactive', true); m.toggleMember('old', false); await m.save()
  const write = m.calls[1]; assert.equal(write.path, '/projects/p_one/members'); assert.equal(write.options.headers['X-TaskLoom-Project'], 'p_one')
  assert.deepEqual(JSON.parse(write.options.body), { addUserIds: ['inactive'], removeUserIds: ['old'], role: 'viewer' })
  assert.equal(m.items.value.find(x => x.id === 'self').role, 'frontend'); assert.equal(m.items.value.find(x => x.id === 'inactive').active, false)
  assert.equal(m.dirty.value, false); assert(m.notice.value); assert.deepEqual(m.emitted, [['changed', 'p_one']]); m.stop()
})
await test('search and untrusted selection IDs do not become writes; save deduplicates and only uses server candidates', async () => {
  const m = fixture(); await flush(); m.selected.value.push('not-in-tenant', 'old', 'inactive', 'inactive'); m.query.value = 'no matches'; await m.save()
  assert.deepEqual(JSON.parse(m.calls[1].options.body), { addUserIds: ['inactive'], removeUserIds: [], role: 'viewer' }); m.stop()
})
await test('late responses from another project cannot replace candidates, selections, permissions or errors', async () => {
  for (const reject of [false, true]) {
    const old = deferred(), m = fixture(path => path.includes('p_one') ? old.promise : Promise.resolve(response([person('two', 'backend')])) )
    const originalSignal = m.calls[0].options.signal; m.props.project = { id: 'p_two', name: 'Two', code: 'TWO' }; await flush()
    assert(originalSignal.aborted); assert.deepEqual(m.selected.value, ['two'])
    if (reject) old.reject(Error('stale denial')); else old.resolve(response(members, { canManage: false }))
    await flush(); assert.deepEqual(m.items.value.map(x => x.id), ['two']); assert.equal(m.error.value, ''); assert(m.canManage.value); m.stop()
  }
})
await test('project switch during an old write cannot emit success or overwrite the new project baseline', async () => {
  const old = deferred(), m = fixture((path, options) => options.method === 'PATCH' ? old.promise : Promise.resolve(response(path.includes('p_two') ? [person('two', 'qa')] : members))); await flush()
  m.toggleMember('inactive', true); const saving = m.save(); const write = m.calls.at(-1)
  m.props.project = { id: 'p_two', name: 'Two', code: 'TWO' }; await flush(); assert(write.options.signal.aborted)
  old.resolve(response()); await saving; assert.deepEqual(m.selected.value, ['two']); assert.equal(m.notice.value, ''); assert.deepEqual(m.emitted, []); assert.equal(m.saving.value, false); m.stop()
})
await test('pending writes block double submission, selection edits, close and navigation', async () => {
  const pending = deferred(), m = fixture((path, options) => options.method === 'PATCH' ? pending.promise : Promise.resolve(response())); await flush()
  m.toggleMember('inactive', true); const saving = m.save(); await m.save(); m.toggleMember('new', true); m.toggleFiltered(false)
  assert.equal(m.requestClose(), false); assert.equal(m.canLeave(), false); assert.equal(m.calls.length, 2); assert(m.selected.value.includes('inactive')); assert(!m.selected.value.includes('new')); assert.deepEqual(m.emitted, [])
  pending.reject(Error('Write denied')); await saving; assert.equal(m.error.value, 'Write denied'); assert.equal(m.dirty.value, true); assert.equal(m.saving.value, false); assert.equal(m.notice.value, ''); m.stop()
})
await test('failed or malformed saves preserve draft and baseline without announcing success', async () => {
  for (const outcome of [() => Promise.reject(Error('Network failed')), () => Promise.resolve({ items: [] })]) {
    const m = fixture((path, options) => options.method === 'PATCH' ? outcome() : Promise.resolve(response())); await flush()
    m.toggleMember('inactive', true); await m.save(); assert.deepEqual(m.baseline.value, ['self', 'admin', 'old']); assert.deepEqual(m.added.value, ['inactive']); assert(m.error.value); assert.equal(m.notice.value, ''); assert.deepEqual(m.emitted, []); m.stop()
  }
})
await test('no-permission and malformed candidate responses never enable writes', async () => {
  for (const result of [response([], { canManage: false }), response([], { currentUserId: '' }), response([person('duplicate'), person('duplicate')]), response([person('bad', undefined, { active: 'true' })])]) {
    const m = fixture(async () => result); await flush(); m.toggleFiltered(true); m.toggleMember('bad', true); await m.save()
    assert.equal(m.loaded.value, false); assert.equal(m.mutable.value, false); assert.equal(m.calls.length, 1); assert(m.error.value); assert.deepEqual(m.items.value, []); m.stop()
  }
})
await test('clean close is immediate but dirty close exposes an inline discard choice and retains edits when cancelled', async () => {
  const m = fixture(); await flush(); assert.equal(m.requestClose(), true); assert.deepEqual(m.emitted, [['close']]); m.emitted.length = 0
  m.toggleMember('inactive', true); let focused = 0; m.discardPrompt.value = { focus: () => focused++ }; assert.equal(m.requestClose(), false); await flush()
  assert(m.discardOpen.value); assert.equal(focused, 1); await m.save(); assert.equal(m.calls.length, 1)
  m.keepEditing(); assert.equal(m.discardOpen.value, false); assert.deepEqual(m.added.value, ['inactive']); m.requestClose(); m.discardChanges()
  assert.equal(m.dirty.value, false); assert.deepEqual(m.emitted, [['close']]); m.stop()
})
await test('route-leave promises resume only after explicit discard; keep editing cancels navigation', async () => {
  const m = fixture(); await flush(); m.toggleMember('inactive', true)
  const stay = m.guards[0](); m.keepEditing(); assert.equal(await stay, false); assert(m.dirty.value)
  const leave = m.guards[1](); m.discardChanges(); assert.equal(await leave, true); assert.equal(m.dirty.value, false); m.stop()
})
await test('project selector and browser close protect unsaved changes without a native confirm dialog', async () => {
  const m = fixture(); await flush(); m.toggleMember('inactive', true); let prevented = 0
  const event = { preventDefault: () => prevented++, returnValue: null }; m.listeners.get('devflow-before-project-change')(event); assert.equal(prevented, 1); assert(m.discardOpen.value)
  m.listeners.get('beforeunload')(event); assert.equal(prevented, 2); assert.equal(event.returnValue, ''); m.stop(); assert.equal(m.listeners.size, 0)
})
await test('identity invalidation cancels requests, clears private draft and ignores late success', async () => {
  const pending = deferred(), m = fixture((path, options) => options.method === 'PATCH' ? pending.promise : Promise.resolve(response())); await flush()
  m.toggleMember('inactive', true); const saving = m.save(), signal = m.calls.at(-1).options.signal
  m.listeners.get('devflow-identity-changed')(); assert(signal.aborted); assert(m.locked.value); assert.deepEqual(m.items.value, []); assert.deepEqual(m.selected.value, [])
  pending.resolve(response()); await saving; assert.deepEqual(m.emitted, []); assert.equal(m.notice.value, ''); assert.equal(m.mutable.value, false); m.stop()
})
await test('unmount abandons pending reads and cannot rehydrate a closed editor', async () => {
  const pending = deferred(), m = fixture(() => pending.promise); const signal = m.calls[0].options.signal; m.stop(); pending.resolve(response()); await flush()
  assert(signal.aborted); assert.deepEqual(m.items.value, []); assert.deepEqual(m.emitted, [])
})
await test('200 member combined change cap fails locally with all edits intact', async () => {
  const m = fixture(async () => response(Array.from({ length: 201 }, (_, i) => person('candidate-' + i)))); await flush(); m.toggleFiltered(true); await m.save()
  assert.equal(m.calls.length, 1); assert.equal(m.added.value.length, 201); assert.match(m.error.value, /200/); assert(m.dirty.value); m.stop()
})
await test('programmatically removing protected rows is rejected even if checkbox guards are bypassed', async () => {
  const m = fixture(); await flush(); m.selected.value = []; await m.save(); assert.equal(m.calls.length, 1); assert(m.error.value); m.stop()
})
await test('project card membership entry uses enterprise capability, not project management role, and keeps a stable target copy', async () => {
  const project = { id: 'explicit-project', name: 'Original', code: 'ONE', status: 'active', canManage: true }
  const m = projectFixture(async () => ({ items: [project], canManageMembers: false })); await m.load(); m.openMembers(project); assert.equal(m.managingMembers.value, null)
  m.canManageMembers.value = true; m.openMembers({ ...project, status: 'archived' }); assert.equal(m.managingMembers.value, null)
  m.openMembers({ ...project, canManage: false }); assert.equal(m.managingMembers.value.id, 'explicit-project')
  project.name = 'Changed'; assert.equal(m.managingMembers.value.name, 'Original'); m.openMembers({ ...project, id: 'other' }); assert.equal(m.managingMembers.value.id, 'explicit-project')
  await m.enter(project); assert.equal(m.calls.length, 1); assert.equal(m.location.href, ''); m.stop()
})
await test('project creation retains the original creation body and offers explicit membership or entry choices without auto-navigation', async () => {
  const project = { id: 'created', name: 'New project', code: 'NEW' }
  const m = projectFixture(async (path, options) => options.method === 'POST' ? project : { items: [{ ...project, status: 'active' }], canCreate: true, canManageMembers: true })
  m.canCreate.value = true; m.form.name = project.name; m.form.code = project.code; const before = JSON.parse(JSON.stringify(m.form)); await m.create()
  assert.deepEqual(JSON.parse(m.calls[0].options.body), before); assert.equal(m.calls[0].path, '/projects'); assert.equal(m.location.href, '')
  assert.equal(m.calls.some(call => call.path.includes('/visit')), false); assert.equal(m.createdProject.value.id, 'created'); m.openMembers(m.createdProject.value); assert.equal(m.managingMembers.value.id, 'created'); m.stop()
})
await test('only the currently open member editor can refresh project counts and shell context', async () => {
  const m = projectFixture(async () => ({ items: [], canManageMembers: true })); await m.load(); m.openMembers({ id: 'p_one', name: 'One', code: 'ONE', status: 'active' })
  m.membersChanged('p_other'); assert.equal(m.calls.length, 1); assert.deepEqual(m.emitted, [])
  m.membersChanged('p_one'); await flush(); assert.equal(m.calls.length, 2); assert.deepEqual(m.emitted, ['devflow-project-list-changed']); m.stop()
})
await test('templates compile with keyboard controls, theme variables, inactive explanation and explicit create follow-up', () => {
  for (const filename of ['src/components/ProjectMembers.vue', 'src/views/Projects.vue']) {
    const source = read(filename), { descriptor } = parse(source), script = compileScript(descriptor, { id: 'project-members' })
    assert.deepEqual(compileTemplate({ id: 'project-members', filename, source: descriptor.template.content, compilerOptions: { bindingMetadata: script.bindings } }).errors, [])
    assert.deepEqual(compileStyle({ id: 'project-members', filename, source: descriptor.styles.map(style => style.content).join('\n'), scoped: true }).errors, [])
  }
  assert.match(source, /:indeterminate="someFilteredSelected"/); assert.match(source, /role="alert"/); assert.match(source, /@media\(max-width:640px\)/); assert.doesNotMatch(source, /window\.confirm/)
  const projects = read('src/views/Projects.vue'); assert.match(projects, /data\.canManageMembers === true/); assert.match(projects, /@click\.stop="openMembers\(x\)"/); assert.match(projects, /<ProjectMembers v-if="managingMembers"/); assert.match(projects, /JSON\.stringify\(form\)/)
  const create = projects.slice(projects.indexOf('async function create()'), projects.indexOf('async function patch(')); assert.doesNotMatch(create, /await enter\(/); assert.match(create, /createdProject\.value/)
})

await test('multiple project roles remain isolated drafts and save only changed users alongside the member delta', async () => {
  const original=person('old','product',{projectRoles:['product','qa']})
  const m=fixture(async (path,options)=>response(options.method==='PATCH'?[...members.filter(item=>item.id!=='old'),{...original,projectRoles:['product','qa','backend']}]:[...members.filter(item=>item.id!=='old'),original]));await flush()
  assert.deepEqual(m.roleDrafts.value.old,['product','qa']);m.roleDrafts.value.old.push('backend')
  assert(m.dirty.value);assert.deepEqual(m.items.value.find(item=>item.id==='old').projectRoles,['product','qa'])
  m.query.value='no visible members';await m.save()
  assert.deepEqual(JSON.parse(m.calls[1].options.body),{addUserIds:[],removeUserIds:[],role:'viewer',roleUpdates:{old:['product','qa','backend']}})
  assert.equal(m.dirty.value,false);m.stop()
})
await test('empty role sets do not submit and discard restores role arrays as well as membership selection',async()=>{
  const m=fixture();await flush();m.roleDrafts.value.old=[];await m.save();assert.equal(m.calls.length,1);assert(m.error.value)
  m.roleDrafts.value.old=['qa','product'];assert.equal(m.requestClose(),false);m.discardChanges();assert.deepEqual(m.roleDrafts.value.old,['qa']);assert.equal(m.dirty.value,false);m.stop()
})

console.log(`Passed ${count} isolated project membership UI tests.`)

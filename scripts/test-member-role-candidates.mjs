import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import ts from 'typescript'
import * as Vue from 'vue'
import { compileScript, parse } from 'vue/compiler-sfc'
const read = path => readFileSync(new URL('../' + path, import.meta.url), 'utf8')
function evaluate(source, imports = {}, globals = {}) { const exports = {}; new Function('require', 'exports', ...Object.keys(globals), ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText)(id => imports[id] || {}, exports, ...Object.values(globals)); return exports }
const helpers = evaluate(read('src/mentions.ts')), fields = evaluate(read('src/requirementFields.ts')),recent=evaluate(read('src/recentMembers.ts'))
const people = [
 { id: 'product', name: '同名', role: 'product', active: true, departmentIds: ['delivery'], departmentNames: ['交付'] },
 { id: 'front', name: '同名', role: 'frontend', active: true, departmentIds: ['delivery'], departmentNames: ['交付'], isCurrent: true },
 { id: 'lead', name: '组长', role: 'tenant_admin', projectRole: 'frontend_lead', active: true, departmentIds: ['delivery', 'leadership'], departmentNames: ['交付', '组长'] },
 { id: 'other-department', name: '其他部门前端', role: 'frontend', active: true, departmentIds: ['other'], departmentNames: ['其他'] },
 { id: 'back', name: '后端', role: 'backend', active: true, departmentIds: ['delivery'], departmentNames: ['交付'] },
 { id: 'back-lead', name: '后端组长', role: 'backend_lead', active: true },
 { id: 'algorithm', name: '算法', role: 'algorithm', active: true },
 { id: 'ui', name: 'UI', role: 'ui', active: true },
 { id: 'qa', name: '测试', role: 'qa', active: true, departmentIds: ['delivery'], departmentNames: ['交付'] },
 { id: 'inactive', name: '停用前端', role: 'frontend', active: false, departmentIds: ['delivery'] },
 { id: 'admin', name: '管理员', role: 'project_admin', active: true },
]
const locale = Vue.ref('zh-CN'), i18n = { t: value => locale.value === 'en-US' ? ({'前端工程师':'Frontend engineer','前端组长':'Frontend lead'}[value] || value) : value, categoryLabel: value => value, formatDate: value => value }
const renderer = Vue.createRenderer({ createElement: () => ({}), createText: () => ({}), createComment: () => ({}), insert() {}, remove() {}, setText() {}, setElementText() {}, parentNode: () => null, nextSibling: () => null, patchProp() {} })
const flush = async () => { for (let i = 0; i < 8; i++) { await Promise.resolve(); await Vue.nextTick() } }
function mount(name, initialProps, handler = async () => ({ items: [] })) {
 const props = Vue.reactive(initialProps), emits = [], calls = [], window = new EventTarget()
 const source = compileScript(parse(read('src/components/' + name + '.vue')).descriptor, { id: name }).content
 const imports = { vue: Vue,'../recentMembers':recent,'../layoutScope':{layoutScope:Vue.ref('t:u')},'../stores/workspace':{useWorkspaceStore:()=>Vue.reactive({session:{tenant:{id:'t'},user:{id:'u'},project:{id:'p'}}})}, '../mentions': helpers, '../requirementFields': fields, '../i18n': i18n, '../api': { api: async (path, options) => { calls.push({ path, options }); return handler(path, options) } } }
 const Component = evaluate(source, new Proxy(imports, { get: (target, id) => id.endsWith('.vue') ? { default: {} } : target[id] }), { window, localStorage: { getItem: () => 'p' } }).default
 Component.render = () => null
 const root = Vue.h(Vue.defineComponent({ setup: () => () => Vue.h(Component, { ...props, 'onUpdate:modelValue': value => { emits.push(value); props.modelValue = value } }) })), container = {}
 renderer.render(root, container)
 return { props, emits, calls, context: root.component.subTree.component.setupState, stop: () => renderer.render(null, container) }
}
function editorFixture(current, existing) {
 const scope = Vue.effectScope(), unmount = [], calls = [], source = read('src/views/Editor.vue').match(/<script setup[^>]*>([\s\S]*?)<\/script>/)[1]
 const imports = { vue: { ...Vue, onMounted() {}, onBeforeUnmount: fn => unmount.push(fn) }, 'vue-router': { useRoute: () => ({ params: existing ? { id: '7' } : {}, query: {} }), useRouter: () => ({ push: async () => {} }), onBeforeRouteLeave() {}, onBeforeRouteUpdate() {} }, '../mentions': helpers, '../requirementFields': fields, '../i18n': i18n, '../api': { api: async (path, options) => { calls.push({ path, options }); if (path === '/session') return { user: { id: current, role: 'product' }, project: { id: 'p' } }; if (path === '/members') return { items: people }; if (path === '/requirement-workflow') return { initialStatus: '规划中' }; if (path === '/requirements/7') return existing; return { items: [] } } } }
 imports['../layoutScope']={useLayoutBoolean:(_name,fallback)=>Vue.ref(fallback)}
 const m = scope.run(() => evaluate(source + '\nexport {load,f,dirty}', imports, { defineProps: () => ({}), defineEmits: () => () => {}, defineExpose() {}, window: { confirm: () => false, addEventListener() {}, removeEventListener() {} } }))
 return { ...m, calls, stop() { unmount.forEach(fn => fn()); scope.stop() } }
}
let count = 0; async function test(name, run) { await run(); count++; console.log('✓ ' + name) }
await test('manager candidates include actual tenant admins without granting QA or engineer eligibility', async () => {
 const directory = [
  { id: 'tenant-manager', name: '企业管理员', tenantRole: 'tenant_admin', projectRole: 'viewer', projectRoles: ['viewer'], active: true, departmentIds: ['delivery'] },
  { id: 'project-manager', name: '项目管理员', tenantRole: 'member', projectRoles: ['project_admin'], active: true, departmentIds: ['delivery'] },
  { id: 'ordinary', name: '普通成员', tenantRole: 'member', projectRoles: ['viewer'], active: true, departmentIds: ['delivery'] },
  { id: 'disabled-manager', name: '停用管理员', tenantRole: 'tenant_admin', projectRoles: ['viewer'], active: false, departmentIds: ['delivery'] },
  { id: 'other-manager', name: '其他部门管理员', tenantRole: 'tenant_admin', projectRoles: ['viewer'], active: true, departmentIds: ['other'] },
 ]
 const allowed = helpers.fieldMemberRoles({key:'managers'})
 assert.deepEqual(helpers.memberCandidates(directory,allowed,'delivery').map(item=>item.id),['tenant-manager','project-manager'])
 for(const roles of [['qa'],['product'],['frontend'],['project_admin']]) assert.equal(helpers.memberCandidates(directory,roles).some(item=>item.id==='tenant-manager'),false)
 const m=mount('MemberMultiSelect',{modelValue:[],members:directory,memberRoles:allowed,departmentId:'delivery',currentUserId:'tenant-manager'})
 assert.deepEqual(m.context.options.map(item=>item.id),['tenant-manager','project-manager']);m.context.selectMe();await flush();assert.deepEqual(m.props.modelValue,['tenant-manager'])
 m.props.memberRoles=['qa'];await flush();assert.deepEqual(m.context.options,[]);assert.deepEqual(m.props.modelValue,['tenant-manager']);assert.equal(m.context.selected[0].historical,true);m.stop()
})
await test('each discipline has exact role candidates; leads are eligible engineers, not the reverse, and project roles take precedence', () => {
 const expected = { frontend: ['front', 'lead', 'other-department'], backend: ['back', 'back-lead'], algorithm: ['algorithm'], ui: ['ui'], product: ['product'] }
 for (const row of fields.roleWeightDefinitions) assert.deepEqual(helpers.memberCandidates(people, row.roles).map(item => item.id), expected[row.key])
 assert.deepEqual(helpers.memberCandidates(people, ['frontend_lead']).map(item => item.id), ['lead'])
 assert.deepEqual(helpers.memberCandidates(people, ['backend_lead']).map(item => item.id), ['back-lead'])
 assert.deepEqual(helpers.memberCandidates(people, ['qa']).map(item => item.id), ['qa'])
 assert.deepEqual(helpers.memberCandidates(people, ['unknown-role']), [])
})
await test('server metadata is authoritative, including explicit unrestricted roles; fallbacks use stable keys rather than user field labels', () => {
 assert.deepEqual(helpers.fieldMemberRoles({ key: 'testers' }), ['qa']); assert.deepEqual(helpers.fieldMemberRoles({ key: 'frontend_leads' }), ['frontend_lead'])
 assert.deepEqual(helpers.fieldMemberRoles({ key: 'testers', memberRoles: [] }), [])
 assert.deepEqual(helpers.fieldMemberRoles({ key: 'testers', memberRoles: ['product', 'product'] }), ['product'])
 assert.deepEqual(helpers.fieldMemberRoles({ key: 'custom_123', name: '前端工程师' }), [])
 assert.deepEqual(helpers.fieldMemberRoles({ key: 'owner' }, 'defect'), [])
})
await test('role, configured department and interactive department constraints intersect for selection, bulk-add and select-me', async () => {
 const m = mount('MemberMultiSelect', { modelValue: [], members: people, memberRoles: ['frontend', 'frontend_lead'], departmentId: 'delivery', currentUserId: 'product' }), c = m.context
 assert.deepEqual(c.options.map(item => item.id), ['front', 'lead']); assert.equal(c.currentMember, undefined)
 c.selectMe(); c.toggle('product'); c.toggle('other-department'); assert.equal(m.emits.length, 0)
 c.department = 'leadership'; await flush(); assert.deepEqual(c.options.map(item => item.id), ['lead']); c.addDepartment(); await flush(); assert.deepEqual(m.props.modelValue, ['lead'])
 m.props.currentUserId = 'front'; await flush(); assert.equal(c.currentMember, undefined); c.selectMe(); assert.equal(m.emits.length, 1)
 c.department = ''; await flush(); c.selectMe(); await flush(); assert.deepEqual(m.props.modelValue, ['lead', 'front']); m.stop()
})
await test('historical out-of-role, out-of-department, disabled and missing identities remain visible without silently changing the model', async () => {
 const original = structuredClone(people), m = mount('MemberMultiSelect', { modelValue: ['product', 'inactive', 'gone'], members: people, memberRoles: ['frontend'], departmentId: 'delivery', snapshots: [{ id: 'gone', name: 'Historical name' }] }), c = m.context
 assert.deepEqual(c.selected.map(item => [item.id, item.historical]), [['product', true], ['inactive', true], ['gone', true]])
 assert.equal(c.selected[2].name, 'Historical name'); assert.equal(m.emits.length, 0)
 m.props.memberRoles = ['qa']; await flush(); assert.deepEqual(m.props.modelValue, ['product', 'inactive', 'gone']); assert.equal(m.emits.length, 0)
 c.remove('product'); await flush(); c.toggle('product'); assert.deepEqual(m.props.modelValue, ['inactive', 'gone']); assert.deepEqual(people, original); m.stop()
})
await test('read-only and disabled member pickers cannot mutate candidates, selected IDs or department groups', async () => {
 const m = mount('MemberMultiSelect', { modelValue: ['product'], members: people, memberRoles: ['product'], currentUserId: 'product', disabled: true }), c = m.context
 c.department = 'delivery'; c.toggle('product'); c.remove('product'); c.selectMe(); c.addDepartment(); await flush(); assert.equal(m.emits.length, 0); assert.deepEqual(m.props.modelValue, ['product']); m.stop()
})
await test('single and multiple custom people fields use role metadata with department intersections while keeping existing values', async () => {
 const defs = [{ id: 1, key: 'testers', name: '质量负责人', type: 'users', departmentId: 'delivery', memberRoles: ['qa'], enabled: true }, { id: 2, key: 'lead', name: '负责人', type: 'user', departmentId: 'delivery', memberRoles: ['frontend_lead'], enabled: true }]
 const m = mount('CustomFieldInputs', { objectType: 'requirement', modelValue: { testers: ['product'], lead: 'front' } }, async path => ({ items: path === '/members' ? people : defs })); await flush()
 assert.deepEqual(m.context.candidates(defs[0]).map(item => item.id), ['qa']); assert.deepEqual(m.context.candidates(defs[1]).map(item => item.id), ['lead'])
 assert.equal(m.context.unavailableMember(defs[1]), true); assert.equal(m.context.memberLabel('front'), '同名'); assert.equal(m.emits.length, 0)
 assert.deepEqual(m.props.modelValue, { testers: ['product'], lead: 'front' }); m.stop()
})
await test('role labels react to language changes but role keys and person names remain untouched', async () => {
 const m = mount('MemberMultiSelect', { modelValue: ['front'], members: people, memberRoles: ['frontend', 'frontend_lead'] })
 assert.match(m.context.candidateRoles, /前端工程师/); locale.value = 'en-US'; await flush(); assert.equal(m.context.candidateRoles, 'Frontend engineer / Frontend lead'); assert.equal(m.context.selected[0].name, '同名'); assert.deepEqual(m.props.memberRoles, ['frontend', 'frontend_lead']); locale.value = 'zh-CN'; m.stop()
})
await test('new requirements do not infer a product owner; editing preserves historical owner data', async () => {
 for (const id of ['front', 'product', 'inactive']) { const m = editorFixture(id); await m.load(); assert.deepEqual(m.f.ownerUserIds, []); assert.equal(m.dirty.value, false); m.stop() }
 const m = editorFixture('product', { id: 7, title: 'Existing', ownerUserIds: ['front', 'inactive'], owners: [{ id: 'front', name: '同名' }, { id: 'inactive', name: '停用前端' }] }); await m.load(); assert.deepEqual(m.f.ownerUserIds, ['front', 'inactive']); assert.equal(m.dirty.value, false); m.stop()
})
await test('requirement details retain product-owner constraints while the compact editor has no product-owner picker', () => {
 const details = read('src/views/Requirements.vue'), editor = read('src/views/Editor.vue'); assert.match(details, /MemberMultiSelect[^>]+detail-owners[^>]+:member-roles="\['product'\]"/); assert(!editor.includes('requirement-owner')); assert(!/MemberMultiSelect[^>]+requirement-assignee[^>]+:member-roles/.test(editor))
 const weights = read('src/components/RequirementWeights.vue'); assert.match(weights, /:members="members" :member-roles="row.roles"/); assert(!weights.includes('active:member.active')); assert.match(weights, /同一职能可绑定多人，难度数值只计算一次/)
 assert.match(read('src/components/CustomFieldInputs.vue'), /:member-roles="rolesFor\(definition\)"/)
})
console.log(`Passed ${count} member-role candidate regressions.`)

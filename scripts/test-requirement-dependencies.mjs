import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import ts from 'typescript'
import * as Vue from 'vue'
import { compileScript, compileTemplate, parse } from 'vue/compiler-sfc'

const read = path => readFile(new URL('../' + path, import.meta.url), 'utf8')
const js = source => ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText
function evaluate(source, imports = {}, globals = {}) {
  const exports = {}
  new Function('require', 'exports', ...Object.keys(globals), js(source))(id => {
    assert(id in imports, 'Unexpected import ' + id)
    return imports[id]
  }, exports, ...Object.values(globals))
  return exports
}

const dependencySource = await read('src/components/RequirementDependencies.vue')
const roadmapSource = await read('src/views/Roadmap.vue')
const scopeSource = await read('src/components/settingsScope.ts')
const mainSource = await read('src/main.ts')
const requirementsSource = await read('src/views/Requirements.vue')
const words = evaluate(await read('src/locales/requirements.en.ts')).default
const flush = async () => { for (let index = 0; index < 30; index++) { await Promise.resolve(); await Vue.nextTick() } }

class Element {
  constructor(tag, text = '') { Object.assign(this, { tag, text, props: {}, children: [], parent: null, isConnected: true }) }
  focus() { this.focused = true }
  contains(value) { return this === value || this.children.some(child => child.contains(value)) }
  closest() { return this.parent?.closest?.() || null }
  getClientRects() { return [{}] }
  querySelectorAll() { return [] }
}
const renderer = Vue.createRenderer({
  createElement: tag => new Element(tag), createText: text => new Element('#text', text), createComment: () => new Element('#comment'),
  insert(child, parent, anchor) { if (child.parent) { const index = child.parent.children.indexOf(child); if (index >= 0) child.parent.children.splice(index, 1) } child.parent = parent; const index = anchor ? parent.children.indexOf(anchor) : -1; if (index < 0) parent.children.push(child); else parent.children.splice(index, 0, child) },
  remove(child) { child.isConnected = false; if (child.parent) child.parent.children.splice(child.parent.children.indexOf(child), 1) },
  setText: (child, value) => { child.text = value }, setElementText: (child, value) => { child.text = value; child.children = [] },
  parentNode: child => child.parent, nextSibling: child => child.parent?.children[child.parent.children.indexOf(child) + 1] || null,
  patchProp: (child, key, _previous, value) => { child.props[key] = value },
})

const appSelect = Vue.defineComponent({
  name: 'AppSelect', props: { modelValue: [String, Number], options: Array, label: String, disabled: Boolean }, emits: ['update:modelValue'],
  setup: props => () => Vue.h('button', { type: 'button', disabled: props.disabled, 'data-select-label': props.label }, props.options?.find(item => item.value === props.modelValue)?.label || ''),
})

const endpoint = (id, projectId = 'project-a', title = 'Requirement ' + id) => ({ tenantId: 'tenant-a', projectId, projectName: projectId === 'project-a' ? 'Current project' : 'Insight project', requirementId: id, code: 'REQ-' + id, title, status: 'custom', statusName: '草稿', statusColor: '#2470ff', statusCategory: 'todo' })
const dependency = (id, relationType, target) => ({ id, relationType, counterpart: target, crossProject: target.projectId !== 'project-a', createdAt: '2026-09-04T00:00:00Z', updatedAt: '2026-09-04T00:00:00Z' })

async function mount(source, name, api, options = {}) {
  const window = new EventTarget(), storage = new Map([['devflow-project', 'project-a']]), localStorage = { getItem: key => storage.get(key) || null }
  const navigations = []
  window.location = { assign: path => navigations.push(path) }
  const globals = { window, document: { activeElement: new Element('button') }, localStorage }
  const scope = evaluate(scopeSource, { vue: Vue, '../api': { api } }, globals)
  const guards = []
  const imports = {
    vue: { ...Vue, withDirectives: value => value },
    'vue-router': { onBeforeRouteLeave: guard => guards.push(guard), onBeforeRouteUpdate: guard => guards.push(guard) },
    '../i18n': { t: (sourceText, params = {}) => (words[sourceText] || sourceText).replace(/\{(\w+)\}/g, (token, key) => String(params[key] ?? token)) },
    '../requirementWorkflow': { workflowStyle: () => ({}) },
    './AppSelect.vue': { default: appSelect }, '../components/AppSelect.vue': { default: appSelect },
    './settingsScope': scope, '../components/settingsScope': scope,
  }
  const descriptor = parse(source, { filename: name + '.vue' }).descriptor
  const script = compileScript(descriptor, { id: 'dependency-' + name })
  const template = compileTemplate({ source: descriptor.template.content, filename: name + '.vue', id: 'dependency-' + name, compilerOptions: { bindingMetadata: script.bindings } })
  assert.deepEqual(template.errors, [], name + ' template compiles')
  const component = evaluate(script.content, imports, globals).default
  component.render = evaluate(template.code, imports).render
  const emitted = []
  const props = Vue.reactive({ requirementId: 17, canEdit: options.canEdit ?? true })
  const app = renderer.createApp({ render: () => Vue.h(component, { ...props, onOpen: id => emitted.push(id) }) })
  const root = new Element('root')
  app.mount(root)
  await flush()
  return { app, context: app._instance.subTree.component.setupState, calls: api.calls, guards, props, window, navigations, emitted, stop: () => app.unmount() }
}

function dependencyAPI() {
  const calls = []
  let records = [dependency(91, 'blocks', endpoint(18))]
  const api = async (path, options = {}) => {
    calls.push({ path, options })
    if (path.startsWith('/requirements/17/dependencies?')) return { items: structuredClone(records), page: 1, pageSize: 20, hasMore: false }
    if (path === '/requirement-dependency-candidates?requirementId=17&scope=current&q=&page=1&pageSize=30') return { items: [endpoint(19)], page: 1, pageSize: 30, hasMore: false }
    if (path === '/requirements/17/dependencies' && options.method === 'POST') {
      const body = JSON.parse(options.body)
      const created = dependency(92, body.relationType, endpoint(body.targetRequirementId, body.targetProjectId || 'project-a'))
      records = [...records, created]
      return { item: structuredClone(created), created: true }
    }
    if (path === '/requirements/17/dependencies/91' && options.method === 'PATCH') {
      const body = JSON.parse(options.body)
      records = records.map(item => item.id === 91 ? { ...item, relationType: body.relationType } : item)
      return { item: structuredClone(records[0]), updated: true }
    }
    if (path === '/requirements/17/dependencies/91' && options.method === 'DELETE') { records = records.filter(item => item.id !== 91); return { deleted: true, id: 91 } }
    throw Error('Unexpected dependency request ' + path)
  }
  api.calls = calls
  return api
}

function roadmapAPI() {
  const calls = []
  const records = [
    { id: 17, code: 'REQ-17', title: 'Current sprint requirement', tenantId: 'tenant-a', projectId: 'project-a', projectName: 'Current project', sprint: 'Sprint 9', startDate: '2026-09-04', endDate: '2026-09-10', status: 'custom', statusName: '草稿', statusColor: '#2470ff', statusCategory: 'todo', priority: 'P1', dependencyStatus: { state: 'blocking', blockedByCount: 0, blockingCount: 1, relatedCount: 0 } },
    { id: 28, code: 'REQ-28', title: 'Cross-project blocker', tenantId: 'tenant-a', projectId: 'project-insight', projectName: 'Insight project', sprint: '', startDate: '', endDate: '', status: 'custom', statusName: '草稿', statusColor: '#2470ff', statusCategory: 'todo', priority: 'P2', dependencyStatus: { state: 'blocked', blockedByCount: 1, blockingCount: 0, relatedCount: 0 } },
  ]
  const api = async (path, options = {}) => {
    calls.push({ path, options })
    if (path.startsWith('/roadmap?')) return { items: structuredClone(records), page: 1, pageSize: 50, hasMore: false, scope: new URLSearchParams(path.split('?')[1]).get('scope') }
    throw Error('Unexpected roadmap request ' + path)
  }
  api.calls = calls
  return api
}

let count = 0
async function test(name, run) { await run(); count++; console.log('✓ ' + name) }

await test('dependency detail uses scoped CRUD endpoints, keeps bounded records, and blocks navigation during a write', async () => {
  const api = dependencyAPI(), mounted = await mount(dependencySource, 'RequirementDependencies', api), context = mounted.context
  assert.deepEqual(context.items.map(item => item.id), [91])
  assert(api.calls[0].path.includes('pageSize=20'))
  context.showPicker(); await flush()
  assert.deepEqual(context.eligibleCandidates.map(item => item.requirementId), [19])
  await context.create(context.eligibleCandidates[0])
  const create = api.calls.find(call => call.options.method === 'POST')
  assert.deepEqual(JSON.parse(create.options.body), { targetTenantId: 'tenant-a', targetProjectId: 'project-a', targetRequirementId: 19, relationType: 'blocks' })
  assert.equal(context.picker, false, 'successful create closes the candidate picker')
  const writing = context.update(context.items.find(item => item.id === 91), 'blocked_by')
  assert(mounted.guards.every(guard => guard() === false), 'route guards hold while relationship changes')
  await writing
  assert.equal(context.items.find(item => item.id === 91).relationType, 'blocked_by')
  await context.remove(context.items.find(item => item.id === 91))
  assert(!context.items.some(item => item.id === 91))
  assert(api.calls.every(call => call.options.headers.get('X-TaskLoom-Project') === 'project-a'))
  mounted.stop()
})

await test('dependency detail is read-only when requested and cross-project navigation carries only an API-provided project scope', async () => {
  const api = dependencyAPI(), mounted = await mount(dependencySource, 'RequirementDependencies', api, { canEdit: false })
  mounted.context.showPicker(); await flush()
  assert.equal(mounted.context.picker, false)
  mounted.context.open(endpoint(42, 'project-insight', 'Authorized cross project item'))
  assert.deepEqual(mounted.navigations, ['/requirements?req=42&project=project-insight'])
  assert.equal(api.calls.some(call => call.options.method), false)
  mounted.stop()
})

await test('roadmap groups planned work, supports the opt-in accessible-project scope, and preserves cross-project context on open', async () => {
  const api = roadmapAPI(), mounted = await mount(roadmapSource, 'Roadmap', api), context = mounted.context
  assert.deepEqual(context.groups.map(group => group.key), ['sprint:Sprint 9', 'unscheduled'])
  assert.equal(context.groups[1].items[0].dependencyStatus.state, 'blocked')
  context.viewScope = 'all'; await flush()
  assert(api.calls.some(call => call.path.includes('scope=all')))
  context.open(context.items.find(item => item.projectId === 'project-insight'))
  assert.deepEqual(mounted.navigations, ['/requirements?req=28&project=project-insight'])
  assert(api.calls.every(call => call.options.headers.get('X-TaskLoom-Project') === 'project-a'))
  mounted.stop()
})

await test('dependency and roadmap entry points remain additive to legacy requirement links', () => {
  assert.match(mainSource, /const Roadmap = \(\) => import\('\.\/views\/Roadmap\.vue'\)/)
  assert.match(mainSource, /\{path:'\/roadmap',component:Roadmap\}/)
  assert.match(requirementsSource, /RequirementDependencies/)
  assert.match(requirementsSource, /name:'需求依赖'/)
  assert.match(dependencySource, /requirement-dependency-candidates/)
  assert.match(dependencySource, /\/dependencies\//)
  assert.match(roadmapSource, /\/roadmap\?/)
  for (const key of ['需求依赖', '依赖状态', '被阻塞', '仅被阻塞', '交付路线图', '被阻塞 · {count} 项前置未完成']) assert(words[key], 'missing English entry ' + key)
})

console.log(`Passed ${count} requirement dependency and roadmap frontend tests.`)

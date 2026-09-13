import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import ts from 'typescript'
import * as Vue from 'vue'
import { compileScript, compileTemplate, parse } from 'vue/compiler-sfc'

/**
 * 测试协作在拆分为多个 SFC 后，回归脚本仍需要以接近真实 setup 生命周期的
 * 方式验证路由、并发请求和未保存内容保护。这一小型运行器只替换网络和路由，
 * 不会伪造待测组件中的业务状态。
 */
const root = new URL('../', import.meta.url)
export const read = path => readFile(new URL(path, root), 'utf8')
export const transpile = source => ts.transpileModule(source, {
  compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
}).outputText

function moduleFrom(source, imports = {}) {
  const output = {}
  new Function('require', 'exports', transpile(source))(id => imports[id] || {}, output)
  return output
}

export const testingWorkspace = moduleFrom(await read('src/testingWorkspace.ts'))
const memberRoles = moduleFrom(await read('src/memberRoles.ts'))

export const flush = async (rounds = 8) => {
  for (let index = 0; index < rounds; index++) {
    await Promise.resolve()
    await Vue.nextTick()
  }
}

export const deferred = () => {
  let resolve
  let reject
  const promise = new Promise((yes, no) => { resolve = yes; reject = no })
  return { promise, resolve, reject }
}

function eventWindow() {
  const listeners = new Map()
  const window = {
    confirm: () => false,
    addEventListener(type, listener) {
      if (!listeners.has(type)) listeners.set(type, new Set())
      listeners.get(type).add(listener)
    },
    removeEventListener(type, listener) { listeners.get(type)?.delete(listener) },
    dispatch(type, event = {}) { for (const listener of [...(listeners.get(type) || [])]) listener(event) },
  }
  return window
}

const cleanQuery = query => Object.fromEntries(Object.entries(query || {}).filter(([, value]) => value !== undefined && value !== null))

/**
 * 编译并挂载一个测试工作台 SFC。子组件替换为轻量桩，父组件/当前组件的
 * setup 和 watch 则全部真实执行，便于覆盖竞态和离开保护。
 */
export async function mountTestingComponent(path, {
  props = {},
  query = {},
  session = {},
  handler = async () => ({ items: [] }),
  compileTemplate: shouldCompileTemplate = true,
} = {}) {
  const source = await read(path)
  const descriptor = parse(source, { filename: path }).descriptor
  const script = compileScript(descriptor, { id: `testing-workspace-${path}` })
  if (shouldCompileTemplate && descriptor.template) {
    const template = compileTemplate({
      id: `testing-workspace-${path}`,
      filename: path,
      source: descriptor.template.content,
      compilerOptions: { bindingMetadata: script.bindings },
    })
    assert.deepEqual(template.errors, [], `${path} template must compile`)
  }

  const route = Vue.reactive({ query: { ...query } })
  const project = session.currentProject || { id: 'project-a' }
  const workspaceStore = Vue.reactive({
    currentProject: project,
    currentUser: { id: 'current-user', role: 'qa', ...(session.currentUser || {}) },
    operationDisabled: false,
    identityConflict: false,
    ...session,
  })
  const apiCalls = []
  const routerCalls = []
  const leaveGuards = []
  const updateGuards = []
  const unmounts = []
  const emits = []
  const window = eventWindow()
  const storage = new Map()
  const api = async (requestPath, options = {}) => {
    apiCalls.push({ path: requestPath, options })
    return handler(requestPath, options)
  }
  const router = {
    replace: async value => {
      routerCalls.push(value)
      route.query = cleanQuery(value?.query)
    },
    push: async value => {
      routerCalls.push(value)
      if (value?.query) route.query = cleanQuery(value.query)
    },
  }
  const imports = {
    vue: { ...Vue, onBeforeUnmount: callback => unmounts.push(callback) },
    'vue-router': {
      useRoute: () => route,
      useRouter: () => router,
      onBeforeRouteLeave: callback => leaveGuards.push(callback),
      onBeforeRouteUpdate: callback => updateGuards.push(callback),
    },
    '../api': { api },
    '../../api': { api },
    '../i18n': { t: value => String(value), formatDate: value => String(value) },
    '../../i18n': { t: value => String(value), formatDate: value => String(value) },
    '../stores/workspace': { useWorkspaceStore: () => workspaceStore },
    '../../stores/workspace': { useWorkspaceStore: () => workspaceStore },
    '../settingsScope': { useSettingsScope: () => ({ request: api, locked: Vue.ref(false), current: () => true, project: project.id }), useSettingsDialog: () => Vue.ref(null) },
    '../../settingsScope': { useSettingsScope: () => ({ request: api, locked: Vue.ref(false), current: () => true, project: project.id }) },
    '../layoutScope': { useLayoutBoolean: (_key, fallback) => Vue.ref(fallback) },
    '../../layoutScope': { useLayoutBoolean: (_key, fallback) => Vue.ref(fallback) },
    '../testingWorkspace': testingWorkspace,
    '../../testingWorkspace': testingWorkspace,
    '../../memberRoles': memberRoles,
  }
  const compiled = {}
  new Function('require', 'exports', 'window', 'localStorage', transpile(script.content))(
    id => imports[id] || (id.endsWith('.vue') ? { default: { name: id.split('/').at(-1).replace('.vue', '') } } : {}),
    compiled,
    window,
    { getItem: key => storage.get(key) ?? null, setItem: (key, value) => storage.set(key, String(value)), removeItem: key => storage.delete(key) },
  )
  const scope = Vue.effectScope()
  let exposed
  const state = scope.run(() => compiled.default.setup(Vue.reactive(props), {
    expose: value => { exposed = value },
    emit: (name, ...args) => emits.push({ name, args }),
  }))
  return {
    ...state,
    source,
    props,
    route,
    session: workspaceStore,
    apiCalls,
    routerCalls,
    leaveGuards,
    updateGuards,
    emits,
    window,
    storage,
    exposed,
    stop: () => {
      for (const callback of unmounts) callback()
      scope.stop()
    },
  }
}

export function workspaceFixture(overrides = {}) {
  return {
    libraries: [{ id: 1, name: '默认用例库', isDefault: true, count: 3 }],
    folders: [],
    locations: [],
    settings: { version: 1, fields: [], blockedEnabled: true, aiReviewRules: [], aiLogicRules: [], businessContext: '' },
    canEdit: true,
    canManage: true,
    designs: [],
    ...overrides,
  }
}

import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import ts from 'typescript'
import * as Vue from 'vue'
import { compileScript, compileTemplate, parse } from 'vue/compiler-sfc'

const read = path => readFileSync(new URL('../' + path, import.meta.url), 'utf8')
const transpile = source => ts.transpileModule(source, {
  compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
}).outputText
function evaluate(source, imports = {}, globals = {}) {
  const exports = {}
  new Function('require', 'exports', ...Object.keys(globals), transpile(source))(
    id => imports[id] || {}, exports, ...Object.values(globals),
  )
  return exports
}

/** 为缓存测试提供可观察的浏览器存储；不让真实浏览器状态影响测试结果。 */
function environment() {
  const values = new Map()
  const writes = []
  const config = { throwRead: false, throwWrite: false }
  const localStorage = {
    getItem(key) {
      if (config.throwRead) throw Error('storage read blocked')
      return values.get(key) ?? null
    },
    setItem(key, value) {
      if (config.throwWrite) throw Error('storage quota exceeded')
      writes.push({ key, value })
      values.set(key, value)
    },
  }
  const layout = evaluate(read('src/layoutScope.ts'), { vue: Vue }, { localStorage })
  const navigation = evaluate(read('src/projectNavigation.ts'), { vue: Vue, './layoutScope': layout }, { localStorage })
  const key = (tenant, user) => `devflow-layout:v1:${encodeURIComponent(tenant)}:${encodeURIComponent(user)}:project-navigation.order`
  function mountedOrder() {
    const scope = Vue.effectScope()
    const order = scope.run(() => navigation.useProjectNavigation())
    return { order, stop: () => scope.stop() }
  }
  return { values, writes, config, layout, navigation, key, mountedOrder }
}

let count = 0
async function test(name, run) { await run(); count++; console.log('✓ ' + name) }

await test('navigation schema is a closed, stable route map with the requested six project destinations', () => {
  const e = environment()
  assert.deepEqual(e.navigation.defaultProjectNavigation, ['requirements', 'iterations', 'defects', 'testing', 'roadmap', 'dashboard'])
  assert.deepEqual(
    e.navigation.defaultProjectNavigation.map(key => e.navigation.projectNavigationItems[key].path),
    ['/requirements', '/iterations', '/defects', '/tests', '/roadmap', '/dashboard'],
  )
  assert.equal(Object.keys(e.navigation.projectNavigationItems).length, 6)
})

await test('normalization rejects unknown or duplicate stored values and appends newly supported tabs safely', () => {
  const e = environment()
  const normalized = e.navigation.normalizeProjectNavigation([
    'testing', 'testing', 'https://untrusted.example', 'dashboard', { key: 'requirements' }, 'nope', 'iterations',
  ])
  assert.deepEqual(normalized, ['testing', 'dashboard', 'iterations', 'requirements', 'defects', 'roadmap'])
  assert.deepEqual(e.navigation.normalizeProjectNavigation('requirements'), e.navigation.defaultProjectNavigation)
  assert.deepEqual(e.navigation.normalizeProjectNavigation(Array(100).fill('defects')), ['defects', 'requirements', 'iterations', 'testing', 'roadmap', 'dashboard'])
})

await test('moves are bounded, immutable, and retain all required tabs', () => {
  const e = environment()
  const before = [...e.navigation.defaultProjectNavigation]
  const moved = e.navigation.moveProjectNavigation(before, 'roadmap', 1)
  assert.deepEqual(moved, ['requirements', 'roadmap', 'iterations', 'defects', 'testing', 'dashboard'])
  assert.deepEqual(before, e.navigation.defaultProjectNavigation)
  assert.deepEqual(e.navigation.moveProjectNavigation(before, 'roadmap', -1), before)
  assert.deepEqual(e.navigation.moveProjectNavigation(before, 'roadmap', 99), before)
})

await test('order persists only inside the active tenant and user scope, without cross-account leakage', () => {
  const e = environment()
  e.layout.applyLayoutScope('tenant A', 'user A')
  const first = e.mountedOrder()
  first.order.value = e.navigation.moveProjectNavigation(first.order.value, 'roadmap', 0)
  assert.deepEqual(JSON.parse(e.values.get(e.key('tenant A', 'user A'))), ['roadmap', 'requirements', 'iterations', 'defects', 'testing', 'dashboard'])

  e.layout.applyLayoutScope('tenant A', 'user B')
  assert.deepEqual(first.order.value, e.navigation.defaultProjectNavigation)
  assert.equal(e.values.has(e.key('tenant A', 'user B')), false)
  first.order.value = e.navigation.moveProjectNavigation(first.order.value, 'testing', 0)
  assert.deepEqual(JSON.parse(e.values.get(e.key('tenant A', 'user B'))), ['testing', 'requirements', 'iterations', 'defects', 'roadmap', 'dashboard'])

  e.layout.applyLayoutScope('tenant A', 'user A')
  assert.deepEqual(first.order.value, ['roadmap', 'requirements', 'iterations', 'defects', 'testing', 'dashboard'])
  first.stop()
})

await test('logged-out, malformed, and unavailable storage states keep navigation usable and never overwrite another scope', () => {
  const e = environment()
  const order = e.mountedOrder()
  order.order.value = e.navigation.moveProjectNavigation(order.order.value, 'defects', 0)
  assert.equal(e.writes.length, 0)

  e.layout.applyLayoutScope('tenant', 'member')
  e.values.set(e.key('tenant', 'member'), '{ bad json')
  e.layout.applyLayoutScope('other', 'member')
  e.layout.applyLayoutScope('tenant', 'member')
  assert.deepEqual(order.order.value, e.navigation.defaultProjectNavigation)
  e.config.throwWrite = true
  assert.doesNotThrow(() => { order.order.value = e.navigation.moveProjectNavigation(order.order.value, 'testing', 0) })
  assert.deepEqual(order.order.value.slice(0, 2), ['testing', 'requirements'])
  order.stop()
})

await test('component compiles and exposes drag, keyboard, pointer, cancellation, and router-safe navigation wiring', () => {
  const source = read('src/components/ProjectNavigation.vue')
  const { descriptor, errors } = parse(source)
  assert.deepEqual(errors, [])
  const script = compileScript(descriptor, { id: 'project-navigation-test' })
  const template = compileTemplate({ source: descriptor.template.content, filename: 'ProjectNavigation.vue', id: 'project-navigation-test', compilerOptions: { bindingMetadata: script.bindings } })
  assert.deepEqual(template.errors, [])
  assert.match(source, /<RouterLink[^>]*custom/)
  assert.match(source, /@click="navigate\(\$event, routeNavigate\)"/)
  assert.match(source, /@dragstart="startDrag\(\$event, key\)"/)
  assert.match(source, /@drop="drop\(\$event, key\)"/)
  assert.match(source, /@keydown="keydown\(\$event, key\)"/)
  assert.match(source, /@pointercancel="pointerFinish\(\$event, true\)"/)
  assert.match(source, /event\.key === 'Escape'/)
  assert.match(source, /event\.altKey/)
  assert.match(source, /suppressNavigationUntil/)
  assert.match(source, /aria-keyshortcuts="Alt\+ArrowLeft Alt\+ArrowRight"/)
  assert.match(source, /touch-action:pan-y/)
  assert.doesNotMatch(source, /router\.(?:push|replace)\(/)
})

console.log(`Passed ${count} project navigation behavior and safety tests.`)

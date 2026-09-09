import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import ts from 'typescript'
import * as Vue from 'vue'
import { parse, compileScript, compileTemplate } from 'vue/compiler-sfc'

const read = path => readFileSync(new URL('../' + path, import.meta.url), 'utf8')
const appSource = read('src/App.vue'), css = read('src/sidebar.css')
const transpile = source => ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText
function evaluate(source, imports = {}, globals = {}) { const exports = {}; new Function('require', 'exports', ...Object.keys(globals), transpile(source))(id => { assert(id in imports, id); return imports[id] }, exports, ...Object.values(globals)); return exports }
const buttonSource = appSource.match(/<button type="button" class="sidebar-collapse-toggle"[\s\S]*?<\/button>/)?.[0]
assert(buttonSource, 'real App sidebar toggle exists')
const buttonTemplate = compileTemplate({ source: buttonSource, filename: 'AppSidebarToggle.vue', id: 'sidebar-test', compilerOptions: { bindingMetadata: { sidebarCollapsed: 'setup-ref', railCollapsed: 'setup-ref', t: 'setup-const' } } })
assert.deepEqual(buttonTemplate.errors, [])
const render = evaluate(buttonTemplate.code, { vue: Vue }).render
const node = (tag, text = '') => ({ tag, text, props: {}, children: [], parent: null })
const renderer = Vue.createRenderer({ createElement: tag => node(tag), createText: text => node('#text', text), createComment: () => node('#comment'), insert(child, parent, anchor) { if (child.parent) child.parent.children.splice(child.parent.children.indexOf(child), 1); child.parent = parent; const at = anchor ? parent.children.indexOf(anchor) : -1; if (at < 0) parent.children.push(child); else parent.children.splice(at, 0, child) }, remove(child) { if (child.parent) child.parent.children.splice(child.parent.children.indexOf(child), 1) }, setText(child, text) { child.text = text }, setElementText(child, text) { child.text = text; child.children = [] }, parentNode: child => child.parent, nextSibling: child => child.parent?.children[child.parent.children.indexOf(child) + 1] || null, patchProp(child, key, _old, value) { child.props[key] = value } })
function environment() {
  const storage = new Map(), writes = [], failure = { read: false, write: false }
  const localStorage = { getItem: key => { if (failure.read) throw Error('blocked'); return storage.get(key) ?? null }, setItem: (key, value) => { if (failure.write) throw Error('quota'); writes.push({ key, value }); storage.set(key, value) } }
  const layout = evaluate(read('src/layoutScope.ts'), { vue: Vue }, { localStorage })
  const stateSource = ['const sidebarCollapsed', 'const railCollapsed'].map(start => appSource.split('\n').find(line => line.startsWith(start))).join('\n')
  assert.match(stateSource, /useLayoutBoolean\('sidebar-collapsed', false\)/)
  function mount(mobile = false) {
    const mobileViewport = Vue.ref(mobile), effects = Vue.effectScope()
    const state = effects.run(() => evaluate(stateSource + '\nexport { sidebarCollapsed, railCollapsed }', {}, { useLayoutBoolean: layout.useLayoutBoolean, computed: Vue.computed, mobileViewport }))
    const locale = Vue.ref('zh-CN'), t = value => locale.value === 'en-US' ? { '展开侧边栏': 'Expand sidebar', '收起侧边栏': 'Collapse sidebar' }[value] || value : value
    const container = node('root'), root = Vue.h(Vue.defineComponent({ setup: () => ({ ...state, t }), render }))
    renderer.render(root, container)
    return { ...state, mobileViewport, locale, button: () => container.children[0], stop() { renderer.render(null, container); effects.stop() } }
  }
  const key = (tenant = 'tenant', user = 'a') => `devflow-layout:v1:${encodeURIComponent(tenant)}:${encodeURIComponent(user)}:sidebar-collapsed`
  return { storage, writes, failure, layout, mount, key }
}
let count = 0
async function test(name, run) { await run(); count++; console.log('✓ ' + name) }

await test('real toggle starts expanded, updates accessible state, and restores the saved desktop preference on remount', async () => {
  const e = environment(); e.layout.applyLayoutScope('tenant', 'a'); let m = e.mount()
  assert.equal(m.railCollapsed.value, false); assert.equal(m.button().props['aria-expanded'], true); assert.equal(m.button().props.type, 'button'); assert.equal(m.button().props['aria-controls'], 'primary-navigation')
  m.button().props.onClick(); await Vue.nextTick(); assert.equal(m.railCollapsed.value, true); assert.equal(m.button().props['aria-expanded'], false); assert.equal(m.button().props['aria-label'], '展开侧边栏'); assert.equal(e.storage.get(e.key()), 'true')
  m.stop(); m = e.mount(); assert.equal(m.railCollapsed.value, true); m.button().props.onClick(); await Vue.nextTick(); assert.equal(m.railCollapsed.value, false); assert.equal(e.storage.get(e.key()), 'false'); m.stop()
})
await test('tenant/account switches and logout restore only the verified account preference without cross-writing', () => {
  const e = environment(); e.layout.applyLayoutScope('tenant', 'a'); const m = e.mount(); m.sidebarCollapsed.value = true; const before = e.writes.length
  e.layout.applyLayoutScope('tenant', 'b'); assert.equal(m.railCollapsed.value, false); assert.equal(e.writes.length, before)
  m.sidebarCollapsed.value = true; m.sidebarCollapsed.value = false; const after = e.writes.length
  e.layout.applyLayoutScope('another', 'a'); assert.equal(m.railCollapsed.value, false); assert.equal(e.writes.length, after)
  e.layout.applyLayoutScope('tenant', 'a'); assert.equal(m.railCollapsed.value, true); e.layout.clearLayoutScope(); assert.equal(m.railCollapsed.value, false); m.sidebarCollapsed.value = true; assert.equal(e.writes.length, after); m.stop()
})
await test('mobile presentation remains expanded and returning to desktop retains the original choice', () => {
  const e = environment(); e.layout.applyLayoutScope('tenant', 'a'); e.storage.set(e.key(), 'true'); const m = e.mount()
  assert.equal(m.railCollapsed.value, true); const writes = e.writes.length
  m.mobileViewport.value = true; assert.equal(m.railCollapsed.value, false); assert.equal(m.sidebarCollapsed.value, true); assert.equal(e.writes.length, writes)
  m.mobileViewport.value = false; assert.equal(m.railCollapsed.value, true); assert.equal(e.writes.length, writes); m.stop()
})
await test('corrupt cache and browser storage errors fall back safely without blocking the toggle', () => {
  for (const raw of ['invalid', 'null', '"true"', '1', '{}']) {
    const e = environment(); e.layout.applyLayoutScope('tenant', 'a'); e.storage.set(e.key(), raw); const m = e.mount(); assert.equal(m.railCollapsed.value, false); m.stop()
  }
  const e = environment(); e.layout.applyLayoutScope('tenant', 'a'); e.failure.read = true; e.failure.write = true; const m = e.mount(); assert.doesNotThrow(() => m.button().props.onClick()); assert.equal(m.railCollapsed.value, true); m.stop()
})
await test('toggle labels react to locale and native button semantics preserve keyboard activation', async () => {
  const e = environment(); e.layout.applyLayoutScope('tenant', 'a'); const m = e.mount(); m.locale.value = 'en-US'; await Vue.nextTick(); assert.equal(m.button().props.title, 'Collapse sidebar')
  assert.equal(m.button().tag, 'button'); assert.equal(m.button().props.tabindex, undefined); assert.equal(m.button().props.onKeydown, undefined)
  m.button().props.onClick(); await Vue.nextTick(); assert.equal(m.button().props.title, 'Expand sidebar'); assert.equal(m.button().props['aria-label'], 'Expand sidebar'); m.stop()
})
await test('collapsed navigation preserves every permitted route, icon, unread badge, and named personal/logout controls', () => {
  const { descriptor } = parse(appSource), script = compileScript(descriptor, { id: 'sidebar-shell' }), template = compileTemplate({ source: descriptor.template.content, filename: 'App.vue', id: 'sidebar-shell', compilerOptions: { bindingMetadata: script.bindings } })
  assert.deepEqual(template.errors, [])
  const rail = descriptor.template.ast.children.flatMap(node => node.children || []).find(node => node.tag === 'aside')
  assert(rail); const links = []
  function walk(node) { if (node.tag === 'router-link') links.push(node); for (const child of node.children || []) walk(child) } walk(rail)
  const targets = links.map(node => node.props.find(prop => prop.name === 'to')?.value?.content)
  for (const path of ['/projects', '/my-work', '/search', '/notifications', '/reports/workload', '/settings/fields', '/organization', '/settings/ai', '/profile']) assert(targets.includes(path), path)
  for (const link of links) { assert(link.props.some(prop => prop.name === 'bind' && prop.arg?.content === 'aria-label')); assert(link.props.some(prop => prop.name === 'bind' && prop.arg?.content === 'title')) }
  assert.match(appSource, /unread > 99 \? '99\+' : unread/); assert.match(appSource, /class="rail-logout-icon"/); assert.match(appSource, /'rail-collapsed':railCollapsed/)
})
await test('desktop-only CSS uses theme tokens, preserves active/focus affordances, and loads after the collaboration layer', () => {
  // 样式文件经过格式化后空格不应影响布局契约；断言语义选择器与关键尺寸，
  // 避免把视觉层是否正确错误地绑定到 CSS 是否被压缩。
  const desktopMedia = /@media\s*\(\s*min-width:\s*1025px\s*\)/
  assert.match(css, desktopMedia); assert.match(css, /\.rail\.rail-collapsed\s*\{[^}]*width:\s*72px;[^}]*min-width:\s*72px/)
  assert.match(css, /sidebar-collapse-toggle\s*\{\s*display:\s*none/); assert.match(css, /:focus-visible/); assert.match(css, /var\(--sidebar-accent[^)]*var\(--nav-hover\)/); assert.match(css, /var\(--sidebar-accent-foreground/)
  const mediaStart = css.search(desktopMedia); assert(css.indexOf('.rail.rail-collapsed') > mediaStart); assert.doesNotMatch(css, /pointer-events\s*:\s*none|opacity\s*:\s*0|router-link-active\s*\{[^}]*display\s*:\s*none/)
  const main = read('src/main.ts'); assert(main.indexOf("import './sidebar.css'") > main.indexOf("import './collaboration.css'"))
})
await test('collapse control remains reachable while the rail scrolls and the top notification badge stays attached to its icon', () => {
  assert.match(css, /sidebar-collapse-toggle\s*\{[^}]*position:\s*sticky;[^}]*top:\s*8px;[^}]*z-index:\s*4;/)
  assert.match(css, /\.top-actions \.icon-link em\s*\{[^}]*position:\s*absolute;[^}]*top:\s*-5px;[^}]*right:\s*-5px;[^}]*left:\s*auto;/)
  assert.match(css, /\.top-actions \.icon-link em\s*\{[^}]*font-variant-numeric:\s*tabular-nums;[^}]*white-space:\s*nowrap;/)
  assert.match(appSource, /class="notification-badge"/)
  assert.match(appSource, /unread > 99 \? '99\+' : unread/)
})
console.log(`Passed ${count} sidebar layout and navigation tests.`)

import assert from 'node:assert/strict'
import { readFileSync, readdirSync } from 'node:fs'
import { createRequire } from 'node:module'
import ts from 'typescript'
import * as Vue from 'vue'
import { parse, compileTemplate, compileStyle } from 'vue/compiler-sfc'
import { workflow } from './workflow-test-support.mjs'

const read = file => readFileSync(new URL('../' + file, import.meta.url), 'utf8')
const postcss = createRequire(import.meta.resolve('vite'))('postcss')
const dropdown = postcss.parse(read('src/dropdown-controls.css'))
const transition = read('src/components/RequirementTransition.vue')
const descriptor = parse(transition).descriptor
const declarations = rule => Object.fromEntries(rule.nodes.filter(node => node.type === 'decl').map(node => [node.prop, node.value]))
const ancestors = node => { const result = []; for (let parent = node.parent; parent; parent = parent.parent) result.push(parent); return result }
const states = ['草稿', '规划中', '评审中', '开发中', '进行中', '已完成'].map((key, id) => ({ id, key, name: key, color: '#059669', category: key === '已完成' ? 'done' : 'todo', enabled: true, system: true, sortOrder: id }))
let passed = 0
async function test(name, run) { await run(); passed++; console.log('✓ ' + name) }

await test('system state badges, board options and old server snapshots share distinct semantic colors without changing stored metadata', () => {
  const expected = { 草稿: '#64748B', 规划中: '#6366F1', 评审中: '#D97706', 开发中: '#2563EB', 进行中: '#2563EB', 已完成: '#059669' }
  const before = JSON.stringify(states)
  for (const [key, color] of Object.entries(expected)) {
    const snapshot = { status: key, statusSystem: true, statusColor: '#059669', statusCategory: 'todo' }
    assert.equal(workflow.workflowColor(key, states), color)
    assert.equal(workflow.workflowColor(snapshot), color)
    assert.equal(workflow.workflowStyle(snapshot).backgroundColor, color + '14')
    assert.equal(workflow.workflowOptions(states).find(option => option.value === key).color, color)
    assert.equal(workflow.stateInfo(snapshot).color, '#059669', 'presentation must not mutate the server snapshot')
    assert.equal(workflow.stateInfo(snapshot).category, 'todo', 'color is never a permission or completion rule')
  }
  assert.equal(JSON.stringify(states), before)
})
await test('custom states retain configured colors and names, including names/keys that resemble system states', () => {
  const custom = [{ ...states[1], system: false, name: '用户自定义规划' }]
  assert.equal(workflow.workflowColor('规划中', custom), '#059669')
  assert.equal(workflow.workflowColor({ status: '规划中', statusSystem: false, statusColor: '#AABBCC' }, states), '#AABBCC')
  assert.equal(workflow.statusLabel('规划中', custom, () => 'wrong translation'), '用户自定义规划')
  assert.equal(workflow.workflowOptions(custom)[0].color, '#059669')
  assert.match(read('src/components/StatusMultiSelect.vue'), /statusSystem:!option\.custom/)
})
await test('requirement, iteration, search and my-work badges use the same pure presentation mapping', () => {
  for (const view of ['Requirements', 'Sprints', 'Search', 'MyWork']) {
    const source = read('src/views/' + view + '.vue')
    assert.match(source, /workflowStyle\(x[,)]/, view)
    assert.match(source, /workflow-color/, view)
  }
  const sprint = read('src/views/Sprints.vue')
  assert.equal((sprint.match(/:style="workflowStyle\(x.status\)"/g) || []).length, 2, 'active and historical iteration cards')
  assert.match(sprint, /:style="workflowStyle\(selected.sprint.status\)"/)
  assert.match(transition, /workflowColor\(key,definitions\)/)
  assert.match(transition, /workflowStyle\(key,definitions\)/)
  assert.match(transition, /statusLabel\(key,definitions,t\)/, 'color always accompanies a readable label')
})
await test('transition content actually receives a viewport/collision height limit across scoped component boundaries', () => {
  const css = descriptor.styles.map(style => compileStyle({ source: style.content, filename: 'RequirementTransition.vue', id: 'data-v-test', scoped: true }).code).join('\n')
  const ast = postcss.parse(css), roots = [], scrolling = []
  ast.walkRules(rule => {
    if (rule.selector === '.transition-menu[data-slot="popover-content"]') roots.push(declarations(rule))
    if (rule.selector.includes('.transition-options')) scrolling.push(declarations(rule))
  })
  assert(roots.some(rule => rule['max-height']?.includes('--reka-popover-content-available-height') && rule['max-height'].includes('100dvh - 24px')))
  assert(roots.some(rule => rule.display === 'flex' && rule.overflow === 'hidden' && rule['min-height'] === '0'))
  assert(scrolling.some(rule => rule['min-height'] === '0' && rule.flex === '1 1 auto' && rule['overflow-y'] === 'auto'))
  assert.match(transition, /position-strategy="fixed"[^>]+:avoid-collisions="true"/)
  assert(!transition.includes('Teleport'), 'content must still inherit the app identity/inert guard')
})
await test('compact transition menu has no duplicate current value or verbose footer, with optional search and visible focus', () => {
  const template = descriptor.template.content
  assert(!template.includes('transition-current'))
  assert(!template.includes('<footer'))
  assert(!template.includes('仅显示当前角色可执行的流转'))
  assert.match(template, /<input v-if="searchable" v-model="query"/)
  assert.match(transition, /\.transition-option:focus-visible\{outline:2px solid var\(--ring\)/)
  let compactHeading = false
  dropdown.walkRules(rule => { if (rule.selector === '.transition-menu .transition-options h4') compactHeading = rule.nodes.some(node => node.prop === 'font-size' && node.value === 'var(--ui-font-caption)' && node.important) })
  assert(compactHeading, 'global page heading typography must not enlarge menu group labels')
  assert.deepEqual(compileTemplate({ source: template, filename: 'RequirementTransition.vue', id: 'transition-test' }).errors, [])
})
await test('search threshold, grouping and compact menu never add transitions or include the current value', async () => {
  const scope = Vue.effectScope(), props = Vue.reactive({ requirementId: 1, status: '草稿', definitions: states }), events = [], module = {}
  const imports = { vue: { ...Vue, onMounted: () => {}, onBeforeUnmount: () => {} }, '../api': { api: async () => ({ currentStatus: '草稿', allowedTransitions: ['草稿', '规划中', '规划中', '已完成'] }) }, '../i18n': { t: x => x }, '../requirementWorkflow': workflow }
  const script = descriptor.scriptSetup.content + '\nexport {allowed,options,groups,searchable,query,choose}'
  const code = ts.transpileModule(script, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText
  scope.run(() => new Function('require', 'exports', 'defineProps', 'defineEmits', 'localStorage', code)(id => imports[id] || {}, module, () => props, () => (...args) => events.push(args), { getItem: () => 'test-only-project' }))
  await Vue.nextTick(); await Vue.nextTick()
  assert.deepEqual(module.options.value, ['规划中', '已完成'])
  assert.equal(module.searchable.value, false)
  assert.deepEqual(module.groups.value.flatMap(group => group.items), ['规划中', '已完成'])
  module.query.value = '完成'; assert.deepEqual(module.options.value, ['已完成'])
  module.choose('开发中'); module.choose('草稿'); assert.deepEqual(events, [])
  module.choose('已完成'); assert.deepEqual(events, [['change', '已完成']])
  module.allowed.value = Array.from({ length: 9 }, (_, i) => 'custom-' + i)
  assert.equal(module.searchable.value, true)
  module.query.value = 'no result'; assert.equal(module.searchable.value, true, 'search must not disappear as the user types')
  scope.stop()
})
await test('native enhancement is doubly feature guarded and leaves touch, rich documents, multiple and size controls native', () => {
  const rules = []
  dropdown.walkRules(rule => { if (rule.selector.includes('::picker(select)')) rules.push(rule) })
  assert(rules.length >= 2)
  for (const rule of rules) {
    const parents = ancestors(rule)
    assert(parents.some(node => node.name === 'supports' && node.params === '(appearance: base-select) and selector(select::picker(select))'))
    assert(parents.some(node => node.name === 'media' && node.params === '(hover: hover) and (pointer: fine)'))
    assert.match(rule.selector, /\[multiple\], \[size\], \.rich-document \*, \.rich-content \*/)
  }
  const appearance = []
  dropdown.walkDecls('appearance', decl => appearance.push(decl))
  assert(appearance.length && appearance.every(decl => decl.value === 'base-select' && decl.important))
  assert(read('src/main.ts').includes("import './dropdown-controls.css'"))
})
await test('known popup families are flat, touch targets stay generous and shared popover keeps local inert/collision handling', () => {
  const css = read('src/dropdown-controls.css'), base = read('src/components/ui/popover/PopoverContent.vue')
  for (const name of ['[data-slot="popover-content"]', '.status-filter-menu', '.top-search-popup', '.requirement-picker-popover', '.mention-popup', '.rich-mention-popup', '.weight-quick-panel']) assert(css.includes(name), name)
  assert.match(css, /box-shadow: none !important/)
  assert.match(css, /--ui-menu-item-height: 30px/)
  assert.match(css, /\(pointer: coarse\)[\s\S]+--ui-menu-item-height: 40px/)
  assert.match(css, /outline: 2px solid var\(--ring\) !important/)
  assert(!base.includes('shadow-lg')); assert(!base.includes('zoom-in')); assert(!base.includes('<Teleport'))
  assert.match(base, /collisionPadding: 12/)
  assert.match(base, /stopImmediatePropagation/)
})
await test('shared single-select menus keep long labels readable and honor keyboard option boundaries', () => {
  const source = read('src/components/AppSelect.vue')
  assert.match(source, /function focusEdge\(last: boolean\)/)
  assert.match(source, /if \(event\.key === 'Home'\)[\s\S]*focusEdge\(false\)/)
  assert.match(source, /if \(event\.key === 'End'\)[\s\S]*focusEdge\(true\)/)
  assert.match(source, /\.app-select-option\{[^}]*min-width:0[^}]*overflow-wrap:anywhere/)
  assert.match(source, /\.app-select-option>span\{min-width:0;overflow-wrap:anywhere\}/)
})
await test('application toolbar and both table variants remain accessible and locally scroll long records', () => {
  const source = read('src/components/OrganizationInvitations.vue'), sfc = parse(source).descriptor
  assert.match(source, /<label v-else class="org-status-filter"><span>\{\{t\('审批状态'\)\}\}/)
  assert.match(source, /v-model="status"[^>]+@change="load"/)
  assert.equal((source.match(/:colspan="section==='invitations'\?5:6"/g) || []).length, 2)
  assert.match(source, /class="org-table-wrap" role="region"[^>]+tabindex="0"/)
  assert.match(source, /\.org-access-table td\{[^}]+overflow-wrap:anywhere/)
  assert.deepEqual(compileTemplate({ source: sfc.template.content, filename: 'OrganizationInvitations.vue', id: 'org-test' }).errors, [])
})
await test('all existing native selects are covered through the global element contract, without component replacement', () => {
  const files = []
  function visit(directory) {
    for (const entry of readdirSync(new URL('../' + directory, import.meta.url), { withFileTypes: true })) {
      if (entry.isDirectory()) visit(directory + '/' + entry.name)
      else if (entry.name.endsWith('.vue') && /<select\b/.test(read(directory + '/' + entry.name))) files.push(directory + '/' + entry.name)
    }
  }
  visit('src')
  assert(files.length >= 30)
  assert(files.includes('src/App.vue')); assert(files.includes('src/components/OrganizationInvitations.vue'))
  console.log('  Native select coverage: ' + files.length + ' Vue files; shared element selectors retain existing bindings.')
})
console.log(`Passed ${passed} dropdown/status-layout regressions.`)

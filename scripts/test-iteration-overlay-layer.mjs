import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { compileScript, compileStyle, compileTemplate, parse } from 'vue/compiler-sfc'

const read = file => readFileSync(new URL('../' + file, import.meta.url), 'utf8')
const app = read('src/App.vue')
const sprints = read('src/views/Sprints.vue')
const panelSource = read('src/slide-panels.css')
const glassSource = read('src/glass-system.css')
let count = 0
const test = (name, run) => { run(); count++; console.log('✓ ' + name) }

function compileVue(filename, source) {
  const { descriptor } = parse(source, { filename })
  const script = compileScript(descriptor, { id: filename })
  const template = compileTemplate({
    filename,
    id: filename,
    source: descriptor.template.content,
    compilerOptions: { bindingMetadata: script.bindings }
  })
  assert.deepEqual(template.errors, [], filename)
  return descriptor
}

const panelCSS = compileStyle({ filename: 'slide-panels.css', id: 'iteration-overlay', source: panelSource })
assert.deepEqual(panelCSS.errors, [])

function declarations(selector) {
  const values = {}
  panelCSS.rawResult.root.walkRules(rule => {
    if (rule.selector !== selector) return
    rule.walkDecls(decl => { values[decl.prop] = { value: decl.value, important: decl.important } })
  })
  return values
}

function cssVariable(name) {
  let value = ''
  panelCSS.rawResult.root.walkDecls(name, decl => { value = decl.value })
  return value
}

test('application exposes one workspace overlay host outside the scrolling main region', () => {
  compileVue('src/App.vue', app)
  const main = app.indexOf('<main id="workspace-content"')
  const host = app.indexOf('<div id="workspace-overlays"')
  const mobileNavigation = app.indexOf('<MobileWorkNavigation', host)
  assert(main >= 0 && host > main && mobileNavigation > host)
})

test('create-iteration drawer teleports to the shared host and keeps self-only dismissal', () => {
  compileVue('src/views/Sprints.vue', sprints)
  assert.match(sprints, /const overlayTargetReady=ref\(false\)/)
  assert.match(sprints, /const SprintOverlayHost=computed\(\(\)=>overlayTargetReady\.value\?Teleport:'div'\)/)
  assert.match(sprints, /overlayTargetReady\.value=typeof document\.getElementById==='function'&&!!document\.getElementById\('workspace-overlays'\)/)
  assert.match(sprints, /<component :is="SprintOverlayHost" v-bind="overlayTargetReady\?\{to:'#workspace-overlays'\}:\{\}"><div v-if="showSprint" class="modal-shade slide-panel-shade" @click\.self="!saving&&\(showSprint=false\)"/)
})

test('shared layer contract keeps navigation below a fixed isolated overlay and its panel', () => {
  const navigation = declarations('.app-shell .workspace > .topbar,\n.app-shell .project-nav')
  const shade = declarations('.slide-panel-shade')
  const panel = declarations('.slide-panel-shade > :is(.modal, .org-modal, .settings-modal, .test-modal)')
  assert.equal(navigation.position.value, 'relative')
  assert.equal(navigation['z-index'].value, 'var(--df-layer-navigation)')
  assert.equal(shade.position.value, 'fixed')
  assert.equal(shade.position.important, true)
  assert.equal(shade['z-index'].value, 'var(--df-layer-overlay)')
  assert.equal(shade['z-index'].important, true)
  assert.equal(shade.isolation.value, 'isolate')
  assert.equal(panel['z-index'].value, 'var(--df-layer-overlay-panel)')
  assert.equal(Number(cssVariable('--df-layer-navigation')), 1, 'topbar and project navigation must stay at layer 1')
  assert(Number(cssVariable('--df-layer-overlay')) > Number(cssVariable('--df-layer-navigation')))
  // 代访问模式需要继续用 top:64px 保留安全横幅，公共层不能用 inset!important 覆盖它。
  assert(!shade.inset?.important)
})

test('requirement creation and completion also escape the iteration page via the shared host', () => {
  const { descriptor } = parse(sprints)
  const found = new Set()
  function visit(node, hosts = []) {
    if (node.type === 1) {
      const isHost = node.tag === 'component' && node.props.some(prop => prop.type === 7 && prop.arg?.content === 'is' && prop.exp?.content === 'SprintOverlayHost')
      const nextHosts = isHost ? [...hosts, node] : hosts
      const condition = node.props.find(prop => prop.type === 7 && prop.name === 'if')?.exp?.content
      if (['showSprint', 'showRequirementComposer', 'showComplete'].includes(condition)) {
        assert(nextHosts.length, `${condition} must not render inside the animated iteration root`)
        const click = node.props.find(prop => prop.type === 7 && prop.name === 'on' && prop.arg?.content === 'click')
        assert(click?.modifiers.some(value => (value.content || value) === 'self'), `${condition} preserves backdrop-only dismissal`)
        found.add(condition)
      }
      for (const child of node.children || []) visit(child, nextHosts)
    } else for (const child of node.children || []) visit(child, hosts)
  }
  visit(descriptor.template.ast)
  assert.deepEqual([...found].sort(), ['showComplete', 'showRequirementComposer', 'showSprint'])
  assert.match(sprints, /class="drawer-shade sprint-requirement-shade" @click\.self="closeRequirementCreator"/)
  assert.match(sprints, /\.sprint-requirement-drawer\{[^}]*height:100%;max-height:100%;/)
})

test('overlay host contributes no layout box or additional stacking context', () => {
  const host = declarations('.workspace-overlay-host')
  assert.equal(host.display.value, 'contents')
  for (const property of ['transform', 'filter', 'backdrop-filter', 'contain', 'isolation', 'opacity', 'animation', 'z-index']) {
    assert(!host[property], `host must not establish ${property}`)
  }
})

test('page fade cannot retain a stacking context after completion or while a legacy overlay is open', () => {
  const glass = compileStyle({ filename: 'glass-system.css', id: 'glass-layer-regression', source: glassSource })
  assert.deepEqual(glass.errors, [])
  let entry, activeOverlay
  glass.rawResult.root.walkRules(rule => {
    if (rule.selector === '.app-shell .workspace > main > *' && rule.parent.params === '(prefers-reduced-motion: no-preference)') entry = rule
    if (rule.selector === '.app-shell .workspace > main > :has([class$="-shade"], [class*="-shade "], [role="dialog"])') activeOverlay = rule
  })
  assert(entry)
  const animation = entry.nodes.find(decl => decl.prop === 'animation').value
  assert(!/\b(both|forwards)\b/.test(animation), 'opacity animation cannot retain its stacking context')
  assert(activeOverlay)
  assert.equal(activeOverlay.nodes.find(decl => decl.prop === 'animation').value, 'none')
  for (const rule of [entry, activeOverlay]) rule.walkDecls(decl => {
    assert(!['transform', 'filter', 'backdrop-filter', 'contain', 'isolation', 'will-change'].includes(decl.prop), 'route motion must not trap fixed children')
  })
})

console.log(`Passed ${count} iteration overlay layer regressions.`)

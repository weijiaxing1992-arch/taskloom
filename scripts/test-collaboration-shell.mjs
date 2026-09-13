import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
const read = file => readFileSync(new URL('../' + file, import.meta.url), 'utf8')
const shell = read('src/App.vue')
const css = read('src/collaboration.css')
const projectNavigation = read('src/projectNavigation.ts')
const projectNavigationView = read('src/components/ProjectNavigation.vue')
let count = 0
function test(name, run) { run(); count++; console.log('✓ ' + name) }
test('workspace refresh retains every existing collaboration and administration route', () => {
  // 全局入口仍归 App；项目协作入口被拆到可排序导航，不能因为组件拆分而漏掉任何只读路由。
  for (const route of ['/projects','/my-work','/search','/notifications','/reports/workload','/profile','/settings/fields','/organization','/settings/ai']) {
    assert(shell.includes(`to="${route}"`), route)
  }
  assert.match(shell, /import ProjectNavigation from '\.\/components\/ProjectNavigation\.vue'/)
  assert.match(shell, /<ProjectNavigation\s*\/>/)
  for (const route of ['/requirements','/iterations','/defects','/tests','/dashboard']) {
    assert(projectNavigation.includes(`path: '${route}'`), route)
  }
  assert.match(projectNavigationView, /projectNavigationItems\[key\]\.path/)
})
test('new menu groups retain role, impersonation and mobile access guards', () => {
  assert.match(shell, /v-if="workspace.canAccessWorkload" to="\/reports\/workload"/)
  assert.match(shell, /v-if="workspace.canManageProject" to="\/settings\/fields"/)
  assert.match(shell, /v-if="workspace.canOpenOrganization" to="\/organization"/)
  assert.match(shell, /v-if="workspace.canManageOrganization&&!session.impersonation" to="\/settings\/ai"/)
  assert.match(shell, /:inert="mobileViewport&&!mobileMenu"/)
  assert.match(shell, /:inert="mobileViewport&&mobileMenu"/)
  assert.match(shell, /@keydown="mobileNavigationKeys"/)
})
test('both skins define readable menu and action tokens without changing business colors', () => {
  for (const selector of [':root', ':root[data-theme="dark"]']) {
    const rule = css.slice(css.indexOf(selector + ' {')).split('}')[0]
    for (const token of ['--primary','--nav-surface','--nav-hover','--nav-text','--nav-selected','--nav-selected-text','--action-fill','--action-hover','--action-text','--table-hover']) {
      assert(rule.includes(token + ':'), `${selector}: ${token}`)
    }
  }
  assert(!/\.status[.#\s,]|--tag-/.test(css), 'User-selected workflow/tag colors stay unchanged')
})
test('refresh keeps navigation scrollable with visible keyboard focus and reduced-motion support', () => {
  const source = read('src/collaboration.css')
  assert.match(source, /overflow-y: auto/)
  assert.match(source, /overflow-x: auto/)
  assert.match(source, /focus-visible/)
  assert.match(source, /prefers-reduced-motion: reduce/)
  assert.doesNotMatch(source, /scrollbar-width:\s*none|height:\s*100vh/)
  assert.doesNotMatch(source.split('.app-shell .rail {')[1].split('}')[0], /overflow:\s*hidden/)
  assert(read('src/main.ts').indexOf("'./collaboration.css'") > read('src/main.ts').indexOf("'./mobile.css'"))
})
console.log(`Passed ${count} collaborative shell checks.`)

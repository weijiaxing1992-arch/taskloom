import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { compileScript, compileStyle, compileTemplate, parse } from 'vue/compiler-sfc'

const read = name => readFileSync(new URL('../' + name, import.meta.url), 'utf8')
let passed = 0
function test(name, run) { run(); passed++; console.log('✓ ' + name) }
function compileVue(filename) {
  const source = read(filename), descriptor = parse(source, { filename }).descriptor
  const script = compileScript(descriptor, { id: filename.replace(/\W/g, '-') })
  assert.deepEqual(compileTemplate({ source: descriptor.template.content, filename, id: 'layout', compilerOptions: { bindingMetadata: script.bindings } }).errors, [], filename + ' template')
  assert.deepEqual(compileStyle({ source: descriptor.styles.map(item => item.content).join('\n'), filename, id: 'layout', scoped: descriptor.styles.some(item => item.scoped) }).errors, [], filename + ' style')
  return source
}

test('成员编辑抽屉把部门、账号状态与项目权限分成可收缩的信息区', () => {
  const source = compileVue('src/components/OrganizationMembers.vue')
  for (const token of ['class="org-form member-form"', 'member-departments-field', 'member-department-options', 'member-account-status', 'member-project-permissions', 'member-project-space', 'member-project-role-label', 'member-project-role-grid']) assert(source.includes(token), token)
  assert(!source.includes("<label>{{t('所属部门')}}<div class=\"org-multi\""), '部门多选不得嵌套 label，避免标签点击区域失效')
  assert.match(source, /\.member-department-options\{display:grid;grid-template-columns:repeat\(auto-fit,minmax\(190px,1fr\)\)/)
  assert.match(source, /\.org-project-role\{grid-template-columns:minmax\(210px,\.85fr\) minmax\(0,1\.65fr\) 32px/)
  assert.match(source, /\.member-project-role-grid\{display:grid;grid-template-columns:repeat\(auto-fit,minmax\(112px,1fr\)\)/)
  assert.match(source, /@media\(max-width:720px\)\{\.member-department-options\{grid-template-columns:minmax\(0,1fr\)/)
})

test('系统通知工具条把说明移到独立行并在小屏给按钮保留触达空间', () => {
  const source = compileVue('src/components/DesktopNotifications.vue')
  for (const token of ['desktop-notice-settings', 'desktop-notice-permission', 'desktop-notice-hint', 'desktop-notice-settings>.btn']) assert(source.includes(token), token)
  assert.match(source, /\.desktop-notice-hint\{order:2;flex:1 0 100%/)
  assert.match(source, /@media\(max-width:1040px\)/)
  assert.match(source, /@media\(max-width:640px\).*?\.desktop-notice-settings>\.btn\{flex:1 1 calc\(50% - 4px\)/s)
})

test('通知筛选与分类说明不会把控制器挤出工具栏', () => {
  const source = compileVue('src/views/Notifications.vue')
  assert.match(source, /\.notice-groups>span\{flex:1 0 100%;min-width:0/)
  assert.match(source, /\.notice-filters\{display:flex;align-items:center;gap:8px;min-width:0\}/)
  assert.match(source, /\.notice-filters :deep\(\.notification-filter-select\)\{flex:0 1 210px;min-width:150px;max-width:250px\}/)
  assert.match(source, /\.notice-filters :deep\(\.notification-filter-select\),\.module-toolbar :deep\(\.notification-filter-select\)\{flex:1 1 135px/)
  assert.match(source, /\.notification-list \.notice-content:focus-visible\{outline:2px solid var\(--ring\);outline-offset:3px\}/)
})

console.log(`Organization and notification layout: ${passed} tests passed`)

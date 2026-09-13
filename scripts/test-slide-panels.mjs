import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { parse, compileScript, compileTemplate, compileStyle } from 'vue/compiler-sfc'
import { createServer } from 'vite'
import { createSSRApp, h } from 'vue'
import { renderToString } from 'vue/server-renderer'

const read = name => readFileSync(new URL('../' + name, import.meta.url), 'utf8')
const panelCSS = compileStyle({ filename: 'slide-panels.css', id: 'slide-panels', source: read('src/slide-panels.css') })
assert.deepEqual(panelCSS.errors, [])
let count = 0
async function test(name, run) { await run(); count++; console.log('✓ ' + name) }
function rule(selector, property, mobile = false) {
  let value
  panelCSS.rawResult.root.walkRules(item => {
    if (item.selector !== selector) return
    let media = false
    for (let ancestor = item.parent; ancestor; ancestor = ancestor.parent) if (ancestor.type === 'atrule' && ancestor.name === 'media') media = ancestor.params.includes('max-width: 820px') ? 'mobile' : 'other'
    if (media === 'other' || media === 'mobile' && !mobile) return
    item.walkDecls(property, decl => value = decl.value)
  })
  return value
}

await test('explicit form and configuration surfaces compile with drawer opt-in', () => {
  for (const name of ['components/OrganizationModal', 'components/RequirementStatusSettings', 'components/WorkflowSettings', 'components/AutomationRulesSettings', 'components/WorkItemColumns', 'components/testing/TestCaseLibrary', 'components/testing/TestDesigns', 'views/Fields', 'views/Projects', 'views/Members', 'views/Sprints', 'views/Requirements']) {
    const filename = 'src/' + name + '.vue', source = read(filename), { descriptor } = parse(source, { filename })
    assert(source.includes('slide-panel-shade'), name)
    const compiled = compileScript(descriptor, { id: name })
    assert.deepEqual(compileTemplate({ filename, id: name, source: descriptor.template.content, compilerOptions: { bindingMetadata: compiled.bindings } }).errors, [], name)
  }
})
await test('dangerous confirmations and unsaved-change decisions remain independent dialogs', () => {
  assert.match(read('src/views/Requirements.vue'), /:class="\{'slide-panel-shade':categoryModal!=='delete'\}"/)
  for (const [filename, flag] of [['Requirements', 'leavePrompt'], ['Sprints', 'showComplete'], ['Members', 'impersonating'], ['Fields', 'deleteTarget']]) {
    const tag = read('src/views/' + filename + '.vue').match(new RegExp('<div v-if="' + flag + '"[^>]*>'))?.[0]
    assert(tag, filename); assert(!tag.includes('slide-panel-shade'), filename)
  }
  assert(!read('src/views/Login.vue').includes('slide-panel-shade'))
  for (const flag of ['impersonating', 'deleteTarget']) assert.match(read('src/components/OrganizationMembers.vue'), new RegExp('<OrganizationModal v-if="' + flag + '" presentation="confirmation"'))
  assert.match(read('src/components/OrganizationMemberBulkDialog.vue'), /:presentation="action==='wecom-config'\?'drawer':'confirmation'"/)
})
await test('desktop panels are edge-aligned and grid forms do not spread rows over the entire height', () => {
  assert.equal(rule('.slide-panel-shade', 'justify-content'), 'flex-end')
  const panel = '.slide-panel-shade > :is(.modal, .org-modal, .settings-modal, .test-modal)'
  assert.equal(rule(panel, 'height'), '100%'); assert.equal(rule(panel, 'max-height'), '100%'); assert.equal(rule(panel, 'box-shadow'), 'none'); assert.equal(rule(panel, 'width'), 'min(620px, 94vw)')
  const bodies = []; panelCSS.rawResult.root.walkRules(item => { if (item.selector.includes('.modal-body')) item.walkDecls('align-content', decl => bodies.push(decl.value)) })
  assert(bodies.includes('start'), 'grid rows must stay compact at the top')
  assert.match(read('src/main.ts'), /import '\.\/slide-panels\.css'/)
  const automationFields = '.slide-panel-shade .automation-panel-body > fieldset'
  assert.equal(rule(automationFields, 'display'), 'grid'); assert.equal(rule(automationFields, 'gap'), '17px'); assert.equal(rule(automationFields, 'border'), '0')
})
await test('mobile panels rise from the bottom within the safe area and honor reduced motion', () => {
  const panel = '.slide-panel-shade > :is(.modal, .org-modal, .settings-modal, .test-modal)'
  assert.equal(rule('.slide-panel-shade', 'align-items', true), 'flex-end')
  assert.equal(rule(panel, 'width', true), '100vw')
  assert.equal(rule(panel, 'height', true), 'calc(100% - max(16px, env(safe-area-inset-top)))')
  assert.equal(rule(panel, 'animation-name', true), 'slide-panel-rise')
  assert.match(read('src/slide-panels.css'), /prefers-reduced-motion: reduce/)
  assert.match(read('src/slide-panels.css'), /min-height: 44px/)
  // 同一 important 契约层才能覆盖原有按钮的40px；外层滚动不得与内部滚动竞争。
  assert.match(read('src/slide-panels.css'), /@layer ui-contract/)
  assert.equal(rule(panel, 'overflow'), 'hidden')
})
const server = await createServer({ server: { middlewareMode: true }, appType: 'custom' })
try {
  await test('shared organization forms render the same title, scroll body, footer and busy close protection', async () => {
    const { default: Panel } = await server.ssrLoadModule('/src/components/OrganizationModal.vue')
    const html = await renderToString(createSSRApp({ render: () => h(Panel, { title: '编辑成员', wide: true, busy: true }, { default: () => h('form', { id: 'example-form' }, '表单内容'), footer: () => h('button', { form: 'example-form', type: 'submit' }, '保存') }) }))
    for (const token of ['slide-panel-shade', 'org-modal-wide', 'role="dialog"', 'aria-modal="true"', 'aria-label="编辑成员"', 'org-modal-body', 'form="example-form"', 'disabled']) assert(html.includes(token), token)
    const confirmation = await renderToString(createSSRApp({ render: () => h(Panel, { title: '删除成员', presentation: 'confirmation' }, () => '确认信息') }))
    assert(!confirmation.includes('slide-panel-shade')); assert(confirmation.includes('确认信息')); assert(confirmation.includes('role="dialog"'))
  })
} finally { await server.close() }
console.log(`Passed ${count} shared slide-panel regressions. Browser geometry is checked separately.`)

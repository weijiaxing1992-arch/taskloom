import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { compileScript, compileTemplate, parse } from 'vue/compiler-sfc'

const read = path => readFile(new URL('../' + path, import.meta.url), 'utf8')
let passed = 0
const test = async (name, run) => { await run(); passed++; console.log('✓ ' + name) }

await test('iteration export stays in the shared list toolbar instead of taking a table row', async () => {
  const source = await read('src/views/Sprints.vue')
  const descriptor = parse(source).descriptor
  const script = compileScript(descriptor, { id: 'flat-list-toolbar' })
  const compiled = compileTemplate({ source: descriptor.template.content, filename: 'Sprints.vue', id: 'flat-list-toolbar', compilerOptions: { bindingMetadata: script.bindings } })
  assert.deepEqual(compiled.errors, [])
  const nodes = []
  const walk = (node, parents = []) => { if (node.type === 1) nodes.push({ node, parents }); for (const child of node.children || []) walk(child, [...parents, node]) }
  walk(descriptor.template.ast)
  const exportNode = nodes.find(({ node }) => node.tag === 'RequirementListExport')
  assert(exportNode, 'list export is rendered')
  const toolbar = source.indexOf('class="list-toolbar configurable-sprint-toolbar"')
  const exportOffset = source.indexOf('<RequirementListExport')
  const tableTemplate = source.indexOf("<template v-else-if=\"activeTab==='工作项列表'\">")
  assert(toolbar >= 0 && exportOffset > toolbar && exportOffset < tableTemplate, 'list export belongs to the shared toolbar')
  assert(exportNode.node.props.some(prop => prop.name === 'if' && prop.exp?.content === "activeTab==='工作项列表'"), 'board view does not render requirement export')
  assert(!source.includes('<template v-else-if="activeTab===\'工作项列表\'">\n <RequirementListExport'), 'export is not a standalone row above the table')
})

await test('common selectors use flat surfaces while retaining visible keyboard focus', async () => {
  const [select, statuses, people, filters, visual] = await Promise.all([
    read('src/components/AppSelect.vue'), read('src/components/StatusMultiSelect.vue'), read('src/components/MemberMultiSelect.vue'), read('src/components/WorkItemFilters.vue'), read('src/work-items-visual.css')
  ])
  assert.match(select, /app-select-menu[^}]*box-shadow:none!important/)
  assert.match(select, /app-select-trigger:focus-visible\{outline:2px solid/)
  assert.match(statuses, /status-filter-menu[^}]*box-shadow:none/)
  assert.match(people, /member-options[^}]*box-shadow:none/)
  assert.match(filters, /work-filter-panel[^}]*box-shadow:none/)
  assert.match(visual, /--work-item-shadow: none/)
  assert.match(visual, /--work-item-raised-shadow: none/)
})

console.log(`Passed ${passed} flat list toolbar checks.`)

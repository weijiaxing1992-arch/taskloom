import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { parse, compileScript, compileTemplate } from 'vue/compiler-sfc'
import ts from 'typescript'

const root = new URL('../', import.meta.url)
const requirements = await readFile(new URL('src/views/Requirements.vue', root), 'utf8')
const codeControl = await readFile(new URL('src/components/RequirementCode.vue', root), 'utf8')
let passed = 0
function test(name, run) { run(); passed++; console.log('✓ ' + name) }

test('requirement table template keeps the bulk selector and the first two data columns in a stable sticky sequence', () => {
  const descriptor = parse(requirements, { filename: 'Requirements.vue' }).descriptor
  const script = compileScript(descriptor, { id: 'requirement-code-column' })
  const template = compileTemplate({ source: descriptor.template.content, filename: 'Requirements.vue', id: 'requirement-code-column', compilerOptions: { bindingMetadata: script.bindings } })
  assert.deepEqual(template.errors, [])
  assert.match(requirements, /const requirementCodeColumnWidth=118/)
  assert.match(requirements, /:class="\{'has-bulk-selection':canEdit\}" :style="tableStyle"/)
  assert.match(requirements, /class="bulk-selection sticky-selection"/)
  assert.match(requirements, /:style="\{width:columnWidth\(c\)\+'px'\}"/)
  assert.match(requirements, /'--requirement-bulk-width':canEdit\.value\?'44px':'0px'/)
  assert.match(requirements, /\.configurable-table \.sticky-code\{left:var\(--requirement-bulk-width\)/)
  assert.match(requirements, /\.configurable-table \.sticky-title\{left:calc\(var\(--requirement-bulk-width\) \+ var\(--requirement-code-width\)\)/)
})

test('the visible requirement sequence is numeric, six digits in the normal range, and never silently collides above that range', () => {
  const definition = requirements.match(/function requirementSerial\(x:any\):string\{[\s\S]*?\n\}/)?.[0]
  assert.ok(definition, 'requirement serial formatter should exist')
  const serial = new Function('x', ts.transpileModule(`${definition}\nreturn requirementSerial(x)`, { compilerOptions: { target: ts.ScriptTarget.ES2022 } }).outputText)
  assert.equal(serial({ id: 1, code: 'REQ-0001' }), '000001')
  assert.equal(serial({ id: 15, code: 'REQ-0015' }), '000015')
  assert.equal(serial({ code: 'REQ-0042' }), '000042')
  assert.equal(serial({ id: 1_000_000, code: 'REQ-1000000' }), '1000000')
  assert.match(requirements, /if\(key==='code'\)return requirementSerial\(x\)/)
  assert.match(requirements, /if\(key==='parentId'\)return value\?requirementSerial\(\{id:value\}\):'—'/)
})

test('numeric display labels preserve the existing keyboard and copy actions without changing the raw collaboration payload', () => {
  assert.match(requirements, /:display-code="requirementSerial\(x\)"/)
  assert.match(requirements, /:aria-label="t\('选择需求 \{code\}',\{code:requirementSerial\(x\)\}\)"/)
  assert.match(codeControl, /displayCode\?:string/)
  assert.match(codeControl, /const code=computed\(\(\)=>props\.displayCode\?\.trim\(\)\|\|props\.requirement\.code\|\|String\(props\.requirement\.id\)\)/)
  assert.match(codeControl, /协作摘要仍使用服务端原始编号/)
})

test('narrow layouts release every sticky column so horizontal scrolling remains usable', () => {
  assert.match(requirements, /\.configurable-table \.sticky-selection,\.configurable-table \.sticky-code,\.configurable-table \.sticky-title\{position:static!important\}/)
  assert.match(requirements, /\.configurable-table \.sticky-code :deep\(\.requirement-code-control\)\{display:inline-flex;min-width:0;max-width:100%\}/)
})

console.log(`Passed ${passed} requirement code-column layout tests.`)

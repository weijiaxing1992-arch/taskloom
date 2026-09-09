import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import ts from 'typescript'
import { parse, compileScript, compileStyle, compileTemplate } from 'vue/compiler-sfc'

const root = new URL('../', import.meta.url)
const read = path => readFileSync(new URL(path, root), 'utf8')
const utilitySource = read('src/workItemDrafts.ts')
const utilityExports = {}
const utilityCode = ts.transpileModule(utilitySource, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText
new Function('exports', utilityCode)(utilityExports)
const { cloneDraftObject, draftPreview, draftReadableHTML, draftTitle, workItemDraftScope, newWorkItemDraftId, MAX_WORK_ITEM_DRAFT_BYTES } = utilityExports
let count = 0
function test(name, run) { run(); count++; console.log('✓ ' + name) }

test('draft data is copied as plain JSON and blocks prototype-pollution keys', () => {
  const source = { title: '安全草稿', nested: { number: 7 }, list: [true, null] }
  const copied = cloneDraftObject(source)
  assert.notEqual(copied, source); assert.deepEqual(JSON.parse(JSON.stringify(copied)), source)
  assert.throws(() => cloneDraftObject(JSON.parse('{"__proto__":{"polluted":true}}')), /受保护字段/)
  assert.throws(() => cloneDraftObject({ date: new Date() }), /非普通对象/)
  assert.throws(() => cloneDraftObject({ bad: Number.NaN }), /无效数字/)
})
test('draft scope is tenant/user/project isolated and UUIDs are well formed', () => {
  assert.notEqual(workItemDraftScope('tenant one', 'u/a', 'p:1'), workItemDraftScope('tenant one', 'u/b', 'p:1'))
  assert.match(workItemDraftScope('tenant one', 'u/a', 'p:1'), /^draft-v1:tenant%20one:u%2Fa:p%3A1$/)
  assert.match(newWorkItemDraftId(), /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i)
})
test('preview and directly-viewable HTML escape user HTML and do not expose embedded asset data', () => {
  const payload = { title: '<img src=x onerror=alert(1)>', descriptionDoc: { content: [{ type: 'paragraph', content: [{ type: 'text', text: '需求 <正文>' }] }, { type: 'image', attrs: { data: 'private-base64-image', name: '图<片>.png' } }] } }
  assert.equal(draftTitle(payload), '<img src=x onerror=alert(1)>')
  assert.equal(draftPreview(payload), '需求 <正文>')
  const html = draftReadableHTML({ kind: 'requirement', payload, updatedAt: '2026-09-04T10:00:00Z' })
  assert.match(html, /&lt;img src=x onerror=alert\(1\)&gt;/); assert.match(html, /图&lt;片&gt;\.png/)
  assert.doesNotMatch(html, /private-base64-image/); assert.doesNotMatch(html, /<img src=x onerror/)
})
test('draft size guard is defined below the private API request cap and component contains required recovery hooks', () => {
  assert.equal(MAX_WORK_ITEM_DRAFT_BYTES, 30 * 1024 * 1024)
  const source = read('src/components/WorkItemDrafts.vue'), { descriptor } = parse(source)
  const script = compileScript(descriptor, { id: 'work-item-drafts' })
  assert.deepEqual(compileTemplate({ id: 'work-item-drafts', filename: 'WorkItemDrafts.vue', source: descriptor.template.content, compilerOptions: { bindingMetadata: script.bindings } }).errors, [])
  assert.deepEqual(compileStyle({ id: 'work-item-drafts', filename: 'WorkItemDrafts.vue', source: descriptor.styles.map(style => style.content).join('\n'), scoped: true }).errors, [])
  assert.match(source, /setInterval\([\s\S]*30_000/); assert.match(source, /window\.addEventListener\('pagehide'/); assert.match(source, /\/drafts\/\$\{encodeURIComponent\(record\.id\)\}/); assert.match(source, /baseVersion: record\.version/); assert.match(source, /确认删除草稿/); assert.match(source, /下载可查看 HTML/); assert.match(source, /emit\('restore'/)
})
console.log(`Passed ${count} work-item draft regression tests.`)

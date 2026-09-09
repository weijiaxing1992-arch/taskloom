import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { parse, compileScript, compileTemplate } from 'vue/compiler-sfc'
import ts from 'typescript'

const root = new URL('../', import.meta.url)
const source = await readFile(new URL('src/views/Requirements.vue', root), 'utf8')
const localeSource = await readFile(new URL('src/locales/requirements.en.ts', root), 'utf8')
const localeExports = {}
new Function('exports', ts.transpileModule(localeSource, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText)(localeExports)
const messages = localeExports.default
let count = 0
function test(name, run) { run(); count++; console.log('✓ ' + name) }

test('requirement board templates compile with the compact disclosure control', () => {
  const descriptor = parse(source, { filename: 'Requirements.vue' }).descriptor
  const script = compileScript(descriptor, { id: 'requirement-board-compact' })
  const template = compileTemplate({ source: descriptor.template.content, filename: 'Requirements.vue', id: 'requirement-board-compact', compilerOptions: { bindingMetadata: script.bindings } })
  assert.deepEqual(template.errors, [])
  assert.match(source, /const expandedBoardCardIds=ref<number\[\]>\(\[\]\)/)
  assert.match(source, /function toggleBoardCard\(id:number\)/)
})

test('the default board card keeps the compact row and only exposes metadata after an explicit action', () => {
  const board = source.slice(source.indexOf("v-else-if=\"listView==='board'\""), source.indexOf('v-else class="table-wrap"'))
  assert.match(board, /class="requirement-board-summary"/)
  assert.match(board, /class="requirement-board-disclosure"/)
  assert.match(board, /@click\.stop="toggleBoardCard\(item\.id\)"/)
  assert.match(board, /:aria-expanded="boardCardExpanded\(item\.id\)"/)
  assert.match(board, /v-show="boardCardExpanded\(item\.id\)" class="requirement-board-details"/)
  assert.ok(board.indexOf('class="requirement-board-summary"') < board.indexOf('class="requirement-board-details"'))
})

test('compact cards preserve a single-line title, semantic colors and mobile touch disclosure', () => {
  assert.match(source, /\.requirement-board-summary\{display:grid;grid-template-columns:auto minmax\(0,1fr\) auto/)
  assert.match(source, /\.requirement-board-summary strong\{[^}]*text-overflow:ellipsis;white-space:nowrap;color:var\(--foreground\)/)
  assert.match(source, /\.requirement-board-details\{[^}]*border-top:1px solid color-mix\(in srgb,var\(--border\)/)
  assert.match(source, /\.requirement-board-disclosure:hover\{background:var\(--accent\);color:var\(--accent-foreground\)\}/)
  assert.match(source, /\.requirement-board-disclosure\{min-height:34px/)
})

test('new disclosure copy is available in English', () => {
  for (const key of ['展开更多信息', '收起更多信息', '展开需求 {title} 的更多信息', '收起需求 {title} 的更多信息']) assert.ok(Object.hasOwn(messages, key), key)
})

console.log(`Passed ${count} compact requirement board tests.`)

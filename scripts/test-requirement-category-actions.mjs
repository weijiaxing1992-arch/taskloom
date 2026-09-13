import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { compileScript, compileTemplate, parse } from 'vue/compiler-sfc'

const source = await readFile(new URL('../src/views/Requirements.vue', import.meta.url), 'utf8')
const descriptor = parse(source).descriptor
const script = compileScript(descriptor, { id: 'requirement-category-actions' })
const template = compileTemplate({
  source: descriptor.template.content,
  filename: 'Requirements.vue',
  id: 'requirement-category-actions',
  compilerOptions: { bindingMetadata: script.bindings },
})

assert.deepEqual(template.errors, [])

const actions = source.match(/<div v-if="categoryMenu===c\.id"[\s\S]*?class="category-actions"[\s\S]*?<\/div><\/div><p v-if="!visibleCategories\.length"/)
assert(actions, 'category action region exists')
for (const label of ['上移', '下移', '重命名', '删除分类']) assert.match(actions[0], new RegExp(`t\\('${label}'\\)`), label)
assert.match(actions[0], /role="group"/)
assert.match(source, /:aria-controls="'category-actions-'\+c\.id"/)
assert.match(actions[0], /:id="'category-actions-'\+c\.id"/)
assert.match(actions[0], /@keydown\.esc\.stop="categoryMenu=null"/)
assert.match(actions[0], /type="button" class="category-action"/)
assert.match(actions[0], /type="button" class="category-action danger"/)

for (const rule of [
  /\.requirements-sidebar \.category-actions\{display:grid;grid-template-columns:minmax\(0,1fr\) minmax\(0,1fr\);/,
  /\.requirements-sidebar \.category-reorder-actions\{grid-column:1\/-1;display:grid;/,
  /\.requirements-sidebar \.category-actions button\{display:flex;[\s\S]*?min-width:0;[\s\S]*?width:100%;/,
  /\.requirements-sidebar \.category-actions \.category-action\.danger\{color:#b54751;/,
  /@media\(max-width:760px\)\{\.requirements-v4 \.category-folder\{min-width:176px\}/,
]) assert.match(source, rule)

console.log('Requirement category actions: compact two-row grid, keyboard dismissal, and mobile width guard passed')

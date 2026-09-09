import assert from 'node:assert/strict'
import { readFileSync, existsSync } from 'node:fs'
import ts from 'typescript'
import { parse, compileScript, compileTemplate } from 'vue/compiler-sfc'

const read = path => readFileSync(new URL('../' + path, import.meta.url), 'utf8')
const exports = {}
new Function('exports', ts.transpileModule(read('src/helpDocumentation.ts'), { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText)(exports)
const { parseHelpDocument, renderHelpSection, safeDocumentationLink, searchHelpDocuments, headingSlug, documentRoutes } = exports
const source = markdown => ({ id: 'guide', title: '产品帮助手册', filename: 'product-handbook.md', description: '测试', markdown })
let count = 0
function test(name, run) { run(); count++; console.log('✓ ' + name) }

test('chapters and duplicate headings get stable searchable anchors, excluding fenced code', () => {
  const document = parseHelpDocument(source('# 手册\n简介\n## 通知与 @\n正文\n### 处理失败\n说明\n## 通知与 @\n```md\n## 不是真章节\n```\n### 处理失败\n第二段'))
  assert.deepEqual(document.sections.map(item => item.id), ['overview', '通知与', '通知与-1'])
  assert.deepEqual(document.sections[2].headings.map(item => item.id), ['通知与-1', '处理失败-1'])
  assert(!renderHelpSection(document.sections[2]).includes('id="不是真章节"'))
  assert.match(renderHelpSection(document.sections[1]), /<h3 id="处理失败">/)
  assert.equal(headingSlug('1. `If-Match` 与 ETag'), '1-if-match-与-etag')
  assert.deepEqual(parseHelpDocument(source('# 空文档')).sections, [])
  const conflicting = parseHelpDocument(source('## Retry\n第一章\n## Retry\n第二章\n## Retry-1\n第三章'))
  assert.deepEqual(conflicting.sections.map(item => item.id), ['retry', 'retry-1', 'retry-1-1'])
  const fenced = parseHelpDocument(source('## 示例\n~~~md\n~~~not-a-closing-fence\n## 代码中的标题\n~~~\n## 后续章节\n真实内容'))
  assert.deepEqual(fenced.sections.map(item => item.id), ['示例', '后续章节'])
  assert.match(renderHelpSection(fenced.sections[0]), /## 代码中的标题/)
})

test('documentation links cannot invoke scripts, access files, bypass origins, or call internal APIs', () => {
  for (const unsafe of ['javascript:alert(1)', 'JaVaScRiPt:alert(1)', 'data:text/html,x', 'file:///tmp/x', '//evil.test', '/\\evil.test', 'https://user:pass@host.test', '/api/auth/logout', '/api', '/unknown', 'https:\n//evil.test', '%6aavascript:evil', '../private.txt', '/api/open/v1/requirements']) assert.equal(safeDocumentationLink(unsafe), null, unsafe)
  assert.equal(safeDocumentationLink('api-reference.md#鉴权'), '/help/api#鉴权')
  assert.equal(safeDocumentationLink('./product-handbook.md'), '/help/guide')
  assert.equal(safeDocumentationLink('/settings/integrations'), '/settings/integrations')
  assert.equal(safeDocumentationLink('https://example.com/reference'), 'https://example.com/reference')
  assert.equal(safeDocumentationLink('/help/../api/auth/logout'), null)
  assert.equal(safeDocumentationLink('/help/%2e%2e/api/auth/logout'), null)
  assert.equal(safeDocumentationLink('https://devflow.example/api/auth/logout', 'https://devflow.example'), null)
  assert.equal(safeDocumentationLink('https://devflow.example/help/%2e%2e/api/auth/logout', 'https://devflow.example'), null)
  assert.equal(safeDocumentationLink('https://developers.openai.com/api/docs/guides/structured-outputs', 'https://devflow.example'), 'https://developers.openai.com/api/docs/guides/structured-outputs')
})

test('HTML, attributes, inline code, and unsafe links are rendered as inert text', () => {
  const document = parseHelpDocument(source('## 安全\n<script>alert(1)</script>\n<img src=x onerror=evil>\n[危险](javascript:evil) **说明** `</code><img>`\n[参考](https://example.com/?x="onclick="evil)\n```html\n</pre><script>evil</script>\n```'))
  const html = renderHelpSection(document.sections[0])
  assert(!html.includes('<script>')); assert(!html.includes('<img')); assert(!html.includes('href="javascript:'))
  assert(html.includes('&lt;script&gt;')); assert(html.includes('<strong>说明</strong>'))
  assert(html.includes('rel="noopener noreferrer"')); assert(html.includes('&quot;onclick=&quot;evil'))
  assert(html.includes('&lt;/pre&gt;&lt;script&gt;evil&lt;/script&gt;'))
  const linkSection = parseHelpDocument(source('## 规范\n[下载规范](openapi.json)')).sections[0]
  assert.match(renderHelpSection(linkSection, '/assets/openapi-hash.json'), /href="\/assets\/openapi-hash.json" download="devflow-openapi.json"/)
  assert.match(renderHelpSection(linkSection, '/docs/openapi.json'), /download="devflow-openapi.json"/)
  assert(!renderHelpSection(linkSection, 'javascript:evil').includes('javascript:evil'))
})

test('tables preserve code enum pipes, escaped pipes and accessible headings', () => {
  const document = parseHelpDocument(source('## 接口\n| 字段 | 类型 |\n| --- | --- |\n| status | `pass|fail` |\n| 标签 | a\\|b |\n\n> 写入前请检查\n\n3. 第三步\n4. 第四步'))
  const html = renderHelpSection(document.sections[0])
  assert.equal((html.match(/<td>/g) || []).length, 4)
  assert.match(html, /<code>pass\|fail<\/code>/)
  assert.match(html, /<td>a\|b<\/td>/)
  assert.match(html, /scope="col"/); assert.match(html, /tabindex="0" role="region"/)
  assert.match(html, /<ol start="3">/); assert.match(html, /<blockquote>/)
})

test('Chinese and case-insensitive API searches require every term and include document context', () => {
  const docs = [parseHelpDocument(source('## 通知中心\n评论 @成员 后有通知。\n## 写入\n更新必须传 If-Match 和幂等键。'))]
  assert.equal(searchHelpDocuments(docs, '通知 @成员').length, 1)
  assert.equal(searchHelpDocuments(docs, 'if-match 更新')[0].title, '写入')
  assert.equal(searchHelpDocuments(docs, '帮助手册 幂等键').length, 1)
  assert.equal(searchHelpDocuments(docs, '不存在').length, 0)
  assert.equal(searchHelpDocuments(docs, '  ').length, 0)
})

test('all published manuals are bundled, have substantive chapters and safe unique anchors', () => {
  const content = read('src/helpContent.ts')
  for (const filename of Object.keys(documentRoutes).filter(name => name.endsWith('.md'))) {
    assert(existsSync(new URL('../docs/' + filename, import.meta.url)), 'Missing packaged document: ' + filename)
    assert(content.includes(filename + '?raw'), 'Document not bundled: ' + filename)
    const document = parseHelpDocument({ ...source(read('docs/' + filename)), filename })
    assert(document.sections.length >= 4, filename + ' must have substantive chapters')
    const ids = []
    for (const section of document.sections) {
      ids.push(...section.headings.map(item => item.id)); const html = renderHelpSection(section)
      assert(!/<script[\s>]/i.test(html), filename)
      assert(!/href="(?:javascript:|\/api\/)/i.test(html), filename)
    }
    assert.equal(ids.length, new Set(ids).size, filename + ' has duplicate anchors')
  }
  const spec = JSON.parse(read('docs/openapi.json'))
  assert.match(spec.openapi, /^3\.1\./); assert(Object.keys(spec.paths).length > 15)
})

test('help UI compiles, question mark navigates directly and content does not perform business requests', () => {
  const page = read('src/views/Help.vue'), { descriptor, errors } = parse(page)
  assert.deepEqual(errors, [])
  const script = compileScript(descriptor, { id: 'help-center' })
  const template = compileTemplate({ source: descriptor.template.content, filename: 'Help.vue', id: 'help-center', compilerOptions: { bindingMetadata: script.bindings } })
  assert.deepEqual(template.errors, [])
  assert.match(page, /v-html="rendered"/); assert.match(page, /renderHelpSection\(section.value, openAPIURL\)/)
  assert.match(page, /type="search" :?aria-label=/); assert.match(page, /download="devflow-openapi.json"/)
  assert(!/from ['"].*\/api['"]|\bfetch\(/.test(page))
  assert.match(page, /<div class="help-directory-toggle-row">\s*<button class="btn"/)
  assert.match(page, /\.help-directory-toggle-row\{display:none\}/)
  assert.match(page, /@media\(max-width:820px\)\{\.help-directory-toggle-row\{display:block;/)
  assert.match(read('src/App.vue'), /class="plain-icon help-entry" to="\/help"/)
  assert(!read('src/App.vue').includes('helpDialog'))
  assert.match(read('src/main.ts'), /path:'\/help\/:document\?'/)
})

console.log(`Passed ${count} help documentation checks (no business requests).`)

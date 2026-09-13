import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import ts from 'typescript'

const source = path => readFileSync(new URL('../' + path, import.meta.url), 'utf8')
const compile = text => ts.transpileModule(text, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText
let callback, disconnected = 0, loads = 0, observed
class FakeObserver {
  constructor(fn) { callback = fn }
  observe(element) { observed = element }
  disconnect() { disconnected++ }
}
const visible = {}
new Function('exports', 'IntersectionObserver', compile(source('src/visibleAsset.ts')))(visible, FakeObserver)
const element = {}
const stop = visible.observeVisibleAsset(element, () => loads++)
assert.equal(observed, element)
callback([{ target: element, isIntersecting: false, boundingClientRect: { width: 200 } }])
callback([{ target: element, isIntersecting: true, boundingClientRect: { width: 0 } }])
assert.equal(loads, 0, 'hidden/collapsed assets must not fetch')
callback([{ target: element, isIntersecting: true, boundingClientRect: { width: 200 } }])
callback([{ target: element, isIntersecting: true, boundingClientRect: { width: 200 } }])
assert.equal(loads, 1)
stop(); assert(disconnected >= 1)
const cancel = visible.observeVisibleAsset(element, () => loads++)
cancel(); callback([{ target: element, isIntersecting: true, boundingClientRect: { width: 200 } }]); assert.equal(loads, 1)

const api = {}, requests = [], events = []
let resolvers = []
const fetchMock = (path, options) => { requests.push({ path, options }); return new Promise(resolve => resolvers.push(resolve)) }
const resolveFile = () => resolvers.shift()(new Response(new Blob(['file'])))
new Function('require', 'exports', 'localStorage', 'window', 'fetch', compile(source('src/api.ts')))(
  () => ({ locale: { value: 'zh-CN' }, t: value => value }), api,
  { getItem: () => 'project-a' }, { dispatchEvent: event => events.push(event) }, fetchMock,
)
const a = api.apiDownload('/requirements/1/attachments/2'), b = api.apiDownload('/requirements/1/attachments/2')
assert.equal(requests.length, 1, 'same concurrent file read shares one request')
resolveFile(); assert.equal(await a, await b)
const c = api.apiDownload('/requirements/1/attachments/2'); assert.equal(requests.length, 2, 'completed private files are not kept in cache'); resolveFile(); await c
const d = api.apiDownload('/requirements/1/attachments/2', { headers: { 'X-TaskLoom-Project': 'project-b' } })
const e = api.apiDownload('/requirements/1/attachments/2', { headers: { 'X-TaskLoom-Project': 'project-c' } })
assert.equal(requests.length, 4, 'different project scopes never share a file')
resolveFile(); resolveFile(); await Promise.all([d, e])
const stale = api.apiDownload('/requirements/1/attachments/2')
const rejected = assert.rejects(stale, error => error.code === 'request_superseded')
const logout = api.api('/auth/logout', { method: 'POST' })
resolvers.pop()(Response.json({})); await logout; resolveFile(); await rejected

const requirements = source('src/views/Requirements.vue')
for (const name of ['Editor', 'RichTextEditor', 'MentionComment', 'TapdImport', 'RequirementAITestCases', 'DefectComposer']) {
  assert(requirements.includes(`const ${name} = defineAsyncComponent`), `${name} must load on demand`)
}
console.log('通过：隐藏附件不下载、可见时只请求一次、并发文件读取去重、项目/身份隔离、需求编辑依赖按需加载。')

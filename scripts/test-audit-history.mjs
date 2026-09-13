import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import ts from 'typescript'
import * as Vue from 'vue'
import { compileScript, compileStyle, compileTemplate, parse } from 'vue/compiler-sfc'

const read = path => readFileSync(new URL('../' + path, import.meta.url), 'utf8')
const source = read('src/views/AuditLog.vue')
const { descriptor, errors } = parse(source, { filename: 'AuditLog.vue' })
assert.deepEqual(errors, [])
const script = compileScript(descriptor, { id: 'audit-history' })
const template = compileTemplate({ source: descriptor.template.content, filename: 'AuditLog.vue', id: 'audit-history', compilerOptions: { bindingMetadata: script.bindings } })
assert.deepEqual(template.errors, [])
for (const style of descriptor.styles) assert.deepEqual(compileStyle({ source: style.content, filename: 'AuditLog.vue', id: 'audit-history', scoped: style.scoped }).errors, [])

function evaluate(sourceText, imports, globals) {
  const output = ts.transpileModule(sourceText, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText
  const exports = {}
  new Function('require', 'exports', ...Object.keys(globals), output)(id => { assert.ok(id in imports, `Unexpected import ${id}`); return imports[id] }, exports, ...Object.values(globals))
  return exports
}
const auditDictionary = evaluate(read('src/locales/audit.en.ts'), {}, {}).default
const auditLabels = evaluate(read('src/auditHistory.ts'), {}, {})
const flush = async () => { for (let index = 0; index < 16; index++) { await Promise.resolve(); await Vue.nextTick() } }
const example = (id, objectId = '7') => ({ id, actorId: 'u_admin', actorName: '林夏', objectType: 'requirement', objectId, action: 'requirement.updated', createdAt: '2026-09-04T10:00:00Z', changes: [{ field: '标题', before: '旧标题', after: '新标题' }], changesTrimmed: false })

async function mount(handler) {
  const calls = [], listeners = new Map(), mounts = [], unmounts = [], locale = Vue.ref('zh-CN'), scope = Vue.effectScope()
  const window = { addEventListener: (name, callback) => listeners.set(name, callback), removeEventListener: name => listeners.delete(name) }
  const imports = {
    vue: { ...Vue, onMounted: callback => mounts.push(callback), onBeforeUnmount: callback => unmounts.push(callback) },
    '../api': { api: async (path, options) => { calls.push({ path, options }); return handler(path, options) } },
    '../i18n': { locale, t: (value, params = {}) => String(value).replace(/\{(\w+)\}/g, (all, key) => String(params[key] ?? all)), formatDate: value => String(value || '') },
    '../components/AppSelect.vue': { default: Vue.defineComponent({ name: 'AppSelect', props: ['modelValue', 'options', 'label'], emits: ['update:modelValue'], setup: () => () => Vue.h('div') }) },
    '../auditHistory': auditLabels,
  }
  const component = evaluate(script.content, imports, { window }).default
  let context
  scope.run(() => { context = component.setup({}, { expose: () => {} }) })
  for (const callback of mounts) await callback()
  await flush()
  return { calls, context, listeners, stop: () => { for (const callback of unmounts) callback(); scope.stop() } }
}

let count = 0
async function test(name, run) { await run(); count++; console.log('✓ ' + name) }

await test('audit view compiles and exposes constrained project-history controls', () => {
  assert.match(source, /\/audit-logs\?\$\{query/)
  assert.match(source, /nextCursor/)
  assert.match(source, /new AbortController\(\)/)
  assert.match(source, /type="date"/)
  assert.match(source, /@submit\.prevent="submitFilters"/)
  assert.match(source, /objectLink\(item\)/)
  assert.match(source, /auditActionLabel\(item\.action, item\.objectType\)/)
  assert.match(source, /function objectLabel\(value: string\) \{ return t\(auditObjectLabel\(value\)\) \}/)
  assert.match(source, /auditFieldLabel\(value\)/)
  assert.match(source, /class="audit-trace"/)
  assert.doesNotMatch(source, /item\.actorId \|\| t\('系统事件'\)/)
  assert.doesNotMatch(source, /:title="item\.action"/)
  assert.match(source, /if \(!\/\^\[1-9\]\\d\*\$\//)
  assert.match(source, /@media\(max-width:760px\)/)
  assert.match(read('src/main.ts'), /path:'\/audit',component:AuditLog/)
  for (const match of source.matchAll(/\bt\((['"])([^'"\n]*)\1/g)) if (/[\u3400-\u9fff]/.test(match[2])) assert.ok(auditDictionary[match[2]], `AuditLog locale missing ${match[2]}`)
  for (const key of ['需求', '缺陷', '迭代', '测试用例', '测试计划', '测试执行', '项目', '成员', '自动化规则', '自定义字段', 'AI 服务配置', '需求分类', '需求状态', '需求工作流']) assert.ok(auditDictionary[key], `Audit object locale missing ${key}`)
})

await test('opaque audit action codes render as stable, readable operation names', () => {
  const label = auditLabels.auditActionLabel
  const cases = [
    ['requirement.description_saved', 'requirement', '保存需求正文'],
    ['impersonation_stopped', 'user', '结束代访问'],
    ['organization_member_saved', 'user', '保存成员资料'],
    ['release_notes_generate', 'sprint', '生成升级日志'],
    ['created', 'requirement', '创建需求'],
    ['unknown.internal_event', 'unknown', '系统操作'],
  ]
  for (const [action, objectType, expected] of cases) {
    assert.equal(label(action, objectType), expected)
    assert.ok(auditDictionary[expected], `audit action locale missing ${expected}`)
  }
})

await test('technical audit identifiers are converted to business labels and stay traceable only on demand', () => {
  assert.equal(auditLabels.auditObjectLabel('comment'), '评论')
  assert.equal(auditLabels.auditObjectLabel('requirement_attachment'), '需求附件')
  assert.equal(auditLabels.auditObjectLabel('wecom_custom_app'), '企业微信自建应用')
  assert.equal(auditLabels.auditObjectLabel('unknown_internal_type'), '其他记录')
  assert.equal(auditLabels.auditActionLabel('comment.rich_created', 'comment'), '创建评论')
  assert.equal(auditLabels.auditActionLabel('attachment.uploaded', 'requirement_attachment'), '上传附件')
  assert.equal(auditLabels.auditActionLabel('project.delete', 'project'), '删除项目')
  assert.equal(auditLabels.auditActorFallbackLabel('u_admin'), '管理员账户')
  assert.equal(auditLabels.auditActorFallbackLabel('admin-credentials-cli'), '系统维护任务')
  assert.equal(auditLabels.auditActorFallbackLabel('unmapped_account'), '未知操作成员')
  assert.equal(auditLabels.auditFieldLabel('requirementId'), '需求编号')
  assert.equal(auditLabels.auditFieldLabel('member.name'), '成员资料 · 名称')
  assert.equal(auditLabels.auditFieldLabel('internalProtocolField'), '配置项')
  for (const key of ['评论', '需求附件', '企业微信自建应用', '其他记录', '管理员账户', '系统维护任务', '未知操作成员', '查看追溯信息', '操作账号标识', '对象标识', '操作标识']) assert.ok(auditDictionary[key], `audit trace locale missing ${key}`)
})

await test('filters, stable cursor pagination, safe deep links and identity clearing work in the rendered view', async () => {
  const view = await mount(path => {
    const params = new URLSearchParams(path.split('?')[1])
    if (params.get('action') === 'fail') throw Error('not allowed')
    return params.get('cursor') === 'cursor-2' ? { items: [example(1, '8')] } : { items: [example(3), example(2)], nextCursor: 'cursor-2' }
  })
  assert.equal(view.calls[0].path, '/audit-logs?limit=25')
  assert.equal(view.context.items.value.length, 2)
  view.context.objectType.value = 'requirement'; view.context.action.value = 'requirement.updated'; view.context.actorId.value = 'u_admin'; view.context.from.value = '2026-09-01'; view.context.to.value = '2026-09-04'
  await view.context.submitFilters(); await flush()
  const filters = new URLSearchParams(view.calls.at(-1).path.split('?')[1])
  assert.equal(filters.get('objectType'), 'requirement'); assert.equal(filters.get('action'), 'requirement.updated'); assert.equal(filters.get('actorId'), 'u_admin'); assert.equal(filters.get('from'), '2026-09-01'); assert.equal(filters.get('to'), '2026-09-04')
  await view.context.load(true); await flush()
  assert.equal(view.context.items.value.length, 3); assert.equal(view.context.page.value, 2); assert.equal(view.calls.at(-1).path.includes('cursor=cursor-2'), true)
  assert.equal(view.context.objectLink(example(1, '7')), '/requirements?req=7')
  assert.equal(view.context.objectLink(example(1, '7?unsafe')), '')
  view.context.action.value = 'fail'; await view.context.submitFilters(); await flush(); assert.match(view.context.error.value, /not allowed/)
  view.listeners.get('devflow-identity-changed')(); assert.equal(view.context.items.value.length, 0); assert.equal(view.context.nextCursor.value, '')
  view.stop()
})

await test('identity changes abort in-flight audit reads and suppress stale records', async () => {
  let resolve, delayed = false
  const pending = new Promise(next => { resolve = next })
  const view = await mount(() => delayed ? pending : { items: [example(2)] })
  delayed = true
  const reading = view.context.load()
  const request = view.calls.at(-1)
  view.listeners.get('devflow-identity-changed')()
  assert.equal(request.options.signal.aborted, true)
  resolve({ items: [example(1)] })
  await reading; await flush()
  assert.equal(view.context.items.value.length, 0)
  assert.match(view.context.error.value, /账号身份已在其他页面切换/)
  view.stop()
})

console.log(`Passed ${count} audit history frontend regression tests.`)

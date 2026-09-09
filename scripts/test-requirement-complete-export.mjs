import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import ts from 'typescript'
import * as Vue from 'vue'
import { parse, compileScript, compileTemplate, compileStyle } from 'vue/compiler-sfc'

const read = path => readFileSync(new URL('../' + path, import.meta.url), 'utf8')
const source = read('src/components/WorkItemPdfExport.vue'), { descriptor } = parse(source)
const deferred = () => { let resolve, reject; const promise = new Promise((yes, no) => { resolve = yes; reject = no }); return { promise, resolve, reject } }
const payload = { schemaVersion: 1, requirement: { id: 42, title: '业务原文 {保留}', descriptionDoc: { type: 'doc', content: [{ type: 'paragraph', text: '完整正文' }] }, customFields: { unexpected: { nested: ['保留', 0, false] } } }, comments: [{ text: '完整评论' }], attachments: [{ name: '附件.png' }], links: [{ targetId: 4 }], history: [{ before: null, after: '保留' }] }
const jsonBlob = () => new Blob([JSON.stringify(payload, null, 2)], { type: 'application/json; charset=utf-8' })
const markdownBlob = () => new Blob(['# 需求正文\n\n<!-- 保留完整导出 -->\n\n| 字段 | 内容 |\n| --- | --- |\n| 零 | 0 |\n\n```json\n{"source":"保留"}\n```\n'], { type: 'text/markdown; charset=utf-8' })
const pdfBlob = () => new Blob(['%PDF-fixture'], { type: 'application/pdf' })
function fixture(handler = async () => jsonBlob(), extra = {}) {
  const effects = Vue.effectScope(), exports = {}, calls = [], downloads = [], unmounts = [], mounts = [], listeners = new Map(), locked = Vue.ref(false)
  const props = Vue.reactive({ objectType: 'requirement', objectId: 42, projectId: 'explicit-project', disabled: false, ...extra })
  const imports = {
    vue: { ...Vue, onBeforeUnmount: fn => unmounts.push(fn), onMounted: fn => mounts.push(fn) },
    '../api': { apiDownload: async (path, options) => { calls.push({ path, options }); return handler(path, options) } },
    '../i18n': { t: text => text }, '../workItemExport': { downloadFile: (blob, name) => downloads.push({ blob, name }) },
    './settingsScope': { useSettingsScope: () => ({ locked, current: () => !locked.value }) },
  }
  const window = { addEventListener: (event, fn) => listeners.set(event, fn), removeEventListener: event => listeners.delete(event) }
  const code = ts.transpileModule(descriptor.scriptSetup.content + '\nexport { busy, error, opened, activeFormat, invalidated, unavailable, exportFile, exportPDF, cancel }', { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText
  effects.run(() => new Function('require', 'exports', 'defineProps', 'window', code)(id => imports[id] || {}, exports, () => props, window))
  mounts.forEach(fn => fn())
  return { ...exports, props, calls, downloads, locked, listeners, stop() { unmounts.forEach(fn => fn()); effects.stop() } }
}
let count = 0
async function test(name, run) { await run(); count++; console.log('✓ ' + name) }

await test('JSON and Markdown use read-only dedicated routes, exact response bytes and backend-compatible filenames', async () => {
  for (const [format, extension, blob] of [['json', 'json', jsonBlob()], ['markdown', 'md', markdownBlob()]]) {
    const m = fixture(async () => blob); await m.exportFile(format)
    assert.equal(m.calls.length, 1); assert.equal(m.calls[0].path, `/requirements/42/export?format=${format}`)
    assert.equal(m.calls[0].options.headers['X-DevFlow-Project'], 'explicit-project'); assert.equal(m.calls[0].options.method, undefined); assert.equal(m.calls[0].options.body, undefined)
    assert.equal(m.downloads[0].name, `REQ-42-complete.${extension}`); assert.equal(m.downloads[0].blob, blob); assert.equal(await m.downloads[0].blob.text(), await blob.text())
    assert.equal(m.error.value, ''); assert.equal(m.busy.value, false); m.stop()
  }
})
await test('PDF retains original requirement and defect endpoints, padded filenames and binary download', async () => {
  for (const [objectType, prefix] of [['requirement', 'REQ'], ['defect', 'BUG']]) {
    const blob = pdfBlob(), m = fixture(async () => blob, { objectType }); await m.exportPDF()
    assert.equal(m.calls[0].path, `/exports/${objectType}/42.pdf`); assert.equal(m.downloads[0].name, `${prefix}-0042.pdf`); assert.equal(m.downloads[0].blob, blob); m.stop()
  }
})
await test('defects cannot call the new requirement-only formats, and unknown formats or invalid identities cannot issue requests', async () => {
  const defect = fixture(undefined, { objectType: 'defect' }); await defect.exportFile('json'); await defect.exportFile('markdown'); assert.deepEqual(defect.calls, []); defect.stop()
  for (const extra of [{ objectId: 0 }, { objectId: -1 }, { objectId: 1.5 }, { objectId: Number.MAX_SAFE_INTEGER + 1 }, { objectId: '42' }, { projectId: '' }, { projectId: ' ' }, { objectType: 'arbitrary' }]) {
    const m = fixture(undefined, extra); await m.exportFile('json'); assert.deepEqual(m.calls, []); m.stop()
  }
  const m = fixture(); await m.exportFile('csv'); assert.deepEqual(m.calls, []); m.stop()
})
await test('single in-flight generation prevents duplicate or competing formats and closes the menu', async () => {
  const pending = deferred(), m = fixture(() => pending.promise); m.opened.value = true; const first = m.exportFile('json')
  await m.exportFile('markdown'); await m.exportPDF(); assert.equal(m.calls.length, 1); assert.equal(m.busy.value, true); assert.equal(m.opened.value, false)
  pending.resolve(jsonBlob()); await first; assert.equal(m.downloads.length, 1); assert.equal(m.busy.value, false); m.stop()
})
await test('cancel aborts pending downloads and late success is ignored even when the transport does not reject', async () => {
  const pending = deferred(), m = fixture(() => pending.promise), first = m.exportFile('json'); const signal = m.calls[0].options.signal
  m.cancel(); assert(signal.aborted); assert.equal(m.busy.value, false); pending.resolve(jsonBlob()); await first; assert.deepEqual(m.downloads, []); assert.equal(m.error.value, ''); m.stop()
})
await test('new export after cancellation remains busy and uncontaminated when the old request completes', async () => {
  const old = deferred(), next = deferred(), m = fixture(path => path.endsWith('json') ? old.promise : next.promise)
  const first = m.exportFile('json'); m.cancel(); const second = m.exportFile('markdown'); old.reject(Error('old request failed')); await first
  assert.equal(m.busy.value, true); assert.equal(m.activeFormat.value, 'markdown'); assert.equal(m.error.value, '')
  next.resolve(markdownBlob()); await second; assert.equal(m.downloads.length, 1); assert.equal(m.downloads[0].name, 'REQ-42-complete.md'); m.stop()
})
await test('switching requirement, type, or target project discards old response and old errors', async () => {
  for (const change of [props => props.objectId = 43, props => props.objectType = 'defect', props => props.projectId = 'other-project']) {
    for (const reject of [false, true]) {
      const pending = deferred(), m = fixture(() => pending.promise), first = m.exportFile('json'); change(m.props)
      assert(m.calls[0].options.signal.aborted); if (reject) pending.reject(Error('wrong scope')); else pending.resolve(jsonBlob())
      await first; assert.deepEqual(m.downloads, []); assert.equal(m.error.value, ''); assert.equal(m.busy.value, false); m.stop()
    }
  }
})
await test('A to B to A navigation never revives an old download with an identical final object ID', async () => {
  const pending = deferred(), m = fixture(() => pending.promise), first = m.exportFile('json'); m.props.objectId = 99; m.props.objectId = 42
  pending.resolve(jsonBlob()); await first; assert.deepEqual(m.downloads, []); m.stop()
})
await test('verified account or project invalidation aborts pending reads and permanently locks this export instance', async () => {
  const pending = deferred(), m = fixture(() => pending.promise), first = m.exportFile('json'); m.locked.value = true
  assert(m.calls[0].options.signal.aborted); assert.equal(m.invalidated.value, true); pending.resolve(jsonBlob()); await first; m.locked.value = false; await m.exportFile('json')
  assert.deepEqual(m.downloads, []); assert.equal(m.calls.length, 1); m.stop()
})
await test('mandatory password change and parent disabled state cancel safely without unexpected file downloads', async () => {
  for (const stop of [m => m.listeners.get('devflow-password-change-required')(), m => m.props.disabled = true]) {
    const pending = deferred(), m = fixture(() => pending.promise), first = m.exportFile('json'); stop(m); assert(m.calls[0].options.signal.aborted)
    pending.resolve(jsonBlob()); await first; assert.deepEqual(m.downloads, []); await m.exportFile('json'); assert.equal(m.calls.length, 1); m.stop()
  }
})
await test('closed editors abort requests and cannot download files or surface late errors', async () => {
  const pending = deferred(), m = fixture(() => pending.promise), first = m.exportFile('json'); m.stop(); assert(m.calls[0].options.signal.aborted)
  pending.reject(Error('late failure')); await first; assert.deepEqual(m.downloads, []); assert.equal(m.error.value, ''); assert.equal(m.listeners.size, 0)
})
await test('wrong MIME types and empty responses fail visibly without downloading error documents', async () => {
  for (const [format, mime] of [['json', 'text/html'], ['markdown', 'application/json'], ['pdf', 'application/pdf-wrong'], ['json', 'application/json']]) {
    const blob = new Blob(format === 'json' && mime === 'application/json' ? [] : ['unexpected error'], { type: mime }), m = fixture(async () => blob)
    await m.exportFile(format); assert.deepEqual(m.downloads, []); assert(m.error.value); assert.equal(m.busy.value, false); m.stop()
  }
})
await test('authorization, missing object, export budget and storage failures retain meaningful errors and never retry automatically', async () => {
  for (const message of ['无权访问项目', '资源不存在', '完整导出超过限制', '数据库暂时不可用']) {
    const m = fixture(async () => { throw Error(message) }); await m.exportFile('json'); assert.equal(m.error.value, message); assert.equal(m.calls.length, 1); assert.deepEqual(m.downloads, []); assert.equal(m.busy.value, false); m.stop()
  }
})
await test('read-only viewers have no frontend write-permission dependency; disabled parents still prevent export', async () => {
  const viewer = fixture(); await viewer.exportFile('json'); assert.equal(viewer.downloads.length, 1); viewer.stop()
  const blocked = fixture(undefined, { disabled: true }); await blocked.exportFile('json'); assert.deepEqual(blocked.calls, []); blocked.stop()
  assert.doesNotMatch(source, /canEdit|canWrite|members\.manage/)
})
await test('shared detail and editor controls expose all saved requirements without changing list CSV or inventing unsaved requirements', () => {
  const detail = read('src/views/Requirements.vue'), editor = read('src/views/Editor.vue'), sprint = read('src/views/Sprints.vue'), list = read('src/components/RequirementListExport.vue')
  assert.match(detail, /<WorkItemPdfExport[^>]+object-type="requirement"[^>]+:object-id="selected.id"[^>]+:project-id="session.project.id"/)
  assert.match(sprint, /<Requirements[^>]+detail-only/); assert.match(detail, /<Editor[^>]+embedded[^>]+:requirement-id="fullEditorID"/)
  assert.match(editor, /<WorkItemPdfExport v-if="edit&&initialized&&!loading&&loadedProject"[^>]+:object-id="Number\(editingID\)"[^>]+:project-id="loadedProject"/)
  assert.match(list, /text\/csv/); assert.doesNotMatch(list, /export\?format=|complete\.json|complete\.md/)
})
await test('popover menus and updated editor compile with non-submit controls, accessible labels, saved-version explanation and responsive styling', () => {
  for (const filename of ['src/components/WorkItemPdfExport.vue', 'src/views/Editor.vue']) {
    const { descriptor } = parse(read(filename)), script = compileScript(descriptor, { id: 'complete-export' })
    assert.deepEqual(compileTemplate({ id: 'complete-export', filename, source: descriptor.template.content, compilerOptions: { bindingMetadata: script.bindings } }).errors, [])
    assert.deepEqual(compileStyle({ id: 'complete-export', filename, source: descriptor.styles.map(style => style.content).join('\n'), scoped: true }).errors, [])
  }
  assert.match(source, /PopoverTrigger as-child/); assert.match(source, /导出已保存版本，不包含未保存的修改/); assert.match(source, /role="alert"/); assert.match(source, /type="button" @click\.stop="exportFile\('json'\)"/); assert.match(source, /type="button" @click\.stop="exportFile\('markdown'\)"/); assert.match(source, /@media\(max-width:640px\)/)
})
console.log(`Passed ${count} complete requirement export UI tests.`)

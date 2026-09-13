import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import ts from 'typescript'
import * as Vue from 'vue'
import { parse, compileScript, compileTemplate } from 'vue/compiler-sfc'

const read = path => readFileSync(new URL('../' + path, import.meta.url), 'utf8')
function evaluate(source, imports = {}, extras = {}) {
  const exports = {}, code = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText
  new Function('require', 'exports', ...Object.keys(extras), code)(id => imports[id] || {}, exports, ...Object.values(extras))
  return exports
}
const rich = evaluate(read('src/richText.ts'), { './mentions': evaluate(read('src/mentions.ts')) })
const imageSource = read('src/components/ImagePreview.vue'), resourceSource = read('src/components/RequirementResources.vue'), assetSource = read('src/components/RichTextAsset.vue')
const raw = source => source.match(/<script setup[^>]*>([\s\S]*?)<\/script>/)[1]
const pngData = 'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+j9r0AAAAASUVORK5CYII='
const pngBlob = () => new Blob([rich.base64ToBytes(pngData)], { type: 'image/png' })
const flush = async () => { for (let i = 0; i < 12; i++) await Vue.nextTick() }
const deferred = () => { let resolve, reject; const promise = new Promise((yes, no) => { resolve = yes; reject = no }); return { promise, resolve, reject } }
const items = [{ key: 'a1', name: '一.png', attachmentId: 1 }, { key: 'a2', name: '二.png', attachmentId: 2 }]
let count = 0
async function test(name, run) { await run(); count++; console.log('✓ ' + name) }

async function preview(input = {}, download = async () => pngBlob()) {
  const props = Vue.reactive({ open: true, items: [...items], requirementId: 9, ...input }), calls = [], events = [], urls = [], revocations = [], mounts = [], unmounts = [], listeners = new Map(), observers = [], scope = Vue.effectScope()
  let protectedAncestor = false, focused = 0, created = 0, value
  class Element { isConnected = true; closest() { return protectedAncestor ? {} : null }; focus() { focused++ } }
  const priorFocus = new Element(), native = { open: false, parentElement: new Element(), showModal() { this.open = true; events.push(['showModal']) }, close() { this.open = false; events.push(['nativeClose']) } }
  const windowMock = { addEventListener: (key, fn) => listeners.set(key, fn), removeEventListener: (key, fn) => { if (listeners.get(key) === fn) listeners.delete(key) } }
  const imports = { vue: { ...Vue, onMounted: fn => mounts.push(fn), onBeforeUnmount: fn => unmounts.push(fn) }, '../api': { apiDownload: path => { calls.push(path); return download(path) } }, '../i18n': { t: source => source }, '../richText': rich }
  const expose = 'dialog,closeButton,shown,index,imageURL,loading,error,step,onKeydown,onCancel,close,show,loadCurrent,checkProtection,imageFailed'
  scope.run(() => { value = evaluate(raw(imageSource) + '\nexport {' + expose + '}', imports, {
    defineProps: () => props, defineEmits: () => (...args) => { events.push(args); if (args[0] === 'update:open') props.open = args[1] },
    URL: { createObjectURL: blob => { const url = 'blob:preview-' + (++created); urls.push({ url, blob }); return url }, revokeObjectURL: url => revocations.push(url) },
    HTMLElement: Element, document: { activeElement: priorFocus, documentElement: {} }, window: windowMock,
    MutationObserver: class { constructor(fn) { this.fn = fn; observers.push(this) }; observe() {}; disconnect() { this.disconnected = true } },
  }) })
  value.dialog.value = native; value.closeButton.value = new Element()
  for (const mount of mounts) mount()
  await flush()
  return { ...value, props, calls, events, urls, revocations, native, observers, listeners, get focused() { return focused },
    protect() { protectedAncestor = true; for (const observer of observers) observer.fn() }, dispatch: name => listeners.get(name)?.(),
    stop() { for (const unmount of unmounts) unmount(); scope.stop() },
  }
}

await test('native modal fetches authenticated image bytes and releases its own URL on close', async () => {
  const p = await preview(); assert.equal(p.native.open, true); assert.equal(p.shown.value, true); assert.deepEqual(p.calls, ['/requirements/9/attachments/1'])
  assert.match(p.imageURL.value, /^blob:/); assert.equal(p.urls[0].blob.type, 'image/png'); const url = p.imageURL.value
  p.close(); await flush(); assert.equal(p.native.open, false); assert.equal(p.props.open, false); assert.ok(p.revocations.includes(url)); assert.ok(p.focused >= 2); p.stop()
})
await test('left and right keys navigate only this gallery; Escape cannot reach the detail close handler', async () => {
  const p = await preview(), event = key => ({ key, prevented: false, stopped: false, immediate: false, preventDefault() { this.prevented = true }, stopPropagation() { this.stopped = true }, stopImmediatePropagation() { this.immediate = true } })
  const right = event('ArrowRight'); p.onKeydown(right); await flush(); assert.equal(p.index.value, 1); assert.equal(right.immediate, true); assert.equal(p.calls.at(-1), '/requirements/9/attachments/2')
  const number = p.calls.length; p.step(1); await flush(); assert.equal(p.calls.length, number, 'last image does not wrap or reload')
  p.onKeydown(event('ArrowLeft')); await flush(); assert.equal(p.index.value, 0)
  const esc = event('Escape'); p.onKeydown(esc); assert.equal(esc.prevented && esc.stopped && esc.immediate, true); assert.equal(p.native.open, false); assert.deepEqual(p.events.filter(e => e[0] === 'update:open'), [['update:open', false]]); p.stop()
})
await test('native cancel prevents browser defaults and closes exactly the preview', async () => {
  const p = await preview(); let prevented = 0, immediate = 0
  p.onCancel({ preventDefault: () => prevented++, stopImmediatePropagation: () => immediate++ }); await flush()
  assert.equal(prevented, 1); assert.equal(immediate, 1); assert.equal(p.shown.value, false); assert.equal(p.events.filter(e => e[0] === 'update:open').length, 1); p.stop()
})
await test('pending draft and already authorized blobs preview locally without any network path', async () => {
  for (const item of [{ key: 'pending', name: '草稿.png', data: pngData }, { key: 'authorized', name: '原图.png', blob: pngBlob() }]) {
    const p = await preview({ items: [item], requirementId: undefined }); assert.equal(p.calls.length, 0); assert.match(p.imageURL.value, /^blob:/); p.stop(); assert.equal(p.revocations.length, 1)
  }
})
await test('SVG, external URL-only items and oversized images never gain an object URL', async () => {
  const invalid = await preview({}, async () => new Blob(['<svg onload="alert(1)"/>'], { type: 'image/png' })); assert.match(invalid.error.value, /不是可预览/); assert.equal(invalid.urls.length, 0); invalid.stop()
  const external = await preview({ items: [{ key: 'url', name: 'unsafe.png', url: 'https://external.invalid/image.png' }], requirementId: undefined }); assert.equal(external.calls.length, 0); assert.equal(external.urls.length, 0); assert.match(external.error.value, /附件暂不可用/); external.stop()
  const huge = await preview({}, async () => new Blob([new Uint8Array(rich.MAX_RICH_FILE_BYTES + 1)])); assert.match(huge.error.value, /10 MiB/); assert.equal(huge.urls.length, 0); huge.stop()
})
await test('out-of-order gallery downloads cannot replace the selected image', async () => {
  const first = deferred(), p = await preview({}, path => path.endsWith('/1') ? first.promise : Promise.resolve(pngBlob()))
  assert.equal(p.loading.value, true); p.step(1); await flush(); const current = p.imageURL.value
  first.resolve(pngBlob()); await flush(); assert.equal(p.imageURL.value, current); assert.equal(p.urls.length, 1); p.stop()
})
await test('requirement changes, closing and unmounting suppress all late image responses', async () => {
  for (const action of ['requirement', 'close', 'unmount']) {
    const late = deferred(), p = await preview({}, () => late.promise)
    if (action === 'requirement') { p.props.requirementId = 10; await flush() } else if (action === 'close') p.close(); else p.stop()
    late.resolve(pngBlob()); await flush(); assert.equal(p.urls.length, 0, action); assert.equal(p.native.open, false, action)
    if (action !== 'unmount') p.stop()
  }
})
await test('identity, expired authentication and inherited inert protection close top-layer images', async () => {
  for (const protection of ['devflow-identity-changed', 'devflow-auth-expired', 'inert']) {
    const p = await preview(); const url = p.imageURL.value
    if (protection === 'inert') p.protect(); else p.dispatch(protection)
    await flush(); assert.equal(p.native.open, false, protection); assert.equal(p.props.open, false); assert.ok(p.revocations.includes(url)); p.stop(); assert.equal(p.listeners.size, 0); assert.equal(p.observers[0].disconnected, true)
  }
})
await test('load failures have retry state and decoding failures revoke their image', async () => {
  let failing = true; const p = await preview({}, async () => { if (failing) throw Error('offline'); return pngBlob() })
  assert.equal(p.error.value, 'offline'); assert.equal(p.loading.value, false); failing = false; await p.loadCurrent(); assert.equal(p.error.value, ''); assert.match(p.imageURL.value, /^blob:/)
  const url = p.imageURL.value; p.imageFailed(); assert.match(p.error.value, /无法解码/); assert.ok(p.revocations.includes(url)); p.stop()
})
await test('gallery refresh retains the selected stable key and closes when that image is removed', async () => {
  const p = await preview({ initialIndex: 1 }); assert.equal(p.index.value, 1)
  p.props.items = [items[1], items[0]]; await flush(); assert.equal(p.index.value, 0); assert.equal(p.calls.at(-1), '/requirements/9/attachments/2')
  p.props.items = [items[0]]; await flush(); assert.equal(p.native.open, false); p.stop()
})
await test('attachment gallery includes image MIME types and legacy names without changing Figma drafts', async () => {
  const props = Vue.reactive({ requirementId: 9, canEdit: false, modelValue: { url: 'https://figma.com/design/a', title: '草稿', opened: true } }), scope = Vue.effectScope(), unmounts = []
  let r
  scope.run(() => { r = evaluate(raw(resourceSource) + '\nexport {attachments,previewItems,previewOpen,previewIndex,openPreview,loading}', {
    vue: { ...Vue, onBeforeUnmount: fn => unmounts.push(fn) }, '../api': { api: async () => ({ items: [] }) }, '../i18n': { t: value => value },
  }, { defineProps: () => props, defineEmits: () => () => {} }) })
  await flush(); r.attachments.value = [{ id: 1, name: 'Screenshot', contentType: 'image/png' }, { id: 2, name: 'Legacy.JPG' }, { id: 3, name: 'Spec.pdf' }, { id: 4, name: 'unsafe.svg', contentType: 'image/svg+xml' }]
  assert.deepEqual(r.previewItems.value.map(item => item.attachmentId), [1, 2]); r.openPreview(r.attachments.value[1]); assert.equal(r.previewOpen.value, true); assert.equal(r.previewIndex.value, 1)
  assert.equal(props.modelValue.title, '草稿'); props.requirementId = 10; await flush(); assert.equal(r.previewOpen.value, false)
  for (const unmount of unmounts) unmount(); scope.stop()
})
await test('rich asset waits for visibility, reuses its authorized gallery blob and cleans up when its scope changes', async () => {
  const props = Vue.reactive({ node: { type: { name: 'image' }, attrs: { attachmentId: 2, name: '正文.png' } }, getPos: () => 7, editor: { state: { doc: { descendants(fn) {
    fn({ type: { name: 'image' }, attrs: { attachmentId: 1, name: '第一张.png' } }, 1)
    fn({ type: { name: 'attachment' }, attrs: { attachmentId: 4, name: '文档.pdf' } }, 4)
    fn(props.node, 7)
  } } } } }), state = Vue.reactive({ requirementId: 9, readonly: true, disabled: false }), scope = Vue.effectScope(), mounts = [], unmounts = [], observers = [], calls = [], metadataCalls = [], urls = [], revocations = [], listeners = new Map()
  const anchor = Vue.markRaw({}), visibleAsset = evaluate(read('src/visibleAsset.ts'), {}, {
    IntersectionObserver: class {
      constructor(callback) { this.callback = callback; this.disconnected = false; observers.push(this) }
      observe(target) { this.target = target }
      disconnect() { this.disconnected = true }
      emit(isIntersecting, width = 100) { this.callback([{ target: this.target, isIntersecting, boundingClientRect: { width } }]) }
    },
  })
  let a
  scope.run(() => { a = evaluate(raw(assetSource) + '\nexport {openPreview,previewItems,previewIndex,previewOpen,imageURL,visibilityAnchor}', {
    vue: { ...Vue, inject: () => Vue.computed(() => state), onMounted: fn => mounts.push(fn), onBeforeUnmount: fn => unmounts.push(fn) }, '@tiptap/vue-3': { nodeViewProps: {} },
    '../api': { apiDownload: async path => { calls.push(path); return pngBlob() }, api: async path => { metadataCalls.push(path); return { category: 'image' } } },
    '../i18n': { t: value => value }, '../richText': rich, '../visibleAsset': visibleAsset,
  }, {
    defineProps: () => props,
    URL: { createObjectURL: blob => { const url = 'blob:authorized-' + (urls.length + 1); urls.push({ url, blob }); return url }, revokeObjectURL: url => revocations.push(url) },
    window: { addEventListener: (name, fn) => listeners.set(name, fn), removeEventListener: (name, fn) => { if (listeners.get(name) === fn) listeners.delete(name) } },
  }) })
  a.visibilityAnchor.value = anchor; for (const mount of mounts) mount()
  await flush(); assert.equal(observers.length, 1); assert.equal(observers[0].target, anchor); assert.deepEqual(calls, []); assert.deepEqual(metadataCalls, [])
  observers[0].emit(false); await flush(); assert.deepEqual(calls, []); assert.deepEqual(metadataCalls, [])
  observers[0].emit(true, 0); await flush(); assert.deepEqual(calls, []); assert.deepEqual(metadataCalls, []); assert.equal(a.imageURL.value, ''); assert.equal(observers[0].disconnected, false)
  a.openPreview(); assert.equal(a.previewOpen.value, false)
  observers[0].emit(true); await flush(); assert.deepEqual(calls, ['/requirements/9/attachments/2']); assert.deepEqual(metadataCalls, ['/requirements/9/attachments/2?metadata=1']); assert.equal(observers[0].disconnected, true)
  a.openPreview(); assert.equal(a.previewOpen.value, true); assert.equal(a.previewIndex.value, 1); assert.deepEqual(a.previewItems.value.map(item => item.attachmentId), [1, 2]); assert.equal(a.previewItems.value[0].blob, undefined); assert.equal(a.previewItems.value[1].blob, urls[0].blob)
  const gallery = await preview({ items: a.previewItems.value, initialIndex: a.previewIndex.value }); assert.deepEqual(gallery.calls, []); assert.match(gallery.imageURL.value, /^blob:/); gallery.stop()
  observers[0].emit(true); await flush(); assert.equal(calls.length, 1)
  const previousURL = a.imageURL.value
  state.requirementId = 10; await flush(); assert.equal(a.previewOpen.value, false); assert.equal(a.imageURL.value, ''); assert.deepEqual(revocations, [previousURL]); assert.equal(observers.length, 2); assert.equal(observers[1].target, anchor); assert.equal(observers[1].disconnected, false); assert.equal(calls.length, 1); assert.equal(metadataCalls.length, 1)
  observers[0].emit(true); observers[1].emit(false); observers[1].emit(true, 0); await flush(); assert.equal(calls.length, 1); assert.equal(metadataCalls.length, 1)
  for (const unmount of unmounts) unmount(); scope.stop(); assert.equal(observers.every(observer => observer.disconnected), true); assert.equal(listeners.size, 0)
  observers[1].emit(true); await flush(); assert.equal(calls.length, 1); assert.equal(metadataCalls.length, 1)
})
await test('preview Vue template compiles with native modal and explicit non-submit controls', () => {
  for (const [filename, content] of [['ImagePreview.vue', imageSource], ['RequirementResources.vue', resourceSource], ['RichTextAsset.vue', assetSource]]) {
    const { descriptor } = parse(content), script = compileScript(descriptor, { id: filename }), template = compileTemplate({ source: descriptor.template.content, filename, id: filename, compilerOptions: { bindingMetadata: script.bindings } })
    assert.deepEqual(template.errors, [], filename)
  }
  assert.match(imageSource, /<dialog\b/); assert.equal(imageSource.includes('<Teleport'), false); assert.match(imageSource, /@click\.self="close"/)
  for (const button of imageSource.matchAll(/<button\b[^>]*>/g)) assert.match(button[0], /type="button"/)
})
console.log(`Passed ${count} image preview tests.`)

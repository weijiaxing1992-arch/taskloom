import assert from 'node:assert/strict'
import { readFileSync, existsSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import ts from 'typescript'

const root = resolve(new URL('..', import.meta.url).pathname)
const read = path => readFileSync(resolve(root, path), 'utf8')
const tick = () => new Promise(resolve => setImmediate(resolve))
let passed = 0
async function test(name, run) { await run(); passed++; console.log(`✓ ${name}`) }
function moduleFrom(path, imports = {}, globals = {}, extra = '') {
  const code = ts.transpileModule(read(path) + extra, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022, jsx: ts.JsxEmit.ReactJSX } }).outputText
  const exports = {}
  new Function('require', 'exports', ...Object.keys(globals), code)(name => {
    if (!(name in imports)) throw new Error(`Unexpected import: ${name}`)
    return imports[name]
  }, exports, ...Object.values(globals))
  return exports
}

// Run actual component/hook logic with controlled effects, requests, and browser events.
function harness() {
  const slots = [], effects = [], layouts = [], timers = new Map(), listeners = new Map(), calls = []
  let cursor = 0, dirty = false, nextTimer = 0, component
  const changed = (before, after) => !before || !after || before.length !== after.length || before.some((value, index) => !Object.is(value, after[index]))
  const effect = queue => (setup, deps) => {
    const slot = slots[cursor++] ||= {}
    if (changed(slot.deps, deps)) { slot.deps = deps; queue.push(() => { slot.cleanup?.(); slot.cleanup = setup() }) }
  }
  const React = {
    useState(initial) {
      const slot = slots[cursor++] ||= { value: typeof initial === 'function' ? initial() : initial }
      return [slot.value, next => { const value = typeof next === 'function' ? next(slot.value) : next; if (!Object.is(value, slot.value)) { slot.value = value; dirty = true } }]
    },
    useRef(initial) { return (slots[cursor++] ||= { current: initial }) },
    useMemo(calculate, deps) { const slot = slots[cursor++] ||= {}; if (changed(slot.deps, deps)) { slot.deps = deps; slot.value = calculate() } return slot.value },
    useCallback(callback, deps) { return this.useMemo(callback, deps) },
    useEffect: effect(effects), useLayoutEffect: effect(layouts),
  }
  React.useCallback = (callback, deps) => React.useMemo(() => callback, deps)
  const window = {
    scrollY: 0,
    scrollTo(_x, y) { this.scrollY = y },
    setTimeout(fn) { timers.set(++nextTimer, fn); return nextTimer },
    clearTimeout(id) { timers.delete(id) },
    addEventListener(name, fn) { if (!listeners.has(name)) listeners.set(name, new Set()); listeners.get(name).add(fn) },
    removeEventListener(name, fn) { listeners.get(name)?.delete(fn) },
  }
  const api = (path, options) => new Promise((resolve, reject) => calls.push({ path, options, resolve, reject }))
  const jsx = (type, props, key) => ({ type, props: props || {}, key })
  const jsxRuntime = { jsx, jsxs: jsx, Fragment: 'fragment' }
  function walk(node, callback) { if (!node || typeof node !== 'object') return; if (Array.isArray(node)) return node.forEach(item => walk(item, callback)); callback(node); walk(node.props?.children, callback) }
  return {
    React, window, api, calls, jsxRuntime, walk,
    render(next) {
      if (next) component = next
      let tree, cycles = 0
      do {
        dirty = false; cursor = 0; tree = component()
        walk(tree, node => { if (node.props?.ref) node.props.ref.current ||= { focus() {} } })
        layouts.splice(0).forEach(run => run()); effects.splice(0).forEach(run => run())
        assert(++cycles < 25, 'component should settle')
      } while (dirty)
      return tree
    },
    flush() { const pending = [...timers.values()]; timers.clear(); pending.forEach(run => run()) },
    scroll(y) { window.scrollY = y; listeners.get('scroll')?.forEach(run => run()) },
    stop() { slots.forEach(slot => slot.cleanup?.()); timers.clear() },
    find(tree, predicate) { const found = []; walk(tree, node => { if (predicate(node)) found.push(node) }); return found },
  }
}

await test('four-tab entry and plain reading do not eagerly import Markdown or code-highlighting engines', () => {
  function dependencies(entry) {
    const found = new Set()
    function visit(path) {
      if (found.has(path)) return
      found.add(path)
      const file = ts.createSourceFile(path, readFileSync(path, 'utf8'), ts.ScriptTarget.Latest, true)
      for (const statement of file.statements) {
        if (!ts.isImportDeclaration(statement) || statement.importClause?.isTypeOnly) continue
        const specifier = statement.moduleSpecifier.text
        if (!specifier.startsWith('.')) { found.add(specifier); continue }
        const base = resolve(dirname(path), specifier)
        const target = [base, `${base}.ts`, `${base}.tsx`].find(candidate => existsSync(candidate) && /\.(?:ts|tsx)$/.test(candidate))
        if (target) visit(target)
      }
    }
    visit(resolve(root, entry)); return [...found].join('\n')
  }
  const firstPaint = dependencies('src/mobile-react/main.tsx')
  assert.doesNotMatch(firstPaint, /MobileContent|MobileDetail|MobileCodeBlock|markdownImport|codeHighlight|lowlight|marked/)
  const reading = dependencies('src/mobile-react/MobileContent.tsx')
  assert.match(reading, /markdownImport/); assert.doesNotMatch(reading, /codeHighlight|lowlight/)
})

await test('unread refresh shares concurrent calls and rejects stale pre-mutation responses', async () => {
  const { createUnreadCounter } = moduleFrom('src/mobile-react/mobileRequests.ts')
  const requests = [], values = []
  const counter = createUnreadCounter(signal => new Promise((resolve, reject) => requests.push({ signal, resolve, reject })), value => values.push(value))
  const first = counter.refresh(), concurrent = counter.refresh()
  assert.equal(first, concurrent); assert.equal(requests.length, 1)
  counter.commit(2); assert.equal(requests[0].signal.aborted, true)
  const fresh = counter.refresh(); assert.equal(requests.length, 2)
  requests[0].resolve({ unread: 20 }); await first
  assert.deepEqual(values, [2])
  assert.equal(counter.refresh(), fresh, 'old completion must not clear the newer pending request')
  requests[1].resolve({ unread: 3 }); await fresh
  assert.deepEqual(values, [2, 3])
  const failed = counter.refresh(); requests[2].reject(Error('offline')); await assert.rejects(failed, /offline/)
  const retry = counter.refresh(); requests[3].resolve({ unread: 0 }); await retry
  const hidden = counter.refresh(); counter.invalidate(); requests[4].resolve({ unread: 10 }); await hidden
  assert.deepEqual(values, [2, 3, 0])
})

await test('inactive tabs abort reads and never dispatch queued searches or apply late results', async () => {
  const h = harness()
  const { useMobileQuery } = moduleFrom('src/mobile-react/useMobilePage.ts', { react: h.React, './mobileApi': { mobileAPI: h.api } }, { window: h.window })
  let path = '/requirements?q=old', active = true, result
  const render = () => h.render(() => (result = useMobileQuery(path, active, 'project-other', 180)))
  render(); h.flush(); assert.equal(h.calls.length, 1)
  assert.equal(h.calls[0].options.headers['X-TaskLoom-Project'], 'project-other')
  path = '/requirements?q=new'; render()
  assert.equal(h.calls[0].options.signal.aborted, true)
  h.calls[0].resolve({ items: ['old'] }); await tick(); render(); assert.equal(result.data, null)
  active = false; render(); h.flush(); assert.equal(h.calls.length, 1)
  active = true; render(); h.flush(); assert.equal(h.calls.length, 2)
  h.calls[1].resolve({ items: ['new'] }); await tick(); render(); assert.deepEqual(result.data.items, ['new'])
  result.reload(); render(); h.flush(); active = false; render()
  assert.equal(h.calls[2].options.signal.aborted, true)
  h.calls[2].resolve({ items: ['late'] }); await tick(); render(); assert.deepEqual(result.data.items, ['new'])
  active = true; render(); h.flush(); h.calls[3].reject(Error('权限已撤销')); await tick(); render()
  assert.equal(result.data, null); assert.equal(result.error, '权限已撤销')
  h.stop()
})

await test('tab return restores its own scroll position only after its list is ready', () => {
  const h = harness()
  const { useMobilePageScroll } = moduleFrom('src/mobile-react/useMobilePage.ts', { react: h.React, './mobileApi': { mobileAPI: h.api } }, { window: h.window })
  let active = true, loading = false
  const render = () => h.render(() => useMobilePageScroll(active, loading))
  render(); h.scroll(640)
  active = false; render(); h.scroll(120)
  active = true; loading = true; render(); assert.equal(h.window.scrollY, 120)
  loading = false; render(); assert.equal(h.window.scrollY, 640)
  h.stop()
})

await test('requirements keep search and filters across tab switches and recheck access on return', async () => {
  const h = harness()
  const hooks = moduleFrom('src/mobile-react/useMobilePage.ts', { react: h.React, './mobileApi': { mobileAPI: h.api } }, { window: h.window })
  const { MobileRequirements } = moduleFrom('src/mobile-react/MobileLists.tsx', { react: h.React, 'react/jsx-runtime': h.jsxRuntime, './MobilePrimitives': {}, './icons': {}, './useMobilePage': hooks })
  let active = true
  const render = () => h.render(() => MobileRequirements({ projectId: 'cross-project', active, onOpen() {} }))
  let tree = render(); h.flush(); h.calls[0].resolve({ items: [], total: 0 }); await tick(); tree = render()
  h.find(tree, node => node.props.label === '搜索需求、人员或部门')[0].props.onChange('登录')
  tree = render(); h.find(tree, node => node.type === 'button' && node.props.children === '与我相关')[0].props.onClick()
  tree = render(); active = false; render(); h.flush(); assert.equal(h.calls.length, 1)
  active = true; tree = render(); h.flush()
  assert.equal(h.find(tree, node => node.props.label === '搜索需求、人员或部门')[0].props.value, '登录')
  const params = new URLSearchParams(h.calls[1].path.split('?')[1])
  assert.equal(params.get('q'), '登录'); assert.equal(params.get('mine'), '1')
  assert.equal(h.calls[1].options.headers['X-TaskLoom-Project'], 'cross-project')
  h.stop()
})

await test('files wait for a click, images wait for the viewport, and unmount aborts/revokes their resources', async () => {
  const h = harness(), blobs = [], observers = [], revoked = [], downloads = []
  class Observer {
    constructor(callback) { this.callback = callback; observers.push(this) }
    observe(element) { this.element = element }
    disconnect() { this.disconnected = true }
  }
  const { AttachmentResource } = moduleFrom('src/mobile-react/MobileAttachment.tsx', { react: h.React, 'react/jsx-runtime': h.jsxRuntime, './mobileApi': { mobileBlob: (path, projectId, signal) => new Promise(resolve => blobs.push({ path, projectId, signal, resolve })) } }, {
    IntersectionObserver: Observer,
    URL: { createObjectURL: () => 'blob:verified', revokeObjectURL: value => revoked.push(value) },
    document: { body: { appendChild() {} }, createElement: () => ({ click() { downloads.push(this.href) }, remove() {} }) },
  }, '\nexport { AttachmentResource }')
  let tree = h.render(() => AttachmentResource({ id: 7, requirementId: 23, projectId: 'other', name: '设计.pdf' }))
  assert.equal(blobs.length, 0); assert.equal(observers.length, 0)
  h.find(tree, node => node.type === 'button' && node.props.children === '下载')[0].props.onClick()
  assert.equal(blobs.length, 1); assert.equal(blobs[0].projectId, 'other'); assert.match(blobs[0].path, /requirements\/23\/attachments\/7/)
  blobs[0].resolve(new Blob(['file'])); await tick(); tree = h.render()
  assert.deepEqual(downloads, ['blob:verified']); assert.equal(h.find(tree, node => node.type === 'a')[0].props.download, '设计.pdf')
  h.stop(); assert.deepEqual(revoked, ['blob:verified'])

  const image = harness(), imageCalls = []
  const imageModule = moduleFrom('src/mobile-react/MobileAttachment.tsx', { react: image.React, 'react/jsx-runtime': image.jsxRuntime, './mobileApi': { mobileBlob: (path, projectId, signal) => new Promise(resolve => imageCalls.push({ signal, resolve })) } }, {
    IntersectionObserver: Observer,
    URL: { createObjectURL: () => { throw Error('late image must not allocate an object URL') }, revokeObjectURL() {} },
  }, '\nexport { AttachmentResource }')
  image.render(() => imageModule.AttachmentResource({ id: 8, requirementId: 23, projectId: 'other', name: '截图.png', image: true }))
  assert.equal(imageCalls.length, 0)
  const observer = observers.at(-1)
  observer.callback([{ isIntersecting: false }]); assert.equal(imageCalls.length, 0)
  observer.callback([{ isIntersecting: true }]); assert.equal(imageCalls.length, 1); assert.equal(observer.disconnected, true)
  image.stop(); assert.equal(imageCalls[0].signal.aborted, true)
  imageCalls[0].resolve(new Blob(['image'])); await tick()
})

await test('closed attachment sections mount no attachment readers and permission changes reset retained pages', async () => {
  const h = harness(), Attachment = () => null
  const { MobileDetail } = moduleFrom('src/mobile-react/MobileDetail.tsx', { react: h.React, 'react/jsx-runtime': h.jsxRuntime, './mobileApi': { mobileAPI: h.api, relativeTime: String }, './icons': {}, './MobileContent': { Attachment, hasContent: Boolean } }, { window: h.window, document: { body: { style: { overflow: '' } } } })
  let tree = h.render(() => MobileDetail({ target: { kind: 'requirement', id: 23, projectId: 'other' }, onClose() {} }))
  h.calls[0].resolve({ id: 23, title: '详情' }); h.calls[1].resolve({ items: [] }); h.calls[2].resolve({ items: [{ id: 7, name: '设计.pdf' }] }); await tick(); tree = h.render()
  assert.equal(h.find(tree, node => node.type === Attachment).length, 0)
  h.find(tree, node => node.type === 'details' && node.props.onToggle)[0].props.onToggle({ currentTarget: { open: true } }); tree = h.render()
  assert.equal(h.find(tree, node => node.type === Attachment).length, 1)
  h.find(tree, node => node.type === 'details' && node.props.onToggle)[0].props.onToggle({ currentTarget: { open: false } }); tree = h.render()
  assert.equal(h.find(tree, node => node.type === Attachment).length, 0); h.stop()
  const app = read('src/mobile-react/MobileApp.tsx')
  assert.match(app, /key=\{sessionScope\(session\)\}/)
  for (const field of ['projectRoles', 'operationDisabled', 'impersonation', 'tenant?.id', 'organizationPermissions']) assert(app.includes(`session?.${field}`) || app.includes(`session?.user?.${field}`))
  assert.match(app, /setForeground\(false\)/)
  assert.match(app, /if\(pending\)return pending/)
  assert.match(app, /if\(previousScope&&changed\)\{unreadCounter.commit\(0\);setDetail\(null\)\}/)
})

console.log(`Passed ${passed} mobile performance and request lifecycle checks.`)

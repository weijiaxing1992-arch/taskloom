import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import ts from 'typescript'

const read = path => readFileSync(new URL(`../${path}`, import.meta.url), 'utf8')
const tick = () => new Promise(resolve => setImmediate(resolve))
let passed = 0
async function test(name, run) { await run(); passed++; console.log(`✓ ${name}`) }
function moduleFrom(path, imports = {}, globals = {}, extra = '') {
  const code = ts.transpileModule(read(path) + extra, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022, jsx: ts.JsxEmit.ReactJSX } }).outputText
  const exports = {}
  new Function('require', 'exports', ...Object.keys(globals), code)(name => {
    assert(name in imports, `Unexpected import: ${name}`)
    return imports[name]
  }, exports, ...Object.values(globals))
  return exports
}

function harness() {
  const slots = [], effects = [], layouts = [], timers = new Map(), calls = [], navigations = []
  let cursor = 0, dirty = false, nextTimer = 0, component
  const changed = (a, b) => !a || !b || a.length !== b.length || a.some((value, index) => !Object.is(value, b[index]))
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
    useEffect: effect(effects), useLayoutEffect: effect(layouts),
    lazy: () => function LazyComponent() {}, Suspense: 'suspense',
  }
  React.useCallback = (callback, deps) => React.useMemo(() => callback, deps)
  const events = () => {
    const listeners = new Map()
    return {
      addEventListener(name, fn) { if (!listeners.has(name)) listeners.set(name, new Set()); listeners.get(name).add(fn) },
      removeEventListener(name, fn) { listeners.get(name)?.delete(fn) },
      dispatch(name, event = {}) { listeners.get(name)?.forEach(fn => fn(event)) },
    }
  }
  const location = { href: 'https://devflow.test/mobile.html?tab=work', origin: 'https://devflow.test', search: '?tab=work', assign(path) { navigations.push(path) }, replace(path) { navigations.push(path) } }
  const window = {
    ...events(), location, scrollY: 0,
    history: { scrollRestoration: 'auto', pushState(_state, _title, path) { const url = new URL(path, location.origin); location.href = url.href; location.search = url.search } },
    scrollTo(_x, y) { this.scrollY = y },
    setTimeout(fn) { timers.set(++nextTimer, fn); return nextTimer }, clearTimeout(id) { timers.delete(id) },
    setInterval() { return 1 }, clearInterval() {},
  }
  const document = { ...events(), visibilityState: 'visible', body: { style: { overflow: '' } } }
  const api = (path, options = {}) => new Promise((resolve, reject) => calls.push({ path, options, resolve, reject }))
  const jsx = (type, props, key) => ({ type, props: props || {}, key })
  function walk(node, callback) { if (!node || typeof node !== 'object') return; if (Array.isArray(node)) return node.forEach(item => walk(item, callback)); callback(node); walk(node.props?.children, callback) }
  const h = {
    React, window, document, api, calls, navigations, jsx: { jsx, jsxs: jsx, Fragment: 'fragment' },
    globals: { window, document, location, localStorage: { getItem: () => 'project-one' } },
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
    scroll(y) { window.scrollY = y; window.dispatch('scroll') },
    stop() { slots.forEach(slot => slot.cleanup?.()) },
    find(tree, predicate) { const found = []; walk(tree, node => { if (predicate(node)) found.push(node) }); return found },
  }
  h.hooks = moduleFrom('src/mobile-react/useMobilePage.ts', { react: React, './mobileApi': { mobileAPI: api } }, h.globals)
  return h
}

class APIError extends Error { constructor(message, status) { super(message); this.status = status } }
function appModule(h) {
  const requests = moduleFrom('src/mobile-react/mobileRequests.ts')
  return moduleFrom('src/mobile-react/MobileApp.tsx', {
    react: h.React, 'react/jsx-runtime': h.jsx, '../assets/taskloom-mark.svg': { default: '/assets/taskloom-mark.svg' }, './icons': {},
    './mobileApi': { MobileAPIError: APIError, mobileAPI: h.api, sameOriginPath: path => path || null },
    './workItems': {}, './MobileLists': { MobileRequirements: 'requirements', MobileIterations: 'iterations' },
    './MobilePrimitives': {}, './mobileRequests': requests, './useMobilePage': h.hooks,
    '../notificationDisplay': moduleFrom('src/notificationDisplay.ts'),
  }, h.globals, '\nexport { MobileNotifications }')
}
const appFrom = h => appModule(h).default

await test('focus refresh retains the open detail and visible list; session outages retry in place', async () => {
  const h = harness(), App = appFrom(h), session = { user: { id: 'me', role: 'member' }, project: { id: 'project-one' } }
  let tree = h.render(App)
  h.calls[0].resolve(session); await tick()
  h.calls.find(call => call.path === '/projects').resolve({ items: [] })
  h.calls.find(call => call.path === '/notifications/unread-count').resolve({ unread: 0 }); await tick(); tree = h.render()
  const work = () => h.find(tree, node => node.props.onOpen && node.props.session)[0]
  work().props.onOpen({ type: '需求', id: 42, projectId: 'project-one' }); tree = h.render()
  const detail = () => h.find(tree, node => node.props.target)[0]
  const originalType = detail().type, originalTarget = detail().props.target
  h.scroll(760); h.window.dispatch('focus'); tree = h.render()
  assert.equal(detail().type, originalType); assert.equal(detail().props.target, originalTarget)
  assert.equal(detail().props.active, true); assert.equal(work().props.active, true); assert.equal(h.window.scrollY, 760)
  const refresh = h.calls.filter(call => call.path === '/session').at(-1)
  refresh.reject(new APIError('网络连接失败', 0)); await tick(); tree = h.render()
  assert.equal(detail().props.target, originalTarget); assert(work())
  const alert = h.find(tree, node => node.props.className === 'dfm-app-error')[0]
  h.find(alert, node => node.type === 'button')[0].props.onClick()
  assert.equal(h.navigations.length, 0)
  h.calls.filter(call => call.path === '/session').at(-1).resolve(session); await tick(); tree = h.render()
  assert.equal(h.find(tree, node => node.props.className === 'dfm-app-error').length, 0)
  h.document.visibilityState = 'hidden'; h.document.dispatch('visibilitychange'); tree = h.render()
  assert.equal(detail().props.target, originalTarget); assert.equal(detail().props.active, false)
  assert.equal(work().props.active, false)
  assert.equal(h.find(tree, node => node.props.hidden === true).length, 0, 'backgrounding must not collapse the selected page')
  h.document.visibilityState = 'visible'; h.document.dispatch('visibilitychange')
  h.calls.filter(call => call.path === '/session').at(-1).resolve({ ...session, user: { ...session.user, role: 'guest' } }); await tick(); tree = h.render()
  assert.equal(detail(), undefined, 'changed permission scope must still close retained details')
  h.stop()
})

await test('my work opens iterations inside mobile with the original project and target', async () => {
  const h = harness(), App = appFrom(h)
  h.render(App); h.calls[0].resolve({ user: { id: 'me' }, project: { id: 'project-one' } }); await tick()
  let tree = h.render()
  h.find(tree, node => node.props.onOpen && node.props.session)[0].props.onOpen({ type: '迭代', id: 17, projectId: 'project-two', url: '/iterations?sprint=17' })
  tree = h.render()
  const iterations = h.find(tree, node => node.type === 'iterations')[0]
  assert.equal(iterations.props.active, true); assert.equal(iterations.props.projectId, 'project-two')
  assert.deepEqual(iterations.props.requestedSprint, { id: 17, projectId: 'project-two' })
  const url = new URL(h.window.location.href)
  assert.equal(url.pathname, '/mobile.html'); assert.equal(url.searchParams.get('sprint'), '17'); assert.equal(url.searchParams.get('project'), 'project-two')
  assert.equal(h.navigations.length, 0)
  h.stop()
})

await test('iteration return restores list filters and scroll; requested iteration selects existing tab', async () => {
  const h = harness()
  const { MobileIterations } = moduleFrom('src/mobile-react/MobileLists.tsx', { react: h.React, 'react/jsx-runtime': h.jsx, './MobilePrimitives': {}, './icons': {}, './useMobilePage': h.hooks }, h.globals)
  let active = true, requestedSprint = null
  const render = () => h.render(() => MobileIterations({ projectId: 'project-one', active, requestedSprint, onOpen() {} }))
  let tree = render(); h.flush()
  const data = { items: [{ id: 17, name: '九月迭代', status: '进行中' }, { id: 18, name: '十月迭代', status: '规划中' }] }
  h.calls[0].resolve(data); await tick(); tree = render()
  h.find(tree, node => node.props.label === '搜索迭代名称或编号')[0].props.onChange('九月'); tree = render()
  h.scroll(640)
  h.find(tree, node => node.props.className === 'dfm-sprint-card')[0].props.onClick(); tree = render(); h.flush()
  assert.equal(h.calls.at(-1).path, '/sprints/17')
  h.calls.at(-1).resolve({ items: [{ id: 42, title: '事项', objectType: 'requirement' }] }); await tick(); tree = render()
  assert.equal(h.window.scrollY, 0)
  h.scroll(190)
  h.find(tree, node => node.props.className === 'dfm-back-link')[0].props.onClick(); tree = render(); h.flush()
  h.calls.at(-1).resolve(data); await tick(); tree = render()
  assert.equal(h.window.scrollY, 640)
  assert.equal(h.find(tree, node => node.props.label === '搜索迭代名称或编号')[0].props.value, '九月')
  active = false; render(); const count = h.calls.length
  requestedSprint = { id: 18, projectId: 'project-one' }; render(); h.flush(); assert.equal(h.calls.length, count)
  active = true; tree = render(); h.flush(); assert.equal(h.calls.at(-1).path, '/sprints/18')
  h.stop()
})

await test('detail backgrounding aborts requests while preserving expanded content; errors can retry', async () => {
  const h = harness(), Attachment = 'attachment', LoadState = 'load-state'
  const { MobileDetail } = moduleFrom('src/mobile-react/MobileDetail.tsx', { react: h.React, 'react/jsx-runtime': h.jsx, './mobileApi': { mobileAPI: h.api }, './icons': {}, './MobileContent': { Attachment, LoadState, hasContent: Boolean } }, h.globals)
  let active = true
  const close = () => {}, target = { kind: 'requirement', id: 42, projectId: 'project-one' }
  const render = () => h.render(() => MobileDetail({ target, onClose: close, active }))
  h.scroll(720)
  let tree = render()
  h.calls[0].resolve({ id: 42, title: '阅读中的需求' }); h.calls[1].resolve({ items: [] }); h.calls[2].resolve({ items: [{ id: 9, name: '设计.png' }] }); await tick(); tree = render()
  h.find(tree, node => node.type === 'details' && node.props.onToggle)[0].props.onToggle({ currentTarget: { open: true } }); tree = render()
  active = false; tree = render(); assert(h.calls.every(call => call.options.signal.aborted))
  assert.equal(h.find(tree, node => node.type === Attachment)[0].props.active, false)
  assert.equal(h.calls.length, 3); assert.equal(h.document.body.style.overflow, 'hidden')
  active = true; tree = render()
  assert.equal(h.find(tree, node => node.type === LoadState)[0].props.loading, false)
  assert.equal(h.find(tree, node => node.type === Attachment).length, 1)
  h.calls[3].reject(new Error('暂时离线')); h.calls[4].resolve({ items: [] }); h.calls[5].resolve({ items: [] }); await tick(); tree = render()
  const state = h.find(tree, node => node.type === LoadState)[0]
  assert.equal(state.props.error, '暂时离线'); state.props.retry(); tree = render()
  h.calls[6].resolve({ id: 42, title: '恢复后的需求' }); h.calls[7].resolve({ items: [] }); h.calls[8].resolve({ items: [] }); await tick(); tree = render()
  assert.equal(h.find(tree, node => node.type === LoadState)[0].props.error, '')
  assert.equal(h.find(tree, node => node.type === 'h1')[0].props.children, '恢复后的需求')
  h.stop(); assert.equal(h.window.scrollY, 720); assert.equal(h.document.body.style.overflow, '')
})

await test('backgrounded attachments cancel pending reads and ignore observer callbacks until active', async () => {
  const h = harness(), observers = [], revoked = []
  class Observer { constructor(callback) { this.callback = callback; observers.push(this) } observe() {} disconnect() {} }
  const { AttachmentResource } = moduleFrom('src/mobile-react/MobileAttachment.tsx', { react: h.React, 'react/jsx-runtime': h.jsx, './mobileApi': { mobileBlob: (path, projectId, signal) => h.api(path, { projectId, signal }) } }, { ...h.globals, IntersectionObserver: Observer, URL: { createObjectURL: () => 'blob:reading', revokeObjectURL: url => revoked.push(url) } }, '\nexport { AttachmentResource }')
  let active = true
  const render = () => h.render(() => AttachmentResource({ id: 9, requirementId: 42, projectId: 'project-one', name: '设计.png', image: true, active }))
  render(); observers[0].callback([{ isIntersecting: true }]); assert.equal(h.calls.length, 1)
  active = false; render(); assert.equal(h.calls[0].options.signal.aborted, true)
  observers[0].callback([{ isIntersecting: true }]); assert.equal(h.calls.length, 1)
  h.calls[0].resolve(new Blob(['old'])); await tick(); render(); assert.deepEqual(revoked, [])
  active = true; render(); observers.at(-1).callback([{ isIntersecting: true }]); assert.equal(h.calls.length, 2)
  h.calls[1].resolve(new Blob(['new'])); await tick(); render()
  active = false; render(); active = true; render(); observers.at(-1).callback([{ isIntersecting: true }]); assert.equal(h.calls.length, 2)
  h.stop(); assert.deepEqual(revoked, ['blob:reading'])
})

await test('mobile notice cards and detail share readable system summaries without altering user text', async () => {
  const h = harness(), { MobileNotifications } = appModule(h)
  const generated = { id: 1, readAt: 'read', eventType: 'requirement.updated', subjectType: 'requirement', subjectId: 42, body: '需求已更新：descriptionDoc,acceptance\nREQ-042 登录优化\n正文仍保留 descriptionDoc 与 REQ-042' }
  const userText = { id: 2, readAt: 'read', eventType: 'comment.created', subjectType: 'requirement', subjectId: 42, body: '需求已更新：descriptionDoc,acceptance\nREQ-042 用户讨论里的原文' }
  const expected = '需求已更新：需求描述、验收标准\n000042 登录优化\n正文仍保留 descriptionDoc 与 REQ-042'
  const render = () => h.render(() => MobileNotifications({ session: { user: { id: 'me' } }, active: true, unread: 0, onUnreadChange() {}, onOpen() {} }))
  let tree = render(); h.flush(); h.calls[0].resolve({ items: [generated, userText] }); await tick(); tree = render()
  const cards = h.find(tree, node => node.props.className === 'dfm-notice-copy')
  assert(h.find(cards[0], node => node.props.children === expected).length)
  assert(h.find(cards[1], node => node.props.children === userText.body).length)
  h.find(tree, node => node.props.className === 'dfm-notice-open')[0].props.onClick(); tree = render()
  const detail = h.find(tree, node => node.props['aria-label'] === '通知详情')[0]
  assert.equal(h.find(detail, node => typeof node.props.text === 'string')[0].props.text, expected)
  const fallback = h.find(detail, node => node.type === 'suspense')[0].props.fallback
  assert.equal(fallback.props.children, expected)
  assert.equal(h.calls.length, 1, 'opening an already-read notice must not write a notification state')
  h.stop()
})

console.log(`Passed ${passed} mobile experience regression checks.`)

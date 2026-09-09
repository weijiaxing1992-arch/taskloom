import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import ts from 'typescript'
import * as Vue from 'vue'
const read = path => readFileSync(new URL('../' + path, import.meta.url), 'utf8')
function evaluate(source, imports = {}, globals = {}) {
  const exports = {}
  new Function('require', 'exports', ...Object.keys(globals), ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText)(id => imports[id] || {}, exports, ...Object.values(globals))
  return exports
}
const helpers = evaluate(read('src/watermark.ts'))
const valid = { userId: 'a', accountName: '成员甲', serverTime: '2026-09-06T08:00:00Z', ipAddress: '::1', ipSource: 'connection' }
assert(helpers.validWatermark(valid, 'a'))
for (const data of [null, {}, { ...valid, userId: 'b' }, { ...valid, serverTime: 'bad' }, { ...valid, accountName: null }, { ...valid, ipSource: 'forwarded' }]) assert(!helpers.validWatermark(data, 'a'))
assert.equal(helpers.watermarkTime(Date.parse(valid.serverTime), 'Asia/Shanghai'), '2026-09-06 16:00:00')
assert.match(helpers.watermarkTime(Date.parse(valid.serverTime), 'bad-timezone'), /UTC$/)
assert.equal(helpers.watermarkName(' A\nB '), 'A B')
assert.equal([...helpers.watermarkName('😀'.repeat(50))].length, 32)

const source = read('src/components/PageWatermark.vue')
const store = Vue.reactive({ session: { tenant: { id: 't' } }, currentUser: { id: 'a', name: '甲', timezone: 'Asia/Shanghai' }, identityConflict: false, operationDisabled: false, mustChangePassword: false })
const calls = [], mounted = [], unmount = [], intervals = new Map(), timeouts = new Map()
const document = Object.assign(new EventTarget(), { hidden: false }), window = new EventTarget()
const scope = Vue.effectScope()
const controller = scope.run(() => evaluate(source.match(/<script setup lang="ts">([\s\S]*?)<\/script>/)[1] + '\nexport {metadata,visible,account,ip,refresh,clock}', {
  vue: { ...Vue, useId: () => 'test', onMounted: fn => mounted.push(fn), onBeforeUnmount: fn => unmount.push(fn) },
  '../watermark': helpers, '../stores/workspace': { useWorkspaceStore: () => store },
  '../api': { api: (path, options) => new Promise((resolve, reject) => calls.push({ path, options, resolve, reject })) },
}, { document, window, setInterval: fn => { intervals.set(fn, fn); return fn }, clearInterval: id => intervals.delete(id), setTimeout: fn => { timeouts.set(fn, fn); return fn }, clearTimeout: id => timeouts.delete(id) }))
const flush = async () => { for (let i = 0; i < 8; i++) { await Promise.resolve(); await Vue.nextTick() } }
mounted.forEach(fn => fn())
assert.equal(calls.length, 1)
const first = calls[0]
store.currentUser = { id: 'b', name: '乙' }; await flush()
assert(first.options.signal.aborted)
first.resolve(valid); await flush()
assert.equal(controller.account.value, '乙')
assert.equal(controller.metadata.value, null)
calls.at(-1).resolve({ ...valid, userId: 'b', accountName: '成员乙' }); await flush()
assert.equal(controller.account.value, '成员乙')
assert.equal(controller.ip.value, '::1')
document.hidden = true
const count = calls.length
await controller.refresh(); assert.equal(calls.length, count)
document.hidden = false
const retry = controller.refresh(); calls.at(-1).reject(new Error('offline')); await retry
assert.equal(controller.ip.value, '暂不可用')
store.identityConflict = true; await flush(); assert(!controller.visible.value)
unmount.forEach(fn => fn()); scope.stop()
assert.equal(intervals.size, 0); assert.equal(timeouts.size, 0)
assert.match(source, /pointer-events:none/); assert.match(source, /<Teleport to="body">/)
assert.match(source, /aria-hidden="true"/); assert(!source.includes('v-html'))
assert.match(read('src/App.vue'), /<PageWatermark\s*\/>/)
console.log('Watermark: validation, time zone, identity race, cancellation, offline, visibility and cleanup passed')

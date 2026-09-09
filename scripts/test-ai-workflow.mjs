import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import ts from 'typescript'
import * as Vue from 'vue'
const read = path => readFileSync(new URL('../' + path, import.meta.url), 'utf8')
const settings = (extra = {}) => ({ provider: 'openai', configured: true, enabled: true, model: 'gpt-5-mini', models: ['gpt-5-mini', 'gpt-5-mini-2025-08-07'], canManage: true, ...extra })
const capability = (extra = {}) => ({ configured: true, enabled: true, model: 'gpt-5-mini', canGenerate: true, inputFields: ['title', 'description', 'acceptance'], maxCases: 10, focusOptions: ['normal', 'boundary', 'permission', 'failure', 'security', 'performance', 'compatibility', 'automation'], ...extra })
const preview = (extra = {}) => ({ draftId: 'draft-1', requirementId: 7, requirementUpdatedAt: 'version-1', expiresAt: '2050-01-01T00:00:00Z', cases: [{ index: 0, title: '用户原文测试', preconditions: '原文前提', priority: 'P1', caseType: '功能测试', stepsDetail: [{ order: 1, action: '执行动作', expected: '原文预期' }] }, { index: 1, title: 'Second case', preconditions: '', priority: 'P2', caseType: '功能测试', stepsDetail: [] }], ...extra })
const imported = (extra = {}) => ({ items: [{ id: 101, code: 'TC-101', title: 'Saved case' }], importedCount: 1, replayed: false, ...extra })
const reviewCapability = (extra = {}) => ({ configured: true, enabled: true, model: 'gpt-5-mini', canReview: true, modes: ['standard', 'logic'], ...extra })
const reviewResult = (extra = {}) => ({ summary: '覆盖主流程，仍需补充异常恢复。', issues: [{ severity: 'medium', field: '步骤 2', message: '没有验证失败后的恢复状态', suggestion: '补充无权限与服务异常后的预期' }], model: 'gpt-5-mini', replayed: false, ...extra })
const deferred = () => { let resolve, reject; const promise = new Promise((a, b) => { resolve = a; reject = b }); return { promise, resolve, reject } }
const flush = async () => { for (let i = 0; i < 8; i++) { await Promise.resolve(); await Vue.nextTick() } }
function fixture(kind, handler = async (path, options) => kind === 'settings' ? settings() : options ? preview() : capability()) {
 const file = kind === 'settings' ? 'src/views/AISettings.vue' : 'src/components/RequirementAITestCases.vue', source = read(file).match(/<script setup[^>]*>([\s\S]*?)<\/script>/)[1], scope = Vue.effectScope(), props = Vue.reactive({ requirementId: 7, requirementUpdatedAt: 'version-1', disabled: false, hasUnsavedChanges: false, libraryId: null, folderId: null }), calls = [], emits = [], unmount = [], mounted = [], routes = [], locked = Vue.ref(false), confirmations = [], listeners = new Map(); let confirm = true
 const settingsScope = { locked, project: 'p', current: () => !locked.value, request: async (path, options) => { calls.push({ path, options }); return handler(path, options) } }
 const imports = { vue: { ...Vue, onMounted: fn => mounted.push(fn), onBeforeUnmount: fn => unmount.push(fn) }, 'vue-router': { onBeforeRouteLeave: fn => routes.push(fn), onBeforeRouteUpdate: fn => routes.push(fn) }, '../i18n': { t: value => value }, './settingsScope': { useSettingsScope: () => settingsScope }, '../components/settingsScope': { useSettingsScope: () => settingsScope } }
 const common = 'loading,saving,error,notice,dirty,load,canLeave', names = common + (kind === 'settings' ? ',settings,apiKey,model,enabled,disabled,save,remove,beforeProjectChange,cancelProjectLeave' : ',capability,draft,selected,consent,imported,stale,expired,canGenerate,canImport,generating,importing,focus,extraInstructions,count,importDestination,supportedFocus,countOptions,generate,toggle,selectAll,importCases,discard')
 const code = ts.transpileModule(source + '\nexport {' + names + '}', { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText, result = {}
 scope.run(() => new Function('require', 'exports', 'defineProps', 'defineEmits', 'defineExpose', 'window', 'localStorage', 'setInterval', 'clearInterval', code)(id => imports[id] || {}, result, () => props, () => (...args) => emits.push(args), () => {}, { confirm: message => { confirmations.push(message); return confirm }, addEventListener: (name, fn) => listeners.set(name, fn), removeEventListener: name => listeners.delete(name) }, { getItem: () => 'p' }, () => 1, () => {}))
 return { ...result, props, calls, emits, locked, confirmations, routes, listeners, setConfirm(value) { confirm = value }, mount() { mounted.forEach(fn => fn()) }, stop() { unmount.forEach(fn => fn()); scope.stop() } }
}
async function generated(handler) { const m = fixture('cases', handler); await m.load(); m.consent.value = true; await m.generate(); return m }
function reviewFixture(handler = async (path, options) => options ? reviewResult() : reviewCapability()) {
 const file = 'src/components/testing/TestCaseAIReview.vue', source = read(file).match(/<script setup[^>]*>([\s\S]*?)<\/script>/)[1], scope = Vue.effectScope(), props = Vue.reactive({ caseId: 101, updatedAt: 'case-version-1', disabled: false }), calls = [], emits = [], unmount = [], mounted = [], locked = Vue.ref(false)
 const settingsScope = { locked, project: 'p', current: () => !locked.value, request: async (path, options) => { calls.push({ path, options }); return handler(path, options) } }
 const imports = { vue: { ...Vue, onMounted: fn => mounted.push(fn), onBeforeUnmount: fn => unmount.push(fn) }, '../../i18n': { t: value => value }, '../settingsScope': { useSettingsScope: () => settingsScope }, '../ui/button': { Button: {} } }
 const names = 'capability,loading,reviewing,confirmed,mode,review,error,notice,load,start,canLeave'
 const code = ts.transpileModule(source + '\nexport {' + names + '}', { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText, result = {}
 scope.run(() => new Function('require', 'exports', 'defineProps', 'defineEmits', 'defineExpose', code)(id => imports[id] || {}, result, () => props, () => (...args) => emits.push(args), () => {}))
 return { ...result, props, calls, emits, locked, mount() { mounted.forEach(fn => fn()) }, stop() { unmount.forEach(fn => fn()); scope.stop() } }
}
let count = 0; async function test(name, run) { await run(); count++; console.log('✓ ' + name) }
await test('AI settings load metadata only and unchanged keys never appear in model/enable patches', async () => {
 const m = fixture('settings'); await m.load(); assert.equal(m.apiKey.value, ''); assert.equal(m.dirty.value, false); m.enabled.value = false; await m.save()
 assert.deepEqual(JSON.parse(m.calls[1].options.body), { enabled: false }); assert.equal(m.calls[1].path, '/organization/ai-settings'); assert.equal(m.calls.length, 2); assert(!m.calls.some(call => call.path.includes('ai-test-cases'))); m.stop()
})
await test('settings failure preserves the key draft, prevents duplicate submits and blocks navigation while saving', async () => {
 const pending = deferred(), m = fixture('settings', async (path, options) => options ? pending.promise : settings()); await m.load(); m.apiKey.value = 'private-test-key'; const saving = m.save(); await m.save(); assert.equal(m.calls.length, 2); assert.equal(m.canLeave(), false)
 pending.reject(Error('Offline')); await saving; assert.equal(m.apiKey.value, 'private-test-key'); assert.equal(m.dirty.value, true); assert.equal(m.saving.value, false); m.setConfirm(false); await m.load(); assert.equal(m.calls.length, 2); assert.equal(m.canLeave(), false); m.stop(); assert.equal(m.apiKey.value, '')
})
await test('settings validation and permission checks fail closed; clearing is explicit and cannot silently erase a key', async () => {
 const m = fixture('settings', async (path, options) => options ? settings({ configured: false, enabled: false }) : settings({ configured: false, enabled: false })); await m.load(); m.enabled.value = true; await m.save(); assert.equal(m.calls.length, 1); assert.match(m.error.value, /API 密钥/)
 m.enabled.value = false; m.model.value = 'unsupported'; await m.save(); assert.equal(m.calls.length, 1)
 m.settings.value = settings(); m.model.value = 'gpt-5-mini'; m.setConfirm(false); await m.remove(); assert.equal(m.calls.length, 1); m.setConfirm(true); await m.remove(); assert.deepEqual(JSON.parse(m.calls.at(-1).options.body), { clear: true }); assert.equal(m.settings.value.configured, false)
 m.settings.value = settings({ canManage: false }); m.apiKey.value = 'never-send'; const before = m.calls.length; await m.save(); assert.equal(m.calls.length, before); m.stop()
})
await test('settings scope changes scrub secrets immediately and late saved metadata cannot revive them', async () => {
 const pending = deferred(), m = fixture('settings', async (path, options) => options ? pending.promise : settings()); await m.load(); m.apiKey.value = 'private-test-key'; const saving = m.save(); m.locked.value = true; assert.equal(m.apiKey.value, ''); assert.equal(m.settings.value, null)
 pending.resolve(settings()); await saving; assert.equal(m.settings.value, null); assert.equal(m.saving.value, false); m.stop()
})
await test('invalid saved metadata preserves the key draft and extra response fields are never retained as settings',async()=>{
 let invalid=true;const m=fixture('settings',async(path,options)=>options&&invalid?{model:'broken'}:settings({apiKey:'must-not-retain'}));await m.load();assert.equal(Object.hasOwn(m.settings.value,'apiKey'),false)
 m.apiKey.value='private-test-key';await m.save();assert.match(m.error.value,/返回格式/);assert.equal(m.apiKey.value,'private-test-key');assert.equal(m.settings.value.model,'gpt-5-mini');assert.equal(m.saving.value,false)
 invalid=false;await m.save();assert.equal(m.apiKey.value,'');assert.equal(Object.hasOwn(m.settings.value,'apiKey'),false);m.stop()
})
await test('generation requires explicit consent and sends only the saved requirement version, never local description text or a key', async () => {
 const m = fixture('cases'); await m.load(); await m.generate(); assert.equal(m.calls.length, 1); m.consent.value = true; m.props.hasUnsavedChanges = true; await m.generate(); assert.equal(m.calls.length, 1)
 m.props.hasUnsavedChanges = false; await m.generate(); assert.equal(m.calls[1].path, '/requirements/7/ai-test-cases'); assert.deepEqual(JSON.parse(m.calls[1].options.body), { confirmed: true, requirementUpdatedAt: 'version-1', focus: [], count: 5 }); assert.equal(m.draft.value.cases[0].title, '用户原文测试'); assert.deepEqual(m.selected.value, []); assert.equal(m.consent.value, false); assert.equal(m.dirty.value, true); m.stop()
})
await test('generation controls send only whitelisted focus, bounded count and explicit target library on confirmed import', async () => {
 const m = fixture('cases', async (path, options) => path.endsWith('/import') ? imported() : options ? preview() : capability({ focusOptions: ['security', 'performance'] }))
 await m.load(); m.focus.value = ['security', 'attacker-value']; m.count.value = 99; m.extraInstructions.value = '仅覆盖高风险登录恢复场景'; m.consent.value = true; await m.generate()
 assert.deepEqual(JSON.parse(m.calls[1].options.body), { confirmed: true, requirementUpdatedAt: 'version-1', focus: ['security'], count: 10, extraInstructions: '仅覆盖高风险登录恢复场景' })
 m.props.libraryId = 11; m.props.folderId = 12; m.toggle(0); await m.importCases()
 assert.deepEqual(JSON.parse(m.calls[2].options.body), { draftId: 'draft-1', indexes: [0], libraryId: 11, folderId: 12 })
 assert(m.emits.some(([event, value]) => event === 'busy-change' && value === true)); assert(m.emits.some(([event, value]) => event === 'busy-change' && value === false)); m.stop()
})
await test('generation does not import automatically, repeated generation failures preserve the existing reviewable preview', async () => {
 let fail = false; const m = await generated(async (path, options) => { if (!options) return capability(); if (fail) throw Error('Provider unavailable'); return preview() }); m.toggle(0); const before = JSON.stringify(m.draft.value); m.consent.value = true; m.setConfirm(false); await m.generate(); assert.equal(m.calls.length, 2)
 m.setConfirm(true); fail = true; await m.generate(); assert.equal(JSON.stringify(m.draft.value), before); assert.deepEqual(m.selected.value, [0]); assert.equal(m.saving.value, false); assert(!m.calls.some(call => call.path.endsWith('/import'))); m.stop()
})
await test('import requires an explicit selection, freezes it while busy and emits only confirmed imported cases', async () => {
 const pending = deferred(), m = await generated(async (path, options) => path.endsWith('/import') ? pending.promise : options ? preview() : capability()); await m.importCases(); assert.equal(m.calls.length, 2); m.toggle(1); const importing = m.importCases(); m.toggle(0); await m.importCases()
 assert.deepEqual(m.selected.value, [1]); assert.equal(m.calls.length, 3); assert.deepEqual(JSON.parse(m.calls[2].options.body), { draftId: 'draft-1', indexes: [1] }); assert.equal(m.canLeave(), false)
 pending.resolve(imported()); await importing; assert.equal(m.imported.value, true); assert.equal(m.dirty.value, false); assert.equal(m.emits.filter(([event]) => event === 'imported').length, 1); await m.importCases(); assert.equal(m.calls.length, 3); m.stop()
})
await test('failed import preserves exact indexes for an idempotent retry and does not show a fake success', async () => {
 let fail = true; const m = await generated(async (path, options) => { if (path.endsWith('/import')) { if (fail) throw Error('Network unavailable'); return imported({ replayed: true }) } return options ? preview() : capability() }); m.toggle(0); await m.importCases(); assert.equal(m.imported.value, false); assert.equal(m.dirty.value, true); assert.deepEqual(m.selected.value, [0]); assert(!m.emits.some(([event]) => event === 'imported'))
 fail = false; await m.importCases(); const writes = m.calls.filter(call => call.path.endsWith('/import')); assert.equal(writes[0].options.body, writes[1].options.body); assert.equal(m.imported.value, true); assert.equal(m.emits.find(([event]) => event === 'imported')[1].replayed, true); m.stop()
})
await test('changed saved content and expired previews block import and require renewed generation consent', async () => {
 const m = await generated(); m.toggle(0); m.consent.value = true; m.props.requirementUpdatedAt = 'version-2'; await flush(); assert.equal(m.stale.value, true); assert.equal(m.consent.value, false); await m.importCases(); assert.equal(m.calls.length, 2)
 m.props.requirementUpdatedAt = 'version-1'; m.draft.value.expiresAt = '2000-01-01T00:00:00Z'; assert.equal(m.expired.value, true); await m.importCases(); assert.equal(m.calls.length, 2); m.stop()
})
await test('viewer/disabled capabilities and invalid responses cannot generate or import any cases', async () => {
 for (const value of [capability({ canGenerate: false }), capability({ configured: false }), capability({ enabled: false })]) { const m = fixture('cases', async () => value); await m.load(); m.consent.value = true; await m.generate(); assert.equal(m.calls.length, 1); m.stop() }
 const m = fixture('cases', async (path, options) => options ? preview({ requirementId: 99 }) : capability()); await m.load(); m.consent.value = true; await m.generate(); assert.equal(m.draft.value, null); assert.match(m.error.value, /预览格式/); m.stop()
})
await test('malformed previews and uncertain imports retain review state without producing fake success',async()=>{
 for(const invalid of [null,preview({cases:[null]}),preview({cases:[{...preview().cases[0],stepsDetail:[null]}]})]){
  let response=preview();const m=await generated(async(path,options)=>options?response:capability());m.toggle(0);response=invalid;m.consent.value=true;await m.generate();assert.match(m.error.value,/预览格式/);assert.equal(m.draft.value.draftId,'draft-1');assert.deepEqual(m.selected.value,[0]);assert(!m.emits.some(([event])=>event==='imported'));m.stop()
 }
 const m=await generated(async(path,options)=>path.endsWith('/import')?null:options?preview():capability());m.toggle(0);await m.importCases();assert.match(m.error.value,/无法确认/);assert.equal(m.imported.value,false);assert.deepEqual(m.selected.value,[0]);assert.equal(m.saving.value,false);m.stop()
})
await test('requirement replacement, account scope loss and unmount invalidate late generation without importing or exposing old text', async () => {
 for (const transition of ['requirement', 'scope', 'unmount']) {
  const pending = deferred(), m = fixture('cases', async (path, options) => options ? pending.promise : capability()); await m.load(); m.consent.value = true; const generation = m.generate()
  if (transition === 'requirement') { m.props.requirementId = 8; await flush() } else if (transition === 'scope') m.locked.value = true; else m.stop()
  pending.resolve(preview()); await generation; assert.equal(m.draft.value, null); assert(!m.emits.some(([event]) => event === 'imported')); if (transition !== 'unmount') m.stop()
 }
})
await test('draft closing has explicit confirmation and discard never submits an import or deletes real cases', async () => {
 const m = await generated(); m.setConfirm(false); assert.equal(m.canLeave(), false); m.discard(); assert(m.draft.value); m.setConfirm(true); assert.equal(m.canLeave(), true); assert(m.draft.value); m.discard(); assert.equal(m.draft.value, null); assert.equal(m.calls.length, 2); m.stop()
})
await test('AI review requires explicit consent, uses the saved case version, and exposes suggestions without auto-editing', async () => {
 const m = reviewFixture(); await m.load(); await m.start(); assert.equal(m.calls.length, 1)
 m.confirmed.value = true; m.mode.value = 'logic'; await m.start(); assert.equal(m.calls[1].path, '/test-cases/101/ai-review'); assert.deepEqual(JSON.parse(m.calls[1].options.body), { confirmed: true, caseUpdatedAt: 'case-version-1', mode: 'logic' })
 assert.equal(m.review.value.summary, '覆盖主流程，仍需补充异常恢复。'); assert.equal(m.confirmed.value, false); assert.equal(m.emits.filter(([event]) => event === 'change').length, 1); assert(m.emits.some(([event, value]) => event === 'busy-change' && value === true)); assert(m.emits.some(([event, value]) => event === 'busy-change' && value === false)); m.stop()
})
await test('AI review fails closed for unavailable capabilities, malformed output, or a case version that changes during the request', async () => {
 for (const result of [reviewCapability({ canReview: false }), reviewCapability({ configured: false }), reviewCapability({ enabled: false })]) { const m = reviewFixture(async () => result); await m.load(); m.confirmed.value = true; await m.start(); assert.equal(m.calls.length, 1); m.stop() }
 const invalid = reviewFixture(async (path, options) => options ? { summary: 'ok', issues: [{ severity: 'critical', field: 'title', message: 'x', suggestion: 'y' }], model: 'gpt-5-mini' } : reviewCapability()); await invalid.load(); invalid.confirmed.value = true; await invalid.start(); assert.equal(invalid.review.value, null); assert.match(invalid.error.value, /返回格式/); assert(!invalid.emits.some(([event]) => event === 'change')); invalid.stop()
 const pending = deferred(), stale = reviewFixture(async (path, options) => options ? pending.promise : reviewCapability()); await stale.load(); stale.confirmed.value = true; const job = stale.start(); stale.props.updatedAt = 'case-version-2'; await flush(); pending.resolve(reviewResult()); await job; assert.equal(stale.review.value, null); assert(!stale.emits.some(([event]) => event === 'change')); stale.stop()
})
await test('settings project guards preserve cancellation and all AI UI text clearly discloses external data and human review', async () => {
 const m = fixture('settings'); await m.load(); m.apiKey.value = 'private-test-key'; m.setConfirm(false); const event = new Event('project', { cancelable: true }); m.beforeProjectChange(event); assert(event.defaultPrevented); assert.equal(m.apiKey.value, 'private-test-key'); m.setConfirm(true); m.beforeProjectChange(new Event('project', { cancelable: true })); assert.equal(m.canLeave(), true); m.cancelProjectLeave(); m.setConfirm(false); assert.equal(m.canLeave(), false); m.stop()
 const settingsSource = read('src/views/AISettings.vue'), caseSource = read('src/components/RequirementAITestCases.vue'), reviewSource = read('src/components/testing/TestCaseAIReview.vue'); assert.match(settingsSource, /type="password"/); assert(!settingsSource.includes('localStorage.setItem')); assert(!settingsSource.includes('sessionStorage')); assert.match(settingsSource, /保存配置不会验证密钥/); assert.match(caseSource, /发送给 OpenAI/); assert.match(caseSource, /可能产生服务商费用/); assert.match(caseSource, /生成结果|人工校验/); assert(!caseSource.includes('v-html')); assert.match(caseSource, /defineExpose\(\{canLeave,saving,dirty\}\)/); assert.match(reviewSource, /关联需求摘要/); assert.match(reviewSource, /不会发送评论、附件或未保存修改/); assert(!reviewSource.includes('v-html')); assert.match(reviewSource, /defineExpose\(\{canLeave,reviewing\}\)/)
})
console.log(`Passed ${count} AI configuration and test-case regressions (mock APIs only).`)

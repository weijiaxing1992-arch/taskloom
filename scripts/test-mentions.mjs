import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import ts from 'typescript'
import * as lowlight from 'lowlight'
import * as Vue from 'vue'
import { parse, compileScript } from 'vue/compiler-sfc'
const source = await readFile(new URL('../src/mentions.ts', import.meta.url), 'utf8')
const { outputText } = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.ESNext, target: ts.ScriptTarget.ES2022 } })
const helpers = await import('data:text/javascript;base64,' + Buffer.from(outputText).toString('base64'))
const dictionarySource = await readFile(new URL('../src/locales/requirements.en.ts', import.meta.url), 'utf8')
const dictionaryJS = ts.transpileModule(dictionarySource, { compilerOptions: { module: ts.ModuleKind.ESNext, target: ts.ScriptTarget.ES2022 } }).outputText
const english = (await import('data:text/javascript;base64,' + Buffer.from(dictionaryJS).toString('base64'))).default
const testLocale = Vue.ref('zh-CN')
const i18n = { t: (source, params = {}) => (testLocale.value === 'en-US' ? english[source] ?? source : source).replace(/\{(\w+)\}/g, (token, key) => Object.hasOwn(params, key) ? String(params[key]) : token) }
let count = 0
function test(name, run) { run(); count++; console.log('✓ ' + name) }
test('report department directory retains empty departments without inventing member candidates', () => {
  const people = [{ id:'u_1',name:'成员',active:true,departmentIds:['active'],departmentNames:['研发'] }]
  const departments = helpers.memberDepartments(people,[{id:'active',name:'研发'},{id:'empty',name:'空部门'}])
  assert.deepEqual(new Set(departments.map(x=>x.id)),new Set(['active','empty']))
  assert.deepEqual(helpers.membersInDepartment(people,'empty'),[])
})
const members = [
  { id: 'u_1', name: '陈澄', email: 'chen@example.test', projectRole: 'frontend', active: true },
  { id: 'u_2', name: '苏禾', email: 'su@example.test', projectRole: 'qa', active: true },
  { id: 'u_3', name: '停用成员', email: 'inactive@example.test', projectRole: 'backend', active: false },
]
test('plain comment and email do not open member suggestions', () => {
  assert.equal(helpers.mentionAtCaret('普通评论', 4), null)
  assert.equal(helpers.mentionAtCaret('联系 chen@example.test', 20), null)
})
test('bare @ offers the whole active project directory', () => {
  assert.deepEqual(helpers.mentionAtCaret('@', 1), { start: 0, end: 1, query: '' })
  assert.deepEqual(helpers.filterMentionMembers(members, '').map(member => member.id), ['u_1', 'u_2'])
})
test('Chinese adjacent @ and caret-middle searches are parsed', () => {
  assert.deepEqual(helpers.mentionAtCaret('请@陈澄 帮忙', 3), { start: 1, end: 4, query: '陈' })
  assert.equal(helpers.mentionAtCaret('请 @陈澄 帮忙', 8), null)
})
test('punctuation and newline terminate mention queries', () => {
  assert.equal(helpers.mentionAtCaret('@陈，完成', 5), null)
  assert.equal(helpers.mentionAtCaret('@陈\n完成', 5), null)
})
test('member filtering matches name, case-insensitive email and localized role', () => {
  assert.deepEqual(helpers.filterMentionMembers(members, '陈').map(member => member.id), ['u_1'])
  assert.deepEqual(helpers.filterMentionMembers(members, 'CHEN@').map(member => member.id), ['u_1'])
  assert.deepEqual(helpers.filterMentionMembers(members, '测试').map(member => member.id), ['u_2'])
  assert.deepEqual(helpers.filterMentionMembers(members, 'backend'), [])
  assert.deepEqual(helpers.filterMentionMembers(members, '未知成员'), [])
})
test('selection replaces the complete current token and preserves surrounding text', () => {
  const body = '请@陈澄 帮忙'
  assert.deepEqual(helpers.insertMention(body, helpers.mentionAtCaret(body, 3), '苏禾'), { body: '请@苏禾 帮忙', caret: 5 })
  assert.deepEqual(helpers.insertMention('@', helpers.mentionAtCaret('@', 1), '陈澄'), { body: '@陈澄 ', caret: 4 })
})
test('submitted IDs are retained only while the matching mention token is present', () => {
  assert.equal(helpers.containsMention('请 @陈澄 完成', '陈澄'), true)
  assert.equal(helpers.containsMention('请 @陈澄，完成', '陈澄'), true)
  assert.equal(helpers.containsMention('请 陈澄 完成', '陈澄'), false)
  assert.equal(helpers.containsMention('请 @陈澄澄 完成', '陈澄'), false)
  assert.equal(helpers.containsMention('mail@陈澄', '陈澄'), false)
})
test('names containing spaces and repeated mentions are supported', () => {
  assert.equal(helpers.containsMention('请 @Alice Smith 检查', 'Alice Smith'), true)
  assert.equal(helpers.containsMention('mail@陈澄 或 @陈澄', '陈澄'), true)
})
// Test setup-state behavior with Vue's in-memory renderer; no DOM/browser or
// extra testing dependency is needed for controlled IDs and tab remounts.
const sfcSource = await readFile(new URL('../src/components/MentionComment.vue', import.meta.url), 'utf8')
const compiled = compileScript(parse(sfcSource).descriptor, { id: 'mention-state-test' })
const componentJS = ts.transpileModule(compiled.content, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText
const codeHelpers={}
new Function('require','exports',ts.transpileModule(await readFile(new URL('../src/codeHighlight.ts',import.meta.url),'utf8'),{compilerOptions:{module:ts.ModuleKind.CommonJS,target:ts.ScriptTarget.ES2022}}).outputText)(key=>key==='lowlight'?lowlight:null,codeHelpers)
const moduleExports = {}
const recentMentionsMock = { useRecentMentions: (directory, query) => ({ matches: Vue.computed(() => helpers.filterMentionMembers(directory(), query(), i18n.t)), usable: Vue.ref(true), accepts: id => directory().some(member => member.id === id && member.active !== false), remember: () => {}, badge: () => '' }) }
new Function('require', 'exports', componentJS)(id => { if (id === './CodeInsertTools.vue') return {default:{}}; if(id==='../codeHighlight')return codeHelpers; if(id==='../editorPaste')return {clipboardCodeText:text=>text,insideCodeFence:()=>false}; if (id === 'vue') return Vue; if (id === '../mentions') return helpers; if (id === '../i18n') return i18n; if (id === '../useRecentMentions') return recentMentionsMock; throw new Error('Unexpected test import: ' + id) }, moduleExports)
const MentionComment = moduleExports.default
MentionComment.render = () => null
const renderer = Vue.createRenderer({ createElement: () => ({}), createText: () => ({}), createComment: () => ({}), insert: () => {}, remove: () => {}, setText: () => {}, setElementText: () => {}, parentNode: () => null, nextSibling: () => null, patchProp: () => {} })
function mountComposer({ body = '', ids = [], directory = members, controlled = true, mode, names = {}, savedIds = [] } = {}) {
  const state = Vue.reactive({ body, ids, names, visible: true, directory })
  const submissions = []
  const root = Vue.defineComponent({ setup: () => () => state.visible ? Vue.h(MentionComment, { modelValue: state.body, mentionUserIds: controlled ? state.ids : undefined, mentionNames: mode === 'field' ? state.names : undefined, savedMentionUserIds: savedIds, mode, members: state.directory, 'onUpdate:modelValue': value => { state.body = value }, 'onUpdate:mentionUserIds': value => { if (controlled) state.ids = value }, 'onUpdate:mentionNames': value => { state.names = value }, onSubmit: value => submissions.push(value) }) : null })
  const vnode = Vue.h(root)
  const container = {}
  renderer.render(vnode, container)
  return { state, submissions, context: () => vnode.component.subTree.component.setupState, cleanup: () => renderer.render(null, container) }
}
async function testAsync(name, run) { await run(); count++; console.log('✓ ' + name) }
await testAsync('controlled mention IDs survive comment tab unmount and remount', async () => {
  const composer = mountComposer({ body: '@陈澄 检查', ids: ['u_1'] })
  composer.state.visible = false; await Vue.nextTick()
  composer.state.visible = true; await Vue.nextTick()
  composer.context().submit()
  assert.deepEqual(composer.submissions[0], { body: '@陈澄 检查', mentionUserIds: ['u_1'] })
  composer.cleanup()
})
await testAsync('deleting mention text updates parent IDs, and clear-on-success resets them', async () => {
  const composer = mountComposer({ body: '@陈澄 @苏禾 检查', ids: ['u_1', 'u_2'] })
  composer.state.body = '@苏禾 检查'; await Vue.nextTick()
  assert.deepEqual([...composer.state.ids], ['u_2'])
  composer.state.body = ''; await Vue.nextTick()
  assert.deepEqual([...composer.state.ids], [])
  composer.cleanup()
})
await testAsync('choosing a dropdown member synchronizes the parent text and IDs', async () => {
  const composer = mountComposer({ body: '@' })
  composer.context().caret = 1
  await composer.context().choose(members[0])
  assert.equal(composer.state.body, '@陈澄 ')
  assert.deepEqual([...composer.state.ids], ['u_1'])
  composer.context().submit()
  assert.deepEqual(composer.submissions[0].mentionUserIds, ['u_1'])
  composer.cleanup()
})
await testAsync('unknown or temporarily unloaded selected members block silent notification loss', async () => {
  const composer = mountComposer({ body: '@陈澄 检查', ids: ['u_1'], directory: [] })
  composer.context().submit()
  assert.equal(composer.submissions.length, 0)
  assert.deepEqual([...composer.state.ids], ['u_1'])
  composer.state.directory = members; await Vue.nextTick()
  composer.context().submit()
  assert.deepEqual(composer.submissions[0].mentionUserIds, ['u_1'])
  composer.cleanup()
})
await testAsync('same-name member selection notifies only the last explicitly selected account', async () => {
  const directory = [{ id: 'zhang_a', name: '张三', email: 'a@example.test', active: true }, { id: 'zhang_b', name: '张三', email: 'b@example.test', active: true }]
  const composer = mountComposer({ body: '@张三 @', ids: ['zhang_a'], directory })
  composer.context().caret = composer.state.body.length
  await composer.context().choose(directory[1])
  assert.deepEqual([...composer.state.ids], ['zhang_b'])
  composer.context().submit()
  assert.deepEqual(composer.submissions[0].mentionUserIds, ['zhang_b'])
  composer.cleanup()
})
await testAsync('uncontrolled usage keeps backward-compatible local ID state', async () => {
  const composer = mountComposer({ body: '@', controlled: false })
  composer.context().caret = 1
  await composer.context().choose(members[1])
  composer.context().submit()
  assert.deepEqual(composer.submissions[0].mentionUserIds, ['u_2'])
  composer.cleanup()
})
await testAsync('role labels and mention search react to language changes without remounting', async () => {
  testLocale.value = 'zh-CN'
  const composer = mountComposer({ body: '@engineer' })
  composer.context().caret = composer.state.body.length
  assert.equal(composer.context().roleLabel(members[0]), '前端工程师')
  assert.equal(composer.context().matches.length, 0)
  testLocale.value = 'en-US'; await Vue.nextTick()
  assert.equal(composer.context().roleLabel(members[0]), 'Frontend engineer')
  assert.deepEqual(composer.context().matches.map(member => member.id), ['u_1'])
  assert.equal(composer.state.body, '@engineer')
  composer.cleanup(); testLocale.value = 'zh-CN'
})
await testAsync('field Enter preserves newline behavior while dropdown Enter chooses only a member', async () => {
  const composer = mountComposer({ body: '正文第一行', mode: 'field' })
  let prevented = 0
  const event = { key: 'Enter', preventDefault: () => prevented++ }
  await composer.context().onKeydown(event)
  await composer.context().onKeydown({ ...event, ctrlKey: true })
  composer.context().submit()
  assert.equal(prevented, 0); assert.equal(composer.submissions.length, 0)
  composer.state.body = '@'; await Vue.nextTick()
  composer.context().caret = 1; composer.context().focused = true
  await composer.context().onKeydown(event)
  assert.equal(prevented, 1); assert.equal(composer.state.body, '@陈澄 ')
  assert.deepEqual([...composer.state.ids], ['u_1']); assert.equal(composer.state.names.u_1, '陈澄')
  assert.equal(composer.submissions.length, 0)
  composer.cleanup()
})
await testAsync('field IME Enter and no-match Enter do not consume text input', async () => {
  const composer = mountComposer({ body: '@不存在', mode: 'field' })
  composer.context().caret = composer.state.body.length; composer.context().focused = true
  let prevented = false
  await composer.context().onKeydown({ key: 'Enter', preventDefault: () => { prevented = true } })
  await composer.context().onKeydown({ key: 'Enter', isComposing: true, preventDefault: () => { prevented = true } })
  assert.equal(prevented, false); assert.deepEqual([...composer.state.ids], [])
  composer.cleanup()
})
await testAsync('saved field snapshots retain renamed and unavailable historical members across remount', async () => {
  const composer = mountComposer({ body: '@旧名 继续跟进', ids: ['u_1'], names: {u_1:'旧名'}, savedIds: ['u_1'], directory: [{id:'u_1',name:'新名',active:false}], mode:'field' })
  assert.deepEqual([...composer.state.ids], ['u_1']); assert.equal(composer.context().blockingMentionIds.length, 0)
  composer.state.visible = false; await Vue.nextTick(); composer.state.visible = true; await Vue.nextTick()
  assert.deepEqual([...composer.state.ids], ['u_1']); assert.equal(composer.context().mentions[0].name, '旧名')
  composer.state.body = '移除了原提及'; await Vue.nextTick()
  assert.deepEqual([...composer.state.ids], [])
  composer.cleanup()
})
await testAsync('field same-name selection keeps the last explicit identity and snapshot', async () => {
  const directory = [{id:'same_a',name:'同名',email:'a@test',active:true},{id:'same_b',name:'同名',email:'b@test',active:true}]
  const composer = mountComposer({body:'@同名 @',ids:['same_a'],names:{same_a:'同名'},directory,mode:'field'})
  composer.context().caret = composer.state.body.length
  await composer.context().choose(directory[1])
  assert.deepEqual([...composer.state.ids], ['same_b']); assert.deepEqual({...composer.state.names}, {same_b:'同名'})
  composer.cleanup()
})
test('field identity helpers preserve unknown directories but block newly unavailable recipients', () => {
  assert.deepEqual(helpers.normalizeMentionIds(['a',null,'a','',42]), ['a'])
  assert.deepEqual(helpers.retainMentionIds('@旧名 处理',['old'],[],{old:'旧名'}), ['old'])
  assert.deepEqual(helpers.retainMentionIds('删去提及',['old'],[],{old:'旧名'}), [])
  assert.deepEqual(helpers.unavailableNewMentionIds(['old','new'],[],['old']), ['new'])
})
console.log(`Passed ${count} mention tests.`)

import assert from 'node:assert/strict'
import { readFile, readdir } from 'node:fs/promises'
import ts from 'typescript'
import * as Vue from 'vue'
import { parse, compileScript, compileTemplate } from 'vue/compiler-sfc'
import { renderToString } from 'vue/server-renderer'

const root = new URL('../', import.meta.url)
async function read(path) { return readFile(new URL(path, root), 'utf8') }
async function loadPure(path) {
  const source = await read(path)
  const compiled = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.ESNext, target: ts.ScriptTarget.ES2022 } }).outputText
  return import('data:text/javascript;base64,' + Buffer.from(compiled).toString('base64'))
}
const localeFiles=(await readdir(new URL('src/locales/',root))).filter(name=>name.endsWith('.en.ts')).sort()
const messages = Object.assign({}, ...(await Promise.all(localeFiles.map(name=>loadPure('src/locales/'+name)))).map(module=>module.default))
const fields = await loadPure('src/requirementFields.ts')
const mentions = await loadPure('src/mentions.ts'),recentMembers=await loadPure('src/recentMembers.ts')
const locale = Vue.ref('zh-CN')
const i18n = { locale, t: (source, params = {}) => (locale.value === 'en-US' ? messages[source] ?? source : source).replace(/\{(\w+)\}/g, (token, key) => Object.hasOwn(params, key) ? String(params[key]) : token) }
let count = 0
async function test(name, run) { await run(); count++; console.log('✓ ' + name) }
const paths = ['src/views/Requirements.vue', 'src/views/Editor.vue', ...['MentionComment', 'MemberMultiSelect', 'RequirementWeights', 'RequirementTags', 'CustomFieldInputs'].map(name => 'src/components/' + name + '.vue')]

await test('all requirement templates compile and route static UI through translation', async () => {
  for (const path of paths) {
    const source = await read(path)
    const { descriptor, errors } = parse(source)
    assert.deepEqual(errors, [], path)
    const script = compileScript(descriptor, { id: path })
    const template = compileTemplate({ source: descriptor.template.content, filename: path, id: path, compilerOptions: { bindingMetadata: script.bindings } })
    assert.deepEqual(template.errors, [], path)
    function walk(node) {
      if (node.type === 2) assert.ok(!/[\u3400-\u9fff]/.test(node.content), path + ': untranslated visible text ' + node.content)
      if (node.type === 1) {
        for (const prop of node.props) if (prop.type === 6 && ['title', 'aria-label', 'placeholder'].includes(prop.name)) assert.ok(!/[\u3400-\u9fff]/.test(prop.value?.content || ''), path + ': untranslated accessible label')
        if (node.tag === 'option' && node.children.some(child => child.type === 5 && /\bt\(/.test(child.content.content))) assert.ok(node.props.some(prop => prop.name === 'value' || (prop.name === 'bind' && prop.arg?.content === 'value')), path + ': translated option must keep an explicit canonical value')
      }
      for (const child of node.children || []) walk(child)
    }
    walk(descriptor.template.ast)
  }
})
await test('every explicit requirement translation key has an English dictionary entry', async () => {
  for (const path of paths) {
    const source = await read(path)
    for (const match of source.matchAll(/\bt\('([^']*)'/g)) assert.ok(Object.hasOwn(messages, match[1]), path + ': missing key ' + match[1])
  }
})
async function component(name) {
  const picker=name==='RequirementWeights'?await component('MemberMultiSelect'):null
  const source = await read('src/components/' + name + '.vue')
  const descriptor = parse(source).descriptor
  const compiled = compileScript(descriptor, { id: 'i18n-test-' + name, inlineTemplate: true })
  const output = ts.transpileModule(compiled.content, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText
  const exports = {}
  new Function('require', 'exports', output)(id => {
    if (id === 'vue') return Vue
    if (id === '../i18n') return i18n
    if (id === '../requirementFields') return fields
    if (id === '../mentions') return mentions
    if (id === '../recentMembers') return recentMembers
    if (id === '../layoutScope') return {layoutScope:Vue.ref('t:u')}
    if (id === '../stores/workspace') return {useWorkspaceStore:()=>Vue.reactive({session:{tenant:{id:'t'},user:{id:'u'},project:{id:'p'}}})}
    if (id === './MemberMultiSelect.vue') return {default:picker}
    throw new Error('Unexpected component dependency: ' + id)
  }, exports)
  return exports.default
}
await test('weight UI renders both languages without translating selected member names', async () => {
  const Weight = await component('RequirementWeights')
  const weights = fields.emptyRoleWeights(); weights.frontend = { userId: 'business-name', value: 1.5 }
  const props = { modelValue: weights, members: [{ id: 'business-name', name: '产品', projectRole: 'frontend' }], readonly: true }
  locale.value = 'zh-CN'
  assert.match(await renderToString(Vue.createSSRApp({ render: () => Vue.h(Weight, props) })), /前端开发难度/)
  locale.value = 'en-US'
  const html = await renderToString(Vue.createSSRApp({ render: () => Vue.h(Weight, props) }))
  assert.match(html, /Frontend difficulty/)
  assert.match(html, /Total weight/)
  assert.match(html, /产品/)
  assert.ok(!html.includes('前端开发难度'))
})
await test('business label text stays original even when it matches a translated system phrase', async () => {
  locale.value = 'en-US'
  const Tags = await component('RequirementTags')
  const html = await renderToString(Vue.createSSRApp({ render: () => Vue.h(Tags, { modelValue: '产品需求', colors: { 产品需求: '#FFFFFF' }, readonly: true }) }))
  assert.match(html, /产品需求/)
  assert.ok(!html.includes('Product requirement'))
})
await test('normal owner pickers default to lead-owner help while role-weight pickers alone explain one-time difficulty',async()=>{
  locale.value='zh-CN';const Picker=await component('MemberMultiSelect'),props={modelValue:['a'],members:[{id:'a',name:'成员'}]}
  const normal=await renderToString(Vue.createSSRApp({render:()=>Vue.h(Picker,props)}));assert.match(normal,/首位为主负责人/);assert.ok(!normal.includes('难度数值只计算一次'))
  const unranked=await renderToString(Vue.createSSRApp({render:()=>Vue.h(Picker,{...props,showLead:false})}));assert.ok(!unranked.includes('难度数值只计算一次'));assert.ok(!unranked.includes('首位为主负责人'))
  const role=await renderToString(Vue.createSSRApp({render:()=>Vue.h(Picker,{...props,showLead:false,hint:'同一职能可绑定多人，难度数值只计算一次。'})}));assert.match(role,/难度数值只计算一次/);assert.ok(!role.includes('首位为主负责人'))
})
await test('parameterized copy renders in either language and preserves the supplied category name', () => {
  locale.value = 'zh-CN'
  assert.equal(i18n.t('确定删除「{name}」？', { name: '产品需求' }), '确定删除「产品需求」？')
  locale.value = 'en-US'
  assert.equal(i18n.t('确定删除「{name}」？', { name: '产品需求' }), 'Delete “产品需求”?')
  assert.equal(i18n.t('{count} 条需求', { count: 22 }), '22 requirements')
})
console.log(`Passed ${count} requirements i18n tests.`)

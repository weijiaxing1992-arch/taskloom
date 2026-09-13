import assert from 'node:assert/strict'
import { readFile, readdir } from 'node:fs/promises'
import ts from 'typescript'
import * as Vue from 'vue'
import { parse, compileScript, compileTemplate } from 'vue/compiler-sfc'

const root = new URL('../', import.meta.url)
const read = path => readFile(new URL(path, root), 'utf8')
const imports = { vue: Vue }
function evaluate(source) {
  const output = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText
  const exports = {}
  new Function('require', 'exports', output)(id => { if (!imports[id]) throw Error('Unexpected import '+id); return imports[id] }, exports)
  return exports
}
const localeFiles=(await readdir(new URL('src/locales/',root))).filter(name=>name.endsWith('.en.ts')).sort()
for (const name of localeFiles) imports['./locales/'+name.slice(0,-3)] = evaluate(await read('src/locales/'+name))
const i18n = evaluate(await read('src/i18n.ts'))
let count = 0
async function test(name, run) { await run(); count++; console.log('✓ '+name) }

await test('every discovered locale dictionary is registered in the runtime catalog',()=>{
  assert.ok(localeFiles.length>=7,'locale discovery must retain the existing catalogs')
  for(const name of localeFiles){const dictionary=imports['./locales/'+name.slice(0,-3)].default;assert.ok(dictionary&&typeof dictionary==='object',name);for(const [key,value] of Object.entries(dictionary)){assert.equal(typeof value,'string',name+': '+key);assert.ok(value.trim(),name+': empty translation '+key);assert.ok(Object.hasOwn(i18n.englishMessages,key),name+': unregistered translation '+key)}}
})

await test('locale switches are reactive without remounting or changing business values', () => {
  const title = '需求'
  const label = Vue.computed(() => i18n.t('需求'))
  i18n.setLocale('zh-CN'); assert.equal(label.value, '需求')
  i18n.setLocale('en-US'); assert.match(label.value, /Requirement/i)
  assert.equal(title, '需求')
  i18n.setLocale('bad-locale'); assert.equal(label.value, '需求')
})
await test('parameter interpolation preserves original names and zero counts', () => {
  i18n.setLocale('en-US')
  assert.equal(i18n.t('确定删除「{name}」？', { name: '产品需求' }), 'Delete “产品需求”?')
  assert.equal(i18n.t('{count} 条需求', { count: 0 }), '0 requirements')
  assert.equal(i18n.t('business text with {unknown}'), 'business text with {unknown}')
})
await test('date formats honor locale/timezone without shifting calendar-only dates', () => {
  i18n.applyLanguagePreferences({ locale: 'en-US', timezone: 'UTC' })
  assert.equal(i18n.formatDate('2026-09-03'), '09/03/2026')
  const utc = i18n.formatDate('2026-09-03T20:00:00Z')
  i18n.applyLanguagePreferences({ locale: 'zh-CN', timezone: 'Asia/Shanghai' })
  assert.match(i18n.formatDate('2026-09-03'), /2026.*09.*03/)
  assert.match(i18n.formatDate('2026-09-03T20:00:00Z'), /2026.*09.*04/)
  assert.notEqual(i18n.formatDate('2026-09-03T20:00:00Z'), utc)
  assert.equal(i18n.formatDate('invalid'), '—')
  assert.equal(i18n.formatDate(null), '—')
  assert.doesNotThrow(() => i18n.formatDate('2026-09-03T20:00:00Z', { dateStyle: 'medium', timeStyle: 'short' }))
  assert.equal(i18n.formatDate(Date.parse('2026-09-03T20:00:00Z')), i18n.formatDate('2026-09-03T20:00:00Z'))
  i18n.applyLanguagePreferences({timezone:'invalid'}); assert.equal(i18n.timezone.value,'Asia/Shanghai')
})
async function vueFiles(directory){const entries=await readdir(new URL(directory+'/',root),{withFileTypes:true});return (await Promise.all(entries.map(entry=>entry.isDirectory()?vueFiles(directory+'/'+entry.name):entry.name.endsWith('.vue')?[directory+'/'+entry.name]:[]))).flat()}
const paths = ['src/App.vue', ...await vueFiles('src/views'),...await vueFiles('src/components')].filter(x=>!x.endsWith('/Placeholder.vue'))
await test('all active Vue templates compile and localized options retain canonical values', async () => {
  for (const path of paths) {
    const {descriptor,errors}=parse(await read(path));assert.deepEqual(errors,[],path)
    const script=descriptor.scriptSetup ? compileScript(descriptor,{id:path}) : null
    const template=compileTemplate({source:descriptor.template.content,filename:path,id:path,compilerOptions:{bindingMetadata:script?.bindings}})
    assert.deepEqual(template.errors,[],path)
    function walk(node) {
      if(node.type===2) assert.ok(!/[\u3400-\u9fff]/.test(node.content) || node.content.trim()==='简体中文', path+': untranslated UI '+node.content)
      if(node.type===1) {
        for(const attr of node.props) if(attr.type===6 && ['title','aria-label','placeholder'].includes(attr.name)) assert.ok(!/[\u3400-\u9fff]/.test(attr.value?.content||''),path+': untranslated attribute')
        if(node.tag==='option'&&node.children.some(child=>child.type===5&&/\bt\(/.test(child.content.content))) assert.ok(node.props.some(prop=>prop.name==='value'||(prop.name==='bind'&&prop.arg?.content==='value')),path+': translated option must preserve its value')
      }
      for(const child of node.children||[])walk(child)
    }
    walk(descriptor.template.ast)
  }
})
await test('all explicit UI translation keys have an English entry', async () => {
  for(const path of paths) {
    for(const match of (await read(path)).matchAll(/\bt\((['"])([^'"\n]*)\1/g)) {
      if(/[\u3400-\u9fff]/.test(match[2])) assert.ok(Object.hasOwn(i18n.englishMessages,match[2]),path+': missing '+match[2])
    }
  }
})
await test('every literal branch of conditional UI translations is covered, including nested components',async()=>{
 function literals(expression){if(ts.isStringLiteral(expression)||ts.isNoSubstitutionTemplateLiteral(expression))return [expression.text];if(ts.isParenthesizedExpression(expression))return literals(expression.expression);if(ts.isConditionalExpression(expression))return [...literals(expression.whenTrue),...literals(expression.whenFalse)];return []}
 function inspect(source,path){const tree=ts.createSourceFile(path+'.ts',source,ts.ScriptTarget.Latest,true);function walk(node){if(ts.isCallExpression(node)&&ts.isIdentifier(node.expression)&&node.expression.text==='t'&&node.arguments[0])for(const key of literals(node.arguments[0]))if(/[\u3400-\u9fff]/.test(key))assert.ok(Object.hasOwn(i18n.englishMessages,key),path+': missing conditional key '+key);ts.forEachChild(node,walk)}walk(tree)}
 for(const path of paths){const {descriptor}=parse(await read(path));if(descriptor.scriptSetup)inspect(descriptor.scriptSetup.content,path);if(descriptor.script)inspect(descriptor.script.content,path);function walk(node){if(node.type===5)inspect(node.content.content,path);if(node.type===1)for(const prop of node.props)if(prop.type===7&&prop.exp)inspect(prop.exp.content,path);for(const child of node.children||[])walk(child)}if(descriptor.template)walk(descriptor.template.ast)}
})
await test('API sends selected language and preserves Headers-instance overrides', async () => {
  const apiSource=await read('src/api.ts')
  imports['./i18n']=i18n
  const requests=[]
  const apiExports={}
  const compiled=ts.transpileModule(apiSource,{compilerOptions:{module:ts.ModuleKind.CommonJS,target:ts.ScriptTarget.ES2022}}).outputText
  new Function('require','exports','fetch','localStorage','window',compiled)(id=>imports[id],apiExports,async(path,opts)=>{requests.push({path,...opts});return{ok:true,json:async()=>({items:[]})}}, {getItem:()=> 'test-project'}, {dispatchEvent:()=>{}})
  i18n.setLocale('en-US');await apiExports.api('/tests')
  assert.equal(requests[0].headers.get('Accept-Language'),'en-US')
  const headers=new Headers({'X-TaskLoom-Project':'explicit-project','Accept-Language':'zh-CN'})
  await apiExports.api('/tests',{headers})
  assert.equal(requests[1].headers.get('X-TaskLoom-Project'),'explicit-project')
  assert.equal(requests[1].headers.get('Accept-Language'),'zh-CN')
  assert.equal(headers.has('Content-Type'),false)
})
console.log(`Passed ${count} application i18n tests.`)

import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import ts from 'typescript'
import * as Vue from 'vue'
import { compileScript, parse } from 'vue/compiler-sfc'

const read = path => readFile(new URL('../' + path, import.meta.url), 'utf8')
async function pure(path) {
  const output = ts.transpileModule(await read(path), {compilerOptions:{module:ts.ModuleKind.ESNext,target:ts.ScriptTarget.ES2022}}).outputText
  return import('data:text/javascript;base64,' + Buffer.from(output).toString('base64'))
}
const mentions = await pure('src/mentions.ts'), query = await pure('src/workItemQuery.ts'), recent = await pure('src/recentMembers.ts')
const people = [
  {id:'front',name:'同名',department:'误写成后端',departmentIds:['dept-front'],departmentNames:['前端组'],active:true,isCurrent:true},
  {id:'both',name:'同名',email:'both@test.invalid',department:'后端组',departmentIds:['dept-back','dept-front'],departmentNames:['后端组','前端组'],active:true},
  {id:'back',name:'后端人员',department:'前端组',departmentIds:['dept-back'],departmentNames:['后端组'],active:true},
  {id:'legacy',name:'旧成员',department:'前端组',departmentIds:['dept-front'],departmentNames:['前端组'],active:false},
  {id:'text-only',name:'无真实部门',department:'前端组',active:true},
]
const i18n = {t: (text, params={}) => text.replace(/\{(\w+)\}/g, (all,key)=>String(params[key] ?? all))}
const flush = async () => { for(let i=0;i<8;i++) { await Promise.resolve();await Vue.nextTick() } }
const deferred=()=>{let resolve,reject;const promise=new Promise((yes,no)=>{resolve=yes;reject=no});return{resolve,reject,promise}}
const renderer=Vue.createRenderer({createElement:()=>({}),createText:()=>({}),createComment:()=>({}),insert:()=>{},remove:()=>{},setText:()=>{},setElementText:()=>{},parentNode:()=>null,nextSibling:()=>null,patchProp:()=>{}})
async function mount(name, initialProps, handler=async()=>({items:[]})) {
  const props=Vue.reactive(initialProps),storage=new Map([['devflow-project','p-a']]),window=new EventTarget(),calls=[],emits=[]
  const source=compileScript(parse(await read('src/components/'+name+'.vue')).descriptor,{id:'test-'+name}).content
  const code=ts.transpileModule(source,{compilerOptions:{module:ts.ModuleKind.CommonJS,target:ts.ScriptTarget.ES2022}}).outputText,exports={}
  const imports={vue:Vue,'../i18n':i18n,'../mentions':mentions,'../api':{api:async(path,options)=>{calls.push({path,options});return handler(path,options)}},'./MemberMultiSelect.vue':{default:{}},'./DatePicker.vue':{default:{}},'../recentMembers':recent,'../layoutScope':{layoutScope:Vue.ref('t:u')},'../stores/workspace':{useWorkspaceStore:()=>Vue.reactive({session:{tenant:{id:'t'},user:{id:'u'},project:{id:'p-a'}}})}}
  new Function('require','exports','window','localStorage',code)(id=>imports[id],exports,window,{getItem:key=>storage.get(key)??null})
  const Component=exports.default;Component.render=()=>null
  const root=Vue.h(Vue.defineComponent({setup:()=>()=>Vue.h(Component,{...props,'onUpdate:modelValue':value=>{emits.push(value);props.modelValue=value}})})),container={}
  renderer.render(root,container)
  return {props,storage,window,calls,emits,context:root.component.subTree.component.setupState,stop:()=>renderer.render(null,container)}
}
let count=0
async function test(name,run){await run();count++;console.log('✓ '+name)}

await test('directory ID membership, including secondary departments, is authoritative; display text cannot grant access',()=>{
  assert.deepEqual(mentions.membersInDepartment(people,'dept-front').map(x=>x.id),['front','both'])
  assert.deepEqual(mentions.membersInDepartment(people,'前端组'),[])
  assert.deepEqual(mentions.memberDepartments(people).map(x=>x.id).sort(),['dept-back','dept-front'])
  assert.deepEqual(mentions.filterMentionMembers(people,'前端组').map(x=>x.id),['front','both','back','text-only'])
})
await test('restricted picker keeps history, rejects out-of-range toggle and select-me, and department bulk is intersection-only',async()=>{
  const m=await mount('MemberMultiSelect',{modelValue:['legacy','back'],members:people,departmentId:'dept-front',currentUserId:'back'})
  const c=m.context;assert.deepEqual(c.options.map(x=>x.id),['front','both']);assert.equal(c.currentMember,undefined)
  assert.deepEqual(c.selected.map(x=>x.historical),[true,true]);c.selectMe();c.toggle('text-only');assert.equal(m.emits.length,0)
  c.department='dept-back';c.addDepartment();await flush();assert.deepEqual(m.props.modelValue,['legacy','back','both'])
  c.remove('back');await flush();c.toggle('back');await flush();assert.deepEqual(m.props.modelValue,['legacy','both'])
  m.props.departmentId='dept-back';await flush();assert.deepEqual(m.props.modelValue,['legacy','both']);assert.equal(c.department,'dept-back');m.stop()
})
await test('same-named departments remain distinct, while disabled picker cannot modify IDs',async()=>{
  const members=[{id:'a',name:'A',departmentIds:['d1'],departmentNames:['同名部门'],active:true},{id:'b',name:'B',departmentIds:['d2'],departmentNames:['同名部门'],active:true}]
  const m=await mount('MemberMultiSelect',{modelValue:[],members,disabled:false});m.context.department='d2';m.context.addDepartment();await flush();assert.deepEqual(m.props.modelValue,['b'])
  m.props.disabled=true;await flush();m.context.toggle('a');m.context.remove('b');m.context.addDepartment();await flush();assert.deepEqual(m.props.modelValue,['b']);m.stop()
})
const defs=[{id:1,key:'reviewer',name:'审核人员',type:'user',departmentId:'dept-front',enabled:true},{id:2,key:'reviewers',name:'审核小组',type:'users',departmentId:'dept-front',enabled:true},{id:3,key:'difficulty',name:'难度',type:'number',enabled:true}]
await test('read-only, saving, loading and identity-invalid custom fields cannot emit writes',async()=>{
  const m=await mount('CustomFieldInputs',{objectType:'requirement',modelValue:{reviewer:'front',difficulty:20},disabled:true},async path=>({items:path==='/members'?people:defs}))
  await flush();m.context.set('reviewer','back');m.context.setNumber(defs[2],{target:{value:'50'}});assert.equal(m.emits.length,0)
  m.props.disabled=false;await flush();m.context.setNumber(defs[2],{target:{value:'',validity:{badInput:true}}});assert.equal(m.emits.length,0);assert.equal(m.props.modelValue.difficulty,20)
  m.context.set('reviewer','front');assert.equal(m.emits.length,1)
  m.context.loading=true;await flush();m.context.set('reviewer','back');assert.equal(m.emits.length,1)
  m.context.loading=false;m.window.dispatchEvent(new Event('devflow-identity-changed'));await flush();m.context.set('reviewer','back');assert.equal(m.emits.length,1);m.stop()
})
await test('personnel grouping separates user fields without changing saved values',async()=>{
  const original={reviewer:'front',reviewers:['legacy'],difficulty:0}
  const m=await mount('CustomFieldInputs',{objectType:'requirement',modelValue:original,visibleTypes:['user','users'],excludedKeys:['reviewer']},async path=>({items:path==='/members'?people:defs}))
  await flush();assert.deepEqual(m.context.visibleDefs.map(x=>x.key),['reviewers']);assert.equal(m.emits.length,0)
  m.props.visibleTypes=undefined;m.props.excludedTypes=['user','users'];await flush()
  assert.deepEqual(m.context.visibleDefs.map(x=>x.key),['difficulty']);assert.deepEqual(m.props.modelValue,original);assert.equal(m.emits.length,0);m.stop()
})
await test('custom user inputs persist stable IDs, retain original names, and never emit while directory loads',async()=>{
  const m=await mount('CustomFieldInputs',{objectType:'requirement',modelValue:{reviewer:'旧姓名',reviewers:['legacy']}},async path=>({items:path==='/members'?people:defs}))
  await flush();assert.equal(m.calls.length,2);assert.equal(m.emits.length,0)
  assert.deepEqual(m.context.candidates(defs[0]).map(x=>x.id),['front','both']);assert.equal(m.context.unavailableMember(defs[0]),true);assert.equal(m.context.memberLabel('front'),'同名')
  m.context.set('reviewer','front');await flush();assert.equal(m.props.modelValue.reviewer,'front');assert.equal(m.context.unavailableMember(defs[0]),false)
  m.context.setNumber(defs[2],{target:{value:''}});await flush();assert.equal(m.props.modelValue.difficulty,null)
  assert.deepEqual(m.props.modelValue.reviewers,['legacy']);m.stop()
})
await test('directory load failures are retryable without clearing saved values',async()=>{
  let fail=true
  const m=await mount('CustomFieldInputs',{objectType:'requirement',modelValue:{reviewer:'旧姓名'}},async path=>{if(path==='/members'&&fail)throw Error('offline');return{items:path==='/members'?people:defs}})
  await flush();assert.equal(m.context.memberError,'offline');assert.equal(m.context.loading,false);assert.equal(m.props.modelValue.reviewer,'旧姓名');assert.equal(m.emits.length,0)
  fail=false;await m.context.load();await flush();assert.equal(m.context.memberError,'');assert.equal(m.context.members.length,people.length);m.stop()
})
await test('project switch and identity loss invalidate late field/member requests without business writes',async()=>{
  const old=deferred();let first=true
  const m=await mount('CustomFieldInputs',{objectType:'requirement',modelValue:{reviewer:'keep'}},async(path,options)=>{if(first){first=false;return old.promise}return {items:path==='/members'?people:defs}})
  m.storage.set('devflow-project','p-b');m.window.dispatchEvent(new Event('devflow-project-changed'));await flush();assert.equal(m.context.defs.length,3)
  old.resolve({items:[{id:99,key:'old',enabled:true,type:'text'}]});await flush();assert.equal(m.context.defs[0].id,1);assert.equal(m.emits.length,0)
  assert.equal(m.calls.at(-1).options.headers['X-TaskLoom-Project'],'p-b')
  m.window.dispatchEvent(new Event('devflow-identity-changed'));await flush();assert.equal(m.context.defs.length,0);assert.equal(m.context.members.length,0)
  const before=m.calls.length;await m.context.load();assert.equal(m.calls.length,before);assert.equal(m.props.modelValue.reviewer,'keep');m.stop()
})
await test('unmount invalidates pending request and removes identity listeners',async()=>{
  const pending=deferred(),m=await mount('CustomFieldInputs',{objectType:'requirement',modelValue:{}},()=>pending.promise)
  m.stop();pending.resolve({items:defs});await flush();assert.equal(m.context.defs.length,0);const count=m.calls.length;m.window.dispatchEvent(new Event('devflow-project-changed'));await flush();assert.equal(m.calls.length,count)
})
await test('typed list filters use stable IDs and include all multi-person values with department-limited suggestions',()=>{
  const fields=query.workItemFields(defs,people);const multi=fields.find(x=>x.key==='cf.reviewers'),single=fields.find(x=>x.key==='cf.reviewer')
  assert.equal(multi.kind,'multi');assert.deepEqual(multi.options.map(x=>x.value),['front','both']);assert.deepEqual(single.options.map(x=>x.value),['front','both'])
  const rows=[{id:1,customFields:{reviewers:['front','both']}},{id:2,customFields:{reviewers:['front']}}]
  assert.deepEqual(query.queryWorkItems(rows,fields,[{field:'cf.reviewers',operator:'includes',value:'both'}],'code','asc').map(x=>x.id),[1])
  assert.equal(query.workItemValue({customFields:{reviewer:'旧姓名'}},'cf.reviewer'),'旧姓名')
})
console.log(`Passed ${count} field-department regression tests.`)

import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import ts from 'typescript'
import * as Vue from 'vue'
import { compileScript, compileTemplate, parse } from 'vue/compiler-sfc'

const read=path=>readFile(new URL('../'+path,import.meta.url),'utf8')
const transpile=source=>ts.transpileModule(source,{compilerOptions:{module:ts.ModuleKind.CommonJS,target:ts.ScriptTarget.ES2022}}).outputText
const fields={},workflow={}
new Function('exports',transpile(await read('src/requirementFields.ts')))(fields)
new Function('require','exports',transpile(await read('src/requirementWorkflow.ts')))(()=>fields,workflow)
const flush=async()=>{for(let i=0;i<8;i++){await Promise.resolve();await Vue.nextTick()}}
const deferred=()=>{let resolve,reject;const promise=new Promise((yes,no)=>{resolve=yes;reject=no});return{resolve,reject,promise}}
const node=(tag,text='')=>({tag,text,props:{},children:[],parent:null,focused:false,focus(){this.focused=true},contains(value){return this===value||this.children.some(child=>child.contains?.(value))}})
const renderer=Vue.createRenderer({createElement:tag=>node(tag),createText:text=>node('#text',text),createComment:()=>node('#comment'),insert(child,parent,anchor){if(child.parent){const index=child.parent.children.indexOf(child);if(index>=0)child.parent.children.splice(index,1)}child.parent=parent;const index=anchor?parent.children.indexOf(anchor):-1;if(index<0)parent.children.push(child);else parent.children.splice(index,0,child)},remove(child){if(child.parent)child.parent.children.splice(child.parent.children.indexOf(child),1)},setText:(child,text)=>{child.text=text},setElementText:(child,text)=>{child.text=text;child.children=[]},parentNode:child=>child.parent,nextSibling:child=>child.parent?.children[child.parent.children.indexOf(child)+1]||null,patchProp:(child,key,_old,value)=>{child.props[key]=value}})
function descendants(root){return[root,...root.children.flatMap(descendants)]}
function text(root){return root.text+root.children.map(text).join('')}
const states=[{id:1,key:'草稿',name:'草稿',color:'#64748B',category:'todo',enabled:true,sortOrder:1,system:true},{id:2,key:'custom_review',name:'开发中',color:'#2563EB',category:'doing',enabled:true,sortOrder:2,system:false},{id:3,key:'done_custom',name:'交付给客户',color:'#059669',category:'done',enabled:true,sortOrder:3,system:false}]
async function mount(name,initialProps,handler=async()=>({currentStatus:'草稿',allowedTransitions:['custom_review']})) {
  const props=Vue.reactive(initialProps),locale=Vue.ref('zh-CN'),storage=new Map([['devflow-project','p-a']]),window=new EventTarget(),document=new EventTarget(),events=[],calls=[]
  const t=(source,params={})=>(locale.value==='en-US'?({'草稿':'Draft','开发中':'In development','产品':'Product'}[source]||source):source).replace(/\{(\w+)\}/g,(all,key)=>String(params[key]??all))
  const descriptor=parse(await read('src/components/'+name+'.vue')).descriptor
  const script=compileScript(descriptor,{id:'workflow-'+name}),template=compileTemplate({source:descriptor.template.content,filename:name+'.vue',id:'workflow-'+name,compilerOptions:{bindingMetadata:script.bindings}})
  assert.deepEqual(template.errors,[])
  // State/authorization tests use deterministic primitives. Real Reka integration
  // is separately covered by the stack SSR tests and browser interaction checks.
  const pass=Vue.defineComponent({setup:(_,ctx)=>()=>Vue.h('div',ctx.attrs,ctx.slots.default?.())})
  const button=Vue.defineComponent({setup:(_,ctx)=>()=>Vue.h('button',ctx.attrs,ctx.slots.default?.())})
  const imports={vue:{...Vue,withDirectives:value=>value},'./ui/popover':{Popover:pass,PopoverTrigger:pass,PopoverContent:pass},'./ui/button':{Button:button},'../i18n':{t},'../requirementWorkflow':workflow,'../api':{api:async(path,options)=>{calls.push({path,options});return handler(path,options)}}},module={},view={}
  const require=id=>imports[id]
  new Function('require','exports','window','document','localStorage',transpile(script.content))(require,module,window,document,{getItem:key=>storage.get(key)??null})
  new Function('require','exports',transpile(template.code))(require,view)
  const Component=module.default;Component.render=view.render
  const root=Vue.h(Vue.defineComponent({setup:()=>()=>Vue.h(Component,{...props,'onUpdate:modelValue':value=>{events.push(value);props.modelValue=value},onChange:value=>events.push(value)})})),container=node('root')
  renderer.render(root,container)
  return{props,locale,storage,window,document,events,calls,container,context:root.component.subTree.component.setupState,stop:()=>renderer.render(null,container)}
}
let count=0
async function test(name,run){await run();count++;console.log('✓ '+name)}

await test('mounted status filter keeps multiple canonical values with OR semantics and clears by emitted selection only',async()=>{
  const m=await mount('StatusMultiSelect',{modelValue:['草稿','草稿'],options:workflow.workflowOptions(states)});const c=m.context
  c.toggle('custom_review');await flush();assert.deepEqual(m.props.modelValue,['草稿','custom_review'])
  assert.equal(workflow.matchesSelectedStatuses('草稿',m.props.modelValue),true);assert.equal(workflow.matchesSelectedStatuses('custom_review',m.props.modelValue),true);assert.equal(workflow.matchesSelectedStatuses('done_custom',m.props.modelValue),false)
  c.open=true;await flush();const clear=descendants(m.container).find(child=>child.tag==='button'&&text(child)==='清空');clear.props.onClick();await flush();assert.deepEqual(m.props.modelValue,[]);assert.equal(workflow.matchesSelectedStatuses('anything',[]),true);assert.equal(m.calls.length,0);m.stop()
})
await test('disabled status picker cannot toggle and Escape closes only its own panel while restoring trigger focus',async()=>{
  const m=await mount('StatusMultiSelect',{modelValue:['草稿'],options:workflow.workflowOptions(states),disabled:true});m.context.toggle('custom_review');assert.equal(m.events.length,0)
  m.props.disabled=false;m.context.open=true;await flush();let prevented=false,stopped=false;m.context.escape({key:'Escape',preventDefault:()=>{prevented=true},stopPropagation:()=>{stopped=true}});await flush();assert.equal(m.context.open,false);assert.equal(m.context.trigger.focused,true);assert(prevented&&stopped)
  m.context.open=true;const event=new Event('pointerdown');m.document.dispatchEvent(event);await flush();assert.equal(m.context.open,false);m.stop()
})
await test('mounted labels react to language but never translate configured custom state names or change option values',async()=>{
  const m=await mount('StatusMultiSelect',{modelValue:['custom_review'],options:workflow.workflowOptions(states)});m.context.open=true;await flush();assert.match(text(m.container),/草稿/)
  m.locale.value='en-US';await flush();assert.match(text(m.container),/Draft/);assert.match(text(m.container),/开发中/);assert.ok(!text(m.container).includes('In development'))
  m.context.query='开发中';await flush();assert.deepEqual(m.context.filtered.map(x=>x.value),['custom_review']);assert.deepEqual(m.props.modelValue,['custom_review']);m.stop()
})
await test('authorized transitions are read-only discovery; selection resets to persisted status and emits only allowed destinations',async()=>{
  const m=await mount('RequirementTransition',{requirementId:10,status:'草稿',definitions:states});await flush()
  assert.deepEqual(m.context.allowed,['custom_review']);assert.equal(m.calls[0].options.headers['X-DevFlow-Project'],'p-a')
  const input={value:'custom_review'};m.context.change({target:input});assert.equal(input.value,'草稿');assert.deepEqual(m.events,['custom_review'])
  m.context.change({target:{value:'done_custom'}});m.context.change({target:{value:'草稿'}});assert.equal(m.events.length,1)
  assert(m.calls.every(call=>!call.options.method));m.locale.value='en-US';await flush();assert.match(text(m.container),/Draft/);assert.match(text(m.container),/开发中/);assert.ok(!text(m.container).includes('In development'));m.stop()
})
await test('permission network failure fails closed, preserves current status, and retry never silently transitions',async()=>{
  let fail=true;const m=await mount('RequirementTransition',{requirementId:10,status:'草稿',definitions:states},async()=>{if(fail)throw Error('offline');return{currentStatus:'草稿',allowedTransitions:['custom_review']}})
  await flush();assert.equal(m.context.error,'offline');assert.deepEqual(m.context.allowed,[]);assert.equal(descendants(m.container).find(x=>x.props['aria-label']==='需求工作状态').props.disabled,true)
  m.context.change({target:{value:'custom_review'}});assert.equal(m.events.length,0);fail=false;await m.context.load();await flush();assert.equal(m.context.error,'');assert.deepEqual(m.context.allowed,['custom_review']);assert.equal(m.events.length,0);assert.equal(m.props.status,'草稿');m.stop()
})
await test('late permissions for a previous requirement cannot authorize or overwrite the current requirement',async()=>{
  const first=deferred(),second=deferred();const m=await mount('RequirementTransition',{requirementId:10,status:'草稿',definitions:states},path=>path.includes('/10/')?first.promise:second.promise)
  m.props.requirementId=11;m.props.status='custom_review';await flush();second.resolve({currentStatus:'custom_review',allowedTransitions:['done_custom']});await flush();first.resolve({currentStatus:'草稿',allowedTransitions:['custom_review']});await flush()
  assert.deepEqual(m.context.allowed,['done_custom']);assert.equal(m.context.error,'');assert.equal(m.context.loading,false);m.context.change({target:{value:'custom_review'}});assert.equal(m.events.length,0);m.stop()
})
await test('mismatched persisted status and malformed transitions fail closed rather than accepting stale permissions',async()=>{
  let data={currentStatus:'custom_review',allowedTransitions:['done_custom']};const m=await mount('RequirementTransition',{requirementId:10,status:'草稿',definitions:states},async()=>data)
  await flush();assert.equal(m.context.error,'需求状态已变化，请刷新后重试');assert.deepEqual(m.context.allowed,[])
  data={currentStatus:'草稿',allowedTransitions:{anything:true}};await m.context.load();await flush();assert.deepEqual(m.context.allowed,[]);m.context.change({target:{value:'done_custom'}});assert.equal(m.events.length,0);m.stop()
})
await test('refreshKey and workflow/focus events recheck permissions and block selection while refresh is pending',async()=>{
  let waiting=null;const m=await mount('RequirementTransition',{requirementId:10,status:'草稿',definitions:states,refreshKey:0},async()=>waiting?waiting.promise:{currentStatus:'草稿',allowedTransitions:['custom_review']})
  await flush();waiting=deferred();m.props.refreshKey=1;await flush();assert.equal(m.context.loading,true);assert.deepEqual(m.context.allowed,[]);m.context.change({target:{value:'custom_review'}});assert.equal(m.events.length,0)
  waiting.resolve({currentStatus:'草稿',allowedTransitions:[]});await flush();waiting=null;m.window.dispatchEvent(new Event('devflow-workflow-changed'));await flush();m.window.dispatchEvent(new Event('focus'));await flush();assert.equal(m.calls.length,4);assert.equal(m.events.length,0);m.stop()
})
await test('identity loss invalidates pending permissions and prevents both retry fetch and change events',async()=>{
  for(const event of ['devflow-identity-changed','devflow-auth-expired']) {
    const waiting=deferred(),m=await mount('RequirementTransition',{requirementId:10,status:'草稿',definitions:states},()=>waiting.promise)
    m.window.dispatchEvent(new Event(event));waiting.resolve({currentStatus:'草稿',allowedTransitions:['custom_review']});await flush();assert.deepEqual(m.context.allowed,[]);assert.equal(m.context.loading,false)
    const calls=m.calls.length;await m.context.load();m.context.change({target:{value:'custom_review'}});assert.equal(m.calls.length,calls);assert.equal(m.events.length,0);m.stop()
  }
})
await test('project switch ignores old responses and refuses to apply old project transition rights',async()=>{
  const waiting=deferred(),m=await mount('RequirementTransition',{requirementId:10,status:'草稿',definitions:states},()=>waiting.promise)
  m.storage.set('devflow-project','p-b');m.window.dispatchEvent(new Event('devflow-project-changed'));waiting.resolve({currentStatus:'草稿',allowedTransitions:['custom_review']});await flush();assert.deepEqual(m.context.allowed,[]);assert.equal(m.context.loading,false);assert.equal(descendants(m.container).find(x=>x.props['aria-label']==='需求工作状态').props.disabled,true)
  const calls=m.calls.length;await m.context.load();m.context.change({target:{value:'custom_review'}});assert.equal(m.calls.length,calls);assert.equal(m.events.length,0);m.stop()
})
await test('unmount cleans event listeners and an in-flight response cannot update disposed controls',async()=>{
  const waiting=deferred(),m=await mount('RequirementTransition',{requirementId:10,status:'草稿',definitions:states},()=>waiting.promise)
  m.stop();waiting.resolve({currentStatus:'草稿',allowedTransitions:['custom_review']});await flush();assert.deepEqual(m.context.allowed,[])
  const calls=m.calls.length;for(const type of ['focus','devflow-workflow-changed','devflow-identity-changed'])m.window.dispatchEvent(new Event(type));await flush();assert.equal(m.calls.length,calls)
})
await test('parent disabled state rejects transitions even when an earlier permission request succeeded',async()=>{
  const m=await mount('RequirementTransition',{requirementId:10,status:'草稿',definitions:states,disabled:false});await flush();m.props.disabled=true;await flush();m.context.change({target:{value:'custom_review'}});assert.equal(m.events.length,0);assert.equal(descendants(m.container).find(x=>x.props['aria-label']==='需求工作状态').props.disabled,true);m.stop()
})
await test('historical unknown custom status selections remain business text in English',async()=>{
  const m=await mount('StatusMultiSelect',{modelValue:['产品'],options:[]});m.locale.value='en-US';m.context.open=true;await flush();assert.match(text(m.container),/产品/);assert.ok(!text(m.container).includes('Product'));m.stop()
})
console.log(`Passed ${count} mounted workflow runtime tests.`)

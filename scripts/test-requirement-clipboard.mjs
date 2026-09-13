import assert from 'node:assert/strict'
import {readFile} from 'node:fs/promises'
import ts from 'typescript'
import * as Vue from 'vue'
import {parse,compileScript,compileTemplate} from 'vue/compiler-sfc'

const read=path=>readFile(new URL('../'+path,import.meta.url),'utf8')
const transpile=source=>ts.transpileModule(source,{compilerOptions:{module:ts.ModuleKind.CommonJS,target:ts.ScriptTarget.ES2022}}).outputText
const clipboard={};new Function('exports',transpile(await read('src/requirementClipboard.ts')))(clipboard)
const dictionary={};new Function('exports',transpile(await read('src/locales/requirements.en.ts')))(dictionary)
const flush=async()=>{for(let i=0;i<8;i++){await Promise.resolve();await Vue.nextTick()}}
const deferred=()=>{let resolve,reject;const promise=new Promise((yes,no)=>{resolve=yes;reject=no});return{resolve,reject,promise}}
const emptyWeights=()=>Object.fromEntries(clipboard.clipboardRoles.map(key=>[key,{userId:'',userIds:[],value:null}]))
const complete=(extra={})=>({id:17,code:'REQ-0017',title:'业务标题 unchanged',createdAt:'2026-09-03T01:02:03Z',assignee:'',assigneeUserIds:[],assignees:[],owner:'',ownerUserIds:[],owners:[],roleWeights:emptyWeights(),...extra})
const node=(tag,text='')=>({tag,text,props:{},children:[],parent:null,open:false,focused:false,selected:false,focus(){this.focused=true},select(){this.selected=true},showModal(){this.open=true},close(){this.open=false},setAttribute(key,value){this.props[key]=value;if(key==='open')this.open=true}})
const renderer=Vue.createRenderer({createElement:tag=>node(tag),createText:text=>node('#text',text),createComment:()=>node('#comment'),insert(child,parent,anchor){if(child.parent){const index=child.parent.children.indexOf(child);if(index>=0)child.parent.children.splice(index,1)}child.parent=parent;const index=anchor?parent.children.indexOf(anchor):-1;if(index<0)parent.children.push(child);else parent.children.splice(index,0,child)},remove(child){if(child.parent)child.parent.children.splice(child.parent.children.indexOf(child),1)},setText:(child,text)=>{child.text=text},setElementText:(child,text)=>{child.text=text;child.children=[]},parentNode:child=>child.parent,nextSibling:child=>child.parent?.children[child.parent.children.indexOf(child)+1]||null,patchProp:(child,key,_old,value)=>{child.props[key]=value}})
const descendants=root=>[root,...root.children.flatMap(descendants)]
const text=root=>root.text+root.children.map(text).join('')
const descriptor=parse(await read('src/components/RequirementCode.vue')).descriptor
const script=compileScript(descriptor,{id:'requirement-code'}),template=compileTemplate({source:descriptor.template.content,filename:'RequirementCode.vue',id:'requirement-code',compilerOptions:{bindingMetadata:script.bindings}})
assert.deepEqual(template.errors,[])
async function mount(initialProps,handler=async()=>{throw Error('Unexpected request')},write=async()=>{}){
  const props=Vue.reactive(initialProps),locale=Vue.ref('zh-CN'),layoutScope=Vue.ref('tenant-a:user-a'),window=new EventTarget(),storage=new Map([['devflow-project','p-a']]),calls=[],writes=[],events=[],timers=new Map(),formats=[]
  let timerID=0,now=0,stopped=false
  const t=(source,params={})=>(locale.value==='en-US'?(dictionary.default[source]||source):source).replace(/\{(\w+)\}/g,(all,key)=>String(params[key]??all))
  const navigator=write===null?{}:{clipboard:{writeText:async value=>{writes.push(value);await write(value)}}}
  const imports={vue:Vue,'../api':{api:async(path,options)=>{calls.push({path,options});return handler(path,options)}},'../i18n':{t,formatDate:(value,options)=>{formats.push({value,options});return new Date(value).toISOString()}},'../layoutScope':{layoutScope},'../requirementClipboard':clipboard}
  const module={},view={},require=id=>{assert(id in imports,'Unexpected import '+id);return imports[id]}
  new Function('require','exports','window','localStorage','navigator','setTimeout','clearTimeout',transpile(script.content))(require,module,window,{getItem:key=>storage.get(key)??null},navigator,(fn,delay=0)=>{const id=++timerID;timers.set(id,{fn,at:now+delay});return id},id=>timers.delete(id))
  new Function('require','exports',transpile(template.code))(require,view);module.default.render=view.render
  const root=Vue.h(Vue.defineComponent({setup:()=>()=>Vue.h(module.default,{...props,onOpen:()=>events.push('open')})})),container=node('root');renderer.render(root,container)
  return{props,locale,layoutScope,window,storage,calls,writes,events,timers,formats,navigator,container,context:root.component.subTree.component.setupState,advance:async ms=>{now+=ms;for(const [id,timer] of [...timers])if(timer.at<=now){timers.delete(id);timer.fn()}await flush()},stop:()=>{if(!stopped){stopped=true;renderer.render(null,container)}}}
}
const event=(extra={})=>({detail:1,preventDefault(){this.prevented=true},stopPropagation(){this.stopped=true},...extra})
let count=0
async function test(name,run){await run();count++;console.log('✓ '+name)}

await test('summary preserves every assigned engineer, product manager and exact creation timestamp',()=>{
  const item=complete({code:'REQ-0007',title:'需求\nAPI integration',assigneeUserIds:['a','b'],assignees:[{id:'a',name:'张一'},{id:'b',name:'Alex'}],ownerUserIds:['p1'],owners:[{id:'p1',name:'产品甲'}],roleWeights:{...emptyWeights(),frontend:{userIds:['a','c']},backend:{userId:'b'},algorithm:{userIds:['d']},ui:{userIds:['e']},product:{userIds:['p1','p2']}}})
  const members=[['a','张一'],['b','Alex'],['c','前端乙'],['d','算法甲'],['e','设计甲'],['p1','产品甲'],['p2','产品乙'],['secret','不相关成员']].map(([id,name])=>({id,name}))
  const value=clipboard.requirementClipboard({...item,description:'private body',customFields:{token:'secret token'}},members)
  assert.match(value,/需求编号：REQ-0007\n标题：需求\n  API integration/);assert.match(value,/处理人：张一、Alex/);assert.match(value,/前端工程师：张一、前端乙/);assert.match(value,/后端工程师：Alex/);assert.match(value,/算法工程师：算法甲/);assert.match(value,/UI 工程师：设计甲/);assert.match(value,/产品经理：产品甲、产品乙/);assert.match(value,/创建时间：2026-09-03T01:02:03.000Z$/)
  assert.ok(!/不相关成员|private body|secret token/.test(value));assert.equal(clipboard.clipboardNeedsMembers(item,members),false);assert.equal(clipboard.completeClipboardRequirement(item),true)
})
await test('same-name members remain distinct, legacy names and genuinely unassigned roles are not lost',()=>{
  const item=complete({assigneeUserIds:['a','b'],assignees:[{id:'a',name:'同名'},{id:'b',name:'同名'}],owner:'Legacy PM; Partner',roleWeights:{...emptyWeights(),frontend:{userName:'旧工程师'},backend:{userId:'removed-id'}}})
  const value=clipboard.requirementClipboard(item)
  assert.match(value,/处理人：同名（a）、同名（b）/);assert.match(value,/前端工程师：旧工程师/);assert.match(value,/后端工程师：姓名暂不可用（removed-id）/);assert.match(value,/算法工程师：未分配/);assert.match(value,/产品经理：Legacy PM; Partner/);assert.equal(clipboard.clipboardNeedsMembers(item),true)
  const legacy=clipboard.requirementClipboard(complete({assignee:'老王、老李'}));assert.match(legacy,/处理人：老王、老李/)
})
await test('translation changes labels only, never translates user titles or names and never invents a creation date',()=>{
  const item=complete({title:'前端工程师',assignees:[{name:'产品经理'}]}),value=clipboard.requirementClipboard(item,[],{translate:key=>dictionary.default[key]||key,formatDate:value=>'DATE '+value})
  assert.match(value,/Title：前端工程师/);assert.match(value,/Assignee：产品经理/);assert.match(value,/DATE 2026-09-03T01:02:03Z/)
  assert.match(clipboard.requirementClipboard(complete({createdAt:''})),/创建时间暂不可用/)
  for(const item of [{id:1,code:'REQ-1',title:'partial'},complete({createdAt:'2026-09-03'}),complete({roleWeights:{}})])assert.equal(clipboard.completeClipboardRequirement(item),false)
})
await test('complete local data never loads background objects or the member directory, and dates include seconds',async()=>{
  const m=await mount({requirement:complete()});await flush();assert.equal(m.calls.length,0);await m.context.copy();await flush();assert.equal(m.calls.length,0);assert.equal(m.writes.length,1);assert.equal(m.context.feedback,'已复制需求协作摘要');assert.equal(m.context.failed,false);assert.equal(m.formats[0].options.second,'2-digit');assert.equal(m.formats[0].options.timeZoneName,'short');m.stop()
})
await test('single click opens after the double-click interval; double-click copies once without opening',async()=>{
  const m=await mount({requirement:complete()});let button=descendants(m.container).find(x=>x.tag==='button')
  button.props.onClick(event());await m.advance(400);assert.deepEqual(m.events,[]);await m.advance(150);assert.deepEqual(m.events,['open'])
  m.events.length=0;button.props.onClick(event());await m.advance(150);button.props.onMousedown(event({detail:2}));button.props.onClick(event({detail:2}));button.props.onDblclick(event({detail:2}));await flush();await m.advance(600)
  assert.deepEqual(m.events,[]);assert.equal(m.writes.length,1);assert.equal(m.calls.length,0)
  const wrapper=descendants(m.container).find(x=>x.props.class==='requirement-code-control'),e=event();wrapper.props.onClick(e);assert.equal(e.stopped,true);m.stop()
})
await test('keyboard activation opens immediately, Ctrl/Cmd+C copies, and detail headers never reopen themselves',async()=>{
  const m=await mount({requirement:complete()});m.context.click(event({detail:0}));assert.deepEqual(m.events,['open'])
  for(const modifier of ['ctrlKey','metaKey']){const e=event({key:'C',[modifier]:true});m.context.keydown(e);await flush();assert(e.prevented&&e.stopped)}assert.equal(m.writes.length,2)
  m.props.openOnClick=false;await flush();m.context.click(event({detail:0}));m.context.click(event());await m.advance(600);assert.deepEqual(m.events,['open']);m.stop()
})
await test('explicit copy affordance is labelled and works without navigating',async()=>{
  const m=await mount({requirement:complete()});const button=descendants(m.container).find(x=>x.props.class==='requirement-code-copy');assert.match(button.props['aria-label'],/REQ-0017/);assert.equal(button.props.type,'button');button.props.onClick();await flush();assert.equal(m.writes.length,1);assert.equal(m.events.length,0)
  m.locale.value='en-US';await flush();assert.match(text(m.container),/Requirement collaboration summary copied/);assert.match(descendants(m.container).find(x=>x.props.class==='requirement-code-value').props['aria-label'],/double-click/);m.stop()
})
await test('partial cross-project work rows fetch authorized detail and only the bound names on demand',async()=>{
  const detail=complete({projectId:'p-b',assigneeUserIds:['a'],assignees:[{id:'a',name:'Alice'}],roleWeights:{...emptyWeights(),frontend:{userIds:['f1','f2']}}})
  const m=await mount({requirement:{id:17,code:'REQ-0017',type:'需求'},projectId:'p-b'},async path=>path==='/requirements/17'?detail:{items:[{id:'f1',name:'Frontend One'},{id:'f2',name:'Frontend Two'},{id:'secret',name:'Unbound private name'}]})
  await flush();assert.equal(m.calls.length,0);await m.context.copy();assert.deepEqual(m.calls.map(x=>x.path),['/requirements/17','/members']);assert(m.calls.every(call=>call.options.headers['X-TaskLoom-Project']==='p-b'&&!call.options.method));assert.match(m.writes[0],/Frontend One、Frontend Two/);assert.ok(!m.writes[0].includes('Unbound private name'));assert.equal(m.storage.get('devflow-project'),'p-a');m.stop()
})
await test('pending copy coalesces repeat clicks and failure never copies fake unassigned data',async()=>{
  const pending=deferred(),m=await mount({requirement:{id:17}},()=>pending.promise);const first=m.context.copy();await m.context.copy();assert.equal(m.calls.length,1);assert.equal(m.context.busy,true);pending.reject(Error('permission denied'));await first
  assert.equal(m.writes.length,0);assert.equal(m.context.feedback,'permission denied');assert.equal(m.context.failed,true);assert.equal(m.context.busy,false);assert.equal(m.context.fallbackText,'');m.stop()
})
await test('invalid IDs and non-requirement codes never request or copy a requirement',async()=>{
  for(const requirement of [{id:0},{id:'n/a'},{id:17,objectType:'defect'},{id:17,type:'缺陷'},{id:17,type:'测试用例'}]){const m=await mount({requirement});await m.context.copy();assert.equal(m.calls.length,0);assert.equal(m.writes.length,0);assert.equal(m.context.failed,true);m.stop()}
})
await test('mismatched detail/project and incomplete responses fail visibly without copying a partial summary',async()=>{
  for(const response of [complete({id:18}),complete({projectId:'p-other'}),{id:17,title:'partial'}]){const m=await mount({requirement:{id:17}},async()=>response);await m.context.copy();assert.equal(m.writes.length,0);assert.equal(m.context.failed,true);assert.equal(m.context.busy,false);m.stop()}
  const m=await mount({requirement:complete({projectId:'p-b'}),projectId:'p-a'});await m.context.copy();assert.equal(m.calls.length,0);assert.equal(m.writes.length,0);assert.match(m.context.feedback,/当前项目不一致/);m.stop()
})
await test('member-directory failure or malformed data does not masquerade as unassigned roles',async()=>{
  for(const response of [null,{items:null}]){const m=await mount({requirement:complete({roleWeights:{...emptyWeights(),frontend:{userIds:['f1']}}})},async()=>{if(response===null)throw Error('members offline');return response});await m.context.copy();assert.equal(m.writes.length,0);assert.equal(m.context.failed,true);assert.equal(m.context.fallbackText,'');m.stop()}
})
await test('project and identity changes abort in-flight copy; late results cannot reach the clipboard',async()=>{
  for(const trigger of ['devflow-project-changed','devflow-identity-changed','devflow-auth-expired','layoutScope']){
    const pending=deferred(),m=await mount({requirement:{id:17}},()=>pending.promise);const operation=m.context.copy()
    if(trigger==='layoutScope')m.layoutScope.value='tenant-a:another-user';else{if(trigger==='devflow-project-changed')m.storage.set('devflow-project','p-b');m.window.dispatchEvent(new Event(trigger))}
    pending.resolve(complete());await operation;await flush();assert.equal(m.calls[0].options.signal.aborted,true);assert.equal(m.writes.length,0);assert.equal(m.context.busy,false);assert.equal(m.context.fallbackText,'');await m.context.copy();assert.equal(m.calls.length,1);m.stop()
  }
})
await test('requirement replacement and disabled state cancel old data while allowing a later safe copy',async()=>{
  for(const replace of [m=>{m.props.requirement=complete({id:18,code:'REQ-0018'})},m=>{m.props.disabled=true}]){
    const pending=deferred(),m=await mount({requirement:{id:17}},()=>pending.promise);const operation=m.context.copy();replace(m);await flush();pending.resolve(complete());await operation
    assert.equal(m.writes.length,0);assert.equal(m.context.busy,false);assert.equal(m.calls[0].options.signal.aborted,true);m.props.requirement=complete({id:18,code:'REQ-0018'});m.props.disabled=false;await flush();await m.context.copy();assert.match(m.writes[0],/REQ-0018/);m.stop()
  }
})
await test('unmount clears pending click timers and prevents late asynchronous clipboard writes',async()=>{
  const pending=deferred(),m=await mount({requirement:{id:17}},()=>pending.promise);m.context.click(event());const operation=m.context.copy();m.stop();pending.resolve(complete());await operation;await m.advance(600);assert.equal(m.timers.size,0);assert.equal(m.events.length,0);assert.equal(m.writes.length,0);assert.equal(m.calls[0].options.signal.aborted,true)
})
await test('missing clipboard API exposes selected readonly summary and closing it never opens the requirement',async()=>{
  const m=await mount({requirement:complete()},undefined,null);await m.context.copy();await flush();assert.equal(m.context.failed,true);assert.match(m.context.fallbackText,/REQ-0017/);const dialog=descendants(m.container).find(x=>x.tag==='dialog'),textarea=descendants(m.container).find(x=>x.tag==='textarea');assert.equal(dialog.open,true);assert.ok(Object.hasOwn(textarea.props,'readonly'));assert.equal(textarea.selected,true);assert.equal(textarea.focused,true);assert.match(textarea.props['aria-label'],/摘要/);assert.ok(!m.context.feedback.includes('已复制'))
  m.context.closeFallback();await flush();assert.equal(m.context.fallbackText,'');assert.equal(m.events.length,0);assert.equal(m.context.trigger.focused,true);m.stop()
})
await test('rejected clipboard write offers a retry with the same complete text without refetching',async()=>{
  let rejected=true;const m=await mount({requirement:complete()},undefined,async()=>{if(rejected)throw Error('not allowed')});await m.context.copy();await flush();const value=m.context.fallbackText;assert.ok(value);assert.equal(m.context.failed,true);rejected=false;await m.context.retry();await flush();assert.deepEqual(m.writes,[value,value]);assert.equal(m.context.fallbackText,'');assert.equal(m.context.feedback,'已复制需求协作摘要');assert.equal(m.context.failed,false);assert.equal(m.calls.length,0);m.stop()
})
await test('scope invalidation clears visible fallback text and refuses manual retry',async()=>{
  const m=await mount({requirement:complete()},undefined,null);await m.context.copy();assert.ok(m.context.fallbackText);m.window.dispatchEvent(new Event('devflow-identity-changed'));await flush();assert.equal(m.context.fallbackText,'');await m.context.retry();assert.equal(m.writes.length,0);assert.equal(descendants(m.container).some(x=>x.tag==='textarea'),false);m.stop()
})
await test('all four views integrate only genuine requirement codes without nesting buttons or intercepting other objects',async()=>{
  for(const [name,minimum] of [['Requirements',5],['Sprints',2],['MyWork',1],['Search',1]]){
    const source=await read('src/views/'+name+'.vue'),{descriptor}=parse(source),nodes=[]
    function walk(node,parents=[]){if(node.tag==='RequirementCode')nodes.push({node,parents});for(const child of node.children||[])walk(child,[...parents,node])}walk(descriptor.template.ast)
    assert.equal(nodes.length,minimum,name)
    for(const {node,parents} of nodes){assert(!parents.some(parent=>['button','a','router-link'].includes(parent.tag)),name+' must not nest interactive controls');const expression=attribute=>node.props.find(p=>p.name==='bind'&&p.arg?.content===attribute)?.exp?.content;assert.ok(expression('requirement'),name+' binds the actual item');if(name!=='Requirements')assert.ok(expression('project-id'),name+' has explicit row project scope');if(name==='Sprints')assert.ok(node.props.find(p=>p.name==='if')?.exp?.content.includes("x.objectType==='requirement'"));if(['MyWork','Search'].includes(name))assert.ok(node.props.find(p=>p.name==='if')?.exp?.content.includes("x.type==='需求'"))}
    assert.ok(source.includes('RequirementCode'))
  }
})
await test('clipboard identity checking remains backed by the verified account and expected-user API boundary',async()=>{
  const source=await read('src/api.ts');assert.match(source,/X-TaskLoom-Expected-User/);assert.match(await read('src/components/RequirementCode.vue'),/request\.identity===layoutScope\.value/)
  const scope={};new Function('require','exports',transpile(await read('src/layoutScope.ts')))(id=>{assert.equal(id,'vue');return Vue},scope)
  assert.equal(scope.layoutScope.value,'');scope.applyLayoutScope('tenant:a','user:b');assert.equal(scope.layoutScope.value,'tenant%3Aa:user%3Ab')
  scope.applyLayoutScope('tenant:a','another-user');assert.equal(scope.layoutScope.value,'tenant%3Aa:another-user');scope.clearLayoutScope();assert.equal(scope.layoutScope.value,'')
  for(const values of [[null,'user'],['tenant',''],[' ','user'],['tenant',{}]]){scope.applyLayoutScope(...values);assert.equal(scope.layoutScope.value,'')}
})
console.log(`Passed ${count} requirement clipboard tests.`)

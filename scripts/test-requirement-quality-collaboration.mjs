import assert from 'node:assert/strict'
import {readFile} from 'node:fs/promises'
import ts from 'typescript'
import * as Vue from 'vue'
import {parse,compileScript,compileTemplate} from 'vue/compiler-sfc'
import {workflow} from './workflow-test-support.mjs'

const read=path=>readFile(new URL('../'+path,import.meta.url),'utf8')
const js=source=>ts.transpileModule(source,{compilerOptions:{module:ts.ModuleKind.CommonJS,target:ts.ScriptTarget.ES2022}}).outputText
const evaluate=(source,imports={},globals={})=>{const exports={};new Function('require','exports',...Object.keys(globals),js(source))(id=>{assert(id in imports,'Unexpected import '+id);return imports[id]},exports,...Object.values(globals));return exports}
const people=evaluate(await read('src/defectPeople.ts')),scopeSource=await read('src/components/settingsScope.ts'),sources={}
for(const name of ['DefectComposer','RequirementLinks','RequirementPicker'])sources[name]=await read('src/components/'+name+'.vue')
sources.Defects=await read('src/views/Defects.vue')
const words={};for(const name of ['core','requirements','modules','settings'])Object.assign(words,evaluate(await read('src/locales/'+name+'.en.ts')).default)
const flush=async()=>{for(let i=0;i<30;i++){await Promise.resolve();await Vue.nextTick()}}
const deferred=()=>{let resolve,reject;const promise=new Promise((a,b)=>{resolve=a;reject=b});return{resolve,reject,promise}}
class Element {
 constructor(tag,text=''){Object.assign(this,{tag,text,props:{},children:[],parent:null,isConnected:true,valid:true})}
 focus(options){this.focused=true;this.focusOptions=options}
 reportValidity(){return this.valid}
 contains(value){return this===value||this.children.some(child=>child.contains(value))}
 closest(selector){return selector.includes('inert')&&this.props.inert?this:this.parent?.closest(selector)||null}
 getClientRects(){return[{}]}
 querySelectorAll(){return this.children.flatMap(descendants).filter(child=>(['button','input','select','textarea'].includes(child.tag)||child.props.tabindex==='0')&&!child.props.disabled)}
}
const descendants=node=>[node,...node.children.flatMap(descendants)],text=node=>node.text+node.children.map(text).join('')
const renderer=Vue.createRenderer({createElement:tag=>new Element(tag),createText:text=>new Element('#text',text),createComment:()=>new Element('#comment'),insert(child,parent,anchor){if(child.parent){const i=child.parent.children.indexOf(child);if(i>=0)child.parent.children.splice(i,1)}child.parent=parent;const i=anchor?parent.children.indexOf(anchor):-1;if(i<0)parent.children.push(child);else parent.children.splice(i,0,child)},remove(child){child.isConnected=false;if(child.parent)child.parent.children.splice(child.parent.children.indexOf(child),1)},setText:(child,value)=>{child.text=value},setElementText:(child,value)=>{child.text=value;child.children=[]},parentNode:child=>child.parent,nextSibling:child=>child.parent?.children[child.parent.children.indexOf(child)+1]||null,patchProp:(child,key,_old,value)=>{child.props[key]=value}})
const defaultMembers=[{id:'u_me',name:'产品',projectRole:'product',active:true},{id:'qa',name:'同名',projectRole:'qa',active:true},{id:'dev',name:'同名',projectRole:'backend',active:true},{id:'inactive',name:'Disabled QA',projectRole:'qa',active:false}]
const requirement=id=>({id,code:'REQ-'+id,title:'User title '+id,status:'custom-id',statusName:'草稿',statusSystem:false,statusColor:'#124abc',statusCategory:'todo'})
const originalDefect=()=>({id:71,code:'BUG-71',title:'Existing defect',description:'Original description',steps:'Existing steps',actual:'Observed',expected:'Expected',environment:'Production',foundVersion:'1.0',fixVersion:'1.1',severity:'一般',priority:'P1',status:'待验证',assigneeUserId:'inactive',assignee:'Disabled engineer',verifierUserId:'',verifier:'Historical tester',sprint:'closed',requirementId:17,tags:'Tag A,Tag B',customFields:{history:'Preserve',points:0},sourceExecutionId:51,progress:80,estimatedHours:3,actualHours:2})
async function mount(name,{props={},override=()=>undefined,members=defaultMembers,currentId='u_me',role='product',defect=originalDefect(),query={}}={}){
 const calls=[],events=[],guards=[],window=new EventTarget(),document={activeElement:new Element('button')},storage=new Map([['devflow-project','project-a']]),localStorage={getItem:key=>storage.get(key)||null},config={confirm:false},locale=Vue.ref('zh-CN'),state=Vue.reactive(props),links=new Map([[17,[requirement(18)]],[18,[requirement(17)]]])
 window.confirm=()=>config.confirm
 const route=Vue.reactive({path:'/defects',query}),navigations=[],router={replace:async target=>{navigations.push(target);route.query=target.query||{}}}
 const api=async(path,options={})=>{calls.push({path,options});const custom=override(path,options);if(custom!==undefined)return custom
  if(path==='/session')return{user:{id:currentId,role}}
  if(path==='/members')return{items:structuredClone(members)}
  if(path.startsWith('/field-definitions'))return{items:[]}
  if(path==='/sprints')return{items:[{id:1,name:'123 full name',status:'进行中'},{id:2,name:'closed',status:'已完成'},{id:3,name:'22',status:'规划中'}]}
  if(path.startsWith('/requirements?')||path==='/requirements')return{items:[requirement(17),requirement(18),requirement(19)]}
  if(/^\/requirements\/\d+$/.test(path)){const id=Number(path.split('/').at(-1));if(![17,18,19].includes(id))throw Error('关联需求无效，请关闭后重新打开');return requirement(id)}
  if(/^\/requirements\/\d+\/links(?:\/\d+)?$/.test(path)){const id=Number(path.split('/')[2]);if(options.method==='POST'){const target=JSON.parse(options.body).requirementId;links.set(id,[...(links.get(id)||[]),requirement(target)])}if(options.method==='DELETE')links.set(id,(links.get(id)||[]).filter(item=>item.id!==Number(path.split('/').at(-1))));return{items:structuredClone(links.get(id)||[])}}
  if(path==='/defects'&&options.method==='POST')return{id:71,code:'BUG-71',...JSON.parse(options.body)}
  if(path==='/defects/71'){if(options.method==='PATCH')defect={...defect,...JSON.parse(options.body)};return structuredClone(defect)}
  if(path.startsWith('/defects?'))return{items:[structuredClone(defect)]}
  if(path==='/defects/71/comments'||path==='/defects/71/activities')return{items:[]}
  throw Error('Unexpected path '+path)
 }
 const memberStub=Vue.defineComponent({name:'MemberMultiSelect',props:{modelValue:Array,members:Array,snapshots:Array,legacyName:String,inputId:String,label:String,disabled:Boolean,single:Boolean},emits:['update:modelValue'],setup:(props,{emit})=>()=>Vue.h('section',{'data-member-picker':props.label,'data-single':props.single},[
  ...(props.modelValue||[]).map(id=>Vue.h('span',{},(props.members||[]).find(item=>item.id===id)?.name||props.snapshots?.find(item=>item.id===id)?.name||id)),
  (props.modelValue||[]).some(id=>!(props.members||[]).some(item=>item.id===id&&item.active))?Vue.h('small',{},'历史成员'):null,props.legacyName?Vue.h('span',{},props.legacyName):null,
  Vue.h('input',{id:props.inputId,'aria-label':props.label,disabled:props.disabled}),
  ...(props.members||[]).filter(item=>item.active).map(item=>Vue.h('button',{type:'button','data-member-id':item.id,disabled:props.disabled,onClick:()=>emit('update:modelValue',[item.id])},item.name)),
  Vue.h('button',{type:'button','data-clear-member':true,disabled:props.disabled,onClick:()=>emit('update:modelValue',[])},'Clear'),
 ])})
 const globals={window,document,localStorage,HTMLElement:Element},settings=evaluate(scopeSource,{vue:Vue,'../api':{api}},globals),stub=child=>child.endsWith('CodeTextEditor.vue')?Vue.defineComponent({props:{inputId:String,modelValue:String,rows:Number},emits:['update:modelValue'],setup:(p,{emit})=>()=>Vue.h('textarea',{id:p.inputId,rows:p.rows,value:p.modelValue,onInput:event=>emit('update:modelValue',event.target.value)})}):child.endsWith('MemberMultiSelect.vue')?memberStub:Vue.defineComponent({name:child,inheritAttrs:false,setup:(_props,{slots})=>()=>Vue.h('section',{'data-stub':child},slots.default?.())})
 const imports={vue:{...Vue,withDirectives:value=>value},'vue-router':{useRoute:()=>route,useRouter:()=>router,onBeforeRouteLeave:guard=>guards.push(guard),onBeforeRouteUpdate:guard=>guards.push(guard)},'../i18n':{locale,formatDate:value=>String(value),t:(source,params={})=>(locale.value==='en-US'?words[source]||source:source).replace(/\{(\w+)\}/g,(all,key)=>String(params[key]??all))},'../defectPeople':people,'../requirementWorkflow':workflow,'./settingsScope':settings,'../components/settingsScope':settings}
 const descriptor=parse(sources[name],{filename:name+'.vue'}).descriptor,script=compileScript(descriptor,{id:'quality-'+name}),template=compileTemplate({source:descriptor.template.content,filename:name+'.vue',id:'quality-'+name,compilerOptions:{bindingMetadata:script.bindings}});assert.deepEqual(template.errors,[])
 for(const match of script.content.matchAll(/from\s+["']([^"']+\.vue)["']/g))imports[match[1]]={default:stub(match[1])}
 const module=evaluate(script.content,imports,globals),view=evaluate(template.code,imports);module.default.render=view.render
 const app=renderer.createApp({render:()=>Vue.h(module.default,{...state,onCreated:item=>events.push({type:'created',item}),onSaved:item=>events.push({type:'saved',item}),onCancel:()=>events.push({type:'cancel'}),onOpen:id=>events.push({type:'open',id}),onCount:count=>events.push({type:'count',count}),'onUpdate:modelValue':id=>events.push({type:'selected',id}),onChange:item=>events.push({type:'change',item})})}),container=new Element('root');app.component('router-link',stub('router-link'));app.mount(container);await flush()
 return{app,context:app._instance.subTree.component.setupState,container,props:state,calls,events,guards,window,document,storage,config,locale,links,route,navigations,stop:()=>app.unmount()}
}
let count=0
async function test(name,run){await run();count++;console.log('✓ '+name)}
await test('bound defect creation uses the full active iteration and stable assignee/verifier IDs',async()=>{
 const m=await mount('DefectComposer',{props:{requirementId:17,requirementTitle:'用户业务名称',initialSprint:'123 full name'}}),c=m.context
 assert.equal(c.form.sprint,'123 full name');assert.equal(c.form.verifierUserId,'qa');assert.equal(c.dirty,false);assert.equal(c.activeMembers.length,3);assert.equal(m.calls.some(call=>call.path==='/requirements'),false)
 c.form.title='New linked bug';c.form.assigneeUserId='dev';c.form.requirementId=18;await c.save();const sent=m.calls.find(call=>call.options.method==='POST'),body=JSON.parse(sent.options.body)
 assert.equal(sent.path,'/defects');assert.equal(body.requirementId,17);assert.equal(body.assigneeUserId,'dev');assert.equal(body.assignee,'同名');assert.equal(body.verifierUserId,'qa');assert.equal(body.verifier,'同名');assert.equal(m.events[0].item.requirementId,17);assert.equal(c.dirty,false);assert(m.calls.every(call=>call.options.headers.get('X-TaskLoom-Project')==='project-a'));assert.equal(m.calls.some(call=>call.options.method==='PATCH'),false);m.stop()
})
await test('default verifier prefers the current QA, otherwise only a single active QA; never arbitrarily assigns a group',async()=>{
 const many=[...defaultMembers,{id:'qa2',name:'QA Two',projectRole:'qa',active:true}]
 for(const [currentId,expected] of [['qa2','qa2'],['u_me','']]){const m=await mount('DefectComposer',{props:{requirementId:17},members:many,currentId});assert.equal(m.context.form.verifierUserId,expected);m.stop()}
 const m=await mount('DefectComposer',{props:{requirementId:17},members:defaultMembers.filter(member=>member.id!=='qa')});assert.equal(m.context.form.verifierUserId,'');m.stop()
})
await test('closed or unavailable iterations fall back explicitly to backlog without inventing an assignment',async()=>{
 for(const initialSprint of ['closed','unknown']){const m=await mount('DefectComposer',{props:{requirementId:17,initialSprint}});assert.equal(m.context.form.sprint,'待规划');assert.match(m.context.notice,/已结束或不可用/);assert.deepEqual(m.context.availableSprints.map(item=>item.name),['123 full name','22']);m.stop()}
})
await test('standalone composer selects an optional authorized requirement and readonly actors cannot post',async()=>{
 const m=await mount('DefectComposer');m.context.form.title='Independent defect';m.context.form.requirementId=19;await m.context.save();assert.equal(m.events[0].item.requirementId,19);m.stop()
 const viewer=await mount('DefectComposer',{role:'viewer',props:{requirementId:17}});viewer.context.form.title='Cannot save';await viewer.context.save();assert.equal(viewer.calls.some(call=>call.options.method==='POST'),false);assert(descendants(viewer.container).find(node=>node.tag==='fieldset').props.disabled);assert.match(viewer.context.error,/只读/);viewer.stop()
})
await test('metadata failure is visible, retry preserves valid loading defaults, and malformed inputs fail closed',async()=>{
 let fail=true;const m=await mount('DefectComposer',{override:path=>path==='/members'&&fail?Promise.reject(Error('members unavailable')):undefined,props:{requirementId:17}})
 assert.equal(m.context.initialized,false);assert.equal(m.context.error,'members unavailable');assert.equal(m.context.loading,false);assert.match(text(m.container),/重新加载/);fail=false;await m.context.load();assert.equal(m.context.initialized,true);assert.equal(m.context.form.verifierUserId,'qa');m.stop()
 const invalid=await mount('DefectComposer',{props:{requirementId:0}});assert.equal(invalid.calls.length,0);assert.equal(invalid.context.initialized,false);invalid.stop()
})
await test('validation rejects unavailable people, closed iterations and invalid optional links without a mutation',async()=>{
 const m=await mount('DefectComposer'),c=m.context;c.form.title='Valid title';c.form.assigneeUserId='inactive';await c.save();assert.match(c.error,/已不可用/);c.form.assigneeUserId='';c.form.sprint='closed';await c.save();assert.match(c.error,/完整迭代名称/);c.form.sprint='待规划';c.form.requirementId=999;await c.save();assert.match(c.error,/关联需求无效/);c.form.requirementId=null;c.formElement.valid=false;await c.save();assert.equal(m.calls.some(call=>call.options.method==='POST'),false);m.stop()
})
await test('failed defect save retains every draft and blocks duplicate submissions and navigation while pending',async()=>{
 const pending=deferred(),m=await mount('DefectComposer',{props:{requirementId:17},override:(path,options)=>options.method==='POST'?pending.promise:undefined}),c=m.context;c.form.title='Keep me';c.form.steps='step 1';const saving=c.save();await flush();assert.equal(c.saving,true);assert.equal(await c.requestClose(),false);assert(m.guards.every(guard=>guard()===false));await c.save();assert.equal(m.calls.filter(call=>call.options.method==='POST').length,1)
 pending.reject(Error('server refused'));await saving;assert.equal(c.error,'server refused');assert.equal(c.form.title,'Keep me');assert.equal(c.form.steps,'step 1');assert.equal(c.dirty,true);assert.equal(m.events.length,0);assert.equal(await c.requestClose(),false);m.config.confirm=true;assert.equal(await c.requestClose(),true);assert.equal(m.events[0].type,'cancel');m.stop()
})
await test('account/project changes abort pending defect requests and cannot emit a stale creation',async()=>{
 for(const event of ['devflow-identity-changed','devflow-project-changed','devflow-auth-expired']){const pending=deferred(),m=await mount('DefectComposer',{props:{requirementId:17},override:(path,options)=>options.method==='POST'?pending.promise:undefined}),c=m.context;c.form.title='Bound draft';const saving=c.save();m.window.dispatchEvent(new Event(event));pending.resolve({id:71,requirementId:17});await saving;assert.equal(m.events.length,0);assert.equal(m.calls.at(-1).options.signal.aborted,true);assert.equal(c.scope.locked.value,true);await c.save();assert.equal(m.calls.filter(call=>call.options.method==='POST').length,1);m.stop()}
})
await test('changing the bound requirement or unmounting ignores a late successful response',async()=>{
 for(const action of ['replace','unmount']){const pending=deferred(),m=await mount('DefectComposer',{props:{requirementId:17},override:(path,options)=>options.method==='POST'?pending.promise:undefined});m.context.form.title='Original target';const saving=m.context.save();if(action==='replace'){m.props.requirementId=18;await flush()}else m.stop();pending.resolve({id:71,requirementId:17});await saving;assert.equal(m.events.length,0);if(action==='replace')m.stop()}
})
await test('composer labels switch language without translating requirement titles and use accessible labeled inputs',async()=>{
 const m=await mount('DefectComposer',{props:{requirementId:17,requirementTitle:'创建缺陷'}});m.locale.value='en-US';await flush();assert.match(text(m.container),/Create defect/);assert.match(text(m.container),/创建缺陷/);const nodes=descendants(m.container),labels=nodes.filter(node=>node.tag==='label').map(node=>node.props.for)
 for(const input of nodes.filter(node=>['input','textarea','select'].includes(node.tag)))assert(labels.includes(input.props.id),input.tag+' is labeled');assert.equal(nodes.filter(node=>node.tag==='textarea')[0].props.rows,4);m.stop()
})
await test('links load only the scoped requirement and preserve a custom status label verbatim in English',async()=>{
 const m=await mount('RequirementLinks',{props:{requirementId:17,canEdit:true}});assert.equal(m.context.items[0].id,18);assert.equal(m.calls[0].path,'/requirements/17/links');m.locale.value='en-US';await flush();assert.match(text(m.container),/草稿/);m.context.open(m.context.items[0]);assert.deepEqual(m.events.at(-1),{type:'open',id:18});m.stop()
})
await test('link picker excludes itself and existing links; mutations only affect the relation resource',async()=>{
 const m=await mount('RequirementLinks',{props:{requirementId:17,canEdit:true}}),c=m.context;c.showPicker();await flush();assert.deepEqual(c.matches.map(item=>item.id),[19]);await c.change(17);assert.equal(m.calls.filter(call=>call.options.method).length,0);await c.change(19);assert.equal(c.picker,false);const sent=m.calls.find(call=>call.options.method==='POST');assert.deepEqual(JSON.parse(sent.options.body),{requirementId:19});assert.equal(sent.path,'/requirements/17/links');assert.equal(c.items.length,2);await c.change(19,true);assert.equal(c.items.length,1);assert.equal(m.calls.find(call=>call.options.method==='DELETE').path,'/requirements/17/links/19');assert.equal(m.calls.some(call=>call.options.method==='PATCH'),false);m.stop()
})
await test('readonly link views cannot search or mutate; empty and loading failures remain actionable',async()=>{
 const m=await mount('RequirementLinks',{props:{requirementId:17,canEdit:false}});m.context.showPicker();await m.context.change(19);assert.equal(m.context.picker,false);assert.equal(m.calls.length,1);assert.equal(descendants(m.container).some(node=>node.props.class==='btn primary compact'),false);m.stop()
 let fail=true;const offline=await mount('RequirementLinks',{props:{requirementId:17,canEdit:true},override:path=>path.endsWith('/links')?(fail?Promise.reject(Error('links offline')):{items:[]}):undefined});assert.equal(offline.context.error,'links offline');fail=false;await offline.context.load();assert.equal(offline.context.error,'');assert.match(text(offline.container),/暂无关联需求/);offline.stop()
})
await test('failed linking retains picker/search and does not invent a relationship; mutation guards coalesce duplicate clicks',async()=>{
 const pending=deferred(),m=await mount('RequirementLinks',{props:{requirementId:17,canEdit:true},override:(path,options)=>options.method==='POST'?pending.promise:undefined}),c=m.context;c.showPicker();await flush();c.query='keep query';const saving=c.change(19);await flush();await c.change(19);assert(m.guards.every(guard=>guard()===false));assert.equal(m.calls.filter(call=>call.options.method==='POST').length,1);pending.reject(Error('link refused'));await saving;assert.equal(c.error,'link refused');assert.equal(c.picker,true);assert.equal(c.query,'keep query');assert.equal(c.items.length,1);m.stop()
})
await test('late link/search responses cannot contaminate another requirement or a changed account',async()=>{
 const pending=deferred(),m=await mount('RequirementLinks',{props:{requirementId:17,canEdit:true},override:path=>path==='/requirements/17/links'?pending.promise:undefined});m.props.requirementId=18;await flush();assert.equal(m.context.items[0].id,17);pending.resolve({items:[requirement(99)]});await flush();assert.deepEqual(m.context.items.map(item=>item.id),[17]);m.stop()
 const result=deferred(),search=await mount('RequirementLinks',{props:{requirementId:17,canEdit:true},override:path=>path.startsWith('/requirements?')?result.promise:undefined});search.context.showPicker();await flush();search.window.dispatchEvent(new Event('devflow-identity-changed'));result.resolve({items:[requirement(99)]});await flush();assert.equal(search.context.items.length,0);assert.equal(search.context.candidates.length,0);assert.equal(search.calls.at(-1).options.signal.aborted,true);search.stop()
})
await test('full defect editing preserves inactive people, historical names, closed iteration and untouched custom fields',async()=>{
 const original=originalDefect(),m=await mount('DefectComposer',{props:{defectId:71},defect:original}),c=m.context;assert.equal(c.editing,true);assert.equal(c.form.assigneeUserId,'inactive');assert.equal(c.form.verifier,'Historical tester');assert.equal(c.form.verifierUserId,'');assert.equal(c.form.sprint,'closed');assert.equal(c.dirty,false);assert.equal(c.form.status,'待验证');assert.match(text(m.container),/历史成员/)
 c.form.title='Updated title only';await c.save();const call=m.calls.find(call=>call.options.method==='PATCH');assert.deepEqual(JSON.parse(call.options.body),{title:'Updated title only'});assert.equal(call.path,'/defects/71');assert.equal(m.events[0].type,'saved');assert.equal(m.events[0].item.sourceExecutionId,51);assert.equal(m.events[0].item.status,'待验证');assert.deepEqual(m.events[0].item.customFields,original.customFields);assert.equal(m.calls.some(call=>call.path.startsWith('/requirements/')),false);m.stop()
})
await test('full defect editing submits all edited text, versions, tags, custom fields and explicit stable people changes only',async()=>{
 const m=await mount('DefectComposer',{props:{defectId:71}}),c=m.context
 for(const key of ['description','steps','actual','expected','environment','foundVersion','fixVersion','tags'])c.form[key]='Edited '+key
 c.form.customFields={history:'Edited',points:0};c.form.sprint='22';c.form.requirementId=18;c.setPerson('assignee','dev');c.setPerson('verifier','qa');await c.save();const payload=JSON.parse(m.calls.find(call=>call.options.method==='PATCH').options.body)
 for(const key of ['description','steps','actual','expected','environment','foundVersion','fixVersion','tags'])assert.equal(payload[key],'Edited '+key)
 assert.equal(payload.requirementId,18);assert.equal(payload.sprint,'22');assert.equal(payload.assigneeUserId,'dev');assert.equal(payload.verifierUserId,'qa');assert.equal(payload.verifier,'同名');assert.deepEqual(payload.customFields,{history:'Edited',points:0});for(const key of ['id','status','sourceExecutionId','progress','actualHours','title'])assert.equal(Object.hasOwn(payload,key),false);m.stop()
})
await test('clearing a legacy person is explicit and no-change saves send no PATCH',async()=>{
 const m=await mount('DefectComposer',{props:{defectId:71}}),c=m.context;await c.save();assert.equal(m.calls.some(call=>call.options.method==='PATCH'),false);c.setPerson('verifier','');await c.save();assert.deepEqual(JSON.parse(m.calls.find(call=>call.options.method==='PATCH').options.body),{verifier:'',verifierUserId:''});m.stop()
})
await test('failed edits retain a complete draft; changed inactive people and historical iterations fail without writes',async()=>{
 const m=await mount('DefectComposer',{props:{defectId:71},override:(_path,options)=>options.method==='PATCH'?Promise.reject(Error('version conflict')):undefined}),c=m.context;c.form.description='Retain this draft';await c.save();assert.equal(c.error,'version conflict');assert.equal(c.form.description,'Retain this draft');assert.equal(c.dirty,true);assert.equal(m.events.length,0);m.stop()
 const invalid=await mount('DefectComposer',{props:{defectId:71}});invalid.context.form.verifierUserId='inactive';await invalid.context.save();assert.match(invalid.context.error,/已不可用/);invalid.context.form.verifierUserId='';invalid.context.form.sprint='another-closed';await invalid.context.save();assert.match(invalid.context.error,/完整迭代名称/);assert.equal(invalid.calls.some(call=>call.options.method==='PATCH'),false);invalid.stop()
})
await test('pending full edit cannot escape or emit into a new defect or identity',async()=>{
 for(const action of ['identity','defect']){const pending=deferred(),m=await mount('DefectComposer',{props:{defectId:71},override:(_path,options)=>options.method==='PATCH'?pending.promise:undefined});m.context.form.title='Pending';const operation=m.context.save();await flush();assert.equal(await m.context.requestClose(),false);assert(m.guards.every(guard=>guard()===false));if(action==='identity')m.window.dispatchEvent(new Event('devflow-identity-changed'));else m.props.defectId=72;await flush();pending.resolve({...originalDefect(),title:'Pending'});await operation;assert.equal(m.events.length,0);m.stop()}
})
await test('requirement picker searches by literal code/title with stable IDs, scoped headers and keyboard selection',async()=>{
 const m=await mount('RequirementPicker',{props:{modelValue:null}}),c=m.context;c.open();await flush();c.query='REQ-19 / 用户标题';await c.search();await flush();assert.equal(new URLSearchParams(m.calls.at(-1).path.split('?')[1]).get('q'),'REQ-19 / 用户标题');assert.equal(m.calls.at(-1).options.headers.get('X-TaskLoom-Project'),'project-a');c.highlighted=2;c.keydown({key:'Enter',preventDefault(){}});assert.deepEqual(m.events.find(event=>event.type==='selected'),{type:'selected',id:19});assert.equal(c.opened,false);assert.equal(c.query,'');m.stop()
})
await test('controlled requirement selection retains the old display until the parent accepts the change',async()=>{
 const m=await mount('RequirementPicker',{props:{modelValue:17}}),c=m.context;assert.match(c.selectedLabel,/REQ-17.*User title 17/);c.open();await flush();c.choose(c.items[1]);await flush();assert.equal(m.props.modelValue,17);assert.match(c.selectedLabel,/REQ-17/);assert.equal(c.opened,false);m.props.modelValue=18;await flush();assert.match(c.selectedLabel,/REQ-18/);c.choose(null);assert.equal(m.events.at(-2).id,null);assert.match(c.selectedLabel,/REQ-18/);m.stop()
})
await test('requirement suggestions fit the drawer scroll area and flip above the input near its bottom',async()=>{
 const m=await mount('RequirementPicker',{props:{modelValue:null}}),c=m.context
 const down=c.menuPlacement(130,170,110,650,90,170)
 assert.equal(down.top,'84px');assert.equal(down.bottom,'auto');assert.equal(down.maxHeight,'330px')
 const up=c.menuPlacement(530,570,110,650,490,570)
 assert.equal(up.top,'auto');assert.equal(up.bottom,'44px');assert.equal(up.maxHeight,'330px')
 const short=c.menuPlacement(190,230,110,300,160,230)
 assert.equal(short.top,'auto');assert.equal(short.maxHeight,'74px')
 const outside=c.menuPlacement(700,740,110,650,670,740)
 assert(!outside.maxHeight.startsWith('-'));m.stop()
})
await test('picker keeps inaccessible existing IDs visible, rejects stale queries, and blocks disabled/scope-invalid changes',async()=>{
 const unavailable=await mount('RequirementPicker',{props:{modelValue:99}});assert.equal(unavailable.context.selectedLabel,'#99');assert.match(unavailable.context.selectedError,/关联需求无效/);assert.equal(unavailable.events.length,0);unavailable.stop()
 const pending=deferred(),m=await mount('RequirementPicker',{props:{modelValue:null},override:path=>path.includes('q=old')?pending.promise:undefined}),c=m.context;c.query='old';const old=c.search();c.query='new';await c.search();pending.resolve({items:[{id:99,code:'Old',title:'stale'}]});await old;assert.equal(c.items.some(item=>item.id===99),false);m.props.disabled=true;await flush();c.choose(c.items[0]);assert.equal(m.events.length,0);m.props.disabled=false;m.window.dispatchEvent(new Event('devflow-identity-changed'));await flush();c.open();c.choose(null);assert.equal(c.opened,false);assert.equal(m.events.length,0);m.stop()
})
await test('defect list status menu stops row activation and only sends allowed changed statuses',async()=>{
 const defect={...originalDefect(),status:'新建',allowedTransitions:['已确认','已拒绝']},m=await mount('Defects',{defect}),c=m.context
 const menu=descendants(m.container).find(node=>String(node.props.class||'').includes('defect-inline-status'));assert.equal(menu.props.value,'新建');assert.equal(menu.props.disabled,false);const event={stopPropagation(){this.stopped=true}};menu.parent.props.onClick(event);assert(event.stopped);menu.parent.props.onKeydown({...event});assert.equal(m.navigations.length,0)
 assert.deepEqual(c.rowStatusOptions(defect),['新建','已确认','已拒绝']);await c.changeStatus(defect,'已关闭');assert.equal(m.calls.some(call=>call.options.method==='PATCH'),false);await c.changeStatus(defect,'已确认');assert.deepEqual(JSON.parse(m.calls.find(call=>call.options.method==='PATCH').options.body),{status:'已确认'});assert.equal(c.items[0].status,'已确认');assert.equal(c.selected,null);assert.equal(m.navigations.length,0);m.stop()
})
await test('failed and pending inline statuses keep their original value, block duplicates and retain project scope',async()=>{
 const pending=deferred(),defect={...originalDefect(),status:'新建',allowedTransitions:['已确认']},m=await mount('Defects',{defect,override:(_path,options)=>options.method==='PATCH'?pending.promise:undefined}),c=m.context,input={value:'已确认'},saving=c.changeStatus(c.items[0],'已确认',{target:input});await flush();assert.equal(c.items[0].status,'新建');assert.equal(c.rowSavingId,71);assert.equal(c.canLeave(),false);await c.changeStatus(c.items[0],'已确认');assert.equal(m.calls.filter(call=>call.options.method==='PATCH').length,1);pending.reject(Error('transition refused'));await saving;assert.equal(c.items[0].status,'新建');assert.equal(input.value,'新建');assert.equal(c.notice,'transition refused');assert.equal(c.rowSavingId,null);assert.equal(m.calls.find(call=>call.options.method==='PATCH').options.headers.get('X-TaskLoom-Project'),'project-a');m.stop()
})
await test('read-only defect views cannot open editors or mutate inline fields/statuses',async()=>{
 const defect={...originalDefect(),allowedTransitions:['已关闭']},m=await mount('Defects',{defect,role:'viewer',query:{bug:'71'}}),c=m.context;assert.equal(c.canWrite,false);c.openCreate();c.editSelected();await c.changeStatus(defect,'已关闭');await c.patch('title','Forbidden');assert.equal(c.show,false);assert.equal(c.editingId,null);assert.equal(m.calls.some(call=>call.options.method==='PATCH'),false);assert(descendants(m.container).find(node=>String(node.props.class||'').includes('defect-inline-status')).props.disabled);m.stop()
})
await test('opening and saving the full defect editor preserves detail comment drafts and list filters',async()=>{
 const m=await mount('Defects',{query:{bug:'71'}}),c=m.context;c.comment='Unposted comment';c.q='Keep filter';c.editSelected();await flush();assert.equal(c.editingId,71);assert.equal(c.comment,'Unposted comment');assert(descendants(m.container).find(node=>node.props.class==='defects-content').props.inert);await c.onSaved({...originalDefect(),description:'Edited'});await flush();assert.equal(c.editingId,null);assert.equal(c.selected.description,'Edited');assert.equal(c.comment,'Unposted comment');assert.equal(c.q,'Keep filter');assert.equal(m.route.query.bug,'71');assert.equal(m.navigations.length,0);m.stop()
})
await test('identity invalidation discards a late inline status response rather than patching another scope',async()=>{
 const pending=deferred(),defect={...originalDefect(),status:'新建',allowedTransitions:['已确认']},m=await mount('Defects',{defect,override:(_path,options)=>options.method==='PATCH'?pending.promise:undefined}),c=m.context,operation=c.changeStatus(c.items[0],'已确认');m.window.dispatchEvent(new Event('devflow-identity-changed'));pending.resolve({...defect,status:'已确认'});await operation;assert.equal(c.items[0].status,'新建');assert.equal(c.canWrite,false);assert.equal(c.rowSavingId,null);m.stop()
})
await test('composer uses shared single pickers, retains historical identities and preserves the default tester',async()=>{
 const m=await mount('DefectComposer'),c=m.context;const pickers=descendants(m.container).filter(node=>node.props['data-member-picker']);assert.equal(pickers.length,2);assert(pickers.every(node=>node.props['data-single']===true));assert.equal(c.form.verifierUserId,'qa');assert.equal(c.verifierMembers[0].id,'qa')
 descendants(pickers[0]).find(node=>node.props['data-member-id']==='dev').props.onClick();assert.equal(c.form.assigneeUserId,'dev');assert.equal(c.form.assignee,'同名');assert.equal(c.form.verifierUserId,'qa');c.setPerson('assignee','inactive');assert.equal(c.form.assigneeUserId,'dev');c.saving=true;c.setPerson('assignee','u_me');assert.equal(c.form.assigneeUserId,'dev');c.saving=false;m.stop()
 const history=await mount('DefectComposer',{props:{defectId:71}});assert.equal(history.context.form.assigneeUserId,'inactive');assert.equal(history.context.dirty,false);const clear=descendants(history.container).find(node=>node.props['data-member-picker']==='验证人');descendants(clear).find(node=>node.props['data-clear-member']).props.onClick();assert.equal(history.context.form.verifier,'');assert.equal(history.context.dirty,true);history.stop()
})
await test('single picker disabled props protect teleported choices for read-only and identity-invalid composers',async()=>{
 const viewer=await mount('DefectComposer',{role:'viewer'});const pickers=descendants(viewer.container).filter(node=>node.props['data-member-picker']);assert(pickers.length===2);assert(pickers.every(node=>descendants(node).filter(child=>['button','input'].includes(child.tag)).every(child=>child.props.disabled===true)));viewer.context.setPerson('assignee','dev');assert.equal(viewer.context.form.assigneeUserId,'');viewer.stop()
 const m=await mount('DefectComposer');m.window.dispatchEvent(new Event('devflow-identity-changed'));await flush();m.context.setPerson('assignee','dev');assert.equal(m.context.form.assigneeUserId,'');assert(descendants(m.container).filter(node=>node.props['data-member-picker']).every(node=>descendants(node).filter(child=>child.tag==='input').every(child=>child.props.disabled===true)));m.stop()
})
await test('detail single pickers patch stable IDs and preserve old people on failures, invalid candidates and read-only state',async()=>{
 const m=await mount('Defects',{query:{bug:'71'}}),c=m.context;const picker=descendants(m.container).find(node=>node.props['data-member-picker']==='负责人');assert(picker);assert.equal(picker.props['data-single'],true)
 await c.setDetailPerson('assigneeUserId',['dev','qa']);await c.setDetailPerson('verifierUserId',['inactive']);assert.equal(m.calls.some(call=>call.options.method==='PATCH'),false)
 descendants(picker).find(node=>node.props['data-member-id']==='dev').props.onClick();await flush();assert.equal(c.selected.assigneeUserId,'dev');assert.deepEqual(JSON.parse(m.calls.find(call=>call.options.method==='PATCH').options.body),{assigneeUserId:'dev',assignee:'同名'});m.stop()
 const fail=await mount('Defects',{query:{bug:'71'},override:(_path,options)=>options.method==='PATCH'?Promise.reject(Error('refused')):undefined});await fail.context.setDetailPerson('verifierUserId',['qa']);assert.equal(fail.context.selected.verifier,'Historical tester');assert.equal(fail.context.selected.verifierUserId,'');assert.equal(fail.context.notice,'refused');fail.stop()
 const viewer=await mount('Defects',{query:{bug:'71'},role:'viewer'});await viewer.context.setDetailPerson('assigneeUserId',['dev']);assert.equal(viewer.calls.some(call=>call.options.method==='PATCH'),false);assert(descendants(viewer.container).filter(node=>node.props['data-member-picker']).every(node=>descendants(node).filter(child=>child.tag==='input').every(child=>child.props.disabled)));viewer.stop()
})
console.log(`Passed ${count} requirement quality collaboration tests.`)

import {workflow} from './workflow-test-support.mjs'
import assert from 'node:assert/strict'
import {readFile} from 'node:fs/promises'
import ts from 'typescript'
import * as Vue from 'vue'
import {parse,compileScript,compileTemplate} from 'vue/compiler-sfc'

const root=new URL('../',import.meta.url)
const favoriteSource=await readFile(new URL('src/components/RequirementFavorite.vue',root),'utf8')
const workSource=await readFile(new URL('src/views/MyWork.vue',root),'utf8')
const transpile=source=>ts.transpileModule(source,{compilerOptions:{module:ts.ModuleKind.CommonJS,target:ts.ScriptTarget.ES2022}}).outputText
const defer=()=>{let resolve,reject;const promise=new Promise((a,b)=>{resolve=a;reject=b});return{promise,resolve,reject}}
const settle=async()=>{await Promise.resolve();await Promise.resolve();await Vue.nextTick()}
class Events{listeners=new Map();emitted=[];addEventListener(name,fn){if(!this.listeners.has(name))this.listeners.set(name,new Set());this.listeners.get(name).add(fn)}removeEventListener(name,fn){this.listeners.get(name)?.delete(fn)}dispatchEvent(event){this.emitted.push(event);for(const fn of [...(this.listeners.get(event.type)||[])])fn(event)}}
class Event{constructor(type,options){this.type=type;this.detail=options?.detail}}
const RequirementCodeStub=Vue.defineComponent({name:'RequirementCode',props:['requirement','members','projectId'],emits:['open'],setup:(props,{emit})=>()=>Vue.h('button',{type:'button',onClick:()=>emit('open')},props.requirement?.code||'')})
function mount(source,handler=async()=>({items:[],counts:{}}),props={}){
 const {descriptor}=parse(source),compiled=compileScript(descriptor,{id:'test-favorites'})
 const mounted=[],unmounts=[],calls=[],emitted=[],navigations=[],win=new Events(),storage=new Map([['devflow-project','p-current']]),location={href:''},language=Vue.ref('zh-CN'),scope=Vue.effectScope()
 const imports={'../components/RequirementCode.vue':{default:RequirementCodeStub},'../requirementWorkflow':workflow,vue:{...Vue,onMounted:fn=>mounted.push(fn),onBeforeUnmount:fn=>unmounts.push(fn)},'../api':{api:async(path,options)=>{calls.push({path,options});return handler(path,options)}},'../i18n':{t:value=>language.value==='en-US'?`en:${value}`:value,locale:language,formatDate:value=>String(value)},'vue-router':{useRouter:()=>({push:async path=>{navigations.push(path)}})}}
 imports['../components/RequirementListExport.vue']={default:Vue.defineComponent({name:'RequirementListExport',props:['items','projectId','columns'],setup:()=>()=>Vue.h('button',{type:'button'},'Export')})}
 imports['../components/Icon.vue']={default:Vue.defineComponent({name:'Icon',props:['name'],setup:()=>()=>Vue.h('svg',{'aria-hidden':'true'})})}
 const module={};new Function('require','exports','localStorage','window','location','CustomEvent',transpile(compiled.content))(id=>imports[id],module,{getItem:key=>storage.get(key)??null,setItem:(key,value)=>storage.set(key,String(value))},win,location,Event)
 const reactiveProps=Vue.reactive(props),state=scope.run(()=>module.default.setup(reactiveProps,{expose:()=>{},emit:(...args)=>emitted.push(args)}))
 return{...state,props:reactiveProps,calls,emitted,navigations,win,storage,location,language,mount:async()=>{for(const fn of mounted)await fn();await settle()},stop:()=>{for(const fn of unmounts)fn();scope.stop()}}
}
const state=(id,favorited)=>({requirementId:id,favorited,createdAt:favorited?'2026-09-03T10:00:00Z':undefined})
let count=0
async function test(name,run){await run();count++;console.log('✓ '+name)}

await test('favorite status is loaded with project scope, never written on mount',async()=>{
 const m=mount(favoriteSource,async()=>state(12,false),{requirementId:12});await m.mount();assert.equal(m.favorited.value,false);assert.equal(m.loading.value,false);assert.equal(m.calls.length,1);assert.equal(m.calls[0].path,'/requirements/12/favorite');assert.equal(m.calls[0].options.headers['X-DevFlow-Project'],'p-current');assert.equal(m.calls[0].options.method,undefined);m.stop()
})
await test('favorite toggles await confirmation and guard double clicks with idempotent PUT/DELETE',async()=>{
 const pending=defer();let next=true
 const m=mount(favoriteSource,async(path,options)=>options?.method?pending.promise:state(12,false),{requirementId:12});await m.mount()
 const first=m.toggle();await m.toggle();assert.equal(m.calls.filter(call=>call.options.method==='PUT').length,1);assert.equal(m.favorited.value,false);assert.equal(m.saving.value,true)
 pending.resolve(state(12,true));await first;assert.equal(m.favorited.value,true);assert.equal(m.saving.value,false);assert.deepEqual(m.emitted,[['change',true]]);assert.equal(m.win.emitted[0].detail.projectId,'p-current');await m.toggle();assert.equal(m.calls.at(-1).options.method,'DELETE');m.stop()
})
await test('failed mutation preserves the known star and exposes retry rather than optimistic success',async()=>{
 const m=mount(favoriteSource,async(path,options)=>{if(options?.method)throw Error('permission denied');return state(12,true)},{requirementId:12});await m.mount();await m.toggle();assert.equal(m.favorited.value,true);assert.equal(m.saving.value,false);assert.equal(m.error.value,'permission denied');assert.equal(m.emitted.length,0);await m.load();assert.equal(m.error.value,'');assert.equal(m.favorited.value,true);m.stop()
})
await test('failed initial load blocks writes until a successful retry validates the returned requirement',async()=>{
 let available=false;const m=mount(favoriteSource,async()=>{if(!available)throw Error('offline');return state(12,false)},{requirementId:12});await m.mount();await m.toggle();assert.equal(m.calls.length,1);assert.equal(m.favorited.value,null);assert.equal(m.error.value,'offline');available=true;await m.load();assert.equal(m.favorited.value,false);m.stop()
 const malformed=mount(favoriteSource,async()=>state(99,true),{requirementId:12});await malformed.mount();assert.equal(malformed.favorited.value,null);assert.equal(malformed.error.value,'收藏状态加载失败');malformed.stop()
})
await test('late reads and writes cannot overwrite a different selected requirement',async()=>{
 const requests=[];const m=mount(favoriteSource,()=>{const pending=defer();requests.push(pending);return pending.promise},{requirementId:12});await m.mount();m.props.requirementId=13;requests[1].resolve(state(13,false));await settle();requests[0].resolve(state(12,true));await settle();assert.equal(m.favorited.value,false)
 const write=m.toggle();m.props.requirementId=14;assert.equal(m.favorited.value,null);requests[3].resolve(state(14,false));await settle();requests[2].resolve(state(13,true));await write;assert.equal(m.favorited.value,false);assert.equal(m.emitted.length,0);assert.equal(m.saving.value,false);m.stop()
})
await test('project changes freeze old request headers and invalidate old favorite responses',async()=>{
 const pending=defer();const m=mount(favoriteSource,async(path,options)=>options?.method?pending.promise:state(12,false),{requirementId:12});await m.mount();const write=m.toggle();m.storage.set('devflow-project','p-next');m.win.dispatchEvent(new Event('devflow-project-changed'));await settle();pending.resolve(state(12,true));await write;assert.equal(m.favorited.value,false);assert.equal(m.calls[1].options.headers['X-DevFlow-Project'],'p-current');assert.equal(m.calls.at(-1).options.headers['X-DevFlow-Project'],'p-next');assert.equal(m.emitted.length,0);m.stop()
})
await test('identity conflicts and unmount invalidate pending favorite work and remove listeners',async()=>{
 for(const action of ['devflow-identity-changed','devflow-auth-expired','unmount']){const pending=defer(),m=mount(favoriteSource,()=>pending.promise,{requirementId:12});await m.mount();if(action==='unmount')m.stop();else m.win.dispatchEvent(new Event(action));pending.resolve(state(12,true));await settle();assert.equal(m.favorited.value,null);const before=m.calls.length;await m.toggle();assert.equal(m.calls.length,before);if(action!=='unmount')m.stop();assert([...m.win.listeners.values()].every(set=>!set.size))}
})
await test('favorite controls and both work views compile with explicit accessible button semantics',()=>{
 for(const source of [favoriteSource,workSource]){const {descriptor}=parse(source),compiled=compileScript(descriptor,{id:'test-favorite-template'});const template=compileTemplate({id:'test-favorite-template',filename:'Test.vue',source:descriptor.template.content,compilerOptions:{bindingMetadata:compiled.bindings}});assert.deepEqual(template.errors,[])}
 assert.match(favoriteSource,/:aria-pressed="favorited === true"/);assert.match(favoriteSource,/:aria-busy="loading \|\| saving"/);assert.match(favoriteSource,/role="alert"/);assert.match(favoriteSource,/<button type="button"/);assert.match(workSource,/:aria-pressed="view==='favorites'"/)
})
await test('favorites view keeps project/search/valid status/sort filters and is independent from assigned categories',async()=>{
const m=mount(workSource,async()=>({items:[{id:44,title:'Other member original title',projectId:'p-other',favorited:true}],counts:{all:1,completed:1}}));m.requirementStatusDefinitions.value=[{key:'已完成',name:'已完成',system:true}];m.q.value='original';m.project.value='p-other';m.status.value='已完成';m.sort.value='title';m.type.value='缺陷';m.selectView('favorites');assert.equal(m.category.value,'');assert.equal(m.q.value,'original');assert.equal(m.project.value,'p-other');assert.equal(m.status.value,'已完成');assert.equal(m.sort.value,'title');await m.load();const query=new URLSearchParams(m.calls.at(-1).path.split('?')[1]);assert.equal(query.get('view'),'favorites');assert.equal(query.get('type'),'需求');assert.equal(query.get('status'),'已完成');assert.equal(m.items.value[0].title,'Other member original title');m.selectView('assigned');assert.equal(m.category.value,'active');m.stop()
})
await test('switching view invalidates in-flight assigned results before debounce starts',async()=>{
 const requests=[];const m=mount(workSource,()=>{const pending=defer();requests.push(pending);return pending.promise});const old=m.load();m.selectView('favorites');requests[0].resolve({items:[{title:'stale assigned'}],counts:{}});await old;assert.equal(m.items.value.length,0);const fresh=m.load();requests[1].resolve({items:[{title:'Current favorite'}],counts:{all:1}});await fresh;assert.equal(m.items.value[0].title,'Current favorite');m.stop()
})
await test('personal work sorting is stable, numeric for IDs, and keeps missing dates last in either direction',()=>{
 const m=mount(workSource);m.items.value=[{id:2,projectId:'a',dueDate:''},{id:10,projectId:'a',dueDate:'2026-09-02'},{id:1,projectId:'a',dueDate:'2026-09-01'}];m.sort.value='code';m.order.value='asc';assert.deepEqual(m.sortedItems.value.map(x=>x.id),[1,2,10]);m.sort.value='dueDate';m.order.value='desc';assert.deepEqual(m.sortedItems.value.map(x=>x.id),[10,1,2]);m.order.value='asc';assert.deepEqual(m.sortedItems.value.map(x=>x.id),[1,10,2]);assert.deepEqual(m.items.value.map(x=>x.id),[2,10,1]);m.stop()
})
await test('inaccessible cross-project favorite does not change current project or navigate',async()=>{
 const m=mount(workSource,async()=>{throw Error('access revoked')});await m.go({id:3,projectId:'p-denied',url:'/requirements?req=3',favorited:true});assert.equal(m.storage.get('devflow-project'),'p-current');assert.equal(m.location.href,'');assert.equal(m.error.value,'access revoked');assert.equal(m.calls[0].options.headers['X-DevFlow-Project'],'p-denied');m.stop()
})
await test('late favorite navigation cannot override a newer project context',async()=>{
 const pending=defer(),m=mount(workSource,()=>pending.promise);const opening=m.go({id:3,projectId:'p-other',url:'/requirements?req=3',favorited:true});m.storage.set('devflow-project','p-new-context');pending.resolve(state(3,true));await opening;assert.equal(m.storage.get('devflow-project'),'p-new-context');assert.equal(m.location.href,'');m.stop()
})
await test('favorite change events refresh only the favorites view and stop after unmount',async()=>{
 const m=mount(workSource);await m.mount();m.calls.length=0;m.win.dispatchEvent(new Event('devflow-favorites-changed'));await settle();assert.equal(m.calls.length,0);m.selectView('favorites');m.win.dispatchEvent(new Event('devflow-favorites-changed'));await settle();assert.equal(m.calls.length,1);m.stop();assert([...m.win.listeners.values()].every(set=>!set.size))
})
await test('iteration shortcuts use stable IDs, preserve other filters and reset on project changes',async()=>{
 const m=mount(workSource,async path=>path.startsWith('/my-work')?{items:[],counts:{},sprints:[{id:71,name:'同名迭代',projectId:'p-current',projectName:'项目一',status:'进行中'},{id:72,name:'同名迭代',projectId:'p-next',projectName:'项目二',status:'进行中'},{id:73,name:'历史',projectId:'p-current',projectName:'项目一',status:'已完成'}]}:{items:[]})
 await m.mount();assert.equal(m.activeSprints.value.length,2);assert.notEqual(m.sprintLabel(m.sprints.value[0]),m.sprintLabel(m.sprints.value[1]))
 m.q.value='关键词';m.type.value='需求';m.selectSprint('72');await m.load()
 let query=new URLSearchParams(m.calls.filter(x=>x.path.startsWith('/my-work')).at(-1).path.split('?')[1]);assert.equal(query.get('sprintId'),'72');assert.equal(query.get('q'),'关键词');assert.equal(query.get('type'),'需求')
 m.selectSprint('72');assert.equal(m.sprintId.value,'');m.selectSprint('71');m.project.value='p-next';assert.equal(m.sprintId.value,'');assert.deepEqual(m.sprints.value,[])
 m.win.dispatchEvent(new Event('devflow-identity-changed'));m.selectSprint('72');assert.equal(m.sprintId.value,'');m.stop()
})
console.log(`Passed ${count} requirement favorite and personal-work regression tests.`)

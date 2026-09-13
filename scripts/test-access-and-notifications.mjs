import { readFileSync } from 'node:fs'
import { createRequire } from 'node:module'
import assert from 'node:assert/strict'
import { createWorkspaceHarness } from './helpers/workspace-harness.mjs'
const require = createRequire(import.meta.url), ts = require('typescript'), Vue = require('vue')
const notificationDisplay={};new Function('exports',ts.transpileModule(readFileSync(new URL('../src/notificationDisplay.ts',import.meta.url),'utf8'),{compilerOptions:{module:ts.ModuleKind.CommonJS,target:ts.ScriptTarget.ES2022}}).outputText)(notificationDisplay)
class APIError extends Error { constructor(message,status,code=''){super(message);this.status=status;this.code=code} }
function setup(file,exposed,handler=async()=>({items:[]})){
 const source=readFileSync(new URL('../'+file,import.meta.url),'utf8').match(/<script setup[^>]*>([\s\S]*?)<\/script>/)[1]
 const calls=[],storage=new Map(),events=[],scope=Vue.effectScope(),exports={},location={href:'',origin:'http://devflow.local',reload(){this.reloaded=true}},language=Vue.ref('zh-CN')
 const localStorage={getItem:k=>storage.get(k)??null,setItem:(k,v)=>storage.set(k,v),removeItem:k=>storage.delete(k)},store=createWorkspaceHarness(localStorage)
 const api=async(path,options)=>{calls.push({path,options});return handler(path,options)}
 const imports={...store.imports,'../notificationDisplay':notificationDisplay,vue:{...Vue,onMounted:()=>{},onBeforeUnmount:()=>{}},'vue-router':{useRoute:()=>({query:{},path:'/notifications'}),useRouter:()=>({})}}
 const code=ts.transpileModule(source+`\nexport {${exposed.join(',')}}`,{compilerOptions:{module:ts.ModuleKind.CommonJS,target:ts.ScriptTarget.ES2022}}).outputText
 imports['./layoutScope']={applyLayoutScope:()=>{},clearLayoutScope:()=>{},useLayoutBoolean:(_key,fallback)=>Vue.ref(fallback)}
 scope.run(()=>new Function('require','exports','localStorage','window','document','location','CustomEvent',code)(id=>id.endsWith('.vue')?{}:id.endsWith('/api')?{api,APIError}:id.endsWith('/i18n')?{t:(source,params={})=>source.replace(/\{(\w+)\}/g,(token,key)=>Object.hasOwn(params,key)?String(params[key]):token),locale:language,formatDate:x=>x,applyLanguagePreferences:()=>{}}:imports[id],exports,localStorage,{addEventListener:()=>{},removeEventListener:()=>{},dispatchEvent:e=>events.push(e)},{visibilityState:'visible',removeEventListener:()=>{}},location,class{constructor(type,options){this.type=type;this.detail=options?.detail}}))
 return {...exports,calls,storage,location,events,stop:()=>{scope.stop();store.stop()}}
}
let count=0;async function test(name,run){await run();count++;console.log('✓ '+name)}
const deferred=()=>{let resolve,reject;const promise=new Promise((a,b)=>{resolve=a;reject=b});return {promise,resolve,reject}}
await test('description and remarks notifications have distinct readable event labels',()=>{
 const m=setup('src/views/Notifications.vue',['eventNames']);assert.equal(m.eventNames['requirement.description_mentioned'],'正文提及');assert.equal(m.eventNames['requirement.remarks_mentioned'],'备注提及');m.stop()
})
await test('notification filters use unified AppSelect controls and advertise only real runtime events',()=>{
 const source=readFileSync(new URL('../src/views/Notifications.vue',import.meta.url),'utf8')
 const toolbar=source.match(/<div class="toolbar module-toolbar[^"]*">([\s\S]*?)<\/div>/)?.[1]||''
 assert.equal((toolbar.match(/<AppSelect\b/g)||[]).length,3)
 assert.doesNotMatch(toolbar,/<select\b/)
 assert.match(source,/:options="readOptions"/);assert.match(source,/:options="eventOptions"/);assert.match(source,/:options="projectOptions"/)
 const m=setup('src/views/Notifications.vue',['eventNames','filterEventTypes','eventOptions'])
 for(const event of ['requirement.updated','defect.verifier_changed','sprint.status_changed','test_plan.executor_changed','user.password_changed'])assert.ok(m.filterEventTypes.includes(event),event)
 for(const legacy of ['requirement.created','defect.created','defect.verification','sprint.started','sprint.created']){assert.ok(m.eventNames[legacy],legacy+' remains readable');assert.ok(!m.filterEventTypes.includes(legacy),legacy+' is not advertised as a live filter')}
 assert.deepEqual(m.eventOptions.value.slice(1).map(option=>option.value),m.filterEventTypes)
 m.stop()
})
await test('unread summary stays global while the current list count is bounded and malformed responses are safe',async()=>{
 let request=0
 const m=setup('src/views/Notifications.vue',['load','items','unread','unreadSummary','resultSummary'],async()=>++request===1?{items:[{id:1},{id:2}],unread:5}:{items:'invalid',unread:'not-a-number'})
 await m.load();assert.equal(m.unread.value,5);assert.equal(m.unreadSummary.value,'未读 5 条');assert.equal(m.resultSummary.value,'当前列表 2 条')
 await m.load();assert.deepEqual(m.items.value,[]);assert.equal(m.unread.value,0);assert.equal(m.unreadSummary.value,'未读 0 条');assert.equal(m.resultSummary.value,'当前列表 0 条')
 m.stop()
})
await test('opening a notification validates its internal target and re-enters the target project before caching navigation',async()=>{
 const item={id:9,readAt:'2026-09-05T00:00:00Z',projectId:'project-target',url:'/requirements?req=7'}
 const m=setup('src/views/Notifications.vue',['open','error'],async(path,options)=>{assert.equal(path,'/projects/project-target/visit');assert.equal(options.method,'POST');assert.equal(options.headers['X-TaskLoom-Project'],'project-target');return {projectId:'project-target'}})
 await m.open(item);assert.equal(m.storage.get('devflow-project'),'project-target');assert.equal(m.location.href,'/requirements?req=7');assert.equal(m.error.value,'');m.stop()
 const invalid=setup('src/views/Notifications.vue',['open','error'],async()=>{throw Error('should not request')})
 await invalid.open({...item,url:'https://outside.example/steal'});assert.equal(invalid.calls.length,0);assert.equal(invalid.error.value,'通知链接无效，请刷新后重试');invalid.stop()
 const revoked=setup('src/views/Notifications.vue',['open','error'],async()=>{throw Error('project access revoked')})
 await revoked.open(item);assert.equal(revoked.storage.get('devflow-project'),undefined);assert.equal(revoked.location.href,'');assert.equal(revoked.error.value,'project access revoked');revoked.stop()
})
await test('background refresh keeps current messages visible and preserves errors on transient failure',async()=>{
 const m=setup('src/views/Notifications.vue',['load','items','loading','error'],async()=>{throw Error('offline')});m.loading.value=false;m.items.value=[{id:1}];m.error.value='Existing error';await m.load(true);assert.equal(m.loading.value,false);assert.deepEqual(m.items.value,[{id:1}]);assert.equal(m.error.value,'Existing error');m.stop()
})
await test('opening unread content avoids a redundant list refresh and remains usable when read tracking fails',async()=>{
 for(const failRead of [false,true]){
  const m=setup('src/views/Notifications.vue',['open','items'],async(path)=>{
   if(path.endsWith('/visit'))return {projectId:'p'}
   if(path==='/notifications/3'){if(failRead)throw new APIError('Temporary failure',503);return {updated:1,unread:0}}
   throw Error('Navigation must not reload the inbox')
  })
  m.items.value=[{id:3,readAt:''}]
  await m.open({id:3,projectId:'p',url:'/requirements?req=1',readAt:''})
  assert.deepEqual(m.calls.map(x=>x.path),['/projects/p/visit','/notifications/3'])
  assert.equal(m.location.href,'/requirements?req=1')
  assert.equal(!!m.items.value[0].readAt,!failRead,'failed tracking must not invent read state');m.stop()
 }
})
await test('rapid notification clicks cannot issue competing project navigation or outlive identity changes',async()=>{
 const pending=deferred(),m=setup('src/views/Notifications.vue',['open','invalidateIdentity','opening'],()=>pending.promise)
 const item={id:1,projectId:'p',url:'/requirements?req=1',readAt:''}
 const first=m.open(item);await m.open({...item,projectId:'other'});assert.equal(m.calls.length,1)
 m.invalidateIdentity();pending.resolve({});await first
 assert.equal(m.location.href,'');assert.equal(m.calls.length,1);assert.equal(m.opening.value,false);m.stop()
})
await test('a manually selected project is not overwritten by a pending notification visit',async()=>{
 const pending=deferred(),m=setup('src/views/Notifications.vue',['open','projectChanged'],path=>path.endsWith('/visit')?pending.promise:Promise.resolve({items:[]}))
 const first=m.open({id:1,projectId:'old-target',url:'/requirements?req=1',readAt:''})
 m.storage.set('devflow-project','manual-selection');m.projectChanged();pending.resolve({});await first
 assert.equal(m.storage.get('devflow-project'),'manual-selection');assert.equal(m.location.href,'')
 assert(!m.calls.some(x=>x.path==='/notifications/1'));m.stop()
})
await test('background notification refresh does not overlap and a newer foreground result wins',async()=>{
 const old=deferred(),m=setup('src/views/Notifications.vue',['load','items','loading'],()=>m.calls.length===1?old.promise:Promise.resolve({items:[{id:2}],unread:0}));m.loading.value=false;const pending=m.load(true);await m.load(true);assert.equal(m.calls.length,1);await m.load();old.resolve({items:[{id:1}],unread:4});await pending;assert.equal(m.items.value[0].id,2);assert.equal(m.loading.value,false);m.stop()
})
await test('notification selection is page bounded with correct all/partial states and synchronous filter resets',async()=>{
 const m=setup('src/views/Notifications.vue',['items','loading','selectedIDs','toggleSelection','togglePageSelection','allPageSelected','somePageSelected','read','eventType','project','group','offset','changePage'],async()=>({items:[{id:1},{id:2}],total:2,unread:2}))
 m.items.value=[{id:1},{id:2}];m.loading.value=false
 m.toggleSelection(999);assert.deepEqual(m.selectedIDs.value,[])
 m.toggleSelection(1);assert(m.somePageSelected.value);assert(!m.allPageSelected.value)
 m.togglePageSelection();assert.deepEqual(m.selectedIDs.value,[1,2]);assert(m.allPageSelected.value);assert(!m.somePageSelected.value)
 m.togglePageSelection();assert.deepEqual(m.selectedIDs.value,[])
 for(const key of ['read','eventType','project','group']){m.selectedIDs.value=[1];m[key].value=key==='read'?'unread':'changed';assert.deepEqual(m.selectedIDs.value,[],key+' immediately clears selection');await Promise.resolve();await Promise.resolve()}
 m.loading.value=false;m.selectedIDs.value=[1];m.changePage(1);assert.deepEqual(m.selectedIDs.value,[]);assert.equal(m.offset.value,40);m.stop()
})
await test('background refresh preserves only still-visible selected notification IDs',async()=>{
 const m=setup('src/views/Notifications.vue',['load','items','selectedIDs','loading'],async()=>({items:[{id:2},{id:3}],total:2,unread:2}));m.loading.value=false;m.items.value=[{id:1},{id:2}];m.selectedIDs.value=[1,2];await m.load(true);assert.deepEqual(m.selectedIDs.value,[2]);assert.equal(m.loading.value,false);m.stop()
})
await test('bulk read and unread send explicit stable IDs and publish committed counts, not list length',async()=>{
 for(const read of [true,false]){
  const m=setup('src/views/Notifications.vue',['markSelected','items','selectedIDs','loading','unread','stateNotice'],async(path,options)=>options?.method?{updated:2,unread:7}:{items:[{id:1,readAt:read?'read':''},{id:2,readAt:read?'read':''}],total:2,unread:7})
  m.loading.value=false;m.items.value=[{id:1},{id:2}];m.selectedIDs.value=[2,1,999];await m.markSelected(read)
  assert.equal(m.calls[0].path,'/notifications/bulk-read');assert.equal(m.calls[0].options.method,'POST');assert.deepEqual(JSON.parse(m.calls[0].options.body),{ids:[2,1],read});assert.deepEqual(m.selectedIDs.value,[]);assert.equal(m.unread.value,7);assert.equal(m.stateNotice.value,'已更新 2 条通知');assert(m.events.some(e=>e.type==='devflow-unread'&&e.detail===7));assert(m.events.some(e=>e.type==='devflow-notifications-changed'));m.stop()
 }
})
await test('failed bulk update retains selection and never invents success; duplicate submissions are blocked',async()=>{
 const pending=deferred(),m=setup('src/views/Notifications.vue',['markSelected','items','selectedIDs','loading','saving','error','unread','stateNotice'],()=>pending.promise)
 m.loading.value=false;m.items.value=[{id:1,readAt:''}];m.selectedIDs.value=[1];m.unread.value=5
 const first=m.markSelected(true);await m.markSelected(true);assert.equal(m.calls.length,1);pending.reject(Error('No access'));await first
 assert.deepEqual(m.selectedIDs.value,[1]);assert.equal(m.items.value[0].readAt,'');assert.equal(m.unread.value,5);assert.equal(m.saving.value,false);assert.equal(m.stateNotice.value,'');assert.equal(m.error.value,'No access');assert.equal(m.events.length,0);m.stop()
})
await test('a pre-mutation poll cannot restore old flags or unread counts while the committed refresh is pending',async()=>{
 const old=deferred(),fresh=deferred(),m=setup('src/views/Notifications.vue',['load','markSelected','items','selectedIDs','loading','unread'],async(path,options)=>options?.method?{updated:1,unread:0}:m.calls.length===1?old.promise:fresh.promise)
 m.loading.value=false;m.items.value=[{id:1,readAt:''}];m.selectedIDs.value=[1];m.unread.value=1
 const poll=m.load(true),mutation=m.markSelected(true);await Promise.resolve();await Promise.resolve();old.resolve({items:[{id:1,readAt:''}],unread:1,total:1});await poll;assert.equal(m.unread.value,0);assert(m.items.value[0].readAt)
 fresh.resolve({items:[{id:1,readAt:'committed'}],unread:0,total:1});await mutation;assert.equal(m.items.value[0].readAt,'committed');m.stop()
})
await test('marking the final filtered page automatically returns to the last available page',async()=>{
 const m=setup('src/views/Notifications.vue',['load','offset','items','selectedIDs','loading'],async path=>new URL('http://local'+path).searchParams.get('offset')==='40'?{items:[],unread:1,total:1}:{items:[{id:9}],unread:1,total:1})
 m.offset.value=40;m.selectedIDs.value=[1];await m.load();assert.equal(m.offset.value,0);assert.equal(m.calls.length,2);assert.deepEqual(m.selectedIDs.value,[]);assert.equal(m.items.value[0].id,9);assert.equal(m.loading.value,false);m.stop()
})
await test('impersonation cannot mutate another inbox, and identity invalidation rejects late mutation results',async()=>{
 const m=setup('src/views/Notifications.vue',['markSelected','mark','all','items','selectedIDs','loading','session','writeLocked'],async()=>{throw Error('must not call')});m.loading.value=false;m.items.value=[{id:1}];m.selectedIDs.value=[1];m.session.value={impersonation:{adminId:'admin'}};assert(m.writeLocked.value);await m.markSelected(true);await m.mark({id:1},true);await m.all();assert.equal(m.calls.length,0);m.stop()
 const pending=deferred(),stale=setup('src/views/Notifications.vue',['mark','invalidateIdentity','items','selectedIDs','unread'],()=>pending.promise);stale.items.value=[{id:1,readAt:''}];stale.selectedIDs.value=[1];const action=stale.mark({id:1},true);stale.invalidateIdentity();pending.resolve({updated:1,unread:100});assert.equal(await action,false);assert.deepEqual(stale.items.value,[]);assert.deepEqual(stale.selectedIDs.value,[]);assert.equal(stale.unread.value,0);assert.equal(stale.events.length,0);stale.stop()
})
await test('late project access approval cannot navigate an inbox belonging to an ended identity',async()=>{
 const pending=deferred(),m=setup('src/views/Notifications.vue',['open','invalidateIdentity'],()=>pending.promise)
 const open=m.open({id:9,readAt:'already-read',projectId:'old-project',url:'/requirements?req=1'})
 assert.equal(m.calls[0].path,'/projects/old-project/visit');m.invalidateIdentity();pending.resolve({projectId:'old-project'});await open
 assert.equal(m.storage.get('devflow-project'),undefined);assert.equal(m.location.href,'');m.stop()
})
await test('notification controls compile with accessible state labels and compact non-wrapping actions',async()=>{
 const {parse,compileScript,compileTemplate,compileStyle}=require('vue/compiler-sfc'),source=readFileSync(new URL('../src/views/Notifications.vue',import.meta.url),'utf8'),descriptor=parse(source).descriptor,script=compileScript(descriptor,{id:'notifications'})
 assert.deepEqual(compileTemplate({source:descriptor.template.content,filename:'Notifications.vue',id:'notifications',compilerOptions:{bindingMetadata:script.bindings}}).errors,[])
 const compiled=compileStyle({source:descriptor.styles.map(s=>s.content).join('\n'),filename:'Notifications.vue',id:'notifications',scoped:false});assert.deepEqual(compiled.errors,[])
 const declarations=selector=>{const values={};compiled.rawResult.root.walkRules(rule=>{if(rule.selector===selector)rule.walkDecls(decl=>{values[decl.prop]=decl.value})});return values}
 assert.equal(declarations('.notification-list article>.notice-state-action')['white-space'],'nowrap');assert.equal(declarations('.notification-list article>.notice-state-action')['grid-column'],'3');assert.match(declarations('.notification-list article.unread')['box-shadow'],/inset 3px/);assert.notEqual(declarations('.notification-list article.unread').background,declarations('.notification-list article.read').background)
 assert.match(source,/:indeterminate="somePageSelected"/);assert.match(source,/:aria-label="t\('选择通知：\{title\}'/);assert.match(source,/class="notice-read-status"/);assert.match(source,/@click\.stop/);assert.doesNotMatch(source,/v-html/)
})
await test('member access sends stable ID and reason; failure retains confirmation draft',async()=>{
 const m=setup('src/views/Members.vue',['impersonating','reason','startImpersonation','error','saving','context'],async()=>{throw Error('forbidden')});m.context.value={canImpersonate:true,user:{id:'u-admin'}};m.impersonating.value={id:'u-specific',name:'同名',active:true};m.reason.value=' 核对站内提及通知 ';await m.startImpersonation();assert.deepEqual(JSON.parse(m.calls[0].options.body),{userId:'u-specific',reason:'核对站内提及通知'});assert.equal(m.impersonating.value.id,'u-specific');assert.equal(m.saving.value,false);assert.equal(m.location.href,'');m.stop()
})
await test('member access enters the project authorized by the server',async()=>{
 const m=setup('src/views/Members.vue',['impersonating','reason','startImpersonation','context'],async()=>({projectId:'p-target'}));m.context.value={canImpersonate:true,user:{id:'u-admin'}};m.impersonating.value={id:'u-member',active:true};m.reason.value='检查成员权限';await m.startImpersonation();assert.equal(m.storage.get('devflow-project'),'p-target');assert.equal(m.location.href,'/my-work');m.stop()
})
await test('late unread response cannot leak previous account counts after switching identities',async()=>{
 const pending=deferred(),m=setup('src/App.vue',['refreshUnread','session','unread'],()=>pending.promise);m.session.value={user:{id:'old'},project:{id:'p'}};const request=m.refreshUnread();assert.equal(m.calls.length,1);assert.equal(m.calls[0].path,'/notifications/unread-count');m.session.value={user:{id:'new'},project:{id:'p'}};m.unread.value=7;pending.resolve({unread:999});await request;assert.equal(m.unread.value,7);m.stop()
})
await test('expired access has explicit return path and restores admin project',async()=>{
 const m=setup('src/App.vue',['identityChanged','identityConflict','impersonationRecovery','stopImpersonation'],async()=>({projectId:'p-admin'}));m.identityChanged({detail:'impersonation_expired'});assert(m.identityConflict.value);assert(m.impersonationRecovery.value);await m.stopImpersonation();assert.equal(m.storage.get('devflow-project'),'p-admin');assert.equal(m.location.href,'/members');m.stop()
})
await test('refresh after target project revocation still exposes return-to-admin recovery',async()=>{
 const m=setup('src/App.vue',['load','impersonationRecovery','session'],async path=>{if(path==='/auth/impersonation')return {active:true};throw new APIError('No access',403,'project_forbidden')});await m.load();assert.equal(m.session.value,null);assert.equal(m.impersonationRecovery.value,true);m.stop()
})
await test('real API refuses soft session identity changes and retains the original write guard',async()=>{
 const exports={},requests=[],events=[];let user='admin'
 const source=readFileSync(new URL('../src/api.ts',import.meta.url),'utf8')
 const code=ts.transpileModule(source,{compilerOptions:{module:ts.ModuleKind.CommonJS,target:ts.ScriptTarget.ES2022}}).outputText
 new Function('require','exports','localStorage','window','CustomEvent','fetch',code)(()=>({locale:{value:'zh-CN'},t:x=>x}),exports,{getItem:()=>null},{dispatchEvent:e=>events.push(e)},class{constructor(type,options){this.type=type;this.detail=options?.detail}},async(path,options)=>{requests.push({path,options});return {ok:true,json:async()=>path==='/api/session'?{user:{id:user}}:{}}})
 await exports.api('/session');user='member';await assert.rejects(exports.api('/session'),error=>error.code==='identity_changed')
 await exports.api('/requirements',{method:'PATCH',body:'{}'});assert.equal(requests.at(-1).options.headers.get('X-TaskLoom-Expected-User'),'admin');assert(events.some(e=>e.detail==='identity_changed'))
 await exports.api('/auth/login',{method:'POST',body:'{}'});await exports.api('/session');await exports.api('/my-work');assert.equal(requests.at(-1).options.headers.get('X-TaskLoom-Expected-User'),'member')
})
console.log(`Passed ${count} access and notification regression tests.`)

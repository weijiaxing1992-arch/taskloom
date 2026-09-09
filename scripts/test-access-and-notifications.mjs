import { readFileSync } from 'node:fs'
import { createRequire } from 'node:module'
import assert from 'node:assert/strict'
import { createWorkspaceHarness } from './helpers/workspace-harness.mjs'
const require = createRequire(import.meta.url), ts = require('typescript'), Vue = require('vue')
class APIError extends Error { constructor(message,status,code=''){super(message);this.status=status;this.code=code} }
function setup(file,exposed,handler=async()=>({items:[]})){
 const source=readFileSync(new URL('../'+file,import.meta.url),'utf8').match(/<script setup[^>]*>([\s\S]*?)<\/script>/)[1]
 const calls=[],storage=new Map(),events=[],scope=Vue.effectScope(),exports={},location={href:'',origin:'http://devflow.local',reload(){this.reloaded=true}},language=Vue.ref('zh-CN')
 const localStorage={getItem:k=>storage.get(k)??null,setItem:(k,v)=>storage.set(k,v),removeItem:k=>storage.delete(k)},store=createWorkspaceHarness(localStorage)
 const api=async(path,options)=>{calls.push({path,options});return handler(path,options)}
 const imports={...store.imports,vue:{...Vue,onMounted:()=>{},onBeforeUnmount:()=>{}},'vue-router':{useRoute:()=>({query:{},path:'/notifications'}),useRouter:()=>({})}}
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
 const m=setup('src/views/Notifications.vue',['open','error'],async(path,options)=>{assert.equal(path,'/projects/project-target/visit');assert.equal(options.method,'POST');assert.equal(options.headers['X-DevFlow-Project'],'project-target');return {projectId:'project-target'}})
 await m.open(item);assert.equal(m.storage.get('devflow-project'),'project-target');assert.equal(m.location.href,'/requirements?req=7');assert.equal(m.error.value,'');m.stop()
 const invalid=setup('src/views/Notifications.vue',['open','error'],async()=>{throw Error('should not request')})
 await invalid.open({...item,url:'https://outside.example/steal'});assert.equal(invalid.calls.length,0);assert.equal(invalid.error.value,'通知链接无效，请刷新后重试');invalid.stop()
 const revoked=setup('src/views/Notifications.vue',['open','error'],async()=>{throw Error('project access revoked')})
 await revoked.open(item);assert.equal(revoked.storage.get('devflow-project'),undefined);assert.equal(revoked.location.href,'');assert.equal(revoked.error.value,'project access revoked');revoked.stop()
})
await test('background refresh keeps current messages visible and preserves errors on transient failure',async()=>{
 const m=setup('src/views/Notifications.vue',['load','items','loading','error'],async()=>{throw Error('offline')});m.loading.value=false;m.items.value=[{id:1}];m.error.value='Existing error';await m.load(true);assert.equal(m.loading.value,false);assert.deepEqual(m.items.value,[{id:1}]);assert.equal(m.error.value,'Existing error');m.stop()
})
await test('background notification refresh does not overlap and a newer foreground result wins',async()=>{
 const old=deferred(),m=setup('src/views/Notifications.vue',['load','items','loading'],()=>m.calls.length===1?old.promise:Promise.resolve({items:[{id:2}],unread:0}));m.loading.value=false;const pending=m.load(true);await m.load(true);assert.equal(m.calls.length,1);await m.load();old.resolve({items:[{id:1}],unread:4});await pending;assert.equal(m.items.value[0].id,2);assert.equal(m.loading.value,false);m.stop()
})
await test('member access sends stable ID and reason; failure retains confirmation draft',async()=>{
 const m=setup('src/views/Members.vue',['impersonating','reason','startImpersonation','error','saving'],async()=>{throw Error('forbidden')});m.impersonating.value={id:'u-specific',name:'同名'};m.reason.value=' 核对站内提及通知 ';await m.startImpersonation();assert.deepEqual(JSON.parse(m.calls[0].options.body),{userId:'u-specific',reason:'核对站内提及通知'});assert.equal(m.impersonating.value.id,'u-specific');assert.equal(m.saving.value,false);assert.equal(m.location.href,'');m.stop()
})
await test('member access enters the project authorized by the server',async()=>{
 const m=setup('src/views/Members.vue',['impersonating','reason','startImpersonation'],async()=>({projectId:'p-target'}));m.impersonating.value={id:'u-member'};m.reason.value='检查成员权限';await m.startImpersonation();assert.equal(m.storage.get('devflow-project'),'p-target');assert.equal(m.location.href,'/my-work');m.stop()
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
 await exports.api('/requirements',{method:'PATCH',body:'{}'});assert.equal(requests.at(-1).options.headers.get('X-DevFlow-Expected-User'),'admin');assert(events.some(e=>e.detail==='identity_changed'))
 await exports.api('/auth/login',{method:'POST',body:'{}'});await exports.api('/session');await exports.api('/my-work');assert.equal(requests.at(-1).options.headers.get('X-DevFlow-Expected-User'),'member')
})
console.log(`Passed ${count} access and notification regression tests.`)

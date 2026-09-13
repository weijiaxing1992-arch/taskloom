import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { createRequire } from 'node:module'
const require=createRequire(import.meta.url),ts=require('typescript')
const source=readFileSync(new URL('../src/desktopNotifications.ts',import.meta.url),'utf8')
const code=ts.transpileModule(source,{compilerOptions:{module:ts.ModuleKind.CommonJS,target:ts.ScriptTarget.ES2022}}).outputText
function harness(){
 const storage=new Map(),listeners=new Map(),documentListeners=new Map(),channels=new Map(),shown=[],events=[],timers=[]
 const session={tenant:{id:'tn'},user:{id:'u'}}
 const location={origin:'https://taskloom.example.com',assign:url=>events.push(url)}
 class Notice {static permission='granted'; static async requestPermission(){return this.permission};constructor(title,options){this.title=title;this.options=options;shown.push(this)}close(){this.closed=true}}
 const window={isSecureContext:true,Notification:Notice,addEventListener:(name,fn)=>{const list=listeners.get(name)||new Set();list.add(fn);listeners.set(name,list)},removeEventListener:(name,fn)=>listeners.get(name)?.delete(fn),dispatchEvent:event=>{events.push(event);listeners.get(event.type)?.forEach(fn=>fn(event))},focus(){},open(){return {location:{},close(){}}},confirm(){return true}}
 const document={visibilityState:'visible',addEventListener:(name,fn)=>{const list=documentListeners.get(name)||new Set();list.add(fn);documentListeners.set(name,list)},removeEventListener:(name,fn)=>documentListeners.get(name)?.delete(fn),dispatchEvent:event=>documentListeners.get(event.type)?.forEach(fn=>fn(event))}
 const localStorage={getItem:k=>storage.get(k)||null,setItem:(k,v)=>storage.set(k,v),removeItem:k=>storage.delete(k)}
 const navigator={locks:{async request(key,options,fn){return fn({name:key})}}},exports={}
 class BroadcastChannel {constructor(name){this.name=name;const peers=channels.get(name)||new Set();peers.add(this);channels.set(name,peers)}postMessage(data){for(const peer of channels.get(this.name)||[])if(peer!==this)peer.onmessage?.({data})}close(){channels.get(this.name)?.delete(this)}}
 new Function('exports','window','location','localStorage','navigator','Notification','CustomEvent','Event','setInterval','clearInterval','AudioContext','document','BroadcastChannel',code)(exports,window,location,localStorage,navigator,Notice,class{constructor(type,options){this.type=type;this.detail=options?.detail}},class{constructor(type){this.type=type}},fn=>{timers.push(fn);return timers.length},()=>{},class{},document,BroadcastChannel)
 return {...exports,session,window,document,localStorage,storage,shown,events,timers,Notice,navigator}
}
const date='2026-09-08T12:00:00Z', time=Date.parse(date)
const row=(id,extra={})=>({id,createdAt:date,title:'需求更新',body:'说明',eventType:'requirement.updated',projectId:'p',projectName:'研发',url:'/requirements?req=1',...extra})
let passed=0
async function test(name,run){await run();passed++;console.log('✓ '+name)}
await test('所有通知分类均可读，未知事件不丢弃',()=>{const h=harness();for(const type of ['requirement.mentioned','defect.assigned','sprint.status_changed','test.failed','new.event'])assert(h.noticeCategory(type));assert.equal(h.noticeCategory('requirement.mentioned'),'提及与回复')})
await test('只允许同源业务详情路径',()=>{const h=harness();for(const url of ['https://evil.test/a','//evil.test/a','javascript:alert(1)','/api/auth/logout','/help','https://user:secret@taskloom.example.com/requirements'])assert.equal(h.safeNoticeURL(url,'https://taskloom.example.com'),null);assert.equal(h.safeNoticeURL('/requirements?req=3','https://taskloom.example.com'),'/requirements?req=3')})
await test('停用、代访问、首登改密和空会话不具有通知作用域',()=>{const h=harness();for(const s of [null,{...h.session,impersonation:{}},{...h.session,user:{id:'u',operationDisabled:true}},{...h.session,user:{id:'u',mustChangePassword:true}}])assert.equal(h.desktopScope(s),'');assert(h.desktopScope(h.session).endsWith(':tn:u'))})
await test('首次基线跨越同一秒多页历史消息，不重放',async()=>{const h=harness();const rows=Array.from({length:150},(_,i)=>row(150-i));const result=await h.collectNewNotices(async path=>{const offset=Number(new URL('https://a'+path).searchParams.get('offset'));return {items:rows.slice(offset,offset+100),hasMore:offset+100<rows.length}},null);assert.equal(result.items.length,0);assert.equal(result.checkpoint.ids.length,150)})
await test('空基线后的同秒首条和全部类型可通知',async()=>{const h=harness();const base=await h.collectNewNotices(async()=>({items:[],hasMore:false}),null);const result=await h.collectNewNotices(async()=>({items:[row(1)],hasMore:false}),base.checkpoint);assert.deepEqual(result.items.map(x=>x.id),[1])})
await test('超过一页的新消息不丢失，已读不振铃',async()=>{const h=harness(),rows=Array.from({length:151},(_,i)=>row(151-i,{readAt:i===0?'read':undefined}));const result=await h.collectNewNotices(async path=>{const offset=Number(new URL('https://a'+path).searchParams.get('offset'));return {items:rows.slice(offset,offset+100),hasMore:offset+100<rows.length}},{time,ids:[1]});assert.equal(result.items.length,149);assert.equal(result.items[0].id,2)})
await test('非法分页或断网抛错，不产出新的游标',async()=>{const h=harness();await assert.rejects(h.collectNewNotices(async()=>({items:[{id:1}],hasMore:false}),{time,ids:[]}));await assert.rejects(h.collectNewNotices(async()=>{throw Error('offline')},{time,ids:[]}))})
await test('拒绝系统权限时不读取通知正文',async()=>{const h=harness();h.Notice.permission='denied';h.saveDesktopPreferences(h.desktopScope(h.session),{enabled:true});let calls=0;const stop=h.startDesktopNotifications(async()=>{calls++;return {}},()=>h.session);await new Promise(r=>setTimeout(r,0));assert.equal(calls,0);stop()})
await test('授权状态会严格归一化，未知桥接返回不得误开启提醒',async()=>{
 const h=harness()
 for(const permission of ['granted','default','denied']){h.Notice.permission=permission;assert.equal(await h.desktopPermission(),permission);assert.equal(await h.desktopPermission(true),permission)}
 h.Notice.permission='unexpected';assert.equal(await h.desktopPermission(),'error')
 h.window.isSecureContext=false;assert.equal(await h.desktopPermission(),'unsupported')
 h.window.isSecureContext=true;h.window.webkit={messageHandlers:{devflowDesktop:{postMessage:async()=>({permission:'granted'})}}};assert.equal(await h.desktopPermission(),'granted')
 h.window.webkit={messageHandlers:{devflowDesktop:{postMessage:async()=>({status:'not-a-permission'})}}};assert.equal(await h.desktopPermission(),'error')
})
await test('拒绝、待授权和不支持状态提供不同且可执行的中文恢复说明',()=>{
 const h=harness()
 assert.match(h.desktopPermissionGuidance('default',false),/浏览器弹窗/)
 assert.match(h.desktopPermissionGuidance('denied',false),/地址栏左侧/)
 assert.match(h.desktopPermissionGuidance('denied',true),/系统设置 → 通知 → TaskLoom/)
 assert.match(h.desktopPermissionGuidance('unsupported',false),/HTTPS/)
 assert.match(h.desktopPermissionGuidance('error',true),/重新检查授权/)
})
await test('重新加载和两个标签页共享游标，不重复弹出',async()=>{const h=harness();h.saveDesktopPreferences(h.desktopScope(h.session),{enabled:true,sound:false});let rows=[row(1)];const api=async path=>path==='/session'?h.session:{items:rows,hasMore:false};const stop=h.startDesktopNotifications(api,()=>h.session);await new Promise(r=>setTimeout(r,0));rows=[row(2),row(1)];await h.timers[0]();assert.equal(h.shown.length,1);await h.timers[0]();assert.equal(h.shown.length,1);stop();const stop2=h.startDesktopNotifications(api,()=>h.session);await new Promise(r=>setTimeout(r,0));assert.equal(h.shown.length,1);stop2()})
await test('退出登录清理横幅并阻止迟到响应',async()=>{const h=harness();h.saveDesktopPreferences(h.desktopScope(h.session),{enabled:true});let resolve;const api=async path=>path==='/session'?h.session:new Promise(r=>resolve=r);const stop=h.startDesktopNotifications(api,()=>h.session);await new Promise(r=>setTimeout(r,0));h.window.dispatchEvent({type:'devflow-auth-session-ended'});resolve({items:[row(1)],hasMore:false});await new Promise(r=>setTimeout(r,0));assert.equal(h.shown.length,0);stop()})
await test('点击通知必须先验证账号和通知，再复验项目权限',async()=>{const h=harness(),calls=[];await h.openDesktopNotice(async(path)=>{calls.push(path);return path==='/session'?h.session:{}},h.desktopScope(h.session),row(1));assert.deepEqual(calls,['/session','/notifications/1','/projects/p/visit','/session']);await assert.rejects(h.openDesktopNotice(async()=>({tenant:{id:'tn'},user:{id:'other'}}),h.desktopScope(h.session),row(1)))})
await test('通知读取请求不随当前项目或通知分类筛选',async()=>{const h=harness();await h.collectNewNotices(async path=>{assert.equal(path,'/notifications?limit=100&offset=0');return {items:[],hasMore:false}},null)})
await test('跨标签同步只传播作用域和事件标识，页面恢复后立即检查新通知',()=>{
 assert.match(source,/devflow-desktop-notification-v1/)
 assert.match(source,/type: 'sync', scope/)
 assert.match(source,/type: 'sound', scope, id/)
 assert.match(source,/document\.addEventListener\('visibilitychange', visible\)/)
 assert.match(source,/window\.addEventListener\('devflow-notifications-changed', refresh\)/)
 assert.match(source,/document\.removeEventListener\('visibilitychange', visible\)/)
})
await test('通知中心显示授权状态、恢复步骤和无副作用的重新检查入口',()=>{
 const view=readFileSync(new URL('../src/components/DesktopNotifications.vue',import.meta.url),'utf8')
 assert.match(view,/desktopPermissionGuidance/)
 assert.match(view,/refreshPermission/)
 assert.match(view,/next === 'granted' && permissionMessage\.value/)
 assert.match(view,/重新检查授权/)
 assert.doesNotMatch(view,/v-if="!enabled && \['denied','unsupported','error'\]/)
 assert.match(view,/正在请求授权/)
 assert.match(view,/系统权限：已拒绝/)
 // The community release includes browser notifications; no native bridge binary is shipped.
 assert.match(source,/new Notification\(title,/)
})
console.log(`Desktop notifications: ${passed} tests passed`)

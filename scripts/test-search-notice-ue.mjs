import assert from 'node:assert/strict'
import {readFileSync} from 'node:fs'
import ts from 'typescript'
import * as Vue from 'vue'

const read=path=>readFileSync(new URL('../'+path,import.meta.url),'utf8')
const compile=source=>ts.transpileModule(source,{compilerOptions:{module:ts.ModuleKind.CommonJS,target:ts.ScriptTarget.ES2022}}).outputText
function evaluate(source,imports={},globals={}){const exports={};new Function('require','exports',...Object.keys(globals),compile(source))(id=>imports[id]||{},exports,...Object.values(globals));return exports}
const display=evaluate(read('src/notificationDisplay.ts'))
const translate=(key,params={})=>key.replace(/\{(\w+)\}/g,(all,name)=>params[name]??all)
const notice=(body,extra={})=>({eventType:'requirement.updated',subjectType:'requirement',subjectId:1,body,...extra})
let count=0
async function test(name,run){await run();count++;console.log('✓ '+name)}

await test('generated change summaries merge storage aliases and canonicalize only the matching subject line',()=>{
 const original=notice('需求已更新：description, descriptionDoc, ownerUserId, ownerUserIds\nREQ-0001 标题写着 REQ-0002\n用户正文 description,descriptionDoc\nREQ-0001 在正文仍原样保留'),copy=structuredClone(original)
 assert.equal(display.notificationBody(original,translate),'需求已更新：需求描述、负责人\n000001 标题写着 REQ-0002\n用户正文 description,descriptionDoc\nREQ-0001 在正文仍原样保留')
 assert.deepEqual(original,copy)
})
await test('comments, assignments, unknown templates and foreign subject IDs preserve their text exactly',()=>{
 const body='需求已更新：description, descriptionDoc\nREQ-0001 用户输入'
 for(const extra of [{eventType:'requirement.mentioned'},{eventType:'requirement.assigned'},{subjectType:'defect'}])assert.equal(display.notificationBody(notice(body,extra),translate),body)
 for(const body of ['description,descriptionDoc REQ-0001','需求已更新：unknownFutureField\nREQ-0001 保留未知格式','我的文本\nREQ-0001'])assert.equal(display.notificationBody(notice(body),translate),body)
 assert.match(display.notificationBody(notice('需求已更新：description\nREQ-0002 另一需求'),translate),/\nREQ-0002 /)
 assert.match(display.notificationBody(notice('需求已更新：description\nREQ-0001 保留',{subjectId:undefined}),translate),/\nREQ-0001 /)
})
await test('English generated summaries and status changes retain user-defined status and description text',()=>{
 const t=(key,params={})=>key==='需求已更新：{fields}'?'Requirement updated: '+params.fields:key==='需求描述'?'Description':key
 assert.equal(display.notificationBody(notice('Requirement updated: description, descriptionDoc\nREQ-0001 Original title\nUser text'),t),'Requirement updated: Description\n000001 Original title\nUser text')
 const status=notice('需求状态已变更为「自定义状态」\nREQ-0001 Original title\nREQ-0001 comment',{eventType:'requirement.status_changed'})
 assert.equal(display.notificationBody(status,translate),'需求状态已变更为「自定义状态」\n000001 Original title\nREQ-0001 comment')
})

function searchFixture(handler){
 const scope=Vue.effectScope(),unmounts=[],calls=[],location={href:''},storage=new Map()
 const source=read('src/views/Search.vue').match(/<script setup[^>]*>([\s\S]*?)<\/script>/)[1]
 const imports={vue:{...Vue,onMounted(){},onBeforeUnmount:fn=>unmounts.push(fn)},'vue-router':{useRoute:()=>({query:{}})},'../api':{api:async(path,options)=>{calls.push({path,options});return handler(path,options)}},'../i18n':{t:translate},'../topSearch':evaluate(read('src/topSearch.ts')),'../recentSearches':evaluate(read('src/recentSearches.ts')),'../requirementPaging':{requirementExportLimit:20000}}
 const state=scope.run(()=>evaluate(source+'\nexport {search,go,items,total,loading,error,searched,invalidateSearchIdentity,recentQueries,projects}',imports,{window:{removeEventListener(){}},localStorage:{setItem:(key,value)=>storage.set(key,value)},location}))
 return{...state,calls,location,stop(){unmounts.forEach(fn=>fn());scope.stop()}}
}
await test('search refresh retains prior results and disables stale navigation until a successful response',async()=>{
 let resolve;const pending=new Promise(done=>resolve=done),m=searchFixture(()=>pending),old={id:1,title:'Previous result',type:'需求',projectId:'p',url:'/requirements?req=1'}
 m.items.value=[old];m.total.value=1;m.searched.value=true;const loading=m.search();assert.equal(m.loading.value,true);assert.equal(m.items.value[0].title,'Previous result');m.go(old);assert.equal(m.location.href,'')
 resolve({items:[{...old,id:2,title:'Current result'}],total:1});await loading;assert.equal(m.loading.value,false);assert.equal(m.items.value[0].title,'Current result');m.stop()
})
await test('search failures retain context, retry replaces it, and malformed data never reports an empty success',async()=>{
 let response='offline';const m=searchFixture(async()=>{if(response==='offline')throw Error('offline');return response==='malformed'?{items:null,total:0}:{items:[],total:0}})
 m.items.value=[{id:1,title:'Retained'}];m.total.value=1;await m.search();assert.equal(m.error.value,'offline');assert.equal(m.items.value.length,1);assert.equal(m.loading.value,false)
 response='malformed';await m.search();assert.match(m.error.value,/搜索结果格式/);assert.equal(m.items.value.length,1)
 response='valid';await m.search();assert.equal(m.error.value,'');assert.deepEqual(m.items.value,[]);assert.equal(m.searched.value,true);m.stop()
})
await test('templates preserve populated lists during refresh and never show empty success alongside errors',()=>{
 const search=read('src/views/Search.vue'),notices=read('src/views/Notifications.vue')
 assert.match(search,/v-if="items.length" class="search-results" :aria-busy="loading" :inert="loading \|\| !!error"/)
 assert.match(search,/v-if="!loading && !error && searched && !items.length"/)
 assert.match(notices,/v-if="items.length" class="notification-list" :aria-busy="loading" :inert="loading"/)
 assert.match(notices,/v-if="!loading && !error && !items.length"/)
 assert.match(notices,/notificationBody\(x,t\)/);assert.match(notices,/header>[\s\S]*?notice-read-status[\s\S]*?<b>\{\{ x.title \}\}/)
 assert.doesNotMatch(notices,/v-html/)
})
await test('identity invalidation clears retained results and history and rejects late responses',async()=>{
 let resolve;const pending=new Promise(done=>resolve=done),m=searchFixture(()=>pending)
 m.items.value=[{id:1,title:'Private previous result'}];m.projects.value=[{id:'private-project'}];m.recentQueries.value=['private query'];const loading=m.search(),signal=m.calls[0].options.signal
 m.invalidateSearchIdentity();assert.equal(signal.aborted,true);assert.deepEqual(m.items.value,[]);assert.deepEqual(m.projects.value,[]);assert.deepEqual(m.recentQueries.value,[]);assert.equal(m.loading.value,false)
 resolve({items:[{id:2,title:'Late private result'}],total:1});await loading;await m.search();assert.deepEqual(m.items.value,[]);assert.equal(m.calls.length,1);m.stop()
})
console.log(`Passed ${count} search and notification UX regressions.`)

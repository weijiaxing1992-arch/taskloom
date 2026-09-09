import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import ts from 'typescript'
import * as Vue from 'vue'
import { parse, compileScript, compileTemplate } from 'vue/compiler-sfc'

const read=path=>readFile(new URL('../'+path,import.meta.url),'utf8')
const transpile=source=>ts.transpileModule(source,{compilerOptions:{module:ts.ModuleKind.CommonJS,target:ts.ScriptTarget.ES2022}}).outputText
const flush=async()=>{for(let i=0;i<8;i++){await Promise.resolve();await Vue.nextTick()}}
const defer=()=>{let resolve,reject;const promise=new Promise((yes,no)=>{resolve=yes;reject=no});return{resolve,reject,promise}}
const source=await read('src/components/QualityComments.vue')
function mount(handler=async path=>path==='/session'?{user:{role:'qa'}}:{items:[]}){
 const descriptor=parse(source).descriptor,script=compileScript(descriptor,{id:'quality-comments'}),template=compileTemplate({id:'quality-comments',source:descriptor.template.content,filename:'QualityComments.vue',compilerOptions:{bindingMetadata:script.bindings}});assert.deepEqual(template.errors,[])
 const props=Vue.reactive({resource:'test-cases',objectId:7,members:[{id:'front',name:'前端',active:true}]}),scope=Vue.effectScope(),starts=[],ends=[],guards=[],calls=[],window=new EventTarget(),locked=Vue.ref(false),confirmation={allowed:false,count:0}
 window.confirm=()=>{confirmation.count++;return confirmation.allowed}
 const scoped={locked,project:'prj_orbit',current:()=>!locked.value,request:async(path,options={})=>{calls.push({path,options});return handler(path,options)}}
 const imports={vue:{...Vue,onMounted:fn=>starts.push(fn),onBeforeUnmount:fn=>ends.push(fn)},'vue-router':{onBeforeRouteLeave:fn=>guards.push(fn),onBeforeRouteUpdate:fn=>guards.push(fn)},'../i18n':{t:value=>value,formatDate:String},'./settingsScope':{useSettingsScope:()=>scoped},'./CodeTextView.vue':{default:{name:'CodeTextView'}},'./MentionComment.vue':{default:{name:'MentionComment'}},'./CommentReplyContext.vue':{default:{name:'CommentReplyContext'}}},module={}
 new Function('require','exports','window','localStorage',transpile(script.content))(key=>imports[key],module,window,{getItem:()=> 'prj_orbit'})
 const state=scope.run(()=>module.default.setup(props,{expose:()=>{}}));starts.forEach(fn=>fn())
 return{...state,props,window,locked,calls,guards,confirmation,stop:()=>{ends.forEach(fn=>fn());scope.stop()}}
}
let count=0
async function test(name,run){await run();count++;console.log('✓ '+name)}
await test('comments load actual object endpoint and publish exact selected IDs, preserving source text',async()=>{
 const m=mount(async(path,options)=>options.method==='POST'?{id:12,author:'测试',body:JSON.parse(options.body).body,mentionUserIds:['front'],createdAt:'now'}:path==='/session'?{user:{role:'qa'}}:{items:[]});await flush();assert.equal(m.mutable.value,true);m.body.value='@前端 请检查';m.mentionUserIds.value=['front'];await m.submit({body:m.body.value,mentionUserIds:['front']});assert.equal(m.comments.value[0].body,'@前端 请检查');assert.equal(m.body.value,'');assert.deepEqual(m.mentionUserIds.value,[]);const call=m.calls.find(x=>x.options.method==='POST');assert.equal(call.path,'/test-cases/7/comments');assert.deepEqual(JSON.parse(call.options.body),{body:'@前端 请检查',mentionUserIds:['front']});m.stop()
})
await test('failure retains comment draft and chosen recipients; retry does not append a fake comment',async()=>{
 const m=mount(async(path,options)=>{if(options.method==='POST')throw Error('validation failed');return path==='/session'?{user:{role:'qa'}}:{items:[]}});await flush();m.body.value='@前端 内容';m.mentionUserIds.value=['front'];await m.submit({body:m.body.value,mentionUserIds:['front']});assert.equal(m.error.value,'validation failed');assert.equal(m.body.value,'@前端 内容');assert.deepEqual(m.mentionUserIds.value,['front']);assert.equal(m.comments.value.length,0);assert.equal(m.saving.value,false);m.stop()
})
await test('in-flight post blocks duplicate sends and leaving; drafts require explicit discard',async()=>{
 const pending=defer(),m=mount(async(path,options)=>options.method==='POST'?pending.promise:path==='/session'?{user:{role:'qa'}}:{items:[]});await flush();m.body.value='内容';assert.equal(m.canLeave(),false);m.confirmation.allowed=true;assert.equal(m.canLeave(),true);const posting=m.submit({body:'内容',mentionUserIds:[]});await flush();assert.equal(m.canLeave(),false);await m.submit({body:'重复',mentionUserIds:[]});assert.equal(m.calls.filter(x=>x.options.method==='POST').length,1);pending.resolve({id:1,body:'内容',mentionUserIds:[]});await posting;assert.equal(m.canLeave(),true);m.stop()
})
await test('viewer and stale account contexts cannot publish',async()=>{
 const m=mount(async path=>path==='/session'?{user:{role:'viewer'}}:{items:[]});await flush();assert.equal(m.mutable.value,false);await m.submit({body:'越权',mentionUserIds:[]});assert.equal(m.calls.filter(x=>x.options.method==='POST').length,0);m.role.value='qa';m.locked.value=true;await m.submit({body:'错身份',mentionUserIds:[]});assert.equal(m.calls.filter(x=>x.options.method==='POST').length,0);m.stop()
})
await test('late responses cannot overwrite a different test object or restore a discarded draft',async()=>{
 const first=defer(),m=mount(async path=>path==='/session'?{user:{role:'qa'}}:path.includes('/7/')?first.promise:{items:[{id:9,body:'新对象评论'}]});await flush();m.props.objectId=8;await flush();first.resolve({items:[{id:2,body:'旧对象评论'}]});await flush();assert.equal(m.comments.value[0].body,'新对象评论');assert(m.calls.some(x=>x.path==='/test-cases/8/comments'));m.stop()
})
await test('split case, plan and execution drawers include real comment components and guarded closes',async()=>{
 const [library,operations]=await Promise.all([read('src/components/testing/TestCaseLibrary.vue'),read('src/components/testing/TestingOperations.vue')])
 assert(library.includes('resource="test-cases"'))
 for(const resource of ['test-plans','test-executions'])assert(operations.includes(`resource="${resource}"`))
 assert.match(library,/comments\.value\?\.canLeave\(\)/)
 assert.match(operations,/commentsPanel\.value\?\.canLeave\(\)/)
 assert.match(source,/MentionComment v-if="mutable"/);assert(!source.includes('v-html'))
})
console.log(`Passed ${count} quality comment regressions.`)

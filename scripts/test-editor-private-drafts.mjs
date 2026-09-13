import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import ts from 'typescript'
import * as Vue from 'vue'

const read = path => readFile(new URL('../' + path, import.meta.url), 'utf8')
async function pure(path) {
  const js = ts.transpileModule(await read(path), {compilerOptions:{module:ts.ModuleKind.ESNext,target:ts.ScriptTarget.ES2022}}).outputText
  return import('data:text/javascript;base64,' + Buffer.from(js).toString('base64'))
}
const fields=await pure('src/requirementFields.ts'),mentions=await pure('src/mentions.ts')
const source=await read('src/views/Editor.vue'),script=source.match(/<script setup[^>]*>([\s\S]*?)<\/script>/)[1]
const names=['load','save','savePrivateDraft','restoreDraft','draftRecovery','draftPayload','draftContext','requestedDraftId','f','dirty','error','notice','baseline','formElement','descriptionDraftRevision','requestClose','finishLeave','savedAssigneeIds','editorDatesValid','titleAssistant','saving','moreFields','expanded','peopleExpanded']
const js=ts.transpileModule(script+'\nexport {'+names.join(',')+'}',{compilerOptions:{module:ts.ModuleKind.CommonJS,target:ts.ScriptTarget.ES2022}}).outputText
function deferred(){let resolve;return {promise:new Promise(done=>resolve=done),resolve:value=>resolve(value)}}
async function mount({edit=false,embedded=false,parentId=9,failSave=false}={}){
  const scope=Vue.effectScope(),exports={},calls=[],events=[],navigations=[],unmounts=[]
  const props=Vue.reactive({embedded,parentId,requirementId:edit&&embedded?11:undefined,restoreId:embedded?'child-draft':''})
  const route={params:edit&&!embedded?{id:'11'}:{},query:{draft:'url-draft'}}
  const api=async(path,options)=>{
    calls.push({path,options})
    if(options?.method){if(failSave)throw Error('offline');return {id:edit?11:901,...JSON.parse(options.body)}}
    if(path==='/session')return {user:{id:'u_me',role:'product'},project:{id:'prj_one'}}
    if(path==='/requirement-workflow')return {initialStatus:'规划中'}
    if(path==='/members')return {items:[{id:'u_me',name:'产品',active:true,role:'product',projectRole:'product'}]}
    if(/^\/requirements\/\d+$/.test(path))return {id:Number(path.split('/').at(-1)),title:'服务端标题',code:'REQ-11',category:'客户端',parentId:null,status:'开发中',createdAt:'2026-09-01',updatedAt:'2026-09-04T00:00:00Z',assigneeUserIds:['u_old'],assignees:[{id:'u_old',name:'历史人员'}]}
    return {items:[]}
  }
  const imports={'../layoutScope':{useLayoutBoolean:(_key,fallback)=>Vue.ref(fallback)},vue:{...Vue,onMounted:()=>{},onBeforeUnmount:fn=>unmounts.push(fn)},'vue-router':{useRoute:()=>route,useRouter:()=>({push:async path=>navigations.push(path)}),onBeforeRouteLeave:()=>{},onBeforeRouteUpdate:()=>{}},'../api':{api},'../i18n':{t:value=>value,categoryLabel:value=>value,formatDate:value=>value},'../requirementFields':fields,'../mentions':mentions}
  scope.run(()=>new Function('require','exports','window','defineProps','defineEmits','defineExpose',js)(key=>key.endsWith('.vue')?{}:imports[key],exports,{addEventListener(){},removeEventListener(){},confirm:()=>true},()=>props,()=>(...args)=>events.push(args),()=>{}))
  exports.formElement.value={checkValidity:()=>true,reportValidity:()=>true}
  await exports.load()
  return {...exports,calls,events,navigations,props,stop(){unmounts.forEach(fn=>fn());scope.stop()}}
}
let count=0
async function test(name,fn){await fn();count++;console.log('✓ '+name)}
await test('quick capture defaults collapsed while editing stays expanded and toggles preserve field values',async()=>{
  const m=await mount();assert.equal(m.expanded.value,false)
  m.f.acceptance='验收保留';m.f.customFields={testers:['u_me']};m.moreFields.value=true;assert.equal(m.expanded.value,true)
  m.moreFields.value=false;assert.equal(m.f.acceptance,'验收保留');assert.deepEqual([...m.f.customFields.testers],['u_me']);m.stop()
  const editing=await mount({edit:true});assert.equal(editing.expanded.value,true);editing.stop()
})
await test('invalid hidden fields expand before native focus and do not submit or erase the draft',async()=>{
  const m=await mount(),details={open:false};m.f.title='保留标题';m.peopleExpanded.value=false;let focused=false
  m.formElement.value={checkValidity:()=>false,querySelectorAll:()=>[details],reportValidity:()=>{assert.equal(m.expanded.value,true);assert.equal(m.peopleExpanded.value,true);assert.equal(details.open,true);focused=true;return false}}
  await m.save();assert.equal(focused,true);assert.equal(m.calls.some(call=>call.options?.method),false);assert.equal(m.f.title,'保留标题');m.stop()
})
await test('manual private drafts save incomplete forms without AI, native validity or a requirement request',async()=>{
  const m=await mount();let saves=0,ai=0
  m.draftRecovery.value={saveNow:async()=>{saves++;return true},complete:async()=>{throw Error('must not complete')}}
  m.titleAssistant.value={generate:async()=>{ai++;return true},cancel(){}}
  m.formElement.value={reportValidity:()=>{throw Error('draft must not validate the business form')}}
  m.editorDatesValid.start=false;m.f.description='尚无标题'
  await m.savePrivateDraft();await m.save(false,true)
  assert.equal(saves,2);assert.equal(ai,0);assert.equal(m.calls.filter(call=>call.options?.method).length,0);assert.equal(m.dirty.value,true);m.stop()
})
await test('payload and restore exclude server identity, timestamps and workflow state',async()=>{
  const m=await mount({edit:true}),baseline=m.baseline.value
  assert.equal('createdAt' in m.draftPayload.value,false);assert.equal('status' in m.draftPayload.value,false);assert.equal('updatedAt' in m.draftPayload.value,false)
  m.restoreDraft({title:'恢复标题',id:999,code:'EVIL',createdAt:'invalid',updatedAt:'older',status:'已上线',assigneeUserIds:[],description:'@产品 评审',descriptionMentionUserIds:['u_me'],descriptionMentionNames:{u_me:'产品'}},{baselineUpdatedAt:'older'},{kind:'requirement',targetId:'11'})
  assert.equal(m.f.title,'恢复标题');assert.equal(m.f.id,11);assert.equal(m.f.code,'REQ-11');assert.equal(m.f.createdAt,'2026-09-01');assert.equal(m.f.updatedAt,'2026-09-04T00:00:00Z');assert.equal(m.f.status,'开发中')
  assert.equal(m.baseline.value,baseline);assert.deepEqual([...m.savedAssigneeIds.value],['u_old']);assert.equal(m.dirty.value,true);assert.equal(m.descriptionDraftRevision.value,1);assert.match(m.notice.value,/较早/);m.stop()
})
await test('rich pending assets and explicit same-name mention IDs survive restoration',async()=>{
  const m=await mount(),doc={type:'doc',content:[{type:'image',attrs:{attachmentId:null,name:'screenshot.png',data:'YWJj'}},{type:'mention',attrs:{id:'u_me',label:'同名'}}]}
  m.restoreDraft({descriptionDoc:doc,description:'同名 截图',descriptionMentionUserIds:['u_me'],descriptionMentionNames:{u_me:'同名'},roleWeights:{frontend:{userIds:['u_me'],userId:'u_me',value:200}},customFields:{testers:['u_me']}})
  assert.deepEqual(JSON.parse(JSON.stringify(m.f.descriptionDoc)),doc);assert.deepEqual([...m.f.descriptionMentionUserIds],['u_me']);assert.equal(m.f.roleWeights.frontend.value,200);assert.equal(m.dirty.value,true);m.stop()
})
await test('wrong work-item targets and embedded parent contexts cannot overwrite forms',async()=>{
  const m=await mount({embedded:true});const old=m.f.title
  m.restoreDraft({title:'错父',parentId:10},{parentId:10},{kind:'requirement',targetId:''});assert.equal(m.f.title,old);assert.match(m.error.value,/不匹配/)
  m.restoreDraft({title:'错需求',parentId:9},{parentId:9},{kind:'requirement',targetId:'11'});assert.equal(m.f.title,old)
  m.restoreDraft({title:'错类型',parentId:9},{parentId:9},{kind:'defect',targetId:''});assert.equal(m.f.title,old)
  m.restoreDraft({title:'子需求草稿',parentId:9},{parentId:9},{kind:'requirement',targetId:''});assert.equal(m.f.title,'子需求草稿');assert.equal(m.f.parentId,9);m.stop()
})
await test('malformed restore is atomic rather than partially replacing valid fields',async()=>{
  const m=await mount({edit:true}),before=JSON.stringify(m.f)
  m.restoreDraft({title:'不应采用',assigneeUserIds:[2],customFields:[]})
  assert.equal(JSON.stringify(m.f),before);assert.match(m.error.value,/格式无效/);m.stop()
})
await test('formal save completes the private draft only after the actual request succeeds',async()=>{
  const m=await mount({embedded:true}),order=[];m.f.title='正式需求'
  m.draftRecovery.value={saveNow:async()=>true,complete:async()=>{assert.equal(m.calls.filter(call=>call.options?.method).length,1);order.push('complete')}}
  await m.save(true);assert.deepEqual(order,['complete']);assert.equal(m.events[0][0],'created');assert.equal(m.f.title,'');assert.equal(m.f.parentId,9);m.stop()
})
await test('failed formal saves retain the draft; failed cleanup never creates a duplicate item',async()=>{
  const failed=await mount({failSave:true});let cleanups=0;failed.f.title='保留';failed.peopleExpanded.value=false
  failed.draftRecovery.value={saveNow:async()=>true,complete:async()=>{cleanups++}}
  await failed.save();assert.equal(cleanups,0);assert.equal(failed.f.title,'保留');assert.equal(failed.dirty.value,true);assert.equal(failed.peopleExpanded.value,true);failed.stop()
  const m=await mount({embedded:true});m.f.title='成功';m.draftRecovery.value={saveNow:async()=>true,complete:async()=>{throw Error('cleanup')}}
  await m.save();assert.equal(m.calls.filter(call=>call.options?.method).length,1);assert.equal(m.events[0][0],'created');assert.equal(m.error.value,'');m.stop()
})
await test('manual draft writes guard closing and duplicate business submits while preserving edit identity',async()=>{
  const m=await mount({edit:true,embedded:true}),pending=deferred();m.f.title='当前修改'
  m.draftRecovery.value={saveNow:()=>pending.promise,complete:async()=>{}}
  const saving=m.savePrivateDraft();assert.equal(await m.requestClose(),false);await m.save();assert.equal(m.calls.filter(call=>call.options?.method).length,0)
  pending.resolve(true);await saving;assert.equal(m.f.title,'当前修改');const leaving=m.requestClose();m.finishLeave(true);assert.equal(await leaving,true);m.stop()
})
await test('standalone URL and embedded restore IDs are independent',async()=>{
  const normal=await mount();assert.equal(normal.requestedDraftId.value,'url-draft');normal.stop()
  const child=await mount({embedded:true});assert.equal(child.requestedDraftId.value,'child-draft');child.stop()
})
assert.match(source,/:target-id="edit\?String\(editingID\):''"/)
assert.match(source,/:busy="saving\|\|descriptionMediaBusy\|\|refinementBusy\|\|titleGenerating"/)
console.log(`Passed ${count} Editor private-draft tests`)

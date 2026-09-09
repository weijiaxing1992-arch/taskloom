import {workflow} from './workflow-test-support.mjs'
import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import ts from 'typescript'
import * as Vue from 'vue'

const read=path=>readFile(new URL('../'+path,import.meta.url),'utf8')
const transpile=source=>ts.transpileModule(source,{compilerOptions:{module:ts.ModuleKind.CommonJS,target:ts.ScriptTarget.ES2022}}).outputText
function evaluate(source,imports={}){const exports={};new Function('require','exports',transpile(source))(id=>imports[id],exports);return exports}
const mentions=evaluate(await read('src/mentions.ts')),fields=evaluate(await read('src/requirementFields.ts')),queries=evaluate(await read('src/workItemQuery.ts')),rich=evaluate(await read('src/richText.ts'),{'./mentions':mentions})
const members=[{id:'u_me',name:'产品同学',role:'product',active:true},{id:'u_dev',name:'研发同学',role:'frontend',active:true}]
const pendingDoc=()=>({type:'doc',content:[{type:'image',attrs:{attachmentId:null,name:'截图.png',alt:'',data:'aW1hZ2U='}}]})
const savedDoc=()=>({type:'doc',content:[{type:'image',attrs:{attachmentId:81,name:'截图.png',alt:''}}]})
const requirement=()=>({id:9,code:'REQ-9',title:'原需求',description:'原纯文本',descriptionDoc:null,descriptionMentionUserIds:[],descriptionMentionNames:{},remarks:'原备注',remarksMentionUserIds:[],remarksMentionNames:{},roleWeights:fields.emptyRoleWeights(),assigneeUserIds:[],assignees:[],ownerUserIds:['u_me'],owners:[{id:'u_me',name:'产品同学'}],category:'未分类',sprint:'待规划',status:'草稿',tags:'',tagColors:{},customFields:{}})
const exposed=['selected','session','members','descriptionDraft','descriptionEditing','detailDraft','assignmentDraft','ownerDraft','resetDescription','resetAssessment','resetAssignments','resetOwners','saveDescription','descriptionDirty','saveAndLeave','confirmDetailLeave','finishLeave','leavePrompt','leaveError','detailDirty','detailError','saving','comment','commentDoc','commentMentions','commentMentionNames','commentHasContent','addComment','descriptionMediaBusy','commentMediaBusy','resourceVersion','patchFields','selectDetailTab','comments']
async function mount(name,extra=[],{saved=requirement(),params={},query={},props={},mutation}={}){
  const calls=[],events=[],navigations=[],guards=[],unmounts=[],scope=Vue.effectScope(),exports={},route=Vue.reactive({params,query}),comments=[]
  const api=async(path,options)=>{
    calls.push({path,options})
    if(options?.method){const body=JSON.parse(options.body);if(mutation)return mutation(body,path,options)
      if(path.endsWith('/comments')){const created={id:7,body:body.contentDoc?rich.richTextPlain(body.contentDoc):body.body,contentDoc:body.contentDoc?savedDoc():null};comments.push(created);return created}
      saved={...saved,...body,id:9,...(body.descriptionDoc?{descriptionDoc:savedDoc(),description:'截图.png'}:{})};return structuredClone(saved)
    }
    if(path==='/session')return {user:{id:'u_me',role:'product'},project:{id:'prj_test'}}
    if(path==='/members')return {items:members}
    if(path==='/requirements/9')return structuredClone(saved)
    if(path.endsWith('/comments'))return {items:comments}
    if(path==='/requirements'||path.startsWith('/requirements?'))return {items:[structuredClone(saved)]}
    if(path==='/requirement-categories')return {items:[{id:1,name:'未分类',count:1}]}
    return {items:[]}
  }
  const imports={'../requirementWorkflow':workflow,vue:{...Vue,onMounted:()=>{},onBeforeUnmount:callback=>unmounts.push(callback)},'vue-router':{useRoute:()=>route,useRouter:()=>({push:async target=>{navigations.push(target)},replace:async target=>{navigations.push(target)}}),onBeforeRouteLeave:callback=>guards.push(callback),onBeforeRouteUpdate:()=>{}},'../api':{api},'../i18n':{t:value=>value,categoryLabel:value=>value,formatDate:value=>value},'../mentions':mentions,'../requirementFields':fields,'../workItemQuery':queries,'../richText':rich}
  const source=(await read('src/views/'+name+'.vue')).match(/<script setup[^>]*>([\s\S]*?)<\/script>/)[1]
  imports['../layoutScope']={useLayoutBoolean:(_key,fallback)=>Vue.ref(fallback)}
  const names=name==='Requirements'?[...exposed,...extra]:extra
  scope.run(()=>new Function('require','exports','window','defineProps','defineEmits','defineExpose',transpile(source+'\nexport {'+names.join(',')+'}'))(id=>id.endsWith('.vue')?{}:imports[id],exports,{addEventListener(){},removeEventListener(){},confirm:()=>false},()=>props,()=>(...event)=>events.push(event),()=>{}))
  if(name==='Requirements'){exports.selected.value=structuredClone(saved);exports.session.value={user:{id:'u_me',role:'product'}};exports.members.value=members;exports.resetDescription();exports.resetAssessment();exports.resetAssignments();exports.resetOwners()}
  return {...exports,calls,events,navigations,guards,stop:()=>{for(const unmount of unmounts)unmount();scope.stop()}}
}
let count=0
async function test(name,run){await run();count++;console.log('✓ '+name)}

await test('double-click editing focuses the existing editor without saving and respects permissions/busy state',async()=>{
  const m=await mount('Requirements',['beginDescriptionEdit','descriptionEditor']);let focuses=0
  m.descriptionEditor.value={focus(){focuses++}}
  await m.beginDescriptionEdit();assert.equal(m.descriptionEditing.value,true);assert.equal(focuses,1)
  assert.equal(m.calls.filter(call=>call.options?.method).length,0)
  m.descriptionEditing.value=false;m.saving.value=true;await m.beginDescriptionEdit();assert.equal(m.descriptionEditing.value,false)
  m.saving.value=false;m.session.value={user:{id:'u_read',role:'viewer'}};await m.beginDescriptionEdit();assert.equal(m.descriptionEditing.value,false)
  m.stop()
})

await test('old plain descriptions open cleanly and save without an invented rich document',async()=>{
  const m=await mount('Requirements');assert.equal(m.descriptionDirty.value,false);assert.equal(m.detailDirty.value,false);assert.equal(m.descriptionDraft.document,null)
  m.descriptionDraft.body='修改纯文本';await m.saveDescription()
  const body=JSON.parse(m.calls.find(call=>call.options?.method==='PATCH').options.body)
  assert.equal(Object.hasOwn(body,'descriptionDoc'),false);assert.equal(body.description,'修改纯文本');m.stop()
})
await test('rich description saves atomically and accepts canonical attachment IDs without resetting other drafts',async()=>{
  const m=await mount('Requirements');m.descriptionDraft.body='截图.png';m.descriptionDraft.document=pendingDoc();m.detailDraft.remarks='尚未保存的备注';m.assignmentDraft.value=['u_dev']
  await m.saveDescription();const body=JSON.parse(m.calls.find(call=>call.options?.method==='PATCH').options.body)
  assert.deepEqual(body.descriptionDoc,pendingDoc());assert.equal(Object.hasOwn(body,'remarks'),false);assert.equal(Object.hasOwn(body,'assigneeUserIds'),false)
  assert.deepEqual(m.descriptionDraft.document,savedDoc());assert.equal(m.descriptionDirty.value,false);assert.equal(m.detailDraft.remarks,'尚未保存的备注');assert.deepEqual([...m.assignmentDraft.value],['u_dev']);assert.equal(m.detailDirty.value,true);assert.equal(m.resourceVersion.value,1);m.stop()
})
await test('failed rich saves retain embedded binary drafts, mention metadata and the open editor',async()=>{
  const m=await mount('Requirements',[],{mutation:async()=>{throw Error('offline')}});m.descriptionEditing.value=true;m.descriptionDraft.body='截图.png';m.descriptionDraft.document=pendingDoc();m.descriptionDraft.mentionNames={u_dev:'研发同学'}
  await m.saveDescription();assert.equal(m.detailError.value,'offline');assert.equal(m.descriptionEditing.value,true);assert.deepEqual(m.descriptionDraft.document,pendingDoc());assert.equal(m.descriptionDraft.mentionNames.u_dev,'研发同学');assert.equal(m.descriptionDirty.value,true);m.stop()
})
await test('cancel/reset restores only the persisted description and never uploads draft images',async()=>{
  const m=await mount('Requirements',[],{saved:{...requirement(),description:'截图.png',descriptionDoc:savedDoc()}});m.descriptionDraft.document.content[0].attrs.name='未保存修改.png';m.detailDraft.remarks='保留备注';m.resetDescription()
  assert.deepEqual(m.descriptionDraft.document,savedDoc());assert.deepEqual(m.selected.value.descriptionDoc,savedDoc());assert.equal(m.detailDraft.remarks,'保留备注');assert.equal(m.calls.some(call=>call.options?.method),false);m.stop()
})
await test('image-only comment drafts block silent navigation and are never automatically posted',async()=>{
  const m=await mount('Requirements');m.comment.value='';m.commentDoc.value=pendingDoc();assert.equal(m.commentHasContent.value,true);assert.equal(m.detailDirty.value,true)
  const leave=m.confirmDetailLeave();await m.saveAndLeave();assert.equal(m.calls.some(call=>call.options?.method),false);assert.equal(m.leavePrompt.value,true)
  m.finishLeave(false);assert.equal(await leave,false);assert.deepEqual(m.commentDoc.value,pendingDoc());m.stop()
})
await test('image-only comments post contentDoc and clear only the comment after success',async()=>{
  const m=await mount('Requirements');m.commentDoc.value=pendingDoc();m.descriptionDraft.body='保留正文草稿'
  await m.addComment({body:'',mentionUserIds:[],contentDoc:m.commentDoc.value})
  const request=m.calls.find(call=>call.options?.method==='POST');assert.equal(request.path,'/requirements/9/comments');assert.deepEqual(JSON.parse(request.options.body).contentDoc,pendingDoc())
  assert.equal(m.commentDoc.value,null);assert.equal(m.comment.value,'');assert.equal(m.comments.value[0].contentDoc.content[0].attrs.attachmentId,81);assert.equal(m.descriptionDraft.body,'保留正文草稿');m.stop()
})
await test('comment failures preserve image drafts and mention identities for retry',async()=>{
  const m=await mount('Requirements',[],{mutation:async()=>{throw Error('offline')}});m.comment.value='截图.png';m.commentDoc.value=pendingDoc();m.commentMentions.value=['u_dev'];m.commentMentionNames.value={u_dev:'研发同学'}
  await m.addComment({body:m.comment.value,mentionUserIds:m.commentMentions.value,contentDoc:m.commentDoc.value})
  assert.deepEqual(m.commentDoc.value,pendingDoc());assert.deepEqual([...m.commentMentions.value],['u_dev']);assert.deepEqual(m.commentMentionNames.value,{u_dev:'研发同学'});assert.equal(m.comment.value,'截图.png');assert.equal(m.saving.value,false);m.stop()
})
await test('detail tab changes preserve description and image-comment drafts without hidden requests',async()=>{
  const m=await mount('Requirements');m.descriptionDraft.document=pendingDoc();m.commentDoc.value=pendingDoc();m.selectDetailTab('角色权重');m.selectDetailTab('详细信息')
  assert.deepEqual(m.descriptionDraft.document,pendingDoc());assert.deepEqual(m.commentDoc.value,pendingDoc());assert.equal(m.calls.length,0);m.stop()
})
await test('save-and-close persists rich description without implicitly posting a comment',async()=>{
  const m=await mount('Requirements');m.descriptionDraft.body='截图.png';m.descriptionDraft.document=pendingDoc();const leave=m.confirmDetailLeave();await m.saveAndLeave()
  assert.equal(await leave,true);assert.deepEqual(m.descriptionDraft.document,savedDoc());assert.equal(m.detailDirty.value,false)
  const requests=m.calls.filter(call=>call.options?.method);assert.equal(requests.length,1);assert.equal(requests[0].options.method,'PATCH');assert.deepEqual(JSON.parse(requests[0].options.body).descriptionDoc,pendingDoc());m.stop()
})
await test('all detail mutations are blocked while media is being read, not only description/comment buttons',async()=>{
  const m=await mount('Requirements');m.descriptionMediaBusy.value=true
  await m.patchFields({priority:'P1'});await m.saveDescription();await m.addComment({body:'评论',mentionUserIds:[]});assert.equal(m.confirmDetailLeave(),false)
  assert.equal(m.calls.some(call=>call.options?.method),false,'a side-panel PATCH would toggle saving and cancel the active file read');m.stop()
})
await test('full Editor sends pending rich content, consumes canonical IDs and blocks closing during reads',async()=>{
const m=await mount('Editor',['load','f','save','formElement','descriptionMediaBusy','requestClose','dirty'],{props:{embedded:true,parentId:9}});await m.load();m.formElement.value={checkValidity:()=>true,reportValidity:()=>true};m.f.title='子需求';m.f.description='截图.png';m.f.descriptionDoc=pendingDoc()
  m.descriptionMediaBusy.value=true;await m.save();assert.equal(await m.requestClose(),false);assert.equal(m.guards[0](),false);assert.equal(m.calls.some(call=>call.options?.method),false)
  m.descriptionMediaBusy.value=false;await m.save();assert.deepEqual(m.f.descriptionDoc,savedDoc());assert.equal(m.dirty.value,false);assert.deepEqual(m.events[0][1].descriptionDoc,savedDoc());assert.equal(m.events[0][0],'created');assert.deepEqual(m.navigations,[]);m.stop()
})
console.log(`Passed ${count} rich-text integration tests.`)

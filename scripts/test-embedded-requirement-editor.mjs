import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import ts from 'typescript'
import * as Vue from 'vue'
import { parse, compileScript, compileTemplate } from 'vue/compiler-sfc'

const read = path => readFile(new URL('../' + path, import.meta.url), 'utf8')
async function pure(path) {
  const result = ts.transpileModule(await read(path), {compilerOptions:{module:ts.ModuleKind.ESNext,target:ts.ScriptTarget.ES2022}})
  return import('data:text/javascript;base64,' + Buffer.from(result.outputText).toString('base64'))
}
const fields = await pure('src/requirementFields.ts'), mentions = await pure('src/mentions.ts')
const source = await read('src/views/Editor.vue')
const script = source.match(/<script setup[^>]*>([\s\S]*?)<\/script>/)[1]
const names = ['load','f','save','dirty','loading','saving','initialized','canEdit','formElement','notice','error','edit','parentContext','requestClose','applyRequirementTemplate','finishLeave','saveDraftAndLeave','draftRecovery','leaveError']
const output = ts.transpileModule(script + '\nexport {' + names.join(',') + '}', {compilerOptions:{module:ts.ModuleKind.CommonJS,target:ts.ScriptTarget.ES2022}}).outputText
const people = [{id:'u_me',name:'同名',email:'me@example.test',role:'product',projectRole:'product',active:true,isCurrent:true},{id:'u_peer',name:'同名',email:'peer@example.test',role:'product',projectRole:'product',active:true}]
const parent = id => ({id,code:'REQ-' + id,title:'原始父需求 ' + id,category:id === 9 ? '客户端' : '算法平台',sprint:id === 9 ? '123' : '22'})
const deferred = () => {let resolve,reject;const promise=new Promise((yes,no)=>{resolve=yes;reject=no});return {promise,resolve,reject}}
const flush = async () => {for(let index=0;index<12;index++)await Vue.nextTick()}

async function mount({props={embedded:true,parentId:9},params={},query={},override=()=>undefined}={}) {
  const reactiveProps=Vue.reactive(props),route=Vue.reactive({params,query}),events=[],calls=[],navigations=[],guards=[],updates=[],unmounts=[]
  const scope=Vue.effectScope(),exports={},context={confirm:false,confirmCount:0,public:null}
  const api=async(path,options)=>{
    calls.push({path,options})
    const overridden=override(path,options)
    if(overridden !== undefined)return overridden
    if(options?.method)return {id:901,code:'REQ-901',...JSON.parse(options.body)}
    if(path==='/session')return {user:{id:'u_me',name:'同名',role:'product'},project:{id:'prj_test'}}
    if(path==='/sprints')return {items:[{id:1,name:'123',status:'进行中'},{id:2,name:'22',status:'规划中'}]}
    if(path==='/members')return {items:people}
    if(path==='/requirement-categories')return {items:[{id:1,name:'客户端'},{id:2,name:'算法平台'}]}
    if(/^\/requirements\/\d+$/.test(path))return parent(Number(path.split('/').at(-1)))
    return {items:[]}
  }
  const imports={'../layoutScope':{useLayoutBoolean:(_key,fallback)=>Vue.ref(fallback)},
    vue:{...Vue,onMounted:()=>{},onBeforeUnmount:callback=>unmounts.push(callback)},
    'vue-router':{useRoute:()=>route,useRouter:()=>({push:async target=>{navigations.push(target)}}),onBeforeRouteLeave:callback=>guards.push(callback),onBeforeRouteUpdate:callback=>updates.push(callback)},
    '../api':{api},'../i18n':{t:value=>value,categoryLabel:value=>value,formatDate:value=>value},'../requirementFields':fields,'../mentions':mentions,
  }
  const window={addEventListener:()=>{},removeEventListener:()=>{},confirm:()=>{context.confirmCount++;return context.confirm}}
  scope.run(()=>new Function('require','exports','window','defineProps','defineEmits','defineExpose',output)(id=>id.endsWith('.vue')?{}:imports[id],exports,window,()=>reactiveProps,()=>(...args)=>events.push(args),value=>{context.public=value}))
  exports.formElement.value={checkValidity:()=>true,reportValidity:()=>true}
  return {...exports,props:reactiveProps,route,events,calls,navigations,guards,updates,context,stop:()=>{for(const callback of unmounts)callback();scope.stop()}}
}
let count=0
async function test(name,run){await run();count++;console.log('✓ ' + name)}

await test('template estimates and product owners fill blanks without overwriting existing estimates',async()=>{
 const m=await mount();await m.load();m.f.roleWeights.product.value=20
 m.applyRequirementTemplate({ownerUserIds:['u_me'],roleWeights:{product:{userIds:['u_me'],value:50},frontend:{userIds:['u_me'],value:0}}},false)
 assert.equal(m.f.roleWeights.product.value,20);assert.equal(m.f.roleWeights.frontend.value,0);assert.deepEqual([...m.f.roleWeights.frontend.userIds],[]);assert.deepEqual([...m.f.ownerUserIds],['u_me'])
 m.applyRequirementTemplate({roleWeights:{product:{value:50}}},true);assert.equal(m.f.roleWeights.product.value,50);m.stop()
})
await test('personal template fills only blank content and preserves identity, title and workflow',async()=>{
 const m=await mount();await m.load();Object.assign(m.f,{title:'已创建标题',description:'已有正文',acceptance:'',remarks:'旧备注',ownerUserIds:['u_me'],sprint:'123'})
 const calls=m.calls.length;m.applyRequirementTemplate({description:'模板正文',descriptionDoc:{type:'doc',content:[]},acceptance:'新标准',remarks:'模板备注'},false)
 assert.equal(m.f.description,'已有正文');assert.equal(m.f.acceptance,'新标准');assert.equal(m.f.remarks,'旧备注');assert.equal(m.f.title,'已创建标题');assert.deepEqual([...m.f.ownerUserIds],['u_me']);assert.equal(m.f.sprint,'123');assert.equal(m.calls.length,calls);assert(m.dirty.value);m.stop()
})
await test('explicit template replacement resets mention IDs without replacing business associations',async()=>{
 const m=await mount();await m.load();Object.assign(m.f,{description:'@同名',descriptionMentionUserIds:['u_peer'],remarks:'@同名',remarksMentionUserIds:['u_peer']})
 m.applyRequirementTemplate({description:'模板正文',descriptionDoc:{type:'doc',content:[{type:'paragraph',content:[{type:'text',text:'模板正文'}]}]},remarks:'模板备注'},true)
 assert.equal(m.f.description,'模板正文');assert.deepEqual([...m.f.descriptionMentionUserIds],[]);assert.deepEqual([...m.f.remarksMentionUserIds],[]);assert.equal(m.f.parentId,9);m.stop()
})
await test('template application cannot mutate a saving or read-only editor',async()=>{
 const m=await mount();await m.load();m.saving.value=true;m.applyRequirementTemplate({description:'禁止'},true);assert.equal(m.f.description,'');m.saving.value=false;m.canEdit.value=false;m.applyRequirementTemplate({description:'禁止'},true);assert.equal(m.f.description,'');m.stop()
})

await test('embedded Editor ignores underlying edit routes and inherits the fixed parent context',async()=>{
  const m=await mount({params:{id:'666'},query:{parentId:'777',sprint:'22'}})
  await m.load()
  assert.equal(m.edit.value,false);assert.equal(m.f.parentId,9);assert.equal(m.parentContext.value.title,'原始父需求 9')
  assert.equal(m.f.category,'客户端');assert.equal(m.f.sprint,'123');assert.deepEqual([...m.f.ownerUserIds],[])
  assert.equal(m.calls.some(call=>['/requirements/666','/requirements/777'].includes(call.path)),false)
  m.f.title='未保存子需求';const calls=m.calls.length
  m.route.params.id='778';m.route.query.parentId='779';m.route.query.sprint='22';await flush()
  assert.equal(m.calls.length,calls);assert.equal(m.f.title,'未保存子需求');assert.equal(m.dirty.value,true);m.stop()
})

await test('embedded creation emits the server object, retains identity payloads and never navigates',async()=>{
  const m=await mount();await m.load()
  Object.assign(m.f,{title:'子需求标题',description:'正文\n@同名 协作',descriptionMentionUserIds:['u_peer'],descriptionMentionNames:{u_peer:'同名'},remarks:'备注 @同名 评审',remarksMentionUserIds:['u_me'],remarksMentionNames:{u_me:'同名'},ownerUserIds:['u_peer','u_me'],assigneeUserIds:['u_me','u_peer'],parentId:999})
  m.f.roleWeights.frontend={userId:'u_me',userIds:['u_me','u_peer'],value:200}
  await m.save()
  const request=m.calls.find(call=>call.options?.method==='POST'),body=JSON.parse(request.options.body)
  assert.equal(body.parentId,9);assert.equal(body.description,'正文\n@同名 协作');assert.deepEqual(body.descriptionMentionUserIds,['u_peer'])
  assert.deepEqual(body.remarksMentionUserIds,['u_me']);assert.deepEqual(body.ownerUserIds,['u_peer','u_me']);assert.deepEqual(body.assigneeUserIds,['u_me','u_peer'])
  assert.deepEqual(body.roleWeights.frontend.userIds,['u_me','u_peer']);assert.equal(body.roleWeights.frontend.value,200)
  assert.equal(request.options.headers['X-DevFlow-Project'],'prj_test');assert.equal(m.events.length,1)
  assert.equal(m.events[0][0],'created');assert.equal(m.events[0][1].id,901);assert.equal(m.events[0][2],false)
  assert.equal(m.dirty.value,false);assert.equal(m.saving.value,false);assert.deepEqual(m.navigations,[]);m.stop()
})
await test('new child requirements never auto-fill a product owner',async()=>{
  const m=await mount({override:path=>path==='/members'?{items:people.map(person=>({...person,role:'frontend',projectRole:'frontend'}))}:path==='/session'?{user:{id:'u_me',name:'同名',role:'frontend'},project:{id:'prj_test'}}:undefined});await m.load();assert.deepEqual([...m.f.ownerUserIds],[])
  m.f.title='前端发起子需求';await m.save(true);assert.deepEqual([...m.f.ownerUserIds],[]);assert.equal(m.f.parentId,9);assert.equal(m.events[0][2],true);m.stop()
})

await test('continue-creating emits each child and resets only its form while retaining inherited context',async()=>{
  const m=await mount();await m.load();m.f.title='第一条';m.f.assigneeUserIds=['u_peer'];m.f.roleWeights.ui.value=500
  await m.save(true)
  assert.equal(m.events[0][2],true);assert.equal(m.f.title,'');assert.equal(m.f.parentId,9);assert.equal(m.f.category,'客户端');assert.equal(m.f.sprint,'123')
  assert.deepEqual([...m.f.ownerUserIds],[]);assert.deepEqual([...m.f.assigneeUserIds],[]);assert.equal(m.f.roleWeights.ui.value,null)
  assert.equal(m.dirty.value,false);assert.match(m.notice.value,/继续/)
  m.f.title='第二条';await m.save(true)
  assert.equal(m.events.length,2);assert.equal(m.events[1][1].title,'第二条');assert.deepEqual(m.navigations,[]);m.stop()
})

await test('failed child creation preserves drafts and identities without emitting or navigating',async()=>{
  const m=await mount({override:(_path,options)=>options?.method?Promise.reject(Error('offline')):undefined});await m.load()
  Object.assign(m.f,{title:'保留',description:'@同名 未保存',descriptionMentionUserIds:['u_peer'],descriptionMentionNames:{u_peer:'同名'},remarks:'草稿备注',assigneeUserIds:['u_peer'],ownerUserIds:['u_me','u_peer']})
  m.f.roleWeights.algorithm.value=800;await m.save()
  assert.equal(m.error.value,'offline');assert.equal(m.f.description,'@同名 未保存');assert.deepEqual([...m.f.descriptionMentionUserIds],['u_peer'])
  assert.deepEqual([...m.f.ownerUserIds],['u_me','u_peer']);assert.equal(m.f.roleWeights.algorithm.value,800)
  assert.equal(m.dirty.value,true);assert.equal(m.saving.value,false);assert.deepEqual(m.events,[]);assert.deepEqual(m.navigations,[]);m.stop()
})

await test('public close confirms unsaved content; cancelled route guards never emit or clear it',async()=>{
  const m=await mount();await m.load();m.f.title='未保存'
  assert.equal(m.context.public.requestClose,m.requestClose);assert.equal(m.context.public.dirty,m.dirty)
  const close=m.requestClose();m.finishLeave(false);assert.equal(await close,false)
  const guard=m.guards[0]();m.finishLeave(false);assert.equal(await guard,false)
  const update=m.updates[0]();m.finishLeave(false);assert.equal(await update,false)
  assert.equal(m.f.title,'未保存');assert.deepEqual(m.events,[])
  const allowed=m.guards[0]();m.finishLeave(true);assert.equal(await allowed,true);assert.deepEqual(m.events,[]);assert.equal(m.f.title,'未保存')
  const accepted=m.requestClose();m.finishLeave(true);assert.equal(await accepted,true);assert.deepEqual(m.events,[['cancel']]);assert.deepEqual(m.navigations,[]);assert.equal(m.context.confirmCount,0);m.stop()
})

await test('save draft and leave only resolves after durable local success; failure keeps content',async()=>{
 const m=await mount();await m.load();m.f.title='重要内容';const closing=m.requestClose()
 m.draftRecovery.value={saveNow:async()=>false};await m.saveDraftAndLeave();assert(m.leaveError.value);assert.deepEqual(m.events,[])
 m.draftRecovery.value={saveNow:async()=>true};await m.saveDraftAndLeave();assert.equal(await closing,true);assert.equal(m.f.title,'重要内容');assert.deepEqual(m.events,[['cancel']]);m.stop()
})

await test('saving blocks close, route departure and duplicate submission until the request completes',async()=>{
  const pending=deferred(),m=await mount({override:(_path,options)=>options?.method?pending.promise:undefined});await m.load();m.f.title='提交中'
  const saving=m.save();assert.equal(m.saving.value,true);m.context.confirm=true
  assert.equal(await m.requestClose(),false);assert.equal(m.guards[0](),false);assert.equal(m.updates[0](),false)
  await m.save();assert.equal(m.calls.filter(call=>call.options?.method).length,1);assert.equal(m.context.confirmCount,0)
  pending.resolve({id:910,title:'提交中'});await saving;assert.equal(m.saving.value,false);assert.equal(m.events.length,1);m.stop()
})

await test('a late parent response cannot overwrite a newly selected parent and its form',async()=>{
  const old=deferred(),m=await mount({override:path=>path==='/requirements/9'?old.promise:undefined})
  const first=m.load();await flush();m.props.parentId=10;await flush()
  assert.equal(m.initialized.value,true);assert.equal(m.f.parentId,10);assert.equal(m.f.category,'算法平台')
  m.f.title='新父需求下的草稿';old.resolve(parent(9));await first
  assert.equal(m.f.parentId,10);assert.equal(m.parentContext.value.id,10);assert.equal(m.f.title,'新父需求下的草稿');m.stop()
})

await test('a late create result remains scoped to its original parent and cannot emit into a new one',async()=>{
  const old=deferred(),m=await mount({override:(_path,options)=>options?.method?old.promise:undefined});await m.load();m.f.title='原父的子需求'
  const saving=m.save();m.props.parentId=10;await flush()
  assert.equal(m.f.parentId,10);m.f.title='新父草稿';old.resolve({id:922,parentId:9,title:'原父的子需求'});await saving
  assert.equal(m.f.parentId,10);assert.equal(m.f.title,'新父草稿');assert.equal(m.dirty.value,true);assert.equal(m.saving.value,false)
  assert.deepEqual(m.events,[]);assert.deepEqual(m.navigations,[]);m.stop()
})

await test('unmounting prevents a late successful create from emitting or redirecting',async()=>{
  const pending=deferred(),m=await mount({override:(_path,options)=>options?.method?pending.promise:undefined});await m.load();m.f.title='原抽屉'
  const saving=m.save();m.stop();pending.resolve({id:933,title:'原抽屉'});await saving
  assert.deepEqual(m.events,[]);assert.deepEqual(m.navigations,[])
})

await test('invalid or inaccessible embedded parents block creation instead of falling back to a root requirement',async()=>{
  const invalid=await mount({props:{embedded:true,parentId:0}});await invalid.load();invalid.f.title='不应提交';await invalid.save()
  assert.equal(invalid.initialized.value,false);assert.match(invalid.error.value,/父需求无效/);assert.equal(invalid.calls.some(call=>call.options?.method),false);invalid.stop()
  const denied=await mount({override:path=>path==='/requirements/9'?Promise.reject(Error('Not found in project')):undefined});await denied.load();denied.f.title='不应提交';await denied.save()
  assert.equal(denied.initialized.value,false);assert.match(denied.error.value,/Not found/);assert.equal(denied.calls.some(call=>call.options?.method),false);denied.stop()
})

await test('ended parent iteration falls back to backlog and read-only accounts cannot create',async()=>{
  const ended=await mount({override:path=>path==='/requirements/9'?{...parent(9),sprint:'已结束'}:undefined});await ended.load()
  assert.equal(ended.f.sprint,'待规划');assert.match(ended.notice.value,/待规划/);ended.stop()
  const viewer=await mount({override:path=>path==='/session'?{user:{id:'u_me',role:'viewer'}}:undefined});await viewer.load();viewer.f.title='只读';await viewer.save()
  assert.equal(viewer.canEdit.value,false);assert.match(viewer.error.value,/只读/);assert.equal(viewer.calls.some(call=>call.options?.method),false);viewer.stop()
})

await test('standalone editing retains PATCH and navigation behavior with no embedded events',async()=>{
  const m=await mount({props:{},params:{id:'9'}});await m.load();m.f.title='编辑现有需求';await m.save()
  const request=m.calls.find(call=>call.options?.method);assert.equal(m.edit.value,true);assert.equal(request.path,'/requirements/9');assert.equal(request.options.method,'PATCH')
  assert.deepEqual(m.navigations,['/requirements']);assert.deepEqual(m.events,[]);m.stop()
})

await test('embedded template compiles with a fixed parent, larger body and non-submitting cancel controls',()=>{
  const {descriptor}=parse(source),compiled=compileScript(descriptor,{id:'embedded-editor-test'})
  const template=compileTemplate({source:descriptor.template.content,filename:'Editor.vue',id:'embedded-editor-test',compilerOptions:{bindingMetadata:compiled.bindings}})
  assert.deepEqual(template.errors,[])
  assert.match(source,/id="requirement-parent"[^>]*:disabled="props\.embedded \|\| saving/)
  assert.match(source,/<RichTextEditor :key="descriptionDraftRevision" input-id="requirement-description"[^>]+v-model:document="f.descriptionDoc"/)
  assert.match(source,/<fieldset class="custom-field-lock" :disabled="saving">/)
  assert.match(source,/<button v-if="props\.embedded" type="button"[^>]*@click="requestClose"/)
})

console.log(`Passed ${count} embedded requirement editor tests.`)

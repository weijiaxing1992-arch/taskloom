import {workflow} from './workflow-test-support.mjs'
import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import ts from 'typescript'
import * as Vue from 'vue'
import { compileScript, parse } from 'vue/compiler-sfc'

const read = path => readFile(new URL('../' + path, import.meta.url), 'utf8')
async function pure(path) {
  const output = ts.transpileModule(await read(path), {compilerOptions:{module:ts.ModuleKind.ESNext,target:ts.ScriptTarget.ES2022}}).outputText
  return import('data:text/javascript;base64,' + Buffer.from(output).toString('base64'))
}
const fields = await pure('src/requirementFields.ts'), mentions = await pure('src/mentions.ts'), workQueries = await pure('src/workItemQuery.ts')
const english = (await pure('src/locales/requirements.en.ts')).default
const locale = Vue.ref('zh-CN')
const i18n = { t: (source, params = {}) => (locale.value === 'en-US' ? english[source] || source : source).replace(/\{(\w+)\}/g, (token,key) => Object.hasOwn(params,key) ? String(params[key]) : token), categoryLabel: value => value, formatDate: value => value || '' }
const members = [{id:'u_a',name:'同名',email:'a@example.test',department:'产品研发',departmentIds:['dept_product'],departmentNames:['产品研发'],projectRole:'frontend',active:true,isCurrent:true},{id:'u_b',name:'同名',email:'b@example.test',department:'产品研发',departmentIds:['dept_product'],departmentNames:['产品研发'],projectRole:'frontend',active:true},{id:'u_old',name:'历史成员',department:'产品研发',departmentIds:['dept_product'],departmentNames:['产品研发'],active:false}]
const requirement = () => ({id:9,title:'Original user content',code:'REQ-0009',description:'@同名 原正文',descriptionMentionUserIds:['u_a'],descriptionMentionNames:{u_a:'同名'},remarks:'@历史旧名 旧备注',remarksMentionUserIds:['u_old'],remarksMentionNames:{u_old:'历史旧名'},assignee:'历史成员',assigneeUserIds:['u_old'],assignees:[{id:'u_old',name:'历史成员'}],owner:'历史成员',ownerUserIds:['u_old'],owners:[{id:'u_old',name:'历史成员'}],roleWeights:fields.emptyRoleWeights(),tags:'',tagColors:{},customFields:{},category:'未分类',sprint:'待规划'})

async function view(name, exposed, handler = async() => ({items:[]}), params = {}, query = {}) {
  const source = (await read('src/views/' + name + '.vue')).match(/<script setup[^>]*>([\s\S]*?)<\/script>/)[1]
  const calls = [], mounts = [], unmounts = [], guards = [], route = Vue.reactive({params,query}), navigations = []
  const scope = Vue.effectScope(), exports = {}
  const imports = {'../requirementWorkflow':workflow,
    vue:{...Vue,onMounted:fn => mounts.push(fn),onBeforeUnmount:fn => unmounts.push(fn)},
    'vue-router':{useRoute:() => route,useRouter:() => ({push:async path => {navigations.push(path)},replace:async path => {navigations.push(path)}}),onBeforeRouteLeave:fn => guards.push(fn),onBeforeRouteUpdate:() => {}},
    '../api':{api:async(path,options) => {calls.push({path,options});return handler(path,options)}},
    '../layoutScope':{useLayoutBoolean:(_key,fallback)=>Vue.ref(fallback)},
    '../i18n':i18n,'../mentions':mentions,'../requirementFields':fields,'../workItemQuery':workQueries,
  }
  const output = ts.transpileModule(source + '\nexport {' + exposed.join(',') + '}', {compilerOptions:{module:ts.ModuleKind.CommonJS,target:ts.ScriptTarget.ES2022}}).outputText
  scope.run(() => new Function('require','exports','window','confirm','defineProps','defineEmits','defineExpose',output)(id => id.endsWith('.vue') ? {} : imports[id],exports,{addEventListener:() => {},removeEventListener:() => {},confirm:() => false},() => false,() => ({}),() => () => {},() => {}))
  return {...exports,calls,route,navigations,guards,stop:() => {for(const fn of unmounts)fn();scope.stop()}}
}
function fixtureHandler(saved, mutation = async body => body) {
  return async(path,options) => {
    if(options?.method) return mutation(JSON.parse(options.body),path,options)
    if(path === '/session') return {user:{id:'u_a',role:'product',name:'同名'}}
    if(path === '/members') return {items:members}
    if(path === '/requirements/9') return structuredClone(saved)
    if(path === '/requirement-categories') return {items:[{id:1,name:'未分类'}]}
    return {items:[]}
  }
}
let count = 0
async function test(name,run) {await run();count++;console.log('✓ ' + name)}

await test('Editor private draft save preserves separate mention IDs and never creates a requirement',async() => {
  const savedDrafts=[]
  const m = await view('Editor',['load','f','save','formElement','draftRecovery','draftPayload','draftSaving','initialized','loading','canEdit'],fixtureHandler({},async() => {throw Error('private drafts must not call the requirement API')}))
await m.load();m.formElement.value={checkValidity:() => true,reportValidity:() => true}
  assert.equal(m.initialized.value,true);assert.equal(m.loading.value,false);assert.equal(m.canEdit.value,true)
  Object.assign(m.f,{title:'Draft title',description:'正文\n@同名 处理',descriptionMentionUserIds:['u_b'],descriptionMentionNames:{u_b:'同名'},remarks:'备注 @同名 协作',remarksMentionUserIds:['u_a'],remarksMentionNames:{u_a:'同名'},assigneeUserIds:['u_a','u_b']})
  m.draftRecovery.value={saveNow:async()=>{savedDrafts.push(JSON.parse(JSON.stringify(m.draftPayload.value)));return true},complete:async()=>true}
  await m.save(false,true)
  assert.equal(savedDrafts.length,1)
  const saved=savedDrafts[0]
  assert.equal(saved.title,'Draft title');assert.equal(saved.description,'正文\n@同名 处理')
  assert.deepEqual(saved.descriptionMentionUserIds,['u_b']);assert.deepEqual(saved.remarksMentionUserIds,['u_a']);assert.deepEqual(saved.assigneeUserIds,['u_a','u_b'])
  assert.deepEqual(saved.descriptionMentionNames,{u_b:'同名'});assert.deepEqual(saved.remarksMentionNames,{u_a:'同名'})
  assert.equal(Object.hasOwn(saved,'status'),false);assert.equal(m.calls.some(call=>call.options?.method),false);assert.equal(m.draftSaving.value,false)
  m.stop()
})

await test('Editor unrelated edits omit unchanged historical assignees and retain unavailable saved mentions',async() => {
  const m = await view('Editor',['load','f','save','formElement'],fixtureHandler(requirement()),{id:'9'})
  await m.load();m.formElement.value={checkValidity:() => true,reportValidity:() => true};m.f.title='Changed title'
  await m.save()
  const body=JSON.parse(m.calls.find(call => call.options?.method==='PATCH').options.body)
  assert.equal(Object.hasOwn(body,'assignee'),false);assert.equal(Object.hasOwn(body,'assigneeUserIds'),false);assert.equal(Object.hasOwn(body,'owner'),false);assert.equal(Object.hasOwn(body,'ownerUserIds'),false)
  assert.deepEqual(body.remarksMentionUserIds,['u_old']);assert.equal(body.remarks,'@历史旧名 旧备注');m.stop()
})

await test('Editor failed save preserves body, mention identities, selected assignees and dirty state',async() => {
  const m = await view('Editor',['load','f','save','formElement','dirty','error','saving'],fixtureHandler(requirement(),async() => {throw Error('save failed')}),{id:'9'})
  await m.load();m.formElement.value={checkValidity:() => true,reportValidity:() => true};m.f.description='@同名 待保存';m.f.descriptionMentionUserIds=['u_b'];m.f.descriptionMentionNames={u_b:'同名'};m.f.assigneeUserIds=['u_a','u_b']
  await m.save();assert.equal(m.error.value,'save failed');assert.equal(m.saving.value,false);assert.equal(m.dirty.value,true)
  assert.equal(m.f.description,'@同名 待保存');assert.deepEqual([...m.f.descriptionMentionUserIds],['u_b']);assert.deepEqual([...m.f.assigneeUserIds],['u_a','u_b']);assert.equal(m.navigations.length,0);m.stop()
})

await test('Editor does not submit newly unavailable mention recipients',async() => {
  const m = await view('Editor',['load','f','save','formElement','error'],fixtureHandler(requirement()),{id:'9'})
  await m.load();m.formElement.value={checkValidity:() => true,reportValidity:() => true};m.f.description='@失效成员 处理';m.f.descriptionMentionUserIds=['gone'];m.f.descriptionMentionNames={gone:'失效成员'}
  await m.save();assert.match(m.error.value,/新增提及/);assert.equal(m.calls.some(call=>call.options?.method),false);m.stop()
})

const detailExposed=['selected','descriptionDraft','descriptionEditing','detailDraft','assignmentDraft','ownerDraft','resetDescription','resetAssessment','resetAssignments','resetOwners','saveDescription','saveAssessment','saveAssignments','saveOwners','detailDirty','detailError','members','saving','close','confirmDetailLeave','leavePrompt','finishLeave','saveAndLeave','leaveError','comment','session','createChild','childParent','resourceDraft','resourceDirty']
async function detail(mutation,query={}) {
  let saved=requirement()
  const m=await view('Requirements',detailExposed,fixtureHandler(saved,async(body,path,options)=>{if(mutation)return mutation(body,path,options);saved={...saved,...body};return structuredClone(saved)}),{},query)
  m.selected.value=structuredClone(saved);m.members.value=members;m.session.value={user:{id:'u_a',role:'product'}};m.resetDescription();m.resetAssessment();m.resetAssignments();m.resetOwners()
  return m
}
await test('detail description save is scoped and does not overwrite unsaved remarks or assignees',async()=>{
  const m=await detail();m.descriptionEditing.value=true;m.descriptionDraft.body='@同名 新正文';m.descriptionDraft.mentionUserIds=['u_b'];m.descriptionDraft.mentionNames={u_b:'同名'}
  m.detailDraft.remarks='@同名 未保存备注';m.detailDraft.remarksMentionUserIds=['u_a'];m.detailDraft.remarksMentionNames={u_a:'同名'};m.assignmentDraft.value=['u_b']
  await m.saveDescription()
  const body=JSON.parse(m.calls.find(call=>call.options?.method==='PATCH').options.body)
  assert.deepEqual(body,{description:'@同名 新正文',descriptionMentionUserIds:['u_b']})
  assert.equal(m.detailDraft.remarks,'@同名 未保存备注');assert.deepEqual([...m.assignmentDraft.value],['u_b']);assert.equal(m.descriptionEditing.value,false);assert.equal(m.detailDirty.value,true);m.stop()
})
await test('detail remarks save carries its IDs without sending description or read-only names',async()=>{
  const m=await detail();m.detailDraft.remarks='@同名 新备注';m.detailDraft.remarksMentionUserIds=['u_b'];m.detailDraft.remarksMentionNames={u_b:'同名'}
  m.descriptionDraft.body='未保存正文';await m.saveAssessment()
  const body=JSON.parse(m.calls.find(call=>call.options?.method==='PATCH').options.body)
  assert.equal(body.remarks,'@同名 新备注');assert.deepEqual(body.remarksMentionUserIds,['u_b']);assert.equal(Object.hasOwn(body,'description'),false);assert.equal(Object.hasOwn(body,'remarksMentionNames'),false)
  assert.equal(m.descriptionDraft.body,'未保存正文');m.stop()
})
await test('detail failed saves retain mention identities and selected multiple assignees',async()=>{
  const m=await detail(async()=>{throw Error('offline')});m.descriptionDraft.body='@同名 草稿';m.descriptionDraft.mentionUserIds=['u_b'];m.descriptionDraft.mentionNames={u_b:'同名'};m.descriptionEditing.value=true
  await m.saveDescription();assert.equal(m.descriptionDraft.body,'@同名 草稿');assert.deepEqual([...m.descriptionDraft.mentionUserIds],['u_b']);assert.equal(m.descriptionEditing.value,true)
  m.assignmentDraft.value=['u_a','u_b'];await m.saveAssignments();assert.deepEqual([...m.assignmentDraft.value],['u_a','u_b']);assert.equal(m.detailError.value,'offline');assert.equal(m.detailDirty.value,true);m.stop()
})
await test('detail assignment save sends stable IDs only and preserves other field drafts',async()=>{
  const m=await detail();m.assignmentDraft.value=['u_a','u_b'];m.descriptionDraft.body='Keep draft';await m.saveAssignments()
  const body=JSON.parse(m.calls.find(call=>call.options?.method==='PATCH').options.body)
  assert.deepEqual(body,{assigneeUserIds:['u_a','u_b']});assert.equal(m.descriptionDraft.body,'Keep draft');m.stop()
})
await test('a completed assessment save releases saving and permits closing without a stale dirty confirmation',async()=>{
  const m=await detail(undefined,{req:'9'});m.detailDraft.roleWeights.frontend={userId:'u_a',value:2.5}
  await m.saveAssessment();assert.equal(m.saving.value,false);assert.equal(m.detailDirty.value,false);assert.equal(m.confirmDetailLeave(),true)
  m.close();assert.deepEqual(m.navigations.at(-1),{query:{}});m.stop()
})

const filterExposed=['filters','session','members','setView','activeView','load','loadOptions','clearFilters','assigneeFilterValue','assigneeOptionLabel','selectedHistoricalAssignee']
await test('requirement mine and assignee filters use exact account IDs, including same-name co-assignees',async()=>{
  const m=await view('Requirements',filterExposed);m.members.value=members;m.session.value={user:{id:'u_b',name:'同名'}}
  assert.equal(m.activeView.value,'all');m.setView('mine');assert.equal(m.filters.mine,'1');assert.equal(m.filters.assigneeUserId,'');assert.equal(m.filters.assignee,'');assert.equal(m.activeView.value,'mine')
  await m.load();let query=new URL('https://example.test'+m.calls.at(-1).path).searchParams;assert.equal(query.get('mine'),'1');assert.equal(query.get('assigneeUserId'),'');assert.equal(query.get('assignee'),'')
  m.assigneeFilterValue.value='u_a';await m.load();query=new URL('https://example.test'+m.calls.at(-1).path).searchParams
  assert.equal(query.get('assigneeUserId'),'u_a');assert.equal(query.get('mine'),'1');assert.equal(m.activeView.value,'mine');assert.equal(m.assigneeOptionLabel(members[0]),'同名 · a@example.test');assert.equal(m.assigneeOptionLabel(members[1]),'同名 · b@example.test')
  m.clearFilters();assert.equal(m.filters.assigneeUserId,'');m.stop()
})
await test('stable filter links preserve inactive IDs and take priority over a legacy name parameter',async()=>{
  const m=await view('Requirements',filterExposed,fixtureHandler({}),{},{assigneeUserId:'u_old',assignee:'同名'})
  await m.loadOptions();assert.equal(m.filters.assigneeUserId,'u_old');assert.equal(m.filters.assignee,'');assert.equal(m.selectedHistoricalAssignee.value.name,'历史成员')
  m.stop()
})
await test('legacy name links upgrade only unambiguous accounts and never choose between duplicate names',async()=>{
  const unique=await view('Requirements',filterExposed,fixtureHandler({}),{},{assignee:'历史成员'});await unique.loadOptions()
  assert.equal(unique.filters.assigneeUserId,'u_old');assert.equal(unique.filters.assignee,'');unique.stop()
  const ambiguous=await view('Requirements',filterExposed,fixtureHandler({}),{},{assignee:'同名'});await ambiguous.loadOptions()
  assert.equal(ambiguous.filters.assigneeUserId,'');assert.equal(ambiguous.filters.assignee,'同名');assert.equal(ambiguous.assigneeFilterValue.value,'__legacy_assignee__')
  ambiguous.assigneeFilterValue.value='u_b';assert.equal(ambiguous.filters.assignee,'');assert.equal(ambiguous.filters.assigneeUserId,'u_b');ambiguous.stop()
})
await test('Editor saves multiple product owner IDs without name inference and preserves them after a failed save',async()=>{
  const m=await view('Editor',['load','f','save','formElement','dirty'],fixtureHandler(requirement(),async()=>{throw Error('offline')}),{id:'9'})
  await m.load();m.formElement.value={checkValidity:()=>true,reportValidity:()=>true};m.f.ownerUserIds=['u_b','u_a'];await m.save()
  const body=JSON.parse(m.calls.find(call=>call.options?.method==='PATCH').options.body)
  assert.deepEqual(body.ownerUserIds,['u_b','u_a']);assert.equal(Object.hasOwn(body,'owner'),false);assert.deepEqual([...m.f.ownerUserIds],['u_b','u_a']);assert.equal(m.dirty.value,true);m.stop()
})
await test('child creation validates the parent in scope and inherits category and an available iteration',async()=>{
  const base=fixtureHandler({...requirement(),category:'客户端',sprint:'123'})
  const m=await view('Editor',['load','f','save','formElement'],async(path,options)=>path==='/sprints'?{items:[{id:123,name:'123',status:'进行中'}]}:base(path,options),{},{parentId:'9'})
  await m.load();assert.equal(m.f.parentId,9);assert.equal(m.f.category,'客户端');assert.equal(m.f.sprint,'123');assert.deepEqual([...m.f.ownerUserIds],[]) // The current frontend engineer is no longer auto-bound as a product owner.
  m.f.title='子需求';m.formElement.value={checkValidity:()=>true,reportValidity:()=>true};await m.save();const body=JSON.parse(m.calls.find(call=>call.options?.method==='POST').options.body);assert.equal(body.parentId,9);m.stop()
})
await test('child of an ended iteration stays in backlog and an inaccessible parent blocks creation',async()=>{
  const m=await view('Editor',['load','f','notice'],fixtureHandler({...requirement(),sprint:'已结束迭代'}),{},{parentId:'9'});await m.load();assert.equal(m.f.sprint,'待规划');assert.match(m.notice.value,/待规划/);m.stop()
  const base=fixtureHandler(requirement()),blocked=await view('Editor',['load','initialized','error'],async(path,options)=>{if(path==='/requirements/99')throw Error('Not found in project');return base(path,options)},{},{parentId:'99'})
  await blocked.load();assert.equal(blocked.initialized.value,false);assert.match(blocked.error.value,/Not found/);blocked.stop()
})
await test('child tab opens an embedded editor with the fixed parent and no navigation or hidden write',async()=>{
  const m=await detail();m.createChild();assert.deepEqual(m.childParent.value,{id:9,title:'Original user content'});assert.deepEqual(m.navigations,[]);assert.equal(m.calls.some(call=>call.options?.method==='POST'),false);m.stop()
})
await test('tag modifications have explicit keep-editing or discard navigation choices without hidden writes',async()=>{
  const m=await detail();m.detailDraft.tags='发布';m.detailDraft.tagColors={发布:'#DC2626'}
  let pending=m.confirmDetailLeave();assert.equal(m.leavePrompt.value,true);m.finishLeave(false);assert.equal(await pending,false);assert.equal(m.detailDraft.tags,'发布')
  pending=m.confirmDetailLeave();m.finishLeave(true);assert.equal(await pending,true);assert.equal(m.calls.some(call=>call.options?.method),false);m.stop()
})
await test('save and leave commits dirty labels and multiple owners in one request, never a comment',async()=>{
  const m=await detail();m.detailDraft.tags='发布';m.detailDraft.tagColors={发布:'#DC2626'};m.ownerDraft.value=['u_b','u_a']
  const pending=m.confirmDetailLeave();await m.saveAndLeave();assert.equal(await pending,true);assert.equal(m.leavePrompt.value,false);assert.equal(m.detailDirty.value,false)
  const mutations=m.calls.filter(call=>call.options?.method);assert.equal(mutations.length,1);assert.equal(mutations[0].options.method,'PATCH')
  const body=JSON.parse(mutations[0].options.body);assert.equal(body.tags,'发布');assert.deepEqual(body.ownerUserIds,['u_b','u_a']);m.stop()
})
await test('save-and-leave failure keeps the dialog and draft; unpublished comments cannot be automatically sent',async()=>{
  const m=await detail(async()=>{throw Error('offline')});m.detailDraft.tags='待保存';const pending=m.confirmDetailLeave();await m.saveAndLeave()
  assert.equal(m.leavePrompt.value,true);assert.equal(m.detailDraft.tags,'待保存');assert.equal(m.leaveError.value,'offline');m.finishLeave(false);assert.equal(await pending,false);m.stop()
  const withComment=await detail();withComment.comment.value='@同名 未发表';const commentPending=withComment.confirmDetailLeave();await withComment.saveAndLeave();assert.equal(withComment.calls.length,0);assert.equal(withComment.leavePrompt.value,true);withComment.finishLeave(false);assert.equal(await commentPending,false);assert.equal(withComment.comment.value,'@同名 未发表');withComment.stop()
})
await test('unassociated Figma drafts trigger explicit leave choices and are never submitted by save-and-leave',async()=>{
  const m=await detail();m.resourceDraft.value={url:'https://figma.com/design/abc',title:'未提交设计',opened:true};assert.equal(m.resourceDirty.value,true);assert.equal(m.detailDirty.value,true)
  const pending=m.confirmDetailLeave();await m.saveAndLeave();assert.equal(m.calls.length,0);assert.equal(m.leavePrompt.value,true);m.finishLeave(false);assert.equal(await pending,false);assert.equal(m.resourceDraft.value.title,'未提交设计')
  const discard=m.confirmDetailLeave();m.finishLeave(true);assert.equal(await discard,true);assert.equal(m.calls.length,0);m.stop()
})

const pickerSource = compileScript(parse(await read('src/components/MemberMultiSelect.vue')).descriptor,{id:'multi-member-test'}).content
const pickerOutput=ts.transpileModule(pickerSource,{compilerOptions:{module:ts.ModuleKind.CommonJS,target:ts.ScriptTarget.ES2022}}).outputText
const pickerExports={},recentMembers=await pure('src/recentMembers.ts')
const pickerImports={vue:Vue,'../i18n':i18n,'../mentions':mentions,'../recentMembers':recentMembers,'../layoutScope':{layoutScope:Vue.ref('t:u_a')},'../stores/workspace':{useWorkspaceStore:()=>Vue.reactive({session:{tenant:{id:'t'},user:{id:'u_a'},project:{id:'p'}}})}}
new Function('require','exports',pickerOutput)(id=>pickerImports[id],pickerExports)
const Picker=pickerExports.default;Picker.render=()=>null
const renderer=Vue.createRenderer({createElement:()=>({}),createText:()=>({}),createComment:()=>({}),insert:()=>{},remove:()=>{},setText:()=>{},setElementText:()=>{},parentNode:()=>null,nextSibling:()=>null,patchProp:()=>{}})
await test('multi-member selection distinguishes same-name IDs, preserves historical chips and blocks implicit submit',async()=>{
  const state=Vue.reactive({ids:['u_old'],members})
  const root=Vue.h(Vue.defineComponent({setup:()=>()=>Vue.h(Picker,{modelValue:state.ids,members:state.members,snapshots:[{id:'u_old',name:'历史成员'}],'onUpdate:modelValue':ids=>{state.ids=ids}})})),container={}
  renderer.render(root,container)
  const context=root.component.subTree.component.setupState
  assert.deepEqual(context.selected.map(member=>member.id),['u_old']);assert.equal(context.selected[0].historical,true)
  context.toggle('u_a');await Vue.nextTick();context.toggle('u_b');await Vue.nextTick()
  assert.deepEqual([...state.ids],['u_old','u_a','u_b'])
  context.remove('u_old');await Vue.nextTick();assert.deepEqual([...state.ids],['u_a','u_b'])
  let prevented=false;context.opened=false;context.keydown({key:'Enter',preventDefault:()=>{prevented=true}});assert.equal(prevented,true);assert.deepEqual([...state.ids],['u_a','u_b'])
  state.members=[];await Vue.nextTick();assert.deepEqual([...state.ids],['u_a','u_b'])
  renderer.render(null,container)
})
await test('select-me and department bulk selection add only active project members once, preserving historical selections',async()=>{
  const state=Vue.reactive({ids:['u_old'],members}),root=Vue.h(Vue.defineComponent({setup:()=>()=>Vue.h(Picker,{modelValue:state.ids,members:state.members,'onUpdate:modelValue':ids=>{state.ids=ids}})})),container={}
  renderer.render(root,container);const context=root.component.subTree.component.setupState
  context.selectMe();await Vue.nextTick();assert.deepEqual([...state.ids],['u_old','u_a'])
  context.department='dept_product';context.addDepartment();await Vue.nextTick();assert.deepEqual([...state.ids],['u_old','u_a','u_b'])
  context.addDepartment();await Vue.nextTick();assert.deepEqual([...state.ids],['u_old','u_a','u_b'])
  context.query='产品研发';assert.deepEqual(context.options.map(member=>member.id),['u_a','u_b'])
  renderer.render(null,container)
})
const tagsSource=compileScript(parse(await read('src/components/RequirementTags.vue')).descriptor,{id:'project-tags-test'}).content
const tagsExports={},tagsOutput=ts.transpileModule(tagsSource,{compilerOptions:{module:ts.ModuleKind.CommonJS,target:ts.ScriptTarget.ES2022}}).outputText
new Function('require','exports',tagsOutput)(id=>id==='vue'?Vue:id==='../i18n'?i18n:fields,tagsExports)
const Tags=tagsExports.default;Tags.render=()=>null
await test('project label suggestions retain catalog colors and an unadded text draft does not dirty the requirement',async()=>{
  const state=Vue.reactive({tags:'',colors:{},options:[{name:'发布',color:'#DC2626',count:2000},{name:'客户端',color:'#059669',count:9}]}),events=[]
  const root=Vue.h(Vue.defineComponent({setup:()=>()=>Vue.h(Tags,{modelValue:state.tags,colors:state.colors,options:state.options,'onUpdate:modelValue':value=>{state.tags=value;events.push(value)},'onUpdate:colors':value=>{state.colors=value}})})),container={}
  renderer.render(root,container);const context=root.component.subTree.component.setupState
  context.name='尚未添加';assert.equal(state.tags,'');assert.equal(events.length,0);context.reset();assert.equal(events.length,0)
  context.name='发';assert.equal(context.suggestions.length,1);context.choose(context.suggestions[0]);await Vue.nextTick()
  assert.equal(state.tags,'发布');assert.equal(state.colors['发布'],'#DC2626');assert.equal(context.name,'');assert.equal(context.opened,false)
  context.name='客户端';context.opened=true;let prevented=false;context.keydown({key:'Enter',preventDefault:()=>{prevented=true}});await Vue.nextTick()
  assert.equal(prevented,true);assert.equal(state.tags,'发布,客户端');assert.equal(state.colors['客户端'],'#059669')
  renderer.render(null,container)
})
const weightSource=compileScript(parse(await read('src/components/RequirementWeights.vue')).descriptor,{id:'weight-quick-test'}).content
const weightExports={},weightOutput=ts.transpileModule(weightSource,{compilerOptions:{module:ts.ModuleKind.CommonJS,target:ts.ScriptTarget.ES2022}}).outputText
new Function('require','exports',weightOutput)(id=>id==='vue'?Vue:id==='../i18n'?i18n:id==='../requirementFields'?fields:{},weightExports)
const Weight=weightExports.default;Weight.render=()=>null
await test('weight presets change only the chosen difficulty and preserve multi-person bindings without a save request',async()=>{
  const initial=fields.emptyRoleWeights();initial.frontend={userId:'u_a',userIds:['u_a','u_b'],value:3};initial.backend={userId:'u_b',userIds:['u_b'],value:7}
  const state=Vue.reactive({weights:initial,disabled:false,readonly:false}),emitted=[]
  const root=Vue.h(Vue.defineComponent({setup:()=>()=>Vue.h(Weight,{modelValue:state.weights,members,disabled:state.disabled,readonly:state.readonly,'onUpdate:modelValue':value=>{state.weights=value;emitted.push(value)}})})),container={}
  renderer.render(root,container);const context=root.component.subTree.component.setupState
  context.openQuick('frontend');assert.equal(context.quickRole,'frontend');context.applyQuick('frontend',2000);await Vue.nextTick()
  assert.equal(state.weights.frontend.value,2000);assert.deepEqual([...state.weights.frontend.userIds],['u_a','u_b']);assert.deepEqual({...state.weights.backend},initial.backend);assert.equal(context.total,2007);assert.equal(emitted.length,1)
  context.applyQuick('frontend',123);assert.equal(emitted.length,1)
  context.setValue('frontend',{target:{value:'0.5'}});await Vue.nextTick();assert.equal(state.weights.frontend.value,0.5);assert.equal(context.total,7.5)
  renderer.render(null,container)
})
await test('weight quick entry supports explicit touch opening and Escape, while disabled and read-only states block all edits',async()=>{
  const state=Vue.reactive({weights:fields.emptyRoleWeights(),disabled:false,readonly:false}),emitted=[]
  const root=Vue.h(Vue.defineComponent({setup:()=>()=>Vue.h(Weight,{modelValue:state.weights,members,disabled:state.disabled,readonly:state.readonly,'onUpdate:modelValue':value=>emitted.push(value)})})),container={}
  renderer.render(root,container);const context=root.component.subTree.component.setupState
  context.hoverQuick('ui',{pointerType:'touch'});assert.equal(context.quickRole,null);context.openQuick('ui');assert.equal(context.quickRole,'ui');context.openQuick('ui');assert.equal(context.quickRole,'ui')
  context.hoverQuick('backend',{pointerType:'mouse'});assert.equal(context.quickRole,'backend')
  // Reproduce the browser order against the actual template button binding:
  // pointerenter opens it before the first mouse click is dispatched.
  const trigger=(await read('src/components/RequirementWeights.vue')).match(/class="weight-quick-trigger"[^>]*@click="(\w+)\(row.key\)"/)
  assert.ok(trigger,'quick-value trigger needs an explicit click handler');context[trigger[1]]('backend');assert.equal(context.quickRole,'backend')
  context[trigger[1]]('backend');assert.equal(context.quickRole,'backend')
  context.blurQuick('backend',{currentTarget:{contains:()=>false},relatedTarget:null});assert.equal(context.quickRole,null)
  context.openQuick('product');let prevented=false,stopped=false;context.escapeQuick({preventDefault:()=>{prevented=true},stopPropagation:()=>{stopped=true}});assert.equal(context.quickRole,null);assert(prevented&&stopped)
  context.openQuick('backend');state.disabled=true;await Vue.nextTick();assert.equal(context.quickRole,null);context.openQuick('ui');context.applyQuick('ui',500);context.setValue('ui',{target:{value:'100'}});assert.equal(context.quickRole,null);assert.equal(emitted.length,0)
  state.disabled=false;state.readonly=true;await Vue.nextTick();context.openQuick('ui');context.applyQuick('ui',1000);context.setMembers('ui',['u_b']);assert.equal(context.quickRole,null);assert.equal(emitted.length,0)
  renderer.render(null,container)
})
console.log(`Passed ${count} requirement collaboration tests.`)

import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import ts from 'typescript'
import * as Vue from 'vue'
import { parse, compileScript, compileTemplate } from 'vue/compiler-sfc'

const read = path => readFileSync(new URL('../' + path, import.meta.url), 'utf8')
function evaluate(source, imports = {}, extras = {}) {
  const exports = {}, js = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText
  new Function('require','exports',...Object.keys(extras),js)(id=>imports[id]||{},exports,...Object.values(extras)); return exports
}
const clone = value => JSON.parse(JSON.stringify(value)), flush = async () => { for(let i=0;i<20;i++)await Vue.nextTick() }
const deferred=()=>{let resolve,reject;const promise=new Promise((yes,no)=>{resolve=yes;reject=no});return {resolve,reject,promise}}
const statuses=[{id:1,key:'规划中',name:'规划中',category:'todo',enabled:true,system:true,color:'#665FE8',sortOrder:10},{id:2,key:'开发中',name:'正在研发',category:'doing',enabled:true,color:'#3399AA',sortOrder:20},{id:3,key:'完成',name:'完成',category:'done',enabled:true,color:'#19A781',sortOrder:30},{id:4,key:'历史',name:'旧状态',category:'doing',enabled:false,color:'#AAAAAA',sortOrder:40}]
const workflow={initialStatus:'规划中',endStatuses:['完成'],transitions:[{from:'规划中',to:'开发中',roles:['backend']}],version:4,canManage:true,roles:[{key:'project_admin',name:'项目管理员'},{key:'backend',name:'后端工程师'},{key:'frontend',name:'前端工程师'},{key:'viewer',name:'只读'}]}
const members=[{id:'u_front',name:'同名',email:'front@example.test',active:true,departmentIds:['d_front']},{id:'u_back',name:'同名',email:'back@example.test',active:true,departmentIds:['d_back']},{id:'u_old',name:'历史成员',active:false,departmentIds:['d_front']}]
const departments=[{id:'d_front',name:'前端部门',status:'active'},{id:'d_back',name:'后端部门',status:'active'},{id:'d_old',name:'历史部门',status:'inactive'}]
const baseField={id:7,objectType:'requirement',key:'testers',name:'测试人员',type:'users',description:'保留说明',required:false,searchable:false,filterable:true,listVisible:true,enabled:false,sortOrder:20,defaultValue:['u_old'],options:[],departmentId:'d_front'}
async function defaultAPI(path, options) {
  if(options?.method){const body=JSON.parse(options.body||'{}');return path==='/requirement-workflow'?{...workflow,...body,version:5}:{id:9,...body}}
  if(path.startsWith('/field-definitions'))return {items:[clone(baseField)]}
  if(path.startsWith('/field-presets'))return {systemFields:[{key:'title',name:'标题',type:'text',required:true}],presets:[{key:'testers',installed:true},{key:'extra_weight',installed:false}],canManage:true}
  if(path==='/departments')return {items:clone(departments)}
  if(path==='/members')return {items:clone(members)}
  if(path==='/requirement-statuses')return {items:clone(statuses),canManage:true}
  if(path==='/requirement-workflow')return clone(workflow)
  return {items:[]}
}
async function component(path, expose, handler=defaultAPI, { mount=true }={}) {
  const mounts=[],unmounts=[],guards=[],calls=[],events=[],listeners=new Map(),storage=new Map([['devflow-project','p-current']]),scope=Vue.effectScope()
  let answer=true,confirmations=0,value
  const windowMock={confirm:()=>{confirmations++;return answer},addEventListener:(name,fn)=>{if(!listeners.has(name))listeners.set(name,new Set());listeners.get(name).add(fn)},removeEventListener:(name,fn)=>listeners.get(name)?.delete(fn)}
  // Fields.vue 的设置页签同步到 URL；测试也提供可观察的最小 route/router，避免把路由行为误当成组件依赖错误。
  const route=Vue.reactive({query:{},params:{},path:'/settings/fields'})
  const router={replace:async(location={})=>{const next=location&&typeof location==='object'&&location.query&&typeof location.query==='object'?location.query:{};for(const key of Object.keys(route.query))delete route.query[key];Object.assign(route.query,next)}}
  const globals={localStorage:{getItem:key=>storage.get(key)||null},window:windowMock,document:{activeElement:null},HTMLElement:class{},defineExpose:()=>{},defineEmits:()=>(...event)=>events.push(event)}
  const vue={...Vue,onMounted:fn=>mounts.push(fn),onBeforeUnmount:fn=>unmounts.push(fn)}
  const api=async(path,options)=>{calls.push({path,options});return handler(path,options)}
  const helpers=evaluate(read('src/components/settingsScope.ts'),{vue,'../api':{api}},globals)
  const imports={vue,'../calendarDates':evaluate(read('src/calendarDates.ts')),'vue-router':{onBeforeRouteLeave:fn=>guards.push(fn),onBeforeRouteUpdate:fn=>guards.push(fn),useRoute:()=>route,useRouter:()=>router},'../i18n':{t:(text,params={})=>text.replace(/\{(\w+)\}/g,(all,key)=>params[key]??all),locale:Vue.ref('zh-CN')},'./settingsScope':helpers,'../components/settingsScope':helpers}
  const source=read(path).match(/<script setup[^>]*>([\s\S]*?)<\/script>/)[1]
  scope.run(()=>{value=evaluate(source+'\nexport {'+expose.join(',')+'}',imports,globals)})
  if(mount){for(const fn of mounts)await fn();await flush()}
  return {...value,calls,events,guards,storage,listeners,setAnswer:value=>{answer=value},get confirmations(){return confirmations},dispatch(name,event={}){for(const fn of listeners.get(name)||[])fn(event)},stop(){for(const fn of unmounts)fn();scope.stop()}}
}
const fieldsExpose=['scope','load','form','open','close','save','error','notice','noticeParams','opened','saving','canManage','items','systemFields','optionsText','eligibleMembers','selectedMemberIds','toggleMember','removeMember','clearDefault','validateAndBuild','selectObject','objectType','directoryReady','directoryError','applyPresets','missingCount','dirty','statusSettings','workflowSettings','beforeProjectChange','beforeUnload','confirmLeave','deleteTarget','deleteError','beginDelete','closeDelete','confirmDelete']
const statusExpose=['scope','load','items','form','open','close','save','fillDefaults','opened','saving','dirty','error','notice','canManage']
const workflowExpose=['scope','load','form','dirty','saving','error','notice','conflict','toggleEdge','toggleRole','edgeOf','editor','save','externalChanged','selectedEdge','permittedRoles','toggleEnd','statusName']
let count=0;async function test(name,run){await run();count++;console.log('✓ '+name)}

await test('field catalogue separates system/custom data and scopes every request',async()=>{
  const m=await component('src/views/Fields.vue',fieldsExpose);assert.equal(m.systemFields.value[0].key,'title');assert.equal(m.items.value[0].key,'testers');assert.equal(m.missingCount.value,1)
  for(const call of m.calls)assert.equal(call.options.headers.get('X-DevFlow-Project'),'p-current');m.stop()
})
await test('people defaults select stable IDs and the real department relationship, including duplicate names',async()=>{
  const m=await component('src/views/Fields.vue',fieldsExpose);m.open();Object.assign(m.form,{name:'评审成员',key:'reviewers',type:'users',departmentId:'d_front'});assert.deepEqual(m.eligibleMembers.value.map(item=>item.id),['u_front'])
  m.toggleMember('u_back');assert.equal(m.form.defaultValue,null);m.toggleMember('u_front');assert.deepEqual(m.form.defaultValue,['u_front']);await m.save()
  const body=JSON.parse(m.calls.find(call=>call.options.method==='POST').options.body);assert.deepEqual(body.defaultValue,['u_front']);assert.equal(body.departmentId,'d_front');m.stop()
})
await test('unchanged historical defaults survive unrelated edits; changing department requires an explicit correction',async()=>{
  const m=await component('src/views/Fields.vue',fieldsExpose);m.open(clone(baseField));m.form.description='改说明';assert.deepEqual(m.validateAndBuild().defaultValue,['u_old']);m.form.departmentId='d_back';assert.throws(()=>m.validateAndBuild(),/默认成员必须/);assert.deepEqual(m.form.defaultValue,['u_old']);m.removeMember('u_old');assert.deepEqual(m.validateAndBuild().defaultValue,[]);m.stop()
})
await test('clear defaults explicitly submits null or arrays, without losing false and zero',async()=>{
  const m=await component('src/views/Fields.vue',fieldsExpose);m.open({...baseField,type:'number',defaultValue:0});assert.equal(m.validateAndBuild().defaultValue,0);m.clearDefault();assert.equal(m.validateAndBuild().defaultValue,null)
  m.open({...baseField,type:'boolean',defaultValue:false});assert.equal(m.validateAndBuild().defaultValue,false);m.clearDefault();assert.equal(m.validateAndBuild().defaultValue,null);m.stop()
})
await test('invalid field default values do not reach the API and remain visible',async()=>{
  const m=await component('src/views/Fields.vue',fieldsExpose);m.open({...baseField,type:'number',defaultValue:1});m.form.defaultValue='invalid';const before=m.calls.length;await m.save();assert.equal(m.calls.length,before);assert.equal(m.form.defaultValue,'invalid');assert.equal(m.opened.value,true);assert.match(m.error.value,/有效数字/);m.stop()
})
await test('field save cannot duplicate, close, switch object, or be invalidated by a reload while pending',async()=>{
  const pending=deferred(),m=await component('src/views/Fields.vue',fieldsExpose,(path,options)=>options?.method?pending.promise:defaultAPI(path,options));m.open(baseField);m.form.description='草稿';const request=m.save();await m.save();m.close();m.selectObject('defect');await m.load();assert.equal(m.calls.filter(call=>call.options.method).length,1);assert.equal(m.opened.value,true);assert.equal(m.objectType.value,'requirement')
  pending.reject(Error('offline'));await request;assert.equal(m.form.description,'草稿');assert.equal(m.opened.value,true);assert.equal(m.saving.value,false);assert.equal(m.error.value,'offline');m.stop()
})
await test('adding default fields sends only the object type and preserves existing values',async()=>{
  const m=await component('src/views/Fields.vue',fieldsExpose,(path,options)=>path==='/field-presets/apply'?{createdCount:1,skippedCount:1,items:[{...baseField,id:8,key:'extra_weight',type:'number',defaultValue:null}]}:defaultAPI(path,options))
  await m.applyPresets();assert.deepEqual(JSON.parse(m.calls.at(-1).options.body),{objectType:'requirement'});assert.equal(m.items.value.find(item=>item.id===7).enabled,false);assert.deepEqual(m.items.value.find(item=>item.id===7).defaultValue,['u_old']);assert.equal(m.missingCount.value,0);m.stop()
})
await test('directory outages keep the field catalogue and prevent unvalidated people defaults',async()=>{
  const m=await component('src/views/Fields.vue',fieldsExpose,(path,options)=>path==='/members'?Promise.reject(Error('directory offline')):defaultAPI(path,options));assert.equal(m.items.value.length,1);assert.equal(m.directoryReady.value,false);assert.equal(m.directoryError.value,'directory offline');m.open(baseField);const count=m.calls.length;await m.save();assert.equal(m.calls.length,count);assert.match(m.error.value,/人员目录未加载/);m.stop()
})
await test('failed object switch cannot leave editable fields from the previous work-item type',async()=>{
  const m=await component('src/views/Fields.vue',fieldsExpose,(path,options)=>path.includes('objectType=defect')?Promise.reject(Error('offline')):defaultAPI(path,options));m.selectObject('defect');await flush();assert.equal(m.items.value.length,0);assert.equal(m.canManage.value,false);m.open();assert.equal(m.opened.value,false);m.stop()
})
await test('route/project/unload guards aggregate child drafts, prohibit saves in flight, and preserve cancelled drafts',async()=>{
  const m=await component('src/views/Fields.vue',fieldsExpose);m.workflowSettings.value={dirty:true,saving:false};m.setAnswer(false);let prevented=0;m.beforeProjectChange({preventDefault:()=>prevented++});assert.equal(prevented,1);assert.equal(m.workflowSettings.value.dirty,true);assert.equal(m.confirmLeave(),false)
  m.setAnswer(true);m.beforeProjectChange({preventDefault:()=>prevented++});const count=m.confirmations;assert.equal(m.confirmLeave(),true);assert.equal(m.confirmations,count)
  m.storage.set('devflow-project','p-next');m.beforeUnload({preventDefault:()=>prevented++});assert.equal(prevented,1)
  m.storage.set('devflow-project','p-current');m.dispatch('devflow-project-change-cancelled');m.beforeUnload({preventDefault:()=>prevented++});assert.equal(prevented,2)
  m.workflowSettings.value.saving=true;m.beforeProjectChange({preventDefault:()=>prevented++});assert.equal(prevented,3);m.stop()
})
await test('identity changes suppress late saves and keep a draft rather than apply another account response',async()=>{
  const pending=deferred(),m=await component('src/views/Fields.vue',fieldsExpose,(path,options)=>options?.method?pending.promise:defaultAPI(path,options));m.open(baseField);m.form.description='身份切换前草稿';const request=m.save();m.dispatch('devflow-identity-changed');pending.resolve({...baseField,description:'迟到响应'});await request;assert.equal(m.scope.locked.value,true);assert.equal(m.form.description,'身份切换前草稿');assert.equal(m.opened.value,true);assert.equal(m.items.value[0].description,'保留说明');assert.equal(m.calls.at(-1).options.signal.aborted,true);m.stop()
})
await test('custom field deletion requires explicit confirmation and cancel never sends a mutation',async()=>{
  const m=await component('src/views/Fields.vue',fieldsExpose)
  m.beginDelete({...baseField,system:true});assert.equal(m.deleteTarget.value,null)
  m.beginDelete({...baseField,id:999});assert.equal(m.deleteTarget.value,null)
  m.beginDelete({...baseField,key:'another-key'});assert.equal(m.deleteTarget.value,null)
  m.beginDelete(baseField);assert.equal(m.deleteTarget.value.id,7);assert.equal(m.calls.some(call=>call.options.method),false)
  assert.equal(m.closeDelete(),true);assert.equal(m.deleteTarget.value,null);assert.equal(m.items.value.length,1)
  m.canManage.value=false;m.beginDelete(baseField);await m.confirmDelete();assert.equal(m.deleteTarget.value,null);assert.equal(m.calls.some(call=>call.options.method),false);m.stop()
})
await test('confirmed deletion binds the exact field, preserves other rows, and reports retained history',async()=>{
  const m=await component('src/views/Fields.vue',fieldsExpose,(path,options)=>options?.method==='DELETE'?{deleted:true,id:7,preservedValueCount:12}:defaultAPI(path,options))
  m.items.value.push({...baseField,id:8,key:'other',name:'另一个字段'});m.beginDelete(baseField);await m.confirmDelete()
  const call=m.calls.find(call=>call.options.method==='DELETE');assert.equal(call.path,'/field-definitions/7');assert.deepEqual(JSON.parse(call.options.body),{confirmKey:'testers',objectType:'requirement'});assert.equal(call.options.headers.get('X-DevFlow-Project'),'p-current')
  assert.deepEqual(m.items.value.map(item=>item.id),[8]);assert.equal(m.deleteTarget.value,null);assert.equal(m.noticeParams.value.count,12);assert.match(m.notice.value,/历史字段值/);assert.equal(m.missingCount.value,1);m.stop()
})
await test('pending deletion cannot duplicate, close, switch object, reload, or leave; failures keep confirmation',async()=>{
  const pending=deferred(),m=await component('src/views/Fields.vue',fieldsExpose,(path,options)=>options?.method==='DELETE'?pending.promise:defaultAPI(path,options))
  m.beginDelete(baseField);const request=m.confirmDelete();await m.confirmDelete();assert.equal(m.closeDelete(),false);m.selectObject('defect');await m.load();assert.equal(m.objectType.value,'requirement');assert.equal(m.deleteTarget.value.id,7);assert.equal(m.calls.filter(call=>call.options.method==='DELETE').length,1);assert.equal(m.confirmLeave(),false)
  let prevented=0;m.beforeProjectChange({preventDefault:()=>prevented++});m.beforeUnload({preventDefault:()=>prevented++});assert.equal(prevented,2)
  pending.reject(Error('audit unavailable'));await request;assert.equal(m.deleteError.value,'audit unavailable');assert.equal(m.items.value[0].id,7);assert.equal(m.deleteTarget.value.id,7);assert.equal(m.saving.value,false);m.stop()
})
await test('invalid deletion receipts cannot falsely remove a row or claim success',async()=>{
  for(const receipt of [{deleted:false,id:7,preservedValueCount:0},{deleted:true,id:8,preservedValueCount:0},{deleted:true,id:7,preservedValueCount:-1}]){
    const m=await component('src/views/Fields.vue',fieldsExpose,(path,options)=>options?.method==='DELETE'?receipt:defaultAPI(path,options));m.beginDelete(baseField);await m.confirmDelete();assert.equal(m.items.value[0].id,7);assert.equal(m.deleteTarget.value.id,7);assert.match(m.deleteError.value,/删除结果未确认/);assert.equal(m.notice.value,'');m.stop()
  }
})
await test('account or project changes ignore a late deletion response and abort the scoped request',async()=>{
  for(const event of ['devflow-identity-changed','devflow-project-changed']){
    const pending=deferred(),m=await component('src/views/Fields.vue',fieldsExpose,(path,options)=>options?.method==='DELETE'?pending.promise:defaultAPI(path,options));m.beginDelete(baseField);const request=m.confirmDelete();m.dispatch(event);pending.resolve({deleted:true,id:7,preservedValueCount:2});await request;assert.equal(m.scope.locked.value,true);assert.equal(m.items.value[0].id,7);assert.equal(m.deleteTarget.value.id,7);assert.equal(m.notice.value,'');assert.equal(m.calls.at(-1).options.signal.aborted,true);m.stop()
  }
})
await test('field deletion dialog is named, keyboard-managed, and explains retained data before confirming',()=>{
  const source=read('src/views/Fields.vue');assert.match(source,/role="alertdialog"/);assert.match(source,/useSettingsDialog\(deleteOpened,\s*\(\) => closeDelete\(\)\)/);assert.match(source,/:aria-label="t\('删除字段 \{name\}'/);assert.match(source,/已有字段值和删除审计会保留/);assert.match(source,/@click="confirmDelete"/)
})
await test('status creation sends name/color/category, never a user-editable stable key',async()=>{
  const m=await component('src/components/RequirementStatusSettings.vue',statusExpose);m.open();Object.assign(m.form,{name:'自定义验收',color:'#AABBCC',category:'doing'});await m.save();const body=JSON.parse(m.calls.find(call=>call.options.method==='POST').options.body);assert.equal(body.name,'自定义验收');assert.equal(Object.hasOwn(body,'key'),false);assert.equal(m.opened.value,false);m.stop()
})
await test('status saves retain drafts on failure and block backdrop/duplicate actions',async()=>{
  const pending=deferred(),m=await component('src/components/RequirementStatusSettings.vue',statusExpose,(path,options)=>options?.method?pending.promise:defaultAPI(path,options));m.open(statuses[0]);m.form.name='新状态名';const request=m.save();m.close();await m.save();assert.equal(m.opened.value,true);assert.equal(m.calls.filter(call=>call.options.method).length,1);pending.reject(Error('cannot disable initial status'));await request;assert.equal(m.error.value,'cannot disable initial status');assert.equal(m.form.name,'新状态名');m.stop()
})
await test('failed status default restoration remains an error instead of being hidden by automatic reload',async()=>{
  const m=await component('src/components/RequirementStatusSettings.vue',statusExpose,(path,options)=>options?.method?Promise.reject(Error('offline')):defaultAPI(path,options));await m.fillDefaults();assert.equal(m.error.value,'offline');assert.equal(m.items.value.length,4);assert.equal(m.calls.length,2);m.stop()
})
await test('read-only settings cannot create statuses, fields, or workflow transitions',async()=>{
  const handler=async(path,options)=>({...await defaultAPI(path,options),canManage:false})
  const f=await component('src/views/Fields.vue',fieldsExpose,handler);f.open();await f.applyPresets();assert.equal(f.opened.value,false);assert.equal(f.calls.some(call=>call.options.method),false);f.stop()
  const s=await component('src/components/RequirementStatusSettings.vue',statusExpose,handler);s.open();await s.fillDefaults();assert.equal(s.opened.value,false);assert.equal(s.calls.some(call=>call.options.method),false);s.stop()
  const w=await component('src/components/WorkflowSettings.vue',workflowExpose,handler);w.toggleEdge('开发中','完成');assert.equal(w.form.transitions.length,1);w.stop()
})
await test('workflow matrix prohibits same-state and disabled targets, allows historical sources, and requires explicit roles',async()=>{
  const m=await component('src/components/WorkflowSettings.vue',workflowExpose);m.toggleEdge('规划中','规划中');m.toggleEdge('规划中','历史');assert.equal(m.form.transitions.length,1);m.toggleEdge('历史','开发中');assert.ok(m.edgeOf('历史','开发中'));await m.save();assert.match(m.error.value,/至少一个有效角色/);assert.equal(m.calls.some(call=>call.options.method==='PUT'),false)
  m.toggleRole('viewer');assert.deepEqual(m.selectedEdge.value.roles,[]);m.toggleRole('backend');assert.deepEqual(m.selectedEdge.value.roles,['backend']);await m.save();assert.equal(m.dirty.value,false);assert.equal(m.form.version,5);const body=JSON.parse(m.calls.find(call=>call.options.method==='PUT').options.body);assert.equal(body.version,4);assert.equal(m.statusName('开发中'),'正在研发');m.stop()
})
await test('workflow initial/end validation prevents invalid graphs before a write',async()=>{
  const m=await component('src/components/WorkflowSettings.vue',workflowExpose);m.form.initialStatus='完成';await m.save();assert.match(m.error.value,/起始状态/);m.form.initialStatus='规划中';m.form.endStatuses=[];await m.save();assert.match(m.error.value,/结束状态/);assert.equal(m.calls.some(call=>call.options.method==='PUT'),false);m.stop()
})
await test('workflow version conflicts preserve all changes and require explicit reload confirmation',async()=>{
  const conflict=Object.assign(Error('version conflict'),{status:409}),m=await component('src/components/WorkflowSettings.vue',workflowExpose,(path,options)=>options?.method==='PUT'?Promise.reject(conflict):defaultAPI(path,options));m.toggleEdge('开发中','完成');m.toggleRole('frontend');const draft=clone(m.form);await m.save();assert.equal(m.conflict.value,true);assert.deepEqual(clone(m.form),draft);assert.equal(m.dirty.value,true)
  m.setAnswer(false);await m.load(true);assert.deepEqual(clone(m.form),draft);m.setAnswer(true);await m.load(true);assert.equal(m.dirty.value,false);assert.equal(m.form.transitions.length,1);m.stop()
})
await test('status changes do not silently replace a workflow draft and pending writes do not duplicate',async()=>{
  const pending=deferred(),m=await component('src/components/WorkflowSettings.vue',workflowExpose,(path,options)=>options?.method==='PUT'?pending.promise:defaultAPI(path,options));m.toggleEdge('开发中','完成');m.toggleRole('backend');const draft=clone(m.form);m.externalChanged();assert.deepEqual(clone(m.form),draft);assert.equal(m.conflict.value,true)
  const request=m.save();await m.save();await m.load(true);assert.equal(m.calls.filter(call=>call.options.method==='PUT').length,1);pending.resolve({...workflow,...draft,version:5});await request;assert.equal(m.saving.value,false);assert.equal(m.dirty.value,false);m.stop()
})
await test('all settings templates compile and every control uses explicit values and submit types',()=>{
  for(const file of ['src/views/Fields.vue','src/components/RequirementStatusSettings.vue','src/components/WorkflowSettings.vue']){
    const content=read(file),{descriptor}=parse(content),script=compileScript(descriptor,{id:file}),template=compileTemplate({source:descriptor.template.content,filename:file,id:file,compilerOptions:{bindingMetadata:script.bindings}});assert.deepEqual(template.errors,[],file)
    for(const button of content.matchAll(/<button\b[^>]*>/g))assert.match(button[0],/type="(?:button|submit)"/,file)
  }
})
console.log(`Passed ${count} application settings tests.`)

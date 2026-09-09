import {workflow} from './workflow-test-support.mjs'
import { readFileSync } from 'node:fs'
import { createRequire } from 'node:module'
import assert from 'node:assert/strict'
import { fileURLToPath } from 'node:url'
import { flush as flushTestingWorkspace, mountTestingComponent, testingWorkspace, workspaceFixture } from './testing-workspace-test-support.mjs'
const root=fileURLToPath(new URL('../', import.meta.url)).replace(/\/$/, '')
const require=createRequire(root+'/package.json'),ts=require('typescript'),Vue=require('vue')
const defectPeople={}
new Function('exports',ts.transpileModule(readFileSync(`${root}/src/defectPeople.ts`,'utf8'),{compilerOptions:{module:ts.ModuleKind.CommonJS,target:ts.ScriptTarget.ES2022}}).outputText)(defectPeople)
function evaluate(name,exposed,handler=async()=>({items:[]}),initialQuery={}){
  const file=readFileSync(`${root}/src/views/${name}.vue`,'utf8'),source=file.match(/<script setup[^>]*>([\s\S]*?)<\/script>/)[1]
  const mounts=[],unmounts=[],calls=[],storage=new Map([['devflow-project','p-current']]),route=Vue.reactive({query:initialQuery}),language=Vue.ref('zh-CN'),location={href:''}
  const scope=Vue.effectScope(),exports={}
  const api=async(path,options)=>{calls.push({path,options});return handler(path,options)}
  const imports={'../requirementWorkflow':workflow,vue:{...Vue,onMounted:fn=>mounts.push(fn),onBeforeUnmount:fn=>unmounts.push(fn)},'vue-router':{useRoute:()=>route,useRouter:()=>({replace:async x=>{route.query=x.query||{}},push:async()=>{}}),onBeforeRouteLeave:()=>{},onBeforeRouteUpdate:()=>{}},'../api':{api},'../components/settingsScope':{useSettingsScope:()=>({locked:Vue.ref(false),current:()=>true,request:api,project:'p-current'}),useSettingsDialog:()=>Vue.ref(null)},'../i18n':{t:x=>language.value==='en-US'?`en:${x}`:x,locale:language,formatDate:x=>String(x)}}
  imports['../defectPeople']=defectPeople
  // Projects 的收藏布局按已验证租户/账户隔离；给旧视图评估器提供稳定作用域，
  // 使本套件继续验证模块行为，而不是绕过新的隐私缓存边界。
  imports['../layoutScope']={layoutScope:Vue.ref('tenant:user')}
  const output=ts.transpileModule(source+`\nexport { ${exposed.join(', ')} }`,{compilerOptions:{module:ts.ModuleKind.CommonJS,target:ts.ScriptTarget.ES2022}}).outputText
  scope.run(()=>new Function('require','exports','localStorage','window','location','CustomEvent',output)(id=>id.endsWith('.vue')?{}:imports[id],exports,{getItem:k=>storage.get(k)??null,setItem:(k,v)=>storage.set(k,String(v)),removeItem:k=>storage.delete(k)},{addEventListener:()=>{},removeEventListener:()=>{},dispatchEvent:()=>{}},location,class{constructor(name,opts){this.name=name;this.detail=opts?.detail}}))
  return {...exports,calls,storage,route,language,location,mount:async()=>{for(const f of mounts)await f()},stop:()=>{for(const f of unmounts)f();scope.stop()}}
}
const deferred=()=>{let resolve,reject;const promise=new Promise((a,b)=>{resolve=a;reject=b});return{resolve,reject,promise}}
let count=0
async function test(name,run){await run();count++;console.log('✓ '+name)}
const data=async path=>path==='/members'?{items:[{id:'u-new',name:'用户原文',active:true},{id:'u-old',name:'旧成员',active:false}]}:path==='/sprints'?{items:[{id:1,name:'123',status:'规划中'},{id:2,name:'22',status:'进行中'},{id:3,name:'已结束原名',status:'已完成'}]}:{items:[],summary:{}}
await test('test plans use project members and full active iteration names, with no seeded identity defaults',async()=>{
  const m=await mountTestingComponent('src/components/testing/TestingOperations.vue',{props:{tab:'plans',workspace:workspaceFixture(),members:[{id:'u-new',name:'用户原文',active:true},{id:'u-old',name:'旧成员',active:false}],plans:[],executions:[],sprints:[{id:1,name:'123',status:'规划中'},{id:2,name:'22',status:'进行中'},{id:3,name:'已结束原名',status:'已完成'}]},handler:data})
  await flushTestingWorkspace();assert.equal(m.planEdit.owner,'');assert.equal(m.planEdit.executorUserId,'');assert.equal(m.planEdit.sprint,'待规划')
  assert.deepEqual(m.executorOptions.value.map(x=>x.value),['','u-new']);assert.match(m.source,/sprints\.filter\(x=>\['规划中','进行中'\]\.includes\(x\.status\)\)/);m.stop()
})
await test('test case requirement links reject unsafe IDs and retain the server-provided code and title',async()=>{
  assert.equal(testingWorkspace.testPositiveID('17'),17);assert.equal(testingWorkspace.testPositiveID(17),17)
  assert.equal(testingWorkspace.testPositiveID(['17']),null);assert.equal(testingWorkspace.testPositiveID('017'),null);assert.equal(testingWorkspace.testPositiveID('17x'),null);assert.equal(testingWorkspace.testPositiveID(0),null);assert.equal(testingWorkspace.testPositiveID(1.5),null)
  const source=readFileSync(`${root}/src/components/testing/TestCaseLibrary.vue`,'utf8')
  assert.match(source,/<router-link v-if="item\.requirement"[^>]*:to="`\/requirements\?req=\$\{item\.requirement\.id\}`"[^>]*:title="item\.requirement\.title"/)
  assert.match(source,/\{\{item\.requirement\.code\}\}/)
})
await test('test case create deep link preselects a requirement before opening its editable draft',async()=>{
  const requirement={id:17,code:'REQ-0017',title:'审批流校验',status:'开发中',updatedAt:'2026-09-04T12:00:00Z'}
  const m=await mountTestingComponent('src/components/testing/TestCaseLibrary.vue',{props:{workspace:workspaceFixture(),members:[],executions:[]},query:{tab:'cases',requirement:'17',create:'1'},handler:async path=>path==='/requirements/17'?requirement:path.startsWith('/test-cases?')?{items:[],total:0}:data(path)})
  await flushTestingWorkspace();assert.equal(m.opened.value,true);assert.equal(m.edit.requirementId,17)
  assert.deepEqual(m.selectedRequirement.value,requirement)
  assert(m.apiCalls.some(call=>call.path==='/requirements/17'))
  m.stop()
})
await test('closing test case creation clears stale create and detail query state',async()=>{
  const m=await mountTestingComponent('src/components/testing/TestCaseLibrary.vue',{props:{workspace:workspaceFixture(),members:[],executions:[]},query:{tab:'cases',requirement:'17',create:'1',case:'4',plan:'5',execution:'6'},handler:async path=>path.startsWith('/test-cases?')?{items:[],total:0}:path==='/test-cases/4'?{id:4,title:'原用例',stepsDetail:[]}:data(path)})
  await flushTestingWorkspace();await m.close()
  assert.equal(m.opened.value,false);assert.deepEqual(m.route.query,{tab:'cases',requirement:'17',plan:'5',execution:'6'});m.stop()
})
await test('test case creation keeps the associated-requirement picker with a stable selected object boundary',async()=>{
  const source=readFileSync(`${root}/src/components/testing/TestCaseLibrary.vue`,'utf8')
  assert.match(source,/<RequirementPicker\b(?=[^>]*v-model="edit\.requirementId")(?=[^>]*:selected-requirement="selectedRequirement")(?=[^>]*@change="selectedRequirement=\$event")/)
  assert.doesNotMatch(source,/关联需求 ID/)
  assert.match(source,/testCaseTypes/);assert.match(source,/testCaseStates/)
})
await test('failed plan save preserves edit drawer and draft, with saving guard reset',async()=>{
  const m=await mountTestingComponent('src/components/testing/TestingOperations.vue',{props:{tab:'plans',workspace:workspaceFixture(),members:[],plans:[],executions:[],sprints:[]},handler:async(path,options)=>options?.method==='POST'?Promise.reject(Error('validation failed')):path.startsWith('/test-cases?')?{items:[],total:0}:data(path)})
  await flushTestingWorkspace();m.createPlan();m.planEdit.name='Keep original title';m.planEdit.scope='unsaved draft'
  await m.savePlan();assert.equal(m.showPlan.value,true);assert.equal(m.planEdit.scope,'unsaved draft');assert.equal(m.error.value,'validation failed');assert.equal(m.saving.value,false);m.stop()
})
await test('duplicate plan save clicks create one request and await success before closing',async()=>{
  const pending=deferred(),m=await mountTestingComponent('src/components/testing/TestingOperations.vue',{props:{tab:'plans',workspace:workspaceFixture(),members:[],plans:[],executions:[],sprints:[]},handler:(path,options)=>options?.method==='POST'?pending.promise:path.startsWith('/test-cases?')?{items:[],total:0}:data(path)})
  await flushTestingWorkspace();m.createPlan();m.planEdit.name='Keep title';const first=m.savePlan();await m.savePlan();assert.equal(m.apiCalls.filter(x=>x.options?.method==='POST'&&x.path==='/test-plans').length,1);assert.equal(m.showPlan.value,true)
  pending.resolve({});await first;assert.equal(m.showPlan.value,false);assert.equal(m.saving.value,false);m.stop()
})
await test('execution save locks its drawer until the request completes and refreshes only its own history',async()=>{
  const pending=deferred(),execution={id:3,caseId:31,caseTitle:'执行项',planName:'计划',status:'未执行',actualResult:'',note:''}
  const m=await mountTestingComponent('src/components/testing/TestingOperations.vue',{props:{tab:'executions',workspace:workspaceFixture(),members:[],plans:[],executions:[execution],sprints:[]},query:{tab:'executions',execution:'3'},handler:(path,options)=>options?.method==='PATCH'?pending.promise:path==='/test-cases/31'?{id:31,title:'执行项',stepsDetail:[]}:path==='/test-executions/3/history'?{items:[]}:data(path)})
  await flushTestingWorkspace();const saving=m.execute();await flushTestingWorkspace();await m.closeDetail();assert.equal(m.execution.value.id,3);pending.resolve({});await saving
  assert(m.apiCalls.some(x=>x.path==='/test-executions/3/history'));assert.equal(m.saving.value,false);m.stop()
})
await test('defect people and iteration options remain project scoped after composer extraction',async()=>{
  const m=evaluate('Defects',['activeMembers','availableSprints','testers'],data);await m.mount()
  assert.deepEqual(m.availableSprints.value.map(x=>x.name),['123','22']);assert.deepEqual(m.activeMembers.value.map(x=>x.id),['u-new']);assert.equal(m.activeMembers.value[0].name,'用户原文');assert.deepEqual(m.testers.value,[]);m.stop()
})
await test('real defect helper picks only an unambiguous active tester or current tester, using stable IDs',async()=>{
  const qa1={id:'qa-one',name:'同名测试',projectRole:'qa',active:true},qa2={id:'qa-two',name:'同名测试',projectRole:'qa',active:true},inactive={id:'old',name:'旧测试',projectRole:'qa',active:false},frontend={id:'front',name:'前端',projectRole:'frontend',active:true}
  assert.equal(defectPeople.defaultVerifier([]),undefined);assert.equal(defectPeople.defaultVerifier([frontend,inactive]),undefined)
  assert.equal(defectPeople.defaultVerifier([frontend,qa1,inactive]).id,'qa-one');assert.equal(defectPeople.defaultVerifier([qa1,qa2]),undefined)
  assert.equal(defectPeople.defaultVerifier([qa1,qa2],qa2.id).id,'qa-two');assert.equal(defectPeople.defaultVerifier([{...qa1,isCurrent:true},qa2]).id,'qa-one')
  assert.deepEqual(defectPeople.defectPersonValue([qa1,qa2],qa2.id),{verifier:'同名测试',verifierUserId:'qa-two'});assert.deepEqual(defectPeople.defectPersonValue([inactive],'old'),{verifier:'',verifierUserId:''})
})
await test('late defect PATCH response cannot overwrite a different selected defect',async()=>{
  const pending=deferred(),m=evaluate('Defects',['selected','patch','saving','session'],(path,options)=>options?.method==='PATCH'?pending.promise:data(path))
  m.session.value={user:{id:'u-authorized',role:'product'}};m.selected.value={id:1,status:'新建',allowedTransitions:['已确认']};const saving=m.patch('status','已确认');assert.equal(m.calls.filter(call=>call.options?.method==='PATCH').length,1);m.selected.value={id:2,status:'修复中'};pending.resolve({id:1,status:'已确认'});await saving
  assert.equal(m.selected.value.id,2);assert.equal(m.selected.value.status,'修复中');m.stop()
})
await test('global search rejects stale responses and exposes retryable errors',async()=>{
  const requests=[],m=evaluate('Search',['search','items','error','loading'],()=>{const request=deferred();requests.push(request);return request.promise})
  const old=m.search(),recent=m.search();requests[1].resolve({items:[{title:'最新'}],total:1});await recent;requests[0].resolve({items:[{title:'旧'}],total:1});await old;assert.equal(m.items.value[0].title,'最新')
  const failed=m.search();requests[2].reject(Error('offline'));await failed;assert.equal(m.error.value,'offline');assert.equal(m.loading.value,false);m.stop()
})
await test('personal work failure terminates loading and reports error',async()=>{
  const m=evaluate('MyWork',['load','loading','error'],async()=>{throw Error('offline')});await m.load();assert.equal(m.loading.value,false);assert.equal(m.error.value,'offline');m.stop()
})
await test('language switch refetches notification and outbox content without navigation',async()=>{
  const m=evaluate('Notifications',['tab'],async()=>({items:[],unread:0,mode:'mock'}));m.tab.value='outbox';await Vue.nextTick();m.calls.length=0;m.language.value='en-US';await Vue.nextTick();await Promise.resolve()
  assert(m.calls.some(x=>x.path.startsWith('/notifications?')));assert(m.calls.some(x=>x.path==='/notifications/outbox'));assert.equal(m.location.href,'');m.stop()
})
await test('notification failure clears loading, read failure prevents silent navigation',async()=>{
  const m=evaluate('Notifications',['load','open','loading','error'],async()=>{throw Error('offline')});await m.load();await m.open({id:1,projectId:'p-new',url:'/requirements?req=1'});assert.equal(m.loading.value,false);assert.equal(m.error.value,'offline');assert.equal(m.storage.get('devflow-project'),'p-current');assert.equal(m.location.href,'');m.stop()
})
await test('project editing uses a copy and failed save retains draft',async()=>{
  const m=evaluate('Projects',['openEdit','editing','patch','error'],async()=>{throw Error('permission denied')});const original={id:'p1',name:'Original',status:'active',canManage:true};m.openEdit(original);m.editing.value.name='Draft';assert.equal(original.name,'Original');await m.patch(m.editing.value,{name:'Draft'});assert.equal(m.editing.value.name,'Draft');assert.equal(m.error.value,'permission denied');m.stop()
})
await test('failed project visit restores original project scope',async()=>{
  const m=evaluate('Projects',['enter'],async()=>{throw Error('permission denied')});await m.enter({id:'p-denied'});assert.equal(m.storage.get('devflow-project'),'p-current');assert.equal(m.location.href,'');m.stop()
})
await test('field multiselect defaults stay arrays, and invalid numeric defaults never reach API',async()=>{
  const m=evaluate('Fields',['form','save','error','open','optionsText','canManage'],async(path,options)=>options?.method==='POST'?{id:1,...JSON.parse(options.body)}:data(path));m.canManage.value=true;m.open();m.form.name='客户类型';m.form.key='customer';m.form.type='multi_select';m.optionsText.value='原文A\noriginal B';m.form.defaultValue=['原文A','original B'];await m.save();const body=JSON.parse(m.calls.find(x=>x.options?.method==='POST').options.body);assert.deepEqual(body.defaultValue,['原文A','original B'])
  m.calls.length=0;m.open();m.form.name='权重';m.form.key='weight';m.form.type='number';m.form.defaultValue='not-a-number';await m.save();assert.equal(m.calls.length,0);assert.equal(m.error.value,'默认值须为有效数字');m.stop()
})
await test('member write errors are captured in the screen rather than uncaught promise/alert',async()=>{
  const m=evaluate('Members',['patch','error','saving'],async()=>{throw Error('forbidden')});await m.patch({id:'u1'},{active:false});assert.equal(m.error.value,'forbidden');assert.equal(m.saving.value,false);m.stop()
})
console.log(`Passed ${count} module regression tests.`)

import assert from 'node:assert/strict'
import {readFile} from 'node:fs/promises'
import ts from 'typescript'
import * as Vue from 'vue'
import {parse, compileScript, compileTemplate} from 'vue/compiler-sfc'
import {workflow} from './workflow-test-support.mjs'

const read=path=>readFile(new URL('../'+path,import.meta.url),'utf8')
async function helper(path,imports={}){const exports={},source=await read(path);new Function('require','exports',ts.transpileModule(source,{compilerOptions:{module:ts.ModuleKind.CommonJS,target:ts.ScriptTarget.ES2022}}).outputText)(id=>imports[id],exports);return exports}
const fields=await helper('src/requirementFields.ts'),mentions=await helper('src/mentions.ts'),queries=await helper('src/workItemQuery.ts'),sprintPeople=await helper('src/sprintPeople.ts',{'./mentions':mentions})
const defectPeople=await helper('src/defectPeople.ts'),calendarDates=await helper('src/calendarDates.ts'),requirementPaging=await helper('src/requirementPaging.ts')
const flush=async()=>{for(let i=0;i<8;i++){await Promise.resolve();await Vue.nextTick()}}
const deferred=()=>{let resolve,reject;const promise=new Promise((yes,no)=>{resolve=yes;reject=no});return{resolve,reject,promise}}
const definitions=[
  {id:1,key:'草稿',name:'草稿',category:'todo',system:true},
  {id:2,key:'dev_a',name:'前端进行',category:'doing',system:false},
  {id:3,key:'dev_b',name:'后端进行',category:'doing',system:false},
  {id:4,key:'delivered',name:'客户验收完成',category:'done',system:false},
  {id:5,key:'cancelled_custom',name:'客户放弃',category:'cancelled',system:false},
  {id:6,key:'已完成',name:'已完成',category:'done',system:true},
].map((item,index)=>({...item,color:'#2563EB',sortOrder:index,enabled:true}))
const requirementRows=[['草稿','P1'],['dev_a','P1'],['dev_b','P1'],['dev_b','P2'],['delivered','P1'],['cancelled_custom','P1'],['已完成','P1']].map(([status,priority],index)=>({id:index+1,code:'REQ-'+(index+1),title:'User title '+(index+1),status,priority,objectType:'requirement',estimatedHours:10,actualHours:3,discipline:'frontend',customFields:{},createdAt:'2026-09-03T01:00:00Z'}))
const defectRows=[['新建','严重'],['修复中','严重'],['修复中','一般'],['待验证','严重'],['已关闭','一般'],['已拒绝','一般']].map(([status,severity],index)=>({id:21+index,code:'BUG-'+(index+21),title:'Bug '+(index+21),status,severity,priority:'P1',objectType:'defect',estimatedHours:10,actualHours:3,assigneeUserId:['front','admin','qa','back','admin','qa'][index],verifierUserId:['qa','qa','admin','admin','front','back'][index],customFields:{}}))
const weightSummary={totalWeight:12.5,requirementCount:7,estimatedCount:3,unestimatedCount:4,roles:{frontend:12.5}}
const mixedRows=[...requirementRows,defectRows[0],defectRows[4],defectRows[5]]
const detail=id=>({sprint:{id,name:'Sprint '+id,status:'进行中',capacity:80},items:structuredClone(mixedRows),weightSummary:structuredClone(weightSummary)})
const params=path=>new URL(path,'http://devflow.test').searchParams
function filtered(rows,path){const query=params(path),statuses=query.has('statuses')?JSON.parse(query.get('statuses')):[],category=query.get('statusCategory'),priority=query.get('priority'),severity=query.get('severity'),assigneeUserId=query.get('assigneeUserId'),verifierUserId=query.get('verifierUserId'),mine=query.get('mine')==='1',search=query.get('q')||'';return rows.filter(item=>(!statuses.length||statuses.includes(item.status))&&(!category||definitions.find(state=>state.key===item.status)?.category===category)&&(!priority||priority===item.priority)&&(!severity||severity===item.severity)&&(!assigneeUserId||assigneeUserId===item.assigneeUserId)&&(!verifierUserId||verifierUserId===item.verifierUserId)&&(!mine||item.assigneeUserId==='admin'||item.verifierUserId==='admin')&&(!search||item.title.includes(search)||item.code.includes(search)))}
async function defaults(path){
  // 模拟真实后端：先完整执行状态/优先级筛选，再分页，total 不能用当前页数量代替。
  if(path.startsWith('/requirements?')){const rows=filtered(requirementRows,path),query=params(path),pageSize=Number(query.get('pageSize')||30),page=Math.min(Number(query.get('page')||1),Math.max(1,Math.ceil(rows.length/pageSize)));return{items:structuredClone(rows.slice((page-1)*pageSize,page*pageSize)),total:rows.length,page,pageSize}}
  if(path.startsWith('/defects?'))return{items:structuredClone(filtered(defectRows,path))}
  if(path==='/requirement-statuses')return{items:structuredClone(definitions)}
  if(path==='/sprints')return{items:[{id:10,name:'Sprint 10',status:'进行中'},{id:11,name:'Sprint 11',status:'规划中'}]}
  if(path==='/sprints/backlog/items')return{items:[]}
  if(path==='/sprints/backlog/weights')return{weightSummary:{totalWeight:0,requirementCount:0,estimatedCount:0,unestimatedCount:0}}
  if(/^\/sprints\/\d+$/.test(path))return detail(Number(path.split('/').at(-1)))
  if(path==='/members')return{items:[{id:'admin',name:'Admin',active:true,isCurrent:true,projectRole:'tenant_admin'}]}
  if(path==='/session')return{user:{id:'admin',role:'tenant_admin'}}
  return{items:[]}
}
const exported={
  Requirements:['load','loadOptions','items','filters','selectedStatuses','statusSelection','clearFilters','setView','activeView','statusDefinitions','statusBoard','listView','loading','error','advancedFilters','cfFilters','page'],
  Sprints:['load','open','selected','statusDefinitions','listStatuses','filteredWorkItems','listRules','listSearch','listSort','listOrder','listRows','pagedRows','board','metrics','percent','statusStats','listPage','activeTab','loading','error','openBacklog','backlog'],
  Defects:['load','items','status','severity','assigneeUserId','verifierUserId','mine','q','view','board','loading','notice','clearFilters','toggleMine'],
}
async function view(name,handler=defaults){
  const source=(await read('src/views/'+name+'.vue')).match(/<script setup[^>]*>([\s\S]*?)<\/script>/)[1]
  const exports={},scope=Vue.effectScope(),mounts=[],unmounts=[],calls=[],timers=new Map(),storage=new Map([['devflow-project','p-a']]),window=new EventTarget(),route=Vue.reactive({query:{},params:{}}),locale=Vue.ref('zh-CN')
  let timerID=0,stopped=false
  const t=(value,params={})=>String(value).replace(/\{(\w+)\}/g,(all,key)=>String(params[key]??all))
  const imports={'../calendarDates':calendarDates,vue:{...Vue,onMounted:fn=>mounts.push(fn),onBeforeUnmount:fn=>unmounts.push(fn)},'vue-router':{useRoute:()=>route,useRouter:()=>({replace:async next=>{route.query=next.query||{}},push:async()=>{}}),onBeforeRouteLeave:()=>{},onBeforeRouteUpdate:()=>{}},'../api':{api:async(path,options)=>{calls.push({path,options});return handler(path,options)}},'../i18n':{t,locale,timezone:Vue.ref('UTC'),categoryLabel:value=>value,formatDate:value=>String(value||''),formatNumber:value=>String(value)},'../layoutScope':{useLayoutBoolean:(_key,fallback)=>Vue.ref(fallback)},'../requirementWorkflow':workflow,'../requirementFields':fields,'../mentions':mentions,'../workItemQuery':queries,'../sprintPeople':sprintPeople,'../sprintTabs':{useSprintTabs:()=>Vue.ref(['工作项列表','概览','看板','权重统计','团队跟踪','进度图','仪表盘'])}}
  imports['../defectPeople']=defectPeople
  imports['../requirementPaging']=requirementPaging
  imports['../components/settingsScope']={useSettingsScope:()=>({locked:Vue.ref(false),current:()=>true,request:imports['../api'].api,project:'test-project'})}
  const code=ts.transpileModule(source+'\nexport {'+exported[name].join(',')+'}',{compilerOptions:{module:ts.ModuleKind.CommonJS,target:ts.ScriptTarget.ES2022}}).outputText
  scope.run(()=>new Function('require','exports','window','localStorage','setTimeout','clearTimeout','defineProps','defineEmits',code)(id=>{if(id.endsWith('.vue'))return{};assert(id in imports,'Unexpected import '+id);return imports[id]},exports,window,{getItem:key=>storage.get(key)??null},fn=>{const id=++timerID;timers.set(id,fn);return id},id=>timers.delete(id),()=>({}),()=>()=>{}))
  return{...exports,calls,route,storage,locale,timers,mount:async()=>{for(const fn of mounts)await fn()},runTimers:async()=>{await flush();const pending=[...timers.values()];timers.clear();for(const fn of pending)fn();await flush()},stop:()=>{if(stopped)return;stopped=true;for(const fn of unmounts)fn();scope.stop();timers.clear()}}
}
const IDs=items=>items.map(item=>item.id).sort((a,b)=>a-b)
const boardIDs=board=>IDs(Array.isArray(board)?board.flatMap(column=>column.items):Object.values(board).flat())
let count=0
async function test(name,run){await run();count++;console.log('✓ '+name)}

await test('requirements send JSON statuses without legacy status and combine OR statuses with priority as AND',async()=>{
  const m=await view('Requirements');await m.loadOptions();m.statusSelection.value=['dev_a','dev_b'];m.filters.priority='P1';await m.runTimers()
  const path=m.calls.findLast(call=>call.path.startsWith('/requirements?')).path,query=params(path)
  assert.deepEqual(JSON.parse(query.get('statuses')),['dev_a','dev_b']);assert.equal(query.has('status'),false);assert.equal(query.get('priority'),'P1');assert.deepEqual(IDs(m.items.value),[2,3]);assert.deepEqual(boardIDs(m.statusBoard.value),[2,3]);assert.equal(m.calls.some(call=>call.options?.method),false);m.stop()
})
await test('clearing requirement status and advanced filters restores the full pool without stale query parameters',async()=>{
  const m=await view('Requirements');m.statusSelection.value=['dev_a'];m.filters.priority='P1';m.cfFilters.cost='5';m.advancedFilters.value=[{field:'priority',operator:'eq',value:'P1'}];await m.runTimers();m.clearFilters();await m.runTimers()
  const query=params(m.calls.findLast(call=>call.path.startsWith('/requirements?')).path)
  assert.equal(query.has('statuses'),false);assert.equal(query.get('status'),'');assert.equal(query.get('statusCategory'),'');assert.equal(query.get('priority'),'');assert.equal(query.has('filters'),false);assert.equal(query.has('cf.cost'),false);assert.deepEqual(m.selectedStatuses.value,[]);assert.equal(m.items.value.length,requirementRows.length);m.stop()
})
await test('completed requirement navigation queries category=done, includes custom done states and excludes cancelled states',async()=>{
  const m=await view('Requirements');await m.loadOptions();m.statusSelection.value=['dev_a','cancelled_custom'];m.setView('completed');await m.runTimers()
  const query=params(m.calls.findLast(call=>call.path.startsWith('/requirements?')).path)
  assert.equal(query.get('statusCategory'),'done');assert.equal(query.has('statuses'),false);assert.equal(m.activeView.value,'completed');assert.deepEqual(IDs(m.items.value),[5,7]);assert.deepEqual(boardIDs(m.statusBoard.value),[5,7])
  m.statusSelection.value=['cancelled_custom'];await m.runTimers();assert.equal(m.filters.statusCategory,'');assert.notEqual(m.activeView.value,'completed');assert.deepEqual(IDs(m.items.value),[6]);m.stop()
})
await test('requirements invalidate an old result immediately on filter change, even before the debounce request starts',async()=>{
  const old=deferred();let first=true;const m=await view('Requirements',path=>{if(path.startsWith('/requirements?')&&first){first=false;return old.promise}return defaults(path)})
  const pending=m.load();m.statusSelection.value=['dev_b'];await flush();old.resolve({items:[{id:999,status:'草稿'}]});await pending;assert.deepEqual(m.items.value,[])
  await m.runTimers();assert.deepEqual(IDs(m.items.value),[3,4]);assert.equal(m.loading.value,false);assert.equal(m.error.value,'');m.stop()
})
await test('late requirement errors and responses cannot replace the newest filtered board or survive unmount',async()=>{
  const old=deferred();let first=true;const m=await view('Requirements',path=>{if(path.startsWith('/requirements?')&&first){first=false;return old.promise}return defaults(path)})
  const pending=m.load();m.statusSelection.value=['dev_a'];await m.runTimers();old.reject(Error('old request failed'));await pending;assert.equal(m.error.value,'');assert.deepEqual(IDs(m.items.value),[2]);assert.deepEqual(boardIDs(m.statusBoard.value),[2]);m.stop()
  const later=deferred(),disposed=await view('Requirements',()=>later.promise),waiting=disposed.load();disposed.stop();later.resolve({items:[{id:999}]});await waiting;assert.deepEqual(disposed.items.value,[])
})
await test('iteration list and board share OR status selection while full metrics and weight summary remain unchanged',async()=>{
  const m=await view('Sprints');await m.load(10);const metrics=JSON.stringify(m.metrics.value),weights=JSON.stringify(m.selected.value.weightSummary),source=JSON.stringify(m.selected.value.items),stats=JSON.stringify(m.statusStats.value)
  m.listStatuses.value=['dev_a','dev_b'];await flush();assert.deepEqual(IDs(m.listRows.value),[2,3,4]);assert.deepEqual(boardIDs(m.board.value),[2,3,4]);assert.deepEqual(IDs(m.filteredWorkItems.value),[2,3,4])
  assert.equal(JSON.stringify(m.metrics.value),metrics);assert.equal(JSON.stringify(m.selected.value.weightSummary),weights);assert.equal(JSON.stringify(m.selected.value.items),source);assert.equal(JSON.stringify(m.statusStats.value),stats)
  m.listStatuses.value=[];await flush();assert.equal(m.listRows.value.length,mixedRows.length);assert.equal(boardIDs(m.board.value).length,mixedRows.length);m.stop()
})
await test('iteration cancelled requirements/rejected defects are terminal, not successful completions',async()=>{
  const m=await view('Sprints');await m.load(10);assert.equal(m.metrics.value.total,10);assert.equal(m.metrics.value.done,3);assert.equal(m.metrics.value.unfinished,5);assert.equal(m.percent.value,30)
  assert.deepEqual(IDs(m.board.value.已完成),[5,7,25]);assert.deepEqual(IDs(m.board.value.已终止),[6,26]);m.listStatuses.value=['cancelled_custom','已拒绝'];await flush();assert.deepEqual(boardIDs(m.board.value),[6,26]);assert.deepEqual(IDs(m.listRows.value),[6,26]);assert.equal(m.metrics.value.done,3);m.stop()
})
await test('iteration list keeps typed priority filters as AND with OR statuses and resets pagination',async()=>{
  const m=await view('Sprints');await m.load(10);m.listPage.value=5;m.listStatuses.value=['dev_a','dev_b'];m.listRules.value=[{field:'priority',operator:'eq',value:'P1'}];await flush();assert.deepEqual(IDs(m.listRows.value),[2,3]);assert.equal(m.listPage.value,1);assert.equal(m.metrics.value.total,10);m.stop()
})
await test('iteration list and board share status OR, priority AND, search and sorting without losing filters across tabs',async()=>{
  const m=await view('Sprints');await m.load(10);const metrics=JSON.stringify(m.metrics.value),weight=JSON.stringify(m.selected.value.weightSummary)
  m.listStatuses.value=['dev_a','dev_b'];m.listRules.value=[{field:'priority',operator:'eq',value:'P1'}];m.listSearch.value='User title 3';m.listSort.value='code';m.listOrder.value='desc';await flush()
  const saved=JSON.stringify({statuses:m.listStatuses.value,rules:m.listRules.value,search:m.listSearch.value,sort:m.listSort.value,order:m.listOrder.value})
  for(const tab of ['工作项列表','看板','概览','看板','工作项列表']){m.activeTab.value=tab;await flush();assert.deepEqual(IDs(m.listRows.value),[3]);assert.deepEqual(boardIDs(m.board.value),[3]);assert.equal(JSON.stringify({statuses:m.listStatuses.value,rules:m.listRules.value,search:m.listSearch.value,sort:m.listSort.value,order:m.listOrder.value}),saved)}
  m.listSearch.value='';await flush();assert.deepEqual(m.listRows.value.map(item=>item.id),[3,2]);assert.deepEqual(m.board.value.进行中.map(item=>item.id),[3,2]);assert.equal(JSON.stringify(m.metrics.value),metrics);assert.equal(JSON.stringify(m.selected.value.weightSummary),weight);m.stop()
})
await test('iteration has one visible shared filter toolbar for both views and column settings only in list view',async()=>{
  const source=await read('src/views/Sprints.vue'),descriptor=parse(source).descriptor,script=compileScript(descriptor,{id:'status-view-toolbar'}),template=compileTemplate({source:descriptor.template.content,filename:'Sprints.vue',id:'status-view-toolbar',compilerOptions:{bindingMetadata:script.bindings}})
  assert.deepEqual(template.errors,[])
  const nodes=[];function walk(node,parents=[]){if(node.type===1)nodes.push({node,parents});for(const child of node.children||[])walk(child,[...parents,node])}walk(descriptor.template.ast)
  const controls=nodes.filter(({node})=>node.tag==='WorkItemFilters');assert.equal(controls.length,1)
  const toolbar=controls[0].parents.find(node=>node.props?.some(prop=>prop.name==='class'&&prop.value?.content==='sprint-shared-tools'));assert(toolbar,'advanced conditions hidden outside shared toolbar')
  assert( toolbar.props.some(prop=>prop.name==='if'&&prop.exp?.content==="['工作项列表','看板'].includes(activeTab)") )
  for(const tag of ['StatusMultiSelect','WorkItemColumns']){const matches=nodes.filter(({node})=>node.tag===tag);assert.equal(matches.length,1);assert(matches[0].parents.includes(toolbar))}
  const columns=nodes.find(({node})=>node.tag==='WorkItemColumns').node;assert(columns.props.some(prop=>prop.name==='if'&&prop.exp?.content==="activeTab==='工作项列表'"))
  assert.equal((source.match(/shown:listRows\.length/g)||[]).length,1);assert(!source.includes('shown:filteredWorkItems.length'));assert(!source.includes('sprint-list-scope'))
})
await test('quick iteration switch and backlog navigation reject old detail responses without mutating the selected source',async()=>{
  const first=deferred(),second=deferred(),m=await view('Sprints',path=>path==='/sprints/10'?first.promise:path==='/sprints/11'?second.promise:defaults(path))
  m.statusDefinitions.value=definitions;m.listStatuses.value=['dev_a'];const a=m.open(10),b=m.open(11);second.resolve({...detail(11),items:[{...requirementRows[1],id:111}]});await b;first.resolve(detail(10));await a;assert.equal(m.selected.value.sprint.id,11);assert.deepEqual(IDs(m.listRows.value),[111]);assert.equal(m.loading.value,false);m.stop()
  const late=deferred(),backlog=await view('Sprints',path=>path==='/sprints/10'?late.promise:defaults(path));backlog.backlog.value=[requirementRows[0]];const opening=backlog.open(10);backlog.openBacklog();late.resolve(detail(10));await opening;assert.equal(backlog.selected.value.backlog,true);assert.deepEqual(IDs(backlog.selected.value.items),[1]);backlog.stop()
})
await test('defects serialize JSON statuses and combine severity/search filters without sending a legacy status',async()=>{
  const m=await view('Defects');m.status.value=['新建','修复中'];m.severity.value='严重';m.q.value='Bug';await m.runTimers()
  const query=params(m.calls.findLast(call=>call.path.startsWith('/defects?')).path);assert.deepEqual(JSON.parse(query.get('statuses')),['新建','修复中']);assert.equal(query.has('status'),false);assert.equal(query.get('severity'),'严重');assert.equal(query.get('q'),'Bug');assert.deepEqual(IDs(m.items.value),[21,22]);assert.deepEqual(boardIDs(m.board.value),[21,22]);assert.deepEqual(m.board.value.map(column=>column.value),['新建','修复中']);m.stop()
})
await test('defects send stable owner/verifier IDs and the current-user shortcut clears person filters',async()=>{
  const m=await view('Defects');m.assigneeUserId.value='front';await m.runTimers();let query=params(m.calls.findLast(call=>call.path.startsWith('/defects?')).path);assert.equal(query.get('assigneeUserId'),'front');assert.equal(query.has('assignee'),false);assert.deepEqual(IDs(m.items.value),[21])
  m.assigneeUserId.value='';m.verifierUserId.value='admin';await m.runTimers();query=params(m.calls.findLast(call=>call.path.startsWith('/defects?')).path);assert.equal(query.get('verifierUserId'),'admin');assert.equal(query.has('verifier'),false);assert.deepEqual(IDs(m.items.value),[23,24])
  m.assigneeUserId.value='front';m.verifierUserId.value='qa';m.toggleMine();await m.runTimers();query=params(m.calls.findLast(call=>call.path.startsWith('/defects?')).path);assert.equal(query.get('mine'),'1');assert.equal(query.has('assigneeUserId'),false);assert.equal(query.has('verifierUserId'),false);assert.deepEqual(IDs(m.items.value),[22,23,24,25]);m.clearFilters();await m.runTimers();query=params(m.calls.findLast(call=>call.path.startsWith('/defects?')).path);assert.equal(query.has('mine'),false);assert.deepEqual(IDs(m.items.value),IDs(defectRows));m.stop()
})
await test('clearing defect statuses restores all statuses and list/board use identical backend-filtered records',async()=>{
  const m=await view('Defects');m.status.value=['修复中'];await m.runTimers();assert.deepEqual(IDs(m.items.value),[22,23]);m.status.value=[];await m.runTimers();const query=params(m.calls.findLast(call=>call.path.startsWith('/defects?')).path);assert.equal(query.has('statuses'),false);assert.deepEqual(IDs(m.items.value),IDs(defectRows));assert.deepEqual(boardIDs(m.board.value),IDs(defectRows));m.stop()
})
await test('defect rapid multi-select changes discard old requests before debounce and preserve latest board against old errors',async()=>{
  const old=deferred();let first=true;const m=await view('Defects',path=>{if(path.startsWith('/defects?')&&first){first=false;return old.promise}return defaults(path)})
  const pending=m.load();m.status.value=['修复中'];await flush();m.status.value=['新建','待验证'];await flush();old.resolve({items:[{id:999,status:'已关闭'}]});await pending;assert.deepEqual(m.items.value,[]);await m.runTimers();assert.deepEqual(IDs(m.items.value),[21,24]);assert.deepEqual(boardIDs(m.board.value),[21,24]);assert.equal(m.loading.value,false);m.stop()
  const stale=deferred();first=true;const errors=await view('Defects',path=>{if(path.startsWith('/defects?')&&first){first=false;return stale.promise}return defaults(path)});const waiting=errors.load();errors.status.value=['新建'];await errors.runTimers();stale.reject(Error('obsolete'));await waiting;assert.equal(errors.notice.value,'');assert.deepEqual(IDs(errors.items.value),[21]);errors.stop()
})
console.log(`Passed ${count} status-view integration tests.`)

import assert from 'node:assert/strict'
import {readFileSync} from 'node:fs'
import ts from 'typescript'
import * as Vue from 'vue'
import {parse,compileScript,compileTemplate,compileStyle} from 'vue/compiler-sfc'
import {createWorkspaceHarness} from './helpers/workspace-harness.mjs'
const source=readFileSync(new URL('../src/workloadIterations.ts',import.meta.url),'utf8'),exports={}
new Function('exports',ts.transpileModule(source,{compilerOptions:{module:ts.ModuleKind.CommonJS,target:ts.ScriptTarget.ES2022}}).outputText)(exports)
const {validIterationReport,iterationSummaries,iterationSparkline}=exports
const point=(sprintId,weight=2,missing=0)=>({sprintId,hasData:true,weight,deliveredWeight:weight/2,requirementCount:1,shippedRequirementCount:1,defectCount:0,estimatedRoleCount:missing?0:1,unestimatedRoleCount:missing})
const person=(id,weights=[2,2,2,4])=>({userId:id,name:id,active:true,departmentId:'d',departmentName:'Team',roles:[{role:'frontend',points:weights.map((v,i)=>point(i+1,v))}]})
const report={scope:'organization',tenantId:'t',userId:'admin',month:'2020-01',count:4,project:'',asOf:'2020-01-31',snapshot:true,generatedAt:'now',iterations:[1,2,3,4].map(id=>({id,name:'Sprint '+id,endDate:'2020-01-01',projectId:'p',projectName:'Project'})),people:[person('a'),person('b'),person('c',[1,1,1,1])],ambiguousSprintItemCount:0,unassignedRequirementCount:0}
const expected={scope:'organization',tenantId:'t',userId:'admin',month:'2020-01',count:4,project:''}
assert(validIterationReport(report,expected))
for(const alter of [r=>r.tenantId='other',r=>r.userId='other',r=>r.iterations.push(r.iterations[0]),r=>r.people.push(r.people[0]),r=>r.people[0].roles[0].points[0].weight=-1,r=>r.people[0].roles[0].points[0].deliveredWeight=999,r=>r.people[0].roles[0].points[0].sprintId=99]){const r=structuredClone(report);alter(r);assert(!validIterationReport(r,expected))}
const personal={...report,scope:'personal'};assert(!validIterationReport(personal,{...expected,scope:'personal'}))
let rows=iterationSummaries(report,'frontend');assert.deepEqual(rows.map(r=>r.rank),[1,1,2]);assert.equal(rows[0].change,100);assert(rows[0].alerts.includes('近期权重明显上升，建议核对负荷'));assert.equal(rows[0].completion,50)
assert(!iterationSummaries(report,'frontend',150)[0].alerts.includes('近期权重明显上升，建议核对负荷'))
assert(iterationSummaries(report,'frontend')[0].alerts.includes('已结束迭代仍有未完成权重，请核对状态'))
const missing=structuredClone(report);missing.people[0].roles[0].points[1]=point(2,0,1);rows=iterationSummaries(missing,'frontend');assert.equal(rows.find(r=>r.person.userId==='a').rank,null)
const inactive=structuredClone(report);inactive.people[0].active=false;assert.equal(iterationSummaries(inactive,'frontend').find(r=>r.person.userId==='a').rank,null)
const tiny=structuredClone(report);tiny.people[0].roles[0].points=tiny.people[0].roles[0].points.map((p,i)=>i?{...p,hasData:false,estimatedRoleCount:0,weight:0,deliveredWeight:0}:p);assert.equal(iterationSummaries(tiny,'frontend').find(r=>r.person.userId==='a').rank,null)
const zero=structuredClone(report);zero.people=[person('zero',[0,0,0,0])];rows=iterationSummaries(zero,'frontend');assert.equal(rows[0].change,null);assert.equal(rows[0].completion,null);assert.equal(rows[0].rank,1)
const dots=iterationSparkline([point(1,0),{...point(2),hasData:false},point(3,3),point(4,0,1),point(5,5)]);assert.equal(dots.length,3);assert(dots.every(d=>d.path.startsWith('M')));assert.equal(dots[0].y,65)
assert.equal(iterationSummaries(report,'backend').length,0)
console.log('Passed iteration analysis: scope validation, role-only dense ties, no-data and incomplete exclusion, workload alerts, zero baselines and chart gaps.')

const componentSource=readFileSync(new URL('../src/components/WorkloadIterations.vue',import.meta.url),'utf8'),descriptor=parse(componentSource).descriptor
const script=compileScript(descriptor,{id:'iteration-test'})
assert.deepEqual(compileTemplate({source:descriptor.template.content,filename:'WorkloadIterations.vue',id:'iteration-test',compilerOptions:{bindingMetadata:script.bindings}}).errors,[])
assert.deepEqual(compileStyle({source:descriptor.styles[0].content,filename:'WorkloadIterations.vue',id:'iteration-test',scoped:true}).errors,[])
const flush=async()=>{for(let i=0;i<20;i++){await Promise.resolve();await Vue.nextTick()}}
const deferred=()=>{let resolve;const promise=new Promise(r=>resolve=r);return {promise,resolve}}
function setup(handler,admin=true){
 const store=createWorkspaceHarness(),scope=Vue.effectScope(),calls=[],events=new EventTarget(),hooks=[],props=Vue.reactive({month:'2020-01',refreshKey:0,disabled:false})
 store.workspace.acceptContext({session:{tenant:{id:'t',name:'Org'},user:{id:'admin',name:'Admin',role:admin?'tenant_admin':'member'},project:{id:'p',name:'P',code:'P'}},projects:[],unread:0})
 const imports={vue:{...Vue,onMounted:()=>{},onBeforeUnmount:fn=>hooks.push(fn)},'../api':{api:async(path,options)=>{calls.push({path,options});return handler(path,options)}},'../i18n':{t:x=>x,formatNumber:String,formatDate:String},'../stores/workspace':store.imports['./stores/workspace'],'../workload':{workloadRoleNames:{frontend:'前端',backend:'后端'}},'../workloadIterations':exports}
 const js=ts.transpileModule(descriptor.scriptSetup.content+'\nexport {mode,count,report,load,role,department,search,rows,noData,expanded,loading,error};',{compilerOptions:{module:ts.ModuleKind.CommonJS,target:ts.ScriptTarget.ES2022}}).outputText,out={}
 scope.run(()=>new Function('require','exports','defineProps','window',js)(id=>imports[id]||{},out,()=>props,events))
 return {...out,props,workspace:store.workspace,calls,stop(){hooks.forEach(fn=>fn());scope.stop();store.stop()}}
}
const respond=path=>{const q=new URL(path,'http://test').searchParams;return {...structuredClone(report),scope:q.get('scope'),count:Number(q.get('count')),month:q.get('month'),project:q.get('project'),people:q.get('scope')==='personal'?[person('admin')]:structuredClone(report.people)}}
const m=setup(respond);await m.load();assert.equal(m.report.value.people.length,1);assert(m.calls.every(c=>c.path.includes('scope=personal')))
m.mode.value='organization';await flush();assert.equal(m.report.value.people.length,3);const before=m.calls.length;m.role.value='backend';m.search.value='nobody';await flush();assert.equal(m.calls.length,before);assert.equal(m.rows.value.length,0);m.stop()
const ordinary=setup(respond,false);ordinary.mode.value='organization';await flush();assert.equal(ordinary.mode.value,'personal');assert(ordinary.calls.every(c=>!c.path.includes('scope=organization')));ordinary.stop()
const pending=deferred(),late=setup(()=>pending.promise);void late.load();const signal=late.calls[0].options.signal;late.props.disabled=true;await flush();assert(signal.aborted);pending.resolve(respond('/x?scope=personal&month=2020-01&count=4&project='));await flush();assert.equal(late.report.value,null);late.stop()
const wrong=setup(path=>({...respond(path),tenantId:'foreign'}));await wrong.load();assert.equal(wrong.report.value,null);assert(wrong.error.value);wrong.stop()
const recovery=setup(respond);await recovery.load();recovery.workspace.markIdentityConflict(false,'Changed');await flush();assert.equal(recovery.report.value,null);recovery.stop()
console.log('Passed iteration component: template/style compilation, personal default, organization permission gate, local filters, stale-response cancellation, tenant validation and identity clearing.')

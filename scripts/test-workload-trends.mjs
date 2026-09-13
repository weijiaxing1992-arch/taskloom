import assert from 'node:assert/strict'
import {readFileSync} from 'node:fs'
import ts from 'typescript'
import * as Vue from 'vue'
import {parse,compileScript,compileTemplate,compileStyle} from 'vue/compiler-sfc'
import {createWorkspaceHarness} from './helpers/workspace-harness.mjs'
const read=path=>readFileSync(new URL('../'+path,import.meta.url),'utf8')
const evaluate=(source,imports={},globals={})=>{const exports={};new Function('require','exports',...Object.keys(globals),ts.transpileModule(source,{compilerOptions:{module:ts.ModuleKind.CommonJS,target:ts.ScriptTarget.ES2022}}).outputText)(id=>{assert(id in imports,'Unexpected import '+id);return imports[id]},exports,...Object.values(globals));return exports}
const helpers=evaluate(read('src/workloadTrends.ts')),workload=evaluate(read('src/workload.ts'))
const flush=async()=>{for(let i=0;i<20;i++){await Promise.resolve();await Vue.nextTick()}}
const deferred=()=>{let resolve,reject;const promise=new Promise((a,b)=>{resolve=a;reject=b});return{promise,resolve,reject}}
const metric=(weight=0,estimated=1)=>({weight,requirementCount:1,shippedRequirementCount:1,defectCount:0,estimatedRoleCount:estimated,unestimatedRoleCount:estimated?0:1})
const point=(key,value=0,estimated=1,hasData=true)=>({key,month:key,sprintCount:1,hasData,metrics:metric(value,estimated)})
function result(path='/reports/workload/trends?month=2050-02&months=6'){
 const q=new URL(path,'http://test').searchParams,month=q.get('month'),months=Number(q.get('months')||6)
 return {month,months,fromMonth:'2049-09',snapshot:true,generatedAt:'2050-03-01T00:00:00Z',precision:6,scope:{type:'organization',tenantId:'tenant',tenantName:'Original 企业',projectCount:2},filters:{department:q.has('department')?q.get('department'):'*',user:q.get('user')||'',role:q.get('role')||''},monthly:[point(month,30)],iterations:[{...point('p-a:1',10),month,sprintId:1,name:'Original 版本',projectId:'p-a',projectName:'Project A',endDate:'2050-02-01'},{...point('p-b:2',20),month,sprintId:2,name:'Original 版本',projectId:'p-b',projectName:'Project B',endDate:'2050-02-02'}],options:{people:[{userId:'u-a',name:'User 原文',departmentId:'d-a',departmentName:'Department 原文'}],departments:[{id:'d-a',name:'Department 原文'}]},ambiguousSprintItemCount:0}
}
class Element{constructor(tag,text=''){Object.assign(this,{tag,text,props:{},children:[],parent:null})}}
const renderer=Vue.createRenderer({createElement:tag=>new Element(tag),createText:text=>new Element('#text',text),createComment:()=>new Element('#comment'),insert(child,parent,anchor){child.parent=parent;const i=anchor?parent.children.indexOf(anchor):-1;if(i<0)parent.children.push(child);else parent.children.splice(i,0,child)},remove(child){if(child.parent)child.parent.children.splice(child.parent.children.indexOf(child),1)},setText:(node,text)=>{node.text=text},setElementText:(node,text)=>{node.text=text;node.children=[]},parentNode:node=>node.parent,nextSibling:node=>node.parent?.children[node.parent.children.indexOf(node)+1]||null,patchProp:(node,key,_old,value)=>{node.props[key]=value}})
const descendants=node=>[node,...node.children.flatMap(descendants)],text=node=>node.text+node.children.map(text).join('')
async function mount(handler=path=>result(path)){
 const store=createWorkspaceHarness(),calls=[],events=new EventTarget(),timers=new Map(),props=Vue.reactive({month:'2050-02',refreshKey:1,disabled:false}),locale=Vue.ref('zh-CN');let timer=0
 store.workspace.acceptContext({session:{tenant:{id:'tenant',name:'Org'},user:{id:'admin',name:'Admin',role:'tenant_admin'},project:{id:'p',name:'P',code:'P'}},projects:[],unread:0})
 const Button=Vue.defineComponent({setup:(_p,{attrs,slots})=>()=>Vue.h('button',attrs,slots.default?.())})
 const AppSelect=Vue.defineComponent({props:['modelValue','options','disabled','label'],emits:['update:modelValue'],setup:(props,{emit})=>()=>Vue.h('select',{value:props.modelValue,disabled:props.disabled,'aria-label':props.label,onChange:event=>emit('update:modelValue',event.target.value)},(props.options||[]).map(option=>Vue.h('option',{value:option.value},option.label)))})
 const MemberMultiSelect=Vue.defineComponent({props:['modelValue','members','single','disabled','label'],emits:['update:modelValue'],setup:(props,{emit})=>()=>Vue.h('div',{'data-member-picker':true,'data-single':props.single},props.members.map(member=>Vue.h('button',{'data-member-id':member.id,disabled:props.disabled,onClick:()=>emit('update:modelValue',[member.id])},member.name)))})
 const imports={vue:{...Vue,withDirectives:value=>value},'../api':{api:async(path,options)=>{calls.push({path,options});return handler(path,options)}},'../i18n':{t:(source,params={})=>String(source).replace(/\{(\w+)\}/g,(all,key)=>String(params[key]??all)),locale,formatNumber:x=>String(x),formatDate:x=>String(x)},'../stores/workspace':store.imports['./stores/workspace'],'../workload':workload,'../workloadTrends':helpers,'./ui/button':{Button}}
 imports['./MemberMultiSelect.vue']={default:MemberMultiSelect}
 imports['./AppSelect.vue']={default:AppSelect}
 const descriptor=parse(read('src/components/WorkloadTrends.vue')).descriptor,script=compileScript(descriptor,{id:'workload-trend-test'}),template=compileTemplate({source:descriptor.template.content,filename:'WorkloadTrends.vue',id:'workload-trend-test',compilerOptions:{bindingMetadata:script.bindings}});assert.deepEqual(template.errors,[])
 const component=evaluate(script.content,imports,{window:events,setTimeout:fn=>{const id=++timer;timers.set(id,fn);return id},clearTimeout:id=>timers.delete(id)}).default;component.render=evaluate(template.code,imports).render
 const root=new Element('root'),app=renderer.createApp({render:()=>Vue.h(component,props)});app.mount(root);await flush()
 return{store,workspace:store.workspace,calls,events,props,locale,root,s:app._instance.subTree.component.setupState,tick:async()=>{const pending=[...timers.values()];timers.clear();pending.forEach(fn=>fn());await flush()},stop(){app.unmount();store.stop()}}
}
let count=0;async function test(name,run){await run();count++;console.log('✓ '+name)}
await test('chart distinguishes missing work, unestimated weights and an explicit zero without connecting gaps',()=>{
 const rows=[point('missing',0,0,false),point('zero',0),point('filled',2),point('unset',0,0),point('later',3)]
 assert.deepEqual(rows.map(x=>helpers.trendMetricValue(x,'weight')),[null,0,2,null,3]);const g=helpers.trendGeometry(rows,'weight');assert.equal(g.paths.length,2);assert.equal(g.dots[1].y,g.height-g.bottom);assert.equal(g.dots[0].y,null);assert(g.dots.every(x=>Number.isFinite(x.x)&&(x.y===null||Number.isFinite(x.y))))
 assert.equal(helpers.trendMetricValue(rows[3],'shippedRequirementCount'),1);assert.equal(helpers.trendGeometry([],'weight').paths.length,0)
})
await test('response checks fail closed for wrong month, tenant, filters, duplicate points and invalid numeric data',()=>{
 const source=result(),expected={month:'2050-02',months:6,tenant:'tenant',filters:{department:'*',user:'',role:''}};assert(helpers.validTrendReport(source,expected))
 for(const alter of [x=>x.month='2050-03',x=>x.months=12,x=>x.scope.tenantId='foreign',x=>x.filters.user='other',x=>x.monthly.push({...x.monthly[0]}),x=>x.monthly[0].metrics.weight=NaN,x=>x.snapshot=false]){const data=structuredClone(source);alter(data);assert(!helpers.validTrendReport(data,expected))}
})
await test('mounted trends use authenticated company context and render an accessible graph and exact table',async()=>{
 const m=await mount();assert.equal(m.calls.length,1);assert.equal(m.s.report.scope.tenantId,'tenant');const query=new URL(m.calls[0].path,'http://test').searchParams;assert.equal(query.get('months'),'6');assert.equal(query.get('department'),'*')
 const nodes=descendants(m.root),svg=nodes.find(x=>x.tag==='svg');assert(svg.props['aria-label'].includes('已填工作权重'));assert(nodes.some(x=>x.tag==='details'));assert(nodes.some(x=>x.tag==='table'));const circle=nodes.find(x=>x.tag==='circle');assert.equal(String(circle.props.tabindex),'0');circle.props.onFocus();await flush();assert(text(m.root).includes('2050-02 · 已填工作权重: 30'));m.stop()
})
await test('metric and iteration grouping reuse the same data without new API reads or changing original names',async()=>{
 const m=await mount();m.s.mode='iterations';m.s.metric='defectCount';await flush();assert.equal(m.calls.length,1);assert.equal(m.s.points.length,2);assert.notEqual(m.s.points[0].key,m.s.points[1].key);assert(text(m.root).includes('Original 版本'));assert(text(m.root).includes('Project A'));assert(text(m.root).includes('Project B'));m.stop()
})
await test('role, department and member filters use stable IDs and share one debounced request',async()=>{
 const m=await mount();m.s.role='frontend';m.s.department='d-a';m.s.member='u-a';assert.equal(m.s.report,null);await m.tick();assert.equal(m.calls.length,2)
 const q=new URL(m.calls[1].path,'http://test').searchParams;assert.equal(q.get('role'),'frontend');assert.equal(q.get('department'),'d-a');assert.equal(q.get('user'),'u-a');assert.equal(m.s.report.filters.user,'u-a');m.stop()
})
await test('changing the month cancels old responses immediately, and refresh triggers a scoped reload',async()=>{
 const pending=deferred();let first=true;const m=await mount(path=>{if(first){first=false;return pending.promise}return result(path)});const signal=m.calls[0].options.signal;m.props.month='2050-03';await flush();assert(signal.aborted);await m.tick();assert.equal(m.s.report.month,'2050-03');pending.resolve(result());await flush();assert.equal(m.s.report.month,'2050-03');m.props.refreshKey++;await flush();await m.tick();assert.equal(m.calls.length,3);m.stop()
})
await test('shared single member filter includes authorized historical members without turning it into an assignment',async()=>{
 const m=await mount(path=>{const data=result(path);data.options.people[0].active=false;return data})
 assert.equal(m.s.options.people[0].active,false);assert.equal(m.s.filterMembers[0].active,true)
 const button=descendants(m.root).find(node=>node.props['data-member-id']==='u-a');assert(button);button.props.onClick();await m.tick();assert.equal(m.s.report.filters.user,'u-a');assert(m.calls.every(call=>!call.options?.method));assert.equal(m.s.options.people[0].active,false);m.stop()
})
await test('failed loads leave no misleading zero chart and retry recovers without losing selected filters',async()=>{
 let fail=true;const m=await mount(path=>{if(fail)throw Error('Permission revoked');return result(path)});assert.equal(m.s.report,null);assert.equal(m.s.loading,false);assert.equal(m.s.error,'Permission revoked');assert(descendants(m.root).some(x=>x.props.role==='alert'));fail=false;m.s.role='backend';await m.tick();assert.equal(m.s.report.filters.role,'backend');assert.equal(m.s.error,'');m.stop()
})
await test('identity conflicts and unmount cancel pending work and reject another tenant report',async()=>{
 const pending=deferred(),m=await mount(()=>pending.promise);const signal=m.calls[0].options.signal;m.workspace.markIdentityConflict(false,'Changed');await flush();assert(signal.aborted);pending.resolve(result());await flush();assert.equal(m.s.report,null);assert.equal(m.s.options.people.length,0);m.stop()
 const wrong=await mount(path=>({...result(path),scope:{type:'organization',tenantId:'other'}}));assert.equal(wrong.s.report,null);assert.match(wrong.s.error,/上下文/);wrong.stop()
 const late=deferred(),gone=await mount(()=>late.promise),state=gone.s;gone.stop();late.resolve(result());await flush();assert.equal(state.report,null)
})
await test('large iteration sets paginate only the chart while retaining every exact row',async()=>{
 const m=await mount(path=>{const data=result(path);data.iterations=Array.from({length:29},(_,i)=>({...data.iterations[0],key:'p:'+i,sprintId:i+1,name:'Iteration '+i}));return data});m.s.mode='iterations';await flush();assert.equal(m.s.pages,3);assert.equal(m.s.visiblePoints.length,12);m.s.page=3;await flush();assert.equal(m.s.visiblePoints.length,5);assert.equal(m.s.points.length,29);assert.equal(descendants(m.root).filter(x=>x.tag==='tbody').flatMap(x=>x.children).filter(x=>x.tag==='tr').length,29);m.stop()
})
await test('component stylesheet keeps mobile controls and chart/table independently scrollable in both themes',()=>{
 const source=read('src/components/WorkloadTrends.vue'),descriptor=parse(source).descriptor,style=compileStyle({source:descriptor.styles[0].content,filename:'WorkloadTrends.vue',id:'trend-css',scoped:true});assert.deepEqual(style.errors,[]);assert.match(source,/overflow-x:auto/);assert.match(source,/max-height:360px/);assert.match(source,/@media\(max-width:760px\)/);assert.match(source,/var\(--surface\)/);assert.match(source,/var\(--primary\)/);assert.match(source,/min-height:40px/);assert.match(source,/MemberMultiSelect compact single/)
 const view=read('src/views/Workload.vue');assert.match(view,/<DatePicker[^>]*mode="month"/);assert.match(view,/<WorkloadTrends[^>]*:month="month"[^>]*:refresh-key="trendRefresh"/)
})
console.log(`Passed ${count} workload trend regressions.`)

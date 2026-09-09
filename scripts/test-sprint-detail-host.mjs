import assert from 'node:assert/strict'
import {readFile} from 'node:fs/promises'
import ts from 'typescript'
import * as Vue from 'vue'
import * as Router from 'vue-router'
import {parse,compileScript,compileTemplate,compileStyle} from 'vue/compiler-sfc'
import {workflow} from './workflow-test-support.mjs'

const read=path=>readFile(new URL('../'+path,import.meta.url),'utf8')
const transpile=source=>ts.transpileModule(source,{compilerOptions:{module:ts.ModuleKind.CommonJS,target:ts.ScriptTarget.ES2022}}).outputText
const evaluate=(source,imports={},globals={})=>{const exports={};new Function('require','exports',...Object.keys(globals),transpile(source))(id=>imports[id],exports,...Object.values(globals));return exports}
const fields=evaluate(await read('src/requirementFields.ts')),mentions=evaluate(await read('src/mentions.ts')),queries=evaluate(await read('src/workItemQuery.ts')),people=evaluate(await read('src/sprintPeople.ts'),{'./mentions':mentions})
const tabsSource=await read('src/sprintTabs.ts'),calendarDates=evaluate(await read('src/calendarDates.ts'))
const flush=async()=>{for(let i=0;i<35;i++){await Promise.resolve();await Vue.nextTick()}}
const deferred=()=>{let resolve,reject;const promise=new Promise((a,b)=>{resolve=a;reject=b});return{resolve,reject,promise}}
class Element {constructor(tag,text=''){Object.assign(this,{tag,text,props:{},children:[],parent:null,isConnected:true,focused:false})}focus(options){this.focused=true;this.focusOptions=options}checkValidity(){return true}reportValidity(){return true}contains(value){return this===value||this.children.some(child=>child.contains(value))}closest(selector){return selector.includes('inert')&&this.props.inert?this:this.parent?.closest(selector)||null}getClientRects(){return[{}]}querySelectorAll(){return this.children.flatMap(descendants).filter(node=>['button','input','select','textarea'].includes(node.tag)&&!node.props.disabled)}}
const renderer=Vue.createRenderer({createElement:tag=>new Element(tag),createText:text=>new Element('#text',text),createComment:()=>new Element('#comment'),insert(child,parent,anchor){if(child.parent){const i=child.parent.children.indexOf(child);if(i>=0)child.parent.children.splice(i,1)}child.parent=parent;const i=anchor?parent.children.indexOf(anchor):-1;if(i<0)parent.children.push(child);else parent.children.splice(i,0,child)},remove(child){child.isConnected=false;if(child.parent)child.parent.children.splice(child.parent.children.indexOf(child),1)},setText:(child,text)=>{child.text=text},setElementText:(child,text)=>{child.text=text;child.children=[]},parentNode:child=>child.parent,nextSibling:child=>child.parent?.children[child.parent.children.indexOf(child)+1]||null,patchProp:(child,key,_old,value)=>{child.props[key]=value}})
const descendants=node=>[node,...node.children.flatMap(descendants)]
const text=node=>node.text+node.children.map(text).join('')
function componentNode(node,name){if(node?.component&&(node.type.name===name||node.type.__name===name))return node;if(node?.component)return componentNode(node.component.subTree,name);if(Array.isArray(node?.children))for(const child of node.children){const found=componentNode(child,name);if(found)return found}return null}
const sources=Object.fromEntries(await Promise.all(['Sprints','Requirements','Editor'].map(async name=>[name,await read('src/views/'+name+'.vue')])));for(const name of ['SprintTabs','RequirementLinks','DefectComposer'])sources[name]=await read('src/components/'+name+'.vue')
const settingsSource=await read('src/components/settingsScope.ts'),defectPeople=evaluate(await read('src/defectPeople.ts'))
const initialReq=id=>({id,code:'REQ-'+id,title:'Requirement '+id,description:'Original body',descriptionDoc:null,descriptionMentionUserIds:[],descriptionMentionNames:{},remarks:'',remarksMentionUserIds:[],remarksMentionNames:{},roleWeights:fields.emptyRoleWeights(),customFields:{},tags:'',tagColors:{},status:'草稿',type:'产品需求',category:'未分类',sprint:'123',createdAt:'2026-09-03T01:02:03Z',assigneeUserIds:[],assignees:[],ownerUserIds:[],owners:[],estimatedHours:3,actualHours:0,progress:0,discipline:'product'})
async function mount({query={},override=()=>undefined,layoutStorage=new Map()}={}){
  const storage=layoutStorage,writes=[],layoutScope=Vue.ref('tenant:user'),scope=Vue.effectScope(),events=[],calls=[],window=new EventTarget(),document={activeElement:new Element('button')},records=new Map([[17,initialReq(17)],[18,initialReq(18)]]),config={confirm:false}
  storage.set('devflow-project','p-a')
  window.confirm=()=>config.confirm
  const localStorage={getItem:key=>storage.get(key)??null,setItem:(key,value)=>{storage.set(key,value);writes.push({key,value})}}
  const tabs=evaluate(tabsSource,{vue:Vue,'./layoutScope':{layoutScope}},{localStorage})
  const sprint=id=>({id,name:id===1?'123':'22',code:'SPR-'+id,status:'进行中',capacity:80,total:records.size,done:0})
  const detail=id=>({sprint:sprint(id),items:[...records.values()].filter(item=>item.sprint===sprint(id).name).map(item=>({...structuredClone(item),objectType:'requirement'})),weightSummary:{totalWeight:[...records.values()].filter(item=>item.sprint===sprint(id).name).reduce((sum,item)=>sum+fields.weightTotal(item.roleWeights),0),requirementCount:[...records.values()].filter(item=>item.sprint===sprint(id).name).length}})
  const api=async(path,options)=>{
    calls.push({path,options});const result=override(path,options);if(result!==undefined)return result
    if(/^\/requirements\/\d+$/.test(path)){const id=Number(path.split('/').at(-1));if(options?.method==='PATCH'){records.set(id,{...records.get(id),...JSON.parse(options.body),id});return structuredClone(records.get(id))}return structuredClone(records.get(id))}
    if(path==='/sprints')return{items:[sprint(1),sprint(2)]}
    if(/^\/sprints\/\d+$/.test(path))return detail(Number(path.split('/').at(-1)))
    if(path==='/sprints/backlog/items')return{items:[...records.values()].filter(item=>item.sprint==='待规划').map(item=>({...structuredClone(item),objectType:'requirement'}))}
    if(path==='/sprints/backlog/weights')return{weightSummary:{totalWeight:0,requirementCount:[...records.values()].filter(item=>item.sprint==='待规划').length}}
    if(path==='/session')return{user:{id:'u_me',role:'product'},project:{id:'p-a'},tenant:{id:'tenant'}}
    if(path==='/members')return{items:[{id:'u_me',name:'Product',projectRole:'product',active:true,isCurrent:true}]}
    if(path==='/requirements'||path.startsWith('/requirements?'))return{items:structuredClone([...records.values()])}
    if(path==='/defects'&&options?.method==='POST')return{id:71,code:'BUG-71',...JSON.parse(options.body)}
    if(/^\/requirements\/\d+\/links$/.test(path))return{items:[initialReq(Number(path.split('/')[2])===17?18:17)]}
    if(path==='/requirement-categories')return{items:[{id:1,name:'未分类'}]}
    if(path==='/requirement-statuses')return{items:[{id:1,key:'草稿',name:'草稿',category:'todo',enabled:true,system:true,color:'#1677ff'}]}
    if(path==='/requirement-workflow')return{initialStatus:'草稿'}
    if(path.startsWith('/preferences/requirement-list'))return{columns:['code','title','status']}
    return{items:[]}
  }
  const components={},stub=name=>Vue.defineComponent({name,inheritAttrs:false,setup:(_props,{slots})=>()=>Vue.h('div',{'data-stub':name},name==='ResizableSplit'?[slots.main?.(),slots.aside?.()]:slots.default?.())})
  const imports={'../calendarDates':calendarDates,vue:{...Vue,withDirectives:value=>value,Transition:stub('Transition')},'vue-router':Router,'../api':{api},'../i18n':{t:(source,params={})=>String(source).replace(/\{(\w+)\}/g,(all,key)=>String(params[key]??all)),categoryLabel:value=>value,formatDate:value=>String(value??''),formatNumber:value=>String(value),locale:Vue.ref('zh-CN'),timezone:Vue.ref('UTC')},'../layoutScope':{useLayoutBoolean:(_name,fallback)=>Vue.ref(fallback)},'../requirementFields':fields,'../mentions':mentions,'../requirementWorkflow':workflow,'../workItemQuery':queries,'../sprintPeople':people,'../sprintTabs':tabs}
  imports['../requirementPaging']=evaluate(await read('src/requirementPaging.ts'))
  imports['../defectPeople']=defectPeople;imports['./settingsScope']=evaluate(settingsSource,{vue:Vue,'../api':{api}},{window,document,localStorage,HTMLElement:Element})
  for(const name of ['Editor','RequirementLinks','DefectComposer','Requirements','SprintTabs','Sprints']){
    const descriptor=parse(sources[name],{filename:name+'.vue'}).descriptor,script=compileScript(descriptor,{id:'host-'+name}),template=compileTemplate({source:descriptor.template.content,filename:name+'.vue',id:'host-'+name,compilerOptions:{bindingMetadata:script.bindings}});assert.deepEqual(template.errors,[])
    const resolve=id=>{if(id.endsWith('.vue')){const child=id.split('/').at(-1).slice(0,-4);return{default:components[child]||stub(child)}}assert(id in imports,'Unexpected '+id);return imports[id]}
    const globals={window,document,localStorage,HTMLElement:Element,navigator:{clipboard:{writeText:async()=>{}}},location:{origin:'http://devflow.test'}}
    const scriptExports={};new Function('require','exports',...Object.keys(globals),transpile(script.content))(resolve,scriptExports,...Object.values(globals));const view={};new Function('require','exports',transpile(template.code))(resolve,view);scriptExports.default.render=view.render;components[name]=scriptExports.default
  }
  const router=Router.createRouter({history:Router.createMemoryHistory(),routes:[{path:'/iterations',component:components.Sprints},{path:'/requirements',component:stub('OtherPage')},{path:'/defects',component:stub('DefectsPage')}]})
  await router.push({path:'/iterations',query});const app=renderer.createApp({render:()=>Vue.h(Router.RouterView)});app.use(router);const container=new Element('root');scope.run(()=>app.mount(container));await router.isReady();await flush()
  const context=name=>componentNode(app._instance.subTree,name)?.component.setupState
  return{app,router,container,context,records,calls,document,window,config,tabs,layoutScope,storage,writes,detail,stop:()=>{app.unmount();scope.stop()}}
}
let count=0;async function test(name,run){await run();count++;console.log('✓ '+name)}
await test('tab cache sanitizes unknown/duplicate values and is isolated by verified account',()=>{
  const layoutScope=Vue.ref('a:one'),storage=new Map(),writes=[],localStorage={getItem:key=>storage.get(key)||null,setItem:(key,value)=>{writes.push({key,value});storage.set(key,value)}},scope=Vue.effectScope(),tabs=evaluate(tabsSource,{vue:Vue,'./layoutScope':{layoutScope}},{localStorage});let order
  scope.run(()=>{order=tabs.useSprintTabs()});assert.equal(order.value[0],'工作项列表');order.value=tabs.moveSprintTab(order.value,'概览',0);assert.equal(JSON.parse(storage.get('devflow-layout:v1:a:one:sprints.tab-order'))[0],'概览');const saved=writes.length;layoutScope.value='a:two';assert.equal(order.value[0],'工作项列表');assert.equal(writes.length,saved);layoutScope.value='a:one';assert.equal(order.value[0],'概览');layoutScope.value='';order.value=tabs.moveSprintTab(order.value,'看板',0);assert.equal(writes.length,saved)
  assert.deepEqual(tabs.normalizeSprintTabs(['看板','看板','secret requirement']),['看板',...tabs.defaultSprintTabs.filter(item=>item!=='看板')]);assert.deepEqual(tabs.normalizeSprintTabs({anything:true}),[...tabs.defaultSprintTabs]);assert.deepEqual(tabs.moveSprintTab(tabs.defaultSprintTabs,'概览',-1),[...tabs.defaultSprintTabs]);scope.stop()
})
await test('storage read/write failures are harmless and never cache business data',()=>{
  const scope=Vue.effectScope(),tabs=evaluate(tabsSource,{vue:Vue,'./layoutScope':{layoutScope:Vue.ref('tenant:user')}},{localStorage:{getItem(){throw Error('blocked')},setItem(){throw Error('full')}}});scope.run(()=>{const order=tabs.useSprintTabs();assert.doesNotThrow(()=>{order.value=tabs.moveSprintTab(order.value,'概览',0)})});scope.stop();assert(!/title|description|remarks|assignee/.test(tabsSource))
})
await test('mounted iteration defaults to work list; pointer drop only changes order and persists it',async()=>{
  const m=await mount(),s=m.context('Sprints'),tabs=m.context('SprintTabs');assert.equal(s.activeTab,'工作项列表');const data=[],e={dataTransfer:{setData:(...x)=>data.push(x)},preventDefault(){this.prevented=true}}
  tabs.start(e,'概览');tabs.dragover(e,'工作项列表');tabs.drop(e,'工作项列表');await flush();assert.equal(s.tabs[0],'概览');assert.equal(s.activeTab,'工作项列表');assert.equal(tabs.dragging,'');assert.equal(m.writes.length,1);assert.equal(data[0][0],'text/plain');m.stop()
})
await test('keyboard sorting retains active tab and Alt arrows do not navigate away',async()=>{
  const m=await mount(),s=m.context('Sprints'),tabs=m.context('SprintTabs'),e={key:'ArrowRight',altKey:true,preventDefault(){this.prevented=true},stopPropagation(){this.stopped=true}};tabs.keydown(e,'工作项列表');await flush();assert(e.prevented&&e.stopped);assert.equal(s.tabs[1],'工作项列表');assert.equal(s.activeTab,'工作项列表');assert.equal(m.router.currentRoute.value.path,'/iterations');tabs.keydown({...e,key:'End',altKey:false},'工作项列表');await flush();assert.equal(s.activeTab,'仪表盘');m.stop()
})
await test('clicking a requirement opens the real current-page host without reloading list or clearing filters',async()=>{
  const m=await mount(),s=m.context('Sprints');s.listSearch='Requirement';s.listRules=[{field:'priority',operator:'eq',value:'P1'}];s.listStatuses=['草稿'];s.activeTab='看板';await flush();const source=s.selected,calls=m.calls.length
  await s.openRequirement({...initialReq(17),objectType:'requirement'});await flush();assert.equal(m.router.currentRoute.value.path,'/iterations');assert.deepEqual({...m.router.currentRoute.value.query},{sprint:'1',req:'17'});assert.equal(s.selected,source);assert.equal(s.activeTab,'看板');assert.equal(s.listSearch,'Requirement');assert.equal(s.listRules.length,1);assert.deepEqual(s.listStatuses,['草稿']);assert.equal(s.loading,false)
  assert.equal(m.calls.slice(calls).some(call=>call.path==='/sprints/1'),false);assert.equal(m.calls.slice(calls).some(call=>call.path.startsWith('/requirements?')||call.path==='/preferences/requirement-list'),false);assert.equal(m.context('Requirements').selected.id,17);assert.equal(descendants(m.container).some(node=>String(node.props.class||'').includes('requirements-sidebar')),false);assert.equal(descendants(m.container).find(node=>node.props.class==='iteration-shell').props.inert,true);m.stop()
})
await test('clean close leaves the selected iteration, route extras, filters and list element intact',async()=>{
  const m=await mount({query:{sprint:'1',release:'keep'}}),s=m.context('Sprints');s.listSearch='Requirement';await s.openRequirement({...initialReq(17),objectType:'requirement'});await flush();const list=descendants(m.container).find(node=>String(node.props.class||'').includes('configurable-sprint-table'));list.scrollLeft=280
  m.context('Requirements').close();await flush();assert.equal(m.context('Requirements'),undefined);assert.deepEqual({...m.router.currentRoute.value.query},{sprint:'1',release:'keep'});assert.equal(m.router.currentRoute.value.path,'/iterations');assert.equal(s.selected.sprint.id,1);assert.equal(s.listSearch,'Requirement');assert.equal(descendants(m.container).find(node=>String(node.props.class||'').includes('configurable-sprint-table')),list);assert.equal(list.scrollLeft,280);assert.equal(m.document.activeElement.focusOptions.preventScroll,true);m.stop()
})
await test('unsaved drawer changes block close and switching linked requirements until explicitly discarded',async()=>{
  const m=await mount({query:{sprint:'1',req:'17'}}),r=m.context('Requirements');r.detailDraft.remarks='Unsaved remarks';r.close();await flush();assert.equal(r.leavePrompt,true);assert.equal(m.router.currentRoute.value.query.req,'17');r.finishLeave(false);await flush();assert.equal(r.detailDraft.remarks,'Unsaved remarks');assert.equal(m.router.currentRoute.value.query.req,'17')
  const nav=r.open(18);await flush();assert.equal(r.leavePrompt,true);assert.equal(r.selected.id,17);r.finishLeave(true);await nav;await flush();assert.equal(m.router.currentRoute.value.path,'/iterations');assert.equal(m.router.currentRoute.value.query.sprint,'1');assert.equal(r.selected.id,18);assert.equal(r.detailDraft.remarks,'');m.stop()
})
await test('save-and-close persists drafts, updates weight summary softly, and retains the current work list',async()=>{
  const m=await mount({query:{sprint:'1',req:'17'}}),s=m.context('Sprints'),r=m.context('Requirements');s.listSearch='Requirement';r.detailDraft.roleWeights.frontend.value=200;r.close();await flush();assert.equal(r.leavePrompt,true);await r.saveAndLeave();await flush();assert.equal(m.router.currentRoute.value.query.req,undefined);assert.equal(m.records.get(17).roleWeights.frontend.value,200);assert.equal(s.selected.weightSummary.totalWeight,200);assert.equal(s.loading,false);assert.equal(s.listSearch,'Requirement');assert.equal(m.router.currentRoute.value.path,'/iterations');m.stop()
})
await test('saving membership changes removes work from the old iteration without resetting the host/filter',async()=>{
  const m=await mount({query:{sprint:'1',req:'17'}}),s=m.context('Sprints'),r=m.context('Requirements');s.listSearch='Requirement';await r.patchFields({sprint:'22'});await flush();assert.equal(r.selected.id,17);assert.equal(s.selected.items.some(item=>item.id===17),false);assert.equal(s.selected.sprint.id,1);assert.equal(s.selected.weightSummary.requirementCount,1);assert.equal(s.listSearch,'Requirement');assert.equal(s.loading,false);m.stop()
})
await test('full embedded edit uses the explicit requirement, ignores route IDs, and saves in the same drawer host',async()=>{
  const m=await mount({query:{sprint:'1',req:'17'}}),r=m.context('Requirements');await r.editFull();await flush();const e=m.context('Editor');assert.equal(e.edit,true);assert.equal(e.f.id,17);e.f.title='Edited in iteration';await e.save();await flush();assert.equal(m.context('Editor'),undefined);assert.equal(r.fullEditorID,null);assert.equal(r.selected.title,'Edited in iteration');assert.equal(m.context('Sprints').selected.items.find(item=>item.id===17).title,'Edited in iteration');assert.equal(m.router.currentRoute.value.path,'/iterations');assert.equal(m.router.currentRoute.value.query.req,'17');assert.equal(m.calls.filter(call=>call.options?.method==='PATCH').at(-1).path,'/requirements/17');m.stop()
})
await test('full editor cancellation keeps unsaved values and saving blocks parent route changes',async()=>{
  const pending=deferred(),m=await mount({query:{sprint:'1',req:'17'},override:(path,options)=>options?.method==='PATCH'?pending.promise:undefined}),r=m.context('Requirements');await r.editFull();await flush();const e=m.context('Editor');e.f.title='Do not lose';assert.equal(await e.requestClose(),false);assert.equal(e.f.title,'Do not lose');const saving=e.save();await flush();const result=await m.router.replace({path:'/iterations',query:{sprint:'2'}});assert(Router.isNavigationFailure(result));assert.equal(m.router.currentRoute.value.query.req,'17');pending.reject(Error('save failed'));await saving;await flush();assert.equal(e.error,'save failed');assert.equal(e.f.title,'Do not lose');assert.equal(r.fullEditorID,17);m.stop()
})
await test('backlog deep links restore backlog and wrong-type item clicks never open requirements',async()=>{
  const m=await mount({query:{sprint:'backlog',req:'17'}}),s=m.context('Sprints');assert.equal(s.selected.backlog,true);await s.openRequirement({id:99,objectType:'defect'});assert.equal(m.router.currentRoute.value.query.req,'17');assert.equal(m.calls.some(call=>call.path==='/requirements/99'),false);m.stop()
})
await test('failed requirement fetch has a dismissible real drawer without displaying an empty requirement pool',async()=>{
  const m=await mount({query:{sprint:'1',req:'17'},override:path=>path==='/requirements/17'?Promise.reject(Error('not authorized')):undefined}),r=m.context('Requirements');assert.equal(r.selected,null);assert.equal(r.detailError,'not authorized');assert.match(text(m.container),/not authorized/);assert.equal(descendants(m.container).some(node=>String(node.props.class||'').includes('requirements-sidebar')),false);r.close();await flush();assert.equal(m.router.currentRoute.value.path,'/iterations');assert.equal(m.router.currentRoute.value.query.req,undefined);m.stop()
})
await test('sidebar navigation keeps the selected iteration URL in sync and honours the same dirty guard',async()=>{
  const m=await mount({query:{sprint:'1',req:'17',release:'keep'}}),s=m.context('Sprints'),r=m.context('Requirements');r.detailDraft.remarks='Pending';const navigating=s.navigateSprint(2);await flush();assert.equal(r.leavePrompt,true);assert.equal(s.selected.sprint.id,1);r.finishLeave(false);await navigating;await flush();assert.equal(m.router.currentRoute.value.query.sprint,'1');assert.equal(s.selected.sprint.id,1)
  r.close();await flush();r.finishLeave(true);await flush();await s.navigateSprint(2);await flush();assert.equal(m.router.currentRoute.value.query.sprint,'2');assert.equal(s.selected.sprint.id,2);assert.equal(s.activeTab,'工作项列表');assert.equal(m.router.currentRoute.value.query.release,'keep');await s.navigateSprint('backlog');await flush();assert.equal(m.router.currentRoute.value.query.sprint,'backlog');assert.equal(s.selected.backlog,true);m.stop()
})
await test('late soft refresh cannot overwrite a newer selected iteration or force a list reload',async()=>{
  const pending=deferred();let waiting=false;const m=await mount({override:path=>path==='/sprints/1'&&waiting?pending.promise:undefined}),s=m.context('Sprints');waiting=true;const refreshing=s.refreshRequirement();await s.navigateSprint(2);await flush();assert.equal(s.selected.sprint.id,2);pending.resolve(m.detail(1));await refreshing;await flush();assert.equal(s.selected.sprint.id,2);assert.equal(s.loading,false);m.stop()
})
await test('iteration title, board, weight detail and code entries all route through the shared host',async()=>{
  assert.match(sources.Sprints,/<Requirements v-if="route.query.req" detail-only @updated="refreshRequirement"/);assert.match(sources.Sprints,/@open-requirement="openRequirement"/);assert(!/router\.push\(workItemLink/.test(sources.Sprints));assert(!/iframe/i.test(sources.Requirements));assert.match(sources.Requirements,/if\(!props.detailOnly\)await Promise.all\(\[load\(\),loadColumns\(\)\]\)/);assert.match(await read('src/components/SprintWeightPanel.vue'),/<RequirementCode/)
})
await test('creating a linked defect preserves parent drafts and disables the underlying requirement until completion',async()=>{
 const m=await mount({query:{sprint:'1',req:'17',release:'keep'}}),r=m.context('Requirements'),s=m.context('Sprints');s.listSearch='Requirement';r.detailDraft.remarks='Keep parent draft';r.selectDetailTab('缺陷');r.createDefect();await flush();const c=m.context('DefectComposer');assert.equal(c.form.requirementId,17);assert.equal(c.form.sprint,'123');assert.equal(r.detailDraft.remarks,'Keep parent draft');assert.equal(componentNode(m.app._instance.subTree,'Requirements').component.setupState.defectContext.id,17)
 c.form.title='Linked from sprint';await c.save();await flush();assert.equal(m.context('DefectComposer'),undefined);assert.equal(r.related.defects[0].requirementId,17);assert.equal(r.detailDraft.remarks,'Keep parent draft');assert.equal(m.records.get(17).remarks,'');assert.equal(s.listSearch,'Requirement');assert.deepEqual({...m.router.currentRoute.value.query},{sprint:'1',req:'17',release:'keep'});assert.equal(m.calls.some(call=>call.options?.method==='PATCH'),false);m.stop()
})
await test('a dirty linked defect guards navigation and cancelling the top drawer never closes its requirement',async()=>{
 const m=await mount({query:{sprint:'1',req:'17'}}),r=m.context('Requirements');r.createDefect();await flush();const c=m.context('DefectComposer');c.form.title='Unsaved defect';const nav=await m.router.replace({path:'/iterations',query:{sprint:'1',req:'18'}});assert(Router.isNavigationFailure(nav));assert.equal(r.selected.id,17);r.close();await flush();assert.equal(m.context('DefectComposer'),c);m.config.confirm=true;r.close();await flush();assert.equal(m.context('DefectComposer'),undefined);assert.equal(m.router.currentRoute.value.query.req,'17');assert.equal(r.selected.id,17);m.stop()
})
await test('related-requirement navigation uses the existing dirty guard and preserves the iteration and extra query',async()=>{
 const m=await mount({query:{sprint:'1',req:'17',release:'keep'}}),r=m.context('Requirements');r.selectDetailTab('关联需求');await flush();const links=m.context('RequirementLinks');assert.equal(links.items[0].id,18);r.detailDraft.remarks='Keep until confirmed';links.open(links.items[0]);await flush();assert.equal(r.leavePrompt,true);assert.equal(r.selected.id,17);r.finishLeave(false);await flush();assert.equal(r.detailDraft.remarks,'Keep until confirmed');links.open(links.items[0]);await flush();r.finishLeave(true);await flush();assert.equal(r.selected.id,18);assert.deepEqual({...m.router.currentRoute.value.query},{sprint:'1',req:'18',release:'keep'});assert.equal(m.context('Sprints').selected.sprint.id,1);m.stop()
})
await test('released styling follows the actual requirement status name, including parent and child rows only',async()=>{
 const m=await mount(),s=m.context('Sprints')
 s.statusDefinitions=[...s.statusDefinitions,{id:20,key:'published-custom',name:'已经上线',category:'done',enabled:true,system:false}]
 const rows=[
  {...initialReq(17),objectType:'requirement',status:'已上线'},
  {...initialReq(18),objectType:'requirement',parentId:17,status:'published-custom'},
  {...initialReq(19),objectType:'requirement',status:'custom-live',statusName:'已上线'},
  ...['已完成','已关闭','已拒绝','已取消','流程终止','待上线'].map((status,index)=>({...initialReq(20+index),objectType:'requirement',status,statusCategory:'done'})),
  {...initialReq(30),objectType:'defect',status:'已上线'},
  {...initialReq(31),objectType:'requirement',status:'已上线',statusName:'等待客户验收'},
 ]
 s.listColumns=['title','status'];s.selected.items=rows;await flush()
 const before=JSON.stringify(s.selected.items)
 assert.deepEqual(rows.map(item=>s.isReleasedRequirement(item)),[true,true,true,false,false,false,false,false,false,false,false])
 const rendered=descendants(m.container).filter(node=>node.tag==='tr'&&String(node.props.class||'').includes('released-requirement-row'))
 assert.equal(rendered.length,3);assert(rendered.some(node=>text(node).includes('Requirement 17')));assert(rendered.some(node=>text(node).includes('Requirement 18')))
 assert(rendered.every(node=>node.props.disabled===undefined&&node.props['aria-disabled']===undefined&&node.props.inert===undefined))
 s.statusDefinitions=s.statusDefinitions.map(state=>state.key==='published-custom'?{...state,name:'待验收'}:state);await flush()
 assert.equal(descendants(m.container).filter(node=>node.tag==='tr'&&String(node.props.class||'').includes('released-requirement-row')).length,2)
 assert.equal(JSON.stringify(s.selected.items),before);m.stop()
})
await test('a released row title opens the real editable drawer and saves without changing release status or iteration',async()=>{
 const m=await mount({query:{sprint:'1',release:'keep'}}),s=m.context('Sprints')
 m.records.set(17,{...m.records.get(17),status:'已上线'});s.listColumns=['title','status'];await s.load(1);await flush()
 const row=descendants(m.container).find(node=>node.tag==='tr'&&String(node.props.class||'').includes('released-requirement-row'))
 const title=descendants(row).find(node=>node.tag==='button'&&String(node.props.class||'').includes('requirement-title'))
 assert(title);assert(!title.props.disabled);await title.props.onClick();await flush()
 const r=m.context('Requirements');assert.equal(r.selected.id,17);assert.equal(r.canEdit,true)
 await r.patchFields({title:'Released but editable'});await flush()
 assert.equal(m.records.get(17).title,'Released but editable');assert.equal(m.records.get(17).status,'已上线');assert.equal(m.router.currentRoute.value.path,'/iterations');assert.deepEqual({...m.router.currentRoute.value.query},{sprint:'1',release:'keep',req:'17'})
 r.close();await flush();assert.equal(m.context('Requirements'),undefined);assert(descendants(m.container).some(node=>node.tag==='tr'&&String(node.props.class||'').includes('released-requirement-row')&&text(node).includes('Released but editable')));m.stop()
})
await test('released rows retain inline edits and only normal permissions disable controls',async()=>{
 const m=await mount(),s=m.context('Sprints');m.records.set(17,{...m.records.get(17),status:'已经上线'});s.listColumns=['title','progress','actualHours'];await s.load(1);await flush()
 const row=descendants(m.container).find(node=>node.tag==='tr'&&String(node.props.class||'').includes('released-requirement-row'))
 const progress=descendants(row).find(node=>node.tag==='input'&&node.props['aria-label']==='进度');assert(progress);assert.equal(progress.props.disabled,false)
 progress.value='35';await progress.props.onChange({target:progress});await flush();assert.equal(m.records.get(17).progress,35);assert.equal(m.records.get(17).status,'已经上线')
 s.members=s.members.map(member=>({...member,projectRole:'viewer'}));await flush()
 const readonly=descendants(m.container).find(node=>node.tag==='tr'&&String(node.props.class||'').includes('released-requirement-row'))
 assert(descendants(readonly).filter(node=>node.tag==='input').every(node=>node.props.disabled===true));assert(!descendants(readonly).find(node=>node.tag==='button'&&String(node.props.class||'').includes('requirement-title')).props.disabled);m.stop()
})
await test('released row styles adapt to both themes, preserve readable contrast and expose hover and keyboard focus',async()=>{
 const descriptor=parse(sources.Sprints).descriptor,style=compileStyle({source:descriptor.styles[0].content,filename:'Sprints.vue',id:'released-test',scoped:true});assert.deepEqual(style.errors,[])
 const rules=[];style.rawResult.root.walkRules(rule=>{if(rule.selector.includes('released-requirement-row'))rules.push(rule)})
 assert(rules.some(rule=>rule.selector.includes('>td')&&rule.nodes.some(node=>node.prop==='background'&&node.value==='var(--sprint-released-bg)')))
 assert(rules.some(rule=>rule.selector.includes(':hover')));assert(rules.some(rule=>rule.selector.includes(':focus-within')));assert(rules.some(rule=>rule.selector.includes(':focus-visible')&&rule.nodes.some(node=>node.prop==='outline')))
 for(const rule of rules)for(const declaration of rule.nodes){assert(!['opacity','pointer-events'].includes(declaration.prop));assert(!/line-through/.test(declaration.value||''))}
 const themes=await read('src/theme.css'),base=await read('src/style.css')
 const light=base.match(/:root\{([^}]+)\}/)[1]+themes.match(/:root\{([^}]+)\}/)[1],dark=themes.match(/:root\[data-theme="dark"\]\{([^}]+)\}/)[1]
 const color=(source,name)=>source.match(new RegExp('--'+name+':(#[0-9a-f]+)','i'))[1]
 const rgb=value=>{const h=value.length===4?value.slice(1).split('').map(x=>x+x).join(''):value.slice(1);return[0,2,4].map(i=>parseInt(h.slice(i,i+2),16)/255)}
 const mix=(a,b,weight)=>a.map((value,i)=>value*weight+b[i]*(1-weight)),luminance=values=>values.map(value=>value<=.04045?value/12.92:((value+.055)/1.055)**2.4).reduce((sum,value,i)=>sum+value*[.2126,.7152,.0722][i],0)
 for(const theme of [light,dark]){const bg=mix(rgb(color(theme,'surface')),rgb(color(theme,'muted')),.88),fg=mix(rgb(color(theme,'ink')),rgb(color(theme,'muted')),.55),l1=luminance(bg),l2=luminance(fg);assert((Math.max(l1,l2)+.05)/(Math.min(l1,l2)+.05)>=4.5,'released text contrast below WCAG AA')}
})
await test('pending relation writes cannot be interrupted by requirement or iteration navigation',async()=>{
 const pending=deferred(),m=await mount({query:{sprint:'1',req:'17'},override:(path,options)=>path.endsWith('/links')&&options?.method==='POST'?pending.promise:undefined}),r=m.context('Requirements');r.selectDetailTab('关联需求');await flush();const links=m.context('RequirementLinks'),saving=links.change(18);await flush();const nav=await m.router.replace({path:'/iterations',query:{sprint:'2'}});assert(Router.isNavigationFailure(nav));assert.equal(r.selected.id,17);assert.equal(m.context('Sprints').selected.sprint.id,1);pending.reject(Error('relation save failed'));await saving;assert.equal(links.error,'relation save failed');m.stop()
})
console.log(`Passed ${count} sprint tabs and real drawer-host tests.`)

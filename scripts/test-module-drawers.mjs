import {workflow} from './workflow-test-support.mjs'
import assert from 'node:assert/strict'
import {readFile,readdir} from 'node:fs/promises'
import ts from 'typescript'
import * as Vue from 'vue'
import {parse,compileScript,compileTemplate} from 'vue/compiler-sfc'

const root=new URL('../',import.meta.url)
const read=path=>readFile(new URL(path,root),'utf8')
const transpile=source=>ts.transpileModule(source,{compilerOptions:{module:ts.ModuleKind.CommonJS,target:ts.ScriptTarget.ES2022}}).outputText
const defectPeople={};new Function('exports',transpile(await read('src/defectPeople.ts')))(defectPeople)
let count=0
async function test(name,run){await run();count++;console.log('✓ '+name)}
function component(source){
 const {descriptor}=parse(source),script=compileScript(descriptor,{id:'test-module-drawer'}),template=compileTemplate({id:'test-module-drawer',filename:'Test.vue',source:descriptor.template.content,compilerOptions:{bindingMetadata:script.bindings}});assert.deepEqual(template.errors,[])
 const scope=Vue.effectScope(),unmounts=[],route=Vue.reactive({query:{}}),routerCalls=[],apiCalls=[],leaveGuards=[],updateGuards=[],confirmation={allowed:false,calls:[]}
 const imports={'../requirementWorkflow':workflow,vue:{...Vue,onMounted:()=>{},onBeforeUnmount:fn=>unmounts.push(fn),resolveComponent:name=>({name}),withDirectives:node=>node},'vue-router':{useRoute:()=>route,onBeforeRouteLeave:fn=>leaveGuards.push(fn),onBeforeRouteUpdate:fn=>updateGuards.push(fn),useRouter:()=>({replace:async value=>{routerCalls.push(value);route.query=value.query||{}},push:async value=>routerCalls.push(value)})},'../i18n':{t:value=>value,locale:Vue.ref('zh-CN'),formatDate:value=>String(value)},'../api':{api:async(path,options)=>{apiCalls.push({path,options});return{items:[]}}},'../components/ResizableDrawer.vue':{default:{name:'ResizableDrawer'}},'../components/CustomFieldInputs.vue':{default:{name:'CustomFieldInputs'}}}
 imports['../defectPeople']=defectPeople
 // Projects 页面新增了按租户和账户隔离的布局状态；测试中提供稳定作用域，避免把
 // 组件的隐私边界实现误判为抽屉交互失败。
 imports['../layoutScope']={layoutScope:Vue.ref('tenant:user')}
 imports['../components/settingsScope']={useSettingsScope:()=>({locked:Vue.ref(false),current:()=>true,request:imports['../api'].api,project:'test-project'})}
 const scriptModule={},renderModule={};new Function('require','exports','localStorage','window','location',transpile(script.content))(id=>imports[id]||(id.endsWith('.vue')?{default:{name:id.split('/').at(-1).replace('.vue','')}}:undefined),scriptModule,{getItem:()=>null,setItem:()=>{}},{addEventListener:()=>{},removeEventListener:()=>{},confirm:message=>{confirmation.calls.push(message);return confirmation.allowed}},{href:''});new Function('require','exports',transpile(template.code))(id=>imports[id]||(id.endsWith('.vue')?{default:{name:id.split('/').at(-1).replace('.vue','')}}:undefined),renderModule)
 const state=scope.run(()=>scriptModule.default.setup({}, {expose:()=>{}})),setup=Vue.proxyRefs(state)
 return{...state,apiCalls,routerCalls,leaveGuards,updateGuards,confirmation,render:()=>renderModule.render({},[],{},setup,{},{}),stop:()=>{unmounts.forEach(fn=>fn());scope.stop()}}
}
function descendants(node,predicate){if(!node||typeof node!=='object')return[];const result=predicate(node)?[node]:[];if(Array.isArray(node.children))for(const child of node.children)result.push(...descendants(child,predicate));return result}
const drawers=node=>descendants(node,child=>child.type?.name==='ResizableDrawer')
const shades=node=>descendants(node,child=>typeof child.props?.class==='string'&&child.props.class.includes('drawer-shade'))

await test('legacy views and the split testing submodules route every detail drawer through the reusable component',async()=>{
 const files=(await readdir(new URL('src/views/',root))).filter(file=>file.endsWith('.vue'))
 for(const file of files){
  let source=await read('src/views/'+file)
  if(file==='Sprints.vue'){
   // The iteration's explicit creation form is not a selected-record detail
   // drawer. Exempt only its known Editor host, not other fixed drawers.
   const creation=/<section class="drawer sprint-requirement-drawer" role="dialog" aria-modal="true" :aria-label="t\('创建需求'\)"><Editor ref="requirementComposer" embedded [^>]*@created="requirementCreated" @cancel="showRequirementComposer=false"\/><\/section>/
   assert.match(source,creation);source=source.replace(creation,'')
  }
  assert(!/<(?:aside|div|section)[^>]*\bclass="(?:drawer|[^"\n]*\sdrawer)(?:\s|\")/.test(source),file+' contains an unconverted fixed drawer')
 }
 for(const file of ['Defects','Projects'])assert.match(await read('src/views/'+file+'.vue'),/import ResizableDrawer from ['"]\.\.\/components\/ResizableDrawer\.vue['"]/)
 const sources=await Promise.all(['TestCaseLibrary','TestDesigns','TestingOperations'].map(name=>read('src/components/testing/'+name+'.vue')))
 for(const source of sources)assert.match(source,/import ResizableDrawer from ['"]\.\.\/ResizableDrawer\.vue['"]/)
 assert.doesNotMatch(await read('src/views/Testing.vue'),/import ResizableDrawer/)
})
await test('defect detail binds width without modifying selection, data or close routing',async()=>{
 const m=component(await read('src/views/Defects.vue'));m.selected.value={id:9,code:'BUG-0009',title:'原始缺陷 title',status:'新建',allowedTransitions:[],customFields:{}}
 let drawer=drawers(m.render())[0];assert.equal(drawer.props.label,'原始缺陷 title');assert.equal(drawer.props.width,1040);drawer.props['onUpdate:width'](1280);drawer=drawers(m.render())[0];assert.equal(drawer.props.width,1280);assert.equal(m.selected.value.id,9);assert.equal(m.apiCalls.length,0)
 await m.close();assert.equal(m.selected.value,null);assert.deepEqual(m.routerCalls,[{query:{}}]);assert.equal(m.drawerWidth.value,1280);m.stop()
})
await test('defect comment route and close guards preserve drafts until explicit discard and block during saves',async()=>{
 const m=component(await read('src/views/Defects.vue'));m.selected.value={id:9,title:'缺陷详情',customFields:{}};m.comment.value='尚未发布的评论'
 assert.equal(m.leaveGuards.length,1);assert.equal(m.updateGuards.length,1)
 assert.equal(m.leaveGuards[0](),false);assert.equal(m.updateGuards[0]({query:{bug:'10'}},{query:{bug:'9'}}),false)
 const asked=m.confirmation.calls.length;assert.equal(m.updateGuards[0]({query:{bug:'9',view:'board'}},{query:{bug:'9'}}),true);assert.equal(m.confirmation.calls.length,asked)
 await m.close();assert.equal(m.selected.value.id,9);assert.equal(m.comment.value,'尚未发布的评论');assert.equal(m.routerCalls.length,0)
 assert(m.confirmation.calls.every(message=>message==='评论尚未发布，确定离开并放弃内容？'))
 m.confirmation.allowed=true;assert.equal(m.leaveGuards[0](),true);assert.equal(m.updateGuards[0]({query:{}},{query:{bug:'9'}}),true)
 m.saving.value=true;const beforeSaveGuard=m.confirmation.calls.length;assert.equal(m.leaveGuards[0](),false);assert.equal(m.updateGuards[0]({query:{bug:'10'}},{query:{bug:'9'}}),false);await m.close();assert.equal(m.selected.value.id,9);assert.equal(m.comment.value,'尚未发布的评论');assert.equal(m.routerCalls.length,0);assert.equal(m.confirmation.calls.length,beforeSaveGuard)
 m.saving.value=false;await m.close();assert.equal(m.selected.value,null);assert.equal(m.comment.value,'');assert.deepEqual(m.routerCalls,[{query:{}}]);assert.equal(m.apiCalls.length,0)
 const afterDiscard=m.confirmation.calls.length;assert.equal(m.leaveGuards[0](),true);assert.equal(m.confirmation.calls.length,afterDiscard);m.stop()
})
await test('split test drawers retain independent cache keys, widths, labels, and self-only backdrop closers',async()=>{
 const library=await read('src/components/testing/TestCaseLibrary.vue'),designs=await read('src/components/testing/TestDesigns.vue'),operations=await read('src/components/testing/TestingOperations.vue')
 const expected=[
  [library,'testing.case.workspace.width','1120','680',"record?.title||t('创建用例')"],
  [library,'testing.ai.width','1000','600',"t('AI 生成用例')"],
  [designs,'testing.design.width','1160','700',"record?.name||t('创建测试设计')"],
  [operations,'testing.plan.width','1000','640',"plan?.name||t('创建测试计划')"],
  [operations,'testing.execution.width','1080','680','execution.caseTitle'],
 ]
 for(const [source,key,width,min,label]of expected){
  assert(source.includes(`storage-key="${key}"`),key+' must remain independently cached')
  assert(source.includes(`:initial-width="${width}"`),key+' must preserve its initial width')
  assert(source.includes(`:min-width="${min}"`),key+' must preserve a usable minimum width')
  assert(source.includes(`:label="${label}"`),key+' must expose an explicit accessible label')
 }
 for(const source of [library,designs,operations])assert.match(source,/<div v-if="[^\n]+" class="drawer-shade" @click\.self=/)
})
await test('project settings use the compact resizable drawer and retain the original edit-close behavior',async()=>{
 const m=component(await read('src/views/Projects.vue'));m.openEdit({id:'project',name:'Original name',code:'PRJ',status:'active',canManage:true});m.editing.value.name='Unsaved draft';const vnode=m.render(),drawer=drawers(vnode)[0];assert.equal(drawer.props.label,'项目设置');assert.equal(drawer.props.width,780);assert.equal(drawer.props['min-width'],480);drawer.props['onUpdate:width'](900);assert.equal(m.editing.value.name,'Unsaved draft');assert.equal(m.apiCalls.length,0);const shade=shades(vnode)[0],target={};shade.props.onClick({target,currentTarget:target});assert.equal(m.editing.value,null);assert.equal(m.drawerWidth.value,900);m.stop()
})
await test('all drawer resize edges use explicit blue hover/focus/drag cues, with mobile stacked content',async()=>{
 const source=await read('src/components/ResizableDrawer.vue');assert.match(source,/\.resizable-drawer-handle:hover::before[^{}]*\.resizable-drawer-handle:focus-visible::before[^{}]*\.resizable-drawer--dragging[^{}]*\{background:#1677ff\}/)
 assert.match(source,/\.resizable-drawer-handle:hover>span[^{}]*\{border-color:#1677ff;background:#e6f4ff/);assert.match(source,/width:15px;cursor:col-resize/);assert.match(source,/@media\(max-width:760px\).*module-detail-drawer.*flex-direction:column;overflow:auto/);assert(!/background:#7067e9|border-color:#7067e9/.test(source))
})
console.log(`Passed ${count} module drawer integration regressions.`)

import {workflow} from './workflow-test-support.mjs'
import assert from 'node:assert/strict'
import {readFileSync} from 'node:fs'
import {fileURLToPath} from 'node:url'
import ts from 'typescript'
import * as Vue from 'vue'
const root=fileURLToPath(new URL('../',import.meta.url))
function evaluateSource(source,imports={}){const exports={};new Function('require','exports',ts.transpileModule(source,{compilerOptions:{module:ts.ModuleKind.CommonJS,target:ts.ScriptTarget.ES2022}}).outputText)(id=>imports[id],exports);return exports}
const read=path=>readFileSync(root+path,'utf8')
const mentions=evaluateSource(read('src/mentions.ts'))
const query=evaluateSource(read('src/workItemQuery.ts'))
const people=evaluateSource(read('src/sprintPeople.ts'),{'./mentions':mentions})
const fields=evaluateSource(read('src/requirementFields.ts'))
const tabs=evaluateSource(read('src/sprintTabs.ts'),{vue:Vue,'./layoutScope':{layoutScope:Vue.ref('')}})
const detail=id=>({sprint:{id,name:'Iteration '+id,status:'进行中'},items:[],summary:{}})
const base=async path=>path==='/sprints'?{items:[detail(1).sprint,detail(2).sprint]}:path==='/members'?{items:[{id:'u1',isCurrent:true,projectRole:'tenant_admin',active:true,name:'First'}]}:path.startsWith('/sprints/')&&/\d+$/.test(path)?detail(Number(path.split('/').at(-1))):{items:[],columns:null}
const deferred=()=>{let resolve,reject;const promise=new Promise((a,b)=>{resolve=a;reject=b});return{promise,resolve,reject}}
function mount(handler=base){
 const calls=[],storage=new Map([['devflow-project','p-original']]),unmounts=[],scope=Vue.effectScope(),exports={}
 const route=Vue.reactive({path:'/iterations',query:{}}),api=async(path,options)=>{calls.push({path,options});return handler(path,options)}
 const router={replace:async target=>{route.path=target.path||route.path;route.query=target.query||{}}}
 const imports={'../sprintTabs':tabs,'../requirementWorkflow':workflow,vue:{...Vue,onMounted:()=>{},onBeforeUnmount:fn=>unmounts.push(fn)},'vue-router':{useRoute:()=>route,useRouter:()=>router},'../api':{api},'../i18n':{t:x=>x,categoryLabel:x=>x,locale:Vue.ref('zh-CN'),timezone:Vue.ref('Asia/Shanghai'),formatNumber:x=>String(x),formatDate:x=>String(x)},'../workItemQuery':query,'../sprintPeople':people,'../requirementFields':fields}
 const exposed=['selected','members','loading','saving','open','openBacklog','load','listColumns','columnPanel','columnsSaving','columnsError','loadColumns','saveColumns','error']
 const source=read('src/views/Sprints.vue').match(/<script setup[^>]*>([\s\S]*?)<\/script>/)[1]+`\nexport {${exposed.join(',')}}`
 scope.run(()=>new Function('require','exports','localStorage',ts.transpileModule(source,{compilerOptions:{module:ts.ModuleKind.CommonJS,target:ts.ScriptTarget.ES2022}}).outputText)(id=>id.endsWith('.vue')?{}:id==='../layoutScope'?{useLayoutBoolean:(_key,value)=>Vue.ref(value)}:imports[id],exports,{getItem:key=>storage.get(key)||null}))
 exports.selected.value=detail(1);exports.members.value=[{id:'u1',isCurrent:true,projectRole:'tenant_admin',active:true,name:'First'}];exports.loading.value=false
 return{...exports,calls,storage,stop:()=>{unmounts.forEach(fn=>fn());scope.stop()}}
}
let count=0
async function test(name,run){await run();count++;console.log('✓ '+name)}
await test('column writes are single-flight and a late initial read cannot overwrite newly saved preferences',async()=>{const readPending=deferred(),writePending=deferred(),m=mount((path,options)=>options?.method==='PATCH'?writePending.promise:readPending.promise);const reading=m.loadColumns();const saving=m.saveColumns(['code','title','priority']);await m.saveColumns(['code','title','status']);await m.loadColumns();assert.equal(m.calls.length,2);writePending.resolve({});await saving;readPending.resolve({columns:['code','title','status']});await reading;assert.deepEqual(m.listColumns.value,['code','title','priority']);assert.equal(m.columnsSaving.value,false);m.stop()})
await test('iteration-wide preference saves do not close another newly opened columns panel',async()=>{let oldClosed=0,newClosed=0;const pending=deferred(),m=mount(()=>pending.promise);m.columnPanel.value={close:()=>oldClosed++};const draft=['code','title','priority'],saving=m.saveColumns(draft);draft.push('status');m.columnPanel.value={close:()=>newClosed++};m.selected.value=detail(2);pending.resolve({});await saving;assert.deepEqual(m.listColumns.value,['code','title','priority']);assert.equal(newClosed,0);assert.equal(oldClosed,0);m.stop()})
await test('project scope changes block old preference submissions and ignore pending responses',async()=>{const pending=deferred(),m=mount(()=>pending.promise);const saving=m.saveColumns(['code','title','priority']);assert.equal(m.calls[0].options.headers['X-DevFlow-Project'],'p-original');m.storage.set('devflow-project','p-new');m.listColumns.value=['code','title','status'];await m.saveColumns(['code']);pending.resolve({});await saving;assert.equal(m.calls.length,1);assert.deepEqual(m.listColumns.value,['code','title','status']);m.stop()})
await test('unmounted pages cannot apply a late preference read or launch follow-up requests',async()=>{const pending=deferred(),m=mount(()=>pending.promise);const reading=m.loadColumns();m.listColumns.value=['code','title','priority'];m.stop();pending.resolve({columns:['code','title','status']});await reading;assert.deepEqual(m.listColumns.value,['code','title','priority']);await m.loadColumns();assert.equal(m.calls.length,1)})
await test('new iteration columns place creation time after title while saved choices stay intact',async()=>{
 const fresh=mount();await fresh.loadColumns();assert.deepEqual(fresh.listColumns.value.slice(0,3),['code','title','createdAt']);fresh.stop()
 for(const columns of [['code','title','priority'],['code','title','priority','createdAt'],['code','title']]){
  const saved=mount(async()=>({columns}));await saved.loadColumns();assert.deepEqual(saved.listColumns.value,columns);saved.stop()
 }
})
console.log(`Passed ${count} sprint draft and request-scope safety tests.`)

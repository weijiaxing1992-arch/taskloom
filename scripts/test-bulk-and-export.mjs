import assert from 'node:assert/strict'
import {readFileSync} from 'node:fs'
import ts from 'typescript'
import * as Vue from 'vue'
import {parse,compileScript,compileTemplate} from 'vue/compiler-sfc'
const read=path=>readFileSync(new URL('../'+path,import.meta.url),'utf8')
function evaluate(source,imports={},globals={}){const exports={};new Function('require','exports',...Object.keys(globals),ts.transpileModule(source,{compilerOptions:{module:ts.ModuleKind.CommonJS,target:ts.ScriptTarget.ES2022}}).outputText)(id=>imports[id]||{},exports,...Object.values(globals));return exports}
const query=evaluate(read('src/workItemQuery.ts')),created=[],released=[],clicks=[],timers=[]
const exporter=evaluate(read('src/workItemExport.ts'),{'./workItemQuery':query,'./i18n':{t:value=>'English:'+value,formatDate:value=>'Date:'+value}},{URL:{createObjectURL:blob=>{created.push(blob);return 'blob:local-'+created.length},revokeObjectURL:url=>released.push(url)},document:{createElement:()=>({href:'',download:'',click(){clicks.push({href:this.href,download:this.download})},remove(){}}),body:{append(){}}},setTimeout:(fn,delay)=>{timers.push({fn,delay})}})
function csvRows(text){const rows=[];let row=[],cell='',quoted=false;for(let index=text.charCodeAt(0)===0xFEFF?1:0;index<text.length;index++){const char=text[index];if(char==='"'){if(quoted&&text[index+1]==='"'){cell+='"';index++}else quoted=!quoted}else if(char===','&&!quoted){row.push(cell);cell=''}else if(char==='\r'&&!quoted&&text[index+1]==='\n'){row.push(cell);rows.push(row);row=[];cell='';index++}else cell+=char}assert.equal(quoted,false);assert.equal(cell,'');return rows}
const requirement=(id=7,extra={})=>({id,code:'REQ-'+id,title:'User title '+id,priority:'P2',...extra})
const deferred=()=>{let resolve;const promise=new Promise(yes=>resolve=yes);return {promise,resolve}}
function bulkFixture(handler=async()=>({}),extra={}){
 const calls=[],events=[],clipboard=[],scope=Vue.effectScope(),locked=Vue.ref(false),unmount=[],props=Vue.reactive({items:[requirement(7),requirement(8)],members:[],sprints:[],categories:[],statuses:[],disabled:false,...extra})
 const source=read('src/components/RequirementBulkActions.vue').match(/<script setup[^>]*>([\s\S]*?)<\/script>/)[1],imports={vue:{...Vue,onBeforeUnmount:fn=>unmount.push(fn),onMounted:fn=>fn()},'../i18n':{t:value=>value},'./settingsScope':{useSettingsScope:()=>({locked,project:'p',current:()=>!locked.value,request:async(path,options)=>{calls.push({path,options});return handler(path,options)}})}}
 const m=scope.run(()=>evaluate(source+'\nexport {opened,busy,action,field,value,people,confirmed,error,progress,results,targets,allowed,checking,valid,begin,close,changeAction,changeField,execute}',imports,{defineProps:()=>props,defineEmits:()=>(...args)=>events.push(args),location:{origin:'https://devflow.test'},navigator:{clipboard:{writeText:async value=>clipboard.push(value)}},window:new EventTarget()}))
 return {...m,props,calls,events,clipboard,locked,stop(){unmount.forEach(fn=>fn());scope.stop()}}
}
let count=0;async function test(name,run){await run();count++;console.log('✓ '+name)}
await test('CSV formulas are neutralized before quoting, including Unicode whitespace and control-prefix variants',()=>{
 for(const value of ['=1+1','+SUM(A1:A2)','-1+2','@SUM(A1:A2)',' \t=1','\uFEFF=1','\nhello','\rhello','\thello'])assert.equal(csvRows(exporter.csvCell(value)+'\r\n')[0][0],"'"+value)
 for(const value of ['ordinary','你好','1.25','1-2','a@b.test',''])assert.equal(csvRows(exporter.csvCell(value)+'\r\n')[0][0],value)
})
await test('CSV escapes quotes, commas and multiline user content without changing its cell boundaries',()=>{
 const value='User "quoted", content\n第二行';assert.equal(exporter.csvCell(value),'"User ""quoted"", content\n第二行"');assert.equal(csvRows(exporter.csvCell(value)+'\r\n')[0][0],value);assert.equal(exporter.csvCell(null),'""')
})
await test('CSV exports every provided sorted row, deduplicates visible columns and preserves custom field names verbatim',()=>{
 const rows=csvRows(exporter.requirementCSV([requirement(9,{customFields:{score:8}}),requirement(7,{customFields:{score:0}}),requirement(1)], [{key:'score',name:'用户评分',type:'number',enabled:true}],[],['priority','cf.requirement.score','title','priority','not-real']))
 assert.deepEqual(rows[0],['English:编号','English:标题','English:优先级','用户评分']);assert.deepEqual(rows.slice(1).map(row=>row[0]),['REQ-9','REQ-7','REQ-1']);assert.equal(rows[2][3],'0');assert.equal(rows.length,4)
})
await test('CSV resolves multi-person IDs and retained historical snapshots without merging same-name accounts',()=>{
 const members=[{id:'a',name:'Same'},{id:'b',name:'Same'}],item=requirement(1,{assigneeUserIds:['a','b','old'],assignees:[{id:'old',name:'Former member'}],owner:'Legacy owner',roleWeights:{frontend:{userIds:['a','b'],value:20}},customFields:{testers:['b','missing']}})
 const rows=csvRows(exporter.requirementCSV([item],[{key:'testers',name:'Tester labels',type:'users',enabled:true}],members,['assignee','owner','role.frontend.userId','role.frontend.value','cf.testers']))
 assert.deepEqual(rows[1].slice(2),['Same、Same、Former member','Legacy owner','Same、Same','20','Same、missing'])
})
await test('CSV keeps zero, false, null, localized status names and saved timestamps distinct',()=>{
 const item=requirement(1,{roleWeights:{ui:{value:0}},weightTotal:0,sensitive:false,authImpact:null,status:'opaque-status',statusName:'自定义状态',createdAt:'2026-09-03T00:00:00Z',tags:'蓝色，绿色\n红色'})
 const row=csvRows(exporter.requirementCSV([item],[],[],['role.ui.value','weightTotal','sensitive','authImpact','status','createdAt','tags']))[1]
 assert.deepEqual(row.slice(2),['0','0','English:否','','自定义状态','Date:2026-09-03T00:00:00Z','蓝色、绿色、红色'])
})
await test('cross-project CSV uses each row’s member directory and custom field type without duplicate columns',()=>{
 const left={definitions:[{key:'shared',name:'人员',type:'users',enabled:true}],members:[{id:'same-id',name:'Left member'}]},right={definitions:[{key:'shared',name:'备注原文',type:'text',enabled:true}],members:[{id:'same-id',name:'Right member'}]},contexts=new Map([['left',left],['right',right]])
 const rows=csvRows(exporter.requirementCSV([requirement(1,{projectId:'left',assigneeUserIds:['same-id'],customFields:{shared:['same-id']}}),requirement(2,{projectId:'right',assigneeUserIds:['same-id'],customFields:{shared:'same-id'}})],[...left.definitions,...right.definitions],[...left.members,...right.members],['assignee','cf.shared'],contexts))
 assert.deepEqual(rows[0],['English:编号','English:标题','English:处理人','人员 / 备注原文']);assert.deepEqual(rows[1].slice(2),['Left member','Left member']);assert.deepEqual(rows[2].slice(2),['Right member','same-id'])
})
await test('download helper creates a local Blob URL and releases it after the browser receives the download click',()=>{
 exporter.downloadFile(new Blob(['test']),'safe.csv');assert.equal(created.length,1);assert.deepEqual(clicks,[{href:'blob:local-1',download:'safe.csv'}]);assert.deepEqual(released,[]);assert.equal(timers[0].delay,30000);timers[0].fn();assert.deepEqual(released,['blob:local-1'])
})
await test('bulk editing requires explicit confirmation and only touches the selection captured when opening',async()=>{
 const m=bulkFixture();m.begin();m.props.items=[requirement(99)];m.field.value='assigneeUserIds';m.people.value=['stable-member'];await m.execute();assert.equal(m.calls.length,0);m.confirmed.value=true;await m.execute();assert.deepEqual(m.calls.map(call=>call.path),['/requirements/7','/requirements/8']);for(const call of m.calls)assert.deepEqual(JSON.parse(call.options.body),{assigneeUserIds:['stable-member']});assert.equal(m.progress.value,2);assert.equal(m.confirmed.value,false);m.stop()
})
await test('bulk failures are reported per item, do not silently retry and do not prevent independent selected items from completing',async()=>{
 const m=bulkFixture(async path=>{if(path.endsWith('/7'))throw Error('Conflict');return {}});m.begin();m.confirmed.value=true;await m.execute();assert.equal(m.calls.length,2);assert.deepEqual(m.results.value.map(item=>[item.id,item.ok]),[[7,false],[8,true]]);assert.equal(m.results.value[0].message,'Conflict');assert.equal(m.busy.value,false);assert(m.events.some(([event])=>event==='changed'));m.stop()
})
await test('bulk transitions use the intersection of server-authorized targets, and read-only scope blocks writes',async()=>{
 const m=bulkFixture(async path=>({allowedTransitions:path.includes('/7/')?['a','b']:['b','c']}));m.begin();m.action.value='status';await m.changeAction();assert.deepEqual(m.allowed.value,['b']);m.value.value='b';m.confirmed.value=true;m.locked.value=true;await m.execute();assert.equal(m.calls.length,2);m.stop()
})
await test('bulk payloads are frozen during execution and completed batches cannot accidentally run twice',async()=>{
 const pending=deferred(),m=bulkFixture(async path=>path.endsWith('/7')?pending.promise:{});m.begin();m.field.value='assigneeUserIds';m.people.value=['original-person'];m.confirmed.value=true;const operation=m.execute();m.field.value='remarks';m.value.value='late text';m.people.value=['later-person'];m.props.items=[requirement(99)];await m.execute();m.close();assert.equal(m.opened.value,true);pending.resolve({});await operation
 assert.equal(m.calls.length,2);for(const call of m.calls)assert.deepEqual(JSON.parse(call.options.body),{assigneeUserIds:['original-person']});m.confirmed.value=true;await m.execute();assert.equal(m.calls.length,2);m.stop()
})
await test('pending transition checks cannot close or replace the batch and late results after unmount do not populate targets',async()=>{
 const pending=deferred(),m=bulkFixture(async()=>pending.promise);m.begin();m.action.value='status';const request=m.changeAction();m.close();assert.equal(m.opened.value,true);m.props.items=[requirement(99)];m.begin();assert.deepEqual(m.targets.value.map(item=>item.id),[7,8]);m.stop();pending.resolve({allowedTransitions:['sensitive-status']});await request;assert.deepEqual(m.allowed.value,[]);assert.equal(m.calls.length,1)
})
await test('copies use a new identity and omit source comments, attachments, parent linkage and workflow status',async()=>{
 const source=requirement(7,{description:'Saved text',status:'已上线',parentId:3,descriptionDoc:{type:'doc'},comments:['private comment'],attachments:[{id:1}],customFields:{score:20}}),m=bulkFixture(async(path,options)=>options?requirement(99):source,{items:[requirement(7)]});m.begin();m.action.value='copy';await m.changeAction();m.confirmed.value=true;await m.execute();const body=JSON.parse(m.calls.find(call=>call.options?.method==='POST').options.body);assert.equal(body.description,'Saved text');assert.deepEqual(body.customFields,{score:20});for(const key of ['id','code','status','parentId','comments','attachments','descriptionDoc'])assert.equal(Object.hasOwn(body,key),false);m.stop()
})
await test('copied links use fixed internal routes and explicit current project scope without mutating any requirement',async()=>{
 const m=bulkFixture(undefined,{items:[requirement(7,{url:'https://untrusted.test'})]});m.begin();m.action.value='links';await m.changeAction();m.confirmed.value=true;await m.execute();assert.equal(m.calls.length,0);assert.equal(m.clipboard[0],'https://devflow.test/requirements?req=7&project=p');m.stop()
})
await test('new search, AI and defect-related components compile with constrained narrow-screen controls',()=>{
 for(const file of ['TopSearch.vue','RequirementAITestCases.vue','DefectComposer.vue','RequirementPicker.vue','RequirementLinks.vue']){const path='src/components/'+file,{descriptor}=parse(read(path));const script=compileScript(descriptor,{id:file});assert.deepEqual(compileTemplate({source:descriptor.template.content,filename:path,id:file,compilerOptions:{bindingMetadata:script.bindings}}).errors,[],file);assert.match(read(path),/@media\(max-width:(?:640|760)px\)/,file)}
 assert.match(read('src/components/TopSearch.vue'),/left:0;right:0;width:100%;max-width:100%/);assert.match(read('src/components/RequirementPicker.vue'),/min-width:0;max-width:100%/);assert.match(read('src/views/AISettings.vue'),/footer :deep\(button\)\{flex:1;white-space:normal/)
})
console.log(`Passed ${count} bulk action, CSV export and responsive component regressions.`)

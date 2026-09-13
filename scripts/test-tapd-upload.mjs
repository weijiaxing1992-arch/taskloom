import assert from 'node:assert/strict'
import fs from 'node:fs'
import ts from 'typescript'
import * as Vue from 'vue'
import {parse,compileScript,compileTemplate} from 'vue/compiler-sfc'
const read=p=>fs.readFileSync(new URL('../'+p,import.meta.url),'utf8')
const transpile=s=>ts.transpileModule(s,{compilerOptions:{module:ts.ModuleKind.CommonJS,target:ts.ScriptTarget.ES2022}}).outputText
const helper={};new Function('exports',transpile(read('src/tapdImport.ts')))(helper)
const mentions={};new Function('exports',transpile(read('src/mentions.ts')))(mentions)
const rich={};new Function('require','exports',transpile(read('src/richText.ts')))(id=>({'./mentions':mentions})[id],rich)
const {descriptor}=parse(read('src/components/TapdImport.vue'))
const compiled=compileScript(descriptor,{id:'tapd-test'})
assert.deepEqual(compileTemplate({source:descriptor.template.content,filename:'TapdImport.vue',id:'tapd-test',compilerOptions:{bindingMetadata:compiled.bindings}}).errors,[])
const result={title:'需求',description:'正文',displayId:'123',sourceId:'123',workspaceId:'321',fileName:'需求.pdf',pageCount:1,images:[],rawText:'原文',warnings:[],fields:[{label:'处理人',value:'张佳琪;李锐鸿'},{label:'UI 奖励',value:'80'}]}
const file=new File([new Uint8Array(100)],'需求.pdf',{type:'application/pdf'})
function harness(reader=async()=>result,{responses={},post=async()=>({id:1,code:'REQ-0001'})}={}){
 const effect=Vue.effectScope(),ends=[],calls=[],reads=[],emitted=[],locked=Vue.ref(false),module={}
 const request=async(path,options)=>{calls.push({path,options});if(options?.method)return post(path,options);if(path in responses)return responses[path];if(path==='/members')return{items:[{id:'front',name:'张佳琪',active:true,isCurrent:false,projectRoles:['frontend']},{id:'back',name:'李锐鸿',active:true,isCurrent:false,projectRoles:['backend']}]};if(path==='/requirement-categories'||path==='/requirement-statuses')return{items:[],canManage:true};return{items:[]}}
 const imports={vue:{...Vue,onBeforeUnmount:fn=>ends.push(fn)},'../i18n':{t:x=>x},'../tapdImport':helper,'../mentions':mentions,'../tapdPdf':{readTapdPdf:async(...args)=>{reads.push(args);return reader(...args)}},'../richText':rich,'../requirementFields':{normalizeSprints:x=>x,sprintSelectable:()=>true},'./settingsScope':{useSettingsScope:()=>({locked,current:()=>!locked.value,request})}}
 new Function('require','exports',transpile(compiled.content))(id=>imports[id]||{default:{}},module)
 const state=effect.run(()=>module.default.setup({}, {expose(){},emit(...args){emitted.push(args)}}))
 return{...state,calls,reads,emitted,locked,end(){ends.forEach(fn=>fn());effect.stop()}}
}
const a=harness();await a.open();assert.equal(a.members.value.length,2)
await a.drop({dataTransfer:{files:[file]}})
assert.equal(a.parsed.value.title,'需求');assert.deepEqual(a.rows.value[0].value,['front','back']);assert.equal(a.rows.value[0].issue,'')
assert.equal(a.shownRows.value.length,1);a.showArchived.value=true;assert.equal(a.shownRows.value.length,2)
const previous=a.parsed.value
for(const files of [[file,file],[{name:'x.txt',size:100}],[{name:'empty.pdf',size:0}]]){await a.drop({dataTransfer:{files}});assert(a.error.value);assert.equal(a.parsed.value,previous)}
assert.equal(a.reads.length,1)
a.dragEnter();a.dragEnter();a.dragLeave();assert.equal(a.dragDepth.value,1);a.dragLeave();a.dragLeave();assert.equal(a.dragDepth.value,0)
a.saving.value=true;await a.drop({dataTransfer:{files:[file]}});assert.equal(a.reads.length,1);a.close();assert.equal(a.opened.value,true);a.saving.value=false
a.reviewed.value=false;await a.submit();assert.equal(a.calls.filter(x=>x.options?.method).length,0)
a.locked.value=true;await a.drop({dataTransfer:{files:[file]}});assert.equal(a.reads.length,1);a.end()
let release
const b=harness(()=>new Promise(resolve=>{release=resolve}));await b.open()
const pending=b.selectFiles([file]);for(let i=0;i<8;i++)await Promise.resolve()
await b.drop({dataTransfer:{files:[file]}});assert.equal(b.reads.length,1)
b.close();assert.equal(b.reads[0][1].aborted,true);release(result);await pending;assert.equal(b.parsed.value,null);assert.equal(b.opened.value,false);b.end()
const c=harness();await c.open();await c.upload({target:{files:[file]}});assert.equal(c.reads.length,1);assert(c.parsed.value);c.end()
const document={type:'doc',content:[{type:'heading',attrs:{level:2},content:[{type:'text',text:'规则'}]},{type:'orderedList',attrs:{start:1},content:[{type:'listItem',content:[{type:'paragraph',content:[{type:'text',text:'同一规则的换行已合并'}]}]}]},{type:'image',attrs:{name:'TAPD-image-1-1.png',data:'AQID',alt:'独立原图'}}]}
const d=harness(async()=>({...result,descriptionDoc:document,imageCount:1}));await d.open();await d.selectFiles([file]);d.reviewed.value=true;d.mediaBusy.value=true;await d.submit();assert.equal(d.calls.filter(x=>x.options?.method).length,0);d.mediaBusy.value=false;await d.submit()
const saved=JSON.parse(d.calls.find(x=>x.options?.method).options.body)
assert.deepEqual(saved.descriptionDoc,document)
assert.equal(saved.tapdImport.descriptionDoc,undefined);assert.equal(saved.tapdImport.imageCount,undefined)
assert(!JSON.stringify(saved).includes('TAPD-page-'));assert(!JSON.stringify(saved).includes('保真参考'))
assert.equal(d.pdfUrl.value,'');d.end()
console.log('TAPD mounted upload/drop parity, project name matching, preserved invalid-file draft, busy/scope guards and cancellation passed.')

// A valid filename can still be a corrupt PDF. A failed replacement preserves every edit and its preview URL.
let replacementFailure=true
const replacement=harness(async()=>{if(replacement.reads.length>1&&replacementFailure)throw Error('损坏的 PDF');return result})
await replacement.open();await replacement.selectFiles([file]);replacement.title.value='用户修订标题';replacement.descriptionDoc.value=document;replacement.reviewed.value=true
const draft=replacement.parsed.value,oldUrl=replacement.pdfUrl.value,oldRows=replacement.rows.value
await replacement.selectFiles([new File(['broken'],'损坏.pdf')])
assert.match(replacement.error.value,/损坏/);assert.equal(replacement.parsed.value,draft);assert.equal(replacement.title.value,'用户修订标题');assert.deepEqual(replacement.descriptionDoc.value,document);assert.equal(replacement.pdfUrl.value,oldUrl);assert.equal(replacement.rows.value,oldRows);assert.equal(replacement.reviewed.value,true)
replacementFailure=false;await replacement.selectFiles([file]);assert.notEqual(replacement.pdfUrl.value,oldUrl);assert.equal(replacement.title.value,'需求');assert.equal(replacement.reviewed.value,false);replacement.end()

// Permission for categories is broader than permission to import. Use the same status-management capability as the write API.
const denied=harness(undefined,{responses:{'/requirement-statuses':{items:[],canManage:false}}});await denied.open();assert.match(denied.error.value,/管理员/);assert.equal(denied.targets.value.length,0);await denied.selectFiles([file]);assert.equal(denied.reads.length,0);denied.end()

const constrained=harness(async()=>({...result,fields:[{label:'测试人员',value:'张佳琪;李锐鸿'}]}),{responses:{
 '/members':{items:[{id:'front',name:'张佳琪',active:true,projectRoles:['frontend'],departmentIds:['quality']},{id:'back',name:'李锐鸿',active:true,projectRoles:['backend','qa'],departmentIds:['quality']},{id:'other',name:'其他测试',active:true,projectRoles:['qa'],departmentIds:['other']}]},
 '/field-definitions?objectType=requirement':{items:[{key:'testers',name:'测试人员',type:'users',enabled:true,memberRoles:['qa'],departmentId:'quality'}]},
 '/requirement-statuses':{items:[{key:'backend_done',name:'后端已完成',enabled:true}],canManage:true},
}})
await constrained.open();await constrained.selectFiles([file]);const target=constrained.targets.value.find(x=>x.key==='cf.testers'),row=constrained.rows.value[0]
assert.deepEqual(target.memberIds,['back']);assert.deepEqual(target.memberRoles,['qa']);assert.equal(target.departmentId,'quality');assert.deepEqual(row.value,['back']);assert.match(row.issue,/张佳琪.*角色或部门/)
row.value=['other'];constrained.changed(row);assert.match(row.issue,/角色、部门或项目/);row.value=['back'];constrained.changed(row);assert.equal(row.issue,'')
assert(constrained.targets.value.find(x=>x.key==='status').options.some(x=>x.value==='backend_done'));constrained.end()

// Network errors leave the edited structured document intact; duplicate clicks and reopening during submission do not create requests.
let finishPost,postCount=0
const retry=harness(undefined,{post:async()=>{postCount++;if(postCount===1)return new Promise((resolve,reject)=>{finishPost={resolve,reject}});return{id:4,code:'REQ-0004',alreadyImported:true}}})
await retry.open();await retry.selectFiles([file]);retry.descriptionDoc.value=document;retry.reviewed.value=true
const firstSubmit=retry.submit();await retry.submit();await retry.open();assert.equal(postCount,1);assert(retry.parsed.value);finishPost.reject(Error('网络断开'));await firstSubmit
assert.match(retry.error.value,/网络断开/);assert.deepEqual(retry.descriptionDoc.value,document);assert.equal(retry.reviewed.value,true);assert.equal(retry.saving.value,false)
await retry.submit();assert.equal(postCount,2);assert.deepEqual(retry.emitted,[['imported',4]]);assert.match(retry.notice.value,/未重复创建/);retry.end()

// Scope invalidation aborts local parsing too, and late writes cannot open a requirement or show success in a different project/account.
let scopeRead
const switched=harness(()=>new Promise(resolve=>{scopeRead=resolve}));await switched.open();const reading=switched.selectFiles([file]);for(let i=0;i<8;i++)await Promise.resolve()
switched.locked.value=true;assert.equal(switched.reads[0][1].aborted,true);assert.equal(switched.loading.value,false);scopeRead(result);await reading;assert.equal(switched.parsed.value,null);assert.equal(switched.error.value,'');switched.end()
for(const invalidate of [h=>{h.locked.value=true},h=>h.end()]){
 let finish;const late=harness(undefined,{post:()=>new Promise(resolve=>{finish=resolve})});await late.open();await late.selectFiles([file]);late.reviewed.value=true;const saving=late.submit();invalidate(late);finish({id:99,code:'REQ-0099'});await saving;assert.deepEqual(late.emitted,[]);assert.equal(late.notice.value,'');assert.equal(late.error.value,'');late.end()
}
const oversized=harness(async()=>({...result,pdfBase64:'A'.repeat(30*1024*1024)}));await oversized.open();await oversized.selectFiles([file]);oversized.reviewed.value=true;await oversized.submit();assert.match(oversized.error.value,/30 MiB/);assert.equal(oversized.calls.filter(x=>x.options?.method).length,0);assert(oversized.parsed.value);oversized.end()
console.log('TAPD corrupt replacement recovery, permission parity, role/department intersections, edited rich-document retries, duplicate submission, scope invalidation and request limits passed.')

// Reset the editor (and its undo/asset history) only for a successfully replaced PDF.
assert.match(descriptor.template.content,/<RichTextEditor :key="pdfUrl"/)
const hugeDocument={type:'doc',content:Array.from({length:2500},()=>({type:'paragraph',content:[{type:'text',text:'保留全文'}]}))}
const tooManyNodes=harness(async()=>({...result,descriptionDoc:hugeDocument}));await tooManyNodes.open();await tooManyNodes.selectFiles([file]);assert.match(tooManyNodes.error.value,/结构超过/);assert.equal(tooManyNodes.parsed.value,null);tooManyNodes.end()
const longText=harness();await longText.open();await longText.selectFiles([file]);longText.descriptionDoc.value={type:'doc',content:[{type:'paragraph',content:[{type:'text',text:'字'.repeat(100001)}]}]};longText.reviewed.value=true;await longText.submit();assert.match(longText.error.value,/100000/);assert.equal(longText.calls.filter(x=>x.options?.method).length,0);assert.equal(longText.descriptionDoc.value.content[0].content[0].text.length,100001);longText.end()
assert.equal(helper.tapdDocumentError({type:'doc',content:[]},'😀'.repeat(100000)),'')
console.log('TAPD per-file editor identity and non-truncating document structure/text limits passed.')

import assert from 'node:assert/strict'
import fs from 'node:fs'
import ts from 'typescript'
import * as Vue from 'vue'
import {parse,compileScript,compileTemplate} from 'vue/compiler-sfc'
const read=p=>fs.readFileSync(new URL('../'+p,import.meta.url),'utf8')
const transpile=s=>ts.transpileModule(s,{compilerOptions:{module:ts.ModuleKind.CommonJS,target:ts.ScriptTarget.ES2022}}).outputText
const helper={};new Function('exports',transpile(read('src/tapdImport.ts')))(helper)
const {descriptor}=parse(read('src/components/TapdImport.vue'))
const compiled=compileScript(descriptor,{id:'tapd-test'})
assert.deepEqual(compileTemplate({source:descriptor.template.content,filename:'TapdImport.vue',id:'tapd-test',compilerOptions:{bindingMetadata:compiled.bindings}}).errors,[])
const result={title:'需求',description:'正文',displayId:'123',sourceId:'123',workspaceId:'321',fileName:'需求.pdf',pageCount:1,images:[],rawText:'原文',warnings:[],fields:[{label:'处理人',value:'张佳琪;李锐鸿'},{label:'UI 奖励',value:'80'}]}
const file={name:'需求.pdf',size:100}
function harness(reader=async()=>result){
 const effect=Vue.effectScope(),ends=[],calls=[],reads=[],locked=Vue.ref(false),module={}
 const request=async(path,options)=>{calls.push({path,options});if(options?.method)return{id:1,code:'REQ-0001'};if(path==='/members')return{items:[{id:'front',name:'张佳琪',active:true,isCurrent:false},{id:'back',name:'李锐鸿',active:true,isCurrent:false}]};if(path==='/requirement-categories')return{items:[],canManage:true};return{items:[]}}
 const imports={vue:{...Vue,onBeforeUnmount:fn=>ends.push(fn)},'../i18n':{t:x=>x},'../tapdImport':helper,'../tapdPdf':{readTapdPdf:async(...args)=>{reads.push(args);return reader(...args)}},'../richText':{plainTextToDocument:()=>({type:'doc',content:[]})},'../requirementFields':{normalizeSprints:x=>x,sprintSelectable:()=>true},'./settingsScope':{useSettingsScope:()=>({locked,current:()=>!locked.value,request})}}
 new Function('require','exports',transpile(compiled.content))(id=>imports[id]||{default:{}},module)
 const state=effect.run(()=>module.default.setup({}, {expose(){},emit(){}}))
 return{...state,calls,reads,locked,end(){ends.forEach(fn=>fn());effect.stop()}}
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
console.log('TAPD mounted upload/drop parity, project name matching, preserved invalid-file draft, busy/scope guards and cancellation passed.')

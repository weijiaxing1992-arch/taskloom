import assert from 'node:assert/strict'
import {readFileSync} from 'node:fs'
import ts from 'typescript'
import * as Vue from 'vue'
const source=readFileSync(new URL('../src/components/RequirementRefinement.vue',import.meta.url),'utf8').match(/<script setup[^>]*>([\s\S]*?)<\/script>/)[1]
const sample={background:'背景',rules:'规则',exceptions:'异常',acceptance:'验收',questions:'待确认'}
function fixture(handler=async()=>({preview:sample,model:'mock'})){
 const props=Vue.reactive({description:'描述',acceptance:'原验收',disabled:false}),events=[],calls=[],scope=Vue.effectScope(),result={},locked=Vue.ref(false)
 const imports={vue:{...Vue,onBeforeUnmount:()=>{}},'./settingsScope':{useSettingsScope:()=>({locked,current:()=>!locked.value,request:async(...args)=>{calls.push(args);return handler(...args)}})}}
 const code=ts.transpileModule(source+'\nexport {generate,apply,preview,selected,consent,busy,error}',{compilerOptions:{module:ts.ModuleKind.CommonJS,target:ts.ScriptTarget.ES2022}}).outputText
 scope.run(()=>new Function('require','exports','defineProps','defineEmits',code)(id=>imports[id]||{},result,()=>props,()=> (...args)=>events.push(args)))
 return {...result,props,events,calls,scope,locked}
}
const f=fixture();await f.generate();assert.equal(f.calls.length,0);f.consent.value=true;await f.generate();assert.equal(f.calls.length,1);assert.deepEqual(f.selected.value,[]);assert(!f.events.some(e=>e[0]==='apply'));f.selected.value=['rules','acceptance'];f.apply();assert.deepEqual(f.events.find(e=>e[0]==='apply')[1],{description:'功能规则\n规则',acceptance:'验收'});assert.equal(f.preview.value,null);f.scope.stop()
let resolve;const g=fixture(()=>new Promise(r=>resolve=r));g.consent.value=true;const task=g.generate();g.props.description='已修改';await Vue.nextTick();resolve({preview:sample});await task;assert.equal(g.preview.value,null);assert(!g.events.some(e=>e[0]==='apply'));g.scope.stop()
const h=fixture(async()=>{throw Error('未配置')});h.consent.value=true;await h.generate();assert.equal(h.error.value,'未配置');assert.equal(h.busy.value,false);assert.equal(h.props.description,'描述');h.scope.stop()
console.log('AI 完善：外发同意、分段采用、不自动写入、过期响应隔离、失败保留输入检查通过。')

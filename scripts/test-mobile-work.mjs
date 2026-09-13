import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import ts from 'typescript'
import * as Vue from 'vue'
const read = path => readFileSync(new URL('../'+path,import.meta.url),'utf8')
const source = read('src/views/Search.vue').match(/<script setup[^>]*>([\s\S]*?)<\/script>/)[1]
const code=ts.transpileModule(source+'\nexport {search,q,items,loading,error,compositionStart,compositionEnd,submitSearch}',{compilerOptions:{module:ts.ModuleKind.CommonJS,target:ts.ScriptTarget.ES2022}}).outputText
const recentSearches={}
new Function('exports',ts.transpileModule(read('src/recentSearches.ts'),{compilerOptions:{module:ts.ModuleKind.CommonJS,target:ts.ScriptTarget.ES2022}}).outputText)(recentSearches)
function setup(){
 const scope=Vue.effectScope(),exports={},calls=[],timers=new Map();let seq=0,unmount
 const imports={vue:{...Vue,onMounted(){},onBeforeUnmount(fn){unmount=fn}},'vue-router':{useRoute:()=>({query:{}})},'../api':{api:(path,options)=>new Promise((resolve,reject)=>calls.push({path,options,resolve,reject}))},'../i18n':{t:x=>x,locale:Vue.ref('zh-CN'),formatDate:x=>x},'../requirementWorkflow':{},'../recentSearches':recentSearches}
 scope.run(()=>new Function('require','exports','setTimeout','clearTimeout','window',code)(id=>imports[id]||{},exports,fn=>{timers.set(++seq,fn);return seq},id=>timers.delete(id),{removeEventListener(){}}))
 return {...exports,calls,flush(){const pending=[...timers.values()];timers.clear();pending.forEach(fn=>fn())},stop(){unmount();scope.stop()}}
}
const tick=()=>new Promise(resolve=>setImmediate(resolve))
let count=0
async function test(name,run){await run();count++;console.log('✓ '+name)}
await test('typing cancels older requests before debounce and late results cannot overwrite the new query',async()=>{
 const m=setup(),first=m.search();m.q.value='前端';assert(m.calls[0].options.signal.aborted);m.calls[0].resolve({items:[{title:'old'}],total:1});await first;assert.deepEqual(m.items.value,[])
 m.flush();assert.equal(m.calls.length,2);assert.equal(new URLSearchParams(m.calls[1].path.split('?')[1]).get('q'),'前端');m.calls[1].resolve({items:[{title:'new'}],total:1});await tick();assert.equal(m.items.value[0].title,'new');assert.equal(m.loading.value,false);m.stop()
})
await test('Chinese composition makes no partial requests; keyboard search dispatches the final text once',async()=>{
 const m=setup();m.compositionStart();m.q.value='chan';m.flush();m.submitSearch({isComposing:true});assert.equal(m.calls.length,0)
 m.q.value='产品';m.compositionEnd();m.submitSearch({isComposing:false});m.flush();assert.equal(m.calls.length,1);m.calls[0].resolve({items:[],total:0});await tick();m.stop()
})
await test('unmount aborts in-flight searches and rejects late UI updates',async()=>{
 const m=setup(),pending=m.search();m.stop();assert(m.calls[0].options.signal.aborted);m.calls[0].resolve({items:[{id:'late'}],total:1});await pending;assert.deepEqual(m.items.value,[])
})
await test('a current network failure remains actionable and retry can recover',async()=>{
 const m=setup(),pending=m.search();m.calls[0].reject(Error('offline'));await pending;assert.equal(m.error.value,'offline');assert.equal(m.loading.value,false)
 const retry=m.search();m.calls[1].resolve({items:[{id:1}],total:1});await retry;assert.equal(m.error.value,'');assert.equal(m.items.value.length,1);m.stop()
})
await test('mobile filter panels keep desktop controls, explicit expanded states and reset actions',()=>{
 for(const [path,id] of [['MyWork','work'],['Notifications','notice']]){
  const body=read('src/views/'+path+'.vue');assert(body.includes('aria-controls="'+id+'-advanced-filters"'));assert(body.includes(':aria-expanded="filtersExpanded"'));assert(body.includes("t('重置筛选')"))
 }
 const css=read('src/mobile-refinements.css');assert(css.includes('.mobile-advanced-filters{display:contents}'));assert(css.includes('.mobile-advanced-filters.is-expanded{display:grid'));assert(css.includes('.app-shell .btn.mobile-filter-toggle{display:none!important}'));assert(!read('src/views/Search.vue').includes('autofocus'))
})
await test('quick navigation uses native internal links and live unread prop, and admin config is discoverable without widening permission',()=>{
 const nav=read('src/components/MobileWorkNavigation.vue');for(const route of ['/my-work','/requirements','/iterations','/notifications'])assert(nav.includes(route));assert(nav.includes('<RouterLink'));assert(nav.includes('env(safe-area-inset-bottom)'));assert(!nav.includes('position:fixed'))
 const admin=read('src/views/Organization.vue');assert(admin.includes('context?.isTenantAdmin&&section!==\'wecom-app\'&&!scope.locked.value'));assert(admin.includes('class="btn org-wechat-entry" to="/organization/wecom-app"'))
})
console.log(`Passed ${count} mobile work and live search regressions.`)

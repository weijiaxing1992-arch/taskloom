import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import ts from 'typescript'
import * as Vue from 'vue'
import { createRouter, createMemoryHistory } from 'vue-router'
const read = path => readFileSync(new URL('../'+path, import.meta.url), 'utf8')
const timers = new Map(); let timerID = 0
const exports = {}
new Function('require','exports','setTimeout','clearTimeout',ts.transpileModule(read('src/navigationFeedback.ts'),{compilerOptions:{module:ts.ModuleKind.CommonJS,target:ts.ScriptTarget.ES2022}}).outputText)(
  () => Vue, exports, (fn,delay)=>{ assert.equal(delay,160);timers.set(++timerID,fn);return timerID }, id=>timers.delete(id),
)
const flush = async () => { for(let i=0;i<20;i++)await Promise.resolve() }
const tick = () => { const pending=[...timers.values()];timers.clear();pending.forEach(fn=>fn()) }
const deferred=()=>{let resolve,reject;const promise=new Promise((a,b)=>{resolve=a;reject=b});return {promise,resolve,reject}}
let count=0
async function test(name,run){await run();count++;console.log('✓ '+name)}
const Page={render:()=>null}
await test('slow route shows delayed progress while the old route remains, then clears on success',async()=>{
  const pending=deferred(),router=createRouter({history:createMemoryHistory(),routes:[{path:'/',component:Page},{path:'/slow',component:()=>pending.promise}]})
  await router.push('/');const feedback=exports.createNavigationFeedback(router)
  const navigation=router.push('/slow');await flush();assert.equal(feedback.state.busy,false);assert.equal(router.currentRoute.value.path,'/')
  tick();assert.equal(feedback.state.busy,true);pending.resolve(Page);await navigation;assert.equal(feedback.state.busy,false);assert.equal(feedback.state.failed,false);feedback.dispose()
})
await test('chunk failure keeps the original route; explicit retry opens exactly the failed internal target',async()=>{
  let calls=0;const router=createRouter({history:createMemoryHistory(),routes:[{path:'/',component:Page},{path:'/work',component:()=>++calls===1?Promise.reject(Error('private detail must not be shown')):Promise.resolve(Page)}]})
  await router.push('/');const feedback=exports.createNavigationFeedback(router)
  await assert.rejects(router.push('/work?q=kept'));assert.equal(router.currentRoute.value.path,'/');assert.equal(feedback.state.failed,true);assert.equal(feedback.state.busy,false)
  assert(!JSON.stringify(feedback.state).includes('private detail'))
  await feedback.retry();assert.equal(router.currentRoute.value.fullPath,'/work?q=kept');assert.equal(calls,2);assert.equal(feedback.state.failed,false);feedback.dispose()
})
await test('navigation cancellation and same-page filters never leave a loading indicator',async()=>{
  const router=createRouter({history:createMemoryHistory(),routes:[{path:'/',component:Page},{path:'/blocked',component:Page,beforeEnter:()=>false}]})
  await router.push('/');const feedback=exports.createNavigationFeedback(router)
  await router.push('/blocked');tick();assert.equal(feedback.state.busy,false);assert.equal(feedback.state.failed,false)
  await router.push('/?q=filter');tick();assert.equal(feedback.state.busy,false);assert.equal(router.currentRoute.value.query.q,'filter');feedback.dispose()
})
await test('memoized import failures offer only an explicitly confirmed internal-page reload',async()=>{
  const router=createRouter({history:createMemoryHistory(),routes:[{path:'/',component:Page},{path:'/cached',component:()=>Promise.reject(Error('module unavailable'))}]})
  await router.push('/');const f=exports.createNavigationFeedback(router),opened=[]
  await assert.rejects(router.push('/cached?q=kept'));assert.equal(f.state.reloadSuggested,false)
  f.reopen(()=>true,path=>opened.push(path));assert.deepEqual(opened,[])
  await f.retry();assert.equal(f.state.reloadSuggested,true);assert.equal(router.currentRoute.value.fullPath,'/')
  f.reopen(()=>false,path=>opened.push(path));assert.deepEqual(opened,[])
  f.reopen(()=>true,path=>opened.push(path));assert.deepEqual(opened,['/cached?q=kept'])
  f.dismiss();f.reopen(()=>true,path=>opened.push(path));assert.equal(opened.length,1);f.dispose()
})
await test('obsolete errors cannot overwrite a later navigation; disposal removes hooks and delayed work',()=>{
  let before,after,error,removed=0;const router={beforeEach:fn=>(before=fn,()=>removed++),afterEach:fn=>(after=fn,()=>removed++),onError:fn=>(error=fn,()=>removed++),push:()=>{throw Error('unexpected')}}
  const f=exports.createNavigationFeedback(router),root={path:'/',fullPath:'/'}
  before({path:'/a',fullPath:'/a'},root);before({path:'/b',fullPath:'/b'},root);error(Error('old'),{fullPath:'/a'});tick();assert.equal(f.state.failed,false);assert.equal(f.state.busy,true)
  after({fullPath:'/b'});assert.equal(f.state.busy,false);before({path:'/c',fullPath:'/c'},root);f.dispose();tick();assert.equal(f.state.busy,false);assert.equal(removed,3)
})
await test('unknown addresses have a helpful route, while keyboard users can skip repeated navigation',()=>{
  assert.match(read('src/main.ts'),/path:'\/:pathMatch\(\.\*\)\*',component:NotFound/)
  const page=read('src/views/NotFound.vue');assert(page.includes('to="/my-work"'));assert(page.includes('to="/search"'));assert(!page.includes('location.reload'))
  const app=read('src/App.vue');assert(app.indexOf('class="workspace-skip-link"')<app.indexOf('<aside id="primary-navigation"'));assert(app.includes('id="workspace-content" ref="workspaceContent" tabindex="-1" :inert="identityConflict"'))
  assert.match(read('src/components/RouteFeedback.vue'),/prefers-reduced-motion:reduce/)
  assert.doesNotMatch(read('src/components/RouteFeedback.vue'),/window\.confirm\(/)
  assert.match(read('src/components/RouteFeedback.vue'),/feedback.reopen\(\(\) => confirming.value/)
})
console.log(`Passed ${count} navigation recovery and accessibility regressions.`)

import { readFileSync } from 'node:fs'
import { createRequire } from 'node:module'
import assert from 'node:assert/strict'
const require=createRequire(import.meta.url),ts=require('typescript'),Vue=require('vue')
const read=path=>readFileSync(new URL('../'+path,import.meta.url),'utf8')
const t=source=>source
function module(source,imports={},extras={}){
 const exports={},code=ts.transpileModule(source,{compilerOptions:{module:ts.ModuleKind.CommonJS,target:ts.ScriptTarget.ES2022}}).outputText
 new Function('require','exports',...Object.keys(extras),code)(id=>imports[id]||{},exports,...Object.values(extras));return exports
}
function component(path,props,expose,handler=async()=>({items:[]}),options={}){
 const events=[],calls=[],mounts=[],unmounts=[],scope=Vue.effectScope(),api=async(path,options)=>{calls.push({path,options});return handler(path,options)}
 const source=read(path).match(/<script setup[^>]*>([\s\S]*?)<\/script>/)[1]
 let value;scope.run(()=>value=module(source+`\nexport {${expose.join(',')}}`,{vue:{...Vue,onMounted:fn=>mounts.push(fn),onBeforeUnmount:fn=>unmounts.push(fn)},'../api':{api},'../i18n':{t},'./settingsScope':{useSettingsScope:()=>({current:()=>true,locked:Vue.ref(false),request:api}),useSettingsDialog:()=>Vue.ref(null)},...options.imports}, {defineProps:()=>props,defineEmits:()=>((...args)=>{events.push(args);options.onEmit?.(...args)})}))
 return {...value,events,calls,mounts,stop:()=>{for(const fn of unmounts)fn();scope.stop()}}
}
const deferred=()=>{let resolve,reject;const promise=new Promise((a,b)=>{resolve=a;reject=b});return {promise,resolve,reject}}
let count=0;async function test(name,run){await run();count++;console.log('✓ '+name)}
await test('font preferences apply precise independent scale and reject prototype keys',()=>{
 const styles=new Map(),m=module(read('src/displayPreferences.ts'),{vue:Vue},{document:{documentElement:{style:{setProperty:(key,value)=>styles.set(key,value)}}}})
 m.applyDisplayPreferences({fontSize:'extraLarge'});assert.equal(styles.get('--devflow-font-scale'),'1.25')
 m.applyDisplayPreferences({fontSize:'constructor'});assert.equal(styles.get('--devflow-font-scale'),'1')
 m.applyDisplayPreferences({fontSize:'small'});assert.equal(m.fontSize.value,'small')
})
await test('detail field visibility is non-destructive and saves only enabled definitions',async()=>{
 const props={modelValue:{basicFields:['status','owner'],customFieldKeys:['enabled','removed']},definitions:[{key:'enabled',name:'New label'}]}
 const m=component('src/components/RequirementDetailPreferences.vue',props,['open','basic','custom','save','opened'],async(path,options)=>JSON.parse(options.body))
 m.open();assert.deepEqual(m.basic.value,['status','owner']);m.custom.value=[];await m.save();assert.deepEqual(JSON.parse(m.calls[0].options.body),{basicFields:['status','owner'],customFieldKeys:[]});assert.equal(m.opened.value,false);assert.equal(props.modelValue.customFieldKeys.length,2);m.stop()
})
await test('failed preference save retains settings draft and supports explicit defaults',async()=>{
 let fail=true;const m=component('src/components/RequirementDetailPreferences.vue',{modelValue:{basicFields:null,customFieldKeys:null},definitions:[]},['open','basic','save','opened','error'],async(path,options)=>{if(fail)throw Error('offline');return JSON.parse(options.body)})
 m.open();m.basic.value=['status'];await m.save();assert.equal(m.opened.value,true);assert.deepEqual(m.basic.value,['status']);assert.equal(m.error.value,'offline');fail=false;await m.save(true);assert.deepEqual(m.events.at(-1),['update:modelValue',{basicFields:null,customFieldKeys:null}]);m.stop()
})
await test('Figma association accepts only non-credentialed HTTPS design URLs',()=>{
 const m=component('src/components/RequirementResources.vue',{requirementId:9,canEdit:true},['safeFigma'])
 for(const good of ['https://www.figma.com/design/abc/My-file?node-id=1-2','https://figma.com/file/abc','https://www.figma.com/proto/abc'])assert.equal(m.safeFigma(good),true,good)
 for(const bad of ['javascript:alert(1)','http://figma.com/file/abc','https://figma.com.attacker.test/design/abc','https://user:pass@figma.com/file/abc','https://figma.com:444/file/abc','https://figma.com/'])assert.equal(m.safeFigma(bad),false,bad)
 m.stop()
})
await test('attachment client rejects over 10 MiB before any upload request',async()=>{
 const m=component('src/components/RequirementResources.vue',{requirementId:9,canEdit:true},['upload','error']);await Promise.resolve();await m.upload({target:{files:[{size:10*1024*1024+1}]}});assert.match(m.error.value,/10 MB/);assert(!m.calls.some(x=>x.options?.method==='POST'));m.stop()
})
await test('attachment multipart and download retain project and identity authorization headers',async()=>{
 const calls=[],events=[],m=module(read('src/api.ts'),{'./i18n':{t,locale:{value:'en-US'}}},{localStorage:{getItem:()=> 'prj_insight'},window:{dispatchEvent:e=>events.push(e)},CustomEvent:class{},fetch:async(path,options)=>{calls.push({path,options});return {ok:true,json:async()=>({user:{id:'u_1'}}),blob:async()=>new Blob(['bytes'])}}})
 await m.api('/session');const form=new FormData();form.append('file',new Blob(['data']),'example.txt');await m.api('/requirements/9/attachments',{method:'POST',body:form});const upload=calls.at(-1);assert.equal(upload.options.headers.has('Content-Type'),false);assert.equal(upload.options.headers.get('X-TaskLoom-Project'),'prj_insight')
 const blob=await m.apiDownload('/requirements/9/attachments/1');assert.equal(await blob.text(),'bytes');assert.equal(calls.at(-1).options.headers.get('X-TaskLoom-Expected-User'),'u_1')
})
await test('controlled Figma draft survives resource-tab unmounts and only explicit cancel discards it',async()=>{
 const state=Vue.reactive({modelValue:{url:'',title:'',opened:false},requirementId:9,canEdit:true})
 const mount=()=>component('src/components/RequirementResources.vue',state,['url','title','showLink','discardLink'],undefined,{onEmit:(event,value)=>{if(event==='update:modelValue')state.modelValue=value}})
 const first=mount();first.url.value='https://figma.com/design/abc';first.title.value='未提交设计';first.showLink.value=true;await Vue.nextTick();first.stop()
 const second=mount();assert.equal(second.url.value,'https://figma.com/design/abc');assert.equal(second.title.value,'未提交设计');assert.equal(second.showLink.value,true)
 assert(!second.calls.some(call=>call.options?.method));second.discardLink();assert.deepEqual({...state.modelValue},{url:'',title:'',opened:false});second.stop()
})
await test('failed Figma association retains its draft and read-only calls never submit or remove',async()=>{
 const m=component('src/components/RequirementResources.vue',{requirementId:9,canEdit:true},['load','url','title','showLink','addLink','error'],async(path,options)=>{if(options?.method)throw Error('offline');return {items:[]}})
 await m.load();m.url.value='https://figma.com/design/abc';m.title.value='Draft';m.showLink.value=true;await m.addLink();assert.equal(m.url.value,'https://figma.com/design/abc');assert.equal(m.title.value,'Draft');assert.equal(m.showLink.value,true);assert.equal(m.error.value,'offline');m.stop()
 const reader=component('src/components/RequirementResources.vue',{requirementId:9,canEdit:false},['load','url','addLink','remove','removeTarget','upload'])
 await reader.load();reader.url.value='https://figma.com/design/abc';reader.removeTarget.value={kind:'design-links',id:3,name:'File'};await reader.addLink();await reader.remove();await reader.upload({target:{files:[new Blob(['data'])]}})
 assert(!reader.calls.some(call=>call.options?.method));reader.stop()
})
await test('resource retries cannot invalidate in-flight mutations or leave the busy state stuck',async()=>{
 const pending=deferred(),m=component('src/components/RequirementResources.vue',{requirementId:9,canEdit:true},['load','url','addLink','busy','links'],async(path,options)=>options?.method?pending.promise:{items:[]})
 await m.load();m.url.value='https://figma.com/design/abc';const mutation=m.addLink(),count=m.calls.length;await m.load();assert.equal(m.calls.length,count)
 pending.resolve({id:55,title:'Saved',url:'https://figma.com/design/abc'});await mutation;assert.equal(m.busy.value,false);assert.equal(m.links.value[0].id,55);m.stop()
})
await test('preference saves cannot overlap or be invalidated by a concurrent reload',async()=>{
 const pending=deferred(),m=component('src/components/RequirementDetailPreferences.vue',{modelValue:{basicFields:null,customFieldKeys:null},definitions:[]},['open','basic','save','load','saving','opened'],()=>pending.promise)
 m.open();m.basic.value=['status'];const saving=m.save();await m.save(true);await m.load();assert.equal(m.calls.length,1)
 pending.resolve({basicFields:['status'],customFieldKeys:[]});await saving;assert.equal(m.saving.value,false);assert.equal(m.opened.value,false);m.stop()
})
await test('stale font responses and failures cannot overwrite a newer applied account preference',async()=>{
 const styles=new Map(),display=module(read('src/displayPreferences.ts'),{vue:Vue},{document:{documentElement:{style:{setProperty:(key,value)=>styles.set(key,value)}}}})
 for(const succeed of [false,true]){
  display.applyDisplayPreferences({fontSize:'standard'});const pending=deferred(),m=component('src/components/FontSizePreference.vue',{},['change','saving'],()=>pending.promise,{imports:{'../displayPreferences':display}})
  const change=m.change({target:{value:'large'}});await m.change({target:{value:'small'}});assert.equal(m.calls.length,1)
  display.applyDisplayPreferences({fontSize:'extraLarge'});if(succeed)pending.resolve({fontSize:'large'});else pending.reject(Error('offline'));await change
  assert.equal(display.fontSize.value,'extraLarge');assert.equal(styles.get('--devflow-font-scale'),'1.25');assert.equal(m.saving.value,false);m.stop()
 }
})
console.log(`Passed ${count} display and resource tests.`)

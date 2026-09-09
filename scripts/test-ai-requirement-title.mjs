import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import ts from 'typescript'
import * as Vue from 'vue'
import { parse, compileScript, compileTemplate } from 'vue/compiler-sfc'

const read=path=>readFile(new URL('../'+path,import.meta.url),'utf8')
const transpile=source=>ts.transpileModule(source,{compilerOptions:{module:ts.ModuleKind.CommonJS,target:ts.ScriptTarget.ES2022}}).outputText
function evaluate(source,imports={}){const result={};new Function('require','exports',transpile(source))(id=>imports[id],result);return result}
const helper=evaluate(await read('src/requirementTitle.ts')),fields=evaluate(await read('src/requirementFields.ts')),mentions=evaluate(await read('src/mentions.ts'))
const flush=async()=>{for(let n=0;n<10;n++){await Promise.resolve();await Vue.nextTick()}}
const defer=()=>{let resolve,reject;const promise=new Promise((a,b)=>{resolve=a;reject=b});return{promise,resolve,reject}}
const cap=extra=>({configured:true,enabled:true,model:'gpt-5-mini',canGenerate:true,maxDescriptionLength:100000,maxTitleLength:80,...extra})
const textDoc=text=>({type:'doc',content:[{type:'paragraph',content:[{type:'text',text}]}]})
const mediaDoc=()=>({type:'doc',content:[{type:'image',attrs:{data:'PRIVATE_BASE64',name:'截图.png'}}]})
const assistantSource=await read('src/components/RequirementTitleAssistant.vue'),editorSource=await read('src/views/Editor.vue')
function compile(source,name){const descriptor=parse(source).descriptor,script=compileScript(descriptor,{id:name}),template=compileTemplate({source:descriptor.template.content,filename:name,id:name,compilerOptions:{bindingMetadata:script.bindings}});assert.deepEqual(template.errors,[]);return transpile(script.content)}
const assistantCode=compile(assistantSource,'Assistant.vue'),editorCode=compile(editorSource,'Editor.vue')
async function assistant({values={},handler,onEmit}={}){
 const props=Vue.reactive({title:'',description:'增加线索分配规则，支持按照团队权重进行分配。',document:null,projectId:'p',userId:'u',actorKey:'u',disabled:false,canConfigure:false,...values}),calls=[],emits=[],starts=[],ends=[],storage=new Map([['devflow-project','p']]),window=new EventTarget(),confirmation={allowed:false,messages:[]},scope=Vue.effectScope(),module={}
 window.confirm=text=>{confirmation.messages.push(text);return confirmation.allowed}
 const api=async(path,options={})=>{calls.push({path,options});if(handler)return handler(path,options);return options.method==='POST'?{title:'支持按团队权重分配线索',model:'gpt-5-mini'}:cap()}
 const imports={vue:{...Vue,onMounted:fn=>starts.push(fn),onBeforeUnmount:fn=>ends.push(fn)},'./AIButton.vue':{default:{}},'../api':{api},'../i18n':{t:value=>value},'../requirementTitle':helper}
 new Function('require','exports','window','localStorage',assistantCode)(id=>imports[id],module,window,{getItem:key=>storage.get(key)||null})
 const state=scope.run(()=>module.default.setup(props,{expose:()=>{},emit:(...args)=>{emits.push(args);onEmit?.(...args)}}));starts.forEach(fn=>fn());await flush()
 let stopped=false;return{...state,props,calls,emits,confirmation,storage,window,stop(){if(stopped)return;stopped=true;ends.forEach(fn=>fn());scope.stop()}}
}
async function editor({embedded=false,requirementId,handler}={}){
 const scope=Vue.effectScope(),props=Vue.reactive({embedded,...(embedded&&!requirementId?{parentId:9}:{}),...(embedded&&requirementId?{requirementId}:{})}),route=Vue.reactive({params:!embedded&&requirementId?{id:String(requirementId)}:{},query:{}}),events=[],calls=[],ends=[],guards=[],navigations=[],confirmation={allowed:false},module={}
 const api=async(path,options={})=>{calls.push({path,options});if(handler){const value=await handler(path,options);if(value!==undefined)return value}if(options.method)return{id:requirementId||91,...JSON.parse(options.body)};if(path==='/session')return{user:{id:'u',role:'product'},project:{id:'p'},canImpersonate:true};if(path==='/requirement-workflow')return{initialStatus:'规划中'};if(path.startsWith('/requirements/'))return{id:Number(path.split('/').at(-1)),title:'既有标题',description:'旧正文',category:'未分类',sprint:'待规划',roleWeights:fields.emptyRoleWeights()};return{items:[]}}
 const imports={'../layoutScope':{useLayoutBoolean:(_key,fallback)=>Vue.ref(fallback)},vue:{...Vue,onMounted:()=>{},onBeforeUnmount:fn=>ends.push(fn)},'vue-router':{useRoute:()=>route,useRouter:()=>({push:async target=>{navigations.push(target)}}),onBeforeRouteLeave:fn=>guards.push(fn),onBeforeRouteUpdate:fn=>guards.push(fn)},'../api':{api},'../i18n':{t:value=>value,formatDate:String,categoryLabel:value=>value},'../requirementFields':fields,'../mentions':mentions}
 new Function('require','exports','window',editorCode)(id=>imports[id]||(id.endsWith('.vue')?{default:{name:id}}:undefined),module,{addEventListener(){},removeEventListener(){},confirm:()=>confirmation.allowed})
 const state=scope.run(()=>module.default.setup(props,{expose:()=>{},emit:(...args)=>events.push(args)}));await state.load();state.formElement.value={checkValidity:()=>true,reportValidity:()=>true}
 return{...state,props,route,calls,events,guards,navigations,confirmation,stop(){ends.forEach(fn=>fn());scope.stop()}}
}
async function integrated(options={},aiOptions={}){
 const e=await editor(options);e.f.title='';e.f.description='为管理员增加按部门配置线索分配权重的能力。'
 const a=await assistant({...aiOptions,values:{...aiOptions.values,title:e.f.title,description:e.f.description,requirementId:options.requirementId},onEmit:(event,value)=>{if(event==='busy')e.titleGenerating.value=value;if(event==='generated')e.applyGeneratedTitle(value)}})
 const stopWatch=Vue.watch(()=>[e.f.title,e.f.description],()=>{a.props.title=e.f.title;a.props.description=e.f.description},{flush:'sync'})
 e.titleAssistant.value={generate:a.generate,cancel:a.cancel};return{e,a,stop(){stopWatch();a.stop();e.stop()}}
}
function fakeDraftRecovery(editor){
 assert.equal(editor.initialized.value,true)
 assert.equal(editor.loading.value,false)
 assert.equal(editor.canEdit.value,true)
 const saved=[]
 assert.ok(editor.draftRecovery?.value===null,'Editor must expose an empty private-draft recovery ref before mounting the drawer')
 editor.draftRecovery.value={saveNow:async()=>{saved.push(JSON.parse(JSON.stringify(editor.draftPayload.value)));return true},complete:async()=>true}
 return saved
}
let count=0;async function test(name,run){await run();count++;console.log('✓ '+name)}
await test('AI title validation counts Unicode characters and rejects empty, multiline or oversized output without truncation',()=>{
 for(const title of ['',null,'单','两行\n标题','\n开头换行','结尾换行\n','x'.repeat(81)])assert(helper.requirementTitleError(title))
 assert.equal(helper.requirementTitleError('改进线索分配规则'),'');assert.equal(helper.requirementTitleError('😀'.repeat(80)),'');assert(helper.requirementTitleError('😀'.repeat(81)))
})
await test('description limits use exact UTF-8 bytes and media-only or data URLs never reach the model',()=>{
 assert.equal(helper.titleDescriptionError('中'.repeat(3),null,9),'');assert(helper.titleDescriptionError('中'.repeat(4),null,9));assert(helper.titleDescriptionError('截图.png',mediaDoc(),100000));assert(helper.titleDescriptionError('@成员',{type:'doc',content:[{type:'mention',attrs:{id:'u',label:'成员'}}]},100000));assert(helper.titleDescriptionError('内容 data:image/png;base64,PRIVATE',null,100000));assert.equal(helper.titleDescriptionError('真实需求说明',{type:'doc',content:[...textDoc('真实需求说明').content,...mediaDoc().content]},100000),'')
})
await test('only an explicit confirmation can send plain description and optional current requirement ID',async()=>{
 const a=await assistant({values:{requirementId:17,document:textDoc('真实需求说明')}});assert.equal(a.calls.length,1);await a.generate();assert.equal(a.calls.length,1);assert.equal(a.emits.length,0);assert.equal(a.props.title,'');a.confirmation.allowed=true;await a.generate();const call=a.calls[1];assert.equal(call.path,'/ai/requirement-title');assert.deepEqual(JSON.parse(call.options.body),{description:a.props.description,confirmed:true,requirementId:17});assert.equal(call.options.headers['X-DevFlow-Project'],'p');assert(!call.options.body.includes('document'));assert.equal(a.emits.filter(([name])=>name==='generated').length,1);assert(a.confirmation.messages.every(text=>text.includes('OpenAI')&&text.includes('再次保存')));a.stop()
})
await test('manual titles, disabled identities and missing/disabled configuration never make generation requests',async()=>{
 for(const testCase of [{values:{title:'已有标题'}},{values:{title:'手'.repeat(150)}},{values:{disabled:true}},{handler:async()=>cap({canGenerate:false})},{handler:async()=>cap({configured:false})},{handler:async()=>cap({enabled:false})}]){const a=await assistant(testCase);a.confirmation.allowed=true;await a.generate();await a.generate();assert.equal(a.calls.filter(call=>call.options.method==='POST').length,0);assert.equal(a.emits.some(([name])=>name==='generated'),false);a.stop()}
})
await test('capability failure and malformed limits fail closed, remain retryable and preserve all text',async()=>{
 for(const answer of [null,cap({maxTitleLength:300}),cap({maxDescriptionLength:0}),cap({configured:'yes'})]){const a=await assistant({handler:async()=>answer});assert.equal(a.capability.value,null);assert(a.error.value);a.confirmation.allowed=true;await a.generate();assert.equal(a.calls.length,1);a.stop()}
 let failed=true;const a=await assistant({handler:async()=>{if(failed)throw Error('404 Not Found');return cap()}});assert(a.error.value.includes('404'));failed=false;await a.load();assert(a.capability.value);assert.equal(a.props.title,'');a.stop()
})
await test('empty, screenshot-only, oversized and resource-data descriptions do not request generation or consent',async()=>{
 for(const values of [{description:''},{description:'截图.png',document:mediaDoc()},{description:'中'.repeat(33334)},{description:'背景 data:image/png;base64,SECRET'}]){const a=await assistant({values});a.confirmation.allowed=true;await a.generate();assert.equal(a.calls.length,1);assert.equal(a.confirmation.messages.length,0);assert(a.error.value);a.stop()}
})
await test('bad output and provider failures never fill a title or fake requirement persistence',async()=>{
 for(const response of [null,{title:'单',model:'m'},{title:'坏\n标题',model:'m'},{title:'x'.repeat(81),model:'m'},{title:'正确标题'}]){const a=await assistant({handler:async(_path,options)=>options.method==='POST'?response:cap()});a.confirmation.allowed=true;await a.generate();assert(a.error.value);assert.equal(a.generating.value,false);assert(!a.emits.some(([name])=>name==='generated'));a.stop()}
 const a=await assistant({handler:async(_path,options)=>{if(options.method==='POST')throw Error('422 信息不足');return cap()}});a.confirmation.allowed=true;await a.generate();assert.match(a.error.value,/422/);assert.equal(a.props.title,'');assert(a.props.description);a.stop()
})
await test('duplicate generation is blocked and explicit cancellation immediately aborts the wait',async()=>{
 const pending=defer(),a=await assistant({handler:async(_path,options)=>options.method==='POST'?pending.promise:cap()});a.confirmation.allowed=true;const sending=a.generate();await a.generate();assert.equal(a.calls.length,2);a.cancel(true);assert.equal(a.generating.value,false);assert.equal(a.calls[1].options.signal.aborted,true);pending.resolve({title:'不可应用的旧标题',model:'m'});await sending;assert(!a.emits.some(([name])=>name==='generated'));assert.match(a.notice.value,/费用/);a.stop()
})
await test('editing or restoring the source text and typing a manual title invalidate late AI results',async()=>{
 for(const change of ['description','title','restore']){const pending=defer(),a=await assistant({handler:async(_path,options)=>options.method==='POST'?pending.promise:cap()});a.confirmation.allowed=true;const sending=a.generate(),original=a.props.description;if(change==='title')a.props.title='手工优先';else{a.props.description='更新后的说明';if(change==='restore')a.props.description=original}assert.equal(a.calls[1].options.signal.aborted,true);pending.resolve({title:'迟到标题',model:'m'});await sending;assert(!a.emits.some(([name])=>name==='generated'));if(change==='title')assert.equal(a.props.title,'手工优先');a.stop()}
})
await test('requirement, actor, project, account lock and unmount changes cannot apply old private results',async()=>{
 for(const change of ['requirement','actor','project','identity','account','unmount']){const pending=defer(),a=await assistant({handler:async(_path,options)=>options.method==='POST'?pending.promise:cap()});a.confirmation.allowed=true;const sending=a.generate();if(change==='requirement')a.props.requirementId=99;else if(change==='actor')a.props.actorKey='new-actor';else if(change==='project')a.storage.set('devflow-project','other-project');else if(change==='identity')a.window.dispatchEvent(new Event('devflow-identity-changed'));else if(change==='account')a.window.dispatchEvent(new Event('devflow-account-disabled'));else a.stop();pending.resolve({title:'之前账号私有标题',model:'m'});await sending;assert(!a.emits.some(([name])=>name==='generated'));a.stop()}
})
await test('metadata fetch from a previous context cannot make a replacement context eligible',async()=>{
 const pending=defer();let first=true;const a=await assistant({handler:async()=>{if(first){first=false;return pending.promise}return cap({canGenerate:false})}});assert(a.loading.value);a.props.userId='new-user';await flush();pending.resolve(cap());await flush();assert.equal(a.capability.value.canGenerate,false);assert.equal(a.canStart.value,false);a.stop()
})
for(const options of [{embedded:false},{embedded:true},{embedded:false,requirementId:17},{embedded:true,requirementId:17}]){
 await test(`Editor ${JSON.stringify(options)}: empty title generates preview, and only a second save persists`,async()=>{
  const m=await integrated(options);m.a.confirmation.allowed=true;await m.e.save();assert.equal(m.e.f.title,'支持按团队权重分配线索');assert.equal(m.e.calls.filter(call=>call.options.method).length,0);assert.equal(m.e.events.length,0);assert.equal(m.e.navigations.length,0);assert.match(m.e.notice.value,/再次保存/);assert.equal(m.e.dirty.value,true);assert.equal(m.a.generating.value,false);assert(!m.a.notice.value.includes('停止等待'));await m.e.save();assert.equal(m.e.calls.filter(call=>call.options.method).length,1);assert.equal(m.a.calls.filter(call=>call.options.method==='POST').length,1);assert.equal(JSON.parse(m.e.calls.find(call=>call.options.method).options.body).title,'支持按团队权重分配线索');m.stop()
 })
}
await test('continue-create still reviews the AI title before its formal requirement mutation',async()=>{
 const m=await integrated({embedded:true});m.a.confirmation.allowed=true
 await m.e.save(true);assert.equal(m.e.calls.filter(call=>call.options.method).length,0);assert.equal(m.e.events.length,0);assert(m.e.f.title)
 await m.e.save(true);const payload=JSON.parse(m.e.calls.find(call=>call.options.method).options.body)
 assert.equal(Object.hasOwn(payload,'status'),false);assert.equal(m.e.events[0][2],true);assert.equal(m.e.f.title,'');m.stop()
})
await test('save draft bypasses AI review and delegates only to the private draft recovery service',async()=>{
 const m=await integrated({embedded:true}),saved=fakeDraftRecovery(m.e)
 await m.e.save(false,true)
 assert.equal(saved.length,1);assert.equal(saved[0].title,'');assert.equal(saved[0].description,'为管理员增加按部门配置线索分配权重的能力。');assert.equal(Object.hasOwn(saved[0],'status'),false)
 assert.equal(m.e.calls.filter(call=>call.options.method).length,0);assert.equal(m.a.calls.filter(call=>call.options.method==='POST').length,0);assert.equal(m.e.events.length,0);m.stop()
})
await test('cancelled consent and provider failure preserve the full Editor draft and never create anything',async()=>{
 for(const fails of [false,true]){const m=await integrated({embedded:true},fails?{handler:async(_path,options)=>{if(options.method==='POST')throw Error('offline');return cap()}}:{});m.e.f.remarks='保留备注';m.e.f.roleWeights.frontend.value=500;m.a.confirmation.allowed=fails;await m.e.save();assert.equal(m.e.f.title,'');assert.equal(m.e.f.remarks,'保留备注');assert.equal(m.e.f.roleWeights.frontend.value,500);assert.equal(m.e.calls.filter(call=>call.options.method).length,0);assert.equal(m.e.events.length,0);m.stop()}
})
await test('manual 1–300 character titles remain valid and available when AI configuration fails',async()=>{
 for(const title of ['单','手'.repeat(300)]){const e=await editor({embedded:true});e.f.title=title;e.titleAssistant.value={generate:()=>{throw Error('must not call AI')},cancel(){}};await e.save();assert.equal(JSON.parse(e.calls.find(call=>call.options.method).options.body).title,title);e.stop()}
 assert.match(editorSource,/novalidate/);assert.match(editorSource,/required maxlength="300"/);assert.match(editorSource,/reportValidity\(\)/)
})
await test('closing during generation asks before dropping edits and can cancel the request without waiting for the provider',async()=>{
 const pending=defer(),m=await integrated({embedded:true},{handler:async(_path,options)=>options.method==='POST'?pending.promise:cap()});m.a.confirmation.allowed=true;const sending=m.e.save();await flush();assert(m.e.titleGenerating.value);const cancelled=m.e.requestClose();m.e.finishLeave(false);assert.equal(await cancelled,false);assert.equal(m.a.calls[1].options.signal.aborted,false);const accepted=m.e.requestClose();m.e.finishLeave(true);assert.equal(await accepted,true);assert.equal(m.a.calls[1].options.signal.aborted,true);assert.equal(m.e.events[0][0],'cancel');pending.resolve({title:'不应写入的旧标题',model:'m'});await sending;assert.equal(m.e.f.title,'');m.stop()
})
await test('manual input remains editable during generation and AI busy only blocks duplicate saves',async()=>{
 const pending=defer(),m=await integrated({embedded:true},{handler:async(_path,options)=>options.method==='POST'?pending.promise:cap()});m.a.confirmation.allowed=true;const sending=m.e.save();await flush();await m.e.save(true);assert.equal(m.a.calls.length,2);m.e.f.title='手工标题优先';assert.equal(m.e.titleGenerating.value,false);assert.equal(m.a.calls[1].options.signal.aborted,true);pending.resolve({title:'迟到内容',model:'m'});await sending;assert.equal(m.e.f.title,'手工标题优先');await m.e.save();assert.equal(m.e.calls.filter(call=>call.options.method).length,1);m.stop()
})
await test('AI title UI is non-submitting, theme-aware, responsive and exposes configuration only through an explicit permission prop',()=>{
 assert.match(assistantSource,/<router-link v-if="canConfigure"/);assert.match(assistantSource,/type="button"/);assert.match(assistantSource,/@media\(max-width:640px\)/);assert.match(assistantSource,/var\(--surface/);assert(!assistantSource.includes('localStorage.setItem'));assert(!assistantSource.includes('sessionStorage'));assert(!assistantSource.includes('apiKey'));assert(!assistantSource.includes('v-html'));assert.match(editorSource,/session.canImpersonate===true&&!session.impersonation/)
})
console.log(`Passed ${count} AI requirement title regressions (mock APIs only).`)

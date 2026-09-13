import assert from 'node:assert/strict'
import {readFileSync} from 'node:fs'
import ts from 'typescript'
import * as Vue from 'vue'
import {parse,compileScript,compileTemplate} from 'vue/compiler-sfc'

const file='src/views/AISettings.vue',source=readFileSync(new URL('../'+file,import.meta.url),'utf8')
const {descriptor}=parse(source,{filename:file}),script=compileScript(descriptor,{id:file}),template=compileTemplate({source:descriptor.template.content,filename:file,id:file,compilerOptions:{bindingMetadata:script.bindings}})
assert.deepEqual(template.errors,[])
const flush=async()=>{for(let i=0;i<8;i++){await Promise.resolve();await Vue.nextTick()}}
const nodes=node=>[node,...node.children.flatMap(nodes)]
async function mount(configured=false){
 let metadata={provider:'openai',configured,enabled:false,model:'gpt-5-mini',models:['gpt-5-mini'],canManage:true,baseUrl:'https://api.openai.com/v1',endpointMode:'default',version:configured?4:0}
 const calls=[],confirmations=[],window=new EventTarget(),locked=Vue.ref(false)
 window.confirm=message=>{confirmations.push(message);return true}
 const scope={locked,project:'p',current:()=>!locked.value,request:async(path,options)=>{calls.push({path,options});if(options){const patch=JSON.parse(options.body);metadata={...metadata,baseUrl:patch.baseUrl||metadata.baseUrl,version:metadata.version+1,configured:metadata.configured||!!patch.apiKey};metadata.endpointMode=metadata.baseUrl==='https://api.openai.com/v1'?'default':'custom'}return {...metadata}}}
 const Button={inheritAttrs:false,setup:(_,{attrs,slots})=>()=>Vue.h('button',attrs,slots.default?.())}
 const imports={vue:{...Vue,withDirectives:vnode=>vnode},'vue-router':{onBeforeRouteLeave(){},onBeforeRouteUpdate(){}},'../i18n':{t:value=>value},'../components/settingsScope':{useSettingsScope:()=>scope},'../components/AIButton.vue':{default:Button},'../components/ui/button':{Button}}
 function evaluate(code){const exports={};new Function('require','exports','window','localStorage',ts.transpileModule(code,{compilerOptions:{module:ts.ModuleKind.CommonJS,target:ts.ScriptTarget.ES2022}}).outputText)(id=>{assert(id in imports,'unexpected import '+id);return imports[id]},exports,window,{getItem:()=> 'p'});return exports}
 const Component=evaluate(script.content).default;Component.render=evaluate(template.code).render
 const element=(tag,text='')=>({tag,text,props:{},children:[],parent:null})
 const renderer=Vue.createRenderer({createElement:element,createText:text=>element('#text',text),createComment:()=>element('#comment'),insert(child,parent,anchor){if(child.parent){const index=child.parent.children.indexOf(child);if(index>=0)child.parent.children.splice(index,1)}child.parent=parent;const index=anchor?parent.children.indexOf(anchor):-1;if(index<0)parent.children.push(child);else parent.children.splice(index,0,child)},remove(child){if(child.parent)child.parent.children.splice(child.parent.children.indexOf(child),1)},setText(child,text){child.text=text},setElementText(child,text){child.text=text;child.children=[]},parentNode:child=>child.parent,nextSibling:child=>child.parent?.children[child.parent.children.indexOf(child)+1]||null,patchProp(child,key,_old,value){child.props[key]=value}})
 const root=element('root'),app=renderer.createApp(Component);app.mount(root);await flush()
 return {calls,confirmations,c:app._instance.setupState,find:predicate=>nodes(root).find(predicate),all:predicate=>nodes(root).filter(predicate),stop:()=>app.unmount()}
}
let count=0;async function test(name,run){await run();count++;console.log('✓ '+name)}
await test('mounted AI URL and secret controls have distinct non-login identities and explicit autofill contracts',async()=>{
 const m=await mount(),key=m.find(node=>node.props.id==='ai-api-key')
 assert.equal(m.find(node=>node.tag==='form').props.autocomplete,'off')
 assert.equal(key.props.name,'devflow-ai-api-key');assert.equal(key.props.type,'password');assert.equal(key.props.autocomplete,'new-password');assert.equal(m.c.apiKey,'')
 assert(m.find(node=>node.tag==='label'&&node.props.for===key.props.id))
 m.c.endpointMode='custom';await flush();const address=m.find(node=>node.props.id==='ai-api-base-url')
 assert.equal(address.props.name,'devflow-ai-api-base-url');assert.equal(address.props.type,'url');assert.equal(address.props.autocomplete,'off');assert.equal(address.props.inputmode,'url')
 assert(m.find(node=>node.tag==='label'&&node.props.for===address.props.id));assert.notEqual(address.props.name,key.props.name)
 assert.equal(m.all(node=>node.tag==='input'&&['username','email','password','login-email','login-password'].includes(node.props.name)).length,0)
 assert.equal(m.all(node=>node.tag==='input'&&['username','current-password'].includes(node.props.autocomplete)).length,0)
 m.stop()
})
await test('an address-only save from the mounted empty-key form never sends a credential',async()=>{
 const m=await mount();m.c.endpointMode='custom';await flush()
 m.find(node=>node.props.id==='ai-api-base-url').props['onUpdate:modelValue']('https://gateway.example.com')
 await m.c.save();await flush();assert.equal(m.calls.length,2)
 assert.deepEqual(JSON.parse(m.calls[1].options.body),{expectedVersion:0,baseUrl:'https://gateway.example.com/v1'})
 assert.equal(m.c.apiKey,'');assert.equal(m.c.error,'');assert.equal(m.confirmations.length,0);m.stop()
})
await test('leaving the replacement field empty retains the existing key only after destination consent',async()=>{
 const m=await mount(true);m.c.endpointMode='custom';await flush()
 m.find(node=>node.props.id==='ai-api-base-url').props['onUpdate:modelValue']('https://gateway.example.com/v1')
 await m.c.save();await flush();assert.deepEqual(JSON.parse(m.calls[1].options.body),{expectedVersion:4,baseUrl:'https://gateway.example.com/v1',reuseKey:true})
 assert.equal(m.confirmations.length,1);assert.equal(m.c.apiKey,'');assert.equal(m.find(node=>node.props.id==='ai-api-key').props.autocomplete,'new-password');m.stop()
})
console.log('Passed '+count+' mounted AI autofill-isolation regressions (mock API; no credentials read).')

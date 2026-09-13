import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import ts from 'typescript'
import * as Vue from 'vue'
import { parse, compileScript, compileTemplate } from 'vue/compiler-sfc'

const read=file=>readFileSync(new URL('../'+file,import.meta.url),'utf8')
const js=source=>ts.transpileModule(source,{compilerOptions:{module:ts.ModuleKind.CommonJS,target:ts.ScriptTarget.ES2022}}).outputText
const evaluate=(source,imports={},globals={})=>{const exports={};new Function('require','exports',...Object.keys(globals),js(source))(id=>{assert(id in imports,'Unexpected import '+id);return imports[id]},exports,...Object.values(globals));return exports}
class Element extends EventTarget {
 constructor(tag,text=''){super();Object.assign(this,{tag,text,props:{},children:[],parent:null,focused:0})}
 get parentElement(){return this.parent}
 contains(node){return this===node||this.children.some(child=>child.contains(node))}
 closest(selector){return selector==='dialog'&&this.tag==='dialog'||selector==='[inert]'&&this.props.inert||selector==='fieldset[disabled]'&&this.tag==='fieldset'&&this.props.disabled?this:this.parent?.closest(selector)||null}
 getBoundingClientRect(){return {left:40,top:100,bottom:132,width:280}}
 getClientRects(){return [this.getBoundingClientRect()]}
 focus(){this.focused++}
 scrollIntoView(){}
 querySelector(selector){const index=selector.match(/data-member-index="(\d+)"/)?.[1];return descendants(this).find(node=>index!==undefined&&String(node.props['data-member-index'])===index)||null}
}
const descendants=node=>[node,...node.children.flatMap(descendants)],content=node=>node.text+node.children.map(content).join('')
const flush=async()=>{for(let i=0;i<12;i++){await Promise.resolve();await Vue.nextTick()}}
const people=[{id:'a',name:'成员甲',email:'a@example.test',projectRoles:['product','qa'],active:true},{id:'b',name:'成员乙',email:'b@example.test',projectRoles:['frontend'],active:true},{id:'old',name:'历史人员',active:false,projectRoles:['product']}]
async function mount(name,{props={},enabled=false,statusError=false,loginHandler}={}){
 const window=new EventTarget(),document=new EventTarget(),body=new Element('body'),storage=new Map(),calls=[],events=[],redirects=[]
 Object.assign(window,{innerWidth:1280,innerHeight:900,location:{origin:'https://app.example',search:'',assign:url=>redirects.push(url)}})
 const globals={window,document,Node:Element,localStorage:{getItem:key=>storage.get(key)||null,setItem:(key,value)=>storage.set(key,value),removeItem:key=>storage.delete(key)}}
 const helpers={mentions:evaluate(read('src/mentions.ts')),recent:evaluate(read('src/recentMembers.ts')),login:evaluate(read('src/loginIdentity.ts')),wechat:evaluate(read('src/wechatLogin.ts'),{},globals)}
 const workspace=Vue.reactive({session:{tenant:{id:'t'},user:{id:'u'},project:{id:'p'}},identityConflict:false,operationDisabled:false}),state=Vue.reactive(props)
 const api=async(path,options={})=>{calls.push({path,options});if(path==='/auth/login'&&loginHandler)return loginHandler(path,options);if(path==='/auth/wechat/status'){if(statusError)throw Error('offline');return{enabled}}if(path==='/auth/wechat/start'){const url=new URL('https://open.weixin.qq.com/connect/qrconnect');url.search=new URLSearchParams({appid:'wx0123456789abcdef',scope:'snsapi_login',response_type:'code',state:'a'.repeat(64),redirect_uri:'https://app.example/api/auth/wechat/callback'});return{url:url.href}}return{}}
 const imports={vue:{...Vue,withDirectives:value=>value},'../i18n':{t:(value,params={})=>value.replace(/\{(\w+)\}/g,(all,key)=>params[key]??all)},'../api':{api},'../mentions':helpers.mentions,'../recentMembers':helpers.recent,'../loginIdentity':helpers.login,'../wechatLogin':helpers.wechat,'../layoutScope':{layoutScope:Vue.ref('t:u')},'../stores/workspace':{useWorkspaceStore:()=>workspace},'../components/LocaleSwitcher.vue':{default:{render:()=>null}}}
 function component(file){const {descriptor}=parse(read(file),{filename:file}),script=compileScript(descriptor,{id:file}),template=compileTemplate({id:file,filename:file,source:descriptor.template.content,compilerOptions:{bindingMetadata:script.bindings}});assert.deepEqual(template.errors,[]);const module=evaluate(script.content,imports,globals);module.default.render=evaluate(template.code,imports,globals).render;return module.default}
 imports['../components/WechatLoginButton.vue']={default:component('src/components/WechatLoginButton.vue')}
 const Component=component(name==='Login'?'src/views/Login.vue':'src/components/'+name+'.vue')
 const renderer=Vue.createRenderer({createElement:tag=>new Element(tag),createText:text=>new Element('#text',text),createComment:()=>new Element('#comment'),insert(child,parent,anchor){if(child.parent){const index=child.parent.children.indexOf(child);if(index>=0)child.parent.children.splice(index,1)}child.parent=parent;const index=anchor?parent.children.indexOf(anchor):-1;if(index<0)parent.children.push(child);else parent.children.splice(index,0,child)},remove(child){if(child.parent)child.parent.children.splice(child.parent.children.indexOf(child),1)},setText(child,text){child.text=text},setElementText(child,text){child.text=text;child.children=[]},parentNode:child=>child.parent,nextSibling:child=>child.parent?.children[child.parent.children.indexOf(child)+1]||null,patchProp(child,key,_old,value){child.props[key]=value},querySelector:selector=>selector==='body'?body:null})
 const root=new Element('root');body.children.push(root);root.parent=body
 const app=renderer.createApp({render:()=>Vue.h(Component,{...state,'onUpdate:modelValue':value=>{events.push(value);state.modelValue=value}})});app.mount(root);await flush()
 return{body,state,c:app._instance.subTree.component.setupState,workspace,calls,events,redirects,find:predicate=>descendants(body).find(predicate),all:predicate=>descendants(body).filter(predicate),text:()=>content(body),stop:()=>app.unmount()}
}
const click=node=>node.props.onClick({preventDefault(){},stopPropagation(){}})
let count=0;async function test(name,run){await run();count++;console.log('✓ '+name)}
await test('plain member focus and clicks never insert an at-sign or change selection; email search remains available',async()=>{
 const m=await mount('MemberMultiSelect',{props:{modelValue:[],members:people,label:'产品负责人',memberRoles:['product'],showLead:false}}),input=m.find(node=>node.tag==='input')
 assert.equal(input.props.placeholder,'搜索成员');input.props.onFocus();await flush();assert.equal(m.c.query,'');assert.deepEqual(m.events,[]);click(input);await flush();assert.equal(m.c.query,'');assert.equal(m.text().includes('尚无最近选择'),false);assert.equal(m.text().includes('仅显示当前可用候选成员'),false)
 input.props['onUpdate:modelValue']('a@example.test');input.props.onInput();await flush();assert.equal(m.all(node=>node.props.role==='option').length,1);assert(m.text().includes('a@example.test'));click(m.find(node=>node.props.role==='option'));await flush();assert.deepEqual([...m.state.modelValue],['a']);assert.equal(m.c.query,'');m.stop()
})
await test('recents are absent when empty and during search, bounded when shown; the search never anchors to its popup',async()=>{
 const m=await mount('MemberMultiSelect',{props:{modelValue:[],members:people,compact:true,showLead:false}})
 click(m.find(node=>node.props.class==='member-filter-trigger'));await flush();assert.equal(m.c.panelStyle.maxHeight,'360px');assert.equal(m.find(node=>node.props.class==='member-recents'),undefined)
 click(m.find(node=>node.props.role==='option'));await flush();assert(m.find(node=>node.props.class==='member-recents'));assert(m.c.visibleRecents.length<=6)
 const input=m.find(node=>node.tag==='input');input.props['onUpdate:modelValue']('b@example.test');input.props.onInput();await flush();assert.equal(m.find(node=>node.props.class==='member-recents'),undefined);assert(m.text().includes('b@example.test'));assert.equal(m.c.panelStyle.top,'137px');m.stop()
})
await test('historical selections remain removable without being selectable again; disabled state closes the menu',async()=>{
 const m=await mount('MemberMultiSelect',{props:{modelValue:['old'],members:people,memberRoles:['product']}});assert(m.text().includes('历史成员'));click(m.find(node=>node.tag==='input'));await flush();assert.equal(m.all(node=>node.props.role==='option').length,1)
 m.state.disabled=true;await flush();m.c.toggle('a');assert.deepEqual([...m.state.modelValue],['old']);assert.equal(m.c.opened,false);m.stop()
})
await test('login renders only a low-weight unavailable WeChat hint and keeps security notices',async()=>{
 const m=await mount('Login',{props:{notice:'请重新登录'}});assert(m.text().includes('微信登录未启用'));assert(m.text().includes('请重新登录'));assert.equal(m.all(node=>node.tag==='button'&&content(node)==='微信扫码登录').length,0)
 for(const removed of ['HTTP-only','Cookie','默认保持登录','努力只能','拼命Vibe'])assert(!m.text().includes(removed));assert.equal(m.c.form.password,'');assert.equal(m.find(node=>node.props.id==='login-password').props.autocomplete,'current-password');m.stop()
})
await test('enabled WeChat still navigates only through the official safe authorization flow',async()=>{
 const m=await mount('WechatLoginButton',{enabled:true}),button=m.find(node=>node.tag==='button');assert.equal(button.props.disabled,false);assert.equal(m.text().includes('将打开微信官方'),false);await click(button);assert.equal(m.redirects.length,1);assert(m.redirects[0].startsWith('https://open.weixin.qq.com/connect/qrconnect?'));assert.equal(m.calls.filter(call=>call.path==='/auth/wechat/start').length,1);m.stop()
})
await test('WeChat status errors remain visible and unavailable direct handlers cannot start login',async()=>{
 const m=await mount('WechatLoginButton',{statusError:true});assert(m.find(node=>node.props.role==='alert'));assert(!m.text().includes('微信登录未启用'));await m.c.start();assert.equal(m.calls.filter(call=>call.path==='/auth/wechat/start').length,0);m.stop()
})
await test('risk verification is absent normally and mounts accessible one-time fields only when the server requires it',async()=>{
 const challenge={id:'c'.repeat(32),image:'data:image/png;base64,aA==',expiresAt:Math.floor(Date.now()/1000)+120},m=await mount('Login',{loginHandler:async()=>{throw Object.assign(Error('Security check'),{status:401,code:'login_challenge_required',details:{challenge}})}})
 assert.equal(m.find(node=>node.props.id==='login-challenge-answer'),undefined)
 m.c.form.email='member@example.test';m.c.form.password='only-a-synthetic-fixture';await m.c.login();await flush()
 const field=m.find(node=>node.props.id==='login-challenge-answer'),img=m.find(node=>node.tag==='img')
 assert.equal(field.props.autocomplete,'off');assert.equal(field.props.name,'devflow-login-challenge');assert.equal(field.props['aria-describedby'],'login-challenge-help');assert.equal(img.props.alt,'登录安全验证码');assert.equal(img.props.src,challenge.image);assert(m.find(node=>node.tag==='button'&&content(node)==='换一张'));assert(m.text().includes('无障碍协助'));assert(!m.text().includes('only-a-synthetic-fixture'));m.stop()
})
console.log('Passed '+count+' mounted member-picker and login simplification regressions.')

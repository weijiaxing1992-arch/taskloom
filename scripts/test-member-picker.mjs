import assert from 'node:assert/strict'
import {readFileSync} from 'node:fs'
import ts from 'typescript'
import * as Vue from 'vue'
import {parse,compileScript,compileTemplate} from 'vue/compiler-sfc'
const read=path=>readFileSync(new URL('../'+path,import.meta.url),'utf8')
const js=source=>ts.transpileModule(source,{compilerOptions:{module:ts.ModuleKind.CommonJS,target:ts.ScriptTarget.ES2022}}).outputText
function helper(path){const exports={};new Function('exports',js(read(path)))(exports);return exports}
const recent=helper('src/recentMembers.ts'),mentions=helper('src/mentions.ts'),source=read('src/components/MemberMultiSelect.vue'),{descriptor}=parse(source),compiled=compileScript(descriptor,{id:'member-picker'})
class Target{listeners=new Map();addEventListener(type,fn){if(!this.listeners.has(type))this.listeners.set(type,new Set());this.listeners.get(type).add(fn)}removeEventListener(type,fn){this.listeners.get(type)?.delete(fn)}dispatch(type,event={}){for(const fn of this.listeners.get(type)||[])fn(event)}}
class Element extends Target{parentElement=null;inert=false;fieldsetDisabled=false;visible=true;focused=0;scrolled=0;rect={left:300,top:100,bottom:140,width:260};closest(selector){return (selector==='[inert]'&&this.inert)||(selector==='fieldset[disabled]'&&this.fieldsetDisabled)?this:null}contains(target){return target===this||target.parentElement===this}getBoundingClientRect(){return this.rect}getClientRects(){return this.visible?[this.rect]:[]}focus(){this.focused++}scrollIntoView(){this.scrolled++}querySelector(){return this.option||null}}
function storage(){const data=new Map();return{data,getItem:key=>data.get(key)||null,setItem:(key,value)=>data.set(key,value),removeItem:key=>data.delete(key)}}
const people=[{id:'a',name:'相同姓名',email:'a@example.test',active:true,role:'frontend',departmentIds:['front'],isCurrent:true},{id:'b',name:'相同姓名',email:'b@example.test',active:true,role:'frontend',departmentIds:['front']},{id:'c',name:'Backend',active:true,role:'backend',departmentIds:['back']},{id:'old',name:'Old',active:false,role:'frontend',departmentIds:['front']}]
const flush=async()=>{for(let i=0;i<4;i++)await Vue.nextTick()}
function mount(extra={}){
 const win=new Target(),doc=new Target(),vp=new Target(),store=storage(),lifecycle={mount:[],unmount:[]},scope=Vue.effectScope(),events=[],observers=[]
 Object.assign(win,{innerWidth:1280,innerHeight:900,visualViewport:vp});Object.assign(vp,{width:1280,height:900,offsetTop:0,offsetLeft:0})
 const workspace=Vue.reactive({session:{tenant:{id:'t'},user:{id:'u'},project:{id:'p'}},identityConflict:false,operationDisabled:false}),layout=Vue.ref('t:u'),props=Vue.reactive({modelValue:[],members:people,single:false,disabled:false,showLead:true,...extra})
 const imports={vue:{...Vue,useId:()=> 'picker',onMounted:fn=>lifecycle.mount.push(fn),onBeforeUnmount:fn=>lifecycle.unmount.push(fn)},'../i18n':{t:text=>text},'../mentions':mentions,'../layoutScope':{layoutScope:layout},'../stores/workspace':{useWorkspaceStore:()=>workspace},'../recentMembers':recent}
 class Observer{constructor(callback){this.callback=callback;this.disconnected=false;observers.push(this)}observe(){}disconnect(){this.disconnected=true}}
 const module={};new Function('require','exports','window','document','Node','MutationObserver','localStorage',js(compiled.content))(id=>imports[id],module,win,doc,Element,Observer,store)
 const c=scope.run(()=>module.default.setup(props,{expose(){},emit:(name,value)=>{events.push([name,value]);props.modelValue=value}})),root=new Element(),input=new Element(),panel=new Element(),option=new Element();input.parentElement=root;panel.option=option
 c.root.value=Vue.markRaw(root);c.input.value=Vue.markRaw(input);c.panel.value=Vue.markRaw(panel);lifecycle.mount.forEach(fn=>fn())
 return{c,props,events,workspace,layout,store,win,doc,vp,root,input,panel,option,observers,stop(){lifecycle.unmount.forEach(fn=>fn());scope.stop()}}
}
function key(name){return{key:name,defaultPrevented:false,stopped:false,preventDefault(){this.defaultPrevented=true},stopImmediatePropagation(){this.stopped=true}}}
let count=0;async function test(name,run){await run();count++;console.log('✓ '+name)}
await test('recent selection scope requires verified matching tenant/user plus a session project',()=>{
 const session={tenant:{id:'tenant'},user:{id:'user'},project:{id:'project'}}
 assert.equal(recent.recentMemberScope('',session),'');assert.equal(recent.recentMemberScope('tenant:other',session),'');assert.equal(recent.recentMemberScope('tenant:user',session),'tenant:user:project');assert.equal(recent.recentMemberScope('tenant:user',null),'')
})
await test('cache stores stable IDs only, caps twenty, ignores malformed storage and isolates every identity boundary',()=>{
 const s=storage(),scope='t:u:p',eligible=Array.from({length:30},(_,i)=>({id:'u'+i,name:'Private name',email:'secret@test'}))
 recent.rememberMembers(s,scope,eligible.map(x=>x.id),eligible);assert.equal(recent.readRecentMembers(s,scope).length,20);const raw=[...s.data.values()][0];assert(!raw.includes('name'));assert(!raw.includes('secret'))
 for(const other of ['t:u:other','t:other:p','other:u:p'])assert.deepEqual(recent.readRecentMembers(s,other),[])
 recent.rememberMembers(s,scope,['u10','evil'],eligible);assert.equal(recent.readRecentMembers(s,scope)[0],'u10');assert(!recent.readRecentMembers(s,scope).includes('evil'))
 s.setItem(recent.recentMemberKey(scope),'{not json');assert.deepEqual(recent.readRecentMembers(s,scope),[]);s.setItem(recent.recentMemberKey(scope),JSON.stringify([{id:'a'},' a','a','a',null]));assert.deepEqual(recent.readRecentMembers(s,scope),['a'])
 assert.doesNotThrow(()=>recent.rememberMembers({getItem(){throw Error('denied')},setItem(){throw Error('full')}},scope,['u1'],eligible))
})
await test('floating geometry uses the full space down to the viewport bottom instead of 225px',()=>{
 const box=recent.memberPanelGeometry({left:300,top:100,bottom:140,width:260},{width:1280,height:900});assert.equal(box.maxHeight,745);assert.equal(box.top,145);assert.equal(box.up,false)
 const small=recent.memberPanelGeometry({left:300,top:640,bottom:680,width:260},{width:390,height:700});assert(small.up);assert.equal(small.width,320);assert(small.left+small.width<=380)
 const keyboard=recent.memberPanelGeometry({left:20,top:360,bottom:400,width:350},{width:390,height:300,top:100});assert(keyboard.up);assert(keyboard.top>=110);assert(keyboard.maxHeight<=300)
})
await test('single mode selects exactly one stable ID, ignores reselect and preserves untouched historical chips',async()=>{
 const m=mount({single:true,modelValue:['old']});assert.equal(m.c.selected.value[0].historical,true);m.c.toggle('a');assert.deepEqual(m.props.modelValue,['a']);assert.equal(m.c.opened.value,false);m.c.toggle('a');assert.equal(m.events.length,1);m.c.remove('a');assert.deepEqual(m.props.modelValue,[]);m.c.toggle('old');assert.deepEqual(m.props.modelValue,[]);await flush();m.stop()
})
await test('recent choices are explicit clicks only, never inferred by merely opening or cancelling',async()=>{
 const m=mount();m.c.search();await flush();m.c.close();assert.equal(m.store.data.size,0);m.c.toggle('b');assert.deepEqual(recent.readRecentMembers(m.store,'t:u:p'),['b']);assert.deepEqual(m.props.modelValue,['b']);m.c.toggle('b');assert.deepEqual(recent.readRecentMembers(m.store,'t:u:p'),['b']);m.c.clearRecent();assert.equal(m.store.data.size,0);m.stop()
})
await test('roles, active status and both configured and interactive department filters gate recent options',async()=>{
 const m=mount({memberRoles:['frontend'],departmentId:'front'});recent.rememberMembers(m.store,'t:u:p',['old','c','b','a'],people);m.c.refreshRecent();assert.deepEqual(m.c.recentOptions.value.map(x=>x.id),['b','a']);m.c.toggle('c');m.c.toggle('old');assert.equal(m.events.length,0);m.c.department.value='back';assert.deepEqual(m.c.recentOptions.value,[]);m.stop()
})
await test('select-me and department bulk record eligible IDs without duplicate selections; single has no bulk mutation',()=>{
 const m=mount({departmentId:'front'});m.c.selectMe();m.c.department.value='front';m.c.addDepartment();assert.deepEqual(m.props.modelValue,['a','b']);assert.deepEqual(recent.readRecentMembers(m.store,'t:u:p'),['b','a']);m.stop()
 const one=mount({single:true});one.c.department.value='front';one.c.addDepartment();assert.equal(one.events.length,0);one.c.selectMe();assert.deepEqual(one.props.modelValue,['a']);one.stop()
})
await test('project/account switch closes stale menus and restores only after a new members directory arrives',async()=>{
 const m=mount();m.c.toggle('a');m.c.search();await flush();m.workspace.session.project.id='p2';assert.equal(m.c.opened.value,false);m.c.toggle('b');assert.equal(m.events.length,1);assert.deepEqual(m.c.recentOptions.value,[])
 m.props.members=[{id:'new',name:'New project',active:true}];await flush();m.c.toggle('new');assert.deepEqual(m.props.modelValue,['a','new']);assert.deepEqual(recent.readRecentMembers(m.store,'t:u:p2'),['new']);assert.deepEqual(recent.readRecentMembers(m.store,'t:u:p'),['a'])
 m.layout.value='t:other';m.workspace.session.user.id='other';m.c.toggle('new');assert.equal(m.events.length,2);m.props.members=[{id:'person',name:'Different account',active:true}];await flush();m.c.toggle('person');assert.deepEqual(recent.readRecentMembers(m.store,'t:other:p2'),['person']);m.stop()
})
await test('disabled, conflicting identity and inert ancestors block all teleported mutations',async()=>{
 for(const type of ['disabled','fieldset','identity','operation','inert']){const m=mount();m.c.search();await flush();if(type==='disabled')m.props.disabled=true;else if(type==='fieldset')m.root.fieldsetDisabled=true;else if(type==='identity')m.workspace.identityConflict=true;else if(type==='operation')m.workspace.operationDisabled=true;else m.root.inert=true;m.c.toggle('a');m.c.selectMe();m.c.department.value='front';m.c.addDepartment();assert.equal(m.events.length,0,type);assert.equal(m.store.data.size,0,type);if(type==='inert'||type==='fieldset')m.observers.filter(x=>!x.disconnected).forEach(x=>x.callback());assert.equal(m.c.opened.value,false);m.stop()}
})
await test('keyboard active options scroll into view, Escape is consumed and outside focus/click dismisses',async()=>{
 const m=mount();m.c.keydown(key('ArrowDown'));await flush();assert.equal(m.c.opened.value,true);const first=m.option.scrolled;m.c.keydown(key('End'));await flush();assert(m.option.scrolled>first);assert.equal(m.c.activeIndex.value,m.c.options.value.length-1)
 const escape=key('Escape');m.c.keydown(escape);assert(escape.defaultPrevented&&escape.stopped);await flush();assert.equal(m.c.opened.value,false);assert(m.input.focused>0)
 m.c.search();await flush();m.doc.dispatch('pointerdown',{target:new Element()});assert.equal(m.c.opened.value,false);m.c.search();await flush();m.doc.dispatch('focusin',{target:new Element()});assert.equal(m.c.opened.value,false);m.stop()
})
await test('visual viewport/ancestor scroll adjusts panel, hidden origin closes, cleanup removes all listeners',async()=>{
 const m=mount();m.c.search();await flush();assert.equal(m.c.panelStyle.value.maxHeight,'360px');m.vp.height=450;m.vp.dispatch('resize');assert.equal(m.c.panelStyle.value.maxHeight,'295px');m.input.rect.top=330;m.input.rect.bottom=370;m.win.dispatch('scroll');assert.equal(m.c.panelStyle.value.transform,'translateY(-100%)')
 m.input.visible=false;m.win.dispatch('scroll');assert.equal(m.c.opened.value,false);await flush();m.stop();for(const target of [m.win,m.doc,m.vp])assert([...target.listeners.values()].every(set=>set.size===0));assert(m.observers.every(observer=>observer.disconnected))
})
await test('clicking the still-focused input reopens after a single selection or Escape',async()=>{
 const m=mount({single:true});m.c.focusInput();await flush();m.c.toggle('a');await flush();assert.equal(m.c.opened.value,false);m.c.focusInput();await flush();assert.equal(m.c.opened.value,true);m.c.keydown(key('Escape'));await flush();assert.equal(m.c.opened.value,false);m.c.focusInput();await flush();assert.equal(m.c.opened.value,true);assert.match(source,/@click="focusInput"/);m.stop()
})
await test('compact filter anchors to its trigger and restores trigger focus after selection or Escape',async()=>{
 const m=mount({single:true,compact:true}),trigger=new Element();trigger.parentElement=m.root
 m.c.trigger.value=Vue.markRaw(trigger)
 // 搜索输入在浮层内部；不能再作为浮层锚点，否则滚动时会不断偏移。
 m.input.rect={left:500,top:500,bottom:540,width:400}
 m.c.openCompact();await flush();assert.equal(m.c.opened.value,true);assert.equal(m.c.panelStyle.value.top,'145px');assert(m.input.focused>0)
 m.c.keydown(key('Escape'));await flush();assert.equal(m.c.opened.value,false);assert.equal(trigger.focused,1)
 m.c.openCompact();await flush();m.c.selectMe();await flush();assert.deepEqual(m.props.modelValue,['a']);assert.equal(m.c.opened.value,false);assert.equal(trigger.focused,2)
 m.c.openCompact();await flush();trigger.visible=false;m.win.dispatch('scroll');assert.equal(m.c.opened.value,false);m.stop()
})
await test('single/recent/floating markup compiles with listbox semantics and scoped colors',()=>{
 assert.deepEqual(compileTemplate({id:'member-picker',filename:'MemberMultiSelect.vue',source:descriptor.template.content,compilerOptions:{bindingMetadata:compiled.bindings}}).errors,[])
 // 原生对话框的 top layer 高于 body；候选菜单必须挂在所在对话框，普通页面仍挂 body。
 assert.match(source,/<Teleport :to="root\?\.closest\('dialog'\) \|\| 'body'">/);assert.match(source,/:aria-multiselectable="!single"/);assert.match(source,/member-options-list/);assert.match(source,/data-member-index/);assert.match(source,/member-recents/);assert.match(source,/var\(--surface-raised/)
})
console.log(`Passed ${count} member-picker scope, recent-selection and floating-panel tests.`)

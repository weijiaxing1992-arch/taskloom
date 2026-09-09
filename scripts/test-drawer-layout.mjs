import assert from 'node:assert/strict'
import {readFile} from 'node:fs/promises'
import ts from 'typescript'
import * as Vue from 'vue'
import {parse,compileScript,compileTemplate} from 'vue/compiler-sfc'

const root=new URL('../',import.meta.url)
const source=await readFile(new URL('src/components/ResizableDrawer.vue',root),'utf8')
const helperSource=await readFile(new URL('src/drawerLayout.ts',root),'utf8')
const transpile=value=>ts.transpileModule(value,{compilerOptions:{module:ts.ModuleKind.CommonJS,target:ts.ScriptTarget.ES2022}}).outputText
const layout={};new Function('exports',transpile(helperSource))(layout)
const {drawerBounds,clampDrawerWidth,drawerWidthFromPointer,drawerWidthFromKey,preferredDrawerWidth}=layout
const {descriptor}=parse(source)
const compiled=compileScript(descriptor,{id:'test-drawer'})
let count=0
async function test(name,run){await run();count++;console.log('✓ '+name)}

class Target {
 listeners=new Map()
 addEventListener(type,listener){if(!this.listeners.has(type))this.listeners.set(type,new Set());this.listeners.get(type).add(listener)}
 removeEventListener(type,listener){this.listeners.get(type)?.delete(listener)}
 dispatch(type,event={}){for(const listener of [...(this.listeners.get(type)||[])]){listener(event);if(event.immediateStopped)break}}
 listenerCount(type){return this.listeners.get(type)?.size||0}
}
class Style {
 values=new Map()
 getPropertyValue(name){return this.values.get(name)?.value||''}
 getPropertyPriority(name){return this.values.get(name)?.priority||''}
 setProperty(name,value,priority=''){this.values.set(name,{value:String(value),priority})}
 removeProperty(name){this.values.delete(name)}
}
class Element extends Target {
 style=new Style();parentElement=null;inert=false;hidden=false;visible=true;capture=null;released=0;focused=0
 closest(){for(let element=this;element;element=element.parentElement)if(element.inert||element.hidden)return element;return null}
 getClientRects(){for(let element=this;element;element=element.parentElement)if(!element.visible)return[];return[{width:1240}]}
 setPointerCapture(id){if(this.captureFails)throw Error('capture unavailable');this.capture=id}
 hasPointerCapture(id){return this.capture===id}
 releasePointerCapture(id){assert.equal(this.capture,id);this.capture=null;this.released++;this.dispatch('lostpointercapture',{pointerId:id})}
 focus(){this.focused++}
}
function event(values={}){return{pointerId:7,clientX:400,button:0,buttons:1,isPrimary:true,defaultPrevented:false,propagationStopped:false,immediateStopped:false,preventDefault(){this.defaultPrevented=true},stopPropagation(){this.propagationStopped=true},stopImmediatePropagation(){this.immediateStopped=true},...values}}
function environment(viewport=1600){
 const win=new Target();win.innerWidth=viewport
 const body=new Element(),doc={body},observers=[];body.ownerDocument=doc
 let hooks=null,ids=0
 class Observer{constructor(callback){this.callback=callback;this.connected=true;this.elements=[];observers.push(this)}observe(element){this.elements.push(element)}disconnect(){this.connected=false}}
 const imports={vue:{...Vue,useId:()=>String(++ids),onMounted:fn=>hooks.mounted.push(fn),onUpdated:fn=>hooks.updated.push(fn),onBeforeUnmount:fn=>hooks.unmount.push(fn)},'../i18n':{t:value=>value},'../drawerLayout':layout}
 imports['../layoutScope']={layoutScope:Vue.ref(''),readLayoutWidth:(_key,fallback)=>fallback,writeLayoutWidth:()=>{}}
 const module={};new Function('require','exports','window','MutationObserver',transpile(compiled.content))(id=>imports[id],module,win,Observer)
 const mount=(overrides={},controlled=false)=>{
  const lifecycle={mounted:[],updated:[],unmount:[]},scope=Vue.effectScope(),props=Vue.reactive({label:'Test drawer',initialWidth:1240,minWidth:860,maxViewportRatio:.96,...overrides}),updates=[],exposed={}
  hooks=lifecycle
  const state=scope.run(()=>module.default.setup(props,{expose:value=>Object.assign(exposed,value),emit:(name,width)=>{assert.equal(name,'update:width');updates.push(width);if(controlled)props.width=width}}))
  hooks=null
  const element=new Element(),handle=new Element();element.parentElement=body;element.ownerDocument=doc;handle.parentElement=element;handle.ownerDocument=doc;state.drawer.value=Vue.markRaw(element)
  lifecycle.mounted.forEach(fn=>fn())
  let stopped=false
  return{...state,props,updates,exposed,element,handle,start(values={}){const pointer=event({currentTarget:handle,...values});state.startPointer(pointer);return pointer},updated(){lifecycle.updated.forEach(fn=>fn())},stop(){if(stopped)return;stopped=true;lifecycle.unmount.forEach(fn=>fn());scope.stop()}}
 }
 return{win,body,observers,mount,mutate(){observers.filter(observer=>observer.connected).forEach(observer=>observer.callback([]))}}
}

await test('desktop geometry clamps safely without overflow, NaN or contradictory limits',()=>{
 const b=drawerBounds(1600);assert.deepEqual(b,{viewport:1600,min:860,max:1536,mobile:false,resizable:true});assert.equal(clampDrawerWidth(2000,b),1536);assert.equal(clampDrawerWidth(200,b),860)
 assert.equal(clampDrawerWidth(NaN,b),1240);assert.equal(preferredDrawerWidth(undefined,NaN),1240)
 for(const viewport of [0,-1,NaN,Infinity,760,761,900,1920])for(const min of [NaN,-1,0,500,3000])for(const ratio of [NaN,-1,0,.96,2]){const range=drawerBounds(viewport,min,ratio);assert(Number.isFinite(range.max));assert(range.min<=range.max);assert(range.max<=range.viewport);assert(clampDrawerWidth(Infinity,range)<=range.max)}
})
await test('760px and narrower are exactly full viewport with no draggable separator range',()=>{for(const width of [320,600,760]){const range=drawerBounds(width);assert.equal(range.min,width);assert.equal(range.max,width);assert.equal(range.resizable,false);assert.equal(clampDrawerWidth(1240,range),width);assert.equal(drawerWidthFromKey(1240,'ArrowLeft',range),null)}})
await test('left-edge pointer movement and keyboard keys use the correct direction and boundaries',()=>{const b=drawerBounds(1600);assert.equal(drawerWidthFromPointer(1240,400,300,b),1340);assert.equal(drawerWidthFromPointer(1240,400,600,b),1040);assert.equal(drawerWidthFromPointer(1240,400,-9999,b),1536);assert.equal(drawerWidthFromPointer(1240,400,9999,b),860);assert.equal(drawerWidthFromPointer(1240,NaN,300,b),1240);assert.equal(drawerWidthFromKey(1240,'ArrowLeft',b),1264);assert.equal(drawerWidthFromKey(1240,'ArrowRight',b,true),1144);assert.equal(drawerWidthFromKey(1240,'Home',b),860);assert.equal(drawerWidthFromKey(1240,'End',b),1536);assert.equal(drawerWidthFromKey(1240,'Enter',b),null)})
await test('v-model echoes keep an active drag alive, capture the pointer and emit subsequent moves',async()=>{
 const e=environment(),m=e.mount({width:1240},true);m.start();assert.equal(m.handle.capture,7);assert.equal(e.body.style.getPropertyValue('cursor'),'col-resize')
 e.win.dispatch('pointermove',event({clientX:370}));await Vue.nextTick();assert.equal(m.dragging.value,true);assert.equal(m.actualWidth.value,1270)
 e.win.dispatch('pointermove',event({clientX:340}));await Vue.nextTick();assert.equal(m.actualWidth.value,1300);assert.deepEqual(m.updates,[1270,1300]);e.win.dispatch('pointerup',event());assert.equal(m.dragging.value,false);assert.equal(m.handle.released,1);assert.equal(e.win.listenerCount('pointermove'),0);assert.equal(e.body.style.getPropertyValue('cursor'),'');m.stop()
})
await test('keyboard adjustments and reset are observable component behavior, not just helper calculations',()=>{const e=environment(),m=e.mount({width:1000});const left=event({key:'ArrowLeft'});m.keydown(left);assert.equal(left.defaultPrevented,true);assert.equal(m.actualWidth.value,1024);m.keydown(event({key:'End'}));assert.equal(m.actualWidth.value,1536);m.resetWidth();assert.equal(m.actualWidth.value,1240);const unrelated=event({key:'Escape'});m.keydown(unrelated);assert.equal(unrelated.defaultPrevented,false);m.stop()})
await test('pointer cancellation, capture loss and window blur all restore original styles and listeners',()=>{
 for(const termination of ['pointercancel','lostpointercapture','blur']){const e=environment(),m=e.mount();e.body.style.setProperty('cursor','crosshair','important');e.body.style.setProperty('user-select','text');m.start();if(termination==='lostpointercapture')m.handle.dispatch(termination,event());else e.win.dispatch(termination,event());assert.equal(m.dragging.value,false);assert.equal(e.body.style.getPropertyValue('cursor'),'crosshair');assert.equal(e.body.style.getPropertyPriority('cursor'),'important');assert.equal(e.body.style.getPropertyValue('user-select'),'text');assert.equal(e.win.listenerCount('pointermove'),0);assert.equal(e.win.listenerCount('keydown'),0);assert.equal(m.handle.listenerCount('lostpointercapture'),0);assert(e.observers.every(observer=>!observer.connected));m.stop()}
})
await test('Escape cancels only an active resize, restoring its start width and preventing outer close handlers',()=>{const e=environment(),m=e.mount({width:1100});m.start();e.win.dispatch('pointermove',event({clientX:300}));assert.equal(m.actualWidth.value,1200);const escape=event({key:'Escape'});e.win.dispatch('keydown',escape);assert.equal(escape.defaultPrevented,true);assert.equal(escape.immediateStopped,true);assert.equal(m.actualWidth.value,1100);assert.equal(m.dragging.value,false);assert.equal(e.win.listenerCount('keydown'),0);m.stop()})
await test('narrowing the viewport cancels dragging but preserves desktop width for subsequent expansion',()=>{const e=environment(),m=e.mount({width:1300});m.start();e.win.innerWidth=600;e.win.dispatch('resize');assert.equal(m.dragging.value,false);assert.equal(m.actualWidth.value,600);assert.equal(m.bounds.value.mobile,true);assert.deepEqual(m.updates,[]);m.start();assert.equal(m.dragging.value,false);e.win.innerWidth=1600;e.win.dispatch('resize');assert.equal(m.actualWidth.value,1300);e.win.innerWidth=900;e.win.dispatch('resize');assert.equal(m.actualWidth.value,864);assert(m.actualWidth.value<=e.win.innerWidth);m.stop()})
await test('unmounting and ancestor inert/v-show changes release global styles immediately',()=>{
 for(const mode of ['unmount','inert','hidden']){const e=environment(),m=e.mount();m.start();if(mode==='unmount')m.stop();else{if(mode==='inert')e.body.inert=true;else m.element.visible=false;e.mutate()};assert.equal(m.dragging.value,false);assert.equal(e.body.style.getPropertyValue('cursor'),'');assert.equal(e.body.style.getPropertyValue('user-select'),'');assert.equal(e.win.listenerCount('pointermove'),0);assert(e.observers.every(observer=>!observer.connected));m.stop();assert.equal(e.win.listenerCount('resize'),0)}
})
await test('capture fallback works and unrelated pointers/buttons cannot take over an active resize',()=>{const e=environment(),m=e.mount();m.handle.captureFails=true;m.start({button:2});assert.equal(m.dragging.value,false);m.start({isPrimary:false});assert.equal(m.dragging.value,false);m.start();assert.equal(m.dragging.value,true);e.win.dispatch('pointermove',event({pointerId:99,clientX:200}));assert.equal(m.actualWidth.value,1240);e.win.dispatch('pointercancel',event({pointerId:99}));assert.equal(m.dragging.value,true);e.win.dispatch('pointermove',event({clientX:300}));assert.equal(m.actualWidth.value,1340);e.win.dispatch('pointermove',event({clientX:200,buttons:0}));assert.equal(m.dragging.value,false);m.stop()})
await test('multiple drawers transfer drag ownership safely and cleanup does not overwrite newer external styles',()=>{const e=environment(),first=e.mount(),second=e.mount();e.body.style.setProperty('cursor','crosshair');first.start();second.start({pointerId:8});assert.equal(first.dragging.value,false);assert.equal(second.dragging.value,true);e.body.style.setProperty('cursor','progress','important');second.stop();assert.equal(e.body.style.getPropertyValue('cursor'),'progress');assert.equal(e.body.style.getPropertyValue('user-select'),'');first.stop()})
await test('external controlled width changes cancel drag without leaking cursor state',async()=>{const e=environment(),m=e.mount({width:1240},true);m.start();m.props.width=1000;await Vue.nextTick();assert.equal(m.actualWidth.value,1000);assert.equal(m.dragging.value,false);assert.equal(e.body.style.getPropertyValue('cursor'),'');m.stop()})
await test('component exposes accessible dialog/separator semantics with no business-close or storage coupling',()=>{
 const template=compileTemplate({id:'test-drawer',filename:'ResizableDrawer.vue',source:descriptor.template.content,compilerOptions:{bindingMetadata:compiled.bindings}});assert.deepEqual(template.errors,[])
 assert.match(source,/role="dialog"/);assert.match(source,/aria-modal="true"/);assert.match(source,/role="separator"/);assert.match(source,/aria-orientation="vertical"/);assert.match(source,/aria-valuenow/);assert.match(source,/aria-valuemin/);assert.match(source,/aria-valuemax/);assert.match(source,/@dblclick\.stop\.prevent="resetWidth"/);assert.match(source,/v-if="bounds.resizable"/)
 assert(!/localStorage|sessionStorage|Teleport|emit\(['"]close/.test(source));assert(!/inheritAttrs:\s*false/.test(source));assert(!/class="drawer[" ]/.test(source))
})
console.log(`Passed ${count} drawer layout and interaction safety tests.`)

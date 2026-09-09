import {workflow} from './workflow-test-support.mjs'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import ts from 'typescript'
import * as Vue from 'vue'

const read=path=>readFileSync(new URL('../'+path,import.meta.url),'utf8')
const source=read('src/views/Requirements.vue')
function module(code,imports={}) {
 const exports={}
 new Function('require','exports','defineProps','defineEmits',ts.transpileModule(code,{compilerOptions:{module:ts.ModuleKind.CommonJS,target:ts.ScriptTarget.ES2022}}).outputText)(id=>imports[id]||{},exports,()=>({}),()=>()=>{})
 return exports
}
const fields=module(read('src/requirementFields.ts')),mentions=module(read('src/mentions.ts')),queries=module(read('src/workItemQuery.ts'))
const baseline=()=>({id:7,title:'父需求',description:'原正文',category:'产品',sprint:'123',roleWeights:fields.emptyRoleWeights(),tags:'已有标签',tagColors:{},remarks:'原备注',customFields:{},assigneeUserIds:['one'],ownerUserIds:['two']})
function fixture(handler=async()=>({items:[]})) {
 const route=Vue.reactive({params:{},query:{req:'7'}}),navigations=[],unmounts=[],scope=Vue.effectScope(),calls=[]
 const script=source.match(/<script setup[^>]*>([\s\S]*?)<\/script>/)[1]
 const names=['selected','session','descriptionDraft','detailDraft','descriptionEditing','resourceDraft','comment','assignmentDraft','ownerDraft','resetDescription','resetAssessment','resetAssignments','resetOwners','selectDetailTab','detailTab','createChild','childParent','childEditor','closeChild','childCancelled','childCreated','related','saving','confirmDetailLeave','detailError','loadRelated','consumeChildEntry','detailLoading','showProperties']
 let state
 scope.run(()=>state=module(script+'\nexport {'+names.join(',')+'}; export function invalidateDetail(){detailVersion++}',{
'../requirementWorkflow':workflow,
  vue:{...Vue,onMounted:()=>{},onBeforeUnmount:fn=>unmounts.push(fn)},
  'vue-router':{useRoute:()=>route,useRouter:()=>({push:async target=>navigations.push(target),replace:async target=>navigations.push(target)}),onBeforeRouteLeave:()=>{},onBeforeRouteUpdate:()=>{}},
  '../api':{api:async(path,options)=>{calls.push({path,options});return handler(path,options)}},
  '../layoutScope':{useLayoutBoolean:(_key,fallback)=>Vue.ref(fallback)},
  '../i18n':{t:s=>s,categoryLabel:s=>s,formatDate:s=>s},'../requirementFields':fields,'../mentions':mentions,'../workItemQuery':queries,
 }))
 state.selected.value=baseline();state.session.value={user:{id:'one',role:'product'}}
 state.resetDescription();state.resetAssessment();state.resetAssignments();state.resetOwners()
 const stop=()=>{for(const fn of unmounts){try{fn()}catch{/* Browser event listener cleanup is outside this harness. */}}scope.stop()}
 return {...state,route,navigations,calls,stop}
}
function draft(m){m.descriptionDraft.body='尚未保存的正文';m.detailDraft.roleWeights.frontend.value=200;m.detailDraft.tags='已有标签,新标签';m.detailDraft.remarks='未保存备注';m.comment.value='未发表';m.resourceDraft.value={opened:true,url:'https://figma.com/design/example',title:'未关联'};m.assignmentDraft.value=['one','two'];m.ownerDraft.value=['two','one']}
function snapshot(m){return JSON.stringify([m.descriptionDraft,m.detailDraft,m.comment.value,m.resourceDraft.value,m.assignmentDraft.value,m.ownerDraft.value])}
let count=0
async function test(name,run){await run();count++;console.log('✓ '+name)}

await test('description is the first content block and labels/weights are separate left-navigation sections',()=>{
 const template=source.split('<template>')[1]
 assert(template.indexOf('description-block')<template.indexOf('<RequirementResources'))
 const overview=template.slice(template.indexOf('class="detail-overview"'),template.indexOf("v-if=\"detailTab==='角色权重'\""))
 assert(!overview.includes('<RequirementWeights'));assert(!overview.includes('<RequirementTags'))
 assert.match(template,/class="detail-navigation"/);assert.match(template,/class="detail-tag-section"/)
 assert.match(template,/<ResizableDrawer[\s\S]+:inert="!!childParent\|\|!!fullEditorID\|\|!!defectContext"/)
 assert.match(template,/<Editor :key="childParent.id"[^>]+embedded :parent-id="childParent.id"/)
})
await test('switching sections preserves every unsaved parent field',()=>{
 const m=fixture();draft(m);const before=snapshot(m)
 for(const tab of ['标签','角色权重','子需求','详细信息'])m.selectDetailTab(tab)
 assert.equal(snapshot(m),before);assert.equal(m.calls.length,0);assert.equal(m.showProperties.value,true);m.stop()
})
await test('opening the child drawer stays on the parent route and does not discard parent drafts',()=>{
 const m=fixture();draft(m);const before=snapshot(m);m.createChild()
 assert.deepEqual({...m.childParent.value},{id:7,title:'父需求'});assert.equal(m.navigations.length,0);assert.equal(snapshot(m),before)
 m.selected.value={...baseline(),id:8};m.createChild();assert.equal(m.childParent.value.id,7);m.stop()
})
await test('read-only and busy parents cannot open a child composer',()=>{
 const m=fixture();m.saving.value=true;m.createChild();assert.equal(m.childParent.value,null)
 m.saving.value=false;m.session.value.user.role='viewer';m.createChild();assert.equal(m.childParent.value,null);m.stop()
})
await test('child creation updates the parent relation without reopening or resetting it',async()=>{
 const m=fixture();draft(m);m.detailTab.value='子需求';m.createChild();const before=snapshot(m)
 await m.childCreated({id:9,parentId:7,title:'新子需求'},false)
 assert.equal(m.childParent.value,null);assert.equal(m.related.value.children[0].id,9);assert.equal(m.selected.value.id,7);assert.equal(m.detailTab.value,'子需求');assert.equal(snapshot(m),before)
 assert(!m.calls.some(call=>call.path==='/requirements/7'));assert.equal(m.navigations.length,0);m.stop()
})
await test('create-and-continue keeps the drawer and deduplicates each returned child',async()=>{
 const m=fixture();m.createChild();const child={id:9,parentId:7,title:'新子需求'}
 await m.childCreated(child,true);await m.childCreated(child,true)
 assert.equal(m.childParent.value.id,7);assert.equal(m.related.value.children.length,1);m.stop()
})
await test('close delegates confirmation to the embedded editor and keeps the parent draft',async()=>{
 const m=fixture();draft(m);m.createChild();const before=snapshot(m)
 m.childEditor.value={requestClose:()=>false,dirty:true,saving:false};assert.equal(await m.closeChild(),false);assert.equal(m.childParent.value.id,7)
 m.childEditor.value={requestClose:async()=>{await m.childCancelled();return true},dirty:true,saving:false};assert.equal(await m.closeChild(),true);assert.equal(m.childParent.value,null);assert.equal(snapshot(m),before);m.stop()
})
await test('a saving child blocks parent route leave before any destructive action',()=>{
 const m=fixture();m.childEditor.value={requestClose:()=>false,dirty:false,saving:true};assert.equal(m.confirmDetailLeave(),false);m.stop()
})
await test('delayed related responses cannot overwrite a newer parent detail version',async()=>{
 let release;const pending=new Promise(resolve=>{release=resolve}),m=fixture(()=>pending)
 const read=m.loadRelated(7);m.invalidateDetail();release({items:[{id:99}]});await read
 assert.equal(m.related.value.children.length,0);m.stop()
})
await test('legacy child URL is redirected to a parent detail plus a child-drawer request',()=>{
 let routes
 module(read('src/main.ts'),{vue:{createApp:()=>({use(){return this},mount(){}})},pinia:{createPinia:()=>({})},'vue-router':{createWebHistory:()=>({}),createRouter:options=>{routes=options.routes;return {}}}})
 const before=routes.find(route=>route.path==='/requirements/new').beforeEnter
 assert.deepEqual(before({query:{parentId:'7',release:'test'}}),{path:'/requirements',query:{release:'test',req:'7',createChild:'1'},replace:true})
 assert.equal(before({query:{}}),undefined);assert.equal(before({query:{parentId:'invalid'}}),undefined)
})
await test('direct child entry opens the composer and removes only its transient query flag',async()=>{
 const m=fixture();m.route.query={req:'7',createChild:'1',release:'test'};await m.consumeChildEntry()
 assert.equal(m.childParent.value.id,7);assert.equal(m.detailTab.value,'子需求');assert.deepEqual(m.navigations.at(-1),{query:{req:'7',release:'test'}});m.stop()
})
console.log(`Passed ${count} requirement drawer tests.`)

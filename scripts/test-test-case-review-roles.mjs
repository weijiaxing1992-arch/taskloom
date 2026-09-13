import assert from 'node:assert/strict'
import * as Vue from 'vue'
import { parse, compileScript, compileTemplate } from 'vue/compiler-sfc'
import { read, transpile, flush, testingWorkspace, workspaceFixture } from './testing-workspace-test-support.mjs'

const evaluate=(source,imports)=>{const exports={};new Function('require','exports','window','localStorage',transpile(source))(id=>{assert(id in imports,'Unexpected import '+id);return imports[id]},exports,new EventTarget(),{getItem:()=>null,setItem(){}});return exports}
const roles=evaluate(await read('src/memberRoles.ts'),{})
const source=await read('src/components/testing/TestCaseLibrary.vue')
const descriptor=parse(source,{filename:'TestCaseLibrary.vue'}).descriptor
const script=compileScript(descriptor,{id:'review-role-regression'})
const template=compileTemplate({source:descriptor.template.content,filename:'TestCaseLibrary.vue',id:'review-role-regression',compilerOptions:{bindingMetadata:script.bindings}})
assert.deepEqual(template.errors,[])
const element=(tag,text='')=>({tag,text,props:{},children:[],parent:null})
const renderer=Vue.createRenderer({createElement:tag=>element(tag),createText:text=>element('#text',text),createComment:()=>element('#comment'),insert(child,parent,anchor){if(child.parent){const index=child.parent.children.indexOf(child);if(index>=0)child.parent.children.splice(index,1)}child.parent=parent;const index=anchor?parent.children.indexOf(anchor):-1;if(index<0)parent.children.push(child);else parent.children.splice(index,0,child)},remove(child){if(child.parent)child.parent.children.splice(child.parent.children.indexOf(child),1)},setText(child,text){child.text=text},setElementText(child,text){child.text=text;child.children=[]},parentNode:child=>child.parent,nextSibling:child=>child.parent?.children[child.parent.children.indexOf(child)+1]||null,patchProp(child,key,_old,value){child.props[key]=value}})
const descendants=node=>[node,...node.children.flatMap(descendants)]
const content=node=>node.text+node.children.map(content).join('')
const caseRecord={id:1,code:'TC-0001',title:'角色合集评审',category:'未分类',preconditions:'',steps:'操作',expected:'通过',priority:'P1',status:'待评审',caseType:'功能测试',owner:'',ownerUserId:'',enabled:true,tags:'',requirementId:null,stepsDetail:[{order:1,action:'操作',expected:'通过'}],updatedAt:'2026-09-10T00:00:00Z'}
async function mount(currentUser,overrides={}){
  const session=Vue.reactive({currentUser,operationDisabled:false,identityConflict:false,...overrides}),calls=[]
  const api=async(path,options={})=>{calls.push({path,options});return path==='/test-cases/1'?structuredClone(caseRecord):{items:[]}}
  const imports={vue:{...Vue,withDirectives:value=>value},'vue-router':{useRoute:()=>({query:{}}),useRouter:()=>({replace:async()=>{}}),onBeforeRouteLeave(){},onBeforeRouteUpdate(){}},'../settingsScope':{useSettingsScope:()=>({request:api}),useSettingsDialog:()=>Vue.ref(null)},'../../i18n':{t:value=>value,formatDate:value=>value},'../../layoutScope':{useLayoutBoolean:(_key,fallback)=>Vue.ref(fallback)},'../../stores/workspace':{useWorkspaceStore:()=>session},'../../testingWorkspace':testingWorkspace,'../../memberRoles':roles}
  const stub=Vue.defineComponent({inheritAttrs:false,setup:(_props,{slots,expose})=>{expose({canLeave:()=>true});return()=>Vue.h('section',{},slots.default?.())}})
  for(const match of script.content.matchAll(/from\s+["']([^"']+\.vue)["']/g))imports[match[1]]={default:stub}
  const component=evaluate(script.content,imports).default
  component.render=evaluate(template.code,imports).render
  const app=renderer.createApp({render:()=>Vue.h(component,{workspace:workspaceFixture({canManage:false}),members:[],executions:[],embedded:true,initialCaseId:1})}),container=element('root')
  app.component('router-link',stub);app.mount(container);await flush()
  const state=app._instance.subTree.component.setupState
  state.changeDetailTab('review');await flush()
  return{session,calls,state,button:label=>descendants(container).find(node=>node.tag==='button'&&content(node)===label),stop:()=>app.unmount()}
}

for(const [name,user,expected] of [
  ['secondary QA',{role:'product',projectRoles:['product','qa']},true],
  ['QA with viewer',{role:'qa',projectRoles:['qa','viewer']},true],
  ['legacy single QA',{role:'qa'},true],
  ['ungranted QA',{role:'product',projectRoles:['product','frontend']},false],
  ['explicitly empty roles',{role:'qa',projectRoles:[]},false],
  ['missing session',null,false],
]){
  const m=await mount(user)
  assert.equal(m.button('评审通过').props.disabled,!expected,name+' approval button')
  m.state.reviewComment='需要补充';await flush()
  assert.equal(m.button('退回修改').props.disabled,!expected,name+' rejection button')
  if(expected){await m.button('评审通过').props.onClick();assert.equal(m.calls.filter(call=>call.options.method==='POST').length,1,name+' review submitted')}
  else{await m.state.review('approve');assert.equal(m.calls.some(call=>call.options.method==='POST'),false,name+' cannot submit programmatically')}
  m.stop()
}
for(const flag of ['operationDisabled','identityConflict']){
  const m=await mount({role:'product',projectRoles:['product','qa']})
  m.session[flag]=true;await flush()
  assert.equal(m.button('评审通过').props.disabled,true,flag+' disables review')
  await m.state.review('approve');assert.equal(m.calls.some(call=>call.options.method==='POST'),false,flag+' blocks pending actions');m.stop()
}
console.log('Passed 8 mounted test-case role-union and safety regressions.')

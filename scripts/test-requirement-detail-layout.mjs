import assert from 'node:assert/strict'
import {readFileSync} from 'node:fs'
import ts from 'typescript'
import * as Vue from 'vue'
import {parse, compileScript, compileTemplate, compileStyle} from 'vue/compiler-sfc'
import {workflow} from './workflow-test-support.mjs'

const read=path=>readFileSync(new URL('../'+path,import.meta.url),'utf8')
const transpile=source=>ts.transpileModule(source,{compilerOptions:{module:ts.ModuleKind.CommonJS,target:ts.ScriptTarget.ES2022}}).outputText
const evaluate=(source,imports={})=>{const exports={};new Function('require','exports',transpile(source))(id=>imports[id]||(id.endsWith('.vue')?{default:{name:id.split('/').at(-1).replace('.vue','')}}:{}),exports);return exports}
const source=read('src/views/Requirements.vue'),descriptor=parse(source).descriptor
const script=compileScript(descriptor,{id:'detail-layout'}),template=compileTemplate({id:'detail-layout',filename:'Requirements.vue',source:descriptor.template.content,compilerOptions:{bindingMetadata:script.bindings}})
assert.deepEqual(template.errors,[])
const fields=evaluate(read('src/requirementFields.ts')),mentions=evaluate(read('src/mentions.ts')),queries=evaluate(read('src/workItemQuery.ts'))
function fixture(){
 const calls=[],unmounts=[],scope=Vue.effectScope(),props=Vue.reactive({detailOnly:true}),route=Vue.reactive({params:{},query:{req:'7'}})
 const imports={vue:{...Vue,onMounted:()=>{},onBeforeUnmount:fn=>unmounts.push(fn),withDirectives:node=>node,resolveComponent:name=>({name})},'vue-router':{useRoute:()=>route,useRouter:()=>({push:async()=>{},replace:async()=>{}}),onBeforeRouteLeave:()=>{},onBeforeRouteUpdate:()=>{}},'../api':{api:async(path,options)=>{calls.push({path,options});return{items:[]}}},'../i18n':{t:value=>value,categoryLabel:value=>value,formatDate:value=>value},'../layoutScope':{useLayoutBoolean:(_key,fallback)=>Vue.ref(fallback)},'../requirementFields':fields,'../mentions':mentions,'../workItemQuery':queries,'../requirementWorkflow':workflow}
 const component=evaluate(script.content,imports).default,view=evaluate(template.code,imports)
 const state=scope.run(()=>component.setup(props,{expose:()=>{}})),setup=Vue.proxyRefs(state)
 state.selected.value={id:7,code:'REQ-7',title:'真实需求',description:'原始正文',roleWeights:fields.emptyRoleWeights(),customFields:{},tags:'',tagColors:{},remarks:'',category:'未分类',sprint:'123',status:'草稿',createdAt:'2026-09-04T01:00:00Z',ownerUserIds:[],assigneeUserIds:[]}
 state.session.value={tenant:{id:'tenant-a'},user:{id:'user-a',role:'product'},project:{id:'project-a'}}
 state.detailLoading.value=false;state.resetAssessment();state.resetAssignments();state.resetOwners();state.resetDescription()
 return{...state,calls,render:()=>view.render({},[],props,setup,{},{}),stop:()=>{for(const fn of unmounts){try{fn()}catch{/* Browser listener teardown is covered by the component's own runtime tests. */}}scope.stop()}}
}
function find(node,predicate){
 if(!node||typeof node!=='object')return null
 if(Array.isArray(node)){for(const child of node){const match=find(child,predicate);if(match)return match}return null}
 if(predicate(node))return node
 if(Array.isArray(node.children))return find(node.children,predicate)
 if(node.children&&typeof node.children==='object')for(const [key,slot]of Object.entries(node.children)){if(key.startsWith('_')||typeof slot!=='function')continue;const match=find(slot(),predicate);if(match)return match}
 return null
}
const named=(node,name)=>find(node,value=>value.type?.name===name)
const hasClass=(node,name)=>find(node,value=>typeof value.props?.class==='string'&&value.props.class.split(' ').includes(name))
const css=compileStyle({source:descriptor.styles.map(style=>style.content).join('\n'),filename:'Requirements.vue',id:'detail-layout'}).rawResult.root
function declaration(selector,property,value){let found=false;css.walkRules(rule=>{if(rule.selector===selector)rule.walkDecls(property,decl=>{if(decl.value===value)found=true})});assert(found,`${selector} must set ${property}: ${value}`)}
let count=0
async function test(name,run){await run();count++;console.log('✓ '+name)}

await test('real detail template keeps navigation and content on the main side and fields on the aside',()=>{
 const m=fixture(),split=named(m.render(),'ResizableSplit');assert(split)
 assert.equal(split.props.scene,'requirements.detail');assert.equal(split.props['project-id'],'project-a');assert.equal(split.props['show-aside'],true)
 assert.equal(split.props['min-main-width'],480);assert.equal(split.props['min-aside-width'],300);assert.equal(split.props.breakpoint,820)
 const main=split.children.main(),aside=split.children.aside();assert(hasClass(main,'detail-navigation'));assert(hasClass(main,'detail-main'));assert(!hasClass(main,'detail-props'));assert(hasClass(aside,'detail-props'));assert(named(aside,'CustomFieldInputs'))
 assert.equal(m.calls.length,0);m.stop()
})
await test('collapsing, resizing the outer drawer and changing content tabs preserve all current drafts',()=>{
 const m=fixture();m.detailDraft.remarks='未保存备注';m.detailDraft.customFields={long_field:'长字段草稿'};m.assignmentDraft.value=['a','b'];m.ownerDraft.value=['c'];m.descriptionDraft.body='正文草稿';m.comment.value='未发布评论'
 const before=JSON.stringify([m.detailDraft,m.assignmentDraft.value,m.ownerDraft.value,m.descriptionDraft,m.comment.value])
 let rendered=m.render();const toggle=find(rendered,node=>node.type==='button'&&node.props?.['aria-label']==='收起基础信息');toggle.props.onClick();assert.equal(m.showProperties.value,false)
 named(m.render(),'ResizableDrawer').props['onUpdate:width'](900);m.selectDetailTab('标签');rendered=m.render();assert.equal(named(rendered,'ResizableSplit').props['show-aside'],false)
 find(rendered,node=>node.type==='button'&&node.props?.['aria-label']==='展开基础信息').props.onClick();assert.equal(m.showProperties.value,true)
 assert.equal(JSON.stringify([m.detailDraft,m.assignmentDraft.value,m.ownerDraft.value,m.descriptionDraft,m.comment.value]),before);assert.equal(m.calls.length,0);m.stop()
})
await test('nested editors disable internal dragging while viewer mode retains disabled field protections',()=>{
 const m=fixture();m.childParent.value={id:7,title:'父需求'};assert.equal(named(m.render(),'ResizableSplit').props.disabled,true)
 m.childParent.value=null;m.fullEditorID.value=7;assert.equal(named(m.render(),'ResizableSplit').props.disabled,true)
 m.fullEditorID.value=null;m.defectContext.value={id:7,title:'父需求',sprint:'123'};assert.equal(named(m.render(),'ResizableSplit').props.disabled,true)
 m.defectContext.value=null;m.session.value.user.role='viewer';const split=named(m.render(),'ResizableSplit');assert.equal(split.props.disabled,false);assert.equal(find(split.children.aside(),node=>node.type==='fieldset').props.disabled,true);assert.equal(m.calls.length,0);m.stop()
})
await test('field styles constrain each actual control instead of masking a fixed-width overflowing form',()=>{
 declaration('.requirement-drawer .detail-props','width','100%');declaration('.requirement-drawer .detail-props','min-width','0');declaration('.requirement-drawer .detail-props','overflow','visible')
 declaration('.detail-props :deep(.custom-fields.inline)','grid-template-columns','minmax(0, 1fr)')
 declaration('.detail-props :deep(.custom-fields > label)','display','block')
 declaration('.detail-props :deep(input:not([type=checkbox])), .detail-props :deep(select), .detail-props :deep(textarea)','min-width','0')
 declaration('.detail-props .assignment-actions','flex-wrap','wrap')
 declaration('.detail-props :deep(.option-checks label), .detail-props :deep(.custom-fields > label.check)','display','flex')
 declaration('.detail-props :deep(input[type=checkbox])','width','auto')
 let covering=false;css.walkRules(rule=>{if(rule.selector.includes('.detail-props'))rule.walkDecls('position',decl=>{if(decl.value==='absolute')covering=true})});assert.equal(covering,false,'fields must never overlay the document at intermediate widths')
})
await test('narrow detail has one component-owned vertical scroll chain and all scoped CSS compiles',()=>{
 const narrow=css.nodes.find(node=>node.type==='atrule'&&node.name==='container'&&node.params==='requirement-detail (max-width: 820px)');assert(narrow)
 const rules=Object.fromEntries(narrow.nodes.filter(node=>node.type==='rule').map(rule=>[rule.selector,Object.fromEntries(rule.nodes.filter(node=>node.type==='decl').map(decl=>[decl.prop,decl.value]))]))
 // 窄抽屉必须由工作区承担纵向滚动；hidden 会裁掉堆叠后的基础信息并造成右栏漂移。
 assert.equal(rules['.detail-workspace'].overflow,'auto');assert.equal(rules['.detail-workspace']['overscroll-behavior'],'contain');assert.equal(rules['.detail-split :deep(.split-main)'].overflow,'visible');assert.equal(rules['.detail-reading'].height,'auto');assert.equal(rules['.detail-reading'].display,'block');assert.equal(rules['.requirement-drawer .detail-main'].overflow,'visible')
 for(const style of descriptor.styles)assert.deepEqual(compileStyle({source:style.content,filename:'Requirements.vue',id:'data-v-detail-layout',scoped:true}).errors,[])
})
console.log(`Passed ${count} requirement detail layout regressions.`)

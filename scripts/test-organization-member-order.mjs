import assert from 'node:assert/strict'
import {readFileSync} from 'node:fs'
import ts from 'typescript'
import * as Vue from 'vue'
import {parse,compileScript,compileTemplate,compileStyle} from 'vue/compiler-sfc'
const read=path=>readFileSync(new URL('../'+path,import.meta.url),'utf8')
function evaluate(source,imports={},globals={}){const exports={},code=ts.transpileModule(source,{compilerOptions:{module:ts.ModuleKind.CommonJS,target:ts.ScriptTarget.ES2022}}).outputText;new Function('require','exports',...Object.keys(globals),code)(id=>id==='../layoutScope'?{useLayoutBoolean:(_key,value)=>Vue.ref(value)}:imports[id]||{},exports,...Object.values(globals));return exports}
const helpers=evaluate(read('src/organization.ts'))
const dep=(id,name,sortOrder,parentId=null)=>({id,name,sortOrder,parentId,status:'active',code:id,memberCount:0})
const person=(id,name,ids=[],primary='')=>({id,name,departmentIds:ids,primaryDepartmentId:primary,email:id+'@example.test',employeeNo:id,active:true,tenantRole:'member',projectMemberships:[],groupIds:[]})
const directory=[dep('alg','算法',2),dep('front','前端',20,'rd'),dep('ui','UI',3),dep('rd','研发',1),dep('back','后端',10,'rd')]
let count=0;async function test(name,fn){await fn();count++;console.log('✓ '+name)}
await test('department tree order respects sibling sortOrder and places every child directly after its parent branch',()=>{
 const before=JSON.stringify(directory);assert.deepEqual(helpers.orderedDepartments(directory).map(item=>item.id),['rd','back','front','alg','ui']);assert.equal(JSON.stringify(directory),before)
 assert.deepEqual(helpers.orderedDepartments([...directory].reverse()).map(item=>item.id),['rd','back','front','alg','ui'])
})
await test('missing parents and cyclic historic departments are retained once without recursion or hangs',()=>{
 const values=[dep('a','A',1,'b'),dep('b','B',2,'a'),dep('orphan','Orphan',3,'gone'),dep('self','Self',4,'self')],sorted=helpers.orderedDepartments(values);assert.equal(sorted.length,4);assert.equal(new Set(sorted.map(item=>item.id)).size,4)
 const deep=Array.from({length:5000},(_,i)=>dep(String(i),String(i),0,i?String(i-1):null));assert.equal(helpers.orderedDepartments(deep).length,5000)
})
await test('primary department wins, missing primary falls back to first tree membership and unknown-only memberships sort last',()=>{
 const members=[person('unknown','阿甲',['missing'],'missing'),person('none','阿乙'),person('front','王前',['front','back'],'front'),person('back','张后',['front','back']),person('root','李根',['rd'])]
 const before=JSON.stringify(members),sorted=helpers.sortOrganizationMembers(members,directory);assert.deepEqual(sorted.map(member=>member.id),['root','back','front','unknown','none']);assert.equal(JSON.stringify(members),before)
 assert.equal(helpers.memberPrimaryDepartment(members[2],helpers.orderedDepartments(directory)).id,'front');assert.equal(helpers.memberPrimaryDepartment(person('bad','错误主部门',['front'],'back'),helpers.orderedDepartments(directory)).id,'front')
})
await test('Chinese names use pinyin ordering and duplicate names use immutable IDs rather than API input order',()=>{
 const values=[person('z2','张三',['back']),person('w','王五',['back']),person('l','李四',['back']),person('z1','张三',['back'])];assert.deepEqual(helpers.sortOrganizationMembers(values,directory,'zh-CN').map(member=>member.id),['l','w','z1','z2']);assert.deepEqual(helpers.sortOrganizationMembers([...values].reverse(),directory,'zh-CN').map(member=>member.id),['l','w','z1','z2'])
})
await test('all displayed memberships use tree order without altering selected department array or hiding unknown IDs',()=>{
 const ids=['ui','front','back','missing','front'];assert.deepEqual(helpers.orderedDepartmentIds(ids,directory),['back','front','ui','missing']);assert.deepEqual(ids,['ui','front','back','missing','front']);assert.equal(helpers.departmentPath(directory.find(item=>item.id==='front'),directory),'研发 / 前端')
})
function setup(){
 const source=read('src/components/OrganizationMembers.vue').match(/<script setup[^>]*>([\s\S]*?)<\/script>/)[1],effects=Vue.effectScope(),unmounts=[],calls=[],locale=Vue.ref('zh-CN'),context={organization:{id:'tenant',name:'Test'},isTenantAdmin:false,permissions:[],roles:[],projects:[]};let result
 effects.run(()=>result=evaluate(source+'\nexport {items,departments,departmentOptions,primaryDepartmentOptions,filtered,paged,pageGroups,page,department,query,status,form,departmentNames,canManage}',{vue:{...Vue,onMounted:()=>{},onBeforeUnmount:fn=>unmounts.push(fn)},'vue-router':{onBeforeRouteLeave:()=>{},onBeforeRouteUpdate:()=>{}},'../organization':helpers,'../i18n':{t:s=>s,locale},'./settingsScope':{useSettingsScope:()=>({locked:Vue.ref(false),request:async(...args)=>{calls.push(args);return{items:[]}},current:()=>true})}},{defineProps:()=>({context}),defineEmits:()=>()=>{},window:{removeEventListener:()=>{},confirm:()=>false}}))
 return{...result,calls,locale,stop:()=>{for(const fn of unmounts)fn();effects.stop()}}
}
await test('actual member view groups after the unchanged 30-member pagination and counts each multi-department member once',()=>{
 const m=setup();m.departments.value=directory;m.items.value=[...Array.from({length:32},(_,i)=>person('b'+i,'后端'+String(i).padStart(2,'0'),['back'])),person('multi','张前',['back','front'],'front'),person('none','未分配')].reverse();assert.equal(m.canManage.value,false);assert.equal(m.filtered.value.length,34);assert.equal(m.paged.value.length,30);assert.equal(m.pageGroups.value.length,1);assert.equal(m.pageGroups.value[0].id,'back');assert.equal(m.pageGroups.value[0].members.length,30);m.page.value=2;assert.deepEqual(m.pageGroups.value.map(group=>group.id),['back','front','']);assert.equal(m.pageGroups.value.flatMap(group=>group.members).length,4);assert.equal(new Set(m.filtered.value.map(member=>member.id)).size,34);assert.equal(m.calls.length,0);m.stop()
})
await test('search, department membership and status filters remain unchanged while department selectors share tree order',async()=>{
 const m=setup();m.departments.value=directory;m.items.value=[person('a','张三',['back','front'],'front'),{...person('b','张三',['back']),active:false},person('c','王五',['ui'])];m.query.value='张三';m.department.value='back';assert.deepEqual(m.filtered.value.map(member=>member.id),['b','a']);m.status.value='enabled';assert.deepEqual(m.filtered.value.map(member=>member.id),['a']);m.page.value=3;m.query.value='王五';await Vue.nextTick();assert.equal(m.page.value,1);assert.equal(m.filtered.value.length,0);assert.deepEqual(m.departmentOptions.value.map(item=>item.id),['rd','back','front','alg','ui']);m.form.departmentIds=['front','back'];m.form.primaryDepartmentId='front';assert.deepEqual(m.primaryDepartmentOptions.value,['back','front']);assert.equal(m.form.primaryDepartmentId,'front');assert.equal(m.departmentNames(['front','back']),'研发 / 后端、研发 / 前端');m.stop()
})
await test('grouped table remains valid Vue and renders accessible headings without removing moderation or row actions',()=>{
 const source=read('src/components/OrganizationMembers.vue'),descriptor=parse(source).descriptor,script=compileScript(descriptor,{id:'org-members'});assert.deepEqual(compileTemplate({source:descriptor.template.content,filename:'OrganizationMembers.vue',id:'org-members',compilerOptions:{bindingMetadata:script.bindings}}).errors,[]);assert.deepEqual(compileStyle({source:descriptor.styles.map(style=>style.content).join('\n'),filename:'OrganizationMembers.vue',id:'org-members',scoped:true}).errors,[]);assert.match(source,/scope="colgroup"/);assert.match(source,/v-for="group in pageGroups"/);assert.match(source,/v-for="member in group.members"/);assert.match(source,/@dblclick="open\(member\)"/);assert.match(source,/v-for="item in departmentOptions"/);assert.match(source,/v-for="item in departmentOptions.filter/);assert.match(source,/v-for="id in primaryDepartmentOptions"/)
})
function setupDirectory(){
 const source=read('src/components/OrganizationDirectory.vue').match(/<script setup[^>]*>([\s\S]*?)<\/script>/)[1],effects=Vue.effectScope(),unmounts=[],calls=[],orderLocales=[],locale=Vue.ref('zh-CN'),locked=Vue.ref(false),context=Vue.reactive({organization:{id:'tenant',name:'Test'},isTenantAdmin:false,permissions:[],roles:[],projects:[]});let result
 effects.run(()=>result=evaluate(source+'\nexport {departments,orderedDepartments,filteredDepartments,eligibleParents,editing,query,form,opened,canManage,open}',{vue:{...Vue,onMounted:()=>{},onBeforeUnmount:fn=>unmounts.push(fn)},'vue-router':{onBeforeRouteLeave:()=>{},onBeforeRouteUpdate:()=>{}},'../organization':{...helpers,orderedDepartments:(items,value)=>{orderLocales.push(value);return helpers.orderedDepartments(items,value)}},'../i18n':{t:s=>s,locale},'./settingsScope':{useSettingsScope:()=>({locked,request:async(...args)=>{calls.push(args);return{items:[]}}})}},{defineProps:()=>({context,section:'departments'}),defineEmits:()=>()=>{},window:{removeEventListener:()=>{},confirm:()=>false}}))
 return{...result,calls,orderLocales,locale,locked,context,stop:()=>{for(const fn of unmounts)fn();effects.stop()}}
}
await test('actual organization directory and parent selector share reactive tree order and the selected locale',()=>{
 const m=setupDirectory();m.departments.value=directory.map(item=>({...item}));const ids=value=>value.map(item=>item.id),expected=['rd','back','front','alg','ui'];assert.deepEqual(ids(m.filteredDepartments.value),expected);assert.deepEqual(ids(m.eligibleParents.value),expected);assert.equal(m.orderLocales.at(-1),'zh-CN')
 m.departments.value.find(item=>item.id==='alg').sortOrder=0;assert.deepEqual(ids(m.filteredDepartments.value),['alg','rd','back','front','ui']);assert.deepEqual(ids(m.eligibleParents.value),['alg','rd','back','front','ui']);m.locale.value='en-US';assert.deepEqual(ids(m.filteredDepartments.value),['alg','rd','back','front','ui']);assert.equal(m.orderLocales.at(-1),'en-US');assert.equal(m.calls.length,0);m.stop()
})
await test('directory search preserves branch order and never narrows the parent department options',()=>{
 const m=setupDirectory();m.departments.value=directory.map(item=>({...item}));m.query.value=' 研发 ';assert.deepEqual(m.filteredDepartments.value.map(item=>item.id),['rd','back','front']);assert.deepEqual(m.eligibleParents.value.map(item=>item.id),['rd','back','front','alg','ui']);m.query.value='BACK';assert.deepEqual(m.filteredDepartments.value.map(item=>item.id),['back']);m.query.value='no matching department';assert.equal(m.filteredDepartments.value.length,0);assert.equal(m.eligibleParents.value.length,5);m.stop()
})
await test('ordered parent options retain self and descendant exclusions and safely handle historical cycles',()=>{
 const m=setupDirectory();m.departments.value=[...directory.map(item=>({...item})),dep('deep','研发子组',0,'back'),{...dep('inactive','已停用部门',4),status:'inactive'}];m.editing.value=m.departments.value.find(item=>item.id==='rd');assert.deepEqual(m.eligibleParents.value.map(item=>item.id),['alg','ui','inactive']);m.editing.value=m.departments.value.find(item=>item.id==='back');assert.deepEqual(m.eligibleParents.value.map(item=>item.id),['rd','front','alg','ui','inactive'])
 m.departments.value.push(dep('cycle-a','Cycle A',5,'cycle-b'),dep('cycle-b','Cycle B',6,'cycle-a'));m.editing.value=m.departments.value.find(item=>item.id==='cycle-a');assert.deepEqual(m.eligibleParents.value.map(item=>item.id),['rd','back','deep','front','alg','ui','inactive']);m.stop()
})
await test('directory ordering retains read-only and locked-scope edit restrictions',()=>{
 const m=setupDirectory();m.departments.value=directory.map(item=>({...item}));assert.equal(m.canManage.value,false);m.open(m.departments.value[0]);assert.equal(m.opened.value,false);m.context.permissions.push('departments.manage');assert.equal(m.canManage.value,true);m.locked.value=true;m.open(m.departments.value[0]);assert.equal(m.opened.value,false);m.locked.value=false;m.open(m.departments.value[0]);assert.equal(m.opened.value,true);assert.equal(m.form.sortOrder,2);assert.equal(m.calls.length,0);m.stop()
})
await test('organization directory compiles with the ordered navigation and parent selector',()=>{
 const source=read('src/components/OrganizationDirectory.vue'),descriptor=parse(source).descriptor,script=compileScript(descriptor,{id:'org-directory'});assert.deepEqual(compileTemplate({source:descriptor.template.content,filename:'OrganizationDirectory.vue',id:'org-directory',compilerOptions:{bindingMetadata:script.bindings}}).errors,[]);assert.deepEqual(compileStyle({source:descriptor.styles.map(style=>style.content).join('\n'),filename:'OrganizationDirectory.vue',id:'org-directory',scoped:true}).errors,[]);assert.match(source,/v-for="item in filteredDepartments"/);assert.match(source,/v-for="item in eligibleParents"/)
})
console.log(`Passed ${count} organization member and directory ordering tests.`)

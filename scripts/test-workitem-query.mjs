import assert from 'node:assert/strict'
import {readFile} from 'node:fs/promises'
import ts from 'typescript'
const root=new URL('../',import.meta.url)
const source=await readFile(new URL('src/workItemQuery.ts',root),'utf8')
const output=ts.transpileModule(source,{compilerOptions:{module:ts.ModuleKind.CommonJS,target:ts.ScriptTarget.ES2022}}).outputText
const helpers={};new Function('exports',output)(helpers)
const {workItemFields,workFilterValue,queryWorkItems,workItemPersonNames,workOperators}=helpers
const members=[{id:'u1',name:'Same'},{id:'u2',name:'Same'}]
const defs=[{objectType:'requirement',key:'cost',name:'用户字段',type:'number',enabled:true},{objectType:'defect',key:'cost',name:'用户字段',type:'text',enabled:true},{objectType:'requirement',key:'checks',name:'数组',type:'multi_select',options:['A','B'],enabled:true},{objectType:'requirement',key:'hidden',name:'disabled',type:'text',enabled:false}]
const fields=workItemFields(defs,members,true)
const a={id:2,objectType:'requirement',code:'REQ-0002',title:'Alpha',assigneeUserIds:['u1','u2'],ownerUserIds:['u2'],roleWeights:{frontend:{userIds:['u1','u2'],value:2},ui:{value:0}},customFields:{cost:10,checks:['A','B']},sensitive:false,tags:'tag1,tag2',createdAt:'2026-09-03T02:00:00Z'}
const b={id:10,objectType:'requirement',code:'REQ-0010',title:'beta',roleWeights:{frontend:{value:10}},customFields:{cost:2},sensitive:true}
const c={id:1,objectType:'defect',code:'BUG-0001',title:'Empty',customFields:{cost:'same business key'}}
let count=0
function test(name,run){run();count++;console.log('✓ '+name)}
test('all five difficulty columns are defaults and all enabled custom types are available',()=>{for(const role of ['frontend','backend','ui','algorithm','product'])assert(fields.find(x=>x.key===`role.${role}.value`).default);assert(!fields.some(x=>x.key.endsWith('.hidden')));assert(fields.some(x=>x.key==='cf.requirement.cost'));assert(fields.some(x=>x.key==='cf.defect.cost'));for(const field of fields)assert(workOperators(field).length)})
test('numeric sorting is numeric, nulls always last, and code sorting is by numeric ID',()=>{assert.deepEqual(queryWorkItems([c,b,a],fields,[],'role.frontend.value','asc').map(x=>x.id),[2,10,1]);assert.deepEqual(queryWorkItems([c,b,a],fields,[],'role.frontend.value','desc').map(x=>x.id),[10,2,1]);assert.deepEqual(queryWorkItems([b,a],fields,[],'code','asc').map(x=>x.id),[2,10])})
test('AND filters include secondary people, explicit zero/false and array membership',()=>{const rules=[{field:'assignee',operator:'includes',value:'u2'},{field:'owner',operator:'includes',value:'u2'},{field:'role.frontend.userId',operator:'includes',value:'u2'},{field:'role.ui.value',operator:'eq',value:0},{field:'sensitive',operator:'eq',value:false},{field:'cf.requirement.checks',operator:'includes',value:'B'},{field:'tags',operator:'includes',value:'tag2'}];assert.deepEqual(queryWorkItems([a,b,c],fields,rules,'code','asc').map(x=>x.id),[2]);assert.deepEqual(workItemPersonNames(a,'assignee',members),['Same','Same'])})
test('scoped custom keys do not collide across requirement/defect types',()=>{assert.deepEqual(queryWorkItems([a,b,c],fields,[{field:'cf.requirement.cost',operator:'gt',value:5}],'code','asc').map(x=>x.id),[2]);assert.deepEqual(queryWorkItems([a,b,c],fields,[{field:'cf.defect.cost',operator:'contains',value:'business'}],'code','asc').map(x=>x.id),[1])})
test('missing values require explicit empty conditions and are not numeric zero/false',()=>{assert.deepEqual(queryWorkItems([a,b,c],fields,[{field:'role.ui.value',operator:'is_empty'}],'code','asc').map(x=>x.id),[1,10]);assert.equal(queryWorkItems([c],fields,[{field:'sensitive',operator:'neq',value:false}],'code','asc').length,0)})
test('filter input parsing rejects cleared numeric input and invalid dates without converting to zero',()=>{const num=fields.find(x=>x.key==='weightTotal'),date=fields.find(x=>x.key==='createdAt'),bool=fields.find(x=>x.key==='sensitive');assert.throws(()=>workFilterValue(num,'eq',''));assert.throws(()=>workFilterValue(num,'eq','abc'));assert.equal(workFilterValue(num,'eq','0'),0);assert.throws(()=>workFilterValue(date,'eq','2026-02-30'));assert.equal(workFilterValue(bool,'eq','false'),false);assert.equal(workFilterValue(num,'is_empty','anything'),undefined)})
test('date comparisons use calendar dates and filtering never mutates aggregate source data',()=>{const before=JSON.stringify([a,b,c]);assert.deepEqual(queryWorkItems([a,b,c],fields,[{field:'createdAt',operator:'eq',value:'2026-09-03'}],'title','desc').map(x=>x.id),[2]);assert.equal(JSON.stringify([a,b,c]),before);assert.deepEqual(workItemPersonNames({owner:'历史姓名'},'owner'),['历史姓名'])})
test('timestamp date filters use the displayed timezone without shifting date-only fields',()=>{const row={...a,createdAt:'2026-09-03T20:00:00Z',startDate:'2026-09-03'};assert.equal(queryWorkItems([row],fields,[{field:'createdAt',operator:'eq',value:'2026-09-04'}],'code','asc',[],'Asia/Shanghai').length,1);assert.equal(queryWorkItems([row],fields,[{field:'startDate',operator:'eq',value:'2026-09-03'}],'code','asc',[],'Asia/Shanghai').length,1)})
test('iteration defaults put creation time after title without fixing it or changing pool defaults',()=>{
 assert.deepEqual(fields.filter(field=>field.fixed||field.default).slice(0,3).map(field=>field.key),['code','title','createdAt'])
 assert.equal(fields.filter(field=>field.key==='createdAt').length,1)
 assert(!fields.find(field=>field.key==='createdAt').fixed)
 assert(!workItemFields([],[],false).find(field=>field.key==='createdAt').default)
})
console.log(`Passed ${count} typed work-item query tests.`)

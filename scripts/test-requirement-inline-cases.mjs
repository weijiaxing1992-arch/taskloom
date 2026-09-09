import assert from 'node:assert/strict'
import {mountTestingComponent,workspaceFixture,flush,read} from './testing-workspace-test-support.mjs'

const item={id:71,code:'TC-71',title:'已有用例',category:'未分类',preconditions:'',steps:'操作',expected:'成功',priority:'P1',status:'草稿',caseType:'功能测试',owner:'',ownerUserId:'',enabled:true,tags:'',requirementId:null,stepsDetail:[{order:1,action:'操作',expected:'成功'}]}
async function mount(extra={},saved=item){
 return mountTestingComponent('src/components/testing/TestCaseLibrary.vue',{
  props:{workspace:workspaceFixture(),members:[],executions:[],embedded:true,requirementId:9,...extra},
  query:{req:'9'},
  handler:(path,options)=>options.method?{...saved,...JSON.parse(options.body)}:path==='/test-cases/71'?structuredClone(saved):{id:9,code:'REQ-9',title:'当前需求'},
 })
}
let m=await mount();await flush()
assert.equal(m.opened.value,true);assert.equal(m.edit.requirementId,9)
m.edit.title='新增';m.edit.stepsDetail=[{order:1,action:'操作',expected:'成功'}]
assert.equal(m.canLeave(),false,'dirty draft blocks accidental navigation')
await m.saveCase()
assert.equal(m.apiCalls.find(x=>x.options.method)?.path,'/test-cases')
assert.equal(m.routerCalls.length,0);assert.equal(m.opened.value,false);m.stop()

m=await mount({initialCaseId:71}, {...item,requirementId:9});await flush()
assert.equal(m.record.value.id,71);m.edit.title='需求中编辑';await m.saveCase()
let write=m.apiCalls.find(x=>x.options.method)
assert.equal(write.options.method,'PATCH');assert.equal(JSON.parse(write.options.body).expectedRequirementId,9)
assert.equal(m.routerCalls.length,0);m.stop()

m=await mount({initialCaseId:71,linkExisting:true});await flush()
assert.equal(m.edit.requirementId,9);assert.equal(m.record.value.requirementId,null)
await m.saveCase();write=m.apiCalls.find(x=>x.options.method)
assert.equal(JSON.parse(write.options.body).expectedRequirementId,null)
assert.equal(JSON.parse(write.options.body).requirementId,9);m.stop()

m=await mount({initialCaseId:71,linkExisting:true},{...item,requirementId:22});await flush()
assert.equal(m.opened.value,false);assert.match(m.error.value,/关联已变化/)
assert.equal(m.apiCalls.filter(x=>x.options.method).length,0);m.stop()

m=await mount({initialCaseId:71,workspace:workspaceFixture({canEdit:false})},{...item,requirementId:9});await flush()
await m.saveCase();assert.equal(m.apiCalls.filter(x=>x.options.method).length,0)
await m.close();assert.equal(m.routerCalls.length,0);m.stop()
assert.doesNotMatch(await read('src/views/Sprints.vue'),/需　需求|缺　缺陷/)
console.log('Passed inline create/edit/link, conflict, readonly, draft protection and unchanged route regressions.')

import assert from 'node:assert/strict'
import { deferred, flush, mountTestingComponent, testingWorkspace, workspaceFixture } from './testing-workspace-test-support.mjs'

// 详情带有只读 ID/统计，草稿必须按字段白名单取值，显式 null 仍表示清空关联。
assert.deepEqual(testingWorkspace.testingEditable({ name: '', requirementId: 9 }, { id: 1, name: '范围', requirementId: null }), { name: '范围', requirementId: null })

const design = await mountTestingComponent('src/components/testing/TestDesigns.vue', {
  props: { workspace: workspaceFixture(), members: [] },
})
await design.open({ id: 7, name: '旧设计', description: '', requirementId: null, ownerUserId: '', tags: '', points: [], updatedAt: '昨天' })
assert.equal('id' in design.edit, false)
await design.open(null)
assert.equal(design.edit.name, '')
assert.equal('updatedAt' in design.edit, false)
assert.equal(design.dirty.value, false)
design.stop()

// 慢速详情不能在“创建新计划”后回填并覆盖用户正在输入的内容。
const pending = deferred()
const operations = await mountTestingComponent('src/components/testing/TestingOperations.vue', {
  props: { tab: 'plans', workspace: workspaceFixture(), plans: [], executions: [], members: [], sprints: [] },
  query: { tab: 'plans', plan: '8' },
  handler: path => path === '/test-plans/8' ? pending.promise : { items: [], total: 0 },
})
await flush()
operations.createPlan()
operations.planEdit.name = '新计划不能被覆盖'
pending.resolve({ id: 8, name: '旧详情', caseIds: [1], total: 10 })
await flush()
assert.equal(operations.plan.value, null)
assert.equal(operations.planEdit.name, '新计划不能被覆盖')
assert.equal(operations.loading.value, false)
assert.equal('id' in operations.planEdit, false)
operations.stop()

console.log('✓ testing editable field whitelist, fresh drafts and stale plan response guards')

import assert from 'node:assert/strict'
import * as Vue from 'vue'
import {
  deferred,
  flush,
  mountTestingComponent,
  read,
  testingWorkspace,
  workspaceFixture,
} from './testing-workspace-test-support.mjs'

let count = 0
async function test(name, run) { await run(); count++; console.log('✓ ' + name) }

const testCase = (id, title = '权限验证') => ({
  id,
  code: `TC-${String(id).padStart(4, '0')}`,
  title,
  category: '未分类',
  preconditions: '',
  steps: '打开页面',
  expected: '页面可用',
  priority: 'P1',
  status: '草稿',
  caseType: '功能测试',
  owner: '',
  ownerUserId: '',
  enabled: true,
  tags: '',
  requirementId: null,
  stepsDetail: [{ order: 1, action: '打开页面', expected: '页面可用' }],
  updatedAt: '2026-09-05T00:00:00Z',
})

const loadedWorkspace = workspaceFixture({
  folders: [
    { id: 10, libraryId: 1, parentId: null, name: '账号', sortOrder: 1, count: 2 },
    { id: 11, libraryId: 1, parentId: 10, name: '权限', sortOrder: 1, count: 1 },
    { id: 12, libraryId: 1, parentId: 10, name: '登录', sortOrder: 2, count: 1 },
  ],
})

const successfulWorkspaceRequest = path => {
  if (path === '/testing/workspace') return loadedWorkspace
  if (path === '/members') return { items: [{ id: 'u-1', name: '测试同学', active: true }] }
  if (path === '/test-plans') return { items: [{ id: 5, name: '发布验证', code: 'TP-0005' }] }
  if (path === '/test-executions') return { items: [{ id: 6, caseId: 1, caseTitle: '权限验证', status: '未执行' }] }
  if (path === '/sprints') return { items: [{ id: 1, name: '9.05 迭代', status: '进行中' }] }
  return { items: [] }
}

await test('workspace shell keeps its six explicit tabs and passes only scoped data to split submodules', async () => {
  const source = await read('src/views/Testing.vue')
  for (const tab of ['designs', 'cases', 'plans', 'executions', 'report', 'settings']) assert.match(source, new RegExp(`key:'${tab}'`))
  for (const component of ['TestCaseLibrary', 'TestDesigns', 'TestingSettings', 'TestingOperations']) assert.match(source, new RegExp(`<${component}\\b`))
  assert.match(source, /:key="session\.currentProject\?\.id"/)
  assert.match(source, /Promise\.allSettled/)
  assert.doesNotMatch(source, /selectedCase|selectedPlan|selectedExecution|caseForm|planForm/)
})

await test('case table keeps checkbox cells as table cells while only form labels use flex alignment', async () => {
  const css = await read('src/testing-workspace.css')
  const tableCheck = css.match(/\.test-table\s+\.test-check\s*\{([^}]*)\}/)
  assert.ok(tableCheck, 'case table must reserve a dedicated checkbox column')
  assert.match(tableCheck[1], /width\s*:\s*42px/)
  assert.doesNotMatch(tableCheck[1], /display\s*:\s*flex/)

  // 复选框标签需要横向对齐，但不能把 th/td 本身变成 flex，否则列宽和表头会错位。
  const labelCheck = css.match(/\.test-check-label\s*,\s*label\.test-check\s*\{([^}]*)\}/)
  assert.ok(labelCheck, 'checkbox form label keeps its own alignment rule')
  assert.match(labelCheck[1], /display\s*:\s*flex/)
})

await test('workspace partial failures retain safe successful data, expose retry state, and ignore stale batches', async () => {
  let mode = 'ok'
  const pending = []
  const handler = path => {
    if (mode === 'partial') {
      if (path === '/members') throw Error('成员暂不可用')
      return successfulWorkspaceRequest(path)
    }
    if (mode === 'deferred') {
      const item = deferred()
      pending.push({ path, item })
      return item.promise
    }
    return successfulWorkspaceRequest(path)
  }
  const m = await mountTestingComponent('src/views/Testing.vue', { handler })
  await flush()
  assert.equal(m.workspace.value.libraries[0].name, '默认用例库')

  mode = 'partial'
  await m.load()
  assert.equal(m.workspace.value.libraries[0].name, '默认用例库')
  assert.equal(m.error.value, '成员暂不可用')
  assert.equal(m.loading.value, false)

  mode = 'deferred'
  const old = m.load()
  const recent = m.load()
  assert.equal(pending.length, 10)
  for (const { path, item } of pending.slice(5)) item.resolve(path === '/testing/workspace' ? workspaceFixture({ libraries: [{ id: 2, name: '最新库', isDefault: true, count: 0 }] }) : { items: [] })
  await recent
  assert.equal(m.workspace.value.libraries[0].name, '最新库')
  for (const { path, item } of pending.slice(0, 5)) item.resolve(path === '/testing/workspace' ? workspaceFixture({ libraries: [{ id: 3, name: '旧库', isDefault: true, count: 0 }] }) : { items: [{ id: 'old' }] })
  await old
  assert.equal(m.workspace.value.libraries[0].name, '最新库')
  assert.equal(m.loading.value, false)
  m.stop()
})

await test('folder trees are bounded, ordered, and retain ancestor context during a search', () => {
  const rows = testingWorkspace.folderRows(loadedWorkspace.folders, 1, new Set())
  assert.deepEqual(rows.map(row => [row.id, row.depth]), [[10, 0]])
  const expanded = testingWorkspace.folderRows(loadedWorkspace.folders, 1, new Set([10]))
  assert.deepEqual(expanded.map(row => [row.id, row.depth]), [[10, 0], [11, 1], [12, 1]])
  const filtered = testingWorkspace.folderRows(loadedWorkspace.folders, 1, new Set(), '权限')
  assert.deepEqual(filtered.map(row => [row.id, row.depth]), [[10, 0], [11, 1]])
  const malformed = [...loadedWorkspace.folders, { id: 99, libraryId: 1, parentId: 99, name: '历史坏数据', sortOrder: 9, count: 0 }]
  assert.doesNotThrow(() => testingWorkspace.folderRows(malformed, 1, new Set([10, 99])))
})

await test('case create deep link accepts only a safe requirement ID and close removes only case/create query state', async () => {
  const requirement = { id: 17, code: 'REQ-0017', title: '审批流校验', updatedAt: '2026-09-05T00:00:00Z' }
  const m = await mountTestingComponent('src/components/testing/TestCaseLibrary.vue', {
    props: { workspace: loadedWorkspace, members: [], executions: [] },
    query: { tab: 'cases', requirement: '17', create: '1', view: 'mine' },
    handler: path => path === '/requirements/17' ? requirement : path.startsWith('/test-cases?') ? { items: [], total: 0 } : { items: [] },
  })
  await flush()
  assert.equal(m.opened.value, true)
  assert.equal(m.edit.requirementId, 17)
  assert.deepEqual(m.selectedRequirement.value, requirement)
  await m.close()
  assert.deepEqual(m.route.query, { tab: 'cases', requirement: '17', view: 'mine' })
  m.stop()

  const unsafe = await mountTestingComponent('src/components/testing/TestCaseLibrary.vue', {
    props: { workspace: loadedWorkspace, members: [], executions: [] },
    query: { tab: 'cases', requirement: '017', create: '1' },
    handler: path => path.startsWith('/test-cases?') ? { items: [], total: 0 } : { items: [] },
  })
  await flush()
  assert.equal(unsafe.edit.requirementId, null)
  assert.equal(unsafe.apiCalls.some(call => call.path === '/requirements/017'), false)
  unsafe.stop()
})

await test('case dirty guards block route changes until discard is explicitly confirmed, including in-flight saves', async () => {
  const m = await mountTestingComponent('src/components/testing/TestCaseLibrary.vue', {
    props: { workspace: loadedWorkspace, members: [], executions: [] },
    query: { tab: 'cases', create: '1' },
    handler: path => path.startsWith('/test-cases?') ? { items: [], total: 0 } : { items: [] },
  })
  await flush()
  assert.equal(m.leaveGuards.length, 1)
  assert.equal(m.updateGuards.length, 1)
  m.edit.title = '未保存标题'
  assert.equal(m.dirty.value, true)
  assert.equal(m.leaveGuards[0](), false)
  assert.equal(m.updateGuards[0]({ query: { tab: 'cases', case: '9' } }), false)
  m.window.confirm = () => true
  assert.equal(m.leaveGuards[0](), true)
  m.saving.value = true
  assert.equal(m.leaveGuards[0](), false)
  m.saving.value = false
  m.stop()
})

await test('case list ignores a late query response and keeps page selection scoped to the latest result', async () => {
  let deferredMode = false
  const pending = []
  const m = await mountTestingComponent('src/components/testing/TestCaseLibrary.vue', {
    props: { workspace: loadedWorkspace, members: [], executions: [] },
    handler: path => {
      if (!path.startsWith('/test-cases?')) return { items: [] }
      if (!deferredMode) return { items: [testCase(1)], total: 1 }
      const item = deferred(); pending.push(item); return item.promise
    },
  })
  await flush()
  deferredMode = true
  const old = m.load()
  const recent = m.load()
  assert.equal(pending.length, 2)
  pending[1].resolve({ items: [testCase(2, '最新结果')], total: 1 })
  await recent
  pending[0].resolve({ items: [testCase(1, '旧结果')], total: 1 })
  await old
  assert.equal(m.items.value[0].title, '最新结果')
  assert.equal(m.loading.value, false)
  m.stop()
})

await test('case detail tabs respect unsaved nested comments and stale detail requests cannot overwrite the latest case', async () => {
  const first = deferred()
  const second = deferred()
  const m = await mountTestingComponent('src/components/testing/TestCaseLibrary.vue', {
    props: { workspace: loadedWorkspace, members: [], executions: [] },
    handler: path => {
      if (path === '/test-cases/91') return first.promise
      if (path === '/test-cases/92') return second.promise
      if (path === '/testing/cases/92/reviews') return { items: [] }
      if (path.startsWith('/test-cases?')) return { items: [], total: 0 }
      return { items: [] }
    },
  })
  await flush()

  const openingOld = m.openCase(91, false)
  await flush()
  const openingCurrent = m.openCase(92, false)
  await flush()
  second.resolve(testCase(92, '当前用例'))
  await openingCurrent
  assert.equal(m.record.value?.id, 92)
  first.resolve(testCase(91, '过期用例'))
  await openingOld
  assert.equal(m.record.value?.id, 92)
  assert.equal(m.record.value?.title, '当前用例')

  m.comments.value = { canLeave: () => false }
  m.changeDetailTab('review')
  assert.equal(m.detailTab.value, 'details')
  m.comments.value = { canLeave: () => true }
  m.changeDetailTab('review')
  assert.equal(m.detailTab.value, 'review')
  m.saving.value = true
  m.changeDetailTab('changes')
  assert.equal(m.detailTab.value, 'review')
  m.saving.value = false
  m.reviewBusy.value = true
  m.changeDetailTab('changes')
  assert.equal(m.detailTab.value, 'review')
  m.stop()
})

await test('test design and settings retain drafts on denied navigation/save and invalidate stale requirement details', async () => {
  const slow = deferred()
  const design = await mountTestingComponent('src/components/testing/TestDesigns.vue', {
    props: { workspace: loadedWorkspace, members: [] },
    handler: path => path === '/requirements/7' ? slow.promise : { items: [] },
  })
  const opening = design.open({ id: 1, name: '设计草稿', description: '', requirementId: 7, ownerUserId: '', tags: '', points: [] })
  await flush()
  design.edit.name = '未保存设计'
  assert.equal(design.leaveGuards[0](), false)
  design.window.confirm = () => true
  design.close()
  slow.resolve({ id: 7, code: 'REQ-0007', title: '旧需求' })
  await opening
  assert.equal(design.requirement.value, null)
  design.stop()

  const settings = await mountTestingComponent('src/components/testing/TestingSettings.vue', {
    props: { settings: loadedWorkspace.settings, canManage: true },
    handler: async () => { throw Error('保存失败') },
  })
  await flush()
  settings.draft.value.businessContext = '未保存背景'
  assert.equal(settings.allowed(), false)
  await settings.save()
  assert.equal(settings.draft.value.businessContext, '未保存背景')
  assert.equal(settings.error.value, '保存失败')
  settings.stop()
})

await test('plans preserve drafts on failure, avoid duplicate saves, and execution links use safe current route state', async () => {
  const workspace = workspaceFixture()
  const plans = []
  const executions = [{ id: 3, caseId: 31, caseTitle: '支付校验', planName: '发布验证', status: '未执行', actualResult: '', note: '' }]
  let pendingSave
  const m = await mountTestingComponent('src/components/testing/TestingOperations.vue', {
    props: { tab: 'plans', workspace, members: [], plans, executions, sprints: [] },
    handler: (path, options) => {
      if (path.startsWith('/test-cases?')) return { items: [testCase(1), { ...testCase(2), enabled: false }], total: 2 }
      if (options.method === 'POST' && path === '/test-plans') return pendingSave.promise
      return { items: [] }
    },
  })
  await flush()
  m.createPlan()
  await flush()
  m.planEdit.name = '保留中的计划'
  pendingSave = deferred()
  const first = m.savePlan()
  await m.savePlan()
  assert.equal(m.apiCalls.filter(call => call.path === '/test-plans' && call.options.method === 'POST').length, 1)
  assert.equal(m.showPlan.value, true)
  pendingSave.reject(Error('校验失败'))
  await first
  assert.equal(m.showPlan.value, true)
  assert.equal(m.planEdit.name, '保留中的计划')
  assert.equal(m.error.value, '校验失败')
  assert.equal(m.saving.value, false)
  assert.deepEqual(m.candidates.value.map(item => item.id), [1])
  m.stop()

  const routed = await mountTestingComponent('src/components/testing/TestingOperations.vue', {
    props: { tab: 'executions', workspace, members: [], plans, executions, sprints: [] },
    query: { tab: 'executions', execution: '3', accidental: 'no' },
    handler: path => {
      if (path === '/test-cases/31') return testCase(31, '支付校验')
      if (path === '/test-executions/3/history') return { items: [] }
      return { items: [] }
    },
  })
  await flush()
  assert.equal(routed.execution.value.id, 3)
  await routed.closeDetail()
  assert.deepEqual(routed.route.query, { tab: 'executions' })
  routed.stop()
})

console.log(`Passed ${count} testing workspace behavior regressions.`)

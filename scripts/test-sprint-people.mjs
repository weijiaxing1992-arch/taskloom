import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import ts from 'typescript'

const root = new URL('../', import.meta.url)
function evaluate(source, imports = {}) {
  const output = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText
  const exports = {}
  new Function('require', 'exports', output)(id => imports[id], exports)
  return exports
}
const mentions = evaluate(await readFile(new URL('src/mentions.ts', root), 'utf8'))
const { sprintWorkPayload, sprintParticipants, sprintTeamParticipation } = evaluate(await readFile(new URL('src/sprintPeople.ts', root), 'utf8'), { './mentions': mentions })
const members = [{ id: 'u1', name: '同名', projectRole: 'frontend', active: true }, { id: 'u2', name: '同名', projectRole: 'backend', active: true }, { id: 'u3', name: '历史成员', projectRole: 'product', active: false }]
let count = 0
function test(name, run) { run(); count++; console.log('✓ ' + name) }
test('quick requirements submit stable ordered multiple IDs and never the defect name', () => {
  const body = sprintWorkPayload({ objectType: 'requirement', title: '用户标题', description: '', assignee: 'stale defect owner', assigneeUserIds: ['u2', 'u1', 'u2'], sprint: '123' })
  assert.deepEqual(body.assigneeUserIds, ['u2', 'u1']); assert(!Object.hasOwn(body, 'assignee')); assert(!Object.hasOwn(body, 'status')); assert.equal(body.title, '用户标题'); assert.equal(body.sprint, '123')
  assert(!Object.hasOwn(sprintWorkPayload({objectType:'requirement',status:'待开发'}),'status'))
})
test('switching to a defect submits only one owner, never stale requirement IDs', () => {
  const body = sprintWorkPayload({ objectType: 'defect', assignee: '单人', assigneeUserIds: ['u1', 'u2'] })
  assert.equal(body.assignee, '单人'); assert(!Object.hasOwn(body, 'assigneeUserIds'))
})
test('list and board participants preserve both same-name members and history', () => {
  const data = sprintParticipants({ objectType: 'requirement', assigneeUserIds: ['u1', 'u2', 'u1', 'u3', 'removed'], assignees: [{ id: 'removed', name: '离职用户原文' }] }, members)
  assert.deepEqual(data.map(x => x.id), ['u1', 'u2', 'u3', 'removed']); assert.deepEqual(data.map(x => x.name), ['同名', '同名', '历史成员', '离职用户原文'])
})
test('legacy names with punctuation are never split or matched to an arbitrary same-name ID', () => {
  assert.deepEqual(sprintParticipants({ objectType: 'requirement', assignee: '同名' }, members), [{ id: 'legacy:同名', name: '同名' }])
  assert.deepEqual(sprintParticipants({ objectType: 'defect', assignee: 'A,B' }, members), [{ id: 'legacy:A,B', name: 'A,B' }])
  assert.equal(sprintParticipants({ objectType: 'defect', assignee: '单人', assigneeUserIds: ['u1', 'u2'] }, members).length, 1)
})
test('team cards include secondary participants without modifying unique item or weight data', () => {
  const item = { id: 4, objectType: 'requirement', assigneeUserIds: ['u1', 'u2'], status: '已完成', progress: 100, estimatedHours: 12, actualHours: 8, weightTotal: 19 }
  const input = [item, item, { id: 4, objectType: 'defect', assigneeUserId: 'u2', assignee: '同名', status: '已关闭', progress: 100, estimatedHours: 3, actualHours: 2 }]
  const before = JSON.stringify(input)
  const rows = sprintTeamParticipation(input, members, x => ['已完成', '已关闭'].includes(x.status))
  assert.equal(rows.length, 2); assert.equal(rows.find(x => x.id === 'u1').total, 1); assert.equal(rows.find(x => x.id === 'u2').total, 2)
  assert.equal(rows.find(x => x.id === 'u2').estimated, 15); assert.equal(rows.find(x => x.id === 'u2').progress, 100)
  assert.equal(JSON.stringify(input), before); assert(rows.every(x => !Object.hasOwn(x, 'weightTotal')))
})
test('unassigned work remains explicit and does not generate fake member IDs', () => {
  const rows = sprintTeamParticipation([{ id: 1, objectType: 'requirement', assigneeUserIds: [] }], members, () => false)
  assert.equal(rows.length, 1); assert.equal(rows[0].unassigned, true); assert.equal(rows[0].name, '')
})
console.log(`Passed ${count} sprint participant tests.`)

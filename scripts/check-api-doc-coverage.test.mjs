import assert from 'node:assert/strict'
import test from 'node:test'
import { collectAPIEntries, documentedOperations, missingEntries } from './check-api-doc-coverage.mjs'

test('识别 mux、认证旁路和前缀入口，忽略 API 总入口', () => {
  const entries = collectAPIEntries(`
    mux.Handle("/api/", handler)
    mux.HandleFunc("/api/requirements", a.requirements)
    if r.URL.Path == "/api/auth/login" {}
    if strings.HasPrefix(r.URL.Path, "/api/organization/") {}
  `)
  assert.deepEqual(entries.map(entry => entry.path), ['/api/requirements', '/api/auth/login', '/api/organization/'])
})

test('相同注册及分发路径去重，但保留来源类型', () => {
  const entries = collectAPIEntries('mux.HandleFunc("/api/session", a.session)\nif r.URL.Path == "/api/session" {}')
  assert.equal(entries.length, 1)
  assert.deepEqual(entries[0].kinds, ['mux', 'dispatch'])
})

test('不会把注释和正文中不存在的接口计为文档覆盖', () => {
  assert.deepEqual(collectAPIEntries('// if r.URL.Path == "/api/not-real" {}'), [])
  const docs = '当前不存在 `/api/not-real`\n| GET / POST | `/api/requirements` | 项目读/写 | 内容 |'
  assert.deepEqual(documentedOperations(docs), ['GET /api/requirements', 'POST /api/requirements'])
})

test('前缀入口可由具名子路由覆盖，精确入口不能被子路由冒充', () => {
  const entries = collectAPIEntries('mux.HandleFunc("/api/requirements/", a.requirement)\nmux.HandleFunc("/api/requirements", a.requirements)')
  assert.deepEqual(missingEntries(entries, ['GET /api/requirements/{id}']).map(entry => entry.path), ['/api/requirements'])
})

test('同一路径多个章节出现不增加操作计数，查询参数不影响入口比较', () => {
  const docs = '| DELETE | `/api/automation-rules/{id}?version=1` | 管理 | 删除 |\n| DELETE | `/api/automation-rules/{id}` | 管理 | 删除 |'
  assert.deepEqual(documentedOperations(docs), ['DELETE /api/automation-rules/{id}'])
})

test('新增未文档化入口必须被检测出来', () => {
  const entries = collectAPIEntries('mux.HandleFunc("/api/new-feature", a.feature)')
  assert.equal(missingEntries(entries, []).length, 1)
})

test('无尾斜杠安全前缀允许具名子路由，但不误匹配相似模块名', () => {
  const entries = collectAPIEntries('if strings.HasPrefix(r.URL.Path, "/api/organization") {}')
  assert.equal(missingEntries(entries, ['GET /api/organization/members']).length, 0)
  assert.equal(missingEntries(entries, ['GET /api/organization-other']).length, 1)
})

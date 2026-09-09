import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import ts from 'typescript'
import * as Vue from 'vue'

const read = path => readFileSync(new URL('../' + path, import.meta.url), 'utf8')
const transpile = source => ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText
function load(path, imports = {}, env = {}) {
  const exports = {}
  new Function('require', 'exports', ...Object.keys(env), transpile(read(path)))(id => { if (!(id in imports)) throw Error('Unknown import ' + id); return imports[id] }, exports, ...Object.values(env))
  return exports
}
const mentions = load('src/mentions.ts'), recentMembers = load('src/recentMembers.ts')
const recent = load('src/recentMentions.ts', { './mentions': mentions, './recentMembers': recentMembers })
const directory = [{ id: 'me', name: '我自己', email: 'me@test', active: true, isCurrent: true }, ...Array.from({ length: 8 }, (_, i) => ({ id: 'u' + i, name: '成员' + i, email: 'u' + i + '@test', active: true }))]
function storage() { const map = new Map(); return { map, getItem: key => map.get(key) || null, setItem: (key, value) => map.set(key, value), removeItem: key => map.delete(key) } }
let passed = 0
async function test(name, run) { await run(); passed++; console.log('✓ ' + name) }

await test('self is first, followed by five unique recent members, then the rest of the directory', () => {
  const s = storage()
  for (let i = 0; i < 7; i++) recent.rememberMention(s, 't:me:p', 'u' + i, directory, 'me')
  const ids = recent.readRecentMentions(s, 't:me:p')
  assert.deepEqual(ids, ['u6', 'u5', 'u4', 'u3', 'u2'])
  assert.deepEqual(recent.rankMentionMembers(directory, '', 'me', ids).map(m => m.id), ['me', 'u6', 'u5', 'u4', 'u3', 'u2', 'u0', 'u1', 'u7'])
  recent.rememberMention(s, 't:me:p', 'u4', directory, 'me')
  recent.rememberMention(s, 't:me:p', 'me', directory, 'me')
  assert.deepEqual(recent.readRecentMentions(s, 't:me:p'), ['u4', 'u6', 'u5', 'u3', 'u2'])
  assert(![...s.map.values()][0].includes('@test'))
})
await test('search still finds every eligible member and accepts me in Chinese or English', () => {
  assert.deepEqual(recent.rankMentionMembers(directory, 'u7@', 'me', ['u1']).map(m => m.id), ['u7'])
  for (const q of ['我', 'me', ' ME ']) assert.deepEqual(recent.rankMentionMembers(directory, q, 'me', []).map(m => m.id), ['me'])
  assert.deepEqual(recent.rankMentionMembers(directory, 'unavailable', 'me', ['u1']), [])
})
await test('inactive, removed, duplicate and forged candidates cannot be introduced through history', () => {
  const s = storage(), people = [...directory, directory[1], { id: 'off', name: 'Disabled', active: false }]
  assert.deepEqual(recent.rememberMention(s, 't:me:p', 'outsider', people, 'me'), [])
  assert.deepEqual(recent.rememberMention(s, 't:me:p', 'off', people, 'me'), [])
  const ids = recent.rankMentionMembers(people, '', 'me', ['off', 'gone', 'u1', 'u1']).map(m => m.id)
  assert.equal(new Set(ids).size, ids.length); assert(!ids.includes('off')); assert(!ids.includes('gone'))
})
await test('history is isolated from assignment pickers, projects, accounts and tenants; invalid storage is harmless', () => {
  const s = storage(); recent.rememberMention(s, 't:me:p', 'u1', directory, 'me')
  for (const scope of ['t:me:p2', 't:other:p', 'other:me:p', '']) assert.deepEqual(recent.readRecentMentions(s, scope), [])
  assert.deepEqual(recentMembers.readRecentMembers(s, 't:me:p'), [])
  s.setItem(recent.recentMentionKey('t:me:p'), '{broken'); assert.deepEqual(recent.readRecentMentions(s, 't:me:p'), [])
  s.setItem(recent.recentMentionKey('t:me:p'), '[{},null," a","u1","u1"]'); assert.deepEqual(recent.readRecentMentions(s, 't:me:p'), ['u1'])
  assert.doesNotThrow(() => recent.rememberMention({ getItem() { throw Error('denied') }, setItem() { throw Error('full') } }, 't:me:p', 'u1', directory, 'me'))
})

function composables() {
  const workspace = Vue.reactive({ session: { tenant: { id: 't' }, user: { id: 'me' }, project: { id: 'p' } }, identityConflict: false, operationDisabled: false, switchingProject: false })
  const layout = Vue.ref('t:me'), s = storage(), events = new Map(), mounts = [], unmounts = []
  const win = { addEventListener: (type, cb) => events.set(cb, type), removeEventListener: (type, cb) => events.delete(cb) }
  const module = load('src/useRecentMentions.ts', { vue: { ...Vue, onMounted: cb => mounts.push(cb), onBeforeUnmount: cb => unmounts.push(cb) }, './layoutScope': { layoutScope: layout }, './stores/workspace': { useWorkspaceStore: () => workspace }, './i18n': { t: value => value }, './recentMembers': recentMembers, './recentMentions': recent }, { window: win, localStorage: s })
  const effects = Vue.effectScope(), data = Vue.reactive({ directory: [...directory], query: '' })
  const first = effects.run(() => module.useRecentMentions(() => data.directory, () => data.query))
  const second = effects.run(() => module.useRecentMentions(() => data.directory, () => data.query))
  mounts.forEach(cb => cb())
  return { first, second, data, workspace, layout, s, events, stop() { unmounts.forEach(cb => cb()); effects.stop() } }
}
await test('shared composer history updates without remount, opening does not cache, and unmount removes listeners', () => {
  const m = composables(); assert.equal(m.s.map.size, 0); assert.equal(m.first.matches.value[0].id, 'me')
  m.first.remember('u4'); assert.deepEqual(m.second.matches.value.slice(0, 2).map(x => x.id), ['me', 'u4']); assert.equal(m.second.badge('u4'), '最近提及')
  m.second.remember('u1'); assert.deepEqual(m.first.matches.value.slice(0, 3).map(x => x.id), ['me', 'u1', 'u4'])
  m.stop(); assert.equal(m.events.size, 0)
})
await test('scope changes immediately hide stale candidates until a fresh directory arrives', () => {
  const m = composables(); m.first.remember('u1'); m.workspace.session.project.id = 'p2'
  assert.deepEqual(m.first.matches.value, []); assert.equal(m.first.accepts('u1'), false)
  m.first.remember('u2'); assert.deepEqual(recent.readRecentMentions(m.s, 't:me:p2'), [])
  m.data.directory = [...directory]; assert.equal(m.first.matches.value[0].id, 'me'); assert.equal(m.first.badge('u1'), '')
  m.first.remember('u3'); assert.deepEqual(recent.readRecentMentions(m.s, 't:me:p2'), ['u3']); assert.deepEqual(recent.readRecentMentions(m.s, 't:me:p'), ['u1'])
  m.layout.value = ''; assert.deepEqual(m.first.matches.value, []); m.stop()
})
await test('disabled, impersonation conflict and project switch block suggestions and cache writes', () => {
  for (const key of ['operationDisabled', 'identityConflict', 'switchingProject']) {
    const m = composables(); m.workspace[key] = true; assert.deepEqual(m.first.matches.value, []); m.first.remember('u1'); assert.equal(m.s.map.size, 0); m.stop()
  }
})
await test('plain remarks and rich comments/descriptions share the same ranking and explicit-selection hook', () => {
  for (const path of ['src/components/MentionComment.vue', 'src/components/RichTextEditor.vue']) {
    const source = read(path)
    assert.match(source, /useRecentMentions\(\(\) => props.members/)
    assert.match(source, /recentMentions\.accepts\(member.id\)/)
    assert.match(source, /recentMentions\.remember\(member.id\)/)
    assert.match(source, /recentMentions\.badge\(member.id\)/)
  }
})
console.log(`Passed ${passed} recent-mention ordering, isolation and shared-composer tests.`)

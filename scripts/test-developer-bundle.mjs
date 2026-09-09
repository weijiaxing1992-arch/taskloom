import assert from 'node:assert/strict'
import fs from 'node:fs'
import os from 'node:os'
import path from 'node:path'
import crypto from 'node:crypto'
import { allowed, inspectContent, collect, documents, stageBundle, root } from './build-developer-bundle.mjs'
import { developerHTML } from './developer-web.mjs'

let count = 0
function test(name, fn) { fn(); count++; console.log('✓ ' + name) }
const temporary = fs.mkdtempSync(path.join(os.tmpdir(), 'devflow-bundle-test-'))
const fixture = path.join(temporary, 'source')
function write(relative, content = 'synthetic fixture\n') {
  const file = path.join(fixture, relative)
  fs.mkdirSync(path.dirname(file), { recursive: true })
  fs.writeFileSync(file, content)
}
try {
  for (const file of ['pnpm-lock.yaml', 'go.mod', 'go.sum', '.env.example', 'src/main.ts', 'cmd/server/main.go', 'clients/devflow_macos/lib/main.dart', 'clients/devflow_macos/pubspec.lock', 'scripts/start-developer.mjs']) write(file)
  write('package.json', JSON.stringify({ name: 'fixture', scripts: { dev: 'node portal/build.mjs && vite', build: 'node portal/build.mjs && vite build' }, dependencies: { vue: '3.5.42' } }))
  write('pnpm-workspace.yaml', 'allowBuilds:\n  esbuild: set this to true or false\n')
  for (const name of documents) write('docs/' + name)
  test('source and all lockfiles are explicitly permitted', () => {
    for (const file of ['src/views/Search.vue', 'cmd/server/main.go', 'cmd/server/developer_mode.go', 'clients/devflow_macos/lib/main.dart', 'clients/devflow_macos/test/core/devflow_client_test.dart', 'clients/devflow_macos/pubspec.lock', 'pnpm-lock.yaml', 'go.sum', '.env.example', 'clients/devflow_macos/macos/Runner/Release.entitlements']) assert(allowed(file), file)
  })
  const excluded = ['data/devflow.db', 'src/private.db', 'src/private.sqlite3', 'src/private.db-wal', 'src/.env', 'src/.env.local', '.env.production', '.npmrc', 'src/signing.p12', 'src/certificate.pem', 'src/secret.key', '.git/config', 'work/toolchain/flutter/bin/flutter', 'node_modules/pkg/index.js', 'clients/devflow_macos/build/app.dart', 'clients/devflow_macos/.dart_tool/cache.dart', 'clients/devflow_macos/macos/Flutter/ephemeral/file.xcconfig', 'clients/devflow_macos/macos/Runner.xcodeproj/xcuserdata/private.xcscheme', 'clients/devflow_macos/macos/Pods/secret.swift', 'clients/devflow_macos/acceptance/evidence/private.dart', 'portal/release/TaskLoom.dmg', 'src/archive.zip', 'docs/department-adjustment-20260905.md', 'docs/initial-passwords.md', 'docs/private-record.md', 'README.md', 'clients/devflow_macos/README.md', '../src/main.ts', '/src/main.ts', 'src/../main.ts', 'src\\main.ts']
  test('sensitive paths, private docs, SDKs, databases and release artifacts are rejected', () => { for (const file of excluded) assert.equal(allowed(file), false, file) })
  test('content scanning rejects private keys and high confidence tokens without echoing secrets', () => {
    const values = ['-----BEGIN ' + 'PRIVATE KEY-----', 'AKIA' + 'A'.repeat(16), 'ghp_' + 'a'.repeat(35), 'sk-proj-' + 'b'.repeat(35), 'https://qyapi.weixin.qq.com/cgi-bin/webhook/send?key=' + 'a'.repeat(32)]
    for (const value of values) assert.throws(() => inspectContent('src/fake.ts', Buffer.from(value)), error => !error.message.includes(value))
    assert.doesNotThrow(() => inspectContent('.env.example', Buffer.from('DEVFLOW_SESSION_SECRET=replace-with-at-least-32-random-bytes')))
  })
  test('unknown and sensitive fixture files never reach the selected manifest', () => {
    for (const file of excluded.filter(file => !file.includes('..') && !file.startsWith('/') && !file.includes('\\'))) write(file, 'private synthetic data')
    const files = collect(fixture)
    for (const file of excluded) assert(!files.includes(file), file)
    assert.deepEqual(files.filter(file => file.startsWith('docs/')).sort(), documents.map(name => 'docs/' + name).sort())
  })
  test('selected symlinks cannot read outside the source', () => {
    fs.symlinkSync(path.join(temporary, 'outside.ts'), path.join(fixture, 'src/linked.ts'))
    assert.throws(() => collect(fixture), /符号链接/)
    fs.unlinkSync(path.join(fixture, 'src/linked.ts'))
  })
  test('even a document directory symlink is rejected', () => {
    const directory = path.join(fixture, 'docs'), moved = path.join(temporary, 'documents')
    fs.renameSync(directory, moved); fs.symlinkSync(moved, directory)
    assert.throws(() => collect(fixture), /符号链接/)
    fs.unlinkSync(directory); fs.renameSync(moved, directory)
  })
  test('staging generates usable developer commands without altering original scripts or dependencies', () => {
    const target = path.join(temporary, 'bundle'), manifest = stageBundle(fixture, target)
    const before = JSON.parse(fs.readFileSync(path.join(fixture, 'package.json'))), after = JSON.parse(fs.readFileSync(path.join(target, 'package.json')))
    assert.match(before.scripts.build, /portal/); assert.equal(after.scripts.build, 'node scripts/developer-web.mjs build')
    assert.equal(after.scripts.dev, 'node scripts/developer-web.mjs dev'); assert.deepEqual(after.dependencies, before.dependencies)
    for (const name of ['test-developer-bundle', 'test-search-layout', 'test-watermark', 'test-top-search']) assert(after.scripts.test.includes(name + '.mjs'))
    assert(!after.scripts.test.includes('run-tests.mjs'))
    const builds = fs.readFileSync(path.join(target, 'pnpm-workspace.yaml'), 'utf8')
    assert.match(builds, /esbuild: true/); assert.match(builds, /vue-demi: true/)
    assert.doesNotMatch(builds, /dangerouslyAllowAllBuilds|set this to true or false/)
    for (const entry of manifest.files) {
      const bytes = fs.readFileSync(path.join(target, entry.path))
      assert.equal(entry.bytes, bytes.length)
      assert.equal(entry.sha256, crypto.createHash('sha256').update(bytes).digest('hex'))
      assert(entry.path === 'README.md' || allowed(entry.path))
    }
    assert.equal(fs.readFileSync(path.join(target, 'README.md'), 'utf8'), fs.readFileSync(path.join(fixture, 'docs/developer-environment.md'), 'utf8'))
    assert.throws(() => stageBundle(fixture, target), /新的目录/)
  })
  test('missing required source fails instead of producing a partial distributable', () => {
    fs.renameSync(path.join(fixture, 'go.mod'), path.join(fixture, 'go.mod.held'))
    assert.throws(() => stageBundle(fixture, path.join(temporary, 'incomplete')), /必要文件/)
    fs.renameSync(path.join(fixture, 'go.mod.held'), path.join(fixture, 'go.mod'))
  })
  test('the developer HTML entry removes the portal redirect and imports the application directly', () => {
    const html = developerHTML(fs.readFileSync(path.join(root, 'index.html'), 'utf8'))
    assert(!html.includes('/portal/index.html'))
    assert(html.includes('<script type="module" src="/src/main.ts"></script>'))
    assert.throws(() => developerHTML('<html>unrecognized entry</html>'), /唯一/)
  })
  test('actual repository selection includes exactly the reviewed documentation list', () => {
    const files = collect(root)
    assert(files.length > 50)
    assert.deepEqual(files.filter(file => file.startsWith('docs/')).sort(), documents.map(name => 'docs/' + name).sort())
    assert(files.includes('scripts/start-developer.mjs'))
    for (const file of files) { assert(allowed(file)); inspectContent(file, fs.readFileSync(path.join(root, file))) }
  })
  console.log(`All ${count} developer bundle tests passed.`)
} finally { fs.rmSync(temporary, { recursive: true, force: true }) }

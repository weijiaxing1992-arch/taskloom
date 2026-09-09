import assert from 'node:assert/strict'
import { existsSync, mkdtempSync, mkdirSync, readFileSync, rmSync, symlinkSync, writeFileSync } from 'node:fs'
import { join } from 'node:path'
import { tmpdir } from 'node:os'
import { retainWebAssets } from './retain-web-assets.mjs'

let count = 0
function test(name, run) {
  const root = mkdtempSync(join(tmpdir(), 'devflow-asset-test-'))
  const oldWeb = join(root, 'old'), newWeb = join(root, 'new')
  const write = (web, name, value) => { const file = join(web, name); mkdirSync(join(file, '..'), { recursive: true }); writeFileSync(file, value) }
  try {
    write(oldWeb, 'index.html', 'old entry'); write(newWeb, 'index.html', 'new entry')
    write(oldWeb, 'assets/old.js', 'old runtime'); write(newWeb, 'assets/new.js', 'new runtime')
    run({ root, oldWeb, newWeb, write }); count++; console.log('✓ ' + name)
  } finally { rmSync(root, { recursive: true, force: true }) }
}
test('retains old lazy routes and CSS without replacing the new entry or touching user files', ({ oldWeb, newWeb, write }) => {
  write(oldWeb, 'assets/MyWork-old.js', 'my-work'); write(oldWeb, 'assets/Notifications-old.css', 'old-css')
  write(oldWeb, 'assets/fonts/font.woff2', 'font'); write(oldWeb, 'private.txt', 'not-an-asset')
  const result = retainWebAssets(oldWeb, newWeb)
  assert.deepEqual(result, { copied: 4, reused: 0, total: 5 })
  assert.equal(readFileSync(join(newWeb, 'index.html'), 'utf8'), 'new entry')
  assert.equal(readFileSync(join(newWeb, 'assets/new.js'), 'utf8'), 'new runtime')
  assert.equal(readFileSync(join(newWeb, 'assets/MyWork-old.js'), 'utf8'), 'my-work')
  assert.equal(readFileSync(join(newWeb, 'assets/Notifications-old.css'), 'utf8'), 'old-css')
  assert.equal(existsSync(join(newWeb, 'private.txt')), false)
})
test('repeated retention is idempotent and preserves identical shared chunks', ({ oldWeb, newWeb, write }) => {
  write(oldWeb, 'assets/shared.js', 'shared'); write(newWeb, 'assets/shared.js', 'shared')
  assert.equal(retainWebAssets(oldWeb, newWeb).reused, 1)
  assert.deepEqual(retainWebAssets(oldWeb, newWeb), { copied: 0, reused: 2, total: 3 })
})
test('different contents under the same filename fail before any copy', ({ oldWeb, newWeb, write }) => {
  write(oldWeb, 'assets/shared.js', 'old'); write(newWeb, 'assets/shared.js', 'new')
  assert.throws(() => retainWebAssets(oldWeb, newWeb), /different contents/)
  assert.equal(existsSync(join(newWeb, 'assets/old.js')), false)
  assert.equal(readFileSync(join(newWeb, 'assets/shared.js'), 'utf8'), 'new')
})
test('symlinks on either side are rejected instead of following paths outside assets', ({ root, oldWeb, newWeb }) => {
  symlinkSync(root, join(oldWeb, 'assets/escape'))
  assert.throws(() => retainWebAssets(oldWeb, newWeb), /symlinks/)
  rmSync(join(oldWeb, 'assets/escape'))
  symlinkSync(root, join(newWeb, 'assets/escape'))
  assert.throws(() => retainWebAssets(oldWeb, newWeb), /symlinks/)
  assert.equal(existsSync(join(newWeb, 'assets/old.js')), false)
})
test('file-directory collisions are rejected before copying other files', ({ oldWeb, newWeb, write }) => {
  write(oldWeb, 'assets/fonts/old.woff2', 'font'); write(newWeb, 'assets/fonts', 'not-a-directory')
  assert.throws(() => retainWebAssets(oldWeb, newWeb), /directory collision/)
  assert.equal(existsSync(join(newWeb, 'assets/old.js')), false)
})
test('asset directory symlinks and HTML entry symlinks are rejected', ({ oldWeb, newWeb }) => {
  rmSync(join(newWeb, 'assets'), { recursive: true })
  symlinkSync(join(oldWeb, 'assets'), join(newWeb, 'assets'))
  assert.throws(() => retainWebAssets(oldWeb, newWeb), /real asset directory/)
  rmSync(join(newWeb, 'assets')); mkdirSync(join(newWeb, 'assets'))
  rmSync(join(newWeb, 'index.html')); symlinkSync(join(oldWeb, 'index.html'), join(newWeb, 'index.html'))
  assert.throws(() => retainWebAssets(oldWeb, newWeb), /regular HTML entry/)
})
test('an incomplete staging build cannot receive assets or be mistaken for a valid release', ({ oldWeb, newWeb }) => {
  rmSync(join(newWeb, 'index.html'))
  assert.throws(() => retainWebAssets(oldWeb, newWeb), /ENOENT/)
  assert.equal(existsSync(join(newWeb, 'assets/old.js')), false)
})
test('same directory and nested release paths cannot be used', ({ oldWeb, newWeb }) => {
  assert.throws(() => retainWebAssets(oldWeb, oldWeb), /separate/)
  const nested = join(oldWeb, 'nested'); mkdirSync(nested)
  assert.throws(() => retainWebAssets(oldWeb, nested), /non-nested/)
  assert.equal(readFileSync(join(newWeb, 'index.html'), 'utf8'), 'new entry')
})
test('in-place Vite builds retain content-hashed resources by default', () => {
  assert.match(readFileSync(new URL('../vite.config.ts', import.meta.url), 'utf8'), /build:\s*\{\s*emptyOutDir:\s*false\s*\}/)
})
console.log(`Passed ${count} web asset retention regressions.`)

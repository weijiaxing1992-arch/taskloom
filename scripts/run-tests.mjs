import { readdirSync } from 'node:fs'
import { spawnSync } from 'node:child_process'
const tests = readdirSync(new URL('.', import.meta.url)).filter(name => /^(?:test-.*|.*\.test)\.mjs$/.test(name)).sort()
const failures = []
for (const name of tests) {
  const result = spawnSync(process.execPath, [new URL(name, import.meta.url).pathname], { stdio: 'inherit' })
  if (result.error) throw result.error
  if (result.status !== 0) failures.push(name)
}
if (failures.length) { console.error(`Failed ${failures.length}/${tests.length} suites: ${failures.join(', ')}`); process.exit(1) }
console.log(`All ${tests.length} frontend test suites passed.`)

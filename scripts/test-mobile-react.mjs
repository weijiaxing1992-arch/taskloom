import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'

const root = new URL('..', import.meta.url)
const read = path => readFileSync(new URL(path, root), 'utf8')
let passed = 0
async function test(name, run) {
  await run()
  passed++
  console.log(`✓ ${name}`)
}

await test('React mobile entry is an independent, accessible multipage application', async () => {
  const html = read('mobile.html')
  const entry = read('src/mobile-react/main.tsx')
  const vite = read('vite.config.ts')
  assert.match(html, /id="mobile-root"/)
  assert.match(html, /mobile-react\/main\.tsx/)
  assert.match(entry, /createRoot\(root\)/)
  assert.match(entry, /StrictMode/)
  assert.match(vite, /mobile:fileURLToPath\(new URL\('\.\/mobile\.html'/)
})

await test('mobile API shares HttpOnly session scope and never persists a credential', async () => {
  const source = read('src/mobile-react/mobileApi.ts')
  assert.match(source, /fetch\(`\/api\$\{path\}`/)
  assert.match(source, /credentials: 'same-origin'/)
  assert.match(source, /X-TaskLoom-Project/)
  assert.doesNotMatch(source, /localStorage\.(?:setItem|getItem)\(['"][^'"]*(?:token|secret|password)/i)
})

await test('work item ordering has an explicit requirements-first, defects-second contract', async () => {
  const source = read('src/mobile-react/workItems.ts')
  assert.match(source, /if \(type === '需求'\) return 0/)
  assert.match(source, /if \(type === '缺陷'\) return 1/)
  assert.match(source, /const byType = workTypeRank\(left\.type\) - workTypeRank\(right\.type\)/)
  assert.match(source, /return rightUpdated - leftUpdated/)
  assert.match(source, /range === 'today' \? 1 : 7/)
})

await test('core work and notifications preserve API behaviors and mobile safety affordances', async () => {
  const app = read('src/mobile-react/MobileApp.tsx')
  const css = read('src/mobile-react/mobile.css')
  const index = read('index.html')
  for (const endpoint of ['/session', '/notifications/unread-count', '/my-work?', '/notifications?', '/notifications/read-all']) assert.ok(app.includes(endpoint), `missing ${endpoint}`)
  assert.match(read('src/mobile-react/MobileDetail.tsx'), /'X-TaskLoom-Project':target.projectId/)
  assert.match(app, /sortMobileWorkItems\(items\)/)
  assert.match(app, /sameOriginPath\(item\.url\)/)
  assert.match(app, /aria-live="polite"/)
  assert.match(app, /readMode === 'unread'/)
  assert.match(css, /env\(safe-area-inset-bottom\)/)
  assert.match(css, /prefers-reduced-motion:reduce/)
  assert.match(css, /focus-visible/)
  assert.match(css, /@keyframes dfm-entry/)
  assert.match(index, /query\.get\('desktop'\) !== '1'/)
  for(const route of ['/my-work','/requirements','/iterations','/notifications']) assert(index.includes(`'${route}'`))
  assert.match(index, /query.set\('tab',tab\)/)
  assert.match(index, /\/mobile\.html\?\$\{query\}/)
})

console.log(`Passed ${passed} React mobile workbench checks.`)

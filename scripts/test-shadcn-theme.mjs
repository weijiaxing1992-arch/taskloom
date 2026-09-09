import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'

const read = path => readFileSync(new URL('../' + path, import.meta.url), 'utf8')
const css = read('src/shadcn-theme.css')
const main = read('src/main.ts')
let count = 0
const test = (name, run) => { run(); count++; console.log('✓ ' + name) }

test('shadcn semantic aliases cover both light and account-scoped dark palettes', () => {
  for (const token of ['background', 'foreground', 'card', 'card-foreground', 'popover', 'popover-foreground', 'muted', 'muted-foreground', 'border', 'input', 'ring', 'radius']) {
    assert(css.includes('--' + token + ':'))
  }
  assert.match(css, /:root\[data-theme="dark"\]/)
  for (const alias of ['--surface: var(--background)', '--ink: var(--foreground)', '--line: var(--border)', '--primary-soft: var(--accent)']) assert(css.includes(alias))
  for (const token of ['success-background', 'success-border', 'warning-background', 'warning-border', 'danger-background', 'danger-border']) {
    assert.match(css, new RegExp('--' + token + ':'))
  }
})

test('foundation styles shared controls without a global reset or interaction override', () => {
  assert.match(css, /\[data-slot="popover-content"\]/)
  assert.match(css, /\[data-slot="scroll-area-thumb"\]/)
  assert.match(css, /:focus-visible/)
  assert.match(css, /::-webkit-scrollbar-thumb/)
  assert(!css.includes('pointer-events: none'))
  assert(!css.includes('* {'))
  assert(!css.includes('!important'))
})

test('theme foundation is the last global visual layer after legacy collaboration and sidebar styles', () => {
  const foundation = main.indexOf("import './shadcn-theme.css'")
  assert(foundation > main.indexOf("import './collaboration.css'"))
  assert(foundation > main.indexOf("import './sidebar.css'"))
})

test('shadcn buttons retain an explicit desktop click affordance', () => {
  const button = read('src/components/ui/button/index.ts')
  assert.match(button, /cursor-pointer/)
  assert.match(button, /disabled:cursor-not-allowed/)
})

console.log(`Passed ${count} shadcn visual foundation regressions.`)

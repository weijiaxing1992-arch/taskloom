import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import ts from 'typescript'
import { parse, compileScript, compileTemplate } from 'vue/compiler-sfc'

const read = file => readFileSync(new URL('../' + file, import.meta.url), 'utf8')
const transpile = source => ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 } }).outputText
let passed = 0
function test(name, run) { run(); passed++; console.log('✓ ' + name) }

test('motion layer loads after the shared glass layer and stays scoped to opt-in behavior', () => {
  const main = read('src/main.ts'), css = read('src/motion-system.css')
  assert(main.indexOf("import './motion-system.css'") > main.indexOf("import './glass-system.css'"))
  assert.match(css, /\.df-motion-pressable\s*\{[\s\S]*?overflow: hidden/)
  assert.match(css, /\.df-motion-pressable::after\s*\{[\s\S]*?pointer-events: none/)
  assert.match(css, /@media \(prefers-reduced-motion: no-preference\)/)
  assert.match(css, /@media \(prefers-reduced-motion: reduce\)/)
  assert.match(css, /@media \(forced-colors: active\)/)
  assert.doesNotMatch(css, /\*\s*\{[^}]*animation/)
  assert.doesNotMatch(css, /sprint|requirement-detail/i)
})

test('shared Vue buttons and select controls use the same non-blocking press feedback', () => {
  const button = read('src/components/ui/button/Button.vue')
  const select = read('src/components/AppSelect.vue')
  assert.match(button, /import \{ usePressRipple \} from '@\/usePressRipple'/)
  assert.match(button, /df-motion-pressable/)
  assert.match(button, /@pointerdown="pressRipple\.pointerdown"/)
  assert.match(button, /@keydown="pressRipple\.keydown"/)
  assert.match(select, /df-motion-pressable df-motion-list-row/)
  assert.match(select, /pressRipple\.keydown\(event\);triggerKeydown\(event\)/)
  assert.match(select, /pressRipple\.keydown\(event\);optionKeydown\(event,index\)/)
  const popover = read('src/components/ui/popover/PopoverContent.vue')
  assert.match(popover, /df-motion-overlay/)
})

test('Vue templates compile while preserving native controls and event handlers', () => {
  for (const file of ['src/components/ui/button/Button.vue', 'src/components/AppSelect.vue']) {
    const descriptor = parse(read(file), { filename: file }).descriptor
    const script = compileScript(descriptor, { id: file })
    const template = compileTemplate({ source: descriptor.template.content, filename: file, id: file, compilerOptions: { bindingMetadata: script.bindings } })
    assert.deepEqual(template.errors, [], file)
  }
})

test('ripple origin is visual only and leaves clicks, keyboard events and disabled controls untouched', () => {
  const source = read('src/usePressRipple.ts')
  assert.doesNotMatch(source, /preventDefault|stopPropagation|stopImmediatePropagation/)
  const timers = []
  const exports = {}
  new Function('exports', 'setTimeout', 'clearTimeout', transpile(source))(exports, (callback, delay) => {
    timers.push({ callback, delay })
    return timers.length
  }, () => {})
  const ripple = exports.usePressRipple()
  const values = new Map()
  const target = {
    style: { setProperty: (key, value) => values.set(key, value) },
    dataset: {},
    matches: () => false,
    getBoundingClientRect: () => ({ left: 20, top: 10, width: 100, height: 40 }),
  }
  ripple.pointerdown({ isPrimary: true, button: 0, currentTarget: target, clientX: 45, clientY: 30 })
  assert.equal(target.dataset.motionPress, 'active')
  assert.equal(values.get('--df-motion-origin-x'), '25.00%')
  assert.equal(values.get('--df-motion-origin-y'), '50.00%')
  assert.equal(timers[0].delay, 520)
  timers[0].callback()
  assert.equal(target.dataset.motionPress, undefined)
  ripple.keydown({ key: 'Enter', currentTarget: target })
  assert.equal(values.get('--df-motion-origin-x'), '50.00%')
  const disabled = { ...target, dataset: {}, matches: () => true }
  ripple.pointerdown({ isPrimary: true, button: 0, currentTarget: disabled, clientX: 45, clientY: 30 })
  assert.equal(disabled.dataset.motionPress, undefined)
})

console.log(`Passed ${passed} shared motion system checks.`)

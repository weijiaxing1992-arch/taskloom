import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import { createRequire } from 'node:module'
// 与实际构建使用 Vite 自带的 PostCSS，避免测试额外引入另一个解析器版本。
const postcss = createRequire(import.meta.resolve('vite'))('postcss')
import { typographyValue, uiTypographyPlugin } from './ui-typography.mjs'

const read = path => readFile(new URL('../' + path, import.meta.url), 'utf8')
let passed = 0
const test = async (name, run) => { await run(); passed++; console.log('✓ ' + name) }

await test('legacy font sizes converge to a documented six-step type scale', () => {
  for (const size of [9,10,11,12]) assert.equal(typographyValue(`${size}px`, '.hint'), 'var(--ui-font-caption)')
  for (const size of [13,14]) assert.equal(typographyValue(`${size}px`, '.copy'), 'var(--ui-font-body)')
  for (const size of [15,16,17]) assert.equal(typographyValue(`${size}px`, '.section'), 'var(--ui-font-section)')
  assert.equal(typographyValue('19px', '.subtitle'), 'var(--ui-font-subtitle)')
  assert.equal(typographyValue('21px', '.heading'), 'var(--ui-font-title)')
  assert.equal(typographyValue('26px', '.hero'), 'var(--ui-font-display)')
})
await test('root rem base never scales twice and existing expressions stay intact', async () => {
  const css = (await postcss([uiTypographyPlugin()]).process(':root{font-size:16px}.text{font-size:14px}.rem{font-size:calc(.875rem * var(--devflow-font-scale,1))}', { from: undefined })).css
  assert.match(css, /:root\{font-size:16px\}/)
  assert.match(css, /\.text\{font-size:var\(--ui-font-body\)\}/)
  assert.match(css, /\.rem\{font-size:calc\(\.875rem \* var\(--devflow-font-scale,1\)\)\}/)
  assert.equal(typographyValue('16px','html, :root'), '16px')
  assert.equal(typographyValue('inherit','.field'), 'inherit')
})
await test('authored content and decorative typography keep their original hierarchy', () => {
  for (const selector of ['.rich-content h1','.rich-document h2','.ProseMirror p','.rich-emoji button','.brand b','.avatar']) {
    assert.equal(typographyValue('21px', selector), 'calc(21px * var(--devflow-font-scale, 1))')
  }
  assert.equal(typographyValue('48px','.metric-value'), 'calc(48px * var(--devflow-font-scale, 1))')
})
await test('shared contract wins over lazy route styles and covers portaled controls', async () => {
  const [main, css] = await Promise.all([read('src/main.ts'), read('src/ui-standards.css')])
  assert(main.indexOf("import './ui-standards.css'") < main.indexOf("import './style.css'"))
  const ast = postcss.parse(css)
  assert(ast.nodes.some(node => node.type === 'atrule' && node.name === 'layer' && node.params === 'ui-contract'))
  const controls = []
  ast.walkRules(rule => { if (rule.selector.includes('.app-select-option')) controls.push(rule) })
  assert(controls.length && controls.every(rule => !rule.selector.includes('.app-shell')), 'teleported options are covered too')
  assert.match(css, /--ui-control-height: 32px/)
  assert.match(css, /--ui-control-height: 40px/)
  assert.match(css, /max\(16px,var\(--ui-font-body\)\)/)
})
await test('search frames stay single-border, keyboard-visible and flat in every context', async () => {
  const css = await read('src/ui-standards.css')
  for (const name of ['.search','.category-search','.test-search','.test-rail-search','.top-search-input','.global-search-box','.member-search']) assert(css.includes(name), name)
  const ast = postcss.parse(css)
  const inner = ast.nodes.flatMap(node => node.nodes || []).find(node => node.type === 'rule' && node.selector.endsWith(') input'))
  assert(inner.nodes.some(node => node.prop === 'border' && node.value === '0' && node.important))
  assert(inner.nodes.some(node => node.prop === 'min-width' && node.value === '0'))
  assert.match(css, /:focus-within[^}]*outline: 2px solid var\(--ring\)/)
  assert.match(css, /prefers-reduced-motion:reduce/)
})
await test('mobile touch targets override desktop tokens at the same cascade layer', async () => {
  const ast = postcss.parse(await read('src/ui-standards.css'))
  const sizes = []
  ast.walkDecls('--ui-control-height', declaration => sizes.push(declaration))
  assert.equal(sizes.length, 2)
  assert.equal(sizes[0].value, '32px')
  assert.equal(sizes[1].value, '40px')
  assert.equal(sizes[1].parent.parent.name, 'media')
  assert.equal(sizes[1].parent.parent.params, '(max-width:820px)')
  for (const size of sizes) {
    for (let ancestor = size.parent; ancestor; ancestor = ancestor.parent) {
      assert.notEqual(ancestor.name, 'layer', 'normal layered tokens cannot override unlayered defaults')
    }
  }
})
await test('button variants remain explicit and destructive actions retain semantic color', async () => {
  const [button, css] = await Promise.all([read('src/components/ui/button/Button.vue'), read('src/ui-standards.css')])
  assert.match(button, /:data-variant="variant"/)
  assert.match(button, /variant: 'default'/)
  assert.match(css, /data-variant="destructive"[^}]*var\(--destructive-foreground\)/)
  assert.match(css, /:disabled[^}]*cursor: not-allowed/)
  assert.match(css, /:focus-visible[^}]*outline: 2px solid var\(--ring\)/)
})
console.log(`Passed ${passed} unified UI standard checks.`)

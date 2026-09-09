import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { createRequire } from 'node:module'
const postcss = createRequire(import.meta.resolve('vite'))('postcss')
const read = path => readFileSync(new URL('../' + path, import.meta.url), 'utf8')
for (const path of ['src/views/Projects.vue','src/views/Defects.vue','src/views/MyWork.vue','src/views/Search.vue','src/components/testing/TestDesigns.vue','src/components/testing/TestingOperations.vue']) {
  const body = read(path)
  assert(body.includes('<Icon name="search"/>'), path)
  assert(!body.includes('<span>⌕</span>') && !body.includes('class="search">⌕'), path)
}
const css = read('src/ui-standards.css'), rules = []
postcss.parse(css).walkRules(rule => rules.push(rule))
const frames = rules.find(rule => rule.selector === ':is(.search, .test-search)')
assert(frames.nodes.some(node => node.prop === 'width' && node.value.includes('24em') && node.important))
assert(frames.nodes.some(node => node.prop === 'min-width' && node.value === 'min(100%, var(--search-min-width, 20em))'))
assert(!frames.selector.includes('member') && !frames.selector.includes('date'))
const layout = rules.find(rule => rule.selector.includes('.help-search') && rule.nodes.some(node => node.prop === 'display'))
assert(layout.nodes.some(node => node.prop === 'box-sizing' && node.value === 'border-box'))
assert(!layout.selector.includes('.dependency-search'))
assert(rules.some(rule => rule.selector.endsWith('> svg') && rule.nodes.some(node => node.prop === 'flex' && node.value === '0 0 16px')))
assert(rules.some(rule => rule.parent.type === 'atrule' && rule.parent.params.includes('820px') && rule.nodes.some(node => node.prop === 'flex-basis' && node.value === '100%')))
console.log('Search layout: six glyph migrations, scalable widths, constrained frames and mobile wrapping passed')

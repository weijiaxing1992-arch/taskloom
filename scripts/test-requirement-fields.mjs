import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import ts from 'typescript'

// Reuse the project's compiler without adding a browser or test-runner dependency.
const source = await readFile(new URL('../src/requirementFields.ts', import.meta.url), 'utf8')
const { outputText } = ts.transpileModule(source, { compilerOptions: { module: ts.ModuleKind.ESNext, target: ts.ScriptTarget.ES2022 } })
const fields = await import('data:text/javascript;base64,' + Buffer.from(outputText).toString('base64'))
let count = 0
function test(name, run) { run(); count++; console.log('✓ ' + name) }

test('empty weights contain all five independent, unevaluated dimensions', () => {
  const weights = fields.emptyRoleWeights()
  assert.deepEqual(Object.keys(weights), ['frontend', 'backend', 'algorithm', 'ui', 'product'])
  assert.equal(fields.weightTotal(weights), 0)
  for (const value of Object.values(weights)) assert.deepEqual(value, { userId: '', userIds: [], value: null })
  weights.frontend.value = 5
  assert.equal(weights.backend.value, null)
})
test('null/missing source initializes empty values and a zero sum', () => {
  assert.deepEqual(fields.normalizeRoleWeights(null), fields.emptyRoleWeights())
  assert.deepEqual(fields.normalizeRoleWeights(undefined), fields.emptyRoleWeights())
  assert.equal(fields.weightTotal(null), 0)
  assert.equal(fields.weightTotal(undefined), 0)
})
test('an intentional zero remains distinct from an unfilled dimension', () => {
  const weights = fields.normalizeRoleWeights({ frontend: { userId: 'u_frontend', value: 0 } })
  assert.equal(weights.frontend.value, 0)
  assert.equal(weights.frontend.userId, 'u_frontend')
  assert.equal(weights.backend.value, null)
})
test('five role values sum without invented multipliers', () => {
  const weights = fields.emptyRoleWeights()
  for (const [index, row] of fields.roleWeightDefinitions.entries()) weights[row.key].value = index + 1
  assert.equal(fields.weightTotal(weights), 15)
})
test('quick weight values use the exact requested fixed presets',()=>{
  assert.deepEqual(fields.weightQuickValues,[20,50,100,200,300,400,500,800,1000,2000])
})
test('multiple role owners preserve order and do not multiply the difficulty; explicit empty arrays clear legacy IDs',()=>{
  const weights=fields.normalizeRoleWeights({frontend:{userId:'legacy',userIds:['u_a','u_b','u_a'],value:5},backend:{userId:'old',value:3},ui:{userId:'stale',userIds:[],value:2}})
  assert.deepEqual(weights.frontend.userIds,['u_a','u_b']);assert.equal(weights.frontend.userId,'u_a')
  assert.deepEqual(weights.backend.userIds,['old']);assert.deepEqual(weights.ui.userIds,[]);assert.equal(weights.ui.userId,'')
  assert.equal(fields.weightTotal(weights),10)
})
test('decimal sum matches six-place server precision without floating artifacts', () => {
  assert.equal(fields.weightTotal({ frontend: { value: 0.1 }, backend: { value: 0.2 } }), 0.3)
  assert.equal(fields.weightTotal({ frontend: { value: 0.000001 }, backend: { value: 0.000002 } }), 0.000003)
})
test('invalid values do not poison the preview sum (save validation rejects them)', () => {
  assert.equal(fields.weightTotal({ frontend: { value: NaN }, backend: { value: Infinity }, algorithm: { value: -2 }, ui: { value: '3' }, product: { value: 7 } }), 7)
})
test('tag normalization trims, splits Chinese/English delimiters, deduplicates and handles empty input', () => {
  assert.deepEqual(fields.splitTags(' 核心, 回归，核心\n UI  ,,'), ['核心', '回归', 'UI'])
  assert.deepEqual(fields.splitTags(null), [])
  assert.deepEqual(fields.splitTags(undefined), [])
})
test('tag colors accept hex, normalize case, and reject arbitrary CSS strings', () => {
  assert.equal(fields.validTagColor('#dc2626'), '#DC2626')
  assert.equal(fields.validTagColor('red; background: url(bad)'), '#5B5CE2')
  assert.equal(fields.validTagColor('#fff'), '#5B5CE2')
  const style = fields.tagStyle('#059669')
  assert.equal(style.color, 'var(--tag-foreground, var(--tag-light-fg))')
  assert.match(style['--tag-light-fg'], /^#[0-9A-F]{6}$/)
  assert.match(style['--tag-dark-fg'], /^#[0-9A-F]{6}$/)
  assert.equal(style.backgroundColor, '#05966914')
  assert.equal(style.borderColor, '#05966940')
})
test('light custom tag colors retain their fill but use readable text', () => {
  const luminance = values => values.map(value => { const srgb = value / 255; return srgb <= .04045 ? srgb / 12.92 : ((srgb + .055) / 1.055) ** 2.4 }).reduce((sum, value, index) => sum + value * [0.2126, 0.7152, 0.0722][index], 0)
  const rgb = color => [1, 3, 5].map(offset => parseInt(color.slice(offset, offset + 2), 16))
  for (const color of ['#FFFFFF', '#FFFF00', '#F0FAFC', '#059669', '#000000']) {
    const style = fields.tagStyle(color)
    const background = rgb(color).map(value => value * (20 / 255) + 255 * (235 / 255))
    const contrast = (luminance(background) + .05) / (luminance(rgb(style['--tag-light-fg'])) + .05)
    assert.ok(contrast >= 4.5, color + ' text needs at least 4.5:1 contrast')
    assert.equal(style.backgroundColor, color + '14')
    assert.equal(style.borderColor, color + '40')
  }
})
test('preset and custom tag text meets 4.5:1 against both actual white/dark translucent fills', () => {
  const luminance = values => values.map(value => { const srgb=value/255;return srgb<=.04045?srgb/12.92:((srgb+.055)/1.055)**2.4 }).reduce((sum,value,index)=>sum+value*[.2126,.7152,.0722][index],0)
  const rgb = color => [1,3,5].map(offset=>parseInt(color.slice(offset,offset+2),16))
  const contrast = (a,b) => (Math.max(luminance(a),luminance(b))+.05)/(Math.min(luminance(a),luminance(b))+.05)
  const surfaces={light:['#FFFFFF','#F7F9FC','#F0EFFF','#EEEDFF','#E8F7F1'],dark:['#182132','#1B2638','#202B3E','#2C294B','#302B4C','#25324D','#3D3425','#1D3B37','#422C38']}
  for (const color of [...fields.tagPalette,'#FFFFFF','#000000','#FFFF00','#00FFFF','#FF00FF','#FF0000','#00FF00','#0000FF','#F0FAFC','#121314','#ABCDEF']) {
    const style=fields.tagStyle(color)
    for (const [mode,bases] of Object.entries(surfaces)) for(const base of bases){const background=rgb(color).map((value,index)=>value*20/255+rgb(base)[index]*235/255);assert.ok(contrast(rgb(style['--tag-'+mode+'-fg']),background)>=4.5,`${color} ${mode} on ${base} requires 4.5:1`)}
    assert.equal(style.backgroundColor,color+'14');assert.equal(style.borderColor,color+'40');assert.deepEqual(fields.tagStyle(color),style)
  }
  assert(!/from ['"]\.\/theme['"]/.test(source),'tag styles remain pure and usable without a browser')
})
test('only planned and running iterations accept new requirements', () => {
  for (const status of ['规划中', '进行中']) assert.equal(fields.sprintSelectable({ status }), true)
  for (const status of ['已完成', '已取消', '已归档', 'archived', '', 'unknown']) assert.equal(fields.sprintSelectable({ status }), false)
})
test('sprint normalization preserves full names in both supported response shapes', () => {
  assert.deepEqual(fields.normalizeSprints([{ id: 1, name: '123', status: '规划中' }, { sprint: { id: 2, name: '22 发布迭代', status: '进行中' } }]).map(item => item.name), ['123', '22 发布迭代'])
})
test('invalid or missing creation times are shown as unknown', () => {
  assert.equal(fields.formatCreatedAt(undefined), '—')
  assert.equal(fields.formatCreatedAt('not-a-date'), '—')
})
console.log(`Passed ${count} requirement field tests.`)

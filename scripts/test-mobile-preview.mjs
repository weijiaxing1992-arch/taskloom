import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'

const read = path => readFileSync(new URL('../' + path, import.meta.url), 'utf8')
const preview = read('src/components/MobilePreview.vue')
const shell = read('src/App.vue')
const icons = read('src/components/Icon.vue')

assert.match(shell, /import MobilePreview from '\.\/components\/MobilePreview\.vue'/)
assert.match(shell, /<MobilePreview v-if="workspace\.canManageOrganization&&!session\.impersonation"\/>/)
assert.match(preview, /\/mobile\.html\?tab=\$\{tab\.value\}&preview=1&v=\$\{revision.value\}/)
for(const value of ['work','requirements','iterations','notifications']) assert(preview.includes(`value:'${value}'`))
assert.match(preview, /target="_blank" rel="noopener noreferrer"/)
assert.match(preview, /@click\.self="closePreview"/)
assert.match(preview, /event\.key !== 'Escape'/)
assert.match(preview, /referrerpolicy="no-referrer"/)
assert.match(preview, /与手机端使用相同页面和数据/)
assert.doesNotMatch(preview, /localStorage|sessionStorage|fetch\(/)
assert.match(icons, /mobile: \[/)

console.log('管理员移动端预览入口校验通过：受企业管理员和非代访问双重条件保护，使用固定同源地址。')

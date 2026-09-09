import assert from 'node:assert/strict'
import fs from 'node:fs'
import { parse, compileStyle } from 'vue/compiler-sfc'

// 冷启动需求页不会加载企业管理 CSS，共用弹窗必须独立拥有遮罩、滚动和固定页脚布局。
const source=fs.readFileSync(new URL('../src/components/OrganizationModal.vue',import.meta.url),'utf8')
const {descriptor}=parse(source)
const style=descriptor.styles.find(x=>x.scoped).content
const desktop=style.split('@media')[0]
assert.match(desktop,/\.org-modal-shade\{[^}]*position:fixed/)
assert.match(desktop,/\.org-modal\{[^}]*max-height:[^}]*display:flex[^}]*flex-direction:column/)
assert.match(desktop,/\.org-modal-body\{[^}]*overflow:auto[^}]*min-height:0/)
assert.match(desktop,/\.org-modal>footer\{[^}]*flex:none/)
assert.deepEqual(compileStyle({source:style,filename:'OrganizationModal.vue',id:'test-modal',scoped:true}).errors,[])
const entry=fs.readFileSync(new URL('../src/views/Requirements.vue',import.meta.url),'utf8')
assert.match(entry,/class="compact-heading-actions"><TapdImport/)
const importer=fs.readFileSync(new URL('../src/components/TapdImport.vue',import.meta.url),'utf8')
assert.match(importer,/!parsed\.value\|\|!reviewed\.value\|\|!scope.current\(\)/)
assert.match(importer,/unresolved>0\|\|loading\|\|saving\|\|scope.locked.value/)
console.log('TAPD cold-route modal layout and explicit review guards passed.')

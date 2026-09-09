import assert from 'node:assert/strict'
import {readFileSync} from 'node:fs'
import {parse,compileScript,compileTemplate} from 'vue/compiler-sfc'
const read=p=>readFileSync(new URL('../'+p,import.meta.url),'utf8')
const files=['src/components/AIButton.vue','src/components/RequirementTitleAssistant.vue','src/components/RequirementAITestCases.vue','src/components/testing/TestCaseAIReview.vue','src/components/testing/TestCaseLibrary.vue','src/views/AISettings.vue']
for(const file of files){const source=read(file),{descriptor}=parse(source),script=compileScript(descriptor,{id:file});assert.deepEqual(compileTemplate({source:descriptor.template.content,filename:file,id:file,compilerOptions:{bindingMetadata:script.bindings}}).errors,[]);if(!file.endsWith('/AIButton.vue'))assert(source.includes('<AIButton'))}
const button=read(files[0]);assert(button.includes('aria-hidden="true"'));assert(button.includes(':disabled="busy||disabled"'));assert(button.includes(':aria-busy="busy"'));assert(!/fetch\(|api\(/.test(button))
const css=read('src/ui-standards.css');for(const token of ['linear-gradient(110deg','#fff','ai-action[data-slot="button"]:disabled','ai-action[data-slot="button"]:focus-visible','max-width:820px'])assert(css.includes(token))
console.log('AI 按钮：6 个组件编译、权限与加载状态、魔法棒和响应式样式检查通过。')

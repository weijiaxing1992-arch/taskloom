import assert from 'node:assert/strict'
import fs from 'node:fs'
import ts from 'typescript'
const read=name=>fs.readFileSync(new URL('../'+name,import.meta.url),'utf8')
const view=read('src/components/DeliveryCenter.vue'),host=read('src/views/Organization.vue')
assert.match(host,/section==='delivery'&&context.isTenantAdmin/)
assert.match(host,/section==='delivery'.*role="alert"/)
assert.match(view,/URL.revokeObjectURL/)
assert.doesNotMatch(view,/fetch\(|\/api\//)
const result={}
new Function('exports',ts.transpileModule(read('src/helpDocumentation.ts'),{compilerOptions:{module:ts.ModuleKind.CommonJS,target:ts.ScriptTarget.ES2022}}).outputText)(result)
for(const name of ['acceptance','capacity','functions','deployment','topology']){
 const markdown=read('docs/delivery-'+name+'.md')
 assert.ok(markdown.length>500)
 assert.ok(view.includes('delivery-'+name+'.md?raw'))
 const doc=result.parseHelpDocument({id:name,title:name,filename:name+'.md',description:'',markdown})
 assert.ok(doc.sections.length>=2)
 for(const section of doc.sections)assert.doesNotMatch(result.renderHelpSection(section),/<script|javascript:/i)
}
assert.match(read('docs/delivery-capacity.md'),/未经.*压测|尚未.*压测/)
assert.match(read('docs/delivery-acceptance.md'),/预发布版/)
assert.match(read('docs/delivery-deployment.md'),/Admin.*123456/)
for(const name of ['acceptance','capacity','functions','deployment','topology'])assert.doesNotMatch(read('docs/delivery-'+name+'.md'),/taskloom\.example\.com|Library\/Application Support|accepted-20260906|portal\/config\.json|deploy\/Caddyfile/)
console.log('Passed community delivery document rendering, same-source integration and administrator access checks.')

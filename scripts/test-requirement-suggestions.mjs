import assert from 'node:assert/strict'
import fs from 'node:fs'
import ts from 'typescript'
const source=fs.readFileSync(new URL('../src/requirementSuggestions.ts',import.meta.url),'utf8')
const js=ts.transpileModule(source,{compilerOptions:{module:ts.ModuleKind.ESNext}}).outputText
const {requirementSuggestions:check}=await import('data:text/javascript;base64,'+Buffer.from(js).toString('base64'))
assert.deepEqual(check('  '),[])
const short=check('优化列表，快速导出')
for(const key of ['detail','audience','goal','acceptance','edge','measurable','permission'])assert(short.some(x=>x.key===key),key)
assert(!check('用户希望优化列表，减少查询耗时，网络失败显示提示，响应小于 2 秒。','选择筛选条件后应显示对应结果').some(x=>['audience','goal','acceptance','edge','measurable'].includes(x.key)))
assert(check('快速导出文件').some(x=>x.key==='permission'))
assert(!check('管理员导出文件，提供二次确认').some(x=>x.key==='permission'))
assert(!check('只是普通功能').some(x=>x.key==='permission'))
assert.deepEqual(check('```go\nfunc main() {}\n```'),[])
assert(!source.includes('fetch('))
console.log('Passed local suggestion rules: empty/code-only, omissions, acceptance, measurable outcomes and conditional permission checks.')

import assert from 'node:assert/strict'
import fs from 'node:fs'
import ts from 'typescript'
const source=fs.readFileSync(new URL('../src/views/Requirements.vue',import.meta.url),'utf8')
assert.doesNotMatch(source,/class="req-title-link" role="button"/,'tree disclosure must not be nested inside another button role')
const body=source.match(/const treeItems=computed\(\(\)=>\{([\s\S]*?)\n\}\)/)[1]
const execute=new Function('paged','collapsedRequirementParents',ts.transpileModule('function tree(){'+body+'}\nreturn tree()', {compilerOptions:{target:ts.ScriptTarget.ES2022}}).outputText)
const rows=[{id:1},{id:2,parentId:1},{id:3,parentId:2},{id:4}]
assert.deepEqual(execute({value:rows},{value:[1]}).map(x=>x.id),[1,4])
assert.deepEqual(execute({value:rows},{value:[2]}).map(x=>[x.id,x._treeDepth]),[[1,0],[2,1],[4,0]])
assert.deepEqual(execute({value:rows},{value:[]}).map(x=>x._treeDepth),[0,1,2,0])
assert.equal(execute({value:[{id:2,parentId:1}]},{value:[1]})[0]._treeDepth,0)
assert.equal(execute({value:[{id:1,parentId:2},{id:2,parentId:1}]},{value:[]}).length,2)
console.log('Passed requirement tree collapse, nesting, filtered parent and cycle regressions.')

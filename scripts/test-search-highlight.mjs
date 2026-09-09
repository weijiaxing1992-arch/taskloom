import assert from 'node:assert/strict'
import fs from 'node:fs'
import ts from 'typescript'
const source=fs.readFileSync(new URL('../src/searchHighlight.ts',import.meta.url),'utf8')
const js=ts.transpileModule(source,{compilerOptions:{module:ts.ModuleKind.ESNext,target:ts.ScriptTarget.ES2022}}).outputText
const {searchMatchRanges:match,startSearchHighlights}=await import('data:text/javascript;base64,'+Buffer.from(js).toString('base64'))
assert.deepEqual(match('需求优化需求','需求'),[[0,2],[4,6]])
assert.deepEqual(match('Vue vue VUE','vue'),[[0,3],[4,7],[8,11]])
assert.deepEqual(match('接口 C++ [a]','C++ [a]'),[[3,6],[7,10]])
assert.deepEqual(match('<script>alert(1)</script>','<script>'),[[0,8]])
assert.deepEqual(match('中文','  '),[])
assert.deepEqual(match('需求测试','需求  测试'),[[0,2],[2,4]])
assert.deepEqual(match('😀需求','需求'),[[2,4]])
assert.deepEqual(match('aaa','aa a'),[[0,2],[2,3]])
assert.equal(typeof startSearchHighlights(),'function')
assert(!source.includes('innerHTML'));assert(!source.includes('replaceWith'));assert(!source.includes('fetch('))
console.log('Passed search highlight literal, Unicode, multi-keyword, casing, empty query and non-mutating safety tests.')

import assert from 'node:assert/strict'
import {readFileSync} from 'node:fs'
import ts from 'typescript'
import * as lowlight from 'lowlight'
import {parse,compileScript,compileTemplate} from 'vue/compiler-sfc'
const source=readFileSync(new URL('../src/codeHighlight.ts',import.meta.url),'utf8'),exports={}
new Function('require','exports',ts.transpileModule(source,{compilerOptions:{module:ts.ModuleKind.CommonJS,target:ts.ScriptTarget.ES2022}}).outputText)(key=>key==='lowlight'?lowlight:null,exports)
const {codeHighlighter,detectCodeLanguage,splitCodeFences,fencedCode}=exports
const text=node=>node.type==='text'?node.value:(node.children||[]).map(text).join('')
const hasHighlight=node=>node.properties?.className?.some(c=>c.startsWith('hljs-'))||(node.children||[]).some(hasHighlight)
for(const [language,code] of Object.entries({vue:'<template><div>{{ count }}</div></template>\n<script setup lang="ts">\nconst count: number = 1\n</script>\n<style scoped>.card { color: red; }</style>',php:'<?php\n$answer = "内容";\necho $answer;',go:'package main\nfunc main() {\n\tprintln("ok")\n}',cpp:'#include <iostream>\nint main() { std::cout << "ok"; }'})){
 assert.equal(detectCodeLanguage(code),language)
 const tree=codeHighlighter.highlight(language,code);assert.equal(text(tree),code);assert.ok(hasHighlight(tree),language)
 assert.equal(text(codeHighlighter.highlight('auto',code)),code)
 for(const value of [code,code+'\n','\t'+code+'\n\n',code+'\n```\n嵌套围栏']){
  const parts=splitCodeFences('说明\n'+fencedCode(value,language)+'\n结尾')
  assert.equal(parts[1].kind,'code');assert.equal(parts[1].text,value);assert.equal(parts[1].language,language)
 }
}
const hostile='<script>alert(1)</script><img src=x onerror=alert(2)>'
assert.equal(text(codeHighlighter.highlight('html',hostile)),hostile)
assert.deepEqual(codeHighlighter.highlight('unknown',hostile).children,[{type:'text',value:hostile}])
assert.equal(text(codeHighlighter.highlight('auto','x'.repeat(30001))),'x'.repeat(30001))
assert.equal(splitCodeFences('```go\n未闭合')[0].kind,'text')
for(const component of ['RichCodeBlock','CodeTextView','CodeTextEditor','CodeInsertTools','RichTextEditor','MentionComment']){
 const source=readFileSync(new URL('../src/components/'+component+'.vue',import.meta.url),'utf8')
 const {descriptor}=parse(source),script=compileScript(descriptor,{id:component})
 const result=compileTemplate({source:descriptor.template.content,filename:component+'.vue',id:component,compilerOptions:{bindingMetadata:script.bindings}})
 assert.deepEqual(result.errors,[]);assert.ok(!source.includes('v-html'),component)
}
console.log('代码阅读通过：Vue/PHP/Go/C++ 高亮、自动识别、缩进换行、嵌套围栏、恶意 HTML、容量保护及组件编译。')

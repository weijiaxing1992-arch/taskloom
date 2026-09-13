import assert from 'node:assert/strict'
import {readFileSync} from 'node:fs'
import ts from 'typescript'
import * as lowlight from 'lowlight'
import * as Vue from 'vue'
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
// Tiptap NodeViewContent 有内联 pre-wrap；仅设置父级 pre 无法保留长行，必须显式继承。
const css=readFileSync(new URL('../src/code-reading.css',import.meta.url),'utf8')
const contentRule=css.match(/\.code-reading \[data-node-view-content\]\s*\{([^}]+)\}/)?.[1]||''
assert.match(contentRule,/white-space:\s*inherit\s*!important/)
assert.match(contentRule,/width:\s*max-content/)
assert.match(contentRule,/word-break:\s*normal/)
assert.match(css,/\.code-reading\.code-wrap \[data-node-view-content\]\s*\{[^}]*width:\s*auto/)
for(const component of ['RichCodeBlock','CodeTextView','CodeTextEditor','CodeInsertTools','RichTextEditor','MentionComment']){
 const source=readFileSync(new URL('../src/components/'+component+'.vue',import.meta.url),'utf8')
 const {descriptor}=parse(source),script=compileScript(descriptor,{id:component})
 const result=compileTemplate({source:descriptor.template.content,filename:component+'.vue',id:component,compilerOptions:{bindingMetadata:script.bindings}})
 assert.deepEqual(result.errors,[]);assert.ok(!source.includes('v-html'),component)
}
// 挂载真实模板并点击按钮，防止同一条评论里的多个代码块共享换行状态。
const viewSource=readFileSync(new URL('../src/components/CodeTextView.vue',import.meta.url),'utf8')
const viewScript=compileScript(parse(viewSource).descriptor,{id:'code-view-wrap-test',inlineTemplate:true}).content,viewModule={}
new Function('require','exports',ts.transpileModule(viewScript,{compilerOptions:{module:ts.ModuleKind.CommonJS,target:ts.ScriptTarget.ES2022}}).outputText)(key=>key==='vue'?Vue:key==='../codeHighlight'?exports:key==='../i18n'?{t:s=>s}:{},viewModule)
const renderer=Vue.createRenderer({createElement:()=>({}),createText:()=>({}),createComment:()=>({}),insert(){},remove(){},setText(){},setElementText(){},parentNode:()=>null,nextSibling:()=>null,patchProp(){}})
const root=Vue.h(viewModule.default,{text:'```go\npackage main\n```\n分隔\n```php\n<?php echo 1;\n```'}),container={}
renderer.render(root,container)
const collect=(node,type)=>!node||typeof node!=='object'?[]:[...(node.type===type?[node]:[]),...(Array.isArray(node.children)?node.children.flatMap(child=>collect(child,type)):[])]
const sections=()=>collect(root.component.subTree,'section'),wrapButton=section=>collect(section,'button')[0]
assert.equal(sections().length,2);assert.equal(wrapButton(sections()[0]).props['aria-pressed'],false)
wrapButton(sections()[0]).props.onClick();await Vue.nextTick()
assert.equal(wrapButton(sections()[0]).props['aria-pressed'],true);assert.equal(wrapButton(sections()[1]).props['aria-pressed'],false)
assert.match(sections()[0].props.class,/code-wrap/);assert(!sections()[1].props.class.includes('code-wrap'))
wrapButton(sections()[1]).props.onClick();await Vue.nextTick();wrapButton(sections()[0]).props.onClick();await Vue.nextTick()
assert.equal(wrapButton(sections()[0]).props['aria-pressed'],false);assert.equal(wrapButton(sections()[1]).props['aria-pressed'],true)
renderer.render(null,container)
console.log('代码阅读通过：Vue/PHP/Go/C++ 高亮、自动识别、独立换行、缩进、嵌套围栏、恶意 HTML、容量保护及组件编译。')

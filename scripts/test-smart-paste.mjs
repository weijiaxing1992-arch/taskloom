import assert from 'node:assert/strict'
import {readFileSync} from 'node:fs'
import ts from 'typescript'
import * as marked from 'marked'
import * as lowlight from 'lowlight'
const evaluate=(path,imports={})=>{const out={};new Function('require','exports',ts.transpileModule(readFileSync(new URL('../src/'+path,import.meta.url),'utf8'),{compilerOptions:{module:ts.ModuleKind.CommonJS,target:ts.ScriptTarget.ES2022}}).outputText)(key=>imports[key],out);return out}
const mentions=evaluate('mentions.ts'),rich=evaluate('richText.ts',{'./mentions':mentions}),md=evaluate('markdownImport.ts',{marked,'./richText':rich}),code=evaluate('codeHighlight.ts',{lowlight}),paste=evaluate('editorPaste.ts',{'./codeHighlight':code,'./markdownImport':md})
for(const [language,text] of Object.entries({vue:'<template><p>{{ title }}</p></template>',php:'<?php\n$answer = "ok";',go:'package main\nfunc main() {\n\tprintln("ok")\n}\n',cpp:'#include <iostream>\nint main() { std::cout << "ok"; }',javascript:'const title = "hello";',typescript:'const count: number = 1;',json:'{"name":"demo"}'})) {
 const result=paste.analyzePaste(text);assert.equal(result.kind,'code');assert.equal(result.language,language);assert.equal(result.document.content[0].content[0].text,text)
 const wrapped=paste.clipboardCodeText(text);assert.equal(code.splitCodeFences(wrapped)[0].text,text)
}
for(const text of ['请前端完善这个页面','普通内容\n第二行','functionality is important','go to project','<img src=x onerror=alert(1)>'])assert.equal(paste.analyzePaste(text),null)
const source='# 一级\n\n###### 六级\n\n> 引用\n\n- [x] **完成**\n- [ ] `待做`\n\n| 左 | 右 |\n| :--- | ---: |\n| A | B |\n\n```go\nfunc main() {\n\tprintln("ok")\n}\n```\n\n[链接](https://example.test)\n\n---'
const result=paste.analyzePaste(source);assert.equal(result.kind,'markdown');assert.deepEqual(result.document.content.filter(n=>n.type==='heading').map(n=>n.attrs.level),[1,6])
const table=result.document.content.find(n=>n.type==='table');assert.equal(table.content[0].content[1].attrs.textAlign,'right')
assert.equal(rich.normalizeRichDocument(result.document).content.find(n=>n.type==='heading'&&n.attrs.level===6).attrs.level,6)
assert.equal(rich.normalizeRichDocument(result.document).content.find(n=>n.type==='table').content[1].content[1].attrs.textAlign,'right')
assert.ok(rich.richTextPlain(result.document).includes('☑ 完成'))
assert.equal(paste.analyzePaste('print(42)','python').language,'python')
assert.equal(paste.analyzePaste('普通正文','plaintext'),null)
assert.equal(paste.analyzePaste('x'.repeat(100001)),null)
assert.equal(paste.insideCodeFence('说明\n```go\nfunc '),true);assert.equal(paste.insideCodeFence('```go\ncode\n```\n'),false)
assert.equal(paste.clipboardCodeText('```go\nfunc main() {}\n```'),'```go\nfunc main() {}\n```')
const unsafe=md.importMarkdown('# title\n\n<script>alert(1)</script>\n\n![x](https://tracking.test/a.png)');assert.ok(unsafe.warnings.length===2);assert.ok(!unsafe.document.content.some(n=>n.type==='image'))
console.log('智能粘贴：语言识别、普通文本、IDE 提示、Markdown 标题/表格/任务/引用/代码、备注围栏与安全边界通过。')

import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import ts from 'typescript'
import * as marked from 'marked'
const evaluate=(path,imports={})=>{const exports={};new Function('require','exports',ts.transpileModule(readFileSync(new URL('../'+path,import.meta.url),'utf8'),{compilerOptions:{module:ts.ModuleKind.CommonJS,target:ts.ScriptTarget.ES2022}}).outputText)(key=>imports[key],exports);return exports}
const mentions=evaluate('src/mentions.ts'), rich=evaluate('src/richText.ts',{'./mentions':mentions}), md=evaluate('src/markdownImport.ts',{'marked':marked,'./richText':rich})
const source='# 验收标准\n\n- [x] 支持 **文档导入**\n- [ ] 保留 `变量`\n\n| 字段 | 内容 |\n| --- | --- |\n| 姓名 | 林夏 |\n\n```go\nfunc main() {\n\tprintln("<script>")\n}\n```\n'
const result=md.importMarkdown(source)
assert.deepEqual(result.document.content.map(n=>n.type),['heading','bulletList','table','codeBlock'])
assert.equal(result.document.content[2].content[1].content[1].content[0].content[0].text,'林夏')
assert.equal(result.document.content[3].attrs.language,'go')
assert.equal(result.document.content[3].content[0].text,'func main() {\n\tprintln("<script>")\n}')
assert.ok(rich.richTextPlain(result.document).includes('☑ 支持 文档导入'))
assert.ok(!rich.richTextPlain(result.document).includes('[x]'))
assert.equal(rich.richTextPlain(result.document),rich.richTextPlain(rich.normalizeRichDocument(result.document)))
const hostile=md.importMarkdown('<script>alert(1)</script>\n\n![x](https://tracking.test/a.png)\n\n[click](javascript:alert(1))\n\n@林夏')
const flat=[];const visit=n=>{flat.push(n);n.content?.forEach(visit)};visit(hostile.document)
assert.ok(!flat.some(n=>['image','mention'].includes(n.type)))
assert.ok(!flat.some(n=>n.marks?.some(m=>m.type==='link')))
assert.ok(hostile.warnings.length>=3)
assert.match(rich.richTextPlain(hostile.document),/tracking\.test/)
assert.throws(()=>md.importMarkdown('a'.repeat(100001)),/100000/)
assert.throws(()=>md.importMarkdown('|'+Array(31).fill('列').join('|')+'|\n|'+Array(31).fill('---').join('|')+'|'),/30 列/)
assert.ok(md.importMarkdown('').document.content.length)
console.log('Markdown 导入：结构、代码保真、表格、恶意内容和容量限制通过')

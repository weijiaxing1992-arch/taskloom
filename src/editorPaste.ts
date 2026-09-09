import { detectCodeLanguage, fencedCode } from './codeHighlight'
import { importMarkdown } from './markdownImport'
import type { JSONContent } from '@tiptap/core'

// 优先尊重显式 Markdown 围栏；纯代码只在有明确语言特征时转换，避免吞掉普通文案。
export function analyzePaste(text:string, editorLanguage=''): {document:JSONContent;kind:'code'|'markdown';language?:string;warnings:string[]}|null {
  if(!text||text.length>100000)return null
  const markdown=/^\s{0,3}(?:#{1,6}\s|`{3,}|~{3,}|>\s|[-*+]\s|\d+[.)]\s)|\n\s*\|?\s*:?-{3,}:?\s*\|/m.test(text)
  const aliases:Record<string,string>={javascriptreact:'javascript',typescriptreact:'typescript','c++':'cpp',golang:'go'}
  const hint=aliases[editorLanguage]||editorLanguage
  const language=detectCodeLanguage(text)
  if(!markdown&&(language!=='auto'||hint&&/^[\w+#.-]{1,40}$/.test(hint)&&!['plaintext','markdown'].includes(hint))) {
    const selected=hint&&/^[\w+#.-]{1,40}$/.test(hint)?hint:language
    return {kind:'code',language:selected,warnings:[],document:{type:'doc',content:[{type:'codeBlock',attrs:{language:selected},content:[{type:'text',text}]}]}}
  }
  if(markdown||editorLanguage==='markdown'||/\*\*[^*\n]+\*\*|\[[^\]\n]+\]\([^\n]+\)|`[^`\n]+`/.test(text)) return {kind:'markdown',...importMarkdown(text)}
  return null
}
export function clipboardCodeText(text:string) { const language=detectCodeLanguage(text);return language==='auto'||/^\s*(`{3,}|~{3,})/m.test(text)?text:fencedCode(text,language) }
export function insideCodeFence(prefix:string) {
  let fence=''
  for(const line of prefix.split('\n')) {const match=line.match(/^\s{0,3}(`{3,}|~{3,})/);if(!match)continue;const run=match[1]!;if(!fence)fence=run;else if(run[0]===fence[0]&&run.length>=fence.length)fence=''}
  return !!fence
}

import { common, createLowlight } from 'lowlight'

// 仅生成高亮语法树，不执行代码、不将用户内容作为 HTML 注入。
const engine = createLowlight(common)
engine.register('vue', () => ({ name: 'Vue', subLanguage: 'xml', contains: [
  { begin: /<script\b[^>]*>/, end: /<\/script\s*>/, subLanguage: 'typescript', excludeBegin: true, excludeEnd: true },
  { begin: /<style\b[^>]*>/, end: /<\/style\s*>/, subLanguage: 'css', excludeBegin: true, excludeEnd: true },
  { begin: /\{\{/, end: /\}\}/, subLanguage: 'javascript', excludeBegin: true, excludeEnd: true },
] }))
engine.registerAlias({ xml: ['html'], cpp: ['c++', 'cxx', 'hpp'], javascript: ['js'], typescript: ['ts'], go: ['golang'] })
export const codeLanguages = ['auto', 'plaintext', 'vue', 'javascript', 'typescript', 'php', 'go', 'cpp', 'c', 'python', 'java', 'dart', 'json', 'sql', 'bash', 'html', 'css', 'yaml', 'markdown']
export function languageLabel(language: string) { return ({ auto: '自动识别', plaintext: '纯文本', vue: 'Vue', php: 'PHP', go: 'Go', cpp: 'C++', 'c++': 'C++' } as Record<string,string>)[language] || language }
export function detectCodeLanguage(code: string): string {
  if(code.length>100000) return 'auto'
  if (/<template[\s>]|<script\b[^>]*\bsetup\b|<style\b[^>]*\bscoped\b/.test(code)) return 'vue'
  if (/<\?php\b|\$\w+\s*->/.test(code)) return 'php'
  if (/^\s*package\s+\w+|\bfunc\s+\w+\s*\(/m.test(code)) return 'go'
  if (/^\s*#include\s*[<"]|\bstd::/m.test(code)) return 'cpp'
  if (/^\s*(interface\s+\w+|type\s+\w+\s*=)|\b(const|let)\s+\w+\s*:\s*(string|number|boolean)\b/m.test(code)) return 'typescript'
  if (/^\s*(?:export\s+)?(?:const|let|var)\s+\w+\s*=|^\s*(?:export\s+)?(?:async\s+)?function\s+\w+\s*\(|\bconsole\.(log|error)\(/m.test(code)) return 'javascript'
  if (/^\s*\$\w+\s*=|^\s*(?:echo|namespace)\s+.+;/m.test(code)) return 'php'
  if (/^\s*(?:async\s+)?def\s+\w+\s*\([^\n]*\)\s*:/m.test(code)) return 'python'
  if (/^\s*(SELECT\s+.+\s+FROM\b|CREATE\s+TABLE\b|INSERT\s+INTO\b)/im.test(code)) return 'sql'
  if (/^\s*[\[{]/.test(code)) { try { JSON.parse(code);return 'json' } catch { /* 不是完整 JSON 时不猜测。 */ } }
  return 'auto'
}
const plain = (value:string) => ({ type:'root' as const, children:[{type:'text' as const,value}], data:{} })
// 大块代码仍完整保留，但停止高亮计算，避免输入期间阻塞页面。
export const codeHighlighter = {
  registered: (language:string) => language==='auto'||engine.registered(language),
  listLanguages: () => [...engine.listLanguages(), 'auto'],
  highlight(language:string, value:string) {
    if(value.length>30000||language==='plaintext') return plain(value)
    try {
      const selected=language==='auto'?detectCodeLanguage(value):language
      if(selected==='auto') return engine.highlightAuto(value,{subset:['javascript','typescript','php','go','cpp','json','sql','bash','xml']})
      return engine.registered(selected)?engine.highlight(selected,value):plain(value)
    } catch { return plain(value) }
  },
  highlightAuto(value:string) { return this.highlight('auto',value) },
}
export type CodePart = { kind:'text'|'code'; text:string; language:string }
// 解析围栏而非整段 HTML；旧评论仍是原始字符串，API 与导出格式不变。
export function splitCodeFences(source:string):CodePart[] {
  const parts:CodePart[]=[]
  const pattern=/^([ \t]*)(`{3,}|~{3,})[ \t]*([^\r\n]*)\r?\n/gm
  let offset=0,match:RegExpExecArray|null
  while((match=pattern.exec(source))) {
    const fence=match[2]!, start=pattern.lastIndex
    const close=new RegExp('^[ \\t]*'+fence[0]+'{'+fence.length+',}[ \\t]*(?:\\r?\\n|$)','gm')
    close.lastIndex=start
    const end=close.exec(source)
    if(!end) continue
    if(match.index>offset) parts.push({kind:'text',text:source.slice(offset,match.index),language:''})
    // 末尾一个换行属于围栏分隔符；代码自身的其余换行原样保留。
    const text=source.slice(start,end.index).replace(/\r?\n$/,'')
    parts.push({kind:'code',text,language:match[3]?.trim().split(/\s+/)[0]?.toLowerCase()||'auto'})
    offset=close.lastIndex;pattern.lastIndex=offset
  }
  if(offset<source.length) parts.push({kind:'text',text:source.slice(offset),language:''})
  return parts
}
export function fencedCode(code:string,language:string) {
  const longest=Math.max(2,...(code.match(/`+/g)||[]).map(run=>run.length))
  const fence='`'.repeat(longest+1)
  return fence+(language==='auto'?'':language)+'\n'+code+'\n'+fence
}

import { marked, type Token, type Tokens } from 'marked'
import type { JSONContent } from '@tiptap/core'
import { safeRichLink, type RichDocument } from './richText'

// 仅消费 Markdown token 并构造白名单 JSON；不渲染解析器输出的 HTML。
export function importMarkdown(source: string): { document: RichDocument; warnings: string[] } {
  if (source.length > 100000) throw Error('Markdown 最多支持 100000 个字符')
  const warnings = new Set<string>()
  let nodes = 0
  const text = (value: string): JSONContent[] => value ? [{type:'text',text:value}] : []
  function convert(tokens: Token[], inline = false, depth = 0): JSONContent[] {
    if (depth > 18) throw Error('Markdown 层级过深，请拆分后导入')
    return tokens.flatMap((token): JSONContent[] => {
      if (++nodes > 1800) throw Error('Markdown 内容过多，请拆分后导入')
      const nested = (items: Token[] = [], asInline = false) => convert(items, asInline, depth + 1)
      const paragraph = (content: JSONContent[]): JSONContent => ({type:'paragraph',content})
      switch (token.type) {
        case 'space': return []
        case 'def': return [] // 引用式链接定义已由解析器解析到 link token，不作为正文重复展示。
        case 'checkbox': return [] // 勾选状态由 list_item 统一加一次，避免重复显示 [x]。
        case 'heading': {
          return [{type:'heading',attrs:{level:Math.min(token.depth,6)},content:nested(token.tokens,true)}]
        }
        case 'paragraph': return [paragraph(nested(token.tokens,true))]
        case 'text': { const content = token.tokens ? nested(token.tokens,true) : text(token.text); return inline ? content : [paragraph(content)] }
        case 'escape': return text(token.text)
        case 'codespan': return text(token.text).map(node=>({...node,marks:[{type:'code'}]}))
        case 'strong': case 'em': case 'del': {
          const mark = token.type==='strong'?'bold':token.type==='em'?'italic':'strike'
          return nested(token.tokens,true).map(node=>({...node,marks:[...(node.marks||[]),{type:mark}]}))
        }
        case 'link': {
          const content=nested(token.tokens,true), href=safeRichLink(token.href)
          if (!href) { warnings.add('不支持的链接已保留为文本'); return [...content,...text(` (${token.href})`)] }
          return content.map(node=>({...node,marks:[...(node.marks||[]),{type:'link',attrs:{href}}]}))
        }
        case 'image': warnings.add('图片地址已保留，请粘贴或上传原图片'); return text(`[图片：${token.text}] (${token.href})`)
        case 'br': return [{type:'hardBreak'}]
        case 'hr': return [{type:'horizontalRule'}]
        case 'code': {
          const language=(token.lang||'auto').split(/\s/)[0] || 'auto'
          return [{type:'codeBlock',attrs:{language:/^[\w+#.\-]{1,40}$/.test(language)?language:'plaintext'},content:text(token.text)}]
        }
        case 'blockquote': return [{type:'blockquote',content:nested(token.tokens)}]
        case 'list': return [{type:token.ordered?'orderedList':'bulletList',...(token.ordered?{attrs:{start:Number(token.start)||1}}:{}),content:token.items.map((item: Tokens.ListItem)=>{
          const content=nested(item.tokens)
          if (content[0]?.type!=='paragraph') content.unshift(paragraph([]))
          if (item.task) { warnings.add('任务勾选状态按文字保留，不会自动创建测试任务'); content[0]!.content=[...text(item.checked?'☑ ':'☐ '),...(content[0]!.content||[])] }
          return {type:'listItem',content}
        })}]
        case 'table': {
          if (token.header.length>30 || token.rows.length>=300) throw Error('表格最多支持 300 行、30 列')
          const row=(cells: Tokens.TableCell[], header=false): JSONContent => ({type:'tableRow',content:cells.map((cell,index)=>({type:header?'tableHeader':'tableCell',...(token.align[index]?{attrs:{textAlign:token.align[index]}}:{}),content:[paragraph(nested(cell.tokens,true))]}))})
          return [{type:'table',content:[row(token.header,true),...token.rows.map((cells: Tokens.TableCell[])=>row(cells))]}]
        }
        case 'html': warnings.add('HTML 按原始文本保留'); return inline?text(token.raw):[paragraph(text(token.raw))]
        default: warnings.add('部分扩展语法按原始文本保留'); return inline?text(token.raw):[paragraph(text(token.raw))]
      }
    })
  }
  const content = convert(marked.lexer(source,{gfm:true}))
  // 标记解析计数之外，还检查表格单元格等生成节点，提前对齐服务端文档上限。
  let count=0
  function validate(node: JSONContent, depth=0) { if (++count>4500 || depth>24) throw Error('Markdown 内容过多或层级过深，请拆分后导入'); node.content?.forEach(child=>validate(child,depth+1)) }
  const document: RichDocument={type:'doc',content:content.length?content:[{type:'paragraph'}]}
  validate(document)
  return {document,warnings:[...warnings]}
}

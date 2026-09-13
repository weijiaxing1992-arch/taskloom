import { lazy, Suspense, useMemo, type ReactNode } from 'react'
import type { JSONContent } from '@tiptap/core'
import { importMarkdown } from '../markdownImport'
import { safeRichLink } from '../richText'
import { hasContent } from './MobilePrimitives'
import { Attachment } from './MobileAttachment'
export { Attachment } from './MobileAttachment'
export { hasContent, ItemCard, StatusBadge, LoadState } from './MobilePrimitives'
const CodeBlock = lazy(() => import('./MobileCodeBlock'))


// 复用现有 Markdown 白名单解析器，以 React 节点渲染；不注入任意 HTML。
export function MobileContent({ text = '', document, requirementId = 0, projectId = '', active = true }: { text?: string; document?: JSONContent | null; requirementId?: number; projectId?: string; active?: boolean }) {
  const doc = useMemo(() => { if (document && hasContent(document)) return document; try { return importMarkdown(text).document } catch { return {type:'doc',content:[{type:'paragraph',content:[{type:'text',text}]}]} } }, [document,text])
  function render(node: JSONContent, index = 0, depth = 0): ReactNode {
    if (depth > 30) return null
    const children = node.content?.map((child,i) => render(child,i,depth+1))
    const attrs = node.attrs || {}
    if(node.type === 'text') {
      let result:ReactNode = node.text
      for(const mark of node.marks || []) {
        if(mark.type === 'bold') result=<strong>{result}</strong>
        if(mark.type === 'italic') result=<em>{result}</em>
        if(mark.type === 'underline') result=<u>{result}</u>
        if(mark.type === 'strike') result=<s>{result}</s>
        if(mark.type === 'code') result=<code>{result}</code>
        if(mark.type === 'link') { const href=safeRichLink(mark.attrs?.href); if(href)result=<a href={href} target="_blank" rel="noopener noreferrer">{result}</a> }
      }
      return <span key={index}>{result}</span>
    }
    switch(node.type) {
      case 'doc': return <div key={index}>{children}</div>
      case 'paragraph': return <p key={index}>{children}</p>
      case 'heading': return attrs.level === 1 ? <h2 key={index}>{children}</h2> : <h3 key={index}>{children}</h3>
      case 'codeBlock': { const code = node.content?.map(x=>x.text||'').join('')||''; return <Suspense key={index} fallback={<section className="dfm-code"><pre><code>{code}</code></pre></section>}><CodeBlock text={code} language={attrs.language||'auto'}/></Suspense> }
      case 'blockquote': return <blockquote key={index}>{children}</blockquote>
      case 'bulletList': return <ul key={index}>{children}</ul>
      case 'orderedList': return <ol key={index} start={Number(attrs.start)||1}>{children}</ol>
      case 'listItem': return <li key={index}>{children}</li>
      case 'hardBreak': return <br key={index}/>
      case 'horizontalRule': return <hr key={index}/>
      case 'table': return <div className="dfm-table-scroll" key={index}><table><tbody>{children}</tbody></table></div>
      case 'tableRow': return <tr key={index}>{children}</tr>
      case 'tableCell': return <td key={index}>{children}</td>
      case 'tableHeader': return <th key={index}>{children}</th>
      case 'mention': return <span className="dfm-mention" key={index}>@{attrs.label||attrs.name||'成员'}</span>
      case 'image': case 'attachment': return requirementId && Number(attrs.attachmentId)>0 ? <Attachment key={index} id={Number(attrs.attachmentId)} requirementId={requirementId} projectId={projectId} name={attrs.name||attrs.alt||'附件'} image={node.type==='image'} active={active}/> : null
      default: return <span key={index}>{children}</span>
    }
  }
  return <div className="dfm-body-content">{render(doc)}</div>
}

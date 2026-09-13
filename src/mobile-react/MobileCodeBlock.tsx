import { useMemo, useState, type ReactNode } from 'react'
import { codeHighlighter, detectCodeLanguage, languageLabel } from '../codeHighlight'
import '../code-reading.css'

export default function MobileCodeBlock({ text, language = 'auto' }: { text: string; language?: string }) {
  const [copied, setCopied] = useState(false)
  const lang = language === 'auto' ? detectCodeLanguage(text) : language
  const nodes = useMemo(() => { try { return codeHighlighter.highlight(lang, text).children } catch { return [{type:'text',value:text}] } }, [lang,text])
  const render = (node: any, key: number): ReactNode => node.type === 'text' ? node.value : <span key={key} className={node.properties?.className?.join(' ')}>{node.children?.map(render)}</span>
  return <section className="dfm-code"><header><span>{languageLabel(lang)}</span><button onClick={() => { void navigator.clipboard.writeText(text).then(() => setCopied(true)).catch(() => setCopied(false)) }}>{copied ? '已复制' : '复制代码'}</button></header><pre><code>{nodes.map(render)}</code></pre></section>
}

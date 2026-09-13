import { useCallback, useEffect, useRef, useState } from 'react'
import { mobileBlob } from './mobileApi'

type AttachmentProps = { id: number; requirementId: number; projectId: string; name: string; image?: boolean; active?: boolean }
export function Attachment(props: AttachmentProps) {
  return <AttachmentResource key={`${props.projectId}:${props.requirementId}:${props.id}:${Boolean(props.image)}`} {...props}/>
}

function AttachmentResource({ id, requirementId, projectId, name, image = false, active = true }: AttachmentProps) {
  const figure = useRef<HTMLElement>(null)
  const resource = useRef<{ controller?: AbortController; pending?: Promise<string>; url: string; active: boolean }>({ url: '', active })
  resource.current.active=active
  const [url, setURL] = useState(''), [error, setError] = useState(''), [loading, setLoading] = useState(false), [expanded, setExpanded] = useState(false)
  const load = useCallback((): Promise<string> => {
    const current = resource.current
    if (!current.active) return Promise.resolve('')
    if (current.url) return Promise.resolve(current.url)
    if (current.pending) return current.pending
    const controller = new AbortController()
    current.controller = controller
    setLoading(true); setError('')
    const pending = mobileBlob(`/requirements/${requirementId}/attachments/${id}`, projectId, controller.signal).then(blob => {
      if (controller.signal.aborted) return ''
      current.url = URL.createObjectURL(blob)
      setURL(current.url)
      return current.url
    }).catch(cause => {
      if (!controller.signal.aborted) setError(cause instanceof Error ? cause.message : '附件暂时无法读取')
      return ''
    }).finally(() => { if (!controller.signal.aborted) { current.pending = undefined; setLoading(false) } })
    current.pending = pending
    return pending
  }, [id, requirementId, projectId])
  useEffect(() => {
    if(active)return
    const current=resource.current
    current.controller?.abort();current.pending=undefined;setLoading(false)
  }, [active])
  useEffect(() => {
    const current = resource.current
    return () => { current.controller?.abort(); current.pending = undefined; if (current.url) URL.revokeObjectURL(current.url); current.url = '' }
  }, [])
  useEffect(() => {
    if (!active || !image || !figure.current || typeof IntersectionObserver === 'undefined') return
    const observer = new IntersectionObserver(entries => {
      if (entries.some(entry => entry.isIntersecting)) { observer.disconnect(); void load() }
    }, { rootMargin: '160px' })
    observer.observe(figure.current)
    return () => observer.disconnect()
  }, [image, load, active])
  async function download() {
    const source = await load()
    if (!source) return
    const link = document.createElement('a')
    link.href = source; link.download = name
    document.body.appendChild(link); link.click(); link.remove()
  }
  return <figure ref={figure} className="dfm-asset">
    {image && (url ? <button className="dfm-image-button" onClick={() => setExpanded(true)}><img src={url} alt={name} loading="lazy"/></button> : <button className="dfm-image-placeholder" disabled={loading} onClick={() => void load()}>{loading ? '正在加载图片…' : error ? '重试加载图片' : '查看图片'}</button>)}
    <figcaption><span>{name}</span>{url ? <a href={url} download={name}>下载</a> : <button disabled={loading} onClick={() => void download()}>{loading ? '正在读取…' : error ? '重试下载' : '下载'}</button>}{error && <small role="alert">{error}</small>}</figcaption>
    {expanded && <div className="dfm-image-overlay" role="dialog" aria-modal="true" aria-label="查看截图" onClick={() => setExpanded(false)}><button aria-label="关闭截图">×</button><img src={url} alt={name}/></div>}
  </figure>
}

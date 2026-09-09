import type { JSONContent } from '@tiptap/core'
import { normalizeMentionIds, type MentionMember } from './mentions'

// 富文本文档使用受限 JSON 节点，不将任意 HTML/远程图片 URL 作为可信持久化格式。
// 这里负责编辑体验与前端净化；附件归属、内容大小与提及权限仍以 Go 服务端校验为准。
export type RichDocument = JSONContent & { type: 'doc' }
export const MAX_RICH_FILE_BYTES = 10 * 1024 * 1024
export const MAX_RICH_DOCUMENT_BYTES = 20 * 1024 * 1024
export const richEmoji = ['😀', '😄', '😂', '🥹', '😍', '🤔', '😅', '😭', '👍', '👏', '🙏', '💪', '🎉', '❤️', '✅', '🚀']
const blocks = new Set(['paragraph', 'heading', 'blockquote', 'codeBlock', 'bulletList', 'orderedList', 'listItem', 'image', 'attachment', 'horizontalRule', 'table', 'tableRow', 'tableCell', 'tableHeader'])
const allowedNodes = new Set(['doc', 'text', 'hardBreak', 'mention', ...blocks])
const allowedMarks = new Set(['bold', 'italic', 'underline', 'strike', 'code', 'link'])
const tokenBoundary = /[\s@,，。！？!?;；:：()[\]{}]/

export function safeRichLink(input: unknown): string | null {
  // 链接仅允许 http/https/mailto，拒绝脚本协议、控制字符、反斜杠及嵌入账号口令。
  if (typeof input !== 'string') return null
  const value = input.trim()
  if (!value || /[\u0000-\u0020\u007f\\]/.test(value)) return null
  try {
    const url = new URL(value)
    if (!['https:', 'http:', 'mailto:'].includes(url.protocol)) return null
    if (url.protocol === 'mailto:') return /^[^\s@]+@[^\s@]+$/.test(url.pathname) ? value : null
    return url.hostname && !url.username && !url.password ? value : null
  } catch { return null }
}

export function plainTextToDocument(body: string, ids: unknown = [], names: Record<string, string> = {}, members: MentionMember[] = []): RichDocument {
  // 只把旧编辑器明确保存过的收件人 ID 转成 mention 节点。
  // 仅出现 @姓名 的普通文字不能自行推断通知对象，同名成员也不能靠姓名合并。
  const labels = new Map<string, string[]>()
  for (const id of normalizeMentionIds(ids)) { const label = names[id] || members.find(member => member.id === id)?.name; if (label) labels.set(label, [...(labels.get(label) || []), id]) }
  const content = String(body || '').replace(/\r\n?/g, '\n').split('\n').map(line => {
    const found: { start: number; end: number; ids: string[]; label: string }[] = []
    for (const [label, ids] of labels) {
      const token = '@' + label
      let start = line.indexOf(token)
      while (start !== -1) {
        const end = start + token.length
        if ((start === 0 || !/[a-zA-Z0-9._%+-]/.test(line[start - 1]!)) && (end === line.length || tokenBoundary.test(line[end]!))) found.push({ start, end, ids, label })
        start = line.indexOf(token, end)
      }
    }
    found.sort((a, b) => a.start - b.start || b.end - a.end)
    const inline: JSONContent[] = []; let cursor = 0
    for (const match of found) {
      if (match.start < cursor) continue
      if (match.start > cursor) inline.push({ type: 'text', text: line.slice(cursor, match.start) })
      // 老数据可能明确绑定多个同名成员，转换后逐个保留可见节点，不能只留第一个 ID。
      for (const [index, id] of match.ids.entries()) {
        if (index) inline.push({ type: 'text', text: ' ' })
        inline.push({ type: 'mention', attrs: { id, label: match.label } })
      }
      cursor = match.end
    }
    if (cursor < line.length) inline.push({ type: 'text', text: line.slice(cursor) })
    return { type: 'paragraph', ...(inline.length ? { content: inline } : {}) }
  })
  return { type: 'doc', content }
}

export function richTextPlain(doc: JSONContent | null | undefined): string {
  if (!doc) return ''
  if (doc.type === 'text') return doc.text || ''
  if (doc.type === 'mention') return '@' + String(doc.attrs?.label || '')
  if (doc.type === 'hardBreak') return '\n'
  if (doc.type === 'image' || doc.type === 'attachment') return String(doc.attrs?.alt || doc.attrs?.name || '')
  const content = doc.content || [], parts: string[] = []
  for (const [index, node] of content.entries()) {
    let value = richTextPlain(node)
    const previous = parts[index - 1] || ''
    if (value && previous && (node.type === 'mention' && /[\p{L}\p{Nd}_]$/u.test(previous) || content[index - 1]?.type === 'mention' && /^[\p{L}\p{Nd}_]/u.test(value))) value = ' ' + value
    parts.push(value)
  }
  return parts.join(['paragraph', 'heading', 'codeBlock'].includes(doc.type || '') ? '' : '\n')
}

export function richMentionState(doc: JSONContent): { ids: string[]; names: Record<string, string> } {
  const ids: string[] = [], names: Record<string, string> = {}
  function visit(node: JSONContent) {
    if (node.type === 'mention' && typeof node.attrs?.id === 'string' && node.attrs.id && typeof node.attrs.label === 'string' && node.attrs.label) {
      if (!ids.includes(node.attrs.id)) ids.push(node.attrs.id)
      names[node.attrs.id] = node.attrs.label
    }
    node.content?.forEach(visit)
  }
  visit(doc); return { ids, names }
}

export function normalizeRichDocument(value: unknown, fallback = '', ids: unknown = [], names: Record<string, string> = {}, members: MentionMember[] = []): RichDocument {
  // 不认识的节点只保留可清洗的子内容；仅复制白名单属性，并限制节点数和递归深度。
  // 已有附件仅保留 attachmentId；新文件暂存 base64，不能从任意 src 自动下载。
  if (!value || typeof value !== 'object' || (value as JSONContent).type !== 'doc' || !Array.isArray((value as JSONContent).content)) return plainTextToDocument(fallback, ids, names, members)
  let count = 0
  function clean(node: JSONContent, depth = 0): JSONContent[] {
    if (!node || typeof node !== 'object' || ++count > 20000 || depth > 32) return []
    const content = Array.isArray(node.content) ? node.content.flatMap(child => clean(child, depth + 1)) : []
    if (!allowedNodes.has(node.type || '')) return content
    const result: JSONContent = { type: node.type }
    if (node.type === 'text') { if (typeof node.text !== 'string' || !node.text) return []; result.text = node.text }
    if (node.type === 'heading') result.attrs = { level: [1, 2, 3, 4, 5, 6].includes(Number(node.attrs?.level)) ? Number(node.attrs?.level) : 2 }
    if (node.type === 'orderedList') result.attrs = { start: Number.isSafeInteger(node.attrs?.start) && node.attrs!.start > 0 ? node.attrs!.start : 1 }
    if (node.type === 'codeBlock') result.attrs = { language: typeof node.attrs?.language === 'string' ? node.attrs.language.slice(0, 40) : null }
    if (['tableCell','tableHeader'].includes(node.type||'')&&['left','center','right'].includes(node.attrs?.textAlign)) result.attrs={textAlign:node.attrs!.textAlign}
    if (node.type === 'mention') {
      if (typeof node.attrs?.id !== 'string' || !node.attrs.id || typeof node.attrs.label !== 'string' || !node.attrs.label) return []
      result.attrs = { id: node.attrs.id, label: node.attrs.label }
    }
    if (node.type === 'image' || node.type === 'attachment') {
      const attachmentId = Number.isSafeInteger(node.attrs?.attachmentId) && node.attrs!.attachmentId > 0 ? node.attrs!.attachmentId : null
      const data = typeof node.attrs?.data === 'string' && /^[A-Za-z0-9+/]*={0,2}$/.test(node.attrs.data) && node.attrs.data.length % 4 === 0 ? node.attrs.data : ''
      if (!attachmentId && !data) return []
      result.attrs = { ...(typeof node.attrs?.category === 'string' && ['bug','api','design','code','other'].includes(node.attrs.category) ? { category: node.attrs.category } : {}), attachmentId, name: typeof node.attrs?.name === 'string' ? node.attrs.name : '', alt: typeof node.attrs?.alt === 'string' ? node.attrs.alt : '', ...(attachmentId ? {} : { data }) }
    }
    if (content.length) result.content = content
    if (Array.isArray(node.marks)) {
      const marks = node.marks.flatMap(mark => {
        if (!allowedMarks.has(mark.type)) return []
        if (mark.type !== 'link') return [{ type: mark.type }]
        const href = safeRichLink(mark.attrs?.href)
        return href ? [{ type: 'link', attrs: { href } }] : []
      })
      if (marks.length) result.marks = marks
    }
    return [result]
  }
  const doc = clean(value as JSONContent)[0]
  return { type: 'doc', content: doc?.content?.length ? doc.content : [{ type: 'paragraph' }] }
}

export function pendingRichBytes(doc: JSONContent | null | undefined): number {
  if (!doc) return 0
  const data = typeof doc.attrs?.data === 'string' ? doc.attrs.data : ''
  const own = data ? Math.floor(data.length * 3 / 4) - (data.endsWith('==') ? 2 : data.endsWith('=') ? 1 : 0) : 0
  return own + (doc.content || []).reduce((sum, node) => sum + pendingRichBytes(node), 0)
}

export function richImageType(bytes: Uint8Array): string | null {
  if (bytes.length >= 8 && [137, 80, 78, 71, 13, 10, 26, 10].every((byte, index) => bytes[index] === byte)) return 'image/png'
  if (bytes.length >= 3 && bytes[0] === 255 && bytes[1] === 216 && bytes[2] === 255) return 'image/jpeg'
  if (bytes.length >= 6 && ['GIF87a', 'GIF89a'].includes(String.fromCharCode(...bytes.subarray(0, 6)))) return 'image/gif'
  return null
}
export function richAssetName(name: string): string { return (name.split(/[\\/]/).at(-1) || 'attachment').replace(/[\u0000-\u001f\u007f]/g, '').slice(0, 240) || 'attachment' }
export function bytesToBase64(bytes: Uint8Array): string {
  let text = ''
  for (let offset = 0; offset < bytes.length; offset += 32768) text += String.fromCharCode(...bytes.subarray(offset, offset + 32768))
  return btoa(text)
}
export function base64ToBytes(data: string): Uint8Array { const text = atob(data); return Uint8Array.from(text, character => character.charCodeAt(0)) }

export async function readRichFiles(files: File[], existingBytes = 0, imagesOnly = false): Promise<JSONContent[]> {
  // 先核大小预算，再读文件字节；图片类型按文件头判断，不能信任扩展名/MIME 声明。
  // SVG 明确拒绝；此处返回待保存节点，不代表附件已上传或已有跨需求访问权限。
  if (files.some(file => file.size > MAX_RICH_FILE_BYTES)) throw new Error('单个文件不能超过 10 MiB')
  if (files.some(file => !file.size)) throw new Error('不能插入空文件')
  if (existingBytes + files.reduce((sum, file) => sum + file.size, 0) > MAX_RICH_DOCUMENT_BYTES) throw new Error('单次编辑中的待保存文件合计不能超过 20 MiB')
  const result: JSONContent[] = []
  for (const file of files) {
    if (file.type === 'image/svg+xml' || /\.svgz?$/i.test(file.name)) throw new Error('图片仅支持 PNG、JPEG 或 GIF，不支持 SVG')
    const bytes = new Uint8Array(await file.arrayBuffer()), imageType = richImageType(bytes)
    if (imagesOnly && !imageType) throw new Error('图片仅支持 PNG、JPEG 或 GIF，不支持 SVG')
    result.push({ type: imageType ? 'image' : 'attachment', attrs: { attachmentId: null, name: richAssetName(file.name), alt: '', data: bytesToBase64(bytes) } })
  }
  return result
}

/**
 * 粘贴 HTML 先进入惰性 template 再净化，之后才交给 ProseMirror schema 解析。
 * 删除活动内容/远程媒体，剥离所有属性后仅重建安全链接与有序列表起始值。
 * 不可改成直接将原 HTML 赋给可见容器，也不可绕过服务端文档验证。
 */
export function sanitizeRichHTML(html: string, dom: Document = document): string {
  const template = dom.createElement('template'); template.innerHTML = html
  const dangerous = new Set(['SCRIPT', 'STYLE', 'IFRAME', 'OBJECT', 'EMBED', 'SVG', 'MATH', 'IMG', 'VIDEO', 'AUDIO', 'SOURCE', 'LINK', 'META', 'FORM', 'INPUT', 'BUTTON', 'TEXTAREA'])
  const allowed = new Set(['P', 'DIV', 'H1', 'H2', 'H3', 'STRONG', 'B', 'EM', 'I', 'U', 'S', 'STRIKE', 'DEL', 'UL', 'OL', 'LI', 'BLOCKQUOTE', 'PRE', 'CODE', 'BR', 'HR', 'A', 'SPAN', 'TABLE', 'THEAD', 'TBODY', 'TR', 'TH', 'TD'])
  for (const element of Array.from(template.content.querySelectorAll('*'))) {
    if (dangerous.has(element.tagName)) { element.remove(); continue }
    if (!allowed.has(element.tagName)) { element.replaceWith(...Array.from(element.childNodes)); continue }
    const href = element.tagName === 'A' ? safeRichLink(element.getAttribute('href')) : null
    const start = element.tagName === 'OL' ? element.getAttribute('start') : null
    const language = element.tagName === 'CODE' ? element.className.match(/(?:^|\s)language-([\w+#.\-]{1,40})(?:\s|$)/)?.[1] : null
    for (const attribute of Array.from(element.attributes)) element.removeAttribute(attribute.name)
    if (href) element.setAttribute('href', href)
    if (start && /^\d+$/.test(start) && Number(start) > 0) element.setAttribute('start', start)
    if (language) element.setAttribute('class','language-'+language)
  }
  return template.innerHTML
}

export type RichAssetContext = { requirementId: number | undefined; readonly: boolean; disabled: boolean }
export const richAssetContextKey = 'devflow-rich-asset-context'

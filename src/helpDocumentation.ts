// 帮助页只读取随代码包发布的文档，不读取业务数据，也不执行文档中的请求示例。
export type HelpDocumentSource = { id: string; title: string; description: string; filename: string; markdown: string }
export type HelpSection = { id: string; title: string; markdown: string; headings: { id: string; title: string }[] }
export type HelpDocument = HelpDocumentSource & { sections: HelpSection[] }

export const documentRoutes: Record<string, string> = {
  'product-handbook.md': '/help/guide', 'api-reference.md': '/help/api',
  'internal-api-reference.md': '/help/internal-api', 'ai-collaboration.md': '/help/ai',
  'ai-release-notes.md': '/help/release-notes',
  'maintenance-zh.md': '/help/operations', 'openapi.json': '/help/api',
}

export function headingSlug(text: string) {
  return text.replace(/[`*_]/g, '').toLowerCase().replace(/[^\p{L}\p{N}]+/gu, '-').replace(/^-|-$/g, '') || 'section'
}

export function parseHelpDocument(source: HelpDocumentSource): HelpDocument {
  const sections: HelpSection[] = [{ id: 'overview', title: '文档概览', markdown: '', headings: [] }]
  const used = new Set<string>(['overview'])
  let fence = '', section = sections[0]
  for (const line of source.markdown.replace(/\r\n?/g, '\n').split('\n')) {
    const marker = line.match(/^\s*(`{3,}|~{3,})([\w-]*)\s*$/)
    if (fence) {
      if (new RegExp('^\\s*' + fence[0] + '{' + fence.length + ',}\\s*$').test(line)) fence = ''
      section.markdown += line + '\n'; continue
    }
    if (marker) {
      fence = marker[1]
      section.markdown += line + '\n'; continue
    }
    const heading = !fence && line.match(/^(#{1,6})\s+(.+?)\s*#*$/)
    if (heading && heading[1].length === 1) continue
    if (heading) {
      const base = headingSlug(heading[2])
      let id = base, count = 0
      // 最终 ID 也必须去重：标题本身可能恰好叫“说明-1”，不能与重复标题的后缀冲突。
      while (used.has(id)) id = `${base}-${++count}`
      used.add(id)
      if (heading[1].length === 2) {
        section = { id, title: heading[2].replace(/[`*_]/g, ''), markdown: '', headings: [] }
        sections.push(section)
      }
      section.headings.push({ id, title: heading[2] })
    }
    section.markdown += line + '\n'
  }
  return { ...source, sections: sections.filter(section => section.markdown.trim()) }
}

const escapeHTML = (text: string) => text.replace(/[&<>"']/g, char => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[char]!))

// 只允许显式的 HTTP(S)、本站页面和已纳入帮助中心的文档；禁止脚本、协议相对地址及本地文件。
export function safeDocumentationLink(value: string, origin = typeof window === 'undefined' ? '' : window.location.origin): string | null {
  const href = value.trim()
  if (!href || /[\u0000-\u0020\u007f\\]/.test(href) || /^(?:\/\/|\/api(?:\/|$))/.test(href)) return null
  if (href.startsWith('#')) return href
  if (/^https?:\/\//i.test(href)) {
    try {
      const parsed = new URL(href)
      if (parsed.username || parsed.password) return null
      // 绝对同站地址也要检查；URL 会先规范化 ../ 和编码的点路径，不能绕过 API 禁止规则。
      if (origin && parsed.origin === new URL(origin).origin && /^\/api(?:\/|$)/.test(decodeURIComponent(parsed.pathname))) return null
      return href
    } catch { return null }
  }
  if (href.startsWith('/')) {
    try {
      const decoded = decodeURIComponent(href.split(/[?#]/)[0])
      if (/[\\\u0000-\u0020\u007f]/.test(decoded) || decoded.split('/').some(part => part === '.' || part === '..')) return null
    } catch { return null }
    return /^\/(?:help|projects|requirements|iterations|defects|tests|notifications|my-work|search|profile|settings|organization|reports|roadmap|dashboard|audit)(?:[/?#]|$)/.test(href) ? href : null
  }
  const [path, hash] = href.split('#'), target = documentRoutes[path.replace(/^\.\//, '')]
  return target ? target + (hash ? '#' + hash : '') : null
}

function inlineMarkdown(text: string, depth = 0, openAPIURL = ''): string {
  if (depth > 4) return escapeHTML(text)
  const pattern = /(`+)([^`]*?)\1|\[([^\]]+)\]\(([^\s)]+)\)|\*\*([^*]+)\*\*|\*([^*]+)\*/g
  let html = '', start = 0
  for (const match of text.matchAll(pattern)) {
    html += escapeHTML(text.slice(start, match.index))
    if (match[1]) html += '<code>' + escapeHTML(match[2]) + '</code>'
    else if (match[3]) {
      // 下载地址只来自构建器注入的固定资源，不允许文档自行声明可执行地址。
      const download = match[4] === 'openapi.json' && /^(?:\/assets\/[\w.-]+\.json|\/docs\/openapi\.json)$/.test(openAPIURL)
      const target = download ? openAPIURL : safeDocumentationLink(match[4]), label = inlineMarkdown(match[3], depth + 1, openAPIURL)
      html += target ? `<a href="${escapeHTML(target)}"${download ? ' download="devflow-openapi.json"' : /^https?:/i.test(target) ? ' target="_blank" rel="noopener noreferrer"' : ''}>${label}</a>` : label
    } else if (match[5]) html += '<strong>' + inlineMarkdown(match[5], depth + 1, openAPIURL) + '</strong>'
    else html += '<em>' + escapeHTML(match[6]) + '</em>'
    start = (match.index || 0) + match[0].length
  }
  return html + escapeHTML(text.slice(start))
}

function tableCells(line: string) {
  // 表格内代码常含枚举竖线；不能把代码里的竖线拆成额外的列。
  const cells: string[] = []; let cell = '', fence = '', escaped = false
  for (let i = 0; i < line.length; i++) {
    const char = line[i]
    if (escaped) { cell += char; escaped = false; continue }
    if (char === '\\') { escaped = true; continue }
    if (char === '`') { const marker = line.slice(i).match(/^`+/)![0]; fence = fence === marker ? '' : fence || marker; cell += marker; i += marker.length - 1; continue }
    if (char === '|' && !fence) { cells.push(cell.trim()); cell = '' } else cell += char
  }
  cells.push(cell.trim())
  if (line.trim().startsWith('|')) cells.shift()
  if (line.trim().endsWith('|')) cells.pop()
  return cells
}

export function renderHelpSection(section: HelpSection, openAPIURL = ''): string {
  const lines = section.markdown.split('\n'), result: string[] = []
  const inline = (text: string) => inlineMarkdown(text, 0, openAPIURL)
  let i = 0, headingIndex = 0
  while (i < lines.length) {
    const line = lines[i]
    if (!line.trim()) { i++; continue }
    const fence = line.match(/^\s*(`{3,}|~{3,})([\w-]*)\s*$/)
    if (fence) {
      const content: string[] = []; i++
      while (i < lines.length && !new RegExp('^\\s*' + fence[1][0] + '{' + fence[1].length + ',}\\s*$').test(lines[i])) content.push(lines[i++])
      if (i < lines.length) i++
      result.push(`<div class="help-code"><span>${escapeHTML(fence[2] || '示例')}</span><pre tabindex="0" aria-label="代码示例"><code>${escapeHTML(content.join('\n'))}</code></pre></div>`)
      continue
    }
    const heading = line.match(/^(#{1,6})\s+(.+?)\s*#*$/)
    if (heading) {
      const level = Math.max(2, heading[1].length), id = section.headings[headingIndex++]?.id || headingSlug(heading[2])
      result.push(`<h${level} id="${escapeHTML(id)}">${inline(heading[2])}</h${level}>`); i++; continue
    }
    if (/^\s*(?:---+|\*\*\*+)\s*$/.test(line)) { result.push('<hr>'); i++; continue }
    if (i + 1 < lines.length && line.includes('|') && /^\s*\|?\s*:?-{3,}:?\s*(\|\s*:?-{3,}:?\s*)+\|?\s*$/.test(lines[i + 1])) {
      const headers = tableCells(line), rows: string[][] = []; i += 2
      while (i < lines.length && lines[i].includes('|') && lines[i].trim()) rows.push(tableCells(lines[i++]))
      result.push('<div class="help-table-scroll" tabindex="0" role="region" aria-label="文档表格，可横向滚动"><table><thead><tr>' + headers.map(cell => '<th scope="col">' + inline(cell) + '</th>').join('') + '</tr></thead><tbody>' + rows.map(row => '<tr>' + headers.map((_, index) => '<td>' + inline(row[index] || '') + '</td>').join('') + '</tr>').join('') + '</tbody></table></div>')
      continue
    }
    if (/^\s*>/.test(line)) {
      const content: string[] = []
      while (i < lines.length && /^\s*>/.test(lines[i])) content.push(lines[i++].replace(/^\s*>\s?/, ''))
      result.push('<blockquote>' + content.map(inline).join('<br>') + '</blockquote>'); continue
    }
    const list = line.match(/^\s*(?:([-+*])|(\d+)\.)\s+(.+)$/)
    if (list) {
      const ordered = !!list[2], items: string[] = []
      while (i < lines.length) {
        const next = lines[i].match(/^\s*(?:([-+*])|(\d+)\.)\s+(.+)$/)
        if (!next || !!next[2] !== ordered) break
        items.push('<li>' + inline(next[3]) + '</li>'); i++
      }
      const tag = ordered ? 'ol' : 'ul'
      result.push(`<${tag}${ordered ? ` start="${Number(list[2])}"` : ''}>${items.join('')}</${tag}>`); continue
    }
    const paragraph = [line]; i++
    while (i < lines.length && lines[i].trim() && !/^\s*(?:#{1,6}\s|`{3,}|~{3,}|>|[-+*]\s|\d+\.\s)/.test(lines[i]) && !(i + 1 < lines.length && /^\s*\|?\s*:?-{3,}/.test(lines[i + 1]))) paragraph.push(lines[i++])
    result.push('<p>' + inline(paragraph.join('\n')) + '</p>')
  }
  return result.join('\n')
}

export function searchHelpDocuments(documents: HelpDocument[], query: string) {
  const terms = query.trim().toLocaleLowerCase().split(/\s+/).filter(Boolean)
  if (!terms.length) return []
  return documents.flatMap(document => document.sections.flatMap(section => {
    const text = section.markdown.replace(/[#`*|]/g, ' ').replace(/\s+/g, ' '), normalized = (document.title + ' ' + section.title + ' ' + text).toLocaleLowerCase()
    if (!terms.every(term => normalized.includes(term))) return []
    const index = text.toLocaleLowerCase().indexOf(terms[0]), start = Math.max(0, index - 35)
    return [{ documentId: document.id, documentTitle: document.title, sectionId: section.id, title: section.title, excerpt: (start ? '…' : '') + text.slice(start, start + 160) + (text.length > start + 160 ? '…' : '') }]
  }))
}

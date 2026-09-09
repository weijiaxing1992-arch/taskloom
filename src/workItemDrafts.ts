/**
 * 工作项草稿的纯数据层。
 *
 * 这里刻意不接触 localStorage、IndexedDB 或网络：调用方先用本模块把 Vue
 * 响应式表单转换成受限的 JSON，再决定写到浏览器或私有草稿 API。这样草稿内容
 * 不会带入函数、原型链或凭据，也方便在服务端和浏览器之间做一致的大小校验。
 */
export const WORK_ITEM_DRAFT_SCHEMA = 1
export const MAX_WORK_ITEM_DRAFT_BYTES = 30 * 1024 * 1024
export const MAX_WORK_ITEM_DRAFTS_PER_SCOPE = 100

export type WorkItemDraftKind = 'requirement' | 'defect'
export type DraftPrimitive = string | number | boolean | null
export type DraftJSON = DraftPrimitive | DraftJSON[] | { [key: string]: DraftJSON }
export type DraftObject = { [key: string]: DraftJSON }

const blockedKeys = new Set(['__proto__', 'constructor', 'prototype'])

function fail(path: string, message: string): never {
  throw new Error(path ? `草稿${path}${message}` : `草稿${message}`)
}

/** 把任意表单数据复制成没有访问器、函数或危险原型键的 JSON 数据。 */
export function cloneDraftJSON(value: unknown, path = '', depth = 0): DraftJSON {
  if (depth > 32) fail(path, '层级超过 32 层，无法保存')
  if (value === null || typeof value === 'string' || typeof value === 'boolean') return value
  if (typeof value === 'number') {
    if (!Number.isFinite(value)) fail(path, '包含无效数字')
    return value
  }
  if (Array.isArray(value)) return value.map((item, index) => cloneDraftJSON(item, `${path}[${index}]`, depth + 1))
  if (typeof value !== 'object') fail(path, '包含不支持的数据类型')
  const prototype = Object.getPrototypeOf(value)
  if (prototype !== Object.prototype && prototype !== null) fail(path, '包含非普通对象')
  const output: DraftObject = Object.create(null) as DraftObject
  for (const key of Object.keys(value as Record<string, unknown>)) {
    if (blockedKeys.has(key)) fail(path, '包含受保护字段')
    output[key] = cloneDraftJSON((value as Record<string, unknown>)[key], path ? `${path}.${key}` : `.${key}`, depth + 1)
  }
  return output
}

export function cloneDraftObject(value: unknown): DraftObject {
  const cloned = cloneDraftJSON(value)
  if (!cloned || Array.isArray(cloned) || typeof cloned !== 'object') throw new Error('草稿内容必须是对象')
  return cloned as DraftObject
}

export function draftBytes(value: unknown): number {
  const json = JSON.stringify(cloneDraftJSON(value))
  return new TextEncoder().encode(json).byteLength
}

/**
 * 在本地写入和远端上传前做同一套大小限制。请求总量由调用方加上 context 后再校验。
 */
export function validatedDraftObject(value: unknown, label = '内容'): DraftObject {
  const cloned = cloneDraftObject(value)
  if (draftBytes(cloned) > MAX_WORK_ITEM_DRAFT_BYTES) throw new Error(`${label}超过 30 MB，无法保存到草稿箱`)
  return cloned
}

/** 作用域必须来自已验证的会话，不能信任仅存于 localStorage 的项目选择。 */
export function workItemDraftScope(tenantId: string, userId: string, projectId: string): string {
  if (!tenantId || !userId || !projectId) throw new Error('当前会话尚未验证，不能保存草稿')
  return `draft-v${WORK_ITEM_DRAFT_SCHEMA}:${encodeURIComponent(tenantId)}:${encodeURIComponent(userId)}:${encodeURIComponent(projectId)}`
}

export function newWorkItemDraftId(): string {
  const cryptoApi = typeof crypto === 'undefined' ? undefined : crypto
  if (cryptoApi?.randomUUID) return cryptoApi.randomUUID()
  const bytes = new Uint8Array(16)
  if (cryptoApi?.getRandomValues) cryptoApi.getRandomValues(bytes)
  else for (let index = 0; index < bytes.length; index++) bytes[index] = Math.floor(Math.random() * 256)
  bytes[6] = (bytes[6] & 0x0f) | 0x40
  bytes[8] = (bytes[8] & 0x3f) | 0x80
  const hex = Array.from(bytes, byte => byte.toString(16).padStart(2, '0')).join('')
  return `${hex.slice(0, 8)}-${hex.slice(8, 12)}-${hex.slice(12, 16)}-${hex.slice(16, 20)}-${hex.slice(20)}`
}

export function draftTitle(payload: unknown, fallback = '未命名草稿'): string {
  if (!payload || typeof payload !== 'object' || Array.isArray(payload)) return fallback
  const value = (payload as Record<string, unknown>).title
  if (typeof value !== 'string') return fallback
  const title = value.replace(/[\u0000-\u001f\u007f]/g, ' ').trim().replace(/\s+/g, ' ')
  return Array.from(title).slice(0, 120).join('') || fallback
}

function collectText(value: unknown, output: string[], depth = 0) {
  if (depth > 16 || output.join('').length > 12000 || value === null || value === undefined) return
  if (typeof value === 'string') { output.push(value); return }
  if (typeof value === 'number' || typeof value === 'boolean') { output.push(String(value)); return }
  if (Array.isArray(value)) { for (const item of value) collectText(item, output, depth + 1); return }
  if (typeof value !== 'object') return
  const object = value as Record<string, unknown>
  // 富文本只读取用户可见的 text 字段，绝不把 base64 图片/附件正文放进预览。
  if (typeof object.text === 'string') output.push(object.text)
  if (Array.isArray(object.content)) for (const item of object.content) collectText(item, output, depth + 1)
}

export function draftPreview(payload: unknown, maximum = 700): string {
  const object = payload && typeof payload === 'object' && !Array.isArray(payload) ? payload as Record<string, unknown> : {}
  const parts: string[] = []
  for (const key of ['description', 'steps', 'actual', 'expected', 'acceptance', 'remarks']) {
    if (typeof object[key] === 'string') parts.push(object[key] as string)
  }
  if (parts.length === 0 && object.descriptionDoc) collectText(object.descriptionDoc, parts)
  const result = parts.join('\n').replace(/\s+/g, ' ').trim()
  return Array.from(result).slice(0, maximum).join('')
}

function collectAttachments(value: unknown, output: string[], depth = 0) {
  if (depth > 20 || !value || typeof value !== 'object') return
  if (Array.isArray(value)) { for (const item of value) collectAttachments(item, output, depth + 1); return }
  const object = value as Record<string, unknown>
  if ((object.type === 'image' || object.type === 'attachment') && object.attrs && typeof object.attrs === 'object') {
    const name = (object.attrs as Record<string, unknown>).name
    if (typeof name === 'string' && name.trim()) output.push(name.trim())
  }
  if (Array.isArray(object.content)) for (const item of object.content) collectAttachments(item, output, depth + 1)
}

export function escapeDraftHTML(value: string): string {
  return value.replace(/[&<>'"]/g, character => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', "'": '&#39;', '"': '&quot;' }[character] || character))
}

/** 生成可直接双击查看的离线 HTML；所有用户文本均转义，附件内容不内嵌。 */
export function draftReadableHTML(options: { kind: WorkItemDraftKind; payload: unknown; updatedAt: string; targetId?: string }): string {
  const title = draftTitle(options.payload)
  const preview = draftPreview(options.payload, 12000) || '（草稿尚未填写正文）'
  const files: string[] = []
  const object = options.payload && typeof options.payload === 'object' && !Array.isArray(options.payload) ? options.payload as Record<string, unknown> : {}
  collectAttachments(object.descriptionDoc, files)
  const uniqueFiles = [...new Set(files)].slice(0, 100)
  const kind = options.kind === 'requirement' ? '需求' : '缺陷'
  const detail = options.targetId ? `编辑对象 #${options.targetId}` : '新建对象草稿'
  const fileList = uniqueFiles.length ? `<h2>待上传附件</h2><ul>${uniqueFiles.map(file => `<li>${escapeDraftHTML(file)}</li>`).join('')}</ul><p class="hint">附件的二进制内容保留在 JSON 导出中；HTML 只显示文件名，避免离线预览意外执行或暴露数据。</p>` : ''
  return `<!doctype html><html lang="zh-CN"><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>${escapeDraftHTML(title)} - TaskLoom 草稿</title><style>body{max-width:860px;margin:40px auto;padding:0 24px;color:#1f2937;font:15px/1.7 -apple-system,BlinkMacSystemFont,"Segoe UI",sans-serif}h1{font-size:28px;line-height:1.3;margin:0 0 8px}.meta,.hint{color:#64748b;font-size:13px}pre{white-space:pre-wrap;overflow-wrap:anywhere;background:#f8fafc;border:1px solid #e2e8f0;border-radius:10px;padding:18px}li{overflow-wrap:anywhere}</style><body><p class="meta">TaskLoom 私有草稿 · ${escapeDraftHTML(kind)} · ${escapeDraftHTML(detail)}</p><h1>${escapeDraftHTML(title)}</h1><p class="meta">最后保存：${escapeDraftHTML(options.updatedAt || '本机时间未知')}</p><h2>正文预览</h2><pre>${escapeDraftHTML(preview)}</pre>${fileList}</body></html>`
}

export function draftExportFilename(kind: WorkItemDraftKind, extension: 'json' | 'html') {
  const day = new Date().toISOString().slice(0, 10)
  return `devflow-${kind}-draft-${day}.${extension}`
}

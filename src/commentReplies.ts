/** Reply references are one hop only. Rendering never walks an ancestor chain. */
export type CommentReplyTarget = { id: number; author?: string; body?: string; unavailable?: boolean }
export function validCommentId(value: unknown): value is number { return typeof value === 'number' && Number.isSafeInteger(value) && value > 0 }
export function commentExcerpt(value: unknown, limit = 160): string {
  if (typeof value !== 'string') return ''
  const text = value.replace(/\s+/g, ' ').trim(), characters = Array.from(text)
  const length = Math.max(1, Math.min(500, Number.isSafeInteger(limit) ? limit : 160))
  return characters.length > length ? characters.slice(0, length).join('') + '…' : text
}

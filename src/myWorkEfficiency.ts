/** 展示层关注顺序只采用服务端分类，不推断审批人、阻塞关系或执行权限。 */
export type PersonalWorkItem = {
  id: number; projectId: string; type: string; category?: string; priority?: string
  dueDate?: string; updatedAt?: string
}
const attentionRanks: Record<string, number> = { overdue: 0, due: 1, todo: 2, doing: 3, completed: 5, cancelled: 6 }
export function personalWorkKey(item: Pick<PersonalWorkItem, 'id' | 'projectId' | 'type'>): string {
  return JSON.stringify([item.projectId, item.type, item.id])
}
export function compareWorkAttention(left: PersonalWorkItem, right: PersonalWorkItem): number {
  const category = (attentionRanks[left.category || ''] ?? 4) - (attentionRanks[right.category || ''] ?? 4)
  if (category) return category
  const priority = (value: string | undefined) => /^P[0-3]$/.test(value || '') ? Number(value!.slice(1)) : 4
  const urgency = priority(left.priority) - priority(right.priority)
  if (urgency) return urgency
  // 没有截止日期不是“今天截止”，始终放在同组有日期的记录之后。
  const due = (value: string | undefined) => /^\d{4}-\d{2}-\d{2}$/.test(value || '') ? value! : '9999-99-99'
  return due(left.dueDate).localeCompare(due(right.dueDate))
    || String(right.updatedAt || '').localeCompare(String(left.updatedAt || ''))
    || personalWorkKey(left).localeCompare(personalWorkKey(right))
}

export type MyWorkPreferences = {
  category: string; q: string; type: string; project: string; view: 'assigned' | 'favorites'
  status: string; sprintId: string; sort: string; order: 'asc' | 'desc'; filtersExpanded: boolean
  assignedCategory: string; selectedKey: string; scrollTop: number
}
type WorkStorage = Pick<Storage, 'getItem' | 'setItem' | 'removeItem'> | undefined
const maxAge = 8 * 60 * 60 * 1000
const keyFor = (scope: string) => scope ? `devflow-my-work:v1:${scope}` : ''
export function readMyWorkPreferences(storage: WorkStorage, scope: string, now = Date.now()): MyWorkPreferences | null {
  const key = keyFor(scope)
  if (!key) return null
  try {
    const value = JSON.parse(storage?.getItem(key) || 'null')
    if (!value || typeof value.savedAt !== 'number' || now < value.savedAt || now - value.savedAt > maxAge) return null
    const p = value.preferences
    if (!p || !['assigned', 'favorites'].includes(p.view) || !['asc', 'desc'].includes(p.order)
      || !['', 'active', 'todo', 'doing', 'due', 'overdue', 'completed', 'cancelled'].includes(p.category)
      || !['', 'active', 'todo', 'doing', 'due', 'overdue', 'completed', 'cancelled'].includes(p.assignedCategory)
      || !['attention', 'updatedAt', 'title', 'priority', 'code', 'dueDate', 'favoritedAt'].includes(p.sort)
      || !['', '需求', '缺陷', '迭代', '测试用例', '测试执行'].includes(p.type)
      || typeof p.filtersExpanded !== 'boolean' || !Number.isFinite(p.scrollTop) || p.scrollTop < 0 || p.scrollTop > 10_000_000
      || ['q', 'project', 'status', 'sprintId', 'selectedKey'].some(field => typeof p[field] !== 'string' || p[field].length > 500)
      || (p.sprintId && !/^[1-9]\d*$/.test(p.sprintId))) return null
    return { category: p.category, q: p.q, type: p.type, project: p.project, view: p.view,
      status: p.status, sprintId: p.sprintId, sort: p.view === 'favorites' && p.sort === 'attention' ? 'updatedAt' : p.sort,
      order: p.order, filtersExpanded: p.filtersExpanded, assignedCategory: p.assignedCategory,
      selectedKey: p.selectedKey, scrollTop: p.scrollTop }
  } catch { return null }
}
export function saveMyWorkPreferences(storage: WorkStorage, scope: string, preferences: MyWorkPreferences, now = Date.now()): void {
  if (!scope) return
  try { storage?.setItem(keyFor(scope), JSON.stringify({ savedAt: now, preferences })) } catch { /* 隐私模式不影响查看工作。 */ }
}
export function clearMyWorkPreferences(storage: WorkStorage, scope: string): void {
  if (!scope) return
  try { storage?.removeItem(keyFor(scope)) } catch { /* 账号隔离始终由作用域保证。 */ }
}

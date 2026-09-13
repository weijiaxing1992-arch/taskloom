export type MobileWorkItem = {
  id: number | string
  projectId: string
  type: string
  code?: string
  title?: string
  status?: string
  priority?: string
  projectName?: string
  sprint?: string
  role?: string
  dueDate?: string
  updatedAt?: string
  url?: string
}

/** 移动端默认先呈现需求，再呈现缺陷；同一类中保留最近更新优先。 */
export function workTypeRank(type: unknown): number {
  if (type === '需求') return 0
  if (type === '缺陷') return 1
  return 2
}

export function sortMobileWorkItems(items: MobileWorkItem[]): MobileWorkItem[] {
  return [...items].sort((left, right) => {
    const byType = workTypeRank(left.type) - workTypeRank(right.type)
    if (byType) return byType
    const leftUpdated = Date.parse(left.updatedAt || '') || 0
    const rightUpdated = Date.parse(right.updatedAt || '') || 0
    if (leftUpdated !== rightUpdated) return rightUpdated - leftUpdated
    return String(left.code || left.id).localeCompare(String(right.code || right.id), 'zh-CN')
  })
}

export type MobileTimeRange = 'all' | 'today' | 'week'

export function inMobileTimeRange(item: MobileWorkItem, range: MobileTimeRange, today = new Date()): boolean {
  if (range === 'all') return true
  const due = item.dueDate ? new Date(item.dueDate) : null
  if (!due || Number.isNaN(due.getTime())) return false
  const start = new Date(today.getFullYear(), today.getMonth(), today.getDate())
  const end = new Date(start)
  end.setDate(start.getDate() + (range === 'today' ? 1 : 7))
  return due >= start && due < end
}

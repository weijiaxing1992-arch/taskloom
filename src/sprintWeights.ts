export const sprintWeightRoles = [
  { key: 'frontend', label: '前端', color: '#6265e8' },
  { key: 'backend', label: '后端', color: '#31a1b0' },
  { key: 'algorithm', label: '算法', color: '#ac70d8' },
  { key: 'ui', label: 'UI 设计', color: '#df9660' },
  { key: 'product', label: '产品', color: '#6aab80' },
] as const

export type SprintWeightRole = typeof sprintWeightRoles[number]['key']
export interface SprintWeightItem {
  id: number
  code: string
  title: string
  status: string
  parentId: number | null
  weights: Record<SprintWeightRole, number | null>
  totalWeight: number
  estimated: boolean
}
export interface SprintWeightSummary {
  totalWeight: number
  requirementCount: number
  estimatedCount: number
  unestimatedCount: number
  roleTotals: Record<SprintWeightRole, number>
  roleEstimatedCounts: Record<SprintWeightRole, number>
  items: SprintWeightItem[]
  precision: number
}

export function sprintWeightCoverage(summary: SprintWeightSummary): number {
  return summary.requirementCount ? Math.round(summary.estimatedCount / summary.requirementCount * 100) : 0
}

// Charts are display-only. Server totals are authoritative and are never
// recomputed from the visible/paginated requirement rows or estimated hours.
export function sprintWeightBreakdown(summary: SprintWeightSummary) {
  return sprintWeightRoles.map(role => ({
    ...role,
    total: summary.roleTotals[role.key] || 0,
    estimatedCount: summary.roleEstimatedCounts[role.key] || 0,
    percent: summary.totalWeight > 0 ? Math.min(100, Math.max(0, (summary.roleTotals[role.key] || 0) / summary.totalWeight * 100)) : 0,
  }))
}

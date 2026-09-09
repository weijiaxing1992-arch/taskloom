export type RoleWeightKey = 'frontend' | 'backend' | 'algorithm' | 'ui' | 'product'
export type RoleWeight = { userId: string; userIds?: string[]; value: number | null }
export type RoleWeights = Record<RoleWeightKey, RoleWeight>
export type RequirementMember = { id: string; name: string; email?: string; department?: string; projectRole?: string; role?: string; active?: boolean; isCurrent?: boolean }
export type RequirementTagOption = { name: string; color?: string; count?: number }
export type RequirementSprint = { id: number; name: string; status: string }

export const roleWeightDefinitions: { key: RoleWeightKey; label: string; memberLabel: string; roles: string[] }[] = [
  { key: 'frontend', label: '前端开发难度', memberLabel: '前端工程师', roles: ['frontend', 'frontend_lead'] },
  { key: 'backend', label: '后端难度', memberLabel: '后端工程师', roles: ['backend', 'backend_lead'] },
  { key: 'algorithm', label: '算法难度', memberLabel: '算法工程师', roles: ['algorithm'] },
  { key: 'ui', label: 'UI 难度', memberLabel: 'UI 工程师', roles: ['ui'] },
  { key: 'product', label: '产品难度', memberLabel: '产品负责人', roles: ['product'] },
]
export const tagPalette = ['#5B5CE2', '#2563EB', '#0891B2', '#059669', '#D97706', '#EA580C', '#DC2626', '#DB2777', '#7C3AED', '#64748B']
export const weightQuickValues = [20, 50, 100, 200, 300, 400, 500, 800, 1000, 2000] as const
export function emptyRoleWeights(): RoleWeights {
  const result = {} as RoleWeights
  for (const {key} of roleWeightDefinitions) result[key] = { userId:'', userIds:[], value:null }
  return result
}
export function normalizeRoleWeights(value: Partial<RoleWeights> | null | undefined): RoleWeights {
  const result = emptyRoleWeights()
  for (const { key } of roleWeightDefinitions) {
    const current = value?.[key]
    const userIds = Array.isArray(current?.userIds) ? [...new Set(current.userIds.filter(id => typeof id === 'string' && !!id.trim()))] : typeof current?.userId === 'string' && current.userId ? [current.userId] : []
    result[key] = { userId: userIds[0] || '', userIds, value: typeof current?.value === 'number' ? current.value : null }
  }
  return result
}
export function weightTotal(value: Partial<RoleWeights> | null | undefined): number {
  const sum = roleWeightDefinitions.reduce((total, { key }) => {
    const amount = value?.[key]?.value
    return total + (typeof amount === 'number' && Number.isFinite(amount) && amount >= 0 ? amount : 0)
  }, 0)
  return Number(sum.toFixed(6))
}
export function splitTags(value: string | undefined | null): string[] {
  return [...new Set((value || '').split(/[,，\n]/).map(tag => tag.trim()).filter(Boolean))]
}
export function validTagColor(value: string | undefined): string {
  return value && /^#[0-9a-f]{6}$/i.test(value) ? value.toUpperCase() : tagPalette[0]!
}
export function tagStyle(color: string | undefined): Record<string, string> {
  const normalized = validTagColor(color)
  const rgb = [1, 3, 5].map(offset => parseInt(normalized.slice(offset, offset + 2), 16))
  const luminance = (values: number[]) => values.map(value => { const srgb = value / 255; return srgb <= .04045 ? srgb / 12.92 : ((srgb + .055) / 1.055) ** 2.4 }).reduce((sum, value, index) => sum + value * [0.2126, 0.7152, 0.0722][index]!, 0)
  // The stored color and translucent fill stay unchanged. Only presentation
  // foregrounds adapt; CSS chooses one without coupling this pure helper to Vue.
  // Include raised/selected surfaces, not just the default page background.
  const backgroundLuminance = (base: string) => luminance(rgb.map((value, index) => value * (20 / 255) + parseInt(base.slice(1 + index * 2, 3 + index * 2), 16) * (235 / 255)))
  const lightBackground = Math.min(...['#FFFFFF', '#F7F9FC', '#F0EFFF', '#EEEDFF', '#E8F7F1'].map(backgroundLuminance))
  const darkBackground = Math.max(...['#182132', '#1B2638', '#202B3E', '#2C294B', '#302B4C', '#25324D', '#3D3425', '#1D3B37', '#422C38'].map(backgroundLuminance))
  const readable = (dark: boolean) => {
    let foreground = [...rgb]
    const contrast = () => dark ? (luminance(foreground) + .05) / (darkBackground + .05) : (lightBackground + .05) / (luminance(foreground) + .05)
    while (contrast() < 4.5) foreground = foreground.map(value => dark ? Math.ceil(value + (255 - value) * .1) : Math.floor(value * .9))
    return '#' + foreground.map(value => value.toString(16).padStart(2, '0')).join('').toUpperCase()
  }
  return { color: 'var(--tag-foreground, var(--tag-light-fg))', '--tag-light-fg': readable(false), '--tag-dark-fg': readable(true), backgroundColor: normalized + '14', borderColor: normalized + '40' }
}
export function formatCreatedAt(value: string | undefined | null): string {
  if (!value) return '—'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return '—'
  return new Intl.DateTimeFormat('zh-CN', { year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false }).format(date)
}
export function normalizeSprints(items: unknown[]): RequirementSprint[] {
  return items.map(item => { const value = item as RequirementSprint & { sprint?: RequirementSprint }; return value.sprint || value }).filter(item => item && typeof item.name === 'string')
}
export function sprintSelectable(sprint: RequirementSprint): boolean { return ['规划中', '进行中'].includes(sprint.status) }

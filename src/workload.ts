export interface WorkloadMetrics { weight: number; requirementCount: number; shippedRequirementCount: number; defectCount: number; estimatedRoleCount: number; unestimatedRoleCount: number }
export interface WorkloadRole extends WorkloadMetrics { key: string }
export interface WorkloadPerson extends WorkloadMetrics { userId: string; name: string; active: boolean; departmentId: string; departmentName: string; roles: WorkloadRole[] }
export interface WorkloadDepartment extends WorkloadMetrics { id: string; name: string; memberCount: number }
export interface WorkloadReport {
  month: string; startDate: string; endDateExclusive: string; generatedAt: string; snapshot: boolean; precision: number
  scope: { type: 'organization'; tenantId: string; tenantName: string; projectCount: number }
  sprintCount: number; totals: WorkloadMetrics; people: WorkloadPerson[]; departments: WorkloadDepartment[]; roles: WorkloadRole[]
  unassignedWeight: number; unassignedRequirementCount: number; unassignedDefectCount: number; unestimatedRequirementCount: number; ambiguousSprintItemCount: number
}
export const workloadRoleNames: Record<string, string> = { frontend: '前端', backend: '后端', algorithm: '算法', ui: 'UI 设计', product: '产品', other: '其他职能' }
export type WorkloadDimension = 'people' | 'departments' | 'roles'
export type WorkloadSort = 'name' | 'weight' | 'requirementCount' | 'shippedRequirementCount' | 'defectCount'
export interface WorkloadRow extends WorkloadMetrics { id: string; name: string; departmentName?: string; departmentId?: string; active?: boolean; memberCount?: number; roleKeys?: string[] }
export function currentWorkloadMonth(date = new Date()): string { return `${date.getUTCFullYear()}-${String(date.getUTCMonth() + 1).padStart(2, '0')}` }
export function validWorkloadMonth(value: string): boolean { return /^\d{4}-(0[1-9]|1[0-2])$/.test(value) && Number(value.slice(0, 4)) >= 1 && Number(value.slice(0, 4)) <= 9998 }
export function moveWorkloadMonth(value: string, offset: number): string {
  if (!validWorkloadMonth(value) || !Number.isInteger(offset)) return value
  const [year, month] = value.split('-').map(Number), total = year * 12 + month - 1 + offset
  const result = `${String(Math.floor(total / 12)).padStart(4, '0')}-${String(total % 12 + 1).padStart(2, '0')}`
  return validWorkloadMonth(result) ? result : value
}
export function workloadRows(report: WorkloadReport, dimension: WorkloadDimension, options: { search?: string; department?: string; role?: string; sort?: WorkloadSort; descending?: boolean; locale?: string; translate?: (value: string) => string } = {}): WorkloadRow[] {
  const translate = options.translate || ((x: string) => x)
  let rows: WorkloadRow[]
  if (dimension === 'people') rows = report.people.filter(person => (options.department === undefined || options.department === '*' || person.departmentId === options.department) && (!options.role || person.roles.some(role => role.key === options.role))).map(person => ({ ...person, ...(options.role ? person.roles.find(role => role.key === options.role)! : {}), id: person.userId, roleKeys: person.roles.map(role => role.key) }))
  else if (dimension === 'departments') rows = report.departments.map(department => ({ ...department, name: department.name || translate('未分配部门') }))
  else rows = report.roles.map(role => ({ ...role, id: role.key, name: translate(workloadRoleNames[role.key] || role.key) }))
  const query = options.search?.trim().toLocaleLowerCase() || ''
  if (query) rows = rows.filter(row => [row.name, row.departmentName || '', ...(row.roleKeys || []).map(key => translate(workloadRoleNames[key] || key))].join(' ').toLocaleLowerCase().includes(query))
  const key = options.sort || 'weight', sign = options.descending === false ? 1 : -1
  return rows.sort((a, b) => { const primary = key === 'name' ? a.name.localeCompare(b.name, options.locale) : a[key] - b[key]; return primary ? primary * sign : a.id.localeCompare(b.id) })
}

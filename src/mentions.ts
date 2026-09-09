export type MentionMember = { id: string; name: string; email?: string; department?: string; departmentIds?: string[]; departmentNames?: string[]; projectRole?: string; role?: string; active?: boolean; isCurrent?: boolean }
export type MentionToken = { start: number; end: number; query: string }
export const mentionRoleLabels: Record<string, string> = { tenant_admin: '企业管理员', project_admin: '项目管理员', product: '产品', frontend: '前端工程师', backend: '后端工程师', algorithm: '算法工程师', ui: 'UI 设计师', frontend_lead: '前端组长', backend_lead: '后端组长', qa: '测试', viewer: '只读' }

export function normalizeMentionIds(value: unknown): string[] {
  return Array.isArray(value) ? [...new Set(value.filter((id): id is string => typeof id === 'string' && !!id.trim()))] : []
}

export function retainMentionIds(body: string, ids: unknown, members: MentionMember[], names: Record<string, string> = {}): string[] {
  if (!body.trim()) return []
  return normalizeMentionIds(ids).filter(id => {
    const name = names[id] || members.find(member => member.id === id)?.name
    // 目录临时不可用时保留历史 ID，不能仅因姓名查不到就把已保存的提及静默删除。
    return !name || containsMention(body, name)
  })
}

export function unavailableNewMentionIds(ids: unknown, members: MentionMember[], savedIds: unknown = []): string[] {
  const preserved = new Set(normalizeMentionIds(savedIds))
  return normalizeMentionIds(ids).filter(id => !preserved.has(id) && !members.some(member => member.id === id && member.active !== false))
}

const emailCharacter = /[a-zA-Z0-9._%+-]/
const tokenBoundary = /[\s@,，。！？!?;；:：()[\]{}]/

export function mentionAtCaret(body: string, caret: number): MentionToken | null {
  // 邮箱中的 @ 不能触发提及；只替换光标所在 token，保留两侧正文和其他已选择人员。
  const position = Math.max(0, Math.min(body.length, caret))
  const before = body.slice(0, position)
  const start = before.lastIndexOf('@')
  if (start < 0 || (start > 0 && emailCharacter.test(body[start - 1]!))) return null
  const query = before.slice(start + 1)
  if (tokenBoundary.test(query)) return null
  let end = position
  while (end < body.length && !tokenBoundary.test(body[end]!)) end++
  return { start, end, query }
}

export function filterMentionMembers(members: MentionMember[], query: string, translate: (source:string)=>string = source=>source): MentionMember[] {
  const value = query.trim().toLocaleLowerCase()
  return members.filter(member => {
    if (member.active === false) return false
    const role = member.projectRole || member.role || ''
    return !value || [member.name, member.email || '', member.department || '', ...(member.departmentNames || []), role, mentionRoleLabels[role] || '', translate(mentionRoleLabels[role] || role)].some(text => text.toLocaleLowerCase().includes(value))
  })
}

export function addActiveMemberIds(selected: unknown, candidates: MentionMember[]): string[] {
  return normalizeMentionIds([...normalizeMentionIds(selected), ...candidates.filter(member => member.active !== false).map(member => member.id)])
}

export function membersInDepartment(members: MentionMember[], departmentId = ''): MentionMember[] {
  return members.filter(member => member.active !== false && (!departmentId || member.departmentIds?.includes(departmentId)))
}
/** 角色/部门只限制新增候选，不自动删除需求上历史绑定的人员。 */
export function membersMatchingRoles(members: MentionMember[], roles?: readonly string[] | null): MentionMember[] {
  const allowed = new Set(normalizeMentionIds(roles))
  return members.filter(member => member.active !== false && (!allowed.size || allowed.has(member.projectRole || member.role || '')))
}
export function memberCandidates(members: MentionMember[], roles?: readonly string[] | null, departmentId = ''): MentionMember[] {
  return membersInDepartment(membersMatchingRoles(members, roles), departmentId)
}
const defaultFieldMemberRoles: Record<string, string[]> = {
  owner: ['product'], ownerUserIds: ['product'], product_owner: ['product'], product_manager: ['product'],
  frontend_leads: ['frontend_lead'], backend_leads: ['backend_lead'], testers: ['qa'], managers: ['tenant_admin', 'project_admin'],
  'role.frontend.userIds': ['frontend', 'frontend_lead'], 'role.backend.userIds': ['backend', 'backend_lead'],
  'role.algorithm.userIds': ['algorithm'], 'role.ui.userIds': ['ui'], 'role.product.userIds': ['product'],
}
export function fieldMemberRoles(definition: {key:string; memberRoles?:string[]; objectType?:string}, objectType = definition.objectType || 'requirement'): string[] {
  // 服务端显式空数组表示“不限角色”；仅缺少配置时套默认角色，不能凭翻译后字段名猜测。
  if (Array.isArray(definition.memberRoles)) return normalizeMentionIds(definition.memberRoles)
  return objectType === 'requirement' ? [...(defaultFieldMemberRoles[definition.key] || [])] : []
}
export function memberDepartments(members: MentionMember[], allDepartments: {id:string;name:string}[] = []): { id: string; name: string }[] {
  // 完整目录优先；成员携带的历史部门作为补充，不因本期无记录而隐藏部门。
  const departments = new Map<string, string>(allDepartments.filter(item=>item.id&&item.name).map(item=>[item.id,item.name]))
  for (const member of membersInDepartment(members)) {
    member.departmentIds?.forEach((id, index) => { if (id && !departments.has(id)) departments.set(id, member.departmentNames?.[index] || id) })
  }
  return [...departments].map(([id, name]) => ({id, name})).sort((a, b) => a.name.localeCompare(b.name) || a.id.localeCompare(b.id))
}

export function containsMention(body: string, name: string): boolean {
  const label = '@' + name
  let start = body.indexOf(label)
  while (start !== -1) {
    const end = start + label.length
    if ((start === 0 || !emailCharacter.test(body[start - 1]!)) && (end === body.length || tokenBoundary.test(body[end]!))) return true
    start = body.indexOf(label, start + label.length)
  }
  return false
}

export function insertMention(body: string, token: MentionToken, name: string): { body: string; caret: number } {
  const suffix = body.slice(token.end)
  const inserted = '@' + name + (suffix.startsWith(' ') ? '' : ' ')
  return { body: body.slice(0, token.start) + inserted + suffix, caret: token.start + name.length + 2 }
}

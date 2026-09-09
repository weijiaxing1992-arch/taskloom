import { normalizeMentionIds, type MentionMember } from './mentions'

export interface SprintParticipant { id: string; name: string; unassigned?: boolean }
export function sprintWorkPayload(form: any) {
  const { assigneeUserIds, assignee, status: _status, ...fields } = form
  const defect = form.objectType === 'defect'
  // New requirements always enter the project's configured initial state.
  return { ...fields, ...(defect ? { assignee: assignee || '', status: '新建' } : { assigneeUserIds: normalizeMentionIds(assigneeUserIds) }), severity: '一般', steps: form.description, expected: '按需求验收' }
}

export function sprintParticipants(item: any, members: MentionMember[]): SprintParticipant[] {
  if (item.objectType !== 'defect') {
    const ids = normalizeMentionIds(item.assigneeUserIds)
    if (ids.length) return ids.map(id => ({ id, name: members.find(member => member.id === id)?.name || item.assignees?.find((person: SprintParticipant) => person.id === id)?.name || id }))
  }
  // Defects still have exactly one owner. Legacy names are never split on
  // punctuation or merged with an arbitrary same-name directory member.
  if (item.assigneeUserId) return [{ id: item.assigneeUserId, name: members.find(member => member.id === item.assigneeUserId)?.name || item.assignee || item.assigneeUserId }]
  if (!item.assignee) return []
  const matches = members.filter(member => member.name === item.assignee)
  return [{ id: matches.length === 1 ? matches[0]!.id : `legacy:${item.assignee}`, name: item.assignee }]
}

export function sprintTeamParticipation(items: any[], members: MentionMember[], isDone: (item: any) => boolean) {
  const people = new Map<string, { id: string; name: string; unassigned: boolean; role: string; total: number; done: number; estimated: number; actual: number; progress: number }>()
  const seenItems = new Set<string>()
  for (const item of items) {
    const itemKey = `${item.objectType}:${item.id}`
    if (seenItems.has(itemKey)) continue
    seenItems.add(itemKey)
    const assigned = sprintParticipants(item, members)
    const participants: SprintParticipant[] = assigned.length ? assigned : [{ id: 'unassigned', name: '', unassigned: true }]
    for (const person of participants) {
      const member = members.find(member => member.id === person.id)
      const row = people.get(person.id) || { id: person.id, name: person.name, unassigned: !!person.unassigned, role: member?.projectRole || member?.role || item.discipline || '—', total: 0, done: 0, estimated: 0, actual: 0, progress: 0 }
      row.total++; row.done += isDone(item) ? 1 : 0
      // These describe participating work items, not a made-up allocation of
      // personal hours. They must not be summed into iteration/weight totals.
      row.estimated += Number(item.estimatedHours || 0); row.actual += Number(item.actualHours || 0)
      row.progress += Number(item.progress || 0)
      people.set(person.id, row)
    }
  }
  return [...people.values()].map(row => ({ ...row, progress: Math.round(row.progress / row.total) }))
}

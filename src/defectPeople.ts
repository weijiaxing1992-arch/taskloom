export interface DefectMember {
  id: string
  name: string
  active?: boolean
  projectRole?: string
  role?: string
  isCurrent?: boolean
}

export function qaMembers<T extends DefectMember>(members: T[]): T[] {
  return members.filter(member => member.active !== false && (member.projectRole || member.role) === 'qa')
}

/** Prefer the current tester; never arbitrarily assign one of several testers. */
export function defaultVerifier<T extends DefectMember>(members: T[], currentId?: string): T | undefined {
  const candidates = qaMembers(members)
  return candidates.find(member => currentId ? member.id === currentId : member.isCurrent) || (candidates.length === 1 ? candidates[0] : undefined)
}

export function defectPersonValue(members: DefectMember[], userId: string): { verifier: string; verifierUserId: string } {
  const member = members.find(item => item.active !== false && item.id === userId)
  return { verifier: member?.name || '', verifierUserId: member?.id || '' }
}

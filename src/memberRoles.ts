type ProjectRoleMember = { projectRoles?: string[]; roles?: string[]; projectRole?: string | null; role?: string | null }

/** Multi-role responses and older single-role servers share one interpretation. */
export function memberProjectRoles(member?: ProjectRoleMember | null): string[] {
  if (!member) return []
  const roles = member.projectRoles ?? member.roles
  return [...new Set((roles ?? [member.projectRole ?? member.role ?? '']).filter(role => typeof role === 'string' && !!role))]
}

export function memberHasProjectRole(member: ProjectRoleMember | null | undefined, allowedRoles: readonly string[]): boolean {
  return memberProjectRoles(member).some(role => allowedRoles.includes(role))
}
